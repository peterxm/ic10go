package ic10_test

import (
	"reflect"
	"testing"

	"ic10go/internal/vm"
	"ic10go/pkg/ic10"
)

// renamedTailSrc has two switch cases with identical tails that the register
// allocator may colour differently; the opt-in structural merge can factor
// them.
const renamedTailSrc = `
func main() {
    d0.Mode = 1
    for {
        yield()
        switch d0.Setting {
        case 1:
            d4.On = 1
            d1.Setting = d0.Open
            d2.Setting = d3.Open
        case 2:
            d4.On = 0
            d1.Setting = d0.Open
            d2.Setting = d3.Open
        }
    }
}
`

// runRenamedTail loads code, drives the two cases and returns the device state.
func runRenamedTail(t *testing.T, code string) map[string]any {
	t.Helper()
	m := vm.New()
	for _, n := range []string{"d0", "d1", "d2", "d3"} {
		m.Device(n).Values["Idle"] = 1
	}
	if err := m.Load(code); err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	for _, setting := range []float64{1, 2} {
		m.Set("d0", "Setting", setting)
		if err := m.Run(500); err != nil && err != vm.ErrStepLimit {
			t.Fatal(err)
		}
		out["d1.Setting"] = m.Get("d1", "Setting")
		out["d2.Setting"] = m.Get("d2", "Setting")
		out["d4.On"] = m.Get("d4", "On")
	}
	return out
}

// TestMergeRenamedTailsOption checks the opt-in structural tail merge compiles
// and preserves behaviour; it is off by default and must not change the default
// output.
func TestMergeRenamedTailsOption(t *testing.T) {
	src := []byte(renamedTailSrc)
	plain, diags, err := ic10.CompileWithOptions("t.icg", src, ic10.Options{})
	if err != nil || diags.HasErrors() {
		t.Fatalf("default compile: %v %v", diags.Diags, err)
	}
	renamed, diags, err := ic10.CompileWithOptions("t.icg", src, ic10.Options{MergeRenamedTails: true})
	if err != nil || diags.HasErrors() {
		t.Fatalf("renamed compile: %v %v", diags.Diags, err)
	}
	if got := len(renamed); got > len(plain) {
		t.Fatalf("renamed tail merge grew the program: %d > %d bytes", got, len(plain))
	}
	if !reflect.DeepEqual(runRenamedTail(t, plain), runRenamedTail(t, renamed)) {
		t.Fatal("renamed tail merge changed behaviour")
	}
}

// TestMergeRenamedTailsEnv checks the environment switch turns the option on,
// matching the CLI/LSP convention for the other size switches.
func TestMergeRenamedTailsEnv(t *testing.T) {
	t.Setenv("IC10C_MERGE_RENAMED_TAILS", "1")
	on, diags, err := ic10.CompileWithOptions("t.icg", []byte(renamedTailSrc), ic10.Options{})
	if err != nil || diags.HasErrors() {
		t.Fatalf("env compile: %v %v", diags.Diags, err)
	}
	explicit, diags, err := ic10.CompileWithOptions("t.icg", []byte(renamedTailSrc), ic10.Options{MergeRenamedTails: true})
	if err != nil || diags.HasErrors() {
		t.Fatalf("explicit compile: %v %v", diags.Diags, err)
	}
	if on != explicit {
		t.Fatal("IC10C_MERGE_RENAMED_TAILS did not match the explicit option")
	}
}
