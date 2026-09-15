package ir

// This file holds the control-flow and data-flow analyses shared by the
// optimizer and the register allocator, so each instruction/terminator's
// use/def is defined exactly once.

// ValueRegs returns the register named by v, if v is a register.
func ValueRegs(v Value) []*Reg {
	if r, ok := v.(*Reg); ok {
		return []*Reg{r}
	}
	return nil
}

// DefUse returns the registers read and written by an instruction.
func DefUse(i Instr) (use, def []*Reg) {
	switch v := i.(type) {
	case *Assign:
		use = ValueRegs(v.Src)
		def = []*Reg{v.Dst}
	case *Bin:
		use = append(ValueRegs(v.A), ValueRegs(v.B)...)
		def = []*Reg{v.Dst}
	case *Un:
		use = ValueRegs(v.A)
		def = []*Reg{v.Dst}
	case *Cmp:
		use = append(ValueRegs(v.A), ValueRegs(v.B)...)
		def = []*Reg{v.Dst}
	case *Select:
		use = append(ValueRegs(v.Cond), ValueRegs(v.Then)...)
		use = append(use, ValueRegs(v.Else)...)
		def = []*Reg{v.Dst}
	case *Load:
		def = []*Reg{v.Dst}
	case *Store:
		use = ValueRegs(v.Src)
	case *LoadSlot:
		use = append(ValueRegs(v.DevPtr), ValueRegs(v.Index)...)
		def = []*Reg{v.Dst}
	case *StoreSlot:
		use = append(ValueRegs(v.DevPtr), ValueRegs(v.Index)...)
		use = append(use, ValueRegs(v.Src)...)
	case *LoadDyn:
		use = append(ValueRegs(v.DevPtr), ValueRegs(v.DevID)...)
		use = append(use, ValueRegs(v.Logic)...)
		def = []*Reg{v.Dst}
	case *StoreDyn:
		use = append(ValueRegs(v.DevPtr), ValueRegs(v.DevID)...)
		use = append(use, ValueRegs(v.Logic)...)
		use = append(use, ValueRegs(v.Src)...)
	case *Builtin:
		for _, a := range v.Args {
			use = append(use, ValueRegs(a)...)
		}
		if v.Dst != nil {
			def = []*Reg{v.Dst}
			// ins reads its destination (IC10 read-modify-write).
			if v.Name == "ins" {
				use = append(use, v.Dst)
			}
		}
	case *Batch:
		use = append(use, ValueRegs(v.Device)...)
		use = append(use, ValueRegs(v.Name)...)
		use = append(use, ValueRegs(v.Slot)...)
		use = append(use, ValueRegs(v.Mode)...)
		use = append(use, ValueRegs(v.Src)...)
		if v.Dst != nil {
			def = []*Reg{v.Dst}
		}
	case *LoadSpecial:
		def = []*Reg{v.Dst}
	case *StoreSpecial:
		use = ValueRegs(v.Src)
	case *LoadIndirect:
		use = ValueRegs(v.Ptr)
		def = []*Reg{v.Dst}
	case *StoreIndirect:
		use = append(ValueRegs(v.Ptr), ValueRegs(v.Src)...)
	case *LoadSpill:
		def = []*Reg{v.Dst}
	case *StoreSpill:
		use = ValueRegs(v.Src)
	}
	return use, def
}

// DefOf returns the single register defined by an instruction, or nil.
func DefOf(i Instr) *Reg {
	_, def := DefUse(i)
	if len(def) == 1 {
		return def[0]
	}
	return nil
}

// TermUses returns the registers read by a terminator.
func TermUses(t Term) []*Reg {
	if t == nil {
		return nil
	}
	var regs []*Reg
	for _, v := range t.Uses() {
		regs = append(regs, ValueRegs(v)...)
	}
	return regs
}

// Liveness computes block-level live-in/live-out sets. The CFG (block Succs)
// must be up to date; call BuildCFG first if the CFG changed.
func Liveness(fn *Function) (in, out map[*Block]map[*Reg]bool) {
	use := map[*Block]map[*Reg]bool{}
	def := map[*Block]map[*Reg]bool{}
	for _, b := range fn.Blocks {
		u := map[*Reg]bool{}
		d := map[*Reg]bool{}
		for _, ins := range b.Instrs {
			iu, id := DefUse(ins)
			for _, r := range iu {
				if !d[r] {
					u[r] = true
				}
			}
			for _, r := range id {
				d[r] = true
			}
		}
		for _, r := range TermUses(b.Term) {
			if !d[r] {
				u[r] = true
			}
		}
		use[b], def[b] = u, d
	}
	in = map[*Block]map[*Reg]bool{}
	out = map[*Block]map[*Reg]bool{}
	for _, b := range fn.Blocks {
		in[b] = map[*Reg]bool{}
		out[b] = map[*Reg]bool{}
	}
	for changed := true; changed; {
		changed = false
		for i := len(fn.Blocks) - 1; i >= 0; i-- {
			b := fn.Blocks[i]
			newOut := map[*Reg]bool{}
			for _, s := range b.Succs {
				for r := range in[s] {
					newOut[r] = true
				}
			}
			newIn := map[*Reg]bool{}
			for r := range use[b] {
				newIn[r] = true
			}
			for r := range newOut {
				if !def[b][r] {
					newIn[r] = true
				}
			}
			if !sameSet(newOut, out[b]) || !sameSet(newIn, in[b]) {
				changed = true
			}
			out[b], in[b] = newOut, newIn
		}
	}
	return in, out
}

// Successors returns the control-flow successors of every block, ignoring the
// conservative JmpRA-to-return edges used only for liveness.
func Successors(fn *Function) map[*Block][]*Block {
	succs := map[*Block][]*Block{}
	for _, b := range fn.Blocks {
		if b.Term != nil {
			succs[b] = b.Term.Successors()
		}
	}
	return succs
}

// Preds returns the predecessors of every block, built from the terminators.
func Preds(fn *Function) map[*Block][]*Block {
	succs := Successors(fn)
	preds := map[*Block][]*Block{}
	for _, b := range fn.Blocks {
		for _, s := range succs[b] {
			preds[s] = append(preds[s], b)
		}
	}
	return preds
}

// Dominators returns the dominator set of every block.
func Dominators(fn *Function) map[*Block]map[*Block]bool {
	preds := Preds(fn)
	all := map[*Block]bool{}
	for _, b := range fn.Blocks {
		all[b] = true
	}
	dom := map[*Block]map[*Block]bool{}
	for _, b := range fn.Blocks {
		if b == fn.Entry {
			dom[b] = map[*Block]bool{b: true}
		} else {
			dom[b] = copySet(all)
		}
	}
	for changed := true; changed; {
		changed = false
		for _, b := range fn.Blocks {
			if b == fn.Entry {
				continue
			}
			ps := preds[b]
			var nd map[*Block]bool
			if len(ps) == 0 {
				nd = map[*Block]bool{}
			} else {
				nd = copySet(dom[ps[0]])
				for _, p := range ps[1:] {
					nd = intersectSet(nd, dom[p])
				}
			}
			nd[b] = true
			if !setEqual(nd, dom[b]) {
				dom[b] = nd
				changed = true
			}
		}
	}
	return dom
}

func sameSet(a, b map[*Reg]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func copySet(s map[*Block]bool) map[*Block]bool {
	c := make(map[*Block]bool, len(s))
	for k := range s {
		c[k] = true
	}
	return c
}

func intersectSet(a, b map[*Block]bool) map[*Block]bool {
	c := make(map[*Block]bool, len(a))
	for k := range a {
		if b[k] {
			c[k] = true
		}
	}
	return c
}

func setEqual(a, b map[*Block]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
