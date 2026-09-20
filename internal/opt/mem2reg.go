package opt

import "ic10go/internal/ir"

// promoteUserStack promotes constant user-stack slots to virtual registers
// (mem2reg), removing their get/put/poke instructions. It is only sound when
// fn.PrivateStack is set: the slots are then private to this program, and IC10
// registers persist across ticks exactly like the stack, so a slot's value can
// live in a register instead. No initial load is emitted: a register read before
// it is written starts at 0, matching the stack slot's initial value.
//
// Merge points use a dedicated register written by a copy at the end of each
// predecessor. Only slots that are written somewhere are promoted, so a
// read-only slot does not leave a lone access for the next round to re-promote.
func promoteUserStack(fn *ir.Function) bool {
	if !fn.PrivateStack || fn.NoMem2Reg || fn.UserLimit <= 0 || fn.UserStackDynamic {
		return false
	}
	fn.BuildCFG()
	slots, ok := promotableSlots(fn)
	if !ok || len(slots) == 0 {
		return false
	}
	changed := false
	for slot := range slots {
		if promoteSlot(fn, slot) {
			changed = true
		}
	}
	return changed
}

// promotableSlots collects the constant user slots written by fn. It reports
// ok=false when the function has an access it cannot reason about: a peek
// (reads sp-1) or a dynamic get/put/poke on the housing stack.
func promotableSlots(fn *ir.Function) (map[int]bool, bool) {
	slots := map[int]bool{}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			v, isB := ins.(*ir.Builtin)
			if !isB {
				continue
			}
			switch v.Name {
			case "peek":
				return nil, false
			case "get", "put":
				if len(v.Args) < 2 {
					continue
				}
				if d, isDev := v.Args[0].(*ir.Device); !isDev || d.Name != "db" {
					continue
				}
				n, isConst := constSlot(v.Args[1])
				if !isConst {
					return nil, false // dynamic housing-stack address
				}
				if n < fn.UserLimit && v.Name == "put" {
					slots[n] = true
				}
			case "poke":
				if len(v.Args) < 1 {
					continue
				}
				n, isConst := constSlot(v.Args[0])
				if !isConst {
					return nil, false
				}
				if n < fn.UserLimit {
					slots[n] = true
				}
			}
		}
	}
	return slots, true
}

func constSlot(v ir.Value) (int, bool) {
	c, ok := v.(*ir.Const)
	if !ok || c.Raw != "" || c.Special != "" {
		return 0, false
	}
	n := int(c.V)
	if float64(n) != c.V || n < 0 {
		return 0, false
	}
	return n, true
}

// slotAccess classifies an instruction as an access to slot. For a use it
// returns the get's destination; for a def it returns the stored value.
func slotAccess(ins ir.Instr, slot int) (use bool, def bool, dst *ir.Reg, val ir.Value) {
	v, ok := ins.(*ir.Builtin)
	if !ok {
		return false, false, nil, nil
	}
	switch v.Name {
	case "get":
		if len(v.Args) != 2 || v.Dst == nil {
			return false, false, nil, nil
		}
		if d, isDev := v.Args[0].(*ir.Device); !isDev || d.Name != "db" {
			return false, false, nil, nil
		}
		if n, ok := constSlot(v.Args[1]); ok && n == slot {
			return true, false, v.Dst, nil
		}
	case "put":
		if len(v.Args) != 3 {
			return false, false, nil, nil
		}
		if d, isDev := v.Args[0].(*ir.Device); !isDev || d.Name != "db" {
			return false, false, nil, nil
		}
		if n, ok := constSlot(v.Args[1]); ok && n == slot {
			return false, true, nil, v.Args[2]
		}
	case "poke":
		if len(v.Args) != 2 {
			return false, false, nil, nil
		}
		if n, ok := constSlot(v.Args[0]); ok && n == slot {
			return false, true, nil, v.Args[1]
		}
	}
	return false, false, nil, nil
}

func promoteSlot(fn *ir.Function, slot int) bool {
	// Must analysis: is the slot defined on every path to a block's entry?
	definedIn := map[*ir.Block]bool{}
	definedOut := map[*ir.Block]bool{}
	for changed := true; changed; {
		changed = false
		for _, b := range fn.Blocks {
			in := false
			if b != fn.Entry && len(b.Preds) > 0 {
				in = true
				for _, p := range b.Preds {
					if !definedOut[p] {
						in = false
						break
					}
				}
			}
			out := in || blockDefines(b, slot)
			if in != definedIn[b] || out != definedOut[b] {
				definedIn[b], definedOut[b] = in, out
				changed = true
			}
		}
	}

	// Read-before-def on some path means the value may come from a previous
	// tick; the entry uses a register that starts at 0 (registers persist).
	needsInit := false
	for _, b := range fn.Blocks {
		def := definedIn[b]
		for _, ins := range b.Instrs {
			use, isDef, _, _ := slotAccess(ins, slot)
			if use && !def {
				needsInit = true
			}
			if isDef {
				def = true
			}
		}
	}

	// Give every def its own register so the value flow is stable while the
	// merge registers are resolved.
	defReg := map[ir.Instr]*ir.Reg{}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			if _, def, _, _ := slotAccess(ins, slot); def {
				defReg[ins] = fn.NewReg("slot")
			}
		}
	}
	outReg := func(b *ir.Block, in *ir.Reg) *ir.Reg {
		cur := in
		for _, ins := range b.Instrs {
			if r, ok := defReg[ins]; ok {
				cur = r
			}
		}
		return cur
	}

	regIn := map[*ir.Block]*ir.Reg{}
	regOut := map[*ir.Block]*ir.Reg{}
	merge := map[*ir.Block]*ir.Reg{}
	if needsInit {
		regIn[fn.Entry] = fn.NewReg("slotinit")
	}
	for changed := true; changed; {
		changed = false
		for _, b := range fn.Blocks {
			var in *ir.Reg
			switch {
			case b == fn.Entry:
				in = regIn[b]
			case len(b.Preds) == 0:
				in = nil
			default:
				first, same := regOut[b.Preds[0]], true
				for _, p := range b.Preds[1:] {
					if regOut[p] != first {
						same = false
						break
					}
				}
				if same {
					in = first
				} else if merge[b] != nil {
					in = merge[b]
				} else {
					in = fn.NewReg("slot")
					merge[b] = in
					changed = true
				}
			}
			if in != regIn[b] {
				regIn[b] = in
				changed = true
			}
			out := outReg(b, in)
			if out != regOut[b] {
				regOut[b] = out
				changed = true
			}
		}
	}

	// Rewrite accesses to use/define registers.
	for _, b := range fn.Blocks {
		cur := regIn[b]
		out := make([]ir.Instr, 0, len(b.Instrs))
		for _, ins := range b.Instrs {
			use, def, dst, val := slotAccess(ins, slot)
			switch {
			case use && cur != nil:
				if cur != dst {
					out = append(out, &ir.Assign{Dst: dst, Src: cur})
				}
			case use:
				// No reaching definition (should not happen with the must
				// analysis); keep the load so the IR stays valid.
				out = append(out, ins)
				cur = dst
			case def:
				out = append(out, &ir.Assign{Dst: defReg[ins], Src: val})
				cur = defReg[ins]
			default:
				out = append(out, ins)
			}
		}
		b.Instrs = out
	}

	// Write each merge register from its predecessors.
	for _, b := range fn.Blocks {
		rm := merge[b]
		if rm == nil {
			continue
		}
		for _, p := range b.Preds {
			if src := regOut[p]; src != nil && src != rm {
				p.Instrs = append(p.Instrs, &ir.Assign{Dst: rm, Src: src})
			}
		}
	}
	return true
}

// blockDefines reports whether b writes slot at least once (a straight-line
// block then has the slot defined at its exit).
func blockDefines(b *ir.Block, slot int) bool {
	for _, ins := range b.Instrs {
		if _, def, _, _ := slotAccess(ins, slot); def {
			return true
		}
	}
	return false
}
