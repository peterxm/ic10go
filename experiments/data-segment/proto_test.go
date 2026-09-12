package dataseg

import (
	"os"
	"path/filepath"
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
	// The Barsiel prototype is derived from a third-party community script and
	// is not distributed with the repository.
	if _, err := os.Stat("barsiel_loader.icg"); err != nil {
		t.Skip("barsiel prototype files not present (third-party, not in this repo)")
	}
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

// TestInGameScriptsRun sanity-checks the hand-written in-game test scripts:
// each must parse and execute without an unsupported-instruction error.
func TestInGameScriptsRun(t *testing.T) {
	files, err := filepath.Glob("ingame/*.ic")
	if err != nil || len(files) == 0 {
		t.Fatalf("no ingame scripts: %v", err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			m := vm.New()
			m.Set("d0", "Setting", 0)
			if err := m.Load(string(src)); err != nil {
				t.Fatalf("load: %v", err)
			}
			for i := 0; i < 30; i++ {
				if err := m.Run(1); err != nil && err != vm.ErrStepLimit {
					t.Fatalf("run: %v", err)
				}
			}
		})
	}
}

// TestLocalStackSharedWithDB locks in the real-hardware finding that the local
// stack (push/pop/poke/peek) and the housing stack (get/put db) are the same
// memory on a standard IC host.
func TestLocalStackSharedWithDB(t *testing.T) {
	m := vm.New()
	if err := m.Load("poke 50 12345\n"); err != nil {
		t.Fatal(err)
	}
	if err := m.Run(10); err != nil && err != vm.ErrStepLimit {
		t.Fatal(err)
	}
	if got := m.Device("db").Stack[50]; got != 12345 {
		t.Errorf("poke 50 -> get db 50 = %v, want 12345", got)
	}

	m2 := vm.New()
	if err := m2.Load("put db 60 54321\nmove r1 sp\nmove sp 61\npeek r0\n"); err != nil {
		t.Fatal(err)
	}
	if err := m2.Run(10); err != nil && err != vm.ErrStepLimit {
		t.Fatal(err)
	}
	if got := m2.Regs[0]; got != 54321 {
		t.Errorf("put db 60 -> peek 60 = %v, want 54321", got)
	}
}

// runPair runs a committed loader/runtime pair and returns the runtime machine
// after `steps` instructions.
func runPair(t *testing.T, dir string, steps int) *vm.Machine {
	t.Helper()
	loader, err := os.ReadFile(dir + "/1_loader.ic")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := os.ReadFile(dir + "/2_runtime.ic")
	if err != nil {
		t.Fatal(err)
	}
	lm := vm.New()
	if err := lm.Load(string(loader)); err != nil {
		t.Fatal(err)
	}
	if err := lm.Run(100); err != nil && err != vm.ErrStepLimit {
		t.Fatal(err)
	}
	m := vm.New()
	copy(m.Stack, lm.Stack)
	if err := m.Load(string(runtime)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < steps; i++ {
		if err := m.Run(1); err != nil && err != vm.ErrStepLimit {
			t.Fatal(err)
		}
	}
	return m
}

func displayedValues(m *vm.Machine, steps int) map[float64]bool {
	seen := map[float64]bool{}
	for i := 0; i < steps; i++ {
		if err := m.Run(1); err != nil && err != vm.ErrStepLimit {
			break
		}
		seen[m.Get("d0", "Setting")] = true
	}
	return seen
}

// TestIngameDataDemo runs the committed data-table loader/runtime pair.
func TestIngameDataDemo(t *testing.T) {
	m := runPair(t, "ingame-data", 0)
	seen := displayedValues(m, 400)
	for _, want := range []float64{111, 222, 333} {
		if !seen[want] {
			t.Errorf("table value %v never displayed", want)
		}
	}
}

// TestIngameSwitchDemo runs the committed switch-table loader/runtime pair.
func TestIngameSwitchDemo(t *testing.T) {
	m := runPair(t, "ingame-switch", 0)
	seen := displayedValues(m, 400)
	for _, want := range []float64{111, 222, 333} {
		if !seen[want] {
			t.Errorf("table switch value %v never displayed", want)
		}
	}
}

// TestIngameStackDemo runs the committed stack-access loader/runtime pair.
func TestIngameStackDemo(t *testing.T) {
	m := runPair(t, "ingame-stack", 0)
	seen := displayedValues(m, 400)
	for _, want := range []float64{111, 222, 333} {
		if !seen[want] {
			t.Errorf("stack-access value %v never displayed", want)
		}
	}
}
