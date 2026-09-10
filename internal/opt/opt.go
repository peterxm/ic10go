// Package opt contains IR optimisation passes. All passes are conservative:
// they preserve observable behaviour, including device access order.
package opt

import (
	"math"
	"strconv"

	"ic10go/internal/ir"
)

// Optimize runs the optimisation pipeline to a fixed point.
func Optimize(fn *ir.Function) {
	fn.BuildCFG()
	for i := 0; i < 16; i++ {
		c1 := propagate(fn)
		c2 := foldAll(fn)
		c3 := cse(fn)
		c4 := selectConvert(fn)
		c5 := dce(fn)
		c6 := removeUnreachable(fn)
		fn.BuildCFG()
		if !c1 && !c2 && !c3 && !c4 && !c5 && !c6 {
			break
		}
	}
}

// ---------------------------------------------------------------------------
// Introspection helpers
// ---------------------------------------------------------------------------

func defOf(i ir.Instr) *ir.Reg {
	switch v := i.(type) {
	case *ir.Assign:
		return v.Dst
	case *ir.Bin:
		return v.Dst
	case *ir.Un:
		return v.Dst
	case *ir.Cmp:
		return v.Dst
	case *ir.Select:
		return v.Dst
	case *ir.Load:
		return v.Dst
	case *ir.LoadSlot:
		return v.Dst
	case *ir.Builtin:
		return v.Dst
	case *ir.Batch:
		return v.Dst
	case *ir.LoadSpecial:
		return v.Dst
	case *ir.LoadIndirect:
		return v.Dst
	}
	return nil
}

func usesOf(i ir.Instr) []ir.Value {
	var u []ir.Value
	add := func(vs ...ir.Value) {
		for _, v := range vs {
			if v != nil {
				u = append(u, v)
			}
		}
	}
	switch v := i.(type) {
	case *ir.Assign:
		add(v.Src)
	case *ir.Bin:
		add(v.A, v.B)
	case *ir.Un:
		add(v.A)
	case *ir.Cmp:
		add(v.A, v.B)
	case *ir.Select:
		add(v.Cond, v.Then, v.Else)
	case *ir.Store:
		add(v.Src)
	case *ir.LoadSlot:
		add(v.Index)
	case *ir.StoreSlot:
		add(v.Index, v.Src)
	case *ir.Builtin:
		for _, a := range v.Args {
			add(a)
		}
	case *ir.Batch:
		add(v.Device, v.Name, v.Slot, v.Mode, v.Src)
	case *ir.StoreSpecial:
		add(v.Src)
	case *ir.LoadIndirect:
		add(v.Ptr)
	case *ir.StoreIndirect:
		add(v.Ptr, v.Src)
	}
	return u
}

func termUses(t ir.Term) []ir.Value {
	switch v := t.(type) {
	case *ir.Br:
		return []ir.Value{v.A, v.B}
	case *ir.Ret:
		if v.Value != nil {
			return []ir.Value{v.Value}
		}
	case *ir.JmpDyn:
		return []ir.Value{v.Target}
	}
	return nil
}

func hasSideEffect(i ir.Instr) bool {
	switch v := i.(type) {
	case *ir.Store, *ir.StoreSlot:
		return true
	case *ir.StoreSpecial, *ir.StoreIndirect:
		return true
	case *ir.Batch:
		switch v.Kind {
		case ir.BatchStore, ir.BatchStoreName, ir.BatchStoreSlot:
			return true
		}
	case *ir.Builtin:
		switch v.Name {
		case "yield", "sleep", "hcf":
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Copy and constant propagation (block-local)
// ---------------------------------------------------------------------------

func propagate(fn *ir.Function) bool {
	changed := false
	for _, b := range fn.Blocks {
		val := map[*ir.Reg]ir.Value{}
		for idx, ins := range b.Instrs {
			if rewriteUses(ins, val) {
				changed = true
			}
			if f, ok := foldConst(ins); ok {
				ins = f
				b.Instrs[idx] = f
				changed = true
			}
			d := defOf(ins)
			if d == nil {
				continue
			}
			for x, v := range val {
				if r, ok := v.(*ir.Reg); ok && r == d {
					delete(val, x)
				}
			}
			switch v := ins.(type) {
			case *ir.Assign:
				switch src := v.Src.(type) {
				case *ir.Const:
					val[d] = src
				case *ir.Reg:
					if rv, ok := val[src]; ok {
						val[d] = rv
					} else {
						val[d] = src
					}
				default:
					delete(val, d)
				}
			default:
				delete(val, d)
			}
		}
	}
	return changed
}

func rewriteUses(i ir.Instr, val map[*ir.Reg]ir.Value) bool {
	changed := false
	rw := func(v ir.Value) ir.Value {
		if r, ok := v.(*ir.Reg); ok {
			if nv, ok := val[r]; ok {
				changed = true
				return nv
			}
		}
		return v
	}
	switch v := i.(type) {
	case *ir.Assign:
		v.Src = rw(v.Src)
	case *ir.Bin:
		v.A = rw(v.A)
		v.B = rw(v.B)
	case *ir.Un:
		v.A = rw(v.A)
	case *ir.Cmp:
		v.A = rw(v.A)
		if v.B != nil {
			v.B = rw(v.B)
		}
	case *ir.Select:
		v.Cond = rw(v.Cond)
		v.Then = rw(v.Then)
		v.Else = rw(v.Else)
	case *ir.Store:
		v.Src = rw(v.Src)
	case *ir.LoadSlot:
		v.Index = rw(v.Index)
	case *ir.StoreSlot:
		v.Index = rw(v.Index)
		v.Src = rw(v.Src)
	case *ir.Builtin:
		for j := range v.Args {
			v.Args[j] = rw(v.Args[j])
		}
	}
	return changed
}

// ---------------------------------------------------------------------------
// Constant folding
// ---------------------------------------------------------------------------

func foldAll(fn *ir.Function) bool {
	changed := false
	for _, b := range fn.Blocks {
		for idx, ins := range b.Instrs {
			if f, ok := foldConst(ins); ok {
				b.Instrs[idx] = f
				changed = true
			}
		}
	}
	return changed
}

func foldConst(i ir.Instr) (ir.Instr, bool) {
	switch v := i.(type) {
	case *ir.Bin:
		ca, ok1 := v.A.(*ir.Const)
		cb, ok2 := v.B.(*ir.Const)
		if ok1 && ok2 {
			if c, ok := foldBin(v.Op, ca, cb); ok {
				return &ir.Assign{Dst: v.Dst, Src: c}, true
			}
		}
	case *ir.Un:
		if ca, ok := v.A.(*ir.Const); ok {
			if c, ok := foldUn(v.Op, ca); ok {
				return &ir.Assign{Dst: v.Dst, Src: c}, true
			}
		}
	case *ir.Cmp:
		if v.B == nil {
			if ca, ok := v.A.(*ir.Const); ok {
				if c, ok := foldCmpUn(v.Cond, ca); ok {
					return &ir.Assign{Dst: v.Dst, Src: c}, true
				}
			}
			return i, false
		}
		ca, ok1 := v.A.(*ir.Const)
		cb, ok2 := v.B.(*ir.Const)
		if ok1 && ok2 {
			if c, ok := foldCmp(v.Cond, ca, cb); ok {
				return &ir.Assign{Dst: v.Dst, Src: c}, true
			}
		}
	case *ir.Select:
		if cc, ok := v.Cond.(*ir.Const); ok {
			if cc.V != 0 {
				return &ir.Assign{Dst: v.Dst, Src: v.Then}, true
			}
			return &ir.Assign{Dst: v.Dst, Src: v.Else}, true
		}
	}
	return i, false
}

// ---------------------------------------------------------------------------
// Common subexpression elimination (block-local)
// ---------------------------------------------------------------------------

func cse(fn *ir.Function) bool {
	changed := false
	for _, b := range fn.Blocks {
		avail := map[string]*ir.Reg{}
		rev := map[*ir.Reg]map[string]bool{}
		invalidate := func(r *ir.Reg) {
			for key := range rev[r] {
				delete(avail, key)
			}
			delete(rev, r)
		}
		add := func(key string, r *ir.Reg, operands []ir.Value) {
			avail[key] = r
			for _, v := range operands {
				if rr, ok := v.(*ir.Reg); ok {
					if rev[rr] == nil {
						rev[rr] = map[string]bool{}
					}
					rev[rr][key] = true
				}
			}
		}
		for idx, ins := range b.Instrs {
			key, operands, ok := exprKey(ins)
			d := defOf(ins)
			if ok {
				if r, found := avail[key]; found {
					b.Instrs[idx] = &ir.Assign{Dst: d, Src: r}
					if d != nil {
						invalidate(d)
					}
					changed = true
					continue
				}
			}
			if d != nil {
				invalidate(d)
			}
			if ok && !containsReg(operands, d) {
				add(key, d, operands)
			}
		}
	}
	return changed
}

func containsReg(vs []ir.Value, r *ir.Reg) bool {
	if r == nil {
		return false
	}
	for _, v := range vs {
		if rr, ok := v.(*ir.Reg); ok && rr == r {
			return true
		}
	}
	return false
}

func exprKey(i ir.Instr) (string, []ir.Value, bool) {
	switch v := i.(type) {
	case *ir.Bin:
		return "b" + strconv.Itoa(int(v.Op)) + "|" + valKey(v.A) + "|" + valKey(v.B),
			[]ir.Value{v.A, v.B}, true
	case *ir.Un:
		return "u" + strconv.Itoa(int(v.Op)) + "|" + valKey(v.A),
			[]ir.Value{v.A}, true
	case *ir.Cmp:
		if v.B == nil {
			return "c" + strconv.Itoa(int(v.Cond)) + "|" + valKey(v.A) + "|-",
				[]ir.Value{v.A}, true
		}
		return "c" + strconv.Itoa(int(v.Cond)) + "|" + valKey(v.A) + "|" + valKey(v.B),
			[]ir.Value{v.A, v.B}, true
	case *ir.Select:
		return "s|" + valKey(v.Cond) + "|" + valKey(v.Then) + "|" + valKey(v.Else),
			[]ir.Value{v.Cond, v.Then, v.Else}, true
	}
	return "", nil, false
}

func valKey(v ir.Value) string {
	switch x := v.(type) {
	case *ir.Reg:
		return "r" + strconv.Itoa(x.ID)
	case *ir.Const:
		return "c" + x.String()
	case *ir.Device:
		return "d" + x.Name
	}
	return "?"
}

// ---------------------------------------------------------------------------
// Select conversion
// ---------------------------------------------------------------------------

// selectConvert rewrites a conditional-assignment diamond into a single select
// instruction.
func selectConvert(fn *ir.Function) bool {
	fn.BuildCFG()
	changed := false
	for _, b := range fn.Blocks {
		br, ok := b.Term.(*ir.Br)
		if !ok {
			continue
		}
		then, els := br.Then, br.Else
		if then == els || then == nil || els == nil {
			continue
		}
		if len(then.Preds) != 1 || len(els.Preds) != 1 {
			continue
		}
		if len(then.Instrs) != 1 || len(els.Instrs) != 1 {
			continue
		}
		ta, ok1 := then.Instrs[0].(*ir.Assign)
		ea, ok2 := els.Instrs[0].(*ir.Assign)
		if !ok1 || !ok2 || ta.Dst != ea.Dst {
			continue
		}
		tj, ok1 := then.Term.(*ir.Jmp)
		ej, ok2 := els.Term.(*ir.Jmp)
		if !ok1 || !ok2 || tj.Target != ej.Target {
			continue
		}

		cond, swap, needCmp := selectCond(br)
		thenSrc, elseSrc := ta.Src, ea.Src
		if swap {
			thenSrc, elseSrc = elseSrc, thenSrc
		}
		if needCmp {
			r := fn.NewReg("selcmp")
			b.Instrs = append(b.Instrs, &ir.Cmp{Cond: br.Cond, Dst: r, A: br.A, B: br.B})
			cond = r
		}
		b.Instrs = append(b.Instrs, &ir.Select{Dst: ta.Dst, Cond: cond, Then: thenSrc, Else: elseSrc})
		b.Term = &ir.Jmp{Target: tj.Target}
		changed = true
	}
	if changed {
		fn.BuildCFG()
		removeUnreachable(fn)
		fn.BuildCFG()
	}
	return changed
}

// selectCond maps a branch condition to a value tested against non-zero.
// swap indicates the branches must be exchanged; needCmp indicates a
// comparison instruction is required.
func selectCond(br *ir.Br) (val ir.Value, swap, needCmp bool) {
	isZero := func(v ir.Value) bool {
		c, ok := v.(*ir.Const)
		return ok && c.Special == "" && c.V == 0
	}
	switch br.Cond {
	case ir.NonZero:
		return br.A, false, false
	case ir.Zero:
		return br.A, true, false
	case ir.Ne:
		if isZero(br.B) {
			return br.A, false, false
		}
		if isZero(br.A) {
			return br.B, false, false
		}
	case ir.Eq:
		if isZero(br.B) {
			return br.A, true, false
		}
		if isZero(br.A) {
			return br.B, true, false
		}
	}
	return nil, false, true
}

func useDef(i ir.Instr) (use, def []*ir.Reg) {
	for _, v := range usesOf(i) {
		if r, ok := v.(*ir.Reg); ok {
			use = append(use, r)
		}
	}
	if d := defOf(i); d != nil {
		def = []*ir.Reg{d}
	}
	return use, def
}

// dce removes pure instructions whose result is not live, including dead stores
// to registers that are overwritten before use.
func dce(fn *ir.Function) bool {
	fn.BuildCFG()
	liveIn, liveOut := liveness(fn)
	changed := false
	for _, b := range fn.Blocks {
		live := map[*ir.Reg]bool{}
		for r := range liveOut[b] {
			live[r] = true
		}
		for _, v := range termUses(b.Term) {
			if r, ok := v.(*ir.Reg); ok {
				live[r] = true
			}
		}
		for i := len(b.Instrs) - 1; i >= 0; i-- {
			ins := b.Instrs[i]
			u, d := useDef(ins)
			if !hasSideEffect(ins) && len(d) == 1 && !live[d[0]] {
				b.Instrs = append(b.Instrs[:i], b.Instrs[i+1:]...)
				changed = true
				continue
			}
			for _, r := range d {
				delete(live, r)
			}
			for _, r := range u {
				live[r] = true
			}
		}
	}
	_ = liveIn
	return changed
}

func liveness(fn *ir.Function) (in, out map[*ir.Block]map[*ir.Reg]bool) {
	use := map[*ir.Block]map[*ir.Reg]bool{}
	def := map[*ir.Block]map[*ir.Reg]bool{}
	for _, b := range fn.Blocks {
		u := map[*ir.Reg]bool{}
		d := map[*ir.Reg]bool{}
		for _, ins := range b.Instrs {
			iu, id := useDef(ins)
			for _, r := range iu {
				if !d[r] {
					u[r] = true
				}
			}
			for _, r := range id {
				d[r] = true
			}
		}
		for _, v := range termUses(b.Term) {
			if r, ok := v.(*ir.Reg); ok && !d[r] {
				u[r] = true
			}
		}
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

// ---------------------------------------------------------------------------
// Unreachable block removal
// ---------------------------------------------------------------------------

func removeUnreachable(fn *ir.Function) bool {
	reachable := map[*ir.Block]bool{}
	var dfs func(b *ir.Block)
	dfs = func(b *ir.Block) {
		if b == nil || reachable[b] {
			return
		}
		reachable[b] = true
		for _, s := range b.Succs {
			dfs(s)
		}
	}
	dfs(fn.Entry)
	if len(reachable) == len(fn.Blocks) {
		return false
	}
	kept := fn.Blocks[:0]
	for _, b := range fn.Blocks {
		if reachable[b] {
			kept = append(kept, b)
		}
	}
	fn.Blocks = kept
	return true
}

// ---------------------------------------------------------------------------
// Constant evaluation helpers
// ---------------------------------------------------------------------------

func foldBin(op ir.BinOp, a, b *ir.Const) (*ir.Const, bool) {
	if a.Special != "" || b.Special != "" {
		return nil, false
	}
	x, y := a.V, b.V
	switch op {
	case ir.Add:
		return &ir.Const{V: x + y}, true
	case ir.Sub:
		return &ir.Const{V: x - y}, true
	case ir.Mul:
		return &ir.Const{V: x * y}, true
	case ir.Div:
		return &ir.Const{V: x / y}, true
	case ir.Mod:
		return &ir.Const{V: ic10Mod(x, y)}, true
	case ir.BitAnd:
		return &ir.Const{V: float64(int64(x) & int64(y))}, true
	case ir.BitOr:
		return &ir.Const{V: float64(int64(x) | int64(y))}, true
	case ir.BitXor:
		return &ir.Const{V: float64(int64(x) ^ int64(y))}, true
	case ir.Shl:
		return &ir.Const{V: float64(int64(x) << uint(int64(y)&63))}, true
	case ir.Shr:
		return &ir.Const{V: float64(int64(x) >> uint(int64(y)&63))}, true
	case ir.Min:
		return &ir.Const{V: math.Min(x, y)}, true
	case ir.Max:
		return &ir.Const{V: math.Max(x, y)}, true
	}
	return nil, false
}

func foldUn(op ir.UnOp, a *ir.Const) (*ir.Const, bool) {
	if a.Special != "" {
		return nil, false
	}
	switch op {
	case ir.Neg:
		return &ir.Const{V: -a.V}, true
	case ir.BitNot:
		return &ir.Const{V: float64(^int64(a.V))}, true
	case ir.Seqz:
		if a.V == 0 {
			return &ir.Const{V: 1}, true
		}
		return &ir.Const{V: 0}, true
	}
	return nil, false
}

func foldCmpUn(c ir.Cond, a *ir.Const) (*ir.Const, bool) {
	if a.Special != "" {
		return nil, false
	}
	switch c {
	case ir.NonZero:
		if a.V != 0 {
			return &ir.Const{V: 1}, true
		}
		return &ir.Const{V: 0}, true
	case ir.Zero:
		if a.V == 0 {
			return &ir.Const{V: 1}, true
		}
		return &ir.Const{V: 0}, true
	}
	return nil, false
}

func foldCmp(c ir.Cond, a, b *ir.Const) (*ir.Const, bool) {
	if a.Special != "" || b.Special != "" {
		return nil, false
	}
	x, y := a.V, b.V
	var r bool
	switch c {
	case ir.Eq:
		r = x == y
	case ir.Ne:
		r = x != y
	case ir.Lt:
		r = x < y
	case ir.Le:
		r = x <= y
	case ir.Gt:
		r = x > y
	case ir.Ge:
		r = x >= y
	default:
		return nil, false
	}
	if r {
		return &ir.Const{V: 1}, true
	}
	return &ir.Const{V: 0}, true
}

func ic10Mod(x, y float64) float64 {
	r := math.Mod(x, y)
	if r != 0 && (r < 0) != (y < 0) {
		r += y
	}
	return r
}
