package decomp

import (
	"strings"
	"testing"
)

func TestDecompileBasic(t *testing.T) {
	src := `define X 5
alias s d0
l r0 s Temperature
add r1 r0 X
s s Setting r1
`
	code, warns, err := Decompile(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	for _, want := range []string{
		"r0 := d0.Temperature",
		"r1 := (r0 + 5)",
		"d0.Setting = r1",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("output missing %q:\n%s", want, code)
		}
	}
}

func TestDecompileControlFlow(t *testing.T) {
	src := `loop:
add r0 r0 1
blt r0 5 loop
jal helper
j ra
helper:
mul r0 r0 2
j ra
`
	code, warns, err := Decompile(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	for _, want := range []string{
		"label loop:",
		"if r0 < 5 { goto loop }",
		"call helper",
		"ret",
		"label helper:",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("output missing %q:\n%s", want, code)
		}
	}
}

func TestDecompileBatchAndHash(t *testing.T) {
	src := `lbn r0 -400115994 HASH("Bank 1") Ratio 0
sbn 1220484876 HASH("Override") Open 1
`
	code, warns, err := Decompile(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if !strings.Contains(code, `batch.readName(-400115994, hash("Bank 1"), "Ratio", "Average")`) {
		t.Errorf("batch read not translated:\n%s", code)
	}
	if !strings.Contains(code, `batch.writeName(1220484876, hash("Override"), "Open", 1)`) {
		t.Errorf("batch write not translated:\n%s", code)
	}
}

func TestDecompileIndirect(t *testing.T) {
	src := `trunc rr5 r0
mod r0 r0 rr5
`
	code, warns, err := Decompile(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if !strings.Contains(code, "setIreg(r5,") {
		t.Errorf("indirect store not translated:\n%s", code)
	}
	if !strings.Contains(code, "ireg(r5)") {
		t.Errorf("indirect read not translated:\n%s", code)
	}
}

func TestDecompileReadFirstDeclaration(t *testing.T) {
	// r1 is read as the select condition before it is written.
	code, warns, err := Decompile("select r1 r1 45000 40000\ns d0 Setting r1\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if !strings.Contains(code, "var r1 = 0") {
		t.Errorf("read-before-write register should be declared:\n%s", code)
	}
	if strings.Contains(code, "r1 :=") {
		t.Errorf("r1 should be assigned with =, not :=\n%s", code)
	}
}

func TestDecompileStructuredSplitsAtTarget(t *testing.T) {
	// brnez r0 2 jumps into the middle of what would otherwise be one block.
	src := "move r0 0\nbrnez r0 2\nmove r1 1\nmove r2 2\ns d0 Setting r2\n"
	code, warns, err := DecompileStructured(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if !strings.Contains(code, "label L3:") {
		t.Errorf("branch target label was not emitted:\n%s", code)
	}
}

func TestDecompileComputedJump(t *testing.T) {
	code, warns, err := Decompile("brnez r0 r0\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if !strings.Contains(code, "if r0 != 0 { jump(r0) }") {
		t.Errorf("computed jump not translated:\n%s", code)
	}
}
