package ic10_test

import (
	"testing"

	"ic10go/internal/vm"
	"ic10go/pkg/ic10"
)

// newBusWorld compiles a multi-chip bus program, wires each slot's access points
// (as `ic10c run` does) and returns the ready-to-run world.
func newBusWorld(t *testing.T, src string) *vm.World {
	t.Helper()
	res, diags, err := ic10.CompileResult("e2e.icg", []byte(src), ic10.Options{})
	if diags.HasErrors() {
		t.Fatalf("compile errors: %v", diags.Diags)
	}
	if err != nil {
		t.Fatal(err)
	}
	w := vm.NewWorld()
	for _, ch := range res.Chips {
		m := w.AddChip()
		if err := m.Load(ch.Code); err != nil {
			t.Fatalf("load chip %s: %v", ch.Name, err)
		}
	}
	busAccess := map[string][]string{}
	for _, ch := range res.Chips {
		for slot, conns := range ch.BusAccess {
			busAccess[slot] = append(busAccess[slot], conns...)
		}
	}
	for _, conns := range busAccess {
		w.Wire(conns...)
	}
	return w
}

// TestMultiChipBusDefault checks the default access point (`use`) is used when
// the slot is accessed without an inline `[dev][conn]`.
func TestMultiChipBusDefault(t *testing.T) {
	src := "bus B {\n    x num\n}\n" +
		"chip producer {\n    use B on db:0\n    func main() { for { yield(); B.x = d1.Pressure } }\n}\n" +
		"chip consumer {\n    use B on d2:1\n    func main() { for { yield(); d0.Setting = B.x } }\n}\n"
	w := newBusWorld(t, src)
	w.Set("d1", "Pressure", 123)
	if err := w.Run(50); err != nil && err != vm.ErrStepLimit {
		t.Fatalf("run: %v", err)
	}
	if got := w.Get("d0", "Setting"); got != 123 {
		t.Errorf("consumer d0.Setting = %v, want 123 (default access point)", got)
	}
}

// TestMultiChipBusInline checks the inline access point `Bus.slot[dev][conn]`
// overrides the default and reaches the same channel.
func TestMultiChipBusInline(t *testing.T) {
	src := "bus B {\n    x num\n}\n" +
		"chip producer {\n    use B on db:0\n    func main() { for { yield(); B.x[d5][1] = d1.Pressure } }\n}\n" +
		"chip consumer {\n    func main() { for { yield(); d0.Setting = B.x[d3][0] } }\n}\n"
	w := newBusWorld(t, src)
	w.Set("d1", "Pressure", 123)
	if err := w.Run(50); err != nil && err != vm.ErrStepLimit {
		t.Fatalf("run: %v", err)
	}
	if got := w.Get("d0", "Setting"); got != 123 {
		t.Errorf("consumer d0.Setting = %v, want 123 (inline access point)", got)
	}
}
