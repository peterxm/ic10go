package ic10_test

import (
	"strings"
	"testing"

	"ic10go/internal/vm"
	"ic10go/pkg/ic10"
)

func popBankCompile(t *testing.T, src string, o ic10.Options) string {
	t.Helper()
	compiled, diags, err := ic10.CompileResult("x.icg", []byte(src), o)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if diags.HasErrors() {
		t.Fatalf("diags: %v", diags)
	}
	return compiled.Code
}

// popBankRun pre-fills the chip stack with 11..55 and runs the program, which
// sets sp=5 and pops. pop() reads Stack[sp-1] downwards, so the pops see
// 55,44,33,22,11.
func popBankRun(t *testing.T, code string) float64 {
	t.Helper()
	m := vm.New()
	copy(m.Device("db").Stack, []float64{11, 22, 33, 44, 55})
	if err := m.Load(code); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := m.Run(300); err != nil && err != vm.ErrStepLimit {
		t.Fatalf("run: %v", err)
	}
	return m.Device("d5").Values["Setting"]
}

func popBankSource(n int) string {
	var b strings.Builder
	b.WriteString("func main() {\n    sp = " + string(rune('0'+n)) + "\n    ")
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(names[i] + " := pop()")
	}
	b.WriteString("\n    for { yield(); d5.Setting = ")
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(" + ")
		}
		b.WriteString(names[i])
	}
	b.WriteString(" }\n}\n")
	return b.String()
}

// A run of five pop() folds into the IC10 rrN idiom (`pop rrC` in a counter
// loop) and behaves exactly like five separate pops.
func TestPopBankFoldsConsecutivePops(t *testing.T) {
	src := popBankSource(5)
	bank := popBankCompile(t, src, ic10.Options{})
	plain := popBankCompile(t, src, ic10.Options{NoPopBank: true})
	if !strings.Contains(bank, "pop rr") {
		t.Fatalf("5 pops did not fold into rrN:\n%s", bank)
	}
	if strings.Contains(plain, "pop rr") {
		t.Fatalf("NoPopBank still folded:\n%s", plain)
	}
	if nbank, nplain := strings.Count(bank, "\n"), strings.Count(plain, "\n"); nbank > nplain {
		t.Fatalf("pop-bank is longer (%d vs %d):\n%s", nbank, nplain, bank)
	}
	if got, want := popBankRun(t, bank), popBankRun(t, plain); got != want {
		t.Fatalf("pop-bank changed semantics: bank=%v plain=%v", got, want)
	}
	// 55+44+33+22+11 = 165, so the loop writes 165.
	if got := popBankRun(t, bank); got != 165 {
		t.Fatalf("d5.Setting = %v, want 165", got)
	}
}

// Four pops break even, so the fold only starts at five.
func TestPopBankThreshold(t *testing.T) {
	if code := popBankCompile(t, popBankSource(4), ic10.Options{}); strings.Contains(code, "pop rr") {
		t.Fatalf("4 pops should not fold:\n%s", code)
	}
}

// A bank variable can be reassigned after the loop; the write goes to its
// physical register.
func TestPopBankVarReassigned(t *testing.T) {
	src := "func main() {\n" +
		"    sp = 5\n" +
		"    a := pop(); b := pop(); c := pop(); d := pop(); e := pop()\n" +
		"    a = a + 100\n" +
		"    for { yield(); d5.Setting = a + b + c + d + e }\n" +
		"}\n"
	bank := popBankCompile(t, src, ic10.Options{})
	if !strings.Contains(bank, "pop rr") {
		t.Fatalf("did not fold:\n%s", bank)
	}
	if got, want := popBankRun(t, bank), float64(165+100); got != want {
		t.Fatalf("d5.Setting = %v, want %v:\n%s", got, want, bank)
	}
}
