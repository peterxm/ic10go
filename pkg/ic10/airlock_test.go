package ic10_test

import (
	"os"
	"strings"
	"testing"

	"ic10go/internal/builtin"
	"ic10go/internal/vm"
	"ic10go/pkg/ic10"
)

// addAirlockDevice registers a device with the prefab and name hashes the
// four-door example uses with batch IO, and marks it Idle.
func addAirlockDevice(m *vm.Machine, prefab, label string) {
	d := m.Device(label)
	d.Hash = builtin.Hash(prefab)
	d.NameHash = builtin.Hash(label)
	d.Values["Idle"] = 1
}

func compileExample(t *testing.T, path string) ic10.Result {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("example not present: %v", err)
	}
	res, diags, err := ic10.CompileResult(path, src, ic10.Options{})
	if err != nil || diags.HasErrors() {
		t.Fatalf("compile: %v %v", diags.Diags, err)
	}
	return res
}

// TestAirlockExampleTwoDoor drives the port-based improved two-door example: the
// doors/vents/sensor are on the fixed ports d0..d5, as in the original script.
func TestAirlockExampleTwoDoor(t *testing.T) {
	res := compileExample(t, "../../examples/气闸控制-双门.icg")
	m := vm.New()
	for _, n := range []string{"d0", "d1", "d2", "d3"} {
		m.Device(n).Values["Idle"] = 1
	}
	m.Device("d4").Values["Pressure"] = 0 // vacuum, so a cycle completes
	if err := m.Load(res.Code); err != nil {
		t.Fatal(err)
	}
	if err := m.Run(2000); err != nil && err != vm.ErrStepLimit {
		t.Fatal(err)
	}

	// After init, the entrance door (d1) is open and the exit door (d0) closed.
	if got := m.Get("d1", "Open"); got != 1 {
		t.Fatalf("after init d1.Open = %v, want 1", got)
	}
	if got := m.Get("d0", "Open"); got != 0 {
		t.Fatalf("after init d0.Open = %v, want 0", got)
	}

	// Press the exit door's switch: the machine closes the entrance, evacuates
	// and opens the exit door.
	m.Set("d0", "Setting", 1)
	if err := m.Run(3000); err != nil && err != vm.ErrStepLimit {
		t.Fatal(err)
	}
	if got := m.Get("d0", "Open"); got != 1 {
		t.Errorf("after cycle d0.Open = %v, want 1", got)
	}
	if got := m.Get("d1", "Open"); got != 0 {
		t.Errorf("after cycle d1.Open = %v, want 0", got)
	}
}

// TestAirlockExampleFourDoor drives the batch-addressed four-door example.
func TestAirlockExampleFourDoor(t *testing.T) {
	res := compileExample(t, "../../examples/气闸控制-四门.icg")
	doors := []string{"DoorNorth", "DoorEast", "DoorSouth", "DoorWest"}
	vents := []string{"VentNorth", "VentEast", "VentSouth", "VentWest"}
	lights := []string{"LightNorth", "LightEast", "LightSouth", "LightWest"}
	m := vm.New()
	for _, n := range doors {
		addAirlockDevice(m, "StructureCompositeDoor", n)
	}
	for _, n := range vents {
		addAirlockDevice(m, "StructureActiveVent", n)
	}
	for _, n := range lights {
		addAirlockDevice(m, "StructureLightLong", n)
	}
	s := m.Device("AirSensor")
	s.Hash = builtin.Hash("StructureGasSensor")
	s.NameHash = builtin.Hash("AirSensor")
	s.Values["Pressure"] = 0

	// Install the data-segment loader, then run the runtime.
	for _, chunk := range res.Loaders {
		if err := m.Load(chunk); err != nil {
			t.Fatal(err)
		}
		if err := m.Run(strings.Count(chunk, "\n") + 1); err != nil && err != vm.ErrStepLimit {
			t.Fatal(err)
		}
	}
	if err := m.Load(res.Code); err != nil {
		t.Fatal(err)
	}
	if err := m.Run(400); err != nil && err != vm.ErrStepLimit {
		t.Fatal(err)
	}

	// After init, only the north door is open.
	if got := m.Get("DoorNorth", "Open"); got != 1 {
		t.Fatalf("after init DoorNorth.Open = %v, want 1", got)
	}
	for _, n := range []string{"DoorEast", "DoorSouth", "DoorWest"} {
		if got := m.Get(n, "Open"); got != 0 {
			t.Errorf("after init %s.Open = %v, want 0", n, got)
		}
	}

	// Request the west door (index 3): north closes, west opens.
	m.Set("DoorWest", "Setting", 1)
	if err := m.Run(400); err != nil && err != vm.ErrStepLimit {
		t.Fatal(err)
	}
	if got := m.Get("DoorWest", "Open"); got != 1 {
		t.Errorf("after cycle DoorWest.Open = %v, want 1", got)
	}
	if got := m.Get("DoorNorth", "Open"); got != 0 {
		t.Errorf("after cycle DoorNorth.Open = %v, want 0", got)
	}
}
