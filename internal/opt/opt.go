// Package opt contains IR optimisation passes. All passes are conservative:
// they preserve observable behaviour, including device access order.
package opt

import (
	"fmt"
	"math"
	"strconv"

	"ic10go/internal/ir"
)

// Optimize runs the optimisation pipeline to a fixed point.
func Optimize(fn *ir.Function) {
	fn.BuildCFG()
	for i := 0; i < 16; i++ {
		c1 := propagate(fn)
		c2 := constProp(fn)
		c3 := foldAll(fn)
		c4 := simplify(fn)
		c5 := redundantLoads(fn)
		c6 := globalCSE(fn)
		c7 := selectConvert(fn)
		c8 := foldBranches(fn)
		c9 := licm(fn)
		c10 := dce(fn)
		c11 := removeUnreachable(fn)
		fn.BuildCFG()
		if !c1 && !c2 && !c3 && !c4 && !c5 && !c6 && !c7 && !c8 && !c9 && !c10 && !c11 {
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
	case *ir.LoadDyn:
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
	case *ir.LoadDyn:
		add(v.Logic)
	case *ir.StoreDyn:
		add(v.Logic, v.Src)
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
	case *ir.StoreDyn:
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

// ---------------------------------------------------------------------------
// Global constant propagation (must analysis)
// ---------------------------------------------------------------------------

// constProp propagates constants across basic blocks. A register is known to
// hold a constant at a block entry only if it holds the same constant on every
// incoming path.
func constProp(fn *ir.Function) bool {
	fn.BuildCFG()
	in := map[*ir.Block]map[*ir.Reg]*ir.Const{}
	out := map[*ir.Block]map[*ir.Reg]*ir.Const{}
	for _, b := range fn.Blocks {
		in[b] = map[*ir.Reg]*ir.Const{}
		out[b] = map[*ir.Reg]*ir.Const{}
	}
	for changed := true; changed; {
		changed = false
		for _, b := range fn.Blocks {
			ni := meetPreds(b, out)
			if !constMapEqual(ni, in[b]) {
				in[b] = ni
				changed = true
			}
			no := transfer(b, in[b])
			if !constMapEqual(no, out[b]) {
				out[b] = no
				changed = true
			}
		}
	}

	rewritten := false
	for _, b := range fn.Blocks {
		state := copyConstMap(in[b])
		for idx, ins := range b.Instrs {
			if replaceConstUses(ins, state) {
				rewritten = true
			}
			if f, ok := foldConst(ins); ok {
				ins = f
				b.Instrs[idx] = f
				rewritten = true
			}
			d := defOf(ins)
			if d == nil {
				continue
			}
			delete(state, d)
			if c := evalConst(ins, state); c != nil {
				state[d] = c
			}
		}
	}
	return rewritten
}

func meetPreds(b *ir.Block, out map[*ir.Block]map[*ir.Reg]*ir.Const) map[*ir.Reg]*ir.Const {
	result := map[*ir.Reg]*ir.Const{}
	if len(b.Preds) == 0 {
		return result
	}
	for r, c := range out[b.Preds[0]] {
		result[r] = c
	}
	for _, p := range b.Preds[1:] {
		for r, c := range result {
			oc, ok := out[p][r]
			if !ok || !constEqual(c, oc) {
				delete(result, r)
			}
		}
	}
	return result
}

func transfer(b *ir.Block, in map[*ir.Reg]*ir.Const) map[*ir.Reg]*ir.Const {
	state := copyConstMap(in)
	for _, ins := range b.Instrs {
		d := defOf(ins)
		if d == nil {
			continue
		}
		delete(state, d)
		if c := evalConst(ins, state); c != nil {
			state[d] = c
		}
	}
	return state
}

// evalConst returns the constant a pure instruction evaluates to, or nil.
func evalConst(i ir.Instr, state map[*ir.Reg]*ir.Const) *ir.Const {
	resolve := func(v ir.Value) ir.Value {
		if r, ok := v.(*ir.Reg); ok {
			if c, ok := state[r]; ok {
				return c
			}
		}
		return v
	}
	switch v := i.(type) {
	case *ir.Assign:
		if c, ok := resolve(v.Src).(*ir.Const); ok {
			return c
		}
	case *ir.Bin:
		a, ok1 := resolve(v.A).(*ir.Const)
		b, ok2 := resolve(v.B).(*ir.Const)
		if ok1 && ok2 {
			if c, ok := foldBin(v.Op, a, b); ok {
				return c
			}
		}
	case *ir.Un:
		if a, ok := resolve(v.A).(*ir.Const); ok {
			if c, ok := foldUn(v.Op, a); ok {
				return c
			}
		}
	case *ir.Cmp:
		if v.B == nil {
			if a, ok := resolve(v.A).(*ir.Const); ok {
				if c, ok := foldCmpUn(v.Cond, a); ok {
					return c
				}
			}
		} else if a, ok1 := resolve(v.A).(*ir.Const); ok1 {
			if b, ok2 := resolve(v.B).(*ir.Const); ok2 {
				if c, ok := foldCmp(v.Cond, a, b); ok {
					return c
				}
			}
		}
	case *ir.Select:
		if c, ok := resolve(v.Cond).(*ir.Const); ok {
			if c.V != 0 {
				if r, ok := resolve(v.Then).(*ir.Const); ok {
					return r
				}
			} else if r, ok := resolve(v.Else).(*ir.Const); ok {
				return r
			}
		}
	}
	return nil
}

func replaceConstUses(i ir.Instr, state map[*ir.Reg]*ir.Const) bool {
	changed := false
	rw := func(v ir.Value) ir.Value {
		if r, ok := v.(*ir.Reg); ok {
			if c, ok := state[r]; ok {
				changed = true
				return c
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

func constEqual(a, b *ir.Const) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Special == b.Special && a.Raw == b.Raw && a.V == b.V
}

func copyConstMap(m map[*ir.Reg]*ir.Const) map[*ir.Reg]*ir.Const {
	c := make(map[*ir.Reg]*ir.Const, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func constMapEqual(a, b map[*ir.Reg]*ir.Const) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !constEqual(v, b[k]) {
			return false
		}
	}
	return true
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
// Algebraic simplification (peephole)
// ---------------------------------------------------------------------------

func isConstVal(v ir.Value, x float64) bool {
	c, ok := v.(*ir.Const)
	return ok && c.Special == "" && c.Raw == "" && c.V == x
}

func sameValue(a, b ir.Value) bool {
	ra, ok1 := a.(*ir.Reg)
	rb, ok2 := b.(*ir.Reg)
	if ok1 && ok2 {
		return ra == rb
	}
	ca, ok1 := a.(*ir.Const)
	cb, ok2 := b.(*ir.Const)
	if ok1 && ok2 {
		return ca.Special == cb.Special && ca.Raw == cb.Raw && ca.V == cb.V
	}
	return false
}

// simplify applies exact algebraic identities. nil with ok=true means the
// instruction should be deleted.
func simplify(fn *ir.Function) bool {
	changed := false
	for _, b := range fn.Blocks {
		kept := b.Instrs[:0]
		for _, ins := range b.Instrs {
			ni, ok := simplifyInstr(ins)
			if ok {
				changed = true
				if ni == nil {
					continue
				}
				ins = ni
			}
			kept = append(kept, ins)
		}
		b.Instrs = kept
	}
	return changed
}

func simplifyInstr(i ir.Instr) (ir.Instr, bool) {
	switch v := i.(type) {
	case *ir.Assign:
		if r, ok := v.Src.(*ir.Reg); ok && r == v.Dst {
			return nil, true // self copy
		}
	case *ir.Bin:
		switch v.Op {
		case ir.Mul:
			if isConstVal(v.B, 1) {
				return &ir.Assign{Dst: v.Dst, Src: v.A}, true
			}
			if isConstVal(v.A, 1) {
				return &ir.Assign{Dst: v.Dst, Src: v.B}, true
			}
		case ir.Div:
			if isConstVal(v.B, 1) {
				return &ir.Assign{Dst: v.Dst, Src: v.A}, true
			}
		case ir.Add:
			if isConstVal(v.B, 0) {
				return &ir.Assign{Dst: v.Dst, Src: v.A}, true
			}
			if isConstVal(v.A, 0) {
				return &ir.Assign{Dst: v.Dst, Src: v.B}, true
			}
		case ir.Sub:
			if isConstVal(v.B, 0) {
				return &ir.Assign{Dst: v.Dst, Src: v.A}, true
			}
		case ir.BitAnd:
			if isConstVal(v.A, 0) || isConstVal(v.B, 0) {
				return &ir.Assign{Dst: v.Dst, Src: &ir.Const{V: 0}}, true
			}
		case ir.Min, ir.Max:
			if sameValue(v.A, v.B) {
				return &ir.Assign{Dst: v.Dst, Src: v.A}, true
			}
		}
	case *ir.Select:
		if sameValue(v.Then, v.Else) {
			return &ir.Assign{Dst: v.Dst, Src: v.Then}, true
		}
	}
	return i, false
}

// ---------------------------------------------------------------------------
// Redundant device load elimination (block-local)
// ---------------------------------------------------------------------------

// redundantLoads reuses a previously loaded value when nothing between the two
// loads can change it. It covers device loads, constant-index slot loads and
// batch loads whose operands are all constant.
func redundantLoads(fn *ir.Function) bool {
	changed := false
	for _, b := range fn.Blocks {
		seen := map[string]*ir.Reg{}
		byDevice := map[string][]string{}
		invalidate := func(dev string) {
			for _, k := range byDevice[dev] {
				delete(seen, k)
			}
			delete(byDevice, dev)
		}
		clearAll := func() {
			clear(seen)
			clear(byDevice)
		}
		add := func(key, dev string, r *ir.Reg) {
			seen[key] = r
			byDevice[dev] = append(byDevice[dev], key)
		}
		for idx, ins := range b.Instrs {
			if key, dev, dst, ok := loadKey(ins); ok {
				if r, found := seen[key]; found {
					b.Instrs[idx] = &ir.Assign{Dst: dst, Src: r}
					changed = true
				} else {
					add(key, dev, dst)
				}
				continue
			}
			switch v := ins.(type) {
			case *ir.Store:
				invalidate(v.Dev)
			case *ir.StoreSlot:
				invalidate(v.Dev)
			case *ir.Batch:
				switch v.Kind {
				case ir.BatchStore, ir.BatchStoreName, ir.BatchStoreSlot:
					clearAll()
				}
			case *ir.Builtin:
				if hasSideEffect(v) {
					clearAll()
				}
			}
		}
	}
	return changed
}

// loadKey recognises a redundant-load candidate and returns a stable key, the
// device it reads and its destination.
func loadKey(i ir.Instr) (key, dev string, dst *ir.Reg, ok bool) {
	switch v := i.(type) {
	case *ir.Load:
		return "l|" + v.Dev + "|" + v.Logic, v.Dev, v.Dst, true
	case *ir.LoadSlot:
		c, isC := v.Index.(*ir.Const)
		if !isC {
			return "", "", nil, false
		}
		return "ls|" + v.Dev + "|" + c.String() + "|" + v.Logic, v.Dev, v.Dst, true
	case *ir.Batch:
		if v.Dst == nil {
			return "", "", nil, false
		}
		dev, ok := constText(v.Device)
		if !ok {
			return "", "", nil, false
		}
		name, ok1 := constText(v.Name)
		slot, ok2 := constText(v.Slot)
		mode, ok3 := constText(v.Mode)
		if !ok1 || !ok2 || !ok3 {
			return "", "", nil, false
		}
		return fmt.Sprintf("b%d|%s|%s|%s|%s|%s", v.Kind, dev, name, slot, v.Logic, mode), dev, v.Dst, true
	}
	return "", "", nil, false
}

func constText(v ir.Value) (string, bool) {
	if v == nil {
		return "", true
	}
	if c, ok := v.(*ir.Const); ok {
		return c.String(), true
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Common subexpression elimination (global, via available expressions)
// ---------------------------------------------------------------------------

// globalCSE extends CSE across basic blocks using available expressions: an
// expression is available at a point if it was computed on every path and its
// operands have not been redefined.
func globalCSE(fn *ir.Function) bool {
	fn.BuildCFG()
	availIn, _ := availableExprs(fn)
	changed := false
	for _, b := range fn.Blocks {
		avail := map[string]*ir.Reg{}
		rev := map[*ir.Reg]map[string]bool{}
		for k, r := range availIn[b] {
			avail[k] = r
			if rev[r] == nil {
				rev[r] = map[string]bool{}
			}
			rev[r][k] = true
		}
		invalidate := func(r *ir.Reg) {
			for key := range rev[r] {
				delete(avail, key)
			}
			delete(rev, r)
		}
		add := func(key string, r *ir.Reg, operands []ir.Value) {
			avail[key] = r
			if rev[r] == nil {
				rev[r] = map[string]bool{}
			}
			rev[r][key] = true
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

// availableExprs computes, for each block, the expressions available at entry
// (and exit), mapped to the register holding them.
func availableExprs(fn *ir.Function) (in, out map[*ir.Block]map[string]*ir.Reg) {
	in = map[*ir.Block]map[string]*ir.Reg{}
	out = map[*ir.Block]map[string]*ir.Reg{}
	for _, b := range fn.Blocks {
		in[b] = map[string]*ir.Reg{}
		out[b] = map[string]*ir.Reg{}
	}
	for changed := true; changed; {
		changed = false
		for _, b := range fn.Blocks {
			ni := meetAvail(b, out)
			no := transferAvail(b, ni)
			if !availEqual(ni, in[b]) {
				in[b] = ni
				changed = true
			}
			if !availEqual(no, out[b]) {
				out[b] = no
				changed = true
			}
		}
	}
	return in, out
}

func meetAvail(b *ir.Block, out map[*ir.Block]map[string]*ir.Reg) map[string]*ir.Reg {
	res := map[string]*ir.Reg{}
	if len(b.Preds) == 0 {
		return res
	}
	for k, r := range out[b.Preds[0]] {
		res[k] = r
	}
	for _, p := range b.Preds[1:] {
		for k, r := range res {
			if pr, ok := out[p][k]; !ok || pr != r {
				delete(res, k)
			}
		}
	}
	return res
}

func transferAvail(b *ir.Block, in map[string]*ir.Reg) map[string]*ir.Reg {
	avail := map[string]*ir.Reg{}
	rev := map[*ir.Reg]map[string]bool{}
	for k, r := range in {
		avail[k] = r
		if rev[r] == nil {
			rev[r] = map[string]bool{}
		}
		rev[r][k] = true
	}
	invalidate := func(r *ir.Reg) {
		for key := range rev[r] {
			delete(avail, key)
		}
		delete(rev, r)
	}
	add := func(key string, r *ir.Reg, operands []ir.Value) {
		avail[key] = r
		if rev[r] == nil {
			rev[r] = map[string]bool{}
		}
		rev[r][key] = true
		for _, v := range operands {
			if rr, ok := v.(*ir.Reg); ok {
				if rev[rr] == nil {
					rev[rr] = map[string]bool{}
				}
				rev[rr][key] = true
			}
		}
	}
	for _, ins := range b.Instrs {
		key, operands, ok := exprKey(ins)
		d := defOf(ins)
		if d != nil {
			invalidate(d)
		}
		if ok && !containsReg(operands, d) {
			add(key, d, operands)
		}
	}
	return avail
}

func availEqual(a, b map[string]*ir.Reg) bool {
	if len(a) != len(b) {
		return false
	}
	for k, r := range a {
		if b[k] != r {
			return false
		}
	}
	return true
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
// Branch folding
// ---------------------------------------------------------------------------

// foldBranches turns branches with a constant or identical condition into an
// unconditional jump, which lets the untaken block be removed.
func foldBranches(fn *ir.Function) bool {
	changed := false
	for _, b := range fn.Blocks {
		br, ok := b.Term.(*ir.Br)
		if !ok {
			continue
		}
		if br.Then == br.Else {
			b.Term = &ir.Jmp{Target: br.Then}
			changed = true
			continue
		}
		if take, ok := constCond(br); ok {
			target := br.Else
			if take {
				target = br.Then
			}
			b.Term = &ir.Jmp{Target: target}
			changed = true
		}
	}
	return changed
}

// constCond evaluates a branch condition when both operands are constants.
func constCond(br *ir.Br) (bool, bool) {
	if br.B == nil {
		c, ok := br.A.(*ir.Const)
		if !ok {
			return false, false
		}
		res, ok := foldCmpUn(br.Cond, c)
		if !ok {
			return false, false
		}
		return res.V != 0, true
	}
	a, ok1 := br.A.(*ir.Const)
	b, ok2 := br.B.(*ir.Const)
	if !ok1 || !ok2 {
		return false, false
	}
	res, ok := foldCmp(br.Cond, a, b)
	if !ok {
		return false, false
	}
	return res.V != 0, true
}

// ---------------------------------------------------------------------------
// Loop-invariant code motion
// ---------------------------------------------------------------------------

type loop struct {
	header *ir.Block
	blocks map[*ir.Block]bool
}

// licm hoists pure, loop-invariant computations out of natural loops.
func licm(fn *ir.Function) bool {
	fn.BuildCFG()
	// A call (jal) can modify any variable and returns via "j ra" to any call
	// site. realSuccs below deliberately omits those return edges, so the loop
	// analysis would not see a callee's writes; hoisting would then treat a
	// variable written by the callee as loop-invariant. Skip LICM entirely
	// when the function uses call/ret.
	for _, b := range fn.Blocks {
		if _, ok := b.Term.(*ir.JmpRA); ok {
			return false
		}
	}
	liveIn, _ := liveness(fn)
	succs := realSuccs(fn)
	preds := buildPreds(fn, succs)
	dom := dominators(fn, preds)
	loops := findLoops(fn, succs, dom, preds)
	changed := false
	for _, lp := range loops {
		pre := ensurePreheader(fn, lp, preds)
		if pre == nil {
			continue
		}
		if hoistLoop(fn, lp, pre, liveIn[lp.header]) {
			changed = true
		}
	}
	if changed {
		fn.BuildCFG()
	}
	return changed
}

// realSuccs returns control-flow successors, ignoring the conservative
// JmpRA-to-return edges used only for liveness.
func realSuccs(fn *ir.Function) map[*ir.Block][]*ir.Block {
	succs := map[*ir.Block][]*ir.Block{}
	for _, b := range fn.Blocks {
		switch t := b.Term.(type) {
		case *ir.Jmp:
			succs[b] = []*ir.Block{t.Target}
		case *ir.Goto:
			succs[b] = []*ir.Block{t.Target}
		case *ir.Call:
			if t.Return != nil {
				succs[b] = []*ir.Block{t.Target, t.Return}
			} else {
				succs[b] = []*ir.Block{t.Target}
			}
		case *ir.Br:
			succs[b] = []*ir.Block{t.Then, t.Else}
		}
	}
	return succs
}

func buildPreds(fn *ir.Function, succs map[*ir.Block][]*ir.Block) map[*ir.Block][]*ir.Block {
	preds := map[*ir.Block][]*ir.Block{}
	for _, b := range fn.Blocks {
		for _, s := range succs[b] {
			preds[s] = append(preds[s], b)
		}
	}
	return preds
}

func dominators(fn *ir.Function, preds map[*ir.Block][]*ir.Block) map[*ir.Block]map[*ir.Block]bool {
	all := map[*ir.Block]bool{}
	for _, b := range fn.Blocks {
		all[b] = true
	}
	dom := map[*ir.Block]map[*ir.Block]bool{}
	for _, b := range fn.Blocks {
		if b == fn.Entry {
			dom[b] = map[*ir.Block]bool{b: true}
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
			var nd map[*ir.Block]bool
			if len(ps) == 0 {
				nd = map[*ir.Block]bool{}
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

func findLoops(fn *ir.Function, succs map[*ir.Block][]*ir.Block, dom map[*ir.Block]map[*ir.Block]bool, preds map[*ir.Block][]*ir.Block) []*loop {
	var loops []*loop
	for _, b := range fn.Blocks {
		for _, s := range succs[b] {
			if dom[b][s] { // back edge b -> s
				blocks := map[*ir.Block]bool{s: true}
				stack := []*ir.Block{b}
				for len(stack) > 0 {
					n := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					if blocks[n] {
						continue
					}
					blocks[n] = true
					stack = append(stack, preds[n]...)
				}
				loops = append(loops, &loop{header: s, blocks: blocks})
			}
		}
	}
	return loops
}

// ensurePreheader returns a block that dominates the loop header and is the
// only entry to it, creating one if needed.
func ensurePreheader(fn *ir.Function, lp *loop, preds map[*ir.Block][]*ir.Block) *ir.Block {
	h := lp.header
	var outside []*ir.Block
	for _, p := range preds[h] {
		if !lp.blocks[p] {
			outside = append(outside, p)
		}
	}
	if len(outside) == 1 && len(realTermSuccs(outside[0])) == 1 {
		return outside[0]
	}
	if len(outside) == 0 {
		return nil
	}
	pre := fn.NewBlock()
	pre.Term = &ir.Jmp{Target: h}
	for _, p := range outside {
		redirect(p, h, pre)
	}
	return pre
}

func realTermSuccs(b *ir.Block) []*ir.Block {
	switch t := b.Term.(type) {
	case *ir.Jmp:
		return []*ir.Block{t.Target}
	case *ir.Goto:
		return []*ir.Block{t.Target}
	case *ir.Call:
		return []*ir.Block{t.Target, t.Return}
	case *ir.Br:
		return []*ir.Block{t.Then, t.Else}
	}
	return nil
}

func redirect(b *ir.Block, from, to *ir.Block) {
	switch t := b.Term.(type) {
	case *ir.Jmp:
		if t.Target == from {
			t.Target = to
		}
	case *ir.Goto:
		if t.Target == from {
			t.Target = to
		}
	case *ir.Call:
		if t.Target == from {
			t.Target = to
		}
		if t.Return == from {
			t.Return = to
		}
	case *ir.Br:
		if t.Then == from {
			t.Then = to
		}
		if t.Else == from {
			t.Else = to
		}
	}
}

func hoistLoop(fn *ir.Function, lp *loop, pre *ir.Block, liveIn map[*ir.Reg]bool) bool {
	changed := false
	for {
		defined := map[*ir.Reg]bool{}
		for _, b := range fn.Blocks {
			if !lp.blocks[b] {
				continue
			}
			for _, ins := range b.Instrs {
				if d := defOf(ins); d != nil {
					defined[d] = true
				}
			}
		}
		moved := false
		for _, b := range fn.Blocks {
			if !lp.blocks[b] {
				continue
			}
			kept := b.Instrs[:0]
			for _, ins := range b.Instrs {
				if hoistable(ins) && !usesAny(ins, defined) && !definesLiveIn(ins, liveIn) {
					pre.Instrs = append(pre.Instrs, ins)
					moved = true
					changed = true
					continue
				}
				kept = append(kept, ins)
			}
			b.Instrs = kept
		}
		if !moved {
			break
		}
	}
	return changed
}

// definesLiveIn reports whether an instruction defines a register that is live
// at the loop header. Hoisting such a definition would change its value on
// iterations that did not originally execute it.
func definesLiveIn(i ir.Instr, liveIn map[*ir.Reg]bool) bool {
	d := defOf(i)
	return d != nil && liveIn[d]
}

func hoistable(i ir.Instr) bool {
	switch i.(type) {
	case *ir.Assign, *ir.Bin, *ir.Un, *ir.Cmp, *ir.Select:
		return true
	}
	return false
}

func usesAny(i ir.Instr, defined map[*ir.Reg]bool) bool {
	for _, v := range usesOf(i) {
		if r, ok := v.(*ir.Reg); ok && defined[r] {
			return true
		}
	}
	return false
}

func copySet(s map[*ir.Block]bool) map[*ir.Block]bool {
	c := make(map[*ir.Block]bool, len(s))
	for k := range s {
		c[k] = true
	}
	return c
}

func intersectSet(a, b map[*ir.Block]bool) map[*ir.Block]bool {
	c := map[*ir.Block]bool{}
	for k := range a {
		if b[k] {
			c[k] = true
		}
	}
	return c
}

func setEqual(a, b map[*ir.Block]bool) bool {
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
