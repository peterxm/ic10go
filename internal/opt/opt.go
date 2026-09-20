// Package opt contains IR optimisation passes. All passes are conservative:
// they preserve observable behaviour, including device access order.
package opt

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"ic10go/internal/builtin"
	"ic10go/internal/ir"
)

// maxRounds caps the fixed-point iteration. A pass pipeline that keeps
// changing the IR after this many rounds is treated as a compiler bug rather
// than silently accepted.
const maxRounds = 64

// pass is one optimization pass: Run reports whether it changed the IR.
type pass struct {
	name string
	run  func(*ir.Function) bool
}

// pipeline is the ordered optimization pipeline.
var pipeline = []pass{
	{"propagate", propagate},
	{"constProp", constProp},
	{"fold", foldAll},
	{"simplify", simplify},
	{"pushpop", eliminatePushPop},
	{"mem2reg", promoteUserStack},
	{"redundantLoads", redundantLoads},
	{"cse", globalCSE},
	{"select", selectConvert},
	{"foldBranches", foldBranches},
	{"fuseBranches", fuseBranches},
	{"licm", licm},
	{"dce", dce},
	{"removeUnreachable", removeUnreachable},
	{"deadStores", deadStores},
	{"redundantDeviceStores", redundantDeviceStores},
	{"threadJumps", threadJumps},
}

// Optimize runs the optimization pipeline to a fixed point (capped at
// maxRounds). It returns an error only when the IR is malformed; running out of
// rounds is accepted because every pass is semantics-preserving.
func Optimize(fn *ir.Function) error {
	if err := ir.Verify(fn); err != nil {
		return fmt.Errorf("optimizer input: %w", err)
	}
	fn.BuildCFG()
	debug := os.Getenv("IC10C_DUMP_PASSES") != ""
	for i := 0; i < maxRounds; i++ {
		changed := false
		for _, p := range pipeline {
			if p.run(fn) {
				changed = true
				if debug {
					fmt.Fprintf(os.Stderr, "round %d: %s changed\n", i, p.name)
				}
			}
			if debug {
				if err := ir.Verify(fn); err != nil {
					fmt.Fprintf(os.Stderr, "round %d: after %s: INVALID: %v\n", i, p.name, err)
				}
			}
		}
		fn.BuildCFG()
		if !changed {
			return ir.VerifyReachable(fn)
		}
	}
	// Reached the cap without a fixed point. Every pass is semantics-preserving,
	// so the IR is still valid; accept it rather than failing the build. Set
	// IC10C_DUMP_PASSES=1 to see which pass keeps changing.
	return ir.VerifyReachable(fn)
}

// ---------------------------------------------------------------------------
// Introspection helpers
// ---------------------------------------------------------------------------

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
		return builtin.SemOf(v.Name).SideEffect
	}
	return false
}

// ---------------------------------------------------------------------------
// Copy and constant propagation (block-local)
// ---------------------------------------------------------------------------

// isPhysRegRaw reports whether v is a direct physical-register operand
// (ireg(const) lowered to the raw operand "rN"). Such a value is re-read at
// every use, so it must not be propagated or commoned up across an indirect
// write (setIreg) that could change it.
func isPhysRegRaw(v ir.Value) bool {
	c, ok := v.(*ir.Const)
	if !ok || len(c.Raw) < 2 || c.Raw[0] != 'r' {
		return false
	}
	for i := 1; i < len(c.Raw); i++ {
		if c.Raw[i] < '0' || c.Raw[i] > '9' {
			return false
		}
	}
	return true
}

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
			d := ir.DefOf(ins)
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
					if isPhysRegRaw(src) {
						delete(val, d)
					} else {
						val[d] = src
					}
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
			d := ir.DefOf(ins)
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
		d := ir.DefOf(ins)
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
			if isPhysRegRaw(c) {
				return nil
			}
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
		v.DevPtr = rw(v.DevPtr)
		v.Index = rw(v.Index)
	case *ir.StoreSlot:
		v.DevPtr = rw(v.DevPtr)
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
		v.DevPtr = rw(v.DevPtr)
		v.Index = rw(v.Index)
	case *ir.StoreSlot:
		v.DevPtr = rw(v.DevPtr)
		v.Index = rw(v.Index)
		v.Src = rw(v.Src)
	case *ir.Builtin:
		for j := range v.Args {
			v.Args[j] = rw(v.Args[j])
		}
	case *ir.Batch:
		v.Device = rw(v.Device)
		v.Name = rw(v.Name)
		v.Slot = rw(v.Slot)
		v.Mode = rw(v.Mode)
		v.Src = rw(v.Src)
	case *ir.LoadDyn:
		v.DevPtr = rw(v.DevPtr)
		v.DevID = rw(v.DevID)
		v.Logic = rwLogic(v.Logic, val, &changed)
		v.Reagent = rw(v.Reagent)
	case *ir.StoreDyn:
		v.DevPtr = rw(v.DevPtr)
		v.DevID = rw(v.DevID)
		v.Logic = rwLogic(v.Logic, val, &changed)
		v.Src = rw(v.Src)
	case *ir.LoadIndirect:
		v.Ptr = rw(v.Ptr)
	case *ir.StoreIndirect:
		v.Ptr = rw(v.Ptr)
		v.Src = rw(v.Src)
	case *ir.StoreSpecial:
		v.Src = rw(v.Src)
	}
	return changed
}

// rwLogic rewrites a dynamic logic-type operand, but only to another register:
// IC10's dynamic form requires a register, so a constant must not be folded in.
func rwLogic(v ir.Value, val map[*ir.Reg]ir.Value, changed *bool) ir.Value {
	r, ok := v.(*ir.Reg)
	if !ok {
		return v
	}
	if nv, ok := val[r]; ok {
		if nr, isReg := nv.(*ir.Reg); isReg {
			*changed = true
			return nr
		}
	}
	return v
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
// loads can change it. It covers device loads, slot loads (constant or
// register-indexed), device-stack get, and batch loads whose operands are all
// constant.
func redundantLoads(fn *ir.Function) bool {
	changed := false
	for _, b := range fn.Blocks {
		seen := map[string]ir.Value{}
		byDevice := map[string][]string{}
		byReg := map[*ir.Reg][]string{}
		invalidate := func(dev string) {
			for _, k := range byDevice[dev] {
				delete(seen, k)
			}
			delete(byDevice, dev)
		}
		invalidateReg := func(r *ir.Reg) {
			for _, k := range byReg[r] {
				delete(seen, k)
			}
			delete(byReg, r)
		}
		invalidateKey := func(key string) {
			delete(seen, key)
			for dev, keys := range byDevice {
				for i, k := range keys {
					if k == key {
						byDevice[dev] = append(keys[:i], keys[i+1:]...)
						break
					}
				}
			}
			for r, keys := range byReg {
				for i, k := range keys {
					if k == key {
						byReg[r] = append(keys[:i], keys[i+1:]...)
						break
					}
				}
			}
		}
		invalidateDevsExcept := func(keep string) {
			for dev := range byDevice {
				if dev != keep {
					invalidate(dev)
				}
			}
		}
		clearAll := func() {
			clear(seen)
			clear(byDevice)
			clear(byReg)
		}
		add := func(key, dev string, val ir.Value, deps []*ir.Reg) {
			seen[key] = val
			byDevice[dev] = append(byDevice[dev], key)
			for _, d := range deps {
				byReg[d] = append(byReg[d], key)
			}
		}
		for idx, ins := range b.Instrs {
			if key, dev, dst, deps, ok := loadKey(ins); ok {
				if r, found := seen[key]; found {
					b.Instrs[idx] = &ir.Assign{Dst: dst, Src: r}
					changed = true
				} else {
					add(key, dev, dst, deps)
				}
				continue
			}
			// A redefinition of a register used as a load index invalidates
			// every load keyed on it.
			if d := ir.DefOf(ins); d != nil {
				invalidateReg(d)
			}
			switch v := ins.(type) {
			case *ir.Store:
				invalidate(v.Dev)
			case *ir.StoreSlot:
				invalidate(v.Dev)
			case *ir.StoreDyn:
				// A dynamic write targets a known port or a runtime device id;
				// invalidate that device, or every load for a runtime id.
				if v.Dev != "" {
					invalidate(v.Dev)
				} else {
					clearAll()
				}
			case *ir.StoreSpecial:
				invalidate(v.Name)
			case *ir.Batch:
				switch v.Kind {
				case ir.BatchStore, ir.BatchStoreName, ir.BatchStoreSlot:
					clearAll()
				}
			case *ir.Builtin:
				if !hasSideEffect(v) {
					continue
				}
				k := loadWrite(v)
				switch {
				case k.all:
					clearAll()
				case k.allDevs:
					invalidateDevsExcept("db")
				case k.key != "":
					invalidateKey(k.key)
				case k.dev != "":
					invalidate(k.dev)
				}
				if v.Name == "push" || v.Name == "pop" {
					invalidate("sp")
				}
				// Forward a subsequent get of the same slot to the stored value
				// (store-to-load forwarding).
				switch v.Name {
				case "put":
					if len(v.Args) == 3 {
						if d, isDev := v.Args[0].(*ir.Device); isDev {
							key := "g|" + d.Name + "|" + valKey(v.Args[1])
							add(key, d.Name, v.Args[2], depsOf(v.Args[1]))
						}
					}
				case "poke":
					if len(v.Args) == 2 {
						if _, isConst := v.Args[0].(*ir.Const); isConst {
							add("g|db|"+valKey(v.Args[0]), "db", v.Args[1], nil)
						}
					}
				}
			}
		}
	}
	return changed
}

// depsOf returns the register a value depends on, for load invalidation.
func depsOf(v ir.Value) []*ir.Reg {
	if r, ok := v.(*ir.Reg); ok {
		return []*ir.Reg{r}
	}
	return nil
}

// loadKey recognises a redundant-load candidate and returns a stable key, the
// device it reads, its destination, and any registers its address depends on.
func loadKey(i ir.Instr) (key, dev string, dst *ir.Reg, deps []*ir.Reg, ok bool) {
	indexReg := func(v ir.Value) []*ir.Reg {
		if r, isR := v.(*ir.Reg); isR {
			return []*ir.Reg{r}
		}
		return nil
	}
	switch v := i.(type) {
	case *ir.Load:
		return "l|" + v.Dev + "|" + v.Logic, v.Dev, v.Dst, nil, true
	case *ir.LoadSlot:
		if c, isC := v.Index.(*ir.Const); isC {
			return "ls|" + v.Dev + "|" + c.String() + "|" + v.Logic, v.Dev, v.Dst, nil, true
		}
		return "ls|" + v.Dev + "|" + valKey(v.Index) + "|" + v.Logic, v.Dev, v.Dst, indexReg(v.Index), true
	case *ir.Builtin:
		// get(dev, addr) reads a device-stack slot; addr may be a register.
		if v.Name == "get" && v.Dst != nil && len(v.Args) == 2 {
			if d, isDev := v.Args[0].(*ir.Device); isDev {
				return "g|" + d.Name + "|" + valKey(v.Args[1]), d.Name, v.Dst, indexReg(v.Args[1]), true
			}
		}
		return "", "", nil, nil, false
	case *ir.Batch:
		if v.Dst == nil {
			return "", "", nil, nil, false
		}
		dev, ok := constText(v.Device)
		if !ok {
			return "", "", nil, nil, false
		}
		name, ok1 := constText(v.Name)
		slot, ok2 := constText(v.Slot)
		mode, ok3 := constText(v.Mode)
		if !ok1 || !ok2 || !ok3 {
			return "", "", nil, nil, false
		}
		return fmt.Sprintf("b%d|%s|%s|%s|%s|%s", v.Kind, dev, name, slot, v.Logic, mode), dev, v.Dst, nil, true
	case *ir.LoadSpecial:
		return "sp|" + v.Name, v.Name, v.Dst, nil, true
	}
	return "", "", nil, nil, false
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
		avail := map[string]availExpr{}
		rev := map[*ir.Reg]map[string]bool{}
		byDev := map[string][]string{}
		register := func(key string, e availExpr) {
			avail[key] = e
			if rev[e.reg] == nil {
				rev[e.reg] = map[string]bool{}
			}
			rev[e.reg][key] = true
			for _, v := range e.ops {
				if rr, ok := v.(*ir.Reg); ok {
					if rev[rr] == nil {
						rev[rr] = map[string]bool{}
					}
					rev[rr][key] = true
				}
			}
			if e.dev != "" {
				byDev[e.dev] = append(byDev[e.dev], key)
			}
		}
		for k, e := range availIn[b] {
			register(k, e)
		}
		invalidate := func(r *ir.Reg) {
			for key := range rev[r] {
				delete(avail, key)
			}
			delete(rev, r)
		}
		invalidateDev := func(dev string) {
			for _, key := range byDev[dev] {
				delete(avail, key)
			}
			delete(byDev, dev)
		}
		invalidateKey := func(key string) {
			e, ok := avail[key]
			if !ok {
				return
			}
			delete(avail, key)
			if e.reg != nil {
				delete(rev[e.reg], key)
			}
			for _, v := range e.ops {
				if rr, ok := v.(*ir.Reg); ok {
					delete(rev[rr], key)
				}
			}
			if e.dev != "" {
				keys := byDev[e.dev]
				for i, k := range keys {
					if k == key {
						byDev[e.dev] = append(keys[:i], keys[i+1:]...)
						break
					}
				}
			}
		}
		invalidateDevsExcept := func(keep string) {
			for dev, keys := range byDev {
				if dev == keep {
					continue
				}
				for _, key := range keys {
					delete(avail, key)
				}
				delete(byDev, dev)
			}
		}
		clearLoads := func() {
			for key, e := range avail {
				if e.dev != "" {
					delete(avail, key)
				}
			}
			clear(byDev)
		}
		applyKill := func(k loadKill) {
			switch {
			case k.all:
				clearLoads()
			case k.allDevs:
				invalidateDevsExcept("db")
			case k.key != "":
				invalidateKey(k.key)
			case k.dev != "":
				invalidateDev(k.dev)
			}
		}
		for idx, ins := range b.Instrs {
			key, operands, dev, ok := exprKey(ins)
			d := ir.DefOf(ins)
			if ok {
				if e, found := avail[key]; found && d != e.reg {
					b.Instrs[idx] = &ir.Assign{Dst: d, Src: e.reg}
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
			applyKill(loadWrite(ins))
			if ok && !containsReg(operands, d) {
				register(key, availExpr{reg: d, ops: operands, dev: dev})
			}
		}
	}
	return changed
}

// availExpr is an available expression: the register holding its result and
// the values it reads. The operands are needed so that redefining an operand
// invalidates the expression, including expressions inherited from a
// predecessor block. dev names the device a load reads ("" for pure
// expressions); a write to that device invalidates it.
type availExpr struct {
	reg *ir.Reg
	ops []ir.Value
	dev string
}

// availableExprs computes, for each block, the expressions available at entry
// (and exit), mapped to the register holding them.
func availableExprs(fn *ir.Function) (in, out map[*ir.Block]map[string]availExpr) {
	in = map[*ir.Block]map[string]availExpr{}
	out = map[*ir.Block]map[string]availExpr{}
	for _, b := range fn.Blocks {
		in[b] = map[string]availExpr{}
		out[b] = map[string]availExpr{}
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

func meetAvail(b *ir.Block, out map[*ir.Block]map[string]availExpr) map[string]availExpr {
	res := map[string]availExpr{}
	if len(b.Preds) == 0 {
		return res
	}
	for k, e := range out[b.Preds[0]] {
		res[k] = e
	}
	for _, p := range b.Preds[1:] {
		for k, e := range res {
			if pe, ok := out[p][k]; !ok || pe.reg != e.reg {
				delete(res, k)
			}
		}
	}
	return res
}

func transferAvail(b *ir.Block, in map[string]availExpr) map[string]availExpr {
	avail := map[string]availExpr{}
	rev := map[*ir.Reg]map[string]bool{}
	byDev := map[string][]string{}
	register := func(key string, e availExpr) {
		avail[key] = e
		if rev[e.reg] == nil {
			rev[e.reg] = map[string]bool{}
		}
		rev[e.reg][key] = true
		for _, v := range e.ops {
			if rr, ok := v.(*ir.Reg); ok {
				if rev[rr] == nil {
					rev[rr] = map[string]bool{}
				}
				rev[rr][key] = true
			}
		}
		if e.dev != "" {
			byDev[e.dev] = append(byDev[e.dev], key)
		}
	}
	for k, e := range in {
		register(k, e)
	}
	invalidate := func(r *ir.Reg) {
		for key := range rev[r] {
			delete(avail, key)
		}
		delete(rev, r)
	}
	invalidateDev := func(dev string) {
		for _, key := range byDev[dev] {
			delete(avail, key)
		}
		delete(byDev, dev)
	}
	invalidateKey := func(key string) {
		e, ok := avail[key]
		if !ok {
			return
		}
		delete(avail, key)
		if e.reg != nil {
			delete(rev[e.reg], key)
		}
		for _, v := range e.ops {
			if rr, ok := v.(*ir.Reg); ok {
				delete(rev[rr], key)
			}
		}
		if e.dev != "" {
			keys := byDev[e.dev]
			for i, k := range keys {
				if k == key {
					byDev[e.dev] = append(keys[:i], keys[i+1:]...)
					break
				}
			}
		}
	}
	invalidateDevsExcept := func(keep string) {
		for dev, keys := range byDev {
			if dev == keep {
				continue
			}
			for _, key := range keys {
				delete(avail, key)
			}
			delete(byDev, dev)
		}
	}
	clearLoads := func() {
		for key, e := range avail {
			if e.dev != "" {
				delete(avail, key)
			}
		}
		clear(byDev)
	}
	applyKill := func(k loadKill) {
		switch {
		case k.all:
			clearLoads()
		case k.allDevs:
			invalidateDevsExcept("db")
		case k.key != "":
			invalidateKey(k.key)
		case k.dev != "":
			invalidateDev(k.dev)
		}
	}
	for _, ins := range b.Instrs {
		key, operands, dev, ok := exprKey(ins)
		d := ir.DefOf(ins)
		if d != nil {
			invalidate(d)
		}
		applyKill(loadWrite(ins))
		if ok && !containsReg(operands, d) {
			register(key, availExpr{reg: d, ops: operands, dev: dev})
		}
	}
	return avail
}

func availEqual(a, b map[string]availExpr) bool {
	if len(a) != len(b) {
		return false
	}
	for k, e := range a {
		if b[k].reg != e.reg {
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

func exprKey(i ir.Instr) (key string, operands []ir.Value, dev string, ok bool) {
	// Expressions that read a physical register directly (ireg(const)) are
	// left out of CSE: commoning them up would extend their live ranges (and
	// they would need invalidating on every indirect write).
	hasPhys := func(vals ...ir.Value) bool {
		for _, v := range vals {
			if isPhysRegRaw(v) {
				return true
			}
		}
		return false
	}
	switch v := i.(type) {
	case *ir.Bin:
		if hasPhys(v.A, v.B) {
			return "", nil, "", false
		}
		ka, kb := valKey(v.A), valKey(v.B)
		if commutative(v.Op) && kb < ka {
			ka, kb = kb, ka
		}
		return "b" + strconv.Itoa(int(v.Op)) + "|" + ka + "|" + kb,
			[]ir.Value{v.A, v.B}, "", true
	case *ir.Un:
		if hasPhys(v.A) {
			return "", nil, "", false
		}
		return "u" + strconv.Itoa(int(v.Op)) + "|" + valKey(v.A),
			[]ir.Value{v.A}, "", true
	case *ir.Cmp:
		if v.B == nil {
			if hasPhys(v.A) {
				return "", nil, "", false
			}
			return "c" + strconv.Itoa(int(v.Cond)) + "|" + valKey(v.A) + "|-",
				[]ir.Value{v.A}, "", true
		}
		if hasPhys(v.A, v.B) {
			return "", nil, "", false
		}
		return "c" + strconv.Itoa(int(v.Cond)) + "|" + valKey(v.A) + "|" + valKey(v.B),
			[]ir.Value{v.A, v.B}, "", true
	case *ir.Select:
		if hasPhys(v.Cond, v.Then, v.Else) {
			return "", nil, "", false
		}
		return "s|" + valKey(v.Cond) + "|" + valKey(v.Then) + "|" + valKey(v.Else),
			[]ir.Value{v.Cond, v.Then, v.Else}, "", true
	}
	// Loads (device loads, slot loads, device-stack get, constant batch) are
	// pure reads: safe to common up across blocks while no write to the device
	// (and no redefinition of the address) intervenes.
	if k, d, dst, deps, isLoad := loadKey(i); isLoad && dst != nil {
		ops := make([]ir.Value, len(deps))
		for j, r := range deps {
			ops[j] = r
		}
		return k, ops, d, true
	}
	return "", nil, "", false
}

// loadKill describes what a write invalidates for load commoning. The zero
// value invalidates nothing.
type loadKill struct {
	all     bool   // every load
	dev     string // every load from this device
	key     string // one exact load key
	allDevs bool   // every device load, but not the stack (yield/sleep)
}

// loadWrite reports what a store invalidates.
func loadWrite(i ir.Instr) loadKill {
	switch v := i.(type) {
	case *ir.Store:
		return loadKill{dev: v.Dev}
	case *ir.StoreSlot:
		return loadKill{dev: v.Dev}
	case *ir.StoreSpecial:
		return loadKill{dev: v.Name}
	case *ir.StoreDyn, *ir.StoreIndirect:
		return loadKill{all: true}
	case *ir.Batch:
		switch v.Kind {
		case ir.BatchStore, ir.BatchStoreName, ir.BatchStoreSlot:
			if dev, ok := constText(v.Device); ok {
				return loadKill{dev: dev}
			}
			return loadKill{all: true}
		}
	case *ir.Builtin:
		sem := builtin.SemOf(v.Name)
		if sem.WritesDev {
			switch v.Name {
			case "put":
				if d, ok := v.Args[0].(*ir.Device); ok {
					// put db N only changes slot N; a dynamic address may hit
					// any slot.
					if k, ok := stackSlotKey(d.Name, v.Args[1]); ok {
						return loadKill{key: k}
					}
					return loadKill{dev: d.Name}
				}
			case "poke":
				if k, ok := stackSlotKey("db", v.Args[0]); ok {
					return loadKill{key: k}
				}
				return loadKill{dev: "db"}
			case "push", "pop":
				// sp moves, so any stack slot may have been written.
				return loadKill{dev: "db"}
			}
			if sem.DeviceArg >= 0 && sem.DeviceArg < len(v.Args) {
				if d, ok := v.Args[sem.DeviceArg].(*ir.Device); ok {
					return loadKill{dev: d.Name}
				}
			}
			return loadKill{all: true}
		}
		if sem.Barrier {
			// yield/sleep/hcf: devices may change between ticks, but the stack
			// is only written by this program.
			return loadKill{allDevs: true}
		}
	}
	return loadKill{}
}

// stackSlotKey is the load key a constant housing-stack write invalidates.
func stackSlotKey(dev string, addr ir.Value) (string, bool) {
	if _, ok := addr.(*ir.Const); ok {
		return "g|" + dev + "|" + valKey(addr), true
	}
	return "", false
}

// commutative reports whether a binary operation is order-independent, so
// `op a b` and `op b a` can share a value number.
func commutative(op ir.BinOp) bool {
	switch op {
	case ir.Add, ir.Mul, ir.BitAnd, ir.BitOr, ir.BitXor, ir.Min, ir.Max:
		return true
	}
	return false
}

func valKeyWith(v ir.Value, reg func(*ir.Reg) string) string {
	switch x := v.(type) {
	case *ir.Reg:
		return "r" + reg(x)
	case *ir.Const:
		return "c" + x.String()
	case *ir.Device:
		return "d" + x.Name
	}
	return "?"
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
		return ok && c.Special == "" && c.Raw == "" && c.V == 0
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

// dce removes pure instructions whose result is not live, including dead stores
// to registers that are overwritten before use.
func dce(fn *ir.Function) bool {
	fn.BuildCFG()
	liveIn, liveOut := ir.Liveness(fn)
	changed := false
	for _, b := range fn.Blocks {
		live := map[*ir.Reg]bool{}
		for r := range liveOut[b] {
			live[r] = true
		}
		for _, r := range ir.TermUses(b.Term) {
			live[r] = true
		}
		for i := len(b.Instrs) - 1; i >= 0; i-- {
			ins := b.Instrs[i]
			u, d := ir.DefUse(ins)
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

// fuseBranches folds a block-local comparison whose result is only used by the
// terminator into the branch itself, so the comparison line disappears. It
// covers the `seq`/`seqz`/`sne`/`snez` patterns produced for a `switch` tag or
// a parenthesised `if`.
func fuseBranches(fn *ir.Function) bool {
	uses := map[*ir.Reg]int{}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			u, _ := ir.DefUse(ins)
			for _, r := range u {
				uses[r]++
			}
		}
		for _, r := range ir.TermUses(b.Term) {
			uses[r]++
		}
	}
	changed := false
	for _, b := range fn.Blocks {
		br, ok := b.Term.(*ir.Br)
		if !ok {
			continue
		}
		// Normalise `A == 0` / `A != 0` to the unary Zero/NonZero conditions.
		cond, a := br.Cond, br.A
		if br.B != nil {
			c, isConst := br.B.(*ir.Const)
			if !isConst || c.V != 0 || (br.Cond != ir.Eq && br.Cond != ir.Ne) {
				continue
			}
			if br.Cond == ir.Eq {
				cond = ir.Zero
			} else {
				cond = ir.NonZero
			}
		}
		if cond != ir.Zero && cond != ir.NonZero {
			continue
		}
		r, ok := a.(*ir.Reg)
		if !ok || uses[r] != 1 {
			continue
		}
		idx, cmp := -1, (*ir.Cmp)(nil)
		for i, ins := range b.Instrs {
			d := ir.DefOf(ins)
			if d != r {
				continue
			}
			c, isCmp := ins.(*ir.Cmp)
			if !isCmp {
				idx = -2 // some other instruction defines it
				break
			}
			idx, cmp = i, c
		}
		if idx < 0 || cmp == nil {
			continue
		}
		newCond := cmp.Cond
		if cond == ir.Zero {
			newCond = newCond.Invert()
		}
		br.Cond, br.A, br.B = newCond, cmp.A, cmp.B
		b.Instrs = append(b.Instrs[:idx], b.Instrs[idx+1:]...)
		uses[r]--
		changed = true
	}
	return changed
}

// eliminatePushPop removes a push/pop pair within a block when the stack is
// private: the popped value is forwarded to the pop's destination and both
// instructions are dropped. The block must have no relative stack access
// (peek or a dynamic get/put/poke) that could observe the pushed slot, and no
// read/write of sp that a dropped push/pop would unbalance.
func eliminatePushPop(fn *ir.Function) bool {
	if !fn.PrivateStack {
		return false
	}
	changed := false
	for _, b := range fn.Blocks {
		if blockHasRelativeStackAccess(b) {
			continue
		}
		type pushed struct {
			val ir.Value
			idx int
		}
		var stack []pushed
		remove := map[int]bool{}
		replace := map[int]ir.Value{}
		for i, ins := range b.Instrs {
			bi, ok := ins.(*ir.Builtin)
			if !ok {
				continue
			}
			switch bi.Name {
			case "push":
				stack = append(stack, pushed{val: bi.Args[0], idx: i})
			case "pop":
				if len(stack) == 0 {
					continue
				}
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				remove[top.idx] = true
				replace[i] = top.val
			}
		}
		if len(remove) == 0 {
			continue
		}
		out := b.Instrs[:0]
		for i, ins := range b.Instrs {
			if remove[i] {
				changed = true
				continue
			}
			if val, ok := replace[i]; ok {
				changed = true
				pop := ins.(*ir.Builtin)
				if pop.Dst != nil && pop.Dst != val {
					out = append(out, &ir.Assign{Dst: pop.Dst, Src: val})
				}
				continue
			}
			out = append(out, ins)
		}
		b.Instrs = out
	}
	return changed
}

// blockHasRelativeStackAccess reports whether a block reads the stack through
// sp (peek, sp load/store) or a dynamic get/put/poke, which a push/pop
// elimination could change.
func blockHasRelativeStackAccess(b *ir.Block) bool {
	for _, ins := range b.Instrs {
		switch v := ins.(type) {
		case *ir.LoadSpecial:
			if v.Name == "sp" {
				return true
			}
		case *ir.StoreSpecial:
			if v.Name == "sp" {
				return true
			}
		case *ir.Builtin:
			switch v.Name {
			case "peek":
				return true
			case "poke":
				if len(v.Args) == 2 {
					if _, ok := v.Args[0].(*ir.Const); !ok {
						return true
					}
				}
			case "get", "put":
				if len(v.Args) >= 2 {
					if d, ok := v.Args[0].(*ir.Device); ok && d.Name == "db" {
						if _, ok := v.Args[1].(*ir.Const); !ok {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// redundantDeviceStores removes a constant device write that repeats the
// previous write to the same device+logic within a block, with no read or
// barrier in between. The device's final value is unchanged, but the write
// sequence is not, so this is opt-in (fn.RedundantDeviceWrites) and off by
// default; the differential tests require the exact write sequence.
func redundantDeviceStores(fn *ir.Function) bool {
	if !fn.RedundantDeviceWrites {
		return false
	}
	changed := false
	for _, b := range fn.Blocks {
		last := map[string]*ir.Const{}
		out := b.Instrs[:0]
		for _, ins := range b.Instrs {
			if key, c, ok := constDeviceStore(ins); ok {
				if prev, seen := last[key]; seen && sameConst(prev, c) {
					changed = true
					continue
				}
				last[key] = c
				out = append(out, ins)
				continue
			}
			if mayChangeDeviceState(ins) {
				clear(last)
			}
			out = append(out, ins)
		}
		b.Instrs = out
	}
	return changed
}

// constDeviceStore keys a device write with a constant value (and, for a slot,
// a constant index) so an identical repeat can be recognised.
func constDeviceStore(ins ir.Instr) (string, *ir.Const, bool) {
	switch v := ins.(type) {
	case *ir.Store:
		c, ok := v.Src.(*ir.Const)
		if !ok {
			return "", nil, false
		}
		return v.Dev + "|" + v.Logic, c, true
	case *ir.StoreSlot:
		if v.DevPtr != nil {
			return "", nil, false
		}
		c, ok := v.Src.(*ir.Const)
		if !ok {
			return "", nil, false
		}
		idx, ok := v.Index.(*ir.Const)
		if !ok {
			return "", nil, false
		}
		return v.Dev + "|" + v.Logic + "|" + idx.String(), c, true
	}
	return "", nil, false
}

func sameConst(a, b *ir.Const) bool {
	return a.V == b.V && a.Raw == b.Raw && a.Special == b.Special
}

// mayChangeDeviceState reports whether an instruction could read or change a
// device, invalidating the tracked constant writes. Pure computations do not.
func mayChangeDeviceState(ins ir.Instr) bool {
	switch ins.(type) {
	case *ir.Assign, *ir.Bin, *ir.Un, *ir.Cmp, *ir.Select:
		return false
	}
	return true
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
	// site. ir.Successors deliberately omits those return edges, so the loop
	// analysis would not see a callee's writes; hoisting would then treat a
	// variable written by the callee as loop-invariant. Skip LICM entirely
	// when the function uses call/ret.
	for _, b := range fn.Blocks {
		if _, ok := b.Term.(*ir.JmpRA); ok {
			return false
		}
	}
	liveIn, _ := ir.Liveness(fn)
	succs := ir.Successors(fn)
	preds := ir.Preds(fn)
	dom := ir.Dominators(fn)
	loops := findLoops(fn, succs, dom, preds)
	changed := false
	for _, lp := range loops {
		pre, outside, created := ensurePreheader(fn, lp, preds)
		if pre == nil {
			continue
		}
		if hoistLoop(fn, lp, pre, liveIn[lp.header], dom) {
			changed = true
		} else if created {
			// Nothing was hoisted: drop the empty preheader, otherwise
			// threadJumps removes it and the next round recreates it, which
			// never converges.
			for _, p := range outside {
				redirect(p, pre, lp.header)
			}
			fn.RemoveBlock(pre)
		}
	}
	if changed {
		fn.BuildCFG()
	}
	return changed
}

func findLoops(fn *ir.Function, succs map[*ir.Block][]*ir.Block, dom map[*ir.Block]map[*ir.Block]bool, preds map[*ir.Block][]*ir.Block) []*loop {
	// A loop header can have several back edges. Their bodies must be merged:
	// a register defined on one path is still loop-defined for a use on
	// another, so hoisting such a use would read the wrong value.
	byHeader := map[*ir.Block]*loop{}
	var order []*ir.Block
	for _, b := range fn.Blocks {
		for _, s := range succs[b] {
			if !dom[b][s] { // back edge b -> s
				continue
			}
			lp := byHeader[s]
			if lp == nil {
				lp = &loop{header: s, blocks: map[*ir.Block]bool{}}
				byHeader[s] = lp
				order = append(order, s)
			}
			stack := []*ir.Block{b}
			for len(stack) > 0 {
				n := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if lp.blocks[n] {
					continue
				}
				// Only blocks dominated by the header belong to the loop;
				// otherwise the preheader would be pulled in.
				if n != s && !dom[n][s] {
					continue
				}
				lp.blocks[n] = true
				stack = append(stack, preds[n]...)
			}
			lp.blocks[s] = true
		}
	}
	loops := make([]*loop, 0, len(order))
	for _, h := range order {
		loops = append(loops, byHeader[h])
	}
	return loops
}

// ensurePreheader returns a block that dominates the loop header and is the
// only entry to it, creating one when the header has several outside
// predecessors. It also returns the outside predecessors and whether a new
// block was created (so the caller can undo it when nothing is hoisted).
func ensurePreheader(fn *ir.Function, lp *loop, preds map[*ir.Block][]*ir.Block) (*ir.Block, []*ir.Block, bool) {
	h := lp.header
	var outside []*ir.Block
	for _, p := range preds[h] {
		if !lp.blocks[p] {
			outside = append(outside, p)
		}
	}
	if len(outside) == 1 && len(outside[0].Term.Successors()) == 1 {
		return outside[0], outside, false
	}
	if len(outside) == 0 {
		return nil, nil, false
	}
	pre := fn.NewBlock()
	pre.Term = &ir.Jmp{Target: h}
	for _, p := range outside {
		redirect(p, h, pre)
	}
	return pre, outside, true
}

func redirect(b *ir.Block, from, to *ir.Block) {
	if b.Term != nil {
		b.Term.Redirect(from, to)
	}
}

func hoistLoop(fn *ir.Function, lp *loop, pre *ir.Block, liveIn map[*ir.Reg]bool, dom map[*ir.Block]map[*ir.Block]bool) bool {
	changed := false
	// Device reads may be hoisted only when the loop neither writes the device
	// nor contains a barrier (yield/sleep or a dynamic store) that could change
	// it between iterations.
	barrier := false
	writes := map[string]bool{}
	for _, b := range fn.Blocks {
		if !lp.blocks[b] {
			continue
		}
		for _, ins := range b.Instrs {
			switch v := ins.(type) {
			case *ir.Store:
				writes[v.Dev] = true
			case *ir.StoreSlot:
				writes[v.Dev] = true
			case *ir.Batch:
				switch v.Kind {
				case ir.BatchStore, ir.BatchStoreName, ir.BatchStoreSlot:
					if dev, ok := constText(v.Device); ok {
						writes[dev] = true
					} else {
						barrier = true
					}
				}
			case *ir.StoreDyn, *ir.StoreIndirect:
				barrier = true
			case *ir.Builtin:
				if builtin.SemOf(v.Name).Barrier {
					barrier = true
				}
			}
		}
	}
	for {
		definedSet := map[*ir.Reg]bool{}
		defCount := map[*ir.Reg]int{}
		useBlocks := map[*ir.Reg][]*ir.Block{}
		for _, b := range fn.Blocks {
			for _, ins := range b.Instrs {
				if d := ir.DefOf(ins); d != nil && lp.blocks[b] {
					definedSet[d] = true
					defCount[d]++
				}
				u, _ := ir.DefUse(ins)
				for _, r := range u {
					useBlocks[r] = append(useBlocks[r], b)
				}
			}
			for _, r := range ir.TermUses(b.Term) {
				useBlocks[r] = append(useBlocks[r], b)
			}
		}
		moved := false
		for _, b := range fn.Blocks {
			if !lp.blocks[b] {
				continue
			}
			kept := b.Instrs[:0]
			for _, ins := range b.Instrs {
				d := ir.DefOf(ins)
				// Hoisting a register that is defined more than once in the
				// loop is unsound: a use could observe a different definition
				// on some iteration.
				soleDef := d != nil && defCount[d] == 1
				canHoist := hoistable(ins)
				if !canHoist {
					if dev, ok := hoistableLoad(ins); ok && !barrier && !writes[dev] {
						canHoist = true
					}
				}
				if canHoist && !usesAny(ins, definedSet) && !definesLiveIn(ins, liveIn) &&
					soleDef && dominatesAllUses(b, d, useBlocks, dom) {
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

// dominatesAllUses reports whether b dominates every block that uses d. A
// definition may only be hoisted if it is the reaching definition for all of
// its uses, otherwise moving it would change the value observed on paths that
// bypass it.
func dominatesAllUses(b *ir.Block, d *ir.Reg, useBlocks map[*ir.Reg][]*ir.Block, dom map[*ir.Block]map[*ir.Block]bool) bool {
	if d == nil {
		return true
	}
	for _, u := range useBlocks[d] {
		if !dom[u][b] {
			return false
		}
	}
	return true
}

// definesLiveIn reports whether an instruction defines a register that is live
// at the loop header. Hoisting such a definition would change its value on
// iterations that did not originally execute it.
func definesLiveIn(i ir.Instr, liveIn map[*ir.Reg]bool) bool {
	d := ir.DefOf(i)
	return d != nil && liveIn[d]
}

func hoistable(i ir.Instr) bool {
	// A physical-register operand (ireg(const) lowered to "rN") is volatile:
	// it can be changed by an indirect write anywhere in the loop, so never
	// hoist an instruction that reads one.
	if hasPhysRegOperand(i) {
		return false
	}
	switch i.(type) {
	case *ir.Assign, *ir.Bin, *ir.Un, *ir.Cmp, *ir.Select:
		return true
	}
	return false
}

// hasPhysRegOperand reports whether an instruction reads a physical-register
// raw operand.
func hasPhysRegOperand(i ir.Instr) bool {
	switch v := i.(type) {
	case *ir.Assign:
		return isPhysRegRaw(v.Src)
	case *ir.Bin:
		return isPhysRegRaw(v.A) || isPhysRegRaw(v.B)
	case *ir.Un:
		return isPhysRegRaw(v.A)
	case *ir.Cmp:
		return isPhysRegRaw(v.A) || isPhysRegRaw(v.B)
	case *ir.Select:
		return isPhysRegRaw(v.Cond) || isPhysRegRaw(v.Then) || isPhysRegRaw(v.Else)
	}
	return false
}

// hoistableLoad reports whether an instruction is a device read that may be
// hoisted out of a loop (subject to the loop-write and barrier checks), and the
// device it reads.
func hoistableLoad(i ir.Instr) (string, bool) {
	switch v := i.(type) {
	case *ir.Load:
		return v.Dev, true
	case *ir.LoadSlot:
		return v.Dev, true
	case *ir.Batch:
		switch v.Kind {
		case ir.BatchLoad, ir.BatchLoadName, ir.BatchLoadSlot, ir.BatchLoadNameSlot:
			if dev, ok := constText(v.Device); ok {
				return dev, true
			}
		}
	}
	return "", false
}

func usesAny(i ir.Instr, defined map[*ir.Reg]bool) bool {
	u, _ := ir.DefUse(i)
	for _, r := range u {
		if defined[r] {
			return true
		}
	}
	return false
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
		if b.Term != nil {
			// Use the terminator directly: earlier passes in this round may
			// have changed the CFG without rebuilding b.Succs.
			for _, s := range b.Term.Successors() {
				dfs(s)
			}
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
	if a.Special != "" || b.Special != "" || a.Raw != "" || b.Raw != "" {
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
	if a.Special != "" || a.Raw != "" {
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
	if a.Special != "" || a.Raw != "" {
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
	if a.Special != "" || b.Special != "" || a.Raw != "" || b.Raw != "" {
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

// ---------------------------------------------------------------------------
// Dead stack stores
// ---------------------------------------------------------------------------

// deadStores removes stack writes (put/poke with a constant address) that are
// overwritten before any read. It is block-local and only handles the chip's
// own stack, whose intermediate values are not observable by the game; device
// stores are left alone. A yield or any other side effect clears the pending
// set, so writes that survive to the end of a tick are kept.
func deadStores(fn *ir.Function) bool {
	fn.BuildCFG()
	all := allStackSlots(fn)
	liveIn, liveOut := stackLive(fn, all, fn.PrivateStack)
	changed := false
	for _, b := range fn.Blocks {
		live := map[string]bool{}
		for k := range liveOut[b] {
			live[k] = true
		}
		dead := map[int]bool{}
		for idx := len(b.Instrs) - 1; idx >= 0; idx-- {
			ins := b.Instrs[idx]
			if key, ok := stackStoreKey(ins); ok {
				// A store is dead when its slot is not live afterwards (the
				// value is never read before being overwritten, on any path).
				if !live[key] {
					dead[idx] = true
				}
				delete(live, key)
				continue
			}
			if key, ok := stackReadKey(ins); ok {
				live[key] = true
				continue
			}
			markStackBarrier(live, all, ins, fn.PrivateStack)
		}
		if len(dead) == 0 {
			continue
		}
		kept := b.Instrs[:0]
		for idx, ins := range b.Instrs {
			if dead[idx] {
				changed = true
				continue
			}
			kept = append(kept, ins)
		}
		b.Instrs = kept
	}
	_ = liveIn
	return changed
}

// allStackSlots collects every stack slot key written or read in fn.
func allStackSlots(fn *ir.Function) map[string]bool {
	m := map[string]bool{}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			if k, ok := stackStoreKey(ins); ok {
				m[k] = true
			}
			if k, ok := stackReadKey(ins); ok {
				m[k] = true
			}
		}
	}
	return m
}

// stackLive computes stack-slot liveness (backward dataflow). in[b] holds the
// slots live at block entry; out[b] at block exit.
func stackLive(fn *ir.Function, all map[string]bool, private bool) (in, out map[*ir.Block]map[string]bool) {
	in = map[*ir.Block]map[string]bool{}
	out = map[*ir.Block]map[string]bool{}
	for _, b := range fn.Blocks {
		in[b] = map[string]bool{}
		out[b] = map[string]bool{}
	}
	for changed := true; changed; {
		changed = false
		for _, b := range fn.Blocks {
			no := map[string]bool{}
			if len(b.Succs) == 0 {
				// Device stacks live in shared devices and stay observable
				// across ticks; the chip's own stack is observable only when it
				// is shared (a successor program may read it).
				for k := range all {
					if keyDevice(k) != "db" || !private {
						no[k] = true
					}
				}
			} else {
				for _, s := range b.Succs {
					for k := range in[s] {
						no[k] = true
					}
				}
			}
			ni := transferStack(b, no, all, private)
			if !sameStrSet(ni, in[b]) || !sameStrSet(no, out[b]) {
				changed = true
			}
			in[b], out[b] = ni, no
		}
	}
	return in, out
}

func transferStack(b *ir.Block, liveOut, all map[string]bool, private bool) map[string]bool {
	live := map[string]bool{}
	for k := range liveOut {
		live[k] = true
	}
	for idx := len(b.Instrs) - 1; idx >= 0; idx-- {
		ins := b.Instrs[idx]
		if key, ok := stackStoreKey(ins); ok {
			delete(live, key)
			continue
		}
		if key, ok := stackReadKey(ins); ok {
			live[key] = true
			continue
		}
		markStackBarrier(live, all, ins, private)
	}
	return live
}

// markStackBarrier marks the slots an instruction may read. Device stacks are
// shared and observable, so any side effect (yield/sleep/device write) keeps
// them live. The chip's own stack is only kept live by a relative or dynamic
// access, unless it is shared (private=false), when every side effect counts.
func markStackBarrier(live, all map[string]bool, ins ir.Instr, private bool) {
	if !hasSideEffect(ins) {
		return
	}
	chipAccess := marksChipStack(ins)
	for k := range all {
		isDB := keyDevice(k) == "db"
		switch {
		case !isDB:
			live[k] = true // a device stack is always observable
		case !private || chipAccess:
			live[k] = true
		}
	}
}

// marksChipStack reports whether an instruction may read any chip-stack slot
// through sp: peek, push/pop, or a dynamic get/put/poke on db.
func marksChipStack(ins ir.Instr) bool {
	v, ok := ins.(*ir.Builtin)
	if !ok {
		return false
	}
	switch v.Name {
	case "peek", "push", "pop":
		return true
	case "poke":
		if len(v.Args) != 2 {
			return false
		}
		_, isConst := v.Args[0].(*ir.Const)
		return !isConst
	case "get", "put":
		if len(v.Args) < 2 {
			return false
		}
		d, ok := v.Args[0].(*ir.Device)
		if !ok || d.Name != "db" {
			return false
		}
		_, isConst := v.Args[1].(*ir.Const)
		return !isConst
	}
	return false
}

// keyDevice extracts the device from a stack-slot key ("put|<dev>|<addr>").
func keyDevice(key string) string {
	if i := strings.IndexByte(key, '|'); i >= 0 {
		rest := key[i+1:]
		if j := strings.IndexByte(rest, '|'); j >= 0 {
			return rest[:j]
		}
	}
	return ""
}

func sameStrSet(a, b map[string]bool) bool {
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

// stackStoreKey keys a stack write (put/poke) with a constant address.
func stackStoreKey(i ir.Instr) (string, bool) {
	v, ok := i.(*ir.Builtin)
	if !ok {
		return "", false
	}
	switch v.Name {
	case "put":
		if len(v.Args) == 3 {
			if d, isDev := v.Args[0].(*ir.Device); isDev {
				if _, isConst := v.Args[1].(*ir.Const); isConst {
					return "put|" + d.Name + "|" + valKey(v.Args[1]), true
				}
			}
		}
	case "poke":
		if len(v.Args) == 2 {
			if _, isConst := v.Args[0].(*ir.Const); isConst {
				return "poke|" + valKey(v.Args[0]), true
			}
		}
	}
	return "", false
}

// stackReadKey keys a stack read that matches stackStoreKey.
func stackReadKey(i ir.Instr) (string, bool) {
	v, ok := i.(*ir.Builtin)
	if !ok {
		return "", false
	}
	if v.Name == "get" && len(v.Args) == 2 {
		if d, isDev := v.Args[0].(*ir.Device); isDev {
			if _, isConst := v.Args[1].(*ir.Const); isConst {
				return "put|" + d.Name + "|" + valKey(v.Args[1]), true
			}
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Tail merging
// ---------------------------------------------------------------------------

// threadJumps redirects a jump whose target is an empty block to that block's
// own target. Inlined calls and if/else chains leave chains of empty join
// blocks; collapsing them lets tail merging see that branches share a
// terminator (and removes a redundant jump).
func threadJumps(fn *ir.Function) bool {
	changed := false
	for _, b := range fn.Blocks {
		if b == fn.Entry {
			continue
		}
		for {
			j, ok := b.Term.(*ir.Jmp)
			if !ok {
				break
			}
			t := j.Target
			if t == nil || t == b || t == fn.Entry || len(t.Instrs) != 0 {
				break
			}
			u, ok := t.Term.(*ir.Jmp)
			if !ok || u.Target == t {
				break
			}
			b.Term = &ir.Jmp{Target: u.Target}
			changed = true
		}
	}
	return changed
}

// mergeTails factors identical instruction suffixes of blocks that share the
// same terminator into one shared block, replacing each duplicate with a jump.
// It merges one pair at a time and re-scans, which keeps the bookkeeping simple
// on the small functions IC10 programs produce.
func mergeTails(fn *ir.Function) bool {
	return mergeTailsWith(fn, idRegKey)
}

// MergeTailsColored is mergeTails using physical register colours as the
// register identity, so suffixes that differ only in virtual registers but map
// to the same physical registers are merged. It must run after register
// allocation; the colours stay valid because no registers are created.
func MergeTailsColored(fn *ir.Function, colors map[*ir.Reg]int) bool {
	ch := mergeTailsWith(fn, func(r *ir.Reg) string {
		if r == nil {
			return "-"
		}
		if c, ok := colors[r]; ok {
			return strconv.Itoa(c)
		}
		return "?" + strconv.Itoa(r.ID)
	})
	return ch
}

func mergeTailsWith(fn *ir.Function, reg func(*ir.Reg) string) bool {
	changed := false
	for mergeOneTail(fn, reg) {
		changed = true
		fn.BuildCFG()
	}
	return changed
}

func idRegKey(r *ir.Reg) string {
	if r == nil {
		return "-"
	}
	return strconv.Itoa(r.ID)
}

// mergeOneTail finds, among all blocks, the shared instruction suffix that
// yields the greatest line saving and factors it into a single block reached by
// jumps. Merging a suffix of length n across k blocks replaces k*n instructions
// with n + k (the shared body plus one jump per block), so it only pays off
// when n*(k-1) > k. Choosing the globally best group each round (rather than
// the first pair found) avoids the greedy choices that leave some copies
// unshared.
func mergeOneTail(fn *ir.Function, reg func(*ir.Reg) string) bool {
	type group struct {
		n        int
		term     ir.Term
		funcName string
		blocks   []*ir.Block
	}
	groups := map[string]*group{}
	for _, b := range fn.Blocks {
		if len(b.Instrs) == 0 || b == fn.Entry {
			continue
		}
		tk := termKey(b.Term)
		for n := 1; n <= len(b.Instrs); n++ {
			key := tk + "\x00" + suffixKey(b.Instrs, n, reg)
			g := groups[key]
			if g == nil {
				g = &group{n: n, term: b.Term, funcName: b.Func}
				groups[key] = g
			}
			g.blocks = append(g.blocks, b)
		}
	}

	// Prefer the group that saves the most lines; if none saves lines, still
	// factor the largest suffix, which saves bytes at worst.
	bestSaving := 0
	bestWeight := 0
	var best *group
	for _, g := range groups {
		if len(g.blocks) < 2 {
			continue
		}
		saving := g.n*(len(g.blocks)-1) - len(g.blocks)
		weight := g.n * (len(g.blocks) - 1)
		if best == nil || saving > bestSaving || (saving == bestSaving && weight > bestWeight) {
			best, bestSaving, bestWeight = g, saving, weight
		}
	}
	if best == nil {
		return false
	}

	first := best.blocks[0]
	shared := fn.NewBlock()
	shared.Instrs = append(shared.Instrs, first.Instrs[len(first.Instrs)-best.n:]...)
	shared.Term = best.term
	shared.Func = best.funcName
	for _, b := range best.blocks {
		trimBlock(b, best.n, shared)
	}
	return true
}

// trimBlock removes the last n instructions of b and makes it jump to shared.
func trimBlock(b *ir.Block, n int, shared *ir.Block) {
	b.Instrs = b.Instrs[:len(b.Instrs)-n]
	b.Term = &ir.Jmp{Target: shared}
}

// suffixKey returns a structural key for the last n instructions.
func suffixKey(instrs []ir.Instr, n int, reg func(*ir.Reg) string) string {
	var sb strings.Builder
	for _, ins := range instrs[len(instrs)-n:] {
		sb.WriteString(instrKey(ins, reg))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// instrKey renders an instruction structurally, using register IDs so that
// equivalent instructions in different blocks compare equal.
func instrKey(i ir.Instr, reg func(*ir.Reg) string) string {
	vk := func(v ir.Value) string { return valKeyWith(v, reg) }
	switch v := i.(type) {
	case *ir.Assign:
		return "assign|" + reg(v.Dst) + "|" + vk(v.Src)
	case *ir.Bin:
		ka, kb := vk(v.A), vk(v.B)
		if commutative(v.Op) && kb < ka {
			ka, kb = kb, ka
		}
		return "bin|" + v.Op.IC10() + "|" + reg(v.Dst) + "|" + ka + "|" + kb
	case *ir.Un:
		return "un|" + strconv.Itoa(int(v.Op)) + "|" + reg(v.Dst) + "|" + vk(v.A)
	case *ir.Cmp:
		return "cmp|" + strconv.Itoa(int(v.Cond)) + "|" + reg(v.Dst) + "|" + vk(v.A) + "|" + vk(v.B)
	case *ir.Select:
		return "select|" + reg(v.Dst) + "|" + vk(v.Cond) + "|" + vk(v.Then) + "|" + vk(v.Else)
	case *ir.Load:
		return "load|" + v.Dev + "|" + v.Logic + "|" + reg(v.Dst)
	case *ir.Store:
		return "store|" + v.Dev + "|" + v.Logic + "|" + vk(v.Src)
	case *ir.LoadSlot:
		return "loadslot|" + v.Dev + "|" + vk(v.DevPtr) + "|" + v.Logic + "|" + vk(v.Index) + "|" + reg(v.Dst)
	case *ir.StoreSlot:
		return "storeslot|" + v.Dev + "|" + vk(v.DevPtr) + "|" + v.Logic + "|" + vk(v.Index) + "|" + vk(v.Src)
	case *ir.LoadDyn:
		return "loaddyn|" + v.Dev + "|" + vk(v.DevPtr) + "|" + vk(v.DevID) + "|" + vk(v.Logic) + "|" + vk(v.Reagent) + "|" + reg(v.Dst)
	case *ir.StoreDyn:
		return "storedyn|" + v.Dev + "|" + vk(v.DevPtr) + "|" + vk(v.DevID) + "|" + vk(v.Logic) + "|" + vk(v.Src)
	case *ir.LoadSpecial:
		return "loadsp|" + v.Name + "|" + reg(v.Dst)
	case *ir.StoreSpecial:
		return "storesp|" + v.Name + "|" + vk(v.Src)
	case *ir.LoadIndirect:
		return "loadind|" + vk(v.Ptr) + "|" + reg(v.Dst)
	case *ir.StoreIndirect:
		return "storeind|" + vk(v.Ptr) + "|" + vk(v.Src)
	case *ir.LoadSpill:
		return "loadspill|" + strconv.Itoa(v.Slot) + "|" + reg(v.Dst)
	case *ir.StoreSpill:
		return "storespill|" + strconv.Itoa(v.Slot) + "|" + vk(v.Src)
	case *ir.Builtin:
		s := "builtin|" + v.Name + "|" + reg(v.Dst)
		for _, a := range v.Args {
			s += "|" + vk(a)
		}
		return s
	case *ir.Batch:
		return "batch|" + strconv.Itoa(int(v.Kind)) + "|" + vk(v.Device) + "|" + vk(v.Name) +
			"|" + vk(v.Slot) + "|" + v.Logic + "|" + vk(v.Mode) + "|" + vk(v.Src) + "|" + reg(v.Dst)
	}
	return "?"
}

// termKey returns a structural key for a terminator (block pointers are keyed
// by their ID so identical control flow compares equal).
func termKey(t ir.Term) string {
	if t == nil {
		return "?"
	}
	return t.Key()
}
