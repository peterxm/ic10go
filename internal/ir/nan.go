package ir

import "math"

// nanInfo carries the "provably not NaN" analysis used to decide when an
// ordering comparison (`< <= > >=`) may be negated. It has two parts:
//
//   - global: registers that never hold NaN on any path (from their defining
//     instructions), and
//   - guard:  per block, registers shown to be numbers on entry by an `isNaN`
//     test — the else edge of a `Br{Cond: NaN, A: r}`, i.e. the path taken
//     when `if isNaN(r) { ... }` does not fire.
//
// The property is "not NaN", not "finite": `inf - inf` is NaN, so arithmetic on
// values that may be infinite is not fully covered. A single-device read
// returns a number or faults the chip; a batched read can be NaN (no match).
type NaNInfo struct {
	global map[*Reg]bool
	guard  map[*Block]map[*Reg]bool
}

func AnalyzeNaN(fn *Function) *NaNInfo {
	return &NaNInfo{global: nanFreeRegs(fn), guard: guardedRegs(fn)}
}

func (ni *NaNInfo) RegFree(b *Block, r *Reg) bool {
	if r == nil {
		return true
	}
	return ni.global[r] || ni.guard[b][r]
}

func (ni *NaNInfo) ValFree(b *Block, v Value) bool {
	switch x := v.(type) {
	case nil:
		return true
	case *Reg:
		return ni.RegFree(b, x)
	case *Const:
		return !isNaNConst(x)
	}
	return true
}

// branchInvertible reports whether br's condition can be negated exactly:
// equality and zero tests always can, an ordering test only when both operands
// are provably numbers, and a NaN test never (IC10 has no branch-if-not-NaN).
func (ni *NaNInfo) BranchInvertible(b *Block, br *Br) bool {
	if br.Cond.Invertible() {
		return true
	}
	if br.Cond == NaN {
		return false
	}
	return ni.ValFree(b, br.A) && ni.ValFree(b, br.B)
}

// nanFreeRegs returns the set of registers that never hold NaN on any path.
// Registers are SSA-like (a mutable variable has several definitions), so a
// register is free when every definition is free; the fixpoint starts
// optimistic and shrinks, which keeps loop counters free.
func nanFreeRegs(fn *Function) map[*Reg]bool {
	defs := map[*Reg][]Instr{}
	regs := map[*Reg]bool{}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			use, def := DefUse(ins)
			for _, r := range use {
				regs[r] = true
			}
			for _, r := range def {
				regs[r] = true
				defs[r] = append(defs[r], ins)
			}
		}
		for _, r := range TermUses(b.Term) {
			regs[r] = true
		}
	}
	free := map[*Reg]bool{}
	for r := range regs {
		free[r] = true
	}
	freeVal := func(v Value) bool { return nanFreeValue(v, free) }
	for changed := true; changed; {
		changed = false
		for r := range regs {
			if !free[r] {
				continue
			}
			ok := len(defs[r]) > 0
			for _, ins := range defs[r] {
				if !nanFreeInstr(ins, freeVal) {
					ok = false
					break
				}
			}
			if !ok {
				free[r] = false
				changed = true
			}
		}
	}
	return free
}

// guardedRegs returns, per block, the registers proven to be numbers on entry.
// It is a must-analysis: a register is guarded only if every path into the
// block narrowed it with an `isNaN` test.
func guardedRegs(fn *Function) map[*Block]map[*Reg]bool {
	fn.BuildCFG()
	regs := map[*Reg]bool{}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			use, def := DefUse(ins)
			for _, r := range use {
				regs[r] = true
			}
			for _, r := range def {
				regs[r] = true
			}
		}
	}
	in := map[*Block]map[*Reg]bool{}
	for _, b := range fn.Blocks {
		if b == fn.Entry {
			in[b] = map[*Reg]bool{}
			continue
		}
		m := make(map[*Reg]bool, len(regs))
		for r := range regs {
			m[r] = true
		}
		in[b] = m
	}
	for changed := true; changed; {
		changed = false
		for _, b := range fn.Blocks {
			if b == fn.Entry {
				continue
			}
			var next map[*Reg]bool
			for _, p := range b.Preds {
				c := make(map[*Reg]bool, len(in[p])+1)
				for r := range in[p] {
					c[r] = true
				}
				if r, ok := nanGuardReg(p, b); ok {
					c[r] = true
				}
				if next == nil {
					next = c
					continue
				}
				for r := range next {
					if !c[r] {
						delete(next, r)
					}
				}
			}
			if next == nil {
				next = map[*Reg]bool{}
			}
			if !sameSet(next, in[b]) {
				in[b] = next
				changed = true
			}
		}
	}
	return in
}

// nanGuardReg returns the register that p's terminator proves to be a number
// when control reaches to: the else edge of a NaN branch is the `!isNaN` path.
func nanGuardReg(p, to *Block) (*Reg, bool) {
	br, ok := p.Term.(*Br)
	if !ok || br.Cond != NaN || br.Else != to {
		return nil, false
	}
	r, ok := br.A.(*Reg)
	return r, ok
}

func isNaNConst(c *Const) bool {
	if c.Raw != "" {
		return false
	}
	if c.Special != "" {
		return c.Special == "nan"
	}
	return math.IsNaN(c.V)
}

// nanFreeInstr reports whether the value defined by ins is provably not NaN,
// assuming freeVal for its operands.
func nanFreeInstr(ins Instr, freeVal func(Value) bool) bool {
	switch v := ins.(type) {
	case *Assign:
		return freeVal(v.Src)
	case *Bin:
		// 0/0 (Div) and x%0 (Mod) are NaN; the rest preserve "not NaN".
		if v.Op == Div || v.Op == Mod {
			return false
		}
		return freeVal(v.A) && freeVal(v.B)
	case *Un:
		return freeVal(v.A)
	case *Cmp:
		return true // a comparison yields 0 or 1
	case *Select:
		return freeVal(v.Then) && freeVal(v.Else)
	case *Builtin:
		return nanFreeBuiltin(v, freeVal)
	case *Load, *LoadSlot, *LoadDyn:
		// A single-device read returns a logic value (a number) or faults the
		// chip; only batched reads yield NaN when nothing matches (verified in
		// game). `LoadDyn` covers reagent (`lr`) reads too.
		return true
	}
	// Batched reads, indirect and spill reads, and calls can bring in anything.
	return false
}

// nanUnsafeBuiltins either yield NaN for some inputs (sqrt/log/asin/acos/
// atan2/pow) or read a value the compiler does not model (the stack, an
// indirect register).
var nanUnsafeBuiltins = map[string]bool{
	"sqrt": true, "log": true, "asin": true, "acos": true,
	"atan2": true, "pow": true, "rand": true,
	"pop": true, "peek": true, "get": true, "getd": true, "ireg": true,
	"readByIdSlot": true,
}

func nanFreeBuiltin(v *Builtin, freeVal func(Value) bool) bool {
	switch v.Name {
	case "isNaN", "isNotNaN", "snan", "snanz":
		return true // 0 or 1
	}
	if nanUnsafeBuiltins[v.Name] {
		return false
	}
	for _, a := range v.Args {
		if !freeVal(a) {
			return false
		}
	}
	return true
}

// nanFreeValue reports whether v provably never holds NaN.
func nanFreeValue(v Value, nf map[*Reg]bool) bool {
	switch x := v.(type) {
	case nil:
		return true
	case *Reg:
		return nf[x]
	case *Const:
		return !isNaNConst(x)
	}
	return true
}
