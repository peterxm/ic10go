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

func TestFoldSpecialIntoDeviceStore(t *testing.T) {
	// `u = sp; s db Setting u` becomes `s db Setting sp`.
	b := ir.NewBuilder("t")
	sp := b.NewReg("spv")
	b.Emit(&ir.LoadSpecial{Dst: sp, Name: "sp"})
	b.Emit(&ir.Store{Dev: "db", Logic: "Setting", Src: sp})
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{sp: 0})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if want := "s db Setting sp\n"; code != want {
		t.Fatalf("code = %q, want %q", code, want)
	}
}

func TestFoldSpecialIntoPoke(t *testing.T) {
	// `u = sp; poke u v` becomes `poke sp v`.
	b := ir.NewBuilder("t")
	v := b.NewReg("v")
	sp := b.NewReg("spv")
	b.Emit(&ir.Assign{Dst: v, Src: &ir.Const{V: 5}})
	b.Emit(&ir.LoadSpecial{Dst: sp, Name: "sp"})
	b.Emit(&ir.Builtin{Name: "poke", Args: []ir.Value{sp, v}})
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{v: 0, sp: 1})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if want := "move r0 5\npoke sp r0\n"; code != want {
		t.Fatalf("code = %q, want %q", code, want)
	}
}

func TestFoldSpecialIntoSelect(t *testing.T) {
	// `u = sp; d = select c u 20` becomes `select d c sp 20`.
	b := ir.NewBuilder("t")
	c := b.NewReg("c")
	sp := b.NewReg("spv")
	d := b.NewReg("d")
	b.Emit(&ir.Assign{Dst: c, Src: &ir.Const{V: 1}})
	b.Emit(&ir.LoadSpecial{Dst: sp, Name: "sp"})
	b.Emit(&ir.Select{Dst: d, Cond: c, Then: sp, Else: &ir.Const{V: 20}})
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{c: 0, sp: 1, d: 2})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if want := "move r0 1\nselect r2 r0 sp 20\n"; code != want {
		t.Fatalf("code = %q, want %q", code, want)
	}
}

func TestFoldSpecialIntoSlotStore(t *testing.T) {
	// `u = sp; ss d0 1 SlotType u` becomes `ss d0 1 SlotType sp`.
	b := ir.NewBuilder("t")
	sp := b.NewReg("spv")
	b.Emit(&ir.LoadSpecial{Dst: sp, Name: "sp"})
	b.Emit(&ir.StoreSlot{Dev: "d0", Index: &ir.Const{V: 1}, Logic: "SlotType", Src: sp})
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{sp: 0})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if want := "ss d0 1 SlotType sp\n"; code != want {
		t.Fatalf("code = %q, want %q", code, want)
	}
}

func TestNoFoldSpecialIntoStoreWhenReused(t *testing.T) {
	// The sp load is used twice, so the store keeps the temporary.
	b := ir.NewBuilder("t")
	sp := b.NewReg("spv")
	other := b.NewReg("other")
	b.Emit(&ir.LoadSpecial{Dst: sp, Name: "sp"})
	b.Emit(&ir.Store{Dev: "db", Logic: "Setting", Src: sp})
	b.Emit(&ir.Assign{Dst: other, Src: sp})
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{sp: 0, other: 1})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if want := "move r0 sp\ns db Setting r0\nmove r1 r0\n"; code != want {
		t.Fatalf("code = %q, want %q", code, want)
	}
}

func TestFoldSpecialArithSelect(t *testing.T) {
	// `t = select c a b; sp = t` becomes `select sp c a b`.
	b := ir.NewBuilder("t")
	tmp := b.NewReg("tmp")
	b.Emit(&ir.Select{Dst: tmp, Cond: &ir.Const{V: 1}, Then: &ir.Const{V: 7}, Else: &ir.Const{V: 20}})
	b.Emit(&ir.StoreSpecial{Name: "sp", Src: tmp})
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{tmp: 0})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if want := "select sp 1 7 20\n"; code != want {
		t.Fatalf("code = %q, want %q", code, want)
	}
}

func TestFoldAcrossFallthroughBlock(t *testing.T) {
	// The select and the store that consumes it are in consecutive blocks
	// joined by a fall-through jump: the fold must span the boundary.
	b := ir.NewBuilder("t")
	saved := b.NewReg("saved")
	dst := b.NewReg("dst")
	nextB := b.NewBlock()
	endB := b.NewBlock()
	b.Emit(&ir.Load{Dst: saved, Dev: "d0", Logic: "Setting"})
	b.Emit(&ir.Select{Dst: dst, Cond: &ir.Const{V: 1}, Then: saved, Else: &ir.Const{V: 20}})
	b.SetTerm(&ir.Jmp{Target: nextB})

	b.SetBlock(nextB)
	b.Emit(&ir.StoreSpecial{Name: "sp", Src: dst})
	b.SetTerm(&ir.Jmp{Target: endB})

	b.SetBlock(endB)
	b.SetTerm(&ir.Ret{})

	code, err := Generate(b.Fn(), map[*ir.Reg]int{saved: 0, dst: 1})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if want := "l r0 d0 Setting\nselect sp 1 r0 20\n"; code != want {
		t.Fatalf("code = %q, want %q", code, want)
	}
}

func TestNoFoldAcrossSharedBlock(t *testing.T) {
	// The consuming block is also reached from elsewhere, so its first
	// instruction must keep its own line and must not be folded into the
	// predecessor.
	b := ir.NewBuilder("t")
	saved := b.NewReg("saved")
	dst := b.NewReg("dst")
	cond := b.NewReg("cond")
	nextB := b.NewBlock()
	sideB := b.NewBlock()
	endB := b.NewBlock()

	b.Emit(&ir.Assign{Dst: cond, Src: &ir.Const{V: 1}})
	b.Emit(&ir.Select{Dst: dst, Cond: cond, Then: saved, Else: &ir.Const{V: 20}})
	b.SetTerm(&ir.Br{Cond: ir.NonZero, A: cond, Then: sideB, Else: nextB})

	b.SetBlock(sideB)
	b.SetTerm(&ir.Jmp{Target: nextB})

	b.SetBlock(nextB)
	b.Emit(&ir.StoreSpecial{Name: "sp", Src: dst})
	b.SetTerm(&ir.Jmp{Target: endB})

	b.SetBlock(endB)
	b.SetTerm(&ir.Ret{})

	code, err := Generate(b.Fn(), map[*ir.Reg]int{cond: 0, saved: 1, dst: 2})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if strings.Contains(code, "select sp") {
		t.Fatalf("code = %q, want no fold into a shared block", code)
	}
}

func TestFoldSpecialIntoBranch(t *testing.T) {
	// `u = sp; bgtz u L` becomes `bgtz sp L` (the load line disappears).
	b := ir.NewBuilder("t")
	u := b.NewReg("u")
	thenB := b.NewBlock()
	endB := b.NewBlock()
	b.Emit(&ir.LoadSpecial{Dst: u, Name: "sp"})
	b.SetTerm(&ir.Br{Cond: ir.Gt, A: u, B: &ir.Const{V: 0}, Then: thenB, Else: endB})
	b.SetBlock(thenB)
	b.Emit(&ir.Store{Dev: "d0", Logic: "Setting", Src: &ir.Const{V: 1}})
	b.SetTerm(&ir.Jmp{Target: endB})
	b.SetBlock(endB)
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{u: 0})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if strings.Contains(code, "move r0 sp") {
		t.Fatalf("load not folded into the branch:\n%s", code)
	}
	if !strings.Contains(code, "bgtz sp ") && !strings.Contains(code, "blez sp ") {
		t.Fatalf("want a branch on sp:\n%s", code)
	}
}

func TestFoldIndirectIntoBranch(t *testing.T) {
	// `u = ireg(rrP); beqz u L` becomes `beqz rrP L`.
	b := ir.NewBuilder("t")
	u := b.NewReg("u")
	p := b.NewReg("p")
	thenB := b.NewBlock()
	endB := b.NewBlock()
	b.Emit(&ir.LoadIndirect{Dst: u, Ptr: p})
	b.SetTerm(&ir.Br{Cond: ir.Zero, A: u, Then: thenB, Else: endB})
	b.SetBlock(thenB)
	b.Emit(&ir.Store{Dev: "d0", Logic: "On", Src: &ir.Const{V: 1}})
	b.SetTerm(&ir.Jmp{Target: endB})
	b.SetBlock(endB)
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{u: 1, p: 3})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if strings.Contains(code, "move r1 rr3") {
		t.Fatalf("load not folded into the branch:\n%s", code)
	}
	if !strings.Contains(code, "beqz rr3 ") && !strings.Contains(code, "bnez rr3 ") {
		t.Fatalf("want a branch on rr3:\n%s", code)
	}
}

func TestFoldSpecialIntoCmp(t *testing.T) {
	// `u = sp; d = u > 5` becomes `sgt d sp 5`.
	b := ir.NewBuilder("t")
	u := b.NewReg("u")
	d := b.NewReg("d")
	b.Emit(&ir.LoadSpecial{Dst: u, Name: "sp"})
	b.Emit(&ir.Cmp{Cond: ir.Gt, Dst: d, A: u, B: &ir.Const{V: 5}})
	b.Emit(&ir.Store{Dev: "d0", Logic: "Setting", Src: d})
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{u: 0, d: 1})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if strings.Contains(code, "move ") {
		t.Fatalf("load not folded into the Cmp:\n%s", code)
	}
	if !strings.Contains(code, "sgt r1 sp 5") {
		t.Fatalf("want `sgt r1 sp 5`:\n%s", code)
	}
}

func TestNoFoldIntoBranchWhenReused(t *testing.T) {
	// The loaded value is read again after the branch's own consumer, so the
	// load keeps its own line.
	b := ir.NewBuilder("t")
	u := b.NewReg("u")
	thenB := b.NewBlock()
	endB := b.NewBlock()
	b.Emit(&ir.LoadSpecial{Dst: u, Name: "sp"})
	b.Emit(&ir.Bin{Op: ir.Add, Dst: u, A: u, B: &ir.Const{V: 1}})
	b.SetTerm(&ir.Br{Cond: ir.Gt, A: u, B: &ir.Const{V: 0}, Then: thenB, Else: endB})
	b.SetBlock(thenB)
	b.Emit(&ir.Store{Dev: "d0", Logic: "Setting", Src: &ir.Const{V: 1}})
	b.SetTerm(&ir.Jmp{Target: endB})
	b.SetBlock(endB)
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{u: 0})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(code, "move r0 sp") || !strings.Contains(code, "add r0 r0 1") {
		t.Fatalf("expected the load and compute to remain:\n%s", code)
	}
}

func TestFoldCopiedLoadIntoPoke(t *testing.T) {
	// The lowerer copies the loaded value to the variable; the fold skips that
	// copy: `v = sp; x = v; poke x 1` -> `poke sp 1`.
	b := ir.NewBuilder("t")
	v := b.NewReg("v")
	x := b.NewReg("x")
	b.Emit(&ir.LoadSpecial{Dst: v, Name: "sp"})
	b.Emit(&ir.Assign{Dst: x, Src: v})
	b.Emit(&ir.Builtin{Name: "poke", Args: []ir.Value{x, &ir.Const{V: 1}}})
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{v: 0, x: 0})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if want := "poke sp 1\n"; code != want {
		t.Fatalf("code = %q, want %q", code, want)
	}
}

func TestFoldCopiedLoadIntoGetAddress(t *testing.T) {
	// `v = sp; x = v; y = get(db, x)` -> `get <dst> db sp`.
	b := ir.NewBuilder("t")
	v := b.NewReg("v")
	x := b.NewReg("x")
	y := b.NewReg("y")
	b.Emit(&ir.LoadSpecial{Dst: v, Name: "sp"})
	b.Emit(&ir.Assign{Dst: x, Src: v})
	b.Emit(&ir.Builtin{Name: "get", Dst: y, Args: []ir.Value{&ir.Device{Name: "db"}, x}})
	b.Emit(&ir.Store{Dev: "d0", Logic: "Setting", Src: y})
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{v: 0, x: 0, y: 1})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if strings.Contains(code, "move ") {
		t.Fatalf("load copy not folded:\n%s", code)
	}
	if !strings.Contains(code, "get r1 db sp") {
		t.Fatalf("want `get r1 db sp`:\n%s", code)
	}
}

func TestFoldCopiedLoadIntoBranch(t *testing.T) {
	// `v = sp; x = v; bgtz x L` -> `bgtz sp L`.
	b := ir.NewBuilder("t")
	v := b.NewReg("v")
	x := b.NewReg("x")
	thenB := b.NewBlock()
	endB := b.NewBlock()
	b.Emit(&ir.LoadSpecial{Dst: v, Name: "sp"})
	b.Emit(&ir.Assign{Dst: x, Src: v})
	b.SetTerm(&ir.Br{Cond: ir.Gt, A: x, B: &ir.Const{V: 0}, Then: thenB, Else: endB})
	b.SetBlock(thenB)
	b.Emit(&ir.Store{Dev: "d0", Logic: "Setting", Src: &ir.Const{V: 1}})
	b.SetTerm(&ir.Jmp{Target: endB})
	b.SetBlock(endB)
	b.SetTerm(&ir.Ret{})
	code, err := Generate(b.Fn(), map[*ir.Reg]int{v: 0, x: 0})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if strings.Contains(code, "move r0 sp") {
		t.Fatalf("load copy not folded into the branch:\n%s", code)
	}
	if !strings.Contains(code, "blez sp ") && !strings.Contains(code, "bgtz sp ") {
		t.Fatalf("want a branch on sp:\n%s", code)
	}
}
