package ic10_test

import (
	"fmt"
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

// TestVMConditionalDefNotHoisted verifies that a variable conditionally
// assigned inside a loop (here returned from an inlined function) is not
// hoisted out of the loop by LICM.
func TestVMConditionalDefNotHoisted(t *testing.T) {
	src := `func unsafe() num {
    var u = 0
    if db.PressureExternal < 35 { u = 1 }
    if db.PressureExternal > 125 { u = 1 }
    return u
}
func main() {
    for {
        yield()
        d0.Setting = unsafe()
    }
}`
	m := runProgram(t, src, 50, func(m *vm.Machine) { m.Set("db", "PressureExternal", 200) })
	if got := m.Get("d0", "Setting"); got != 1 {
		t.Errorf("high pressure: Setting = %v, want 1", got)
	}
	m = runProgram(t, src, 50, func(m *vm.Machine) { m.Set("db", "PressureExternal", 100) })
	if got := m.Get("d0", "Setting"); got != 0 {
		t.Errorf("safe pressure: Setting = %v, want 0", got)
	}
}

// TestVMMultiBackEdgeLoop verifies that a condition using a variable assigned
// on another path through a loop with several back edges is not hoisted by
// LICM: the loop body must be treated as a whole.
func TestVMMultiBackEdgeLoop(t *testing.T) {
	src := `func main() {
    for {
        x := d0.Setting
        dt := ((x >> 4) ^ x) & 9
        x &= 15
        retry := true
        for retry {
            retry = false
            if x != 0 && ((x-1)&x) == 0 {
                if dt != 0 {
                    x = x * 2
                    retry = true
                    dt = 0
                } else {
                    d1.Setting = x
                }
            }
        }
        d2.Setting = dt
    }
}`
	m := runProgram(t, src, 300, func(m *vm.Machine) { m.Set("d0", "Setting", 130) })
	if got := m.Get("d1", "Setting"); got != 4 {
		t.Errorf("d1.Setting = %v, want 4 (loop-carried condition hoisted)", got)
	}
}

// TestVMBitwisePrecedence verifies the documented Go-style precedence where
// bitwise operators bind tighter than comparisons.
func TestVMBitwisePrecedence(t *testing.T) {
	m := runProgram(t, "func main() { x := 2\n d0.Setting = (x-1)&x == 0 }\n", 50, nil)
	if got := m.Get("d0", "Setting"); got != 1 {
		t.Errorf("(x-1)&x == 0 = %v, want 1", got)
	}
}

// TestVMCallRetKeepsCalleeWrites verifies that a value written by a call/ret
// routine is observed by the caller on the next iteration; LICM must not treat
// it as loop-invariant.
func TestVMCallRetKeepsCalleeWrites(t *testing.T) {
	src := `func main() {
    var x = 0
    for {
        yield()
        d0.Setting = x
        call f
    }
    label f:
    x = d1.Ratio
    ret
}`
	m := runProgram(t, src, 100, func(m *vm.Machine) { m.Set("d1", "Ratio", 0.42) })
	if got := m.Get("d0", "Setting"); got != 0.42 {
		t.Errorf("d0.Setting = %v, want 0.42 (callee write dropped)", got)
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
func TestVMRegisterSpilling(t *testing.T) {
	src := "func main() {"
	for i := 0; i < 17; i++ {
		src += fmt.Sprintf(" x%d := d%d.Temperature", i, i%6)
	}
	src += " d0.Setting ="
	for i := 0; i < 17; i++ {
		if i > 0 {
			src += " +"
		}
		src += fmt.Sprintf(" x%d", i)
	}
	src += " }\n"

	m := runProgram(t, src, 500, func(m *vm.Machine) {
		for i := 0; i < 6; i++ {
			m.Set(fmt.Sprintf("d%d", i), "Temperature", float64(i+1))
		}
	})
	// x0..x5 = 1..6, x6..x11 = 1..6, x12..x16 = 1..5 => 42 + 15 = 57.
	if got := m.Get("d0", "Setting"); got != 57 {
		t.Errorf("spilled sum = %v, want 57", got)
	}
}

func TestVMLoopInvariant(t *testing.T) {
	src := `func main() {
    a := d0.Temperature
    b := d1.Temperature
    var s = 0
    for i := 0; i < 5; i++ {
        x := a + b
        s += x + i
    }
    d2.Setting = s
}`
	m := runProgram(t, src, 500, func(m *vm.Machine) {
		m.Set("d0", "Temperature", 10)
		m.Set("d1", "Temperature", 20)
	})
	// x = 30 every iteration; s = sum(30+i) for i=0..4 = 150 + 10 = 160.
	if got := m.Get("d2", "Setting"); got != 160 {
		t.Errorf("loop-invariant result = %v, want 160", got)
	}
}

func TestVMDynamicLogic(t *testing.T) {
	code := mustCompile(t, "func main() { lt := 1\n d0.Setting = read(d1, lt)\n write(d1, lt, 5) }\n")
	m := vm.New()
	m.LogicByID[1] = "Temperature"
	m.Set("d1", "Temperature", 42)
	if err := m.Load(code); err != nil {
		t.Fatal(err)
	}
	if err := m.Run(50); err != nil && err != vm.ErrStepLimit {
		t.Fatal(err)
	}
	if got := m.Get("d0", "Setting"); got != 42 {
		t.Errorf("dynamic read = %v, want 42", got)
	}
	if got := m.Get("d1", "Temperature"); got != 5 {
		t.Errorf("dynamic write = %v, want 5", got)
	}
}

func TestVMDeviceValidity(t *testing.T) {
	code := mustCompile(t, "func main() { if !isLoadValid(d1, \"Temperature\") { d0.On = 0 } else { d0.On = 1 } }\n")
	valid := vm.New()
	valid.Set("d1", "Temperature", 1)
	if err := valid.Load(code); err != nil {
		t.Fatal(err)
	}
	if err := valid.Run(20); err != nil && err != vm.ErrStepLimit {
		t.Fatal(err)
	}
	if got := valid.Get("d0", "On"); got != 1 {
		t.Errorf("valid load -> On = %v, want 1", got)
	}
	invalid := vm.New()
	if err := invalid.Load(code); err != nil {
		t.Fatal(err)
	}
	if err := invalid.Run(20); err != nil && err != vm.ErrStepLimit {
		t.Fatal(err)
	}
	if got := invalid.Get("d0", "On"); got != 0 {
		t.Errorf("invalid load -> On = %v, want 0", got)
	}
}

func TestVMCSEInvalidation(t *testing.T) {
	// The second a+b must be recomputed because t was redefined.
	src := `func main() {
    a := d0.Temperature
    b := d1.Temperature
    t := a + b
    d2.Setting = t
    t = 5
    d4.Setting = t
    u := a + b
    d3.Setting = u
}`
	m := runProgram(t, src, 100, func(m *vm.Machine) {
		m.Set("d0", "Temperature", 3)
		m.Set("d1", "Temperature", 4)
	})
	if got := m.Get("d2", "Setting"); got != 7 {
		t.Errorf("d2 = %v, want 7", got)
	}
	if got := m.Get("d3", "Setting"); got != 7 {
		t.Errorf("d3 = %v, want 7 (stale CSE)", got)
	}
	if got := m.Get("d4", "Setting"); got != 5 {
		t.Errorf("d4 = %v, want 5", got)
	}
}

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
