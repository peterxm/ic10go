package codegen

import (
	"math"

	"ic10go/internal/ir"
)

// nanFreeRegs returns the set of registers in fn that provably never hold NaN.
//
// It backs the decision to negate an ordering comparison (`< <= > >=`): IC10's
// `bge` is not `!(blt)` when an operand is NaN, so an ordering condition may
// only be inverted when both operands are known to be numbers. Registers are
// defined once (SSA), so the property is a recursive computation over each
// register's defining instruction; a register with several definitions or none
// is treated as unknown.
//
// The property is "not NaN", not "finite": `inf - inf` is NaN, so arithmetic on
// values that may be infinite is not fully covered. Device reads are the
// important source of NaN in practice and they are never free.
func nanFreeRegs(fn *ir.Function) map[*ir.Reg]bool {
	defs := map[*ir.Reg][]ir.Instr{}
	regs := map[*ir.Reg]bool{}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			use, def := ir.DefUse(ins)
			for _, r := range use {
				regs[r] = true
			}
			for _, r := range def {
				regs[r] = true
				defs[r] = append(defs[r], ins)
			}
		}
		for _, r := range ir.TermUses(b.Term) {
			regs[r] = true
		}
	}
	// A register is free when every definition is (a mutable variable like a
	// loop counter has several). Start optimistic and shrink to a fixpoint.
	free := map[*ir.Reg]bool{}
	for r := range regs {
		free[r] = true
	}
	freeVal := func(v ir.Value) bool { return nanFreeValue(v, free) }
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

func isNaNConst(c *ir.Const) bool {
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
func nanFreeInstr(ins ir.Instr, freeVal func(ir.Value) bool) bool {
	switch v := ins.(type) {
	case *ir.Assign:
		return freeVal(v.Src)
	case *ir.Bin:
		// 0/0 (Div) and x%0 (Mod) are NaN; the rest preserve "not NaN".
		if v.Op == ir.Div || v.Op == ir.Mod {
			return false
		}
		return freeVal(v.A) && freeVal(v.B)
	case *ir.Un:
		return freeVal(v.A)
	case *ir.Cmp:
		return true // a comparison yields 0 or 1
	case *ir.Select:
		return freeVal(v.Then) && freeVal(v.Else)
	case *ir.Builtin:
		return nanFreeBuiltin(v, freeVal)
	case *ir.Load, *ir.LoadSlot, *ir.LoadDyn:
		// A single-device read returns a logic value (a number) or faults the
		// chip; only batched reads yield NaN when nothing matches (verified in
		// game). `LoadDyn` covers reagent (`lr`) reads too.
		return true
	}
	// Loads (device/slot/batch), indirect and spill reads, and calls can bring
	// in arbitrary values.
	return false
}

// nanUnsafeBuiltins either yield NaN for some inputs (sqrt/log/asin/acos/
// atan2/pow) or read a value the compiler does not model (the stack, a device
// logic value, an indirect register). `lr` (readReagent) is deliberately absent:
// it returns a reagent *amount*, a non-negative count that cannot be NaN.
var nanUnsafeBuiltins = map[string]bool{
	"sqrt": true, "log": true, "asin": true, "acos": true,
	"atan2": true, "pow": true, "rand": true,
	"pop": true, "peek": true, "get": true, "getd": true, "ireg": true,
	"readByIdSlot": true,
}

func nanFreeBuiltin(v *ir.Builtin, freeVal func(ir.Value) bool) bool {
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
func nanFreeValue(v ir.Value, nf map[*ir.Reg]bool) bool {
	switch x := v.(type) {
	case nil:
		return true
	case *ir.Reg:
		return nf[x]
	case *ir.Const:
		return !isNaNConst(x)
	}
	return true
}

// branchInvertible reports whether br's condition can be negated exactly:
// equality and zero tests always can, an ordering test only when both operands
// are provably not NaN, and a NaN test never (IC10 has no branch-if-not-NaN).
func branchInvertible(br *ir.Br, nf map[*ir.Reg]bool) bool {
	if br.Cond.Invertible() {
		return true
	}
	if br.Cond == ir.NaN {
		return false
	}
	return nanFreeValue(br.A, nf) && nanFreeValue(br.B, nf)
}
