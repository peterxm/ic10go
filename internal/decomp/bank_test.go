package decomp

import (
	"strings"
	"testing"
)

// A register written through a runtime pointer must stay physical: the
// translator emits reserveRegs(lo, hi) and accesses the range through
// ireg/setIreg, otherwise the variable view and the pointer view diverge.
func TestIndirectBankKeptPhysical(t *testing.T) {
	src := "move r1 2\nsin rr1 r3\nadd r1 r1 1\nsin rr1 r3\nput db 0 r2\nput db 1 r3\n"
	code, warns, err := Decompile(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v\n%s", warns, code)
	}
	for _, want := range []string{"reserveRegs(2, 3)", "setIreg(r1, t1)", "ireg(2)", "ireg(3)"} {
		if !strings.Contains(code, want) {
			t.Errorf("output is missing %q:\n%s", want, code)
		}
	}
	if strings.Contains(code, "var r2") || strings.Contains(code, "var r3") {
		t.Errorf("bank registers must not be declared as variables:\n%s", code)
	}
}

// A pointer the analysis cannot bound is reported instead of being translated
// silently wrong.
func TestIndirectBankUnknownPointerWarns(t *testing.T) {
	src := "l r1 db Setting\nsin rr1 r3\n"
	code, warns, err := Decompile(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) == 0 {
		t.Fatalf("expected an unbounded-pointer warning:\n%s", code)
	}
	if strings.Contains(code, "reserveRegs") {
		t.Errorf("unbounded pointer must not reserve a range:\n%s", code)
	}
}
