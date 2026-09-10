// Package minify reduces the line count of IC10 assembly without changing its
// observable behaviour. It drops comments and blank lines, inlines
// alias/define symbols, converts labels to absolute line numbers (dropping the
// label lines), renumbers absolute jump targets, and can remove instructions
// that are provably unreachable.
package minify

import (
	"fmt"
	"strconv"
	"strings"

	"ic10go/internal/ic10asm"
)

// MaxLineLen is the IC10 per-line character limit.
const MaxLineLen = 90

// Options controls the transformations applied by Minify.
type Options struct {
	// KeepDefines keeps alias/define lines instead of inlining their symbols.
	KeepDefines bool
	// KeepLabels keeps label lines instead of rewriting jump targets to
	// absolute line numbers.
	KeepLabels bool
	// DeadCode removes instructions that cannot be reached from line 0.
	DeadCode bool
}

type kind int

const (
	kInstr kind = iota
	kLabel
	kDef
)

type item struct {
	kind kind
	orig int
	toks []string // instruction: [op, args...]; def: [alias, name, ...]
	name string
	tgt  int // for an instruction: index into toks of the jump target, or -1
}

// Minify rewrites IC10 source into an equivalent program with fewer lines.
func Minify(src string, opt Options) (string, error) {
	raw := strings.Split(src, "\n")
	items, labels, defs := parse(raw)
	if opt.KeepDefines {
		defs = nil
	}

	for i := range items {
		if items[i].kind == kInstr {
			items[i].toks = expand(items[i].toks, defs, labels)
			items[i].tgt = targetPos(items[i].toks)
		}
	}

	byLine := map[int]*item{}
	for i := range items {
		byLine[items[i].orig] = &items[i]
	}

	// Resolve every branch target to the original line it refers to. A branch
	// whose target cannot be resolved (dynamic "j ra" / "j reg", relative
	// branches) disables dead-code elimination, which is then unsafe.
	targetByLine := map[int]int{}
	dynamic := false
	for _, it := range items {
		if it.kind != kInstr {
			continue
		}
		t, branch, ok := branchTarget(&it, labels)
		if !branch {
			continue
		}
		if !ok {
			dynamic = true
			continue
		}
		targetByLine[it.orig] = t
	}

	reachable := map[int]bool{}
	if opt.DeadCode && !dynamic {
		reachable = reach(raw, byLine, targetByLine)
	}

	type survivor struct {
		it  *item
		out int
	}
	var survivors []survivor
	for i := range items {
		it := &items[i]
		switch it.kind {
		case kLabel:
			if !opt.KeepLabels {
				continue
			}
		case kDef:
			if !opt.KeepDefines {
				continue
			}
		case kInstr:
			if opt.DeadCode && !dynamic && !reachable[it.orig] {
				continue
			}
		}
		survivors = append(survivors, survivor{it: it, out: len(survivors)})
	}

	mapTarget := func(orig int) int {
		for _, s := range survivors {
			if s.it.kind == kInstr && s.it.orig >= orig {
				return s.out
			}
		}
		return len(survivors)
	}

	var out []string
	for _, s := range survivors {
		it := s.it
		switch it.kind {
		case kLabel:
			out = append(out, it.name+":")
		case kDef:
			out = append(out, strings.Join(it.toks, " "))
		case kInstr:
			toks := it.toks
			if it.tgt >= 0 {
				_, isLabel := labels[it.toks[it.tgt]]
				if !(isLabel && opt.KeepLabels) {
					if t, ok := targetByLine[it.orig]; ok {
						toks = append([]string(nil), it.toks...)
						toks[it.tgt] = strconv.Itoa(mapTarget(t))
					}
				}
			}
			out = append(out, strings.Join(toks, " "))
		}
	}

	for i, ln := range out {
		if len(ln) > MaxLineLen {
			return "", fmt.Errorf("line %d is %d characters, exceeding the %d character limit (try --keep-defines)",
				i, len(ln), MaxLineLen)
		}
	}
	if len(out) == 0 {
		return "", nil
	}
	return strings.Join(out, "\n") + "\n", nil
}

// parse splits source into ordered items, recording label positions and
// alias/define expansions.
func parse(lines []string) (items []item, labels map[string]int, defs map[string][]string) {
	labels = map[string]int{}
	defs = map[string][]string{}
	for i, raw := range lines {
		code := strings.TrimSpace(ic10asm.StripComment(raw))
		if code == "" {
			continue
		}
		if ic10asm.IsLabel(code) {
			name := strings.TrimSuffix(code, ":")
			labels[name] = i
			items = append(items, item{kind: kLabel, orig: i, name: name, tgt: -1})
			continue
		}
		toks := ic10asm.Tokenize(code)
		if len(toks) == 0 {
			continue
		}
		op := strings.ToLower(toks[0])
		if op == "alias" || op == "define" {
			if len(toks) < 2 {
				continue
			}
			if len(toks) >= 3 {
				defs[toks[1]] = toks[2:]
			}
			items = append(items, item{kind: kDef, orig: i, name: toks[1], toks: toks, tgt: -1})
			continue
		}
		items = append(items, item{kind: kInstr, orig: i, toks: toks, tgt: -1})
	}
	return items, labels, defs
}

// expand replaces alias/define symbols with their definitions. A name that is
// also a label is left untouched so that branch targets keep resolving.
func expand(toks []string, defs map[string][]string, labels map[string]int) []string {
	if defs == nil {
		return toks
	}
	out := make([]string, 0, len(toks))
	for _, t := range toks {
		out = append(out, expandTok(t, defs, labels, 0)...)
	}
	return out
}

func expandTok(tok string, defs map[string][]string, labels map[string]int, depth int) []string {
	if depth > 10 {
		return []string{tok}
	}
	if _, isLabel := labels[tok]; isLabel {
		return []string{tok}
	}
	exp, ok := defs[tok]
	if !ok {
		return []string{tok}
	}
	var out []string
	for _, e := range exp {
		out = append(out, expandTok(e, defs, labels, depth+1)...)
	}
	return out
}

// targetPos returns the index into toks of the jump target, or -1 if the
// instruction is not an absolute branch.
func targetPos(toks []string) int {
	if len(toks) == 0 {
		return -1
	}
	op := strings.ToLower(toks[0])
	switch op {
	case "j", "jal":
		if len(toks) >= 2 {
			return 1
		}
		return -1
	}
	cond, relative, _, ok := ic10asm.BranchInfo(op)
	if !ok || relative {
		return -1
	}
	idx := 1 + ic10asm.TargetIndex(cond)
	if idx < len(toks) {
		return idx
	}
	return -1
}

// branchTarget reports the original line an instruction branches to.
func branchTarget(it *item, labels map[string]int) (int, bool, bool) {
	if it.tgt < 0 {
		op := ""
		if len(it.toks) > 0 {
			op = strings.ToLower(it.toks[0])
		}
		if _, relative, _, ok := ic10asm.BranchInfo(op); ok && relative {
			return 0, true, false
		}
		return 0, false, false
	}
	tok := it.toks[it.tgt]
	if l, ok := labels[tok]; ok {
		return l, true, true
	}
	if n, err := strconv.Atoi(tok); err == nil {
		return n, true, true
	}
	return 0, true, false
}

// reach returns the set of original lines reachable from line 0.
func reach(raw []string, byLine map[int]*item, targetByLine map[int]int) map[int]bool {
	n := len(raw)
	seen := map[int]bool{}
	stack := []int{0}
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if i < 0 || i >= n || seen[i] {
			continue
		}
		seen[i] = true
		it := byLine[i]
		if it == nil || it.kind != kInstr {
			stack = append(stack, i+1)
			continue
		}
		op := strings.ToLower(it.toks[0])
		switch {
		case op == "j" || op == "jal":
			stack = append(stack, targetByLine[i])
		case it.tgt >= 0:
			stack = append(stack, i+1, targetByLine[i])
		default:
			stack = append(stack, i+1)
		}
	}
	return seen
}
