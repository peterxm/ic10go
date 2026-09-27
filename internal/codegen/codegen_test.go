package codegen

import (
	"strings"
	"testing"

	"ic10go/internal/ir"
)

func TestValidateOK(t *testing.T) {
	if err := Validate("move r0 1\ns d0 On r0\n"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTooManyLines(t *testing.T) {
	code := strings.Repeat("move r0 1\n", MaxLines+1)
	if err := Validate(code); err == nil {
		t.Fatal("expected a line count error")
	}
}

func TestValidateLineTooLong(t *testing.T) {
	code := "s d0 Setting " + strings.Repeat("1", MaxLineLen) + "\n"
	if err := Validate(code); err == nil {
		t.Fatal("expected a line length error")
	}
}

func TestValidateTooManyBytes(t *testing.T) {
	// 90-character lines that fit the line count but exceed the byte budget.
	line := "s d0 Setting " + strings.Repeat("1", MaxLineLen-len("s d0 Setting ")-1)
	code := strings.Repeat(line+"\n", MaxBytes/len(line)+2)
	if err := Validate(code); err == nil {
		t.Fatal("expected a byte size error")
	}
}

func TestValidateLineLimitMentionsBudget(t *testing.T) {
	err := Validate(strings.Repeat("move r0 1\n", MaxLines+1))
	if err == nil || !strings.Contains(err.Error(), "stats") {
		t.Fatalf("line limit error should hint at `ic10c stats`, got: %v", err)
	}
}

func TestValidateWithCustomLimits(t *testing.T) {
	code := strings.Repeat("move r0 1\n", 5)
	if err := ValidateWith(code, Limits{Lines: 5}); err != nil {
		t.Fatalf("5 lines should fit Lines=5: %v", err)
	}
	if err := ValidateWith(code, Limits{Lines: 4}); err == nil {
		t.Fatal("expected a line count error with Lines=4")
	}

	long := "s d0 Setting " + strings.Repeat("1", 100) + "\n"
	if err := ValidateWith(long, Limits{LineLen: 200}); err != nil {
		t.Fatalf("long line should fit LineLen=200: %v", err)
	}
	if err := ValidateWith(long, Limits{LineLen: 50}); err == nil {
		t.Fatal("expected a line length error with LineLen=50")
	}

	big := strings.Repeat("move r0 1\n", 200)
	if err := ValidateWith(big, Limits{Lines: 500, Bytes: 1 << 20}); err != nil {
		t.Fatalf("large program should fit raised lines/bytes: %v", err)
	}
}

func TestLimitsResolveDefaults(t *testing.T) {
	got := Limits{Lines: 256}.Resolve()
	if got.Lines != 256 || got.Bytes != MaxBytes || got.LineLen != MaxLineLen {
		t.Fatalf("Resolve = %+v, want {256 %d %d}", got, MaxBytes, MaxLineLen)
	}
}

func TestFoldSpecialArithLoadComputeStore(t *testing.T) {
	b := ir.NewBuilder("t")
	sp := b.NewReg("spv")
	tmp := b.NewReg("tmp")
	b.Emit(&ir.LoadSpecial{Dst: sp, Name: "sp"})
	b.Emit(&ir.Bin{Op: ir.Sub, Dst: tmp, A: sp, B: &ir.Const{V: 46}})
	b.Emit(&ir.StoreSpecial{Name: "sp", Src: tmp})
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{sp: 0, tmp: 1})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if code != "sub sp sp 46\n" {
		t.Fatalf("code = %q, want a single folded line", code)
	}
}

func TestFoldSpecialArithComputeStore(t *testing.T) {
	b := ir.NewBuilder("t")
	a := b.NewReg("a")
	tmp := b.NewReg("tmp")
	b.Emit(&ir.Assign{Dst: a, Src: &ir.Const{V: 100}})
	b.Emit(&ir.Bin{Op: ir.Add, Dst: tmp, A: a, B: &ir.Const{V: 2}})
	b.Emit(&ir.StoreSpecial{Name: "sp", Src: tmp})
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{a: 0, tmp: 1})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(code, "add sp r0 2\n") || strings.Contains(code, "move sp") {
		t.Fatalf("code = %q, want the store folded into the add", code)
	}
}

func TestNoFoldSpecialArithWhenTempIsReused(t *testing.T) {
	b := ir.NewBuilder("t")
	a := b.NewReg("a")
	tmp := b.NewReg("tmp")
	other := b.NewReg("other")
	b.Emit(&ir.Assign{Dst: a, Src: &ir.Const{V: 100}})
	b.Emit(&ir.Bin{Op: ir.Add, Dst: tmp, A: a, B: &ir.Const{V: 2}})
	b.Emit(&ir.StoreSpecial{Name: "sp", Src: tmp})
	b.Emit(&ir.Assign{Dst: other, Src: tmp}) // second use: keep the temporary
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{a: 0, tmp: 1, other: 2})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if strings.Contains(code, "add sp") || !strings.Contains(code, "move sp r1") {
		t.Fatalf("code = %q, want no fold when the temporary is reused", code)
	}
}

func TestFoldSpecialArithKeepsLoadWhenReused(t *testing.T) {
	// The sp load is used twice, so only the compute+store fold is applied and
	// the load stays: `move r0 sp` then `sub sp r0 46`.
	b := ir.NewBuilder("t")
	sp := b.NewReg("spv")
	tmp := b.NewReg("tmp")
	other := b.NewReg("other")
	b.Emit(&ir.LoadSpecial{Dst: sp, Name: "sp"})
	b.Emit(&ir.Bin{Op: ir.Sub, Dst: tmp, A: sp, B: &ir.Const{V: 46}})
	b.Emit(&ir.StoreSpecial{Name: "sp", Src: tmp})
	b.Emit(&ir.Assign{Dst: other, Src: sp}) // second use of the loaded value
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{sp: 0, tmp: 1, other: 2})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	want := "move r0 sp\nsub sp r0 46\nmove r2 r0\n"
	if code != want {
		t.Fatalf("code = %q, want %q", code, want)
	}
}

func TestNoFoldSpecialArithWhenStoreSourceDiffers(t *testing.T) {
	// The temporary is used once and the next instruction stores a *different*
	// register to sp: the fold must not fire (it would drop the real store).
	b := ir.NewBuilder("t")
	other := b.NewReg("other")
	tmp := b.NewReg("tmp")
	use := b.NewReg("use")
	b.Emit(&ir.Assign{Dst: other, Src: &ir.Const{V: 7}})
	b.Emit(&ir.Bin{Op: ir.Add, Dst: tmp, A: other, B: &ir.Const{V: 1}})
	b.Emit(&ir.StoreSpecial{Name: "sp", Src: other})
	b.Emit(&ir.Assign{Dst: use, Src: tmp})
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{other: 0, tmp: 1, use: 2})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	want := "move r0 7\nadd r1 r0 1\nmove sp r0\nmove r2 r1\n"
	if code != want {
		t.Fatalf("code = %q, want %q", code, want)
	}
}

func TestDropNoOpBranch(t *testing.T) {
	// A conditional branch whose both edges lead to the next line does nothing
	// and must be dropped.
	b := ir.NewBuilder("t")
	cond := b.NewReg("cond")
	next := b.NewBlock()
	b.Emit(&ir.Assign{Dst: cond, Src: &ir.Const{V: 1}})
	b.SetTerm(&ir.Br{Cond: ir.NonZero, A: cond, Then: next, Else: next})
	b.SetBlock(next)
	x := b.NewReg("x")
	b.Emit(&ir.Bin{Op: ir.Add, Dst: x, A: &ir.Const{V: 1}, B: &ir.Const{V: 2}})
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{cond: 0, x: 1})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if want := "move r0 1\nadd r1 1 2\n"; code != want {
		t.Fatalf("code = %q, want %q", code, want)
	}
}

func TestJumpTableEntriesSurviveNoOpRemoval(t *testing.T) {
	// The second table entry points at a case block laid out right after the
	// table, which looks like a no-op jump; it must be kept because the entries
	// are addressed by index.
	b := ir.NewBuilder("t")
	idx := b.NewReg("idx")
	b0, b1 := b.NewBlock(), b.NewBlock()
	b.Emit(&ir.Assign{Dst: idx, Src: &ir.Const{V: 0}})
	b.SetTerm(&ir.JmpDyn{Target: idx, Table: []*ir.Block{b0, b1}})
	b.SetBlock(b1)
	y := b.NewReg("y")
	b.Emit(&ir.Assign{Dst: y, Src: &ir.Const{V: 2}})
	b.SetTerm(&ir.Ret{})
	b.SetBlock(b0)
	x := b.NewReg("x")
	b.Emit(&ir.Assign{Dst: x, Src: &ir.Const{V: 1}})
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{idx: 0, y: 2, x: 1})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	want := "move r0 0\nadd r0 r0 1\njr r0\nj 6\nj 5\nmove r2 2\nmove r1 1\n"
	if code != want {
		t.Fatalf("code = %q, want %q", code, want)
	}
}

func TestFoldIndirectDestination(t *testing.T) {
	// `t = a * 2; setIreg(5, t)` becomes one instruction writing r5.
	b := ir.NewBuilder("t")
	a := b.NewReg("a")
	tmp := b.NewReg("tmp")
	b.Emit(&ir.Assign{Dst: a, Src: &ir.Const{V: 3}})
	b.Emit(&ir.Bin{Op: ir.Mul, Dst: tmp, A: a, B: &ir.Const{V: 2}})
	b.Emit(&ir.StoreIndirect{Ptr: &ir.Const{V: 5}, Src: tmp})
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{a: 0, tmp: 1})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if want := "move r0 3\nmul r5 r0 2\n"; code != want {
		t.Fatalf("code = %q, want %q", code, want)
	}
}

func TestFoldIndirectLoadOperand(t *testing.T) {
	// `u = ireg(rr0); d = u * -1` becomes `mul r2 rr0 -1`.
	b := ir.NewBuilder("t")
	ptr := b.NewReg("ptr")
	u := b.NewReg("u")
	d := b.NewReg("d")
	b.Emit(&ir.Assign{Dst: ptr, Src: &ir.Const{V: 2}})
	b.Emit(&ir.LoadIndirect{Dst: u, Ptr: ptr})
	b.Emit(&ir.Bin{Op: ir.Mul, Dst: d, A: u, B: &ir.Const{V: -1}})
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{ptr: 0, u: 1, d: 2})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if want := "move r0 2\nmul r2 rr0 -1\n"; code != want {
		t.Fatalf("code = %q, want %q", code, want)
	}
}

func TestNoFoldIndirectLoadWhenReused(t *testing.T) {
	b := ir.NewBuilder("t")
	ptr := b.NewReg("ptr")
	u := b.NewReg("u")
	d := b.NewReg("d")
	e := b.NewReg("e")
	b.Emit(&ir.Assign{Dst: ptr, Src: &ir.Const{V: 2}})
	b.Emit(&ir.LoadIndirect{Dst: u, Ptr: ptr})
	b.Emit(&ir.Bin{Op: ir.Mul, Dst: d, A: u, B: &ir.Const{V: -1}})
	b.Emit(&ir.Assign{Dst: e, Src: u}) // second use: keep the load
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{ptr: 0, u: 1, d: 2, e: 3})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(code, "move r1 rr0") {
		t.Fatalf("code = %q, want the indirect load kept", code)
	}
}
