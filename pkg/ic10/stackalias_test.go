package ic10_test

import (
	"strings"
	"testing"

	"ic10go/internal/vm"
	"ic10go/pkg/ic10"
)

// A pop reads the stack at sp-1, which aliases the absolute user slots that
// poke/get/put address. User-stack promotion (mem2reg) must not move a slot to
// a register while a pop can still read it, or the pop sees a stale value.
func TestPokePopAliasing(t *testing.T) {
	src := "func main() {\n" +
		"    sp = 3\n" +
		"    poke(0, 11); poke(1, 22); poke(2, 33)\n" +
		"    a := pop(); b := pop(); c := pop()\n" +
		"    for { yield(); d0.Setting = a + b + c }\n" +
		"}\n"
	compiled, diags, err := ic10.CompileResult("x.icg", []byte(src), ic10.Options{})
	if err != nil || diags.HasErrors() {
		t.Fatalf("compile: err=%v diags=%v", err, diags.Diags)
	}
	if !strings.Contains(compiled.Code, "poke 2 33") {
		t.Fatalf("a poke to a slot read by pop was dropped:\n%s", compiled.Code)
	}
	m := vm.New()
	if err := m.Load(compiled.Code); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := m.Run(200); err != nil && err != vm.ErrStepLimit {
		t.Fatalf("run: %v", err)
	}
	// pops read slots 2,1,0 -> 33+22+11.
	if got := m.Device("d0").Values["Setting"]; got != 66 {
		t.Fatalf("d0.Setting = %v, want 66 (pop read a stale stack slot?)\n%s", got, compiled.Code)
	}
}
