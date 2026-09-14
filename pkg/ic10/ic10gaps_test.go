package ic10_test

import (
	"testing"

	"ic10go/internal/vm"
)

func TestVMClrById(t *testing.T) {
	src := `func main() {
    id := d0.ReferenceId
    putd(id, 0, 5)
    before := getd(id, 0)
    clrById(id)
    after := getd(id, 0)
    d1.Setting = before
    d2.Setting = after
}`
	m := runProgram(t, src, 100, func(m *vm.Machine) { m.Set("d0", "ReferenceId", 99) })
	if got := m.Get("d1", "Setting"); got != 5 {
		t.Errorf("before clear = %v, want 5", got)
	}
	if got := m.Get("d2", "Setting"); got != 0 {
		t.Errorf("after clear = %v, want 0", got)
	}
}

func TestVMReadReagent(t *testing.T) {
	src := `func main() {
    d1.Setting = readReagent(d0, ReagentMode.Contents, 7)
}`
	m := runProgram(t, src, 50, func(m *vm.Machine) {
		m.Device("d0").Reagents[7] = 42
	})
	if got := m.Get("d1", "Setting"); got != 42 {
		t.Errorf("readReagent = %v, want 42", got)
	}
}

func TestVMBitwiseAndApproxValues(t *testing.T) {
	src := `func main() {
    a := d0.Setting
    b := d1.Setting
    d2.Setting = logicalNor(a, b)
    d3.Setting = notApprox(a, b, 0.001)
    d4.Setting = notApproxZero(a, 0.001)
    d5.Setting = isNotNaN(a)
}`
	m := runProgram(t, src, 50, func(m *vm.Machine) {
		m.Set("d0", "Setting", 0)
		m.Set("d1", "Setting", 0)
	})
	if got := m.Get("d2", "Setting"); got != -1 { // ^(0|0) = -1
		t.Errorf("logicalNor(0,0) = %v, want -1", got)
	}
	if got := m.Get("d3", "Setting"); got != 0 { // 0 ≈ 0
		t.Errorf("notApprox(0,0) = %v, want 0", got)
	}
	if got := m.Get("d4", "Setting"); got != 0 { // 0 ≈ 0
		t.Errorf("notApproxZero(0) = %v, want 0", got)
	}
	if got := m.Get("d5", "Setting"); got != 1 {
		t.Errorf("isNotNaN(0) = %v, want 1", got)
	}
}

func TestVMApproxBranch(t *testing.T) {
	src := `func main() {
    a := d0.Setting
    b := d1.Setting
    if approx(a, b, 0.001) { d2.Setting = 1 } else { d2.Setting = 2 }
    if notApprox(a, b, 0.001) { d3.Setting = 1 } else { d3.Setting = 2 }
    if approxZero(a, 0.001) { d4.Setting = 1 } else { d4.Setting = 2 }
    if notApproxZero(a, 0.001) { d5.Setting = 1 } else { d5.Setting = 2 }
}`
	check := func(name string, av, bv float64, d2, d3, d4, d5 float64) {
		t.Helper()
		m := runProgram(t, src, 100, func(m *vm.Machine) {
			m.Set("d0", "Setting", av)
			m.Set("d1", "Setting", bv)
		})
		if got := m.Get("d2", "Setting"); got != d2 {
			t.Errorf("%s: approx d2 = %v, want %v", name, got, d2)
		}
		if got := m.Get("d3", "Setting"); got != d3 {
			t.Errorf("%s: notApprox d3 = %v, want %v", name, got, d3)
		}
		if got := m.Get("d4", "Setting"); got != d4 {
			t.Errorf("%s: approxZero d4 = %v, want %v", name, got, d4)
		}
		if got := m.Get("d5", "Setting"); got != d5 {
			t.Errorf("%s: notApproxZero d5 = %v, want %v", name, got, d5)
		}
	}
	check("equal", 5, 5, 1, 2, 2, 1)
	check("zero", 0, 0, 1, 2, 1, 2)
	check("differ", 1, 2, 2, 1, 2, 1)
}
