package opt

import (
	"strconv"

	"ic10go/internal/ir"
)

// SplitSetup moves one-time setup instructions out of fn into a new function.
//
// A "setup" instruction is a device write whose operands are all compile-time
// constants and that runs once before the first loop (its block is not inside a
// loop and dominates a loop header). Device state persists, so such writes only
// need to run once; hoisting them into a separate loader lets the runtime stay
// inside the line budget.
//
// It also hoists a constant device write from *inside* a loop when the write runs
// on every iteration of every loop that contains it and the device has no other
// writer anywhere. Re-asserting the same constant every tick is normally
// redundant (device state persists), and those repetitions are what push state
// machines over the per-tick instruction budget. Two cases are deliberately left
// alone:
//
//   - a write in a conditional arm (a state machine's `if mode == 0 { d0.On = 1 }`):
//     leaving the loop would stop re-asserting the value when that state is
//     entered again, so only writes that run on every iteration qualify;
//   - a device that some other instruction also writes: the loader runs before
//     the runtime, so hoisting part of a device's writes would reorder them.
//
// Moving a pre-loop write is semantically a no-op (it happened once either way,
// in the same order), so the caller may do that whenever a loader exists for
// another reason. Moving an in-loop write *changes how often the device is
// written*, so it only happens when hoistInLoop is set -- the explicit
// `--extract-setup` -- because a device may deliberately act on each write.
//
// It returns the setup function, or nil when nothing is hoistable. The caller
// generates the two functions separately (the loader runs once, then the
// runtime replaces it).
func SplitSetup(fn *ir.Function, hoistInLoop bool) *ir.Function {
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

	// A block runs on every iteration of a loop when it dominates each of the
	// loop's latches (the blocks whose back edge returns to the header). Requiring
	// it for every loop that contains the block also covers a loop that is itself
	// entered conditionally: then no block inside it dominates the outer latch.
	everyIteration := map[*ir.Block]bool{}
	for _, b := range fn.Blocks {
		if !inLoop[b] {
			continue
		}
		ok := true
		for _, lp := range loops {
			if !lp.blocks[b] {
				continue
			}
			latches := 0
			for _, src := range fn.Blocks {
				for _, s := range succs[src] {
					if s != lp.header || !dom[src][lp.header] {
						continue
					}
					latches++
					if !dom[src][b] {
						ok = false
					}
				}
			}
			if latches == 0 {
				ok = false
			}
		}
		everyIteration[b] = ok
	}

	// How each device is written across the whole function: an in-loop hoist is
	// only safe when every write of that device is hoistable too (see above).
	writes := map[string]int{}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			if d, ok := deviceOfWrite(ins); ok {
				writes[d]++
			}
		}
	}
	hoistableWriters := map[string]int{}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			d, ok := deviceOfWrite(ins)
			if !ok || !isSetupWrite(ins) {
				continue
			}
			if preLoop[b] || (everyIteration[b] && writes[d] > 0) {
				hoistableWriters[d]++
			}
		}
	}
	deviceOK := map[string]bool{}
	for d, n := range writes {
		deviceOK[d] = hoistableWriters[d] == n
	}

	// A device that is *read* inside a loop must keep its in-loop write: hoisting
	// it stops the program re-asserting the value, so the read can observe
	// whatever another writer left behind -- a behaviour change the
	// "re-asserting the same constant each tick is redundant" rationale does not
	// cover (review §2.6).
	readInLoop := map[string]bool{}
	for _, b := range fn.Blocks {
		if !inLoop[b] {
			continue
		}
		for _, ins := range b.Instrs {
			if d, ok := deviceOfRead(ins); ok {
				readInLoop[d] = true
			}
		}
	}

	setup := ir.NewBuilder("__setup")
	moved := 0
	for _, b := range fn.Blocks {
		if !preLoop[b] && !everyIteration[b] {
			continue
		}
		kept := make([]ir.Instr, 0, len(b.Instrs))
		for _, ins := range b.Instrs {
			if !isSetupWrite(ins) {
				kept = append(kept, ins)
				continue
			}
			if !preLoop[b] && (!hoistInLoop || !deviceOK[deviceKey(ins)] || readInLoop[deviceKey(ins)]) {
				// Inside a loop: only with --extract-setup, only when the device
				// has no writer left behind, and only when the loop does not read
				// the device it writes.
				kept = append(kept, ins)
				continue
			}
			setup.Emit(ins)
			moved++
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

// deviceOfWrite names the device a write addresses, or reports that this is not
// a device write. The name is an identity key for "does anything else write this
// device", so two instructions addressing the same device must agree; a
// runtime-selected device (a register) is not nameable.
func deviceOfWrite(ins ir.Instr) (string, bool) {
	switch x := ins.(type) {
	case *ir.Store:
		return "d:" + x.Dev, true
	case *ir.StoreSlot:
		if x.DevPtr != nil {
			return "", false
		}
		return "d:" + x.Dev, true
	case *ir.StoreDyn:
		if x.DevPtr != nil {
			return "", false
		}
		if x.Dev != "" {
			return "d:" + x.Dev, true
		}
		if id, ok := constKey(x.DevID); ok {
			return "id:" + id, true
		}
		return "", false
	case *ir.Batch:
		switch x.Kind {
		case ir.BatchStore, ir.BatchStoreName, ir.BatchStoreSlot:
			dev, ok1 := constKey(x.Device)
			name, ok2 := constKey(x.Name)
			if !ok1 || !ok2 {
				return "", false
			}
			return "b:" + dev + "/" + name, true
		}
	}
	return "", false
}

// deviceKey is deviceOfWrite for a write we already know is one.
func deviceKey(ins ir.Instr) string {
	d, _ := deviceOfWrite(ins)
	return d
}

// deviceOfRead names the device a load addresses, using the same identity keys
// as deviceOfWrite so the two sets can be compared.
func deviceOfRead(ins ir.Instr) (string, bool) {
	switch x := ins.(type) {
	case *ir.Load:
		return "d:" + x.Dev, true
	case *ir.LoadSlot:
		if x.DevPtr != nil {
			return "", false
		}
		return "d:" + x.Dev, true
	case *ir.LoadDyn:
		if x.DevPtr != nil {
			return "", false
		}
		if x.Dev != "" {
			return "d:" + x.Dev, true
		}
		if id, ok := constKey(x.DevID); ok {
			return "id:" + id, true
		}
		return "", false
	case *ir.Batch:
		switch x.Kind {
		case ir.BatchLoad, ir.BatchLoadName, ir.BatchLoadSlot, ir.BatchLoadNameSlot:
			dev, ok1 := constKey(x.Device)
			name, ok2 := constKey(x.Name)
			if !ok1 || !ok2 {
				return "", false
			}
			return "b:" + dev + "/" + name, true
		}
	}
	return "", false
}

// constKey renders a constant operand as text, so two identical constants
// compare equal. A non-constant (nil is the "absent" operand) reports false.
func constKey(v ir.Value) (string, bool) {
	if v == nil {
		return "", false
	}
	c, ok := v.(*ir.Const)
	if !ok {
		return "", false
	}
	switch {
	case c.Raw != "":
		return "raw:" + c.Raw, true
	case c.Special != "":
		return "sp:" + c.Special, true
	default:
		return "num:" + strconv.FormatFloat(c.V, 'g', -1, 64), true
	}
}

func constValue(v ir.Value) bool {
	if v == nil {
		return true
	}
	_, ok := v.(*ir.Const)
	return ok
}
