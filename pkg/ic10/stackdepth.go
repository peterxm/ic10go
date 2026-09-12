package ic10

import (
	"fmt"

	"ic10go/internal/ir"
	"ic10go/internal/lower"
)

// MaxStackDepth compiles the source and returns the maximum sp depth reached by
// push/pop, and whether it is unbounded (a loop grows the stack without a
// matching pop). Each push writes slot sp then increments it, so the deepest
// slot written is depth-1: a data segment starting at base is safe when
// depth <= base.
func MaxStackDepth(name string, src []byte, opts Options) (depth int, unbounded bool, err error) {
	info, diags := parseAndCheck(name, src, opts)
	if info == nil {
		if diags.HasErrors() {
			return 0, false, fmt.Errorf("compile failed")
		}
		return 0, false, fmt.Errorf("no IR produced")
	}
	fn := lowerAndOptimize(info, opts, lower.PlanOutlines(info), diags)
	if fn == nil {
		return 0, false, fmt.Errorf("compile failed")
	}
	return stackDepth(fn), stackDepthUnbounded(fn), nil
}

// stackDepth returns the maximum push depth over the CFG. Positive cycles make
// the result a large but finite number; use stackDepthUnbounded to detect them.
func stackDepth(fn *ir.Function) int {
	depth, _ := analyzeDepth(fn)
	return depth
}

// stackDepthUnbounded reports whether a positive push cycle exists.
func stackDepthUnbounded(fn *ir.Function) bool {
	_, unbounded := analyzeDepth(fn)
	return unbounded
}

func analyzeDepth(fn *ir.Function) (int, bool) {
	fn.BuildCFG()
	net := map[*ir.Block]int{}
	peak := map[*ir.Block]int{}
	for _, b := range fn.Blocks {
		d, p := 0, 0
		for _, ins := range b.Instrs {
			bi, ok := ins.(*ir.Builtin)
			if !ok {
				continue
			}
			switch bi.Name {
			case "push":
				d++
				if d > p {
					p = d
				}
			case "pop":
				d--
			}
		}
		net[b], peak[b] = d, p
	}

	const maxIter = 2000
	depthIn := map[*ir.Block]int{}
	unbounded := false
	for iter := 0; iter < maxIter; iter++ {
		changed := false
		for _, b := range fn.Blocks {
			best := 0
			has := false
			for _, p := range b.Preds {
				v := depthIn[p] + net[p]
				if !has || v > best {
					best, has = v, true
				}
			}
			if b == fn.Entry {
				best = 0
			}
			if best > depthIn[b] {
				depthIn[b] = best
				changed = true
			}
		}
		if !changed {
			break
		}
		if iter == maxIter-1 {
			unbounded = true
		}
	}

	maxDepth := 0
	for _, b := range fn.Blocks {
		if d := depthIn[b] + peak[b]; d > maxDepth {
			maxDepth = d
		}
	}
	return maxDepth, unbounded
}
