package vm

import (
	"math"
	"testing"
)

func run(t *testing.T, src string, steps int) *Machine {
	t.Helper()
	m := New()
	if err := m.Load(src); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := m.Run(steps); err != nil && err != ErrStepLimit {
		t.Fatalf("run: %v", err)
	}
	return m
}

func TestArithmetic(t *testing.T) {
	m := run(t, "move r0 2\nadd r1 r0 3\nmul r2 r1 4\ns d0 Setting r2", 10)
	if got := m.Get("d0", "Setting"); got != 20 {
		t.Errorf("Setting = %v, want 20", got)
	}
}

func TestLabelLoop(t *testing.T) {
	src := "move r0 0\nmove r1 0\nL:\nadd r1 r1 r0\nadd r0 r0 1\nblt r0 5 L\ns d0 Setting r1"
	m := run(t, src, 100)
	if got := m.Get("d0", "Setting"); got != 10 {
		t.Errorf("sum = %v, want 10", got)
	}
}

func TestAbsJumpTargets(t *testing.T) {
	// r0 != 0 so beqz does not jump; fall through to line 2.
	src := "move r0 1\nbeqz r0 3\nmove r1 99\ns d0 Setting r1"
	m := run(t, src, 10)
	if got := m.Get("d0", "Setting"); got != 99 {
		t.Errorf("Setting = %v, want 99", got)
	}

	// r0 == 0 so beqz jumps over line 2.
	src = "move r0 0\nbeqz r0 3\nmove r1 99\ns d0 Setting r1"
	m = run(t, src, 10)
	if got := m.Get("d0", "Setting"); got != 0 {
		t.Errorf("Setting = %v, want 0", got)
	}
}

func TestStackOps(t *testing.T) {
	m := run(t, "push 3\npush 4\npop r0\npeek r1\ns d0 A r0\ns d1 B r1", 10)
	if m.Get("d0", "A") != 4 || m.Get("d1", "B") != 3 {
		t.Errorf("A=%v B=%v, want 4 and 3", m.Get("d0", "A"), m.Get("d1", "B"))
	}
}

func TestBatchAggregate(t *testing.T) {
	src := "lb r0 7 Charge 1\nlb r1 7 Charge 0\nlb r2 7 Charge 3\ns d0 Sum r0\ns d1 Avg r1\ns d2 Max r2"
	m := New()
	a := m.Device("a")
	a.Hash = 7
	a.Values["Charge"] = 2
	b := m.Device("b")
	b.Hash = 7
	b.Values["Charge"] = 4
	if err := m.Load(src); err != nil {
		t.Fatal(err)
	}
	if err := m.Run(20); err != nil {
		t.Fatal(err)
	}
	if m.Get("d0", "Sum") != 6 || m.Get("d1", "Avg") != 3 || m.Get("d2", "Max") != 4 {
		t.Errorf("sum=%v avg=%v max=%v", m.Get("d0", "Sum"), m.Get("d1", "Avg"), m.Get("d2", "Max"))
	}
}

func TestEmptyBatchAverageIsNaN(t *testing.T) {
	m := run(t, "lb r0 99 Charge 0\ns d0 A r0", 10)
	if !math.IsNaN(m.Get("d0", "A")) {
		t.Errorf("empty average = %v, want NaN", m.Get("d0", "A"))
	}
}

func TestExtIns(t *testing.T) {
	m := run(t, "move r0 4660\next r1 r0 8 8\ns d0 A r1", 10)
	if got := m.Get("d0", "A"); got != 18 {
		t.Errorf("ext = %v, want 18", got)
	}
	m = run(t, "move r0 0\nins r0 171 8 8\ns d0 B r0", 10)
	if got := m.Get("d0", "B"); got != 171*256 {
		t.Errorf("ins = %v, want %v", got, 171*256)
	}
}
