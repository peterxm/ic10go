package regalloc

import (
	"testing"

	"ic10go/internal/ir"
)

func TestReuseNonOverlapping(t *testing.T) {
	b := ir.NewBuilder("f")
	x := b.NewReg("x")
	y := b.NewReg("y")
	b.Emit(&ir.Bin{Op: ir.Add, Dst: x, A: &ir.Const{V: 1}, B: &ir.Const{V: 2}})
	b.Emit(&ir.Builtin{Name: "sleep", Args: []ir.Value{x}})
	b.Emit(&ir.Bin{Op: ir.Add, Dst: y, A: &ir.Const{V: 3}, B: &ir.Const{V: 4}})
	b.Emit(&ir.Builtin{Name: "sleep", Args: []ir.Value{y}})
	b.SetTerm(&ir.Ret{})

	colors, err := Allocate(b.Fn(), 16)
	if err != nil {
		t.Fatal(err)
	}
	if colors[x] != colors[y] {
		t.Errorf("expected x and y to share a register, got %d and %d", colors[x], colors[y])
	}
}

func TestCoalescing(t *testing.T) {
	b := ir.NewBuilder("f")
	a := b.NewReg("a")
	c := b.NewReg("c")
	b.Emit(&ir.Bin{Op: ir.Add, Dst: a, A: &ir.Const{V: 1}, B: &ir.Const{V: 2}})
	b.Emit(&ir.Assign{Dst: c, Src: a})
	b.Emit(&ir.Builtin{Name: "sleep", Args: []ir.Value{c}})
	b.SetTerm(&ir.Ret{})

	colors, err := Allocate(b.Fn(), 16)
	if err != nil {
		t.Fatal(err)
	}
	if colors[a] != colors[c] {
		t.Errorf("copy a=c not coalesced: %d vs %d", colors[a], colors[c])
	}
}

func TestPressureSpills(t *testing.T) {
	b := ir.NewBuilder("f")
	regs := make([]*ir.Reg, 20)
	for i := range regs {
		regs[i] = b.NewReg("r")
		b.Emit(&ir.Assign{Dst: regs[i], Src: &ir.Const{V: float64(i)}})
	}
	for _, r := range regs {
		b.Emit(&ir.Builtin{Name: "sleep", Args: []ir.Value{r}})
	}
	b.SetTerm(&ir.Ret{})

	if _, err := Allocate(b.Fn(), 16); err != nil {
		t.Fatalf("expected spilling to succeed, got %v", err)
	}
}
