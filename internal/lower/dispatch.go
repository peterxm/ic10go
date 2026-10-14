// Dispatch transforms for switch statements under --fast: case reordering by
// body cost and balanced compare trees (docs/fast-statemachine.md P3).
package lower

import (
	"slices"
	"sort"

	"ic10go/internal/ast"
	"ic10go/internal/ir"
	"ic10go/internal/sema"
)

// treeDispatchMin is the smallest constant-case switch worth a compare tree:
// below it log2 n + leaf recheck is not meaningfully shallower than the
// minimax-sorted chain.
const treeDispatchMin = 5

type caseConstant struct {
	v  float64
	ci int // index into s.Cases
}

// constCaseValues collects the case values of a switch whose dispatch order is
// free. It reports false unless s has a tag, every non-default case is a plain
// `case v:` with a compile-time constant (no ranges) and no value repeats in
// two different cases: overlapping or first-match-ordered arms would change
// which body runs if the tests are reordered.
func (l *lowerer) constCaseValues(s *ast.SwitchStmt) ([]caseConstant, bool) {
	if s.Tag == nil {
		return nil, false
	}
	var vals []caseConstant
	seen := map[float64]int{}
	for i, c := range s.Cases {
		if c.Default {
			continue
		}
		for _, ce := range c.Exprs {
			if _, ok := ce.(*ast.RangeExpr); ok {
				return nil, false
			}
			v, ok := sema.Eval(ce, l.info.Consts)
			if !ok {
				return nil, false
			}
			if prev, dup := seen[v]; dup && prev != i {
				return nil, false
			}
			seen[v] = i
			vals = append(vals, caseConstant{v: v, ci: i})
		}
	}
	if len(vals) == 0 {
		return nil, false
	}
	return vals, true
}

// lowerSwitchBodies lowers every case body into its reserved block and returns
// a per-case instruction count (blocks created while lowering a body count
// toward it): a cheap proxy for the per-tick cost of taking that arm.
func (l *lowerer) lowerSwitchBodies(s *ast.SwitchStmt, bodyBlocks []*ir.Block, endB *ir.Block) []int {
	costs := make([]int, len(s.Cases))
	fn := l.b.Fn()
	for i := range s.Cases {
		bb := bodyBlocks[i]
		before := len(fn.Blocks)
		l.b.SetBlock(bb)
		l.lowerStmts(s.Cases[i].Body)
		if l.b.Cur().Term == nil {
			l.b.SetTerm(&ir.Jmp{Target: endB})
		}
		cost := len(bb.Instrs)
		if bb.Term != nil {
			cost++
		}
		for _, b := range fn.Blocks[before:] {
			if b == bb {
				continue
			}
			cost += len(b.Instrs)
			if b.Term != nil {
				cost++
			}
		}
		costs[i] = cost
	}
	return costs
}

// lowerSwitchSortedChain emits the constant-case dispatch tests in descending
// body-cost order (stable within ties), the minimax ordering for the worst
// path between two yields. Bodies are already lowered.
func (l *lowerer) lowerSwitchSortedChain(tag ir.Value, s *ast.SwitchStmt, bodyBlocks []*ir.Block, costs []int, defaultIdx int, endB *ir.Block) {
	var order []int
	for i, c := range s.Cases {
		if !c.Default {
			order = append(order, i)
		}
	}
	natural := order
	order = append([]int(nil), natural...)
	sort.SliceStable(order, func(a, b int) bool {
		return costs[order[a]] > costs[order[b]]
	})
	if !slices.Equal(order, natural) {
		l.b.Fn().DispatchSorted++
	}
	l.emitSwitchChain(tag, s, bodyBlocks, order, defaultIdx, endB)
}

// emitSwitchChain tests the given case order against tag, one fused
// Cmp+Br per case value, falling through to the default arm or endB.
func (l *lowerer) emitSwitchChain(tag ir.Value, s *ast.SwitchStmt, bodyBlocks []*ir.Block, order []int, defaultIdx int, endB *ir.Block) {
	for _, ci := range order {
		for _, ce := range s.Cases[ci].Exprs {
			l.ensure()
			next := l.newBlock()
			v := l.lowerExpr(ce)
			cmp := l.b.NewReg("swcmp")
			l.b.Emit(&ir.Cmp{Cond: ir.Eq, Dst: cmp, A: tag, B: v})
			l.b.SetTerm(&ir.Br{Cond: ir.NonZero, A: cmp, Then: bodyBlocks[ci], Else: next})
			l.b.SetBlock(next)
		}
	}
	l.ensure()
	if defaultIdx >= 0 {
		l.b.SetTerm(&ir.Jmp{Target: bodyBlocks[defaultIdx]})
	} else {
		l.b.SetTerm(&ir.Jmp{Target: endB})
	}
}

// lowerSwitchTree turns a distinct-integer constant switch into a balanced
// binary search over the sorted case values. Interior nodes are ordering
// tests (`tag < pivot`), leaves keep the equality re-check, and any value
// that fails its leaf test (fractional, NaN, outside the range) falls to the
// default arm or endB - the same result the linear chain gives for every tag
// value, at ~ceil(log2 n)+1 tests on the worst path instead of n. The root
// test is written into cur (the block the switch sits in), so no entry jump
// is spent and the dispatch stays reachable.
func (l *lowerer) lowerSwitchTree(cur *ir.Block, tag ir.Value, vals []caseConstant, bodyBlocks []*ir.Block, defaultIdx int, endB *ir.Block) {
	sort.SliceStable(vals, func(i, j int) bool { return vals[i].v < vals[j].v })
	miss := endB
	if defaultIdx >= 0 {
		miss = bodyBlocks[defaultIdx]
	}
	var build func(lo, hi int, blk *ir.Block)
	build = func(lo, hi int, blk *ir.Block) {
		l.b.SetBlock(blk)
		if hi-lo == 1 {
			cmp := l.b.NewReg("swtst")
			l.b.Emit(&ir.Cmp{Cond: ir.Eq, Dst: cmp, A: tag, B: &ir.Const{V: vals[lo].v}})
			l.b.SetTerm(&ir.Br{Cond: ir.NonZero, A: cmp, Then: bodyBlocks[vals[lo].ci], Else: miss})
			return
		}
		mid := (lo + hi) / 2
		left, right := l.newBlock(), l.newBlock()
		lt := l.b.NewReg("swtlt")
		l.b.Emit(&ir.Cmp{Cond: ir.Lt, Dst: lt, A: tag, B: &ir.Const{V: vals[mid].v}})
		l.b.SetTerm(&ir.Br{Cond: ir.NonZero, A: lt, Then: left, Else: right})
		build(lo, mid, left)
		build(mid, hi, right)
	}
	build(0, len(vals), cur)
}
