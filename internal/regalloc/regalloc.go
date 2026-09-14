// Package regalloc assigns virtual registers to the sixteen IC10 CPU registers
// using liveness analysis and graph colouring.
package regalloc

import (
	"fmt"
	"sort"

	"ic10go/internal/ir"
)

// Allocate maps every virtual register in fn to a physical register index in
// [0, k). If k registers are not enough it spills the excess to the IC10 stack,
// reserving the highest register as a scratch for the spill load sequence.
func Allocate(fn *ir.Function, k int) (map[*ir.Reg]int, error) {
	return AllocateReserved(fn, k, 0)
}

// AllocateReserved is Allocate with the top `reserved` stack slots kept free
// (used by the persistent data segment), so spills start below them.
func AllocateReserved(fn *ir.Function, k, reserved int) (map[*ir.Reg]int, error) {
	colors, _, err := AllocateReservedSpills(fn, k, reserved)
	return colors, err
}

// AllocateReservedSpills is AllocateReserved and also returns the number of
// stack slots used for spills (so the caller can check for data-segment
// overlap in the fixed-middle layout).
func AllocateReservedSpills(fn *ir.Function, k, reserved int) (map[*ir.Reg]int, int, error) {
	if colors, ok := tryColor(fn, k); ok {
		return colors, 0, nil
	}
	// Spilling path: reserve one register for the spill-load scratch.
	top := 511 - reserved
	sp := &spiller{fn: fn, slots: map[*ir.Reg]int{}, next: top}
	for iter := 0; iter < 64; iter++ {
		colors, spilled := tryColorPartial(fn, k-1)
		if len(spilled) == 0 {
			return colors, top - sp.next, nil
		}
		sp.spill(spilled)
	}
	return nil, 0, fmt.Errorf("register allocation did not converge")
}

func tryColor(fn *ir.Function, k int) (map[*ir.Reg]int, bool) {
	colors, spilled := tryColorPartial(fn, k)
	return colors, len(spilled) == 0
}

func tryColorPartial(fn *ir.Function, k int) (map[*ir.Reg]int, map[*ir.Reg]bool) {
	fn.BuildCFG()
	_, out := liveness(fn)
	graph := interference(fn, out)
	return colorGraph(fn, graph, k)
}

func valuesRegs(v ir.Value) []*ir.Reg {
	if r, ok := v.(*ir.Reg); ok {
		return []*ir.Reg{r}
	}
	return nil
}

func useDefInstr(i ir.Instr) (use, def []*ir.Reg) {
	switch v := i.(type) {
	case *ir.Assign:
		use = valuesRegs(v.Src)
		def = []*ir.Reg{v.Dst}
	case *ir.Bin:
		use = append(valuesRegs(v.A), valuesRegs(v.B)...)
		def = []*ir.Reg{v.Dst}
	case *ir.Un:
		use = valuesRegs(v.A)
		def = []*ir.Reg{v.Dst}
	case *ir.Cmp:
		use = append(valuesRegs(v.A), valuesRegs(v.B)...)
		def = []*ir.Reg{v.Dst}
	case *ir.Select:
		use = append(valuesRegs(v.Cond), valuesRegs(v.Then)...)
		use = append(use, valuesRegs(v.Else)...)
		def = []*ir.Reg{v.Dst}
	case *ir.Load:
		def = []*ir.Reg{v.Dst}
	case *ir.Store:
		use = valuesRegs(v.Src)
	case *ir.LoadSlot:
		use = valuesRegs(v.Index)
		def = []*ir.Reg{v.Dst}
	case *ir.StoreSlot:
		use = append(valuesRegs(v.Index), valuesRegs(v.Src)...)
	case *ir.LoadDyn:
		use = append(valuesRegs(v.DevPtr), valuesRegs(v.Logic)...)
		def = []*ir.Reg{v.Dst}
	case *ir.StoreDyn:
		use = append(valuesRegs(v.DevPtr), valuesRegs(v.Logic)...)
		use = append(use, valuesRegs(v.Src)...)
	case *ir.Builtin:
		for _, a := range v.Args {
			use = append(use, valuesRegs(a)...)
		}
		if v.Dst != nil {
			def = []*ir.Reg{v.Dst}
			// ins reads its destination (IC10 read-modify-write).
			if v.Name == "ins" {
				use = append(use, v.Dst)
			}
		}
	case *ir.Batch:
		use = append(use, valuesRegs(v.Device)...)
		use = append(use, valuesRegs(v.Name)...)
		use = append(use, valuesRegs(v.Slot)...)
		use = append(use, valuesRegs(v.Mode)...)
		use = append(use, valuesRegs(v.Src)...)
		if v.Dst != nil {
			def = []*ir.Reg{v.Dst}
		}
	case *ir.LoadSpecial:
		def = []*ir.Reg{v.Dst}
	case *ir.StoreSpecial:
		use = valuesRegs(v.Src)
	case *ir.LoadIndirect:
		use = valuesRegs(v.Ptr)
		def = []*ir.Reg{v.Dst}
	case *ir.StoreIndirect:
		use = append(valuesRegs(v.Ptr), valuesRegs(v.Src)...)
	case *ir.LoadSpill:
		def = []*ir.Reg{v.Dst}
	case *ir.StoreSpill:
		use = valuesRegs(v.Src)
	}
	return use, def
}

func termUses(t ir.Term) []*ir.Reg {
	switch v := t.(type) {
	case *ir.Br:
		return append(valuesRegs(v.A), valuesRegs(v.B)...)
	case *ir.BrApprox:
		return append(append(valuesRegs(v.A), valuesRegs(v.B)...), valuesRegs(v.Tol)...)
	case *ir.BrApproxZero:
		return append(valuesRegs(v.A), valuesRegs(v.Tol)...)
	case *ir.Ret:
		return valuesRegs(v.Value)
	case *ir.JmpDyn:
		return valuesRegs(v.Target)
	}
	return nil
}

func instrRegs(i ir.Instr) []*ir.Reg {
	use, def := useDefInstr(i)
	return append(append([]*ir.Reg{}, use...), def...)
}

func liveness(fn *ir.Function) (in, out map[*ir.Block]map[*ir.Reg]bool) {
	use := map[*ir.Block]map[*ir.Reg]bool{}
	def := map[*ir.Block]map[*ir.Reg]bool{}
	for _, b := range fn.Blocks {
		u, d := computeUseDef(b)
		use[b], def[b] = u, d
	}
	in = map[*ir.Block]map[*ir.Reg]bool{}
	out = map[*ir.Block]map[*ir.Reg]bool{}
	for _, b := range fn.Blocks {
		in[b] = map[*ir.Reg]bool{}
		out[b] = map[*ir.Reg]bool{}
	}

	for changed := true; changed; {
		changed = false
		for i := len(fn.Blocks) - 1; i >= 0; i-- {
			b := fn.Blocks[i]
			newOut := map[*ir.Reg]bool{}
			for _, s := range b.Succs {
				for r := range in[s] {
					newOut[r] = true
				}
			}
			newIn := map[*ir.Reg]bool{}
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

func computeUseDef(b *ir.Block) (map[*ir.Reg]bool, map[*ir.Reg]bool) {
	use := map[*ir.Reg]bool{}
	def := map[*ir.Reg]bool{}
	for _, ins := range b.Instrs {
		u, d := useDefInstr(ins)
		for _, r := range u {
			if !def[r] {
				use[r] = true
			}
		}
		for _, r := range d {
			def[r] = true
		}
	}
	for _, r := range termUses(b.Term) {
		if !def[r] {
			use[r] = true
		}
	}
	return use, def
}

func interference(fn *ir.Function, liveOut map[*ir.Block]map[*ir.Reg]bool) map[*ir.Reg]map[*ir.Reg]bool {
	g := map[*ir.Reg]map[*ir.Reg]bool{}
	addEdge := func(a, b *ir.Reg) {
		if a == b {
			return
		}
		if g[a] == nil {
			g[a] = map[*ir.Reg]bool{}
		}
		if g[b] == nil {
			g[b] = map[*ir.Reg]bool{}
		}
		g[a][b] = true
		g[b][a] = true
	}
	for _, b := range fn.Blocks {
		live := map[*ir.Reg]bool{}
		for r := range liveOut[b] {
			live[r] = true
		}
		for _, r := range termUses(b.Term) {
			live[r] = true
		}
		for i := len(b.Instrs) - 1; i >= 0; i-- {
			u, d := useDefInstr(b.Instrs[i])
			for _, dr := range d {
				for r := range live {
					if r != dr {
						addEdge(dr, r)
					}
				}
			}
			for _, r := range d {
				delete(live, r)
			}
			for _, r := range u {
				live[r] = true
			}
		}
	}
	return g
}

func allRegs(fn *ir.Function) map[*ir.Reg]bool {
	nodes := map[*ir.Reg]bool{}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			for _, r := range instrRegs(ins) {
				nodes[r] = true
			}
		}
		for _, r := range termUses(b.Term) {
			nodes[r] = true
		}
	}
	return nodes
}

// colorGraph colours the interference graph. It first coalesces copy-related
// registers (union-find, only when they do not interfere), then colours the
// resulting classes with Chaitin-Briggs.
func colorGraph(fn *ir.Function, g map[*ir.Reg]map[*ir.Reg]bool, k int) (map[*ir.Reg]int, map[*ir.Reg]bool) {
	nodes := allRegs(fn)

	parent := map[*ir.Reg]*ir.Reg{}
	var find func(*ir.Reg) *ir.Reg
	find = func(r *ir.Reg) *ir.Reg {
		if p, ok := parent[r]; ok {
			root := find(p)
			parent[r] = root
			return root
		}
		return r
	}
	cadj := map[*ir.Reg]map[*ir.Reg]bool{}
	addEdge := func(a, b *ir.Reg) {
		if a == b {
			return
		}
		if cadj[a] == nil {
			cadj[a] = map[*ir.Reg]bool{}
		}
		if cadj[b] == nil {
			cadj[b] = map[*ir.Reg]bool{}
		}
		cadj[a][b] = true
		cadj[b][a] = true
	}
	for u := range g {
		for v := range g[u] {
			addEdge(find(u), find(v))
		}
	}
	union := func(a, b *ir.Reg) {
		for n := range cadj[b] {
			if n != a {
				addEdge(a, n)
			}
		}
		delete(cadj, b)
		parent[b] = a
	}
	for _, blk := range fn.Blocks {
		for _, ins := range blk.Instrs {
			a, ok := ins.(*ir.Assign)
			if !ok {
				continue
			}
			s, ok := a.Src.(*ir.Reg)
			if !ok {
				continue
			}
			rd, rs := find(a.Dst), find(s)
			if rd == rs || cadj[rd][rs] {
				continue
			}
			union(rd, rs)
		}
	}

	// Build the class node list and costs.
	classSet := map[*ir.Reg]bool{}
	for r := range nodes {
		classSet[find(r)] = true
	}
	list := make([]*ir.Reg, 0, len(classSet))
	for r := range classSet {
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })

	cost := spillCosts(fn)
	ccost := map[*ir.Reg]int{}
	for r := range nodes {
		ccost[find(r)] += cost[r]
	}

	classColors, spilledClasses := chaitinBriggs(list, cadj, ccost, k)

	colors := map[*ir.Reg]int{}
	spilled := map[*ir.Reg]bool{}
	for r := range nodes {
		rep := find(r)
		if c, ok := classColors[rep]; ok {
			colors[r] = c
		} else if spilledClasses[rep] {
			spilled[r] = true
		}
	}
	return colors, spilled
}

// chaitinBriggs colours the graph: simplify by removing low-degree nodes,
// spilling the cheapest node when stuck, then assign colours in reverse.
func chaitinBriggs(list []*ir.Reg, g map[*ir.Reg]map[*ir.Reg]bool, cost map[*ir.Reg]int, k int) (map[*ir.Reg]int, map[*ir.Reg]bool) {
	degree := map[*ir.Reg]int{}
	for _, r := range list {
		degree[r] = len(g[r])
	}
	removed := map[*ir.Reg]bool{}
	var stack []*ir.Reg

	for len(stack) < len(list) {
		var trivial *ir.Reg
		for _, r := range list {
			if !removed[r] && degree[r] < k {
				trivial = r
				break
			}
		}
		if trivial == nil {
			bestRatio := 0.0
			for _, r := range list {
				if removed[r] {
					continue
				}
				d := degree[r]
				if d == 0 {
					d = 1
				}
				ratio := float64(cost[r]) / float64(d)
				if trivial == nil || ratio < bestRatio ||
					(ratio == bestRatio && r.ID < trivial.ID) {
					bestRatio = ratio
					trivial = r
				}
			}
		}
		if trivial == nil {
			break
		}
		removed[trivial] = true
		stack = append(stack, trivial)
		for n := range g[trivial] {
			if !removed[n] {
				degree[n]--
			}
		}
	}

	colors := map[*ir.Reg]int{}
	spilled := map[*ir.Reg]bool{}
	for i := len(stack) - 1; i >= 0; i-- {
		r := stack[i]
		used := map[int]bool{}
		for n := range g[r] {
			if c, ok := colors[n]; ok {
				used[c] = true
			}
		}
		c := -1
		for j := 0; j < k; j++ {
			if !used[j] {
				c = j
				break
			}
		}
		if c < 0 {
			spilled[r] = true
			continue
		}
		colors[r] = c
	}
	return colors, spilled
}

// spillCosts counts how often each register is used or defined.
func spillCosts(fn *ir.Function) map[*ir.Reg]int {
	cost := map[*ir.Reg]int{}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			u, d := useDefInstr(ins)
			for _, r := range u {
				cost[r]++
			}
			for _, r := range d {
				cost[r]++
			}
		}
		for _, r := range termUses(b.Term) {
			cost[r]++
		}
	}
	for r := range allRegs(fn) {
		cost[r]++ // never zero
	}
	return cost
}

// ---------------------------------------------------------------------------
// Spilling
// ---------------------------------------------------------------------------

type spiller struct {
	fn    *ir.Function
	slots map[*ir.Reg]int
	next  int
}

func (s *spiller) slotOf(r *ir.Reg) int {
	if slot, ok := s.slots[r]; ok {
		return slot
	}
	slot := s.next
	s.next--
	s.slots[r] = slot
	return slot
}

// spill rewrites the function so that the given registers live in stack slots.
func (s *spiller) spill(spilled map[*ir.Reg]bool) {
	s.slots = map[*ir.Reg]int{}
	for r := range spilled {
		s.slotOf(r)
	}
	for _, b := range s.fn.Blocks {
		var out []ir.Instr
		for _, ins := range b.Instrs {
			s.rewriteInstr(ins, &out)
		}
		s.rewriteTerm(b.Term, &out)
		b.Instrs = out
	}
}

func (s *spiller) isSpilled(r *ir.Reg) bool {
	_, ok := s.slots[r]
	return ok
}

func (s *spiller) loadIfSpilled(v ir.Value, out *[]ir.Instr) ir.Value {
	r, ok := v.(*ir.Reg)
	if !ok || !s.isSpilled(r) {
		return v
	}
	t := s.fn.NewReg("spill")
	*out = append(*out, &ir.LoadSpill{Dst: t, Slot: s.slotOf(r)})
	return t
}

func (s *spiller) rewriteInstr(ins ir.Instr, out *[]ir.Instr) {
	replaceInstrUses(ins, func(v ir.Value) ir.Value {
		return s.loadIfSpilled(v, out)
	})
	d := instrDef(ins)
	if d == nil || !s.isSpilled(d) {
		*out = append(*out, ins)
		return
	}
	t := s.fn.NewReg("spill")
	setInstrDef(ins, t)
	*out = append(*out, ins)
	*out = append(*out, &ir.StoreSpill{Slot: s.slotOf(d), Src: t})
}

func (s *spiller) rewriteTerm(t ir.Term, out *[]ir.Instr) {
	switch v := t.(type) {
	case *ir.Br:
		v.A = s.loadIfSpilled(v.A, out)
		if v.B != nil {
			v.B = s.loadIfSpilled(v.B, out)
		}
	case *ir.BrApprox:
		v.A = s.loadIfSpilled(v.A, out)
		v.B = s.loadIfSpilled(v.B, out)
		v.Tol = s.loadIfSpilled(v.Tol, out)
	case *ir.BrApproxZero:
		v.A = s.loadIfSpilled(v.A, out)
		v.Tol = s.loadIfSpilled(v.Tol, out)
	case *ir.Ret:
		if v.Value != nil {
			v.Value = s.loadIfSpilled(v.Value, out)
		}
	case *ir.JmpDyn:
		v.Target = s.loadIfSpilled(v.Target, out)
	}
}

func instrDef(i ir.Instr) *ir.Reg {
	_, def := useDefInstr(i)
	if len(def) == 1 {
		return def[0]
	}
	return nil
}

func setInstrDef(i ir.Instr, r *ir.Reg) {
	switch v := i.(type) {
	case *ir.Assign:
		v.Dst = r
	case *ir.Bin:
		v.Dst = r
	case *ir.Un:
		v.Dst = r
	case *ir.Cmp:
		v.Dst = r
	case *ir.Select:
		v.Dst = r
	case *ir.Load:
		v.Dst = r
	case *ir.LoadSlot:
		v.Dst = r
	case *ir.LoadSpecial:
		v.Dst = r
	case *ir.LoadDyn:
		v.Dst = r
	case *ir.LoadIndirect:
		v.Dst = r
	case *ir.LoadSpill:
		v.Dst = r
	case *ir.Builtin:
		v.Dst = r
	case *ir.Batch:
		v.Dst = r
	}
}

func replaceInstrUses(i ir.Instr, f func(ir.Value) ir.Value) {
	switch v := i.(type) {
	case *ir.Assign:
		v.Src = f(v.Src)
	case *ir.Bin:
		v.A = f(v.A)
		v.B = f(v.B)
	case *ir.Un:
		v.A = f(v.A)
	case *ir.Cmp:
		v.A = f(v.A)
		if v.B != nil {
			v.B = f(v.B)
		}
	case *ir.Select:
		v.Cond = f(v.Cond)
		v.Then = f(v.Then)
		v.Else = f(v.Else)
	case *ir.Store:
		v.Src = f(v.Src)
	case *ir.LoadSlot:
		v.Index = f(v.Index)
	case *ir.StoreSlot:
		v.Index = f(v.Index)
		v.Src = f(v.Src)
	case *ir.LoadDyn:
		v.DevPtr = f(v.DevPtr)
		v.Logic = f(v.Logic)
	case *ir.StoreDyn:
		v.DevPtr = f(v.DevPtr)
		v.Logic = f(v.Logic)
		v.Src = f(v.Src)
	case *ir.Builtin:
		for j := range v.Args {
			v.Args[j] = f(v.Args[j])
		}
	case *ir.Batch:
		v.Device = f(v.Device)
		v.Name = f(v.Name)
		v.Slot = f(v.Slot)
		v.Mode = f(v.Mode)
		v.Src = f(v.Src)
	case *ir.StoreSpecial:
		v.Src = f(v.Src)
	case *ir.LoadIndirect:
		v.Ptr = f(v.Ptr)
	case *ir.StoreIndirect:
		v.Ptr = f(v.Ptr)
		v.Src = f(v.Src)
	case *ir.StoreSpill:
		v.Src = f(v.Src)
	}
}

func sameSet(a, b map[*ir.Reg]bool) bool {
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
