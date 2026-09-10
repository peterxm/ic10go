// Package regalloc assigns virtual registers to the sixteen IC10 CPU registers
// using liveness analysis and graph colouring.
package regalloc

import (
	"fmt"
	"sort"

	"ic10go/internal/ir"
)

// Allocate maps every virtual register in fn to a physical register index in
// [0, k). It returns an error if spilling would be required.
func Allocate(fn *ir.Function, k int) (map[*ir.Reg]int, error) {
	fn.BuildCFG()
	in, out := liveness(fn)
	graph := interference(fn, out)
	_ = in
	return color(fn, graph, k)
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
	case *ir.Builtin:
		for _, a := range v.Args {
			use = append(use, valuesRegs(a)...)
		}
		if v.Dst != nil {
			def = []*ir.Reg{v.Dst}
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
	}
	return use, def
}

func termUses(t ir.Term) []*ir.Reg {
	switch v := t.(type) {
	case *ir.Br:
		return append(valuesRegs(v.A), valuesRegs(v.B)...)
	case *ir.Ret:
		return valuesRegs(v.Value)
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

func color(fn *ir.Function, g map[*ir.Reg]map[*ir.Reg]bool, k int) (map[*ir.Reg]int, error) {
	nodes := allRegs(fn)
	list := make([]*ir.Reg, 0, len(nodes))
	for r := range nodes {
		list = append(list, r)
	}
	sort.Slice(list, func(i, j int) bool {
		di, dj := len(g[list[i]]), len(g[list[j]])
		if di != dj {
			return di > dj
		}
		return list[i].ID < list[j].ID
	})

	colors := map[*ir.Reg]int{}
	for _, r := range list {
		used := map[int]bool{}
		for n := range g[r] {
			if c, ok := colors[n]; ok {
				used[c] = true
			}
		}
		c := -1
		for i := 0; i < k; i++ {
			if !used[i] {
				c = i
				break
			}
		}
		if c < 0 {
			return nil, fmt.Errorf("register pressure too high: cannot allocate a register for %s", r)
		}
		colors[r] = c
	}
	return colors, nil
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
