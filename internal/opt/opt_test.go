package opt

import (
	"testing"

	"ic10go/internal/ir"
)

func TestGlobalCSEAcrossBlocks(t *testing.T) {
	b := ir.NewBuilder("f")
	a := b.NewReg("a")
	c := b.NewReg("b")
	b.Emit(&ir.Load{Dst: a, Dev: "d0", Logic: "Temperature"})
	b.Emit(&ir.Load{Dst: c, Dev: "d1", Logic: "Temperature"})
	t1 := b.NewReg("t1")
	b.Emit(&ir.Bin{Op: ir.Add, Dst: t1, A: a, B: c})
	b.Emit(&ir.Builtin{Name: "sleep", Args: []ir.Value{t1}})

	thenB := b.NewBlock()
	endB := b.NewBlock()
	b.SetTerm(&ir.Br{Cond: ir.NonZero, A: a, Then: thenB, Else: endB})

	b.SetBlock(thenB)
	t2 := b.NewReg("t2")
	b.Emit(&ir.Bin{Op: ir.Add, Dst: t2, A: a, B: c})
	b.Emit(&ir.Builtin{Name: "sleep", Args: []ir.Value{t2}})
	b.SetTerm(&ir.Jmp{Target: endB})

	b.SetBlock(endB)
	b.SetTerm(&ir.Ret{})

	fn := b.Fn()
	if !globalCSE(fn) {
		t.Fatal("globalCSE made no change")
	}
	ins := fn.Blocks[1].Instrs[0]
	as, ok := ins.(*ir.Assign)
	if !ok {
		t.Fatalf("then block first instr = %T, want *ir.Assign", ins)
	}
	if as.Dst != t2 || as.Src != ir.Value(t1) {
		t.Errorf("reuse = %v = %v, want t2 = t1", as.Dst, as.Src)
	}
}

func TestCSEInvalidatedOnRedefinition(t *testing.T) {
	b := ir.NewBuilder("f")
	a := b.NewReg("a")
	c := b.NewReg("b")
	b.Emit(&ir.Load{Dst: a, Dev: "d0", Logic: "Temperature"})
	b.Emit(&ir.Load{Dst: c, Dev: "d1", Logic: "Temperature"})
	t1 := b.NewReg("t1")
	b.Emit(&ir.Bin{Op: ir.Add, Dst: t1, A: a, B: c})
	b.Emit(&ir.Builtin{Name: "sleep", Args: []ir.Value{t1}})
	// Redefine t1, then recompute a+b: it must not reuse the stale t1.
	b.Emit(&ir.Assign{Dst: t1, Src: &ir.Const{V: 5}})
	b.Emit(&ir.Builtin{Name: "sleep", Args: []ir.Value{t1}})
	t2 := b.NewReg("t2")
	b.Emit(&ir.Bin{Op: ir.Add, Dst: t2, A: a, B: c})
	b.Emit(&ir.Builtin{Name: "sleep", Args: []ir.Value{t2}})
	b.SetTerm(&ir.Ret{})

	globalCSE(b.Fn())
	var found ir.Instr
	for _, ins := range b.Fn().Blocks[0].Instrs {
		if d := ir.DefOf(ins); d == t2 {
			found = ins
		}
	}
	if _, ok := found.(*ir.Bin); !ok {
		t.Errorf("stale CSE: t2 defined by %T, want *ir.Bin", found)
	}
}

// TestGlobalCSESelfLoopRedefinedOperand checks that an expression is not reused
// across a loop back edge when one of its operands is redefined each iteration
// (e.g. a device load feeding an arithmetic expression in a label/goto loop).
func TestGlobalCSESelfLoopRedefinedOperand(t *testing.T) {
	b := ir.NewBuilder("f")
	a := b.NewReg("a")
	c := b.NewReg("b")
	b.Emit(&ir.Load{Dst: a, Dev: "d1", Logic: "PositionX"})
	b.Emit(&ir.Bin{Op: ir.Sub, Dst: c, A: a, B: &ir.Const{V: 1}})
	b.Emit(&ir.Store{Dev: "d0", Logic: "Horizontal", Src: c})
	b.SetTerm(&ir.Goto{Target: b.Fn().Blocks[0]})

	if globalCSE(b.Fn()) {
		t.Error("globalCSE reused an expression whose operand was redefined in a loop")
	}
}

func TestRedundantGetCSE(t *testing.T) {
	b := ir.NewBuilder("f")
	idx := b.NewReg("idx")
	b.Emit(&ir.Load{Dst: idx, Dev: "d0", Logic: "Setting"})
	a := b.NewReg("a")
	b.Emit(&ir.Builtin{Name: "get", Dst: a, Args: []ir.Value{&ir.Device{Name: "db"}, idx}})
	c := b.NewReg("c")
	b.Emit(&ir.Builtin{Name: "get", Dst: c, Args: []ir.Value{&ir.Device{Name: "db"}, idx}})
	b.Emit(&ir.Builtin{Name: "sleep", Args: []ir.Value{a, c}})
	b.SetTerm(&ir.Ret{})
	fn := b.Fn()
	if !redundantLoads(fn) {
		t.Fatal("redundantLoads made no change")
	}
	if _, ok := fn.Blocks[0].Instrs[2].(*ir.Assign); !ok {
		t.Fatalf("second get = %T, want *ir.Assign", fn.Blocks[0].Instrs[2])
	}
}

func TestStoreToLoadForwarding(t *testing.T) {
	b := ir.NewBuilder("f")
	idx := b.NewReg("idx")
	b.Emit(&ir.Load{Dst: idx, Dev: "d0", Logic: "Setting"})
	a := b.NewReg("a")
	b.Emit(&ir.Builtin{Name: "get", Dst: a, Args: []ir.Value{&ir.Device{Name: "db"}, idx}})
	b.Emit(&ir.Builtin{Name: "put", Args: []ir.Value{&ir.Device{Name: "db"}, idx, &ir.Const{V: 99}}})
	c := b.NewReg("c")
	b.Emit(&ir.Builtin{Name: "get", Dst: c, Args: []ir.Value{&ir.Device{Name: "db"}, idx}})
	b.Emit(&ir.Builtin{Name: "sleep", Args: []ir.Value{a, c}})
	b.SetTerm(&ir.Ret{})
	fn := b.Fn()
	if !redundantLoads(fn) {
		t.Fatal("expected store-to-load forwarding")
	}
	as, ok := fn.Blocks[0].Instrs[3].(*ir.Assign)
	if !ok {
		t.Fatalf("second get = %T, want *ir.Assign", fn.Blocks[0].Instrs[3])
	}
	if _, isConst := as.Src.(*ir.Const); !isConst {
		t.Fatalf("forwarded value = %T, want *ir.Const (the stored value)", as.Src)
	}
}

func TestRedundantGetInvalidatedByIndexRedef(t *testing.T) {
	b := ir.NewBuilder("f")
	idx := b.NewReg("idx")
	b.Emit(&ir.Load{Dst: idx, Dev: "d0", Logic: "Setting"})
	a := b.NewReg("a")
	b.Emit(&ir.Builtin{Name: "get", Dst: a, Args: []ir.Value{&ir.Device{Name: "db"}, idx}})
	b.Emit(&ir.Assign{Dst: idx, Src: &ir.Const{V: 3}})
	c := b.NewReg("c")
	b.Emit(&ir.Builtin{Name: "get", Dst: c, Args: []ir.Value{&ir.Device{Name: "db"}, idx}})
	b.Emit(&ir.Builtin{Name: "sleep", Args: []ir.Value{a, c}})
	b.SetTerm(&ir.Ret{})
	if redundantLoads(b.Fn()) {
		t.Fatal("redundantLoads merged a get across an index redefinition")
	}
}

func TestGlobalCSELoadAcrossBlocks(t *testing.T) {
	b := ir.NewBuilder("f")
	a := b.NewReg("a")
	b.Emit(&ir.Load{Dst: a, Dev: "d1", Logic: "Temperature"})
	thenB := b.NewBlock()
	endB := b.NewBlock()
	b.SetTerm(&ir.Br{Cond: ir.NonZero, A: a, Then: thenB, Else: endB})

	b.SetBlock(thenB)
	a2 := b.NewReg("a2")
	b.Emit(&ir.Load{Dst: a2, Dev: "d1", Logic: "Temperature"})
	b.Emit(&ir.Builtin{Name: "sleep", Args: []ir.Value{a2}})
	b.SetTerm(&ir.Jmp{Target: endB})

	b.SetBlock(endB)
	b.SetTerm(&ir.Ret{})

	fn := b.Fn()
	if !globalCSE(fn) {
		t.Fatal("globalCSE did not CSE a load available from a dominating block")
	}
	if _, ok := fn.Blocks[1].Instrs[0].(*ir.Assign); !ok {
		t.Fatalf("load in dominated block = %T, want *ir.Assign", fn.Blocks[1].Instrs[0])
	}
}

func TestGlobalCSELoadInvalidatedByWrite(t *testing.T) {
	b := ir.NewBuilder("f")
	a := b.NewReg("a")
	b.Emit(&ir.Load{Dst: a, Dev: "d1", Logic: "Temperature"})
	b.Emit(&ir.Store{Dev: "d1", Logic: "On", Src: &ir.Const{V: 1}})
	thenB := b.NewBlock()
	endB := b.NewBlock()
	b.SetTerm(&ir.Br{Cond: ir.NonZero, A: a, Then: thenB, Else: endB})

	b.SetBlock(thenB)
	a2 := b.NewReg("a2")
	b.Emit(&ir.Load{Dst: a2, Dev: "d1", Logic: "Temperature"})
	b.Emit(&ir.Builtin{Name: "sleep", Args: []ir.Value{a2}})
	b.SetTerm(&ir.Jmp{Target: endB})

	b.SetBlock(endB)
	b.SetTerm(&ir.Ret{})

	if globalCSE(b.Fn()) {
		t.Fatal("globalCSE reused a load across a write to the same device")
	}
}

func TestGlobalCSELoadInvalidatedByYield(t *testing.T) {
	b := ir.NewBuilder("f")
	a := b.NewReg("a")
	b.Emit(&ir.Load{Dst: a, Dev: "d1", Logic: "Temperature"})
	b.Emit(&ir.Builtin{Name: "yield"})
	thenB := b.NewBlock()
	endB := b.NewBlock()
	b.SetTerm(&ir.Br{Cond: ir.NonZero, A: a, Then: thenB, Else: endB})

	b.SetBlock(thenB)
	a2 := b.NewReg("a2")
	b.Emit(&ir.Load{Dst: a2, Dev: "d1", Logic: "Temperature"})
	b.Emit(&ir.Builtin{Name: "sleep", Args: []ir.Value{a2}})
	b.SetTerm(&ir.Jmp{Target: endB})

	b.SetBlock(endB)
	b.SetTerm(&ir.Ret{})

	if globalCSE(b.Fn()) {
		t.Fatal("globalCSE reused a load across a yield")
	}
}

func TestStackGetSurvivesOtherSlotPut(t *testing.T) {
	b := ir.NewBuilder("f")
	a := b.NewReg("a")
	b.Emit(&ir.Builtin{Name: "get", Dst: a, Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 5}}})
	b.Emit(&ir.Builtin{Name: "put", Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 6}, &ir.Const{V: 9}}})
	c := b.NewReg("c")
	b.Emit(&ir.Builtin{Name: "get", Dst: c, Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 5}}})
	b.Emit(&ir.Builtin{Name: "sleep", Args: []ir.Value{a, c}})
	b.SetTerm(&ir.Ret{})
	if !redundantLoads(b.Fn()) {
		t.Fatal("a write to another slot must not invalidate the get")
	}
	if _, ok := b.Fn().Blocks[0].Instrs[2].(*ir.Assign); !ok {
		t.Fatalf("second get = %T, want *ir.Assign", b.Fn().Blocks[0].Instrs[2])
	}
}

func TestStackGetInvalidatedBySameSlotPut(t *testing.T) {
	b := ir.NewBuilder("f")
	a := b.NewReg("a")
	b.Emit(&ir.Builtin{Name: "get", Dst: a, Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 5}}})
	b.Emit(&ir.Builtin{Name: "put", Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 5}, &ir.Const{V: 9}}})
	c := b.NewReg("c")
	b.Emit(&ir.Builtin{Name: "get", Dst: c, Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 5}}})
	b.Emit(&ir.Builtin{Name: "sleep", Args: []ir.Value{a, c}})
	b.SetTerm(&ir.Ret{})
	if !redundantLoads(b.Fn()) {
		t.Fatal("expected store-to-load forwarding of the same slot")
	}
	as, ok := b.Fn().Blocks[0].Instrs[2].(*ir.Assign)
	if !ok {
		t.Fatalf("second get = %T, want *ir.Assign", b.Fn().Blocks[0].Instrs[2])
	}
	if _, isConst := as.Src.(*ir.Const); !isConst {
		t.Fatalf("forwarded value = %T, want the stored constant", as.Src)
	}
}

func TestGlobalCSEStackLoadSurvivesYield(t *testing.T) {
	b := ir.NewBuilder("f")
	a := b.NewReg("a")
	b.Emit(&ir.Builtin{Name: "get", Dst: a, Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 5}}})
	b.Emit(&ir.Builtin{Name: "yield"})
	thenB := b.NewBlock()
	endB := b.NewBlock()
	b.SetTerm(&ir.Br{Cond: ir.NonZero, A: a, Then: thenB, Else: endB})

	b.SetBlock(thenB)
	a2 := b.NewReg("a2")
	b.Emit(&ir.Builtin{Name: "get", Dst: a2, Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 5}}})
	b.Emit(&ir.Builtin{Name: "sleep", Args: []ir.Value{a2}})
	b.SetTerm(&ir.Jmp{Target: endB})

	b.SetBlock(endB)
	b.SetTerm(&ir.Ret{})

	if !globalCSE(b.Fn()) {
		t.Fatal("a stack load only this program writes survives a yield")
	}
	if _, ok := b.Fn().Blocks[1].Instrs[0].(*ir.Assign); !ok {
		t.Fatalf("load after yield = %T, want *ir.Assign", b.Fn().Blocks[1].Instrs[0])
	}
}

func TestDeadStackStore(t *testing.T) {
	b := ir.NewBuilder("f")
	b.Emit(&ir.Builtin{Name: "put", Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 5}, &ir.Const{V: 1}}})
	b.Emit(&ir.Builtin{Name: "put", Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 5}, &ir.Const{V: 2}}})
	b.SetTerm(&ir.Ret{})
	if !deadStores(b.Fn()) {
		t.Fatal("expected the overwritten put to be removed")
	}
	if n := len(b.Fn().Blocks[0].Instrs); n != 1 {
		t.Fatalf("instrs = %d, want 1", n)
	}
}

func TestDeadStackStoreKeptWhenRead(t *testing.T) {
	b := ir.NewBuilder("f")
	b.Emit(&ir.Builtin{Name: "put", Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 5}, &ir.Const{V: 1}}})
	b.Emit(&ir.Builtin{Name: "get", Dst: b.NewReg("x"), Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 5}}})
	b.Emit(&ir.Builtin{Name: "put", Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 5}, &ir.Const{V: 2}}})
	b.SetTerm(&ir.Ret{})
	if deadStores(b.Fn()) {
		t.Fatal("a read between the stores keeps both live")
	}
}

func TestDeadStackStoreKeptAcrossYield(t *testing.T) {
	b := ir.NewBuilder("f")
	b.Emit(&ir.Builtin{Name: "put", Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 5}, &ir.Const{V: 1}}})
	b.Emit(&ir.Builtin{Name: "yield"})
	b.Emit(&ir.Builtin{Name: "put", Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 5}, &ir.Const{V: 2}}})
	b.SetTerm(&ir.Ret{})
	if deadStores(b.Fn()) {
		t.Fatal("a yield between the stores keeps both live")
	}
}

func TestMergeTails(t *testing.T) {
	b := ir.NewBuilder("f")
	a := b.NewReg("a")
	b.Emit(&ir.Load{Dst: a, Dev: "d0", Logic: "Setting"})
	thenB := b.NewBlock()
	elseB := b.NewBlock()
	endB := b.NewBlock()
	b.SetTerm(&ir.Br{Cond: ir.NonZero, A: a, Then: thenB, Else: elseB})

	b.SetBlock(thenB)
	b.Emit(&ir.Store{Dev: "d2", Logic: "On", Src: &ir.Const{V: 1}})
	b.Emit(&ir.Store{Dev: "d3", Logic: "On", Src: &ir.Const{V: 1}})
	b.SetTerm(&ir.Jmp{Target: endB})

	b.SetBlock(elseB)
	b.Emit(&ir.Store{Dev: "d2", Logic: "On", Src: &ir.Const{V: 0}})
	b.Emit(&ir.Store{Dev: "d3", Logic: "On", Src: &ir.Const{V: 1}})
	b.SetTerm(&ir.Jmp{Target: endB})

	b.SetBlock(endB)
	b.SetTerm(&ir.Ret{})
	if !mergeTails(b.Fn()) {
		t.Fatal("mergeTails did not factor the shared suffix")
	}
}

func TestMergeTailsColored(t *testing.T) {
	// Two branches whose tails differ only in virtual registers that map to
	// the same physical register; the coloured merge should factor them.
	b := ir.NewBuilder("f")
	a := b.NewReg("a")
	b.Emit(&ir.Load{Dst: a, Dev: "d0", Logic: "Setting"})
	thenB := b.NewBlock()
	elseB := b.NewBlock()
	endB := b.NewBlock()
	b.SetTerm(&ir.Br{Cond: ir.NonZero, A: a, Then: thenB, Else: elseB})

	t1 := b.NewReg("t1")
	b.SetBlock(thenB)
	b.Emit(&ir.Bin{Op: ir.Add, Dst: t1, A: a, B: &ir.Const{V: 1}})
	b.Emit(&ir.Store{Dev: "d3", Logic: "Setting", Src: t1})
	b.SetTerm(&ir.Jmp{Target: endB})

	t2 := b.NewReg("t2")
	b.SetBlock(elseB)
	b.Emit(&ir.Bin{Op: ir.Add, Dst: t2, A: a, B: &ir.Const{V: 1}})
	b.Emit(&ir.Store{Dev: "d3", Logic: "Setting", Src: t2})
	b.SetTerm(&ir.Jmp{Target: endB})

	b.SetBlock(endB)
	b.SetTerm(&ir.Ret{})
	colors := map[*ir.Reg]int{a: 0, t1: 1, t2: 1}
	if !MergeTailsColored(b.Fn(), colors) {
		t.Fatal("coloured tail merge did not factor the shared suffix")
	}
}

func TestCommutativeCSE(t *testing.T) {
	b := ir.NewBuilder("f")
	a := b.NewReg("a")
	c := b.NewReg("b")
	b.Emit(&ir.Load{Dst: a, Dev: "d0", Logic: "Setting"})
	b.Emit(&ir.Load{Dst: c, Dev: "d1", Logic: "Setting"})
	x := b.NewReg("x")
	b.Emit(&ir.Bin{Op: ir.Add, Dst: x, A: a, B: c})
	y := b.NewReg("y")
	b.Emit(&ir.Bin{Op: ir.Add, Dst: y, A: c, B: a})
	b.Emit(&ir.Builtin{Name: "sleep", Args: []ir.Value{x, y}})
	b.SetTerm(&ir.Ret{})
	if !globalCSE(b.Fn()) {
		t.Fatal("globalCSE did not merge a+b and b+a")
	}
}

func TestDeadStackStoreCrossBlock(t *testing.T) {
	b := ir.NewBuilder("f")
	c := b.NewReg("c")
	b.Emit(&ir.Load{Dst: c, Dev: "d0", Logic: "On"})
	thenB := b.NewBlock()
	elseB := b.NewBlock()
	joinB := b.NewBlock()
	b.SetTerm(&ir.Br{Cond: ir.NonZero, A: c, Then: thenB, Else: elseB})
	b.SetBlock(thenB)
	b.Emit(&ir.Builtin{Name: "put", Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 5}, &ir.Const{V: 1}}})
	b.SetTerm(&ir.Jmp{Target: joinB})
	b.SetBlock(elseB)
	b.Emit(&ir.Builtin{Name: "put", Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 5}, &ir.Const{V: 2}}})
	b.SetTerm(&ir.Jmp{Target: joinB})
	b.SetBlock(joinB)
	b.Emit(&ir.Builtin{Name: "put", Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 5}, &ir.Const{V: 3}}})
	b.SetTerm(&ir.Ret{})
	if !deadStores(b.Fn()) {
		t.Fatal("expected the branch stores to be removed")
	}
	if n := len(b.Fn().Blocks[1].Instrs); n != 0 {
		t.Fatalf("then block instrs = %d, want 0", n)
	}
	if n := len(b.Fn().Blocks[3].Instrs); n != 1 {
		t.Fatalf("join block instrs = %d, want 1", n)
	}
}

func TestFuseBranches(t *testing.T) {
	b := ir.NewBuilder("f")
	cmp := b.NewReg("c")
	b.Emit(&ir.Cmp{Cond: ir.Eq, Dst: cmp, A: &ir.Const{V: 1}, B: &ir.Const{V: 1}})
	thenB := b.NewBlock()
	endB := b.NewBlock()
	b.SetTerm(&ir.Br{Cond: ir.NonZero, A: cmp, Then: thenB, Else: endB})
	b.SetBlock(thenB)
	b.SetTerm(&ir.Jmp{Target: endB})
	b.SetBlock(endB)
	b.SetTerm(&ir.Ret{})
	fn := b.Fn()
	if !fuseBranches(fn) {
		t.Fatal("fuseBranches made no change")
	}
	br := fn.Blocks[0].Term.(*ir.Br)
	if br.Cond != ir.Eq {
		t.Errorf("branch cond = %v, want Eq", br.Cond)
	}
	if n := len(fn.Blocks[0].Instrs); n != 0 {
		t.Errorf("cmp not removed: %d instrs left", n)
	}
}

func TestEliminatePushPop(t *testing.T) {
	b := ir.NewBuilder("f")
	b.Emit(&ir.Builtin{Name: "push", Args: []ir.Value{&ir.Const{V: 5}}})
	x := b.NewReg("x")
	b.Emit(&ir.Builtin{Name: "pop", Dst: x})
	b.Emit(&ir.Store{Dev: "d0", Logic: "Setting", Src: x})
	b.SetTerm(&ir.Ret{})
	fn := b.Fn()
	fn.PrivateStack = true
	if !eliminatePushPop(fn) {
		t.Fatal("eliminatePushPop made no change")
	}
	// The push is gone and the pop became an assignment of the pushed value.
	for _, ins := range fn.Blocks[0].Instrs {
		if bi, ok := ins.(*ir.Builtin); ok && (bi.Name == "push" || bi.Name == "pop") {
			t.Fatalf("%s survived", bi.Name)
		}
	}
	if as, ok := fn.Blocks[0].Instrs[0].(*ir.Assign); !ok {
		t.Fatalf("first = %T, want *ir.Assign", fn.Blocks[0].Instrs[0])
	} else if c, ok := as.Src.(*ir.Const); !ok || c.V != 5 {
		t.Fatalf("forwarded value = %v, want 5", as.Src)
	}
}

func TestPromoteUserStack(t *testing.T) {
	b := ir.NewBuilder("f")
	b.Emit(&ir.Builtin{Name: "put", Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 0}, &ir.Const{V: 5}}})
	x := b.NewReg("x")
	b.Emit(&ir.Builtin{Name: "get", Dst: x, Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 0}}})
	b.Emit(&ir.Store{Dev: "d0", Logic: "Setting", Src: x})
	b.SetTerm(&ir.Ret{})
	fn := b.Fn()
	fn.PrivateStack = true
	fn.UserLimit = 128
	if !promoteUserStack(fn) {
		t.Fatal("promoteUserStack made no change")
	}
	for _, ins := range fn.Blocks[0].Instrs {
		if bi, ok := ins.(*ir.Builtin); ok && (bi.Name == "get" || bi.Name == "put") {
			t.Fatalf("stack access survived: %v", bi.Name)
		}
	}
}

func TestPromoteUserStackShared(t *testing.T) {
	b := ir.NewBuilder("f")
	b.Emit(&ir.Builtin{Name: "put", Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 0}, &ir.Const{V: 5}}})
	b.Emit(&ir.Builtin{Name: "get", Dst: b.NewReg("x"), Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 0}}})
	b.SetTerm(&ir.Ret{})
	fn := b.Fn()
	fn.UserLimit = 128 // PrivateStack false
	if promoteUserStack(fn) {
		t.Fatal("shared stack must not be promoted")
	}
}

func TestDeadStoreRelaxedOnPrivateStack(t *testing.T) {
	b := ir.NewBuilder("f")
	b.Emit(&ir.Builtin{Name: "put", Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 5}, &ir.Const{V: 1}}})
	b.Emit(&ir.Builtin{Name: "yield"})
	b.Emit(&ir.Builtin{Name: "put", Args: []ir.Value{&ir.Device{Name: "db"}, &ir.Const{V: 5}, &ir.Const{V: 2}}})
	b.SetTerm(&ir.Ret{})
	fn := b.Fn()
	fn.PrivateStack = true
	if !deadStores(fn) {
		t.Fatal("a private stack store overwritten after a yield is dead")
	}
}

func TestRedundantDeviceStores(t *testing.T) {
	build := func() *ir.Function {
		b := ir.NewBuilder("f")
		b.Emit(&ir.Store{Dev: "d0", Logic: "On", Src: &ir.Const{V: 0}})
		b.Emit(&ir.Store{Dev: "d1", Logic: "On", Src: &ir.Const{V: 0}})
		b.Emit(&ir.Store{Dev: "d0", Logic: "On", Src: &ir.Const{V: 0}})
		b.SetTerm(&ir.Ret{})
		return b.Fn()
	}
	// Off by default.
	fn := build()
	if redundantDeviceStores(fn) {
		t.Fatal("redundant device stores must be opt-in")
	}
	fn = build()
	fn.RedundantDeviceWrites = true
	if !redundantDeviceStores(fn) {
		t.Fatal("expected the repeated write to be removed")
	}
	if n := len(fn.Blocks[0].Instrs); n != 2 {
		t.Fatalf("instrs = %d, want 2", n)
	}
}
