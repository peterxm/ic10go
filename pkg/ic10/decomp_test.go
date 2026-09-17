package ic10_test

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ic10go/internal/decomp"
	"ic10go/internal/vm"
	"ic10go/pkg/ic10"
)

func compareDevices(t *testing.T, a, b *vm.Machine) {
	t.Helper()
	for name, da := range a.Devices {
		db, ok := b.Devices[name]
		if !ok {
			t.Errorf("device %s missing in decompiled run", name)
			continue
		}
		for logic, va := range da.Values {
			vb := db.Values[logic]
			if math.IsNaN(va) && math.IsNaN(vb) {
				continue
			}
			if va != vb {
				t.Errorf("device %s.%s: original=%v decompiled=%v", name, logic, va, vb)
			}
		}
		for slot, sa := range da.Slots {
			sb := db.Slots[slot]
			for logic, va := range sa {
				vb := sb[logic]
				if math.IsNaN(va) && math.IsNaN(vb) {
					continue
				}
				if va != vb {
					t.Errorf("device %s.slot[%d].%s: original=%v decompiled=%v", name, slot, logic, va, vb)
				}
			}
		}
		// The IC host's stack (db) holds the chip's persistent stack, which
		// legitimately differs between two layouts (return addresses, data
		// segment, spills); compare every other device's stack.
		if name == "db" {
			continue
		}
		for i, va := range da.Stack {
			vb := db.Stack[i]
			if math.IsNaN(va) && math.IsNaN(vb) {
				continue
			}
			if va != vb {
				t.Errorf("device %s.Stack[%d]: original=%v decompiled=%v", name, i, va, vb)
			}
		}
	}
}

func solarSetup(m *vm.Machine) {
	p1 := m.Device("p1")
	p1.Hash = vm.HashOf(-2045627372)
	p2 := m.Device("p2")
	p2.Hash = vm.HashOf(-295209029)
	m.Set("d0", "Vertical", 30)
	m.Set("d0", "Horizontal", 120)
	m.Set("d1", "Ratio", 0)
}

func batterySetup(m *vm.Machine) {
	b1 := m.Device("b1")
	b1.Hash = vm.HashOf(-400115994)
	b1.Values["Charge"] = 100
	b1.Values["Ratio"] = 0.5
	b2 := m.Device("b2")
	b2.Hash = vm.HashOf(-400115994)
	b2.Values["Charge"] = 200
	b2.Values["Ratio"] = 0.7
	l := m.Device("lights")
	l.Hash = vm.HashOf(797794350)
	m.Set("d0", "Activate", 1)
}

func TestDecompileSolar(t *testing.T) {
	roundTripWith(t, "../../testdata/ic10/solar.ic", solarSetup, decomp.Decompile)
}

func TestDecompileBattery(t *testing.T) {
	roundTripWith(t, "../../testdata/ic10/battery.ic", batterySetup, decomp.Decompile)
}

func TestDecompileStructuredSolar(t *testing.T) {
	roundTripWith(t, "../../testdata/ic10/solar.ic", solarSetup, decomp.DecompileStructured)
}

func TestDecompileStructuredBattery(t *testing.T) {
	roundTripWith(t, "../../testdata/ic10/battery.ic", batterySetup, decomp.DecompileStructured)
}

func TestDecompileSmoke(t *testing.T) {
	files, err := filepath.Glob("../../testdata/ic10/*.ic")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no .ic test files")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			code, warns, err := decomp.Decompile(string(src))
			if err != nil {
				t.Fatal(err)
			}
			if len(warns) > 0 {
				t.Errorf("unsupported instructions: %v", warns)
			}
			compiled, diags, err := ic10.Compile(f, []byte(code))
			if diags.HasErrors() || err != nil {
				t.Fatalf("decompiled source failed to compile: diags=%v err=%v\n%s", diags.Diags, err, code)
			}
			m := vm.New()
			if err := m.Load(compiled); err != nil {
				t.Fatal(err)
			}
			if err := m.Run(300); err != nil && err != vm.ErrStepLimit {
				t.Fatalf("decompiled run: %v", err)
			}
		})
	}
}

func TestDecompileStructuredSmoke(t *testing.T) {
	files, err := filepath.Glob("../../testdata/ic10/*.ic")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			code, _, err := decomp.DecompileStructured(string(src))
			if err != nil {
				t.Fatal(err)
			}
			compiled, diags, err := ic10.Compile(f, []byte(code))
			if diags.HasErrors() || err != nil {
				t.Fatalf("structured source failed to compile: diags=%v err=%v\n%s", diags.Diags, err, code)
			}
			orig := vm.New()
			if err := orig.Load(string(src)); err != nil {
				t.Fatal(err)
			}
			if err := orig.Run(3000); err != nil && err != vm.ErrStepLimit {
				t.Fatalf("original run: %v", err)
			}
			dec := vm.New()
			if err := dec.Load(compiled); err != nil {
				t.Fatal(err)
			}
			if err := dec.Run(3000); err != nil && err != vm.ErrStepLimit {
				t.Fatalf("structured run: %v", err)
			}
			compareDevices(t, orig, dec)
		})
	}
}

func roundTripWith(t *testing.T, path string, setup func(*vm.Machine), dec func(string) (string, []decomp.Warning, error)) {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code, warns, err := dec(string(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) > 0 {
		t.Fatalf("unsupported instructions: %v", warns)
	}
	compiled, diags, err := ic10.Compile(path, []byte(code))
	if diags.HasErrors() || err != nil {
		t.Fatalf("decompiled source failed to compile: diags=%v err=%v\n%s", diags.Diags, err, code)
	}

	orig := vm.New()
	setup(orig)
	if err := orig.Load(string(src)); err != nil {
		t.Fatal(err)
	}
	if err := orig.Run(3000); err != nil && err != vm.ErrStepLimit {
		t.Fatalf("original run: %v", err)
	}

	decVM := vm.New()
	setup(decVM)
	if err := decVM.Load(compiled); err != nil {
		t.Fatal(err)
	}
	if err := decVM.Run(3000); err != nil && err != vm.ErrStepLimit {
		t.Fatalf("decompiled run: %v", err)
	}

	compareDevices(t, orig, decVM)
}

// TestDecompileStructuredFallback checks that ic10.Decompile falls back to the
// flat form when the structured decompiler produces source that does not
// compile (the CLI's policy).
func TestDecompileStructuredFallback(t *testing.T) {
	requireIc10Code(t)
	files := corpusFiles(t, ".ic", ".ic10")
	tested := 0
	for _, path := range files {
		base := filepath.Base(path)
		if _, ok := knownUnsupported[base]; ok {
			continue
		}
		if ext := filepath.Ext(base); ext != "" {
			if _, ok := knownUnsupported[strings.TrimSuffix(base, ext)]; ok {
				continue
			}
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		st, _, _ := decomp.DecompileStructured(string(src))
		if _, diags, cerr := ic10.Compile(path, []byte(st)); !diags.HasErrors() && cerr == nil {
			continue // structured compiles; not a fallback case
		}
		code, _, err := ic10.Decompile(path, src, true)
		if err != nil {
			t.Fatalf("%s: fallback decompile: %v", base, err)
		}
		if _, diags, cerr := ic10.Compile(path, []byte(code)); diags.HasErrors() || cerr != nil {
			t.Errorf("%s: fallback output does not compile: %v", base, cerr)
		}
		tested++
	}
	if tested == 0 {
		t.Skip("no structured compile failures in the corpus")
	}
	t.Logf("checked %d structured fallbacks", tested)
}
