package vm

import (
	"errors"
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

func runErr(t *testing.T, m *Machine, src string, steps int) error {
	t.Helper()
	if err := m.Load(src); err != nil {
		t.Fatalf("load: %v", err)
	}
	return m.Run(steps)
}

func TestStackBoundsError(t *testing.T) {
	cases := []struct {
		src  string
		want error
	}{
		{"poke 600 1", ErrStackOverflow},
		{"poke -1 1", ErrStackUnderflow},
		{"get r0 d0 600", ErrStackOverflow},
		{"put d0 -1 1", ErrStackUnderflow},
	}
	for _, c := range cases {
		if err := runErr(t, New(), c.src, 10); !errors.Is(err, c.want) {
			t.Errorf("%q: err = %v, want %v", c.src, err, c.want)
		}
	}
}

func TestOperandCountError(t *testing.T) {
	for _, src := range []string{"add r0 1", "move r0", "s d0"} {
		if err := runErr(t, New(), src, 10); !errors.Is(err, ErrOperandCount) {
			t.Errorf("%q: err = %v, want ErrOperandCount", src, err)
		}
	}
}

func TestRawConstants(t *testing.T) {
	m := run(t, "move r0 pi\nmove r1 deg2rad\nmove r2 rad2deg\nmove r3 epsilon\ns d0 A r0\ns d0 B r1\ns d0 C r2\ns d0 D r3", 20)
	if got := m.Get("d0", "A"); got != math.Pi {
		t.Errorf("pi = %v, want %v", got, math.Pi)
	}
	if got := m.Get("d0", "C"); got != 57.2957801818848 {
		t.Errorf("rad2deg = %v", got)
	}
}

func TestRandDeterministic(t *testing.T) {
	src := "rand r0\nrand r1\ns d0 A r0\ns d0 B r1"
	a := run(t, src, 10)
	b := run(t, src, 10)
	if a.Get("d0", "A") != b.Get("d0", "A") || a.Get("d0", "B") != b.Get("d0", "B") {
		t.Errorf("rand not deterministic: %v/%v vs %v/%v",
			a.Get("d0", "A"), a.Get("d0", "B"), b.Get("d0", "A"), b.Get("d0", "B"))
	}
	if v := a.Get("d0", "A"); v < 0 || v >= 1 {
		t.Errorf("rand = %v, want [0,1)", v)
	}
	c := New()
	c.SetSeed(123)
	if err := c.Load("rand r0\ns d0 A r0"); err != nil {
		t.Fatal(err)
	}
	_ = c.Run(10)
	if c.Get("d0", "A") == a.Get("d0", "A") {
		t.Errorf("different seeds produced the same value")
	}
}

func TestRmap(t *testing.T) {
	m := New()
	m.Reagents[111] = 222
	if err := m.Load("rmap r0 d0 111\ns d0 A r0"); err != nil {
		t.Fatal(err)
	}
	if err := m.Run(10); err != nil && err != ErrStepLimit {
		t.Fatal(err)
	}
	if got := m.Get("d0", "A"); got != 222 {
		t.Errorf("rmap = %v, want 222", got)
	}
}

func TestLineNumber(t *testing.T) {
	m := run(t, "move r0 0\nl r1 db LineNumber\ns d0 A r1", 10)
	if got := m.Get("d0", "A"); got != 1 {
		t.Errorf("LineNumber = %v, want 1 (the l instruction's line)", got)
	}
}

func TestStrictDevice(t *testing.T) {
	m := New()
	m.Strict = true
	if err := runErr(t, m, "l r0 d0 On", 10); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("strict unset device err = %v, want ErrDeviceNotFound", err)
	}
	m2 := New()
	m2.Strict = true
	if err := runErr(t, m2, "ld r0 9999 On", 10); !errors.Is(err, ErrUnknownDeviceID) {
		t.Errorf("strict unknown id err = %v, want ErrUnknownDeviceID", err)
	}
	// Default (lenient) still reads 0 from an auto-created device.
	got := run(t, "l r0 d0 On\ns d0 A r0", 10).Get("d0", "A")
	if got != 0 {
		t.Errorf("lenient unset device = %v, want 0", got)
	}
}

func TestWorldChannelSharing(t *testing.T) {
	w := NewWorld()
	a := w.AddChip()
	b := w.AddChip()
	if err := a.Load("s db:0 Channel0 42\nj 0\n"); err != nil {
		t.Fatalf("load a: %v", err)
	}
	if err := b.Load("l r0 db:0 Channel0\ns d0 Setting r0\nj 0\n"); err != nil {
		t.Fatalf("load b: %v", err)
	}
	if err := w.Run(30); err != nil && err != ErrStepLimit {
		t.Fatalf("run: %v", err)
	}
	if got := w.Get("d0", "Setting"); got != 42 {
		t.Errorf("d0.Setting = %v, want 42 (channel not shared)", got)
	}
}

func TestWorldSeparateStacks(t *testing.T) {
	w := NewWorld()
	a := w.AddChip()
	b := w.AddChip()
	// Each chip pokes its own stack; the values must not collide.
	if err := a.Load("poke 0 111\nget r0 db 0\ns d0 Setting r0\nj 0\n"); err != nil {
		t.Fatalf("load a: %v", err)
	}
	if err := b.Load("poke 0 222\nget r0 db 0\ns d1 Setting r0\nj 0\n"); err != nil {
		t.Fatalf("load b: %v", err)
	}
	if err := w.Run(30); err != nil && err != ErrStepLimit {
		t.Fatalf("run: %v", err)
	}
	if got := w.Get("d0", "Setting"); got != 111 {
		t.Errorf("chip a stack = %v, want 111", got)
	}
	if got := w.Get("d1", "Setting"); got != 222 {
		t.Errorf("chip b stack = %v, want 222", got)
	}
}

func TestWorldWire(t *testing.T) {
	w := NewWorld()
	a := w.AddChip()
	b := w.AddChip()
	if err := a.Load("s db:0 Channel0 42\nj 0\n"); err != nil {
		t.Fatalf("load a: %v", err)
	}
	if err := b.Load("l r0 d2:1 Channel0\ns d0 Setting r0\nj 0\n"); err != nil {
		t.Fatalf("load b: %v", err)
	}
	w.Wire("db:0", "d2:1")
	if err := w.Run(30); err != nil && err != ErrStepLimit {
		t.Fatalf("run: %v", err)
	}
	if got := w.Get("d0", "Setting"); got != 42 {
		t.Errorf("d0.Setting = %v, want 42 (wired access points not shared)", got)
	}
}

func TestForwardAliasResolution(t *testing.T) {
	// IC10 aliases are position independent: `self` is aliased to db after it
	// is used, so the write must land on db rather than a device named "self".
	src := "s self Mode 1\nalias self db\n"
	m := run(t, src, 10)
	if got := m.Get("db", "Mode"); got != 1 {
		t.Errorf("db.Mode = %v, want 1", got)
	}
	if _, ok := m.Devices["self"]; ok {
		t.Errorf("created a device named %q; alias was not resolved", "self")
	}
}

func TestForwardDefineResolution(t *testing.T) {
	// A define used above its definition must still be substituted.
	src := "move r0 LIMIT\ns d0 Setting r0\ndefine LIMIT 42\n"
	m := run(t, src, 10)
	if got := m.Get("d0", "Setting"); got != 42 {
		t.Errorf("d0.Setting = %v, want 42", got)
	}
}
