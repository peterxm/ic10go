package opt

import "ic10go/internal/ir"

// SplitSetup moves one-time setup instructions out of fn into a new function.
//
// A "setup" instruction is a device write whose operands are all compile-time
// constants and that runs once before the first loop (its block is not inside a
// loop and dominates a loop header). Device state persists, so such writes only
// need to run once; hoisting them into a separate loader lets the runtime stay
// inside the 128-line budget.
//
// It returns the setup function, or nil when nothing is hoistable. The caller
// generates the two functions separately (the loader runs once, then the
// runtime replaces it).
func SplitSetup(fn *ir.Function) *ir.Function {
	fn.BuildCFG()
	succs := ir.Successors(fn)
	preds := ir.Preds(fn)
	dom := ir.Dominators(fn)
	loops := findLoops(fn, succs, dom, preds)
	inLoop := map[*ir.Block]bool{}
	for _, lp := range loops {
		inLoop[lp.header] = true
		for b := range lp.blocks {
			inLoop[b] = true
		}
	}

	// A block is pre-loop when it is not inside a loop and dominates a loop
	// header: every path that reaches the loop runs it exactly once. This
	// covers a straight-line prologue and one guarded by a conditional (for
	// example the data-segment sentinel check).
	preLoop := map[*ir.Block]bool{}
	for _, b := range fn.Blocks {
		if inLoop[b] {
			continue
		}
		for _, lp := range loops {
			if dom[lp.header][b] {
				preLoop[b] = true
				break
			}
		}
	}

	setup := ir.NewBuilder("__setup")
	moved := 0
	for _, b := range fn.Blocks {
		if !preLoop[b] {
			continue
		}
		kept := make([]ir.Instr, 0, len(b.Instrs))
		for _, ins := range b.Instrs {
			if isSetupWrite(ins) {
				setup.Emit(ins)
				moved++
				continue
			}
			kept = append(kept, ins)
		}
		b.Instrs = kept
	}
	if moved == 0 {
		return nil
	}
	setup.SetTerm(&ir.Ret{})
	return setup.Fn()
}

// isSetupWrite reports whether ins is a device write with all-constant operands.
func isSetupWrite(ins ir.Instr) bool {
	switch x := ins.(type) {
	case *ir.Store:
		return constValue(x.Src)
	case *ir.StoreSlot:
		return x.DevPtr == nil && constValue(x.Index) && constValue(x.Src)
	case *ir.StoreDyn:
		return x.DevPtr == nil && constValue(x.DevID) && constValue(x.Logic) && constValue(x.Src)
	case *ir.Batch:
		switch x.Kind {
		case ir.BatchStore, ir.BatchStoreName, ir.BatchStoreSlot:
			return constValue(x.Device) && constValue(x.Name) && constValue(x.Slot) && constValue(x.Src)
		}
	}
	return false
}

func constValue(v ir.Value) bool {
	if v == nil {
		return true
	}
	_, ok := v.(*ir.Const)
	return ok
}
