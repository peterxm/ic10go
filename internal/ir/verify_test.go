package ir

import "testing"

func TestVerifyAcceptsWellFormed(t *testing.T) {
	fn := &Function{Name: "main"}
	entry := fn.NewBlock()
	end := fn.NewBlock()
	entry.Term = &Jmp{Target: end}
	end.Term = &Ret{}
	if err := VerifyReachable(fn); err != nil {
		t.Fatalf("VerifyReachable: %v", err)
	}
}

func TestVerifyRejectsMissingTerminator(t *testing.T) {
	fn := &Function{Name: "main"}
	fn.NewBlock()
	if err := Verify(fn); err == nil {
		t.Fatal("expected an error for a block without a terminator")
	}
}

func TestVerifyRejectsForeignBlock(t *testing.T) {
	fn := &Function{Name: "main"}
	entry := fn.NewBlock()
	foreign := &Block{ID: 99}
	entry.Term = &Jmp{Target: foreign}
	if err := Verify(fn); err == nil {
		t.Fatal("expected an error for a reference to a block outside the function")
	}
}

func TestVerifyRejectsDanglingJumpTable(t *testing.T) {
	fn := &Function{Name: "main"}
	entry := fn.NewBlock()
	entry.Term = &JmpDyn{Target: &Const{V: 0}, Table: []*Block{{ID: 42}}}
	if err := Verify(fn); err == nil {
		t.Fatal("expected an error for a jump table entry outside the function")
	}
}

func TestVerifyReachableRejectsUnreachable(t *testing.T) {
	fn := &Function{Name: "main"}
	entry := fn.NewBlock()
	end := fn.NewBlock()
	dead := fn.NewBlock()
	entry.Term = &Jmp{Target: end}
	end.Term = &Ret{}
	dead.Term = &Ret{}
	if err := Verify(fn); err != nil {
		t.Fatalf("Verify should accept unreachable blocks: %v", err)
	}
	if err := VerifyReachable(fn); err == nil {
		t.Fatal("expected VerifyReachable to reject the unreachable block")
	}
}

func TestVerifyRejectsUndefinedUse(t *testing.T) {
	fn := &Function{Name: "main"}
	entry := fn.NewBlock()
	entry.Instrs = append(entry.Instrs, &Assign{Dst: fn.NewReg("a"), Src: fn.NewReg("never")})
	entry.Term = &Ret{}
	if err := Verify(fn); err == nil {
		t.Fatal("expected an error for a use of a register that is never defined")
	}
}

func TestVerifyAcceptsParamUse(t *testing.T) {
	fn := &Function{Name: "main"}
	p := fn.NewReg("p")
	fn.Params = append(fn.Params, p)
	entry := fn.NewBlock()
	entry.Instrs = append(entry.Instrs, &Assign{Dst: fn.NewReg("a"), Src: p})
	entry.Term = &Ret{}
	if err := Verify(fn); err != nil {
		t.Fatalf("a Params register must count as defined: %v", err)
	}
}
