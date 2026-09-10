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
		if d := defOf(ins); d == t2 {
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
