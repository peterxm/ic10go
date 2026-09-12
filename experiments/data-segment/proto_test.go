package dataseg

import (
	"os"
	"testing"

	"ic10go/internal/builtin"
	"ic10go/internal/vm"
	"ic10go/pkg/ic10"
)

// compileAndLoad compiles an .icg prototype and loads it into a fresh VM.
func compileAndLoad(t *testing.T, name string) *vm.Machine {
	t.Helper()
	src, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	code, diags, err := ic10.Compile(name, src)
	if err != nil || diags.HasErrors() {
		t.Fatalf("compile %s: %v %v", name, diags.Diags, err)
	}
	m := vm.New()
	if err := m.Load(code); err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return m
}

func run(t *testing.T, m *vm.Machine, steps int) {
	t.Helper()
	for i := 0; i < steps; i++ {
		if err := m.Run(1); err != nil && err != vm.ErrStepLimit {
			t.Fatalf("run: %v", err)
		}
	}
}

// TestBarsielDataSegment verifies the two-program workflow: the loader fills
// the persistent stack, then a separate runtime reads it via get(db, addr).
func TestBarsielDataSegment(t *testing.T) {
	// 1. Run the loader once.
	loader := compileAndLoad(t, "barsiel_loader.icg")
	run(t, loader, 200)

	// 2. The stack now holds the table (slots 0..33) and the version sentinel.
	wantFirst := -1301215609.0 // Iron display hash, slot 0
	if got := loader.Device("db").Stack[0]; got != wantFirst {
		t.Fatalf("after loader: stack[0] = %v, want %v", got, wantFirst)
	}
	if got := loader.Device("db").Stack[34]; got != 1 {
		t.Fatalf("after loader: sentinel = %v, want 1", got)
	}

	// 3. Run the runtime with the preloaded stack.
	rt := compileAndLoad(t, "barsiel_runtime.icg")
	copy(rt.Device("db").Stack, loader.Device("db").Stack)

	// Make the Ingot Dial read 3 (Gold) so the lookup path runs.
	dial := rt.Device("dial")
	dial.Hash = uint32(int64(554524804))
	dial.NameHash = builtin.Hash("Ingot Dial")
	dial.Values["Setting"] = 3

	run(t, rt, 4000)
	if got := rt.Get("db", "Setting"); got != 226410516 {
		t.Errorf("runtime db.Setting = %v, want 226410516 (Gold)", got)
	}

	// 4. Without the loader the table is missing and the lookup reads 0.
	missing := compileAndLoad(t, "barsiel_runtime.icg")
	dial2 := missing.Device("dial")
	dial2.Hash = uint32(int64(554524804))
	dial2.NameHash = builtin.Hash("Ingot Dial")
	dial2.Values["Setting"] = 3
	run(t, missing, 4000)
	if got := missing.Get("db", "Setting"); got != 0 {
		t.Errorf("no-loader db.Setting = %v, want 0", got)
	}
}
