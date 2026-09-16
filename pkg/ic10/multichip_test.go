package ic10_test

import (
	"testing"

	"ic10go/internal/vm"
	"ic10go/pkg/ic10"
)

// TestMultiChipBusEndToEnd compiles a two-chip bus program and runs both chips
// lockstep in a World, checking the consumer sees the producer's value even
// though the two chips use different access points.
func TestMultiChipBusEndToEnd(t *testing.T) {
	src := "bus B {\n    x num\n}\n" +
		"chip producer {\n    use B on db:0\n    func main() { for { yield(); B.x = d1.Pressure } }\n}\n" +
		"chip consumer {\n    use B on d2:1\n    func main() { for { yield(); d0.Setting = B.x } }\n}\n"
	res, diags, err := ic10.CompileResult("e2e.icg", []byte(src), ic10.Options{})
	if diags.HasErrors() {
		t.Fatalf("compile errors: %v", diags.Diags)
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Chips) != 2 {
		t.Fatalf("chips = %d, want 2", len(res.Chips))
	}

	w := vm.NewWorld()
	a := w.AddChip()
	b := w.AddChip()
	if err := a.Load(res.Chips[0].Code); err != nil {
		t.Fatalf("load producer: %v", err)
	}
	if err := b.Load(res.Chips[1].Code); err != nil {
		t.Fatalf("load consumer: %v", err)
	}
	// Wire the bus from the compiler's per-chip bindings (as `ic10c run` does).
	w.WireBus(res.Chips[0].Buses["B"], res.Chips[1].Buses["B"])
	w.Set("d1", "Pressure", 123)
	if err := w.Run(50); err != nil && err != vm.ErrStepLimit {
		t.Fatalf("run: %v", err)
	}
	if got := w.Get("d0", "Setting"); got != 123 {
		t.Errorf("consumer d0.Setting = %v, want 123 (bus value not delivered)", got)
	}
}
