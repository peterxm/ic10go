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
		"var r0 = 0",
		"var r1 = 0",
		"r0 = d0.Temperature",
		"r1 = (r0 + 5)",
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
