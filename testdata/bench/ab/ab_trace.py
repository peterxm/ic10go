#!/usr/bin/env python3
"""Real-hardware write-effect A/B via the in-game testbench `trace` command.

Decompiles the original IC10 with `ic10c`, recompiles it, then runs both on the
selected chip one instruction at a time (Execute(1)) and compares the decoded
store-effect sequences (device id + logic + value). The effect is independent
of register allocation, so an optimized recompilation must produce the same
sequence.

Usage:
    python3 ab_trace.py <chip-index-or-name> <original.ic10> [--n 4000] [--ic10c ic10c]

Run the original twice as a determinism control. See README.md for the mod
build/restart steps this depends on.
"""
import argparse, json, os, re, socket, subprocess, sys, tempfile, zlib

ADDR = ("127.0.0.1", 7800)
HERE = os.path.dirname(os.path.abspath(__file__))
REG = re.compile(r'^r(\d+)$')

try:
    ENUMS = json.load(open(os.path.join(HERE, "enums.json")))
except Exception:
    ENUMS = {}


class Bench:
    def __init__(self):
        self.s = socket.create_connection(ADDR, timeout=180)
        self.f = self.s.makefile("rwb", buffering=0)
        self.f.readline()
        self.n = 0

    def call(self, cmd, args=None):
        self.n += 1
        self.f.write((json.dumps({"id": self.n, "cmd": cmd, "args": args or {}}) + "\n").encode())
        while True:
            line = self.f.readline()
            if not line:
                raise RuntimeError("testbench closed the connection")
            o = json.loads(line)
            if o.get("id") == self.n:
                if not o.get("ok"):
                    raise RuntimeError("%s: %s" % (cmd, o.get("error")))
                return o["result"]


def symbols(src):
    al, de = {}, {}
    for line in src.replace("\r", "").split("\n"):
        p = line.strip().split()
        if len(p) >= 3 and p[0] == "alias":
            al[p[1]] = p[2]
        elif len(p) >= 3 and p[0] == "define":
            de[p[1]] = p[2]
    return al, de


def val(tok, al, de, regs):
    t = tok.strip()
    for _ in range(6):
        if t in al:
            t = al[t]
        elif t in de:
            t = de[t]
        else:
            break
    m = REG.match(t)
    if m:
        i = int(m.group(1))
        return regs[i] if i < len(regs) else None
    if t == "ra":
        return regs[16] if len(regs) > 16 else None
    if t == "sp":
        return regs[17] if len(regs) > 17 else None
    mh = re.match(r'HASH\("([^"]*)"\)', t)
    if mh:
        h = zlib.crc32(mh.group(1).encode()) & 0xffffffff
        return h - (1 << 32) if h >= (1 << 31) else h
    if t in ENUMS:
        return ENUMS[t]
    try:
        return float(t)
    except ValueError:
        return ("LIT", t)


def effect(hit, al, de):
    regs = hit["regs"]
    parts = hit["text"].split()
    op = parts[0]
    # `s <reg> ...` addresses a device by ReferenceId exactly like `sd`.
    if op == "s":
        op = "sd"
    if op == "clr":
        op = "clrd"
    a = parts[1:]
    dev = lambda t: val(t, al, de, regs)
    if op == "sd" and len(a) >= 3:
        return (op, dev(a[0]), a[1], val(a[2], al, de, regs))
    if op == "ss" and len(a) >= 4:
        return (op, dev(a[0]), a[2], a[1], val(a[3], al, de, regs))
    if op in ("put", "putd") and len(a) >= 3:
        return (op, dev(a[0]), val(a[1], al, de, regs), val(a[2], al, de, regs))
    if op == "clrd" and len(a) >= 1:
        return (op, dev(a[0]))
    if op in ("sb", "sbn", "sbs") and len(a) >= 2:
        return (op, dev(a[0]), val(a[1], al, de, regs))
    return (op,) + tuple(a)


def recompile(ic10c, source):
    icg = tempfile.NamedTemporaryFile(suffix=".icg", delete=False).name
    subprocess.run([ic10c, "decompile", source, "-o", icg], check=True,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    out = subprocess.run([ic10c, "build", icg], check=True, capture_output=True, text=True).stdout
    os.unlink(icg)
    return out


def run_once(b, chip, code, n):
    al, de = symbols(code)
    b.call("push", {"chip": chip, "code": code, "reset": True})
    hits = b.call("trace", {"chip": chip, "n": n})["hits"]
    return [effect(h, al, de) for h in hits]


def fetch_program(b, chip):
    return b.call("program", {"chip": chip})["code"]


def compare(label, A, B):
    m = min(len(A), len(B))
    bad = [(i, A[i], B[i]) for i in range(m) if A[i] != B[i]]
    for i, x, y in bad[:5]:
        print("  %s #%d:\n    %s\n    %s" % (label, i, x, y))
    print("%s: %d vs %d effects, compared %d, %d mismatches" % (label, len(A), len(B), m, len(bad)))
    return len(bad)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("chip")
    ap.add_argument("original", nargs="?", help="IC10 file; omit with --from-chip")
    ap.add_argument("--from-chip", action="store_true", help="use the chip's current source")
    ap.add_argument("--n", type=int, default=4000)
    ap.add_argument("--ic10c", default="ic10c")
    ap.add_argument("--addr", default="127.0.0.1:7800")
    args = ap.parse_args()

    global ADDR
    host, _, port = args.addr.partition(":")
    ADDR = (host, int(port or 7800))

    target = {"index": int(args.chip)} if args.chip.isdigit() else {"name": args.chip}
    b = Bench()
    b.call("pause", {"on": True})
    b.call("chip.select", {"target": target})
    srcpath = args.original
    try:
        if args.from_chip or not args.original:
            orig = fetch_program(b, target)
            srcpath = tempfile.NamedTemporaryFile(suffix=".ic10", delete=False).name
            open(srcpath, "w").write(orig)
        else:
            orig = open(srcpath).read()
        recomp = recompile(args.ic10c, srcpath)

        O1 = run_once(b, target, orig, args.n)
        O2 = run_once(b, target, orig, args.n)
        R = run_once(b, target, recomp, args.n)
        print("store effects: original=%d recompiled=%d" % (len(O1), len(R)))
        if compare("O1~O2 (control)", O1, O2) != 0:
            print("!! the original is not deterministic in this window; fix that first")
        compare("O1~R  (A/B)", O1, R)
    finally:
        b.call("push", {"chip": target, "code": orig, "reset": True})
        b.call("pause", {"on": False})
        print("restored the original on the chip")


if __name__ == "__main__":
    main()
