package ic10

import (
	"fmt"

	"ic10go/internal/ir"
	"ic10go/internal/lower"
)

// MaxStackDepth compiles the source and returns the highest user stack slot
// touched, as slot+1: the push/pop depth, or the highest explicit
// db.stack[addr]/poke address. Each push writes slot sp then increments it, so
// the deepest slot written is depth-1: a data segment starting at base is safe
// when depth <= base. unbounded reports a loop that grows the stack without a
// matching pop.
func MaxStackDepth(name string, src []byte, opts Options) (depth int, unbounded bool, err error) {
	info, diags, private := parseAndCheck(name, src, opts)
	if info == nil {
		if diags.HasErrors() {
			return 0, false, fmt.Errorf("compile failed")
		}
		return 0, false, fmt.Errorf("no IR produced")
	}
	opts.PrivateStack = private
	noCheck, noOutline, noOpt := envSwitches()
	fn := lowerAndOptimize(info, opts, lower.PlanOutlines(info, noOutline), noCheck, noOpt, diags)
	if fn == nil {
		return 0, false, fmt.Errorf("compile failed")
	}
	d, unb := analyzeDepth(fn)
	if fn.UserStackManual > d {
		d = fn.UserStackManual
	}
	return d, unb, nil
}

// analyzeDepth returns the maximum push depth over the CFG and whether a
// positive push cycle makes it unbounded. Positive cycles yield a large but
// finite depth; the bool is the authoritative signal.
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
