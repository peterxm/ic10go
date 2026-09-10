package ic10_test

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"ic10go/internal/vm"
	"ic10go/pkg/ic10"
)

func runProgram(t *testing.T, src string, steps int, setup func(m *vm.Machine)) *vm.Machine {
	t.Helper()
	code := mustCompile(t, src)
	m := vm.New()
	if setup != nil {
		setup(m)
	}
	if err := m.Load(code); err != nil {
		t.Fatalf("vm load: %v", err)
	}
	if err := m.Run(steps); err != nil && err != vm.ErrStepLimit {
		t.Fatalf("vm run: %v", err)
	}
	return m
}

func TestVMTemperatureControl(t *testing.T) {
	src := "func main() { d0.On = d1.Temperature > 300 }\n"
	m := runProgram(t, src, 50, func(m *vm.Machine) { m.Set("d1", "Temperature", 350) })
	if got := m.Get("d0", "On"); got != 1 {
		t.Errorf("hot: On = %v, want 1", got)
	}
	m = runProgram(t, src, 50, func(m *vm.Machine) { m.Set("d1", "Temperature", 250) })
	if got := m.Get("d0", "On"); got != 0 {
		t.Errorf("cold: On = %v, want 0", got)
	}
}

func TestVMHysteresis(t *testing.T) {
	src := `const (
    MaxTemp = 296.15
    MinTemp = 283.15
)

func main() {
    var on = 0
    for {
        yield()
        t := d1.Temperature
        if t < MinTemp { on = 1 }
        if t > MaxTemp { on = 0 }
        d0.On = on
    }
}`
	hot := runProgram(t, src, 200, func(m *vm.Machine) { m.Set("d1", "Temperature", 300) })
	if got := hot.Get("d0", "On"); got != 0 {
		t.Errorf("hot: On = %v, want 0", got)
	}
	cold := runProgram(t, src, 200, func(m *vm.Machine) { m.Set("d1", "Temperature", 200) })
	if got := cold.Get("d0", "On"); got != 1 {
		t.Errorf("cold: On = %v, want 1", got)
	}
}

func TestVMArithmetic(t *testing.T) {
	src := "func main() { d0.Setting = (2 + 3) * 4 - 10 / 2 }\n"
	m := runProgram(t, src, 50, nil)
	if got := m.Get("d0", "Setting"); got != 15 {
		t.Errorf("Setting = %v, want 15", got)
	}
}

func TestVMTernary(t *testing.T) {
	src := "func main() { d0.Setting = d1.On != 0 ? 11 : 22 }\n"
	m := runProgram(t, src, 50, func(m *vm.Machine) { m.Set("d1", "On", 1) })
	if got := m.Get("d0", "Setting"); got != 11 {
		t.Errorf("true branch: %v, want 11", got)
	}
	m = runProgram(t, src, 50, func(m *vm.Machine) { m.Set("d1", "On", 0) })
	if got := m.Get("d0", "Setting"); got != 22 {
		t.Errorf("false branch: %v, want 22", got)
	}
}

func TestVMLoopSum(t *testing.T) {
	src := "func main() { var s = 0; for i := 0; i < 10; i++ { s += i }; d0.Setting = s }\n"
	m := runProgram(t, src, 500, nil)
	if got := m.Get("d0", "Setting"); got != 45 {
		t.Errorf("sum = %v, want 45", got)
	}
}

func TestVMInlining(t *testing.T) {
	src := "func add(a num, b num) num { return a + b }\nfunc main() { d0.Setting = add(2, 3) * add(4, 5) }\n"
	m := runProgram(t, src, 100, nil)
	if got := m.Get("d0", "Setting"); got != 45 {
		t.Errorf("Setting = %v, want 45", got)
	}
}

func TestVMStack(t *testing.T) {
	src := "func main() { push(5); push(7); d0.Setting = pop(); d1.Setting = peek(); d2.Setting = pop() }\n"
	m := runProgram(t, src, 50, nil)
	if m.Get("d0", "Setting") != 7 || m.Get("d1", "Setting") != 5 || m.Get("d2", "Setting") != 5 {
		t.Errorf("stack results: d0=%v d1=%v d2=%v", m.Get("d0", "Setting"), m.Get("d1", "Setting"), m.Get("d2", "Setting"))
	}
}

func TestVMBatch(t *testing.T) {
	src := `func main() {
    d0.Setting = batch.read(123, "Charge", "Sum")
    d1.Setting = batch.read(123, "Charge", "Average")
    d2.Setting = batch.read(123, "Charge", "Maximum")
    batch.write(123, "On", 1)
}`
	m := runProgram(t, src, 100, func(m *vm.Machine) {
		a := m.Device("a")
		a.Hash = 123
		a.Values["Charge"] = 10
		b := m.Device("b")
		b.Hash = 123
		b.Values["Charge"] = 20
	})
	if m.Get("d0", "Setting") != 30 {
		t.Errorf("sum = %v, want 30", m.Get("d0", "Setting"))
	}
	if m.Get("d1", "Setting") != 15 {
		t.Errorf("average = %v, want 15", m.Get("d1", "Setting"))
	}
	if m.Get("d2", "Setting") != 20 {
		t.Errorf("max = %v, want 20", m.Get("d2", "Setting"))
	}
	if m.Device("a").Values["On"] != 1 || m.Device("b").Values["On"] != 1 {
		t.Errorf("batch write did not reach both devices")
	}
}

func TestVMChannel(t *testing.T) {
	src := "func main() { d0.channel[1][3] = 42; d1.Setting = d0.channel[1][3] }\n"
	m := runProgram(t, src, 50, nil)
	if got := m.Get("d1", "Setting"); got != 42 {
		t.Errorf("channel = %v, want 42", got)
	}
}

func TestVMSlots(t *testing.T) {
	src := "func main() { d1.Setting = d0.slot[2].Occupied }\n"
	m := runProgram(t, src, 50, func(m *vm.Machine) { m.SetSlot("d0", 2, "Occupied", 7) })
	if got := m.Get("d1", "Setting"); got != 7 {
		t.Errorf("slot = %v, want 7", got)
	}
}

// TestVMSolarTracker runs the ported real-world solar tracker script.
func TestVMSolarTracker(t *testing.T) {
	src, err := os.ReadFile("../../testdata/programs/solar_tracker.icg")
	if err != nil {
		t.Fatal(err)
	}
	code, diags, err := ic10.Compile("solar_tracker.icg", src)
	if diags.HasErrors() || err != nil {
		t.Fatalf("diags=%v err=%v", diags.Diags, err)
	}
	m := vm.New()
	panel := m.Device("panel")
	panel.Hash = vm.HashOf(-539224550)
	m.Set("d0", "Vertical", 30)
	m.Set("d0", "Horizontal", 120)
	if err := m.Load(code); err != nil {
		t.Fatal(err)
	}
	if err := m.Run(50); err != nil && err != vm.ErrStepLimit {
		t.Fatal(err)
	}
	if got := panel.Values["Vertical"]; got != 60 {
		t.Errorf("Vertical = %v, want 60", got)
	}
	if got := panel.Values["Horizontal"]; got != 30 {
		t.Errorf("Horizontal = %v, want 30", got)
	}
}

// TestVMAllPrograms smoke-tests every testdata program in the VM.
func TestVMAllPrograms(t *testing.T) {
	programs, err := filepath.Glob("../../testdata/programs/*.icg")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range programs {
		name := filepath.Base(p)
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			code, diags, err := ic10.Compile(p, src)
			if diags.HasErrors() || err != nil {
				t.Fatalf("compile: diags=%v err=%v", diags.Diags, err)
			}
			m := vm.New()
			if err := m.Load(code); err != nil {
				t.Fatalf("load: %v", err)
			}
			if err := m.Run(500); err != nil && err != vm.ErrStepLimit {
				t.Fatalf("run: %v", err)
			}
		})
	}
}

// TestVMConstantFolding cross-checks compile-time folding against the VM.
func TestVMConstantFolding(t *testing.T) {
	cases := []struct {
		expr string
		want float64
	}{
		{"2 + 3 * 4", 14},
		{"(2 + 3) * 4", 20},
		{"10 % 3", 1},
		{"-7 % 3", 2},
		{"1 << 4", 16},
		{"255 & 15", 15},
		{"255 | 256", 511},
		{"255 ^ 15", 240},
		{"abs(0 - 9)", 9},
		{"min(3, 5)", 3},
		{"max(3, 5)", 5},
		{"sqrt(16)", 4},
	}
	for _, c := range cases {
		src := "func main() { d0.Setting = " + c.expr + " }\n"
		m := runProgram(t, src, 50, nil)
		got := m.Get("d0", "Setting")
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s = %v, want %v", c.expr, got, c.want)
		}
	}
}
