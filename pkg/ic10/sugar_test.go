package ic10_test

import (
	"testing"

	"ic10go/internal/vm"
	"ic10go/pkg/ic10"
)

// runDataSrc compiles src (which may contain a `data` table), runs the loader
// on a fresh machine, then runs the runtime with the loaded stack.
func runDataSrc(t *testing.T, src string, steps int, setup func(m *vm.Machine)) *vm.Machine {
	t.Helper()
	code, diags, err := ic10.Compile("t.icg", []byte(src))
	if err != nil || diags.HasErrors() {
		t.Fatalf("compile: %v %v", diags.Diags, err)
	}
	m := vm.New()
	if setup != nil {
		setup(m)
	}
	if loader, lerr := ic10.DataLoader("t.icg", []byte(src)); lerr != nil {
		t.Fatal(lerr)
	} else if loader != "" {
		lm := vm.New()
		if err := lm.Load(loader); err != nil {
			t.Fatal(err)
		}
		if err := lm.Run(1000); err != nil && err != vm.ErrStepLimit {
			t.Fatal(err)
		}
		copy(m.Stack, lm.Stack)
	}
	if err := m.Load(code); err != nil {
		t.Fatalf("vm load: %v", err)
	}
	if err := m.Run(steps); err != nil && err != vm.ErrStepLimit {
		t.Fatalf("vm run: %v", err)
	}
	return m
}

func TestVMRangeInt(t *testing.T) {
	src := `func main() {
    sum := 0
    for i := range 5 { sum += i }
    d0.Setting = sum
}`
	m := runProgram(t, src, 200, nil)
	if got := m.Get("d0", "Setting"); got != 10 {
		t.Errorf("range 5 sum = %v, want 10", got)
	}
}

func TestVMRangeRuntimeBound(t *testing.T) {
	src := `func main() {
    sum := 0
    n := d0.Setting
    for i := range n { sum += i }
    d1.Setting = sum
}`
	m := runProgram(t, src, 200, func(m *vm.Machine) { m.Set("d0", "Setting", 4) })
	if got := m.Get("d1", "Setting"); got != 6 {
		t.Errorf("range 4 sum = %v, want 6", got)
	}
}

func TestVMRangeTable(t *testing.T) {
	src := `data T = [10, 20, 30]
func main() {
    sum := 0
    for i, v := range T { sum += v }
    for _, v := range T { sum += v }
    d0.Setting = sum
}`
	m := runDataSrc(t, src, 300, nil)
	if got := m.Get("d0", "Setting"); got != 120 {
		t.Errorf("range table sum = %v, want 120", got)
	}
}

func TestVMRangeTableIndex(t *testing.T) {
	src := `data T = [4, 5, 6]
func main() {
    sum := 0
    for i := range T { sum += T[i] * i }
    d0.Setting = sum
}`
	m := runDataSrc(t, src, 300, nil)
	// 4*0 + 5*1 + 6*2 = 17
	if got := m.Get("d0", "Setting"); got != 17 {
		t.Errorf("range table index sum = %v, want 17", got)
	}
}

func TestVMSwitchRange(t *testing.T) {
	src := `func main() {
    switch d0.Setting {
    case 1..5: d1.Setting = 1
    case 6..10: d1.Setting = 2
    default: d1.Setting = 3
    }
}`
	cases := []struct {
		in, want float64
	}{{3, 1}, {5, 1}, {6, 2}, {10, 2}, {0, 3}, {11, 3}}
	for _, c := range cases {
		m := runProgram(t, src, 100, func(m *vm.Machine) { m.Set("d0", "Setting", c.in) })
		if got := m.Get("d1", "Setting"); got != c.want {
			t.Errorf("switch %v = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestVMIfInit(t *testing.T) {
	src := `func main() {
    if x := d0.Setting + 1; x > 5 {
        d1.Setting = x
    } else {
        d1.Setting = 0
    }
}`
	m := runProgram(t, src, 100, func(m *vm.Machine) { m.Set("d0", "Setting", 10) })
	if got := m.Get("d1", "Setting"); got != 11 {
		t.Errorf("if-init high = %v, want 11", got)
	}
	m = runProgram(t, src, 100, func(m *vm.Machine) { m.Set("d0", "Setting", 1) })
	if got := m.Get("d1", "Setting"); got != 0 {
		t.Errorf("if-init low = %v, want 0", got)
	}
}

func TestVMSwitchInit(t *testing.T) {
	src := `func main() {
    switch y := d0.Setting; y {
    case 1: d1.Setting = 100
    case 2: d1.Setting = 200
    default: d1.Setting = y
    }
}`
	m := runProgram(t, src, 100, func(m *vm.Machine) { m.Set("d0", "Setting", 2) })
	if got := m.Get("d1", "Setting"); got != 200 {
		t.Errorf("switch-init case 2 = %v, want 200", got)
	}
	m = runProgram(t, src, 100, func(m *vm.Machine) { m.Set("d0", "Setting", 9) })
	if got := m.Get("d1", "Setting"); got != 9 {
		t.Errorf("switch-init default = %v, want 9", got)
	}
}

func TestVMLabeledBreak(t *testing.T) {
	src := `func main() {
    count := 0
    label Outer:
    for i := 0; i < 5; i++ {
        for j := 0; j < 5; j++ {
            count += 1
            if j == 2 { break Outer }
        }
    }
    d0.Setting = count
}`
	m := runProgram(t, src, 300, nil)
	if got := m.Get("d0", "Setting"); got != 3 {
		t.Errorf("labeled break count = %v, want 3", got)
	}
}

func TestVMLabeledContinue(t *testing.T) {
	src := `func main() {
    count := 0
    label Outer:
    for i := 0; i < 3; i++ {
        for j := 0; j < 3; j++ {
            if j == 1 { continue Outer }
            count += 1
        }
    }
    d0.Setting = count
}`
	m := runProgram(t, src, 300, nil)
	if got := m.Get("d0", "Setting"); got != 3 {
		t.Errorf("labeled continue count = %v, want 3", got)
	}
}

func TestLabeledGotoStillWorks(t *testing.T) {
	// A `label L:` immediately before a loop is both a goto target and the
	// loop label.
	src := `func main() {
    n := 0
    label Retry:
    for i := 0; i < 3; i++ {
        n += 1
    }
    if n < 3 { goto Retry }
    d0.Setting = n
}`
	m := runProgram(t, src, 300, nil)
	if got := m.Get("d0", "Setting"); got != 3 {
		t.Errorf("goto loop label n = %v, want 3", got)
	}
}

func TestRangeCaseRejectedInTableSwitch(t *testing.T) {
	src := `func main() {
    switch d0.Setting table {
    case 1..3: d1.Setting = 1
    }
}`
	_, diags, _ := ic10.Compile("bad.icg", []byte(src))
	if !diags.HasErrors() {
		t.Error("expected an error for a range case in a table switch")
	}
}
