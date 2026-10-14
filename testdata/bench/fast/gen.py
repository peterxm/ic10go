#!/usr/bin/env python3
# 再生成本目录的状态机基准语料（评估见 docs/fast-statemachine.md）。
#
#   var<N>_2.icg   N 个状态、每状态 2 条语句（1 设备写 + 1 转移），tag=局部变量
#   db<N>_2.icg    同上，tag=db.stack[0]（跨 tick 状态，气闸/臂的真实写法）
#   var<N>_1.icg   N 个状态、每状态 1 条语句（仅转移）——唯一能命中 --jump-table 的形状
#   ord_asc<N>.icg / ord_desc<N>.icg   同一台机器，case 源码顺序不同（升/降 handler 体积）
#   tree<N>.ic     手写 IC10：同一状态机的二分决策树布局（--fast 的期望形状参照）
#
# 用法：python3 gen.py            # 重新生成全部文件
#       对比命令见 docs/fast-statemachine.md 附录 A。

N_DEVS = 5  # IC10 只有 d0..d5


def body_db(k):  # db/N_2 的 case 体
    return [f"        db.stack[0] = {(k + 1) % N}", f"        d0.On = {k}"]


def gen_var(n, stmts, fname):
    global N
    N = n
    s = ["func main() {", "    st := 0", "    for {", "        yield()", "        switch st {"]
    for k in range(n):
        nxt = (k + 1) % n
        s.append(f"    case {k}:")
        if stmts == 1:
            s.append(f"        st = {nxt}")
        else:
            s.append(f"        d0.On = {k}")
            s.append(f"        st = {nxt}")
    s += ["    default:", "        st = 0", "    }", "    }", "}"]
    open(fname, "w").write("\n".join(s) + "\n")


def gen_db(n, fname):
    global N
    N = n
    s = [
        "func main() {",
        "    for {",
        "        yield()",
        "        step()",
        "    }",
        "}",
        "",
        "func step() {",
        "    switch db.stack[0] {",
    ]
    for k in range(n):
        s.append(f"    case {k}:")
        s += body_db(k)
    s += ["    default:", "        db.stack[0] = 0", "    }", "}"]
    open(fname, "w").write("\n".join(s) + "\n")


def gen_ord(n, fname, descending):
    global N
    N = n

    def w(k):  # handler 体积（设备写条数）
        return (k % N_DEVS) + 1

    order = list(range(n))
    if descending:  # 大 handler 靠前：最坏路径更短（minimax 排序）
        order.sort(key=lambda k: -w(k))
    s = ["func main() {", "    st := 0", "    for {", "        yield()", "        switch st {"]
    for k in order:
        nxt = (k + 1) % n
        s.append(f"    case {k}:")
        for j in range(w(k)):
            s.append(f"        d{j}.Setting = {j + k}")
        s.append(f"        st = {nxt}")
    s += ["    default:", "        st = 0", "    }", "    }", "}"]
    open(fname, "w").write("\n".join(s) + "\n")


def gen_tree(n, fname):
    """手写决策树版：body(k) = d0.On=k; st=k+1; 回跳 yield。

    语义与 var<N>_2 一致，但派发是二分树（节点 1 行 bge，叶子 3 行）。
    叶子**不带等值复核**：只覆盖整数 tag（0..N-1）；自动生成的树必须保留
    叶子的 eq 复核或加 floor+范围守卫，见 docs/fast-statemachine.md §4。
    """
    global N
    N = n
    seq = []

    def walk(lo, hi):
        if hi - lo == 1:
            seq.append(("leaf", lo))
            return
        mid = (lo + hi) // 2
        seq.append(("node", lo, mid, hi))
        walk(lo, mid)
        walk(mid, hi)

    walk(0, N)
    num, ln = {}, 3  # 文本行号（1 基；1=move, 2=yield）
    for it in seq:
        key = ("leaf", it[1]) if it[0] == "leaf" else ("node",) + it[1:]
        num[key] = ln
        ln += 3 if it[0] == "leaf" else 1
    out = ["move r0 0", "yield"]
    for it in seq:
        if it[0] == "node":
            _, lo, mid, hi = it
            right = ("leaf", mid) if hi - mid == 1 else ("node", mid, (mid + hi) // 2, hi)
            # >= mid 跳到右子树，< mid 落进左子树（左子树紧跟其后）
            out.append(f"bge r0 {mid} {num[right] - 1}")  # IC10 绝对跳转 = 0 基行号
        else:
            k = it[1]
            out += [f"s d0 On {k}", f"move r0 {(k + 1) % N}", "j 1"]
    open(fname, "w").write("\n".join(out) + "\n")


for n in (8, 12):
    gen_var(n, 2, f"var{n}_2.icg")
    gen_var(n, 1, f"var{n}_1.icg")
    gen_db(n, f"db{n}_2.icg")
    gen_ord(n, f"ord_asc{n}.icg", False)
    gen_ord(n, f"ord_desc{n}.icg", True)
    gen_tree(n, f"tree{n}.ic")
print("ok")
