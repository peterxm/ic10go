package ic10_test

import (
	"strings"
	"testing"
)

// TestGameEnums compiles a sample of the game enums added from the live game
// (StationeersIC10Editor reflection / Stationeers-ic consts) and checks the
// numeric values.
func TestGameEnums(t *testing.T) {
	src := `func main() {
    d0.Mode = AirCon.Cold
    d1.Mode = AirControl.Pressure
    d2.Mode = ElevatorMode.Upward
    d3.Mode = RobotMode.Follow
    d4.Mode = RocketMode.Survey
    d5.Mode = ShuttleType.Large
    db.Mode = Vent.Inward
    d0.Setting = GasType.Oxygen
    d1.Setting = LogicSlotType.Occupied
    d2.Setting = EntityState.Decay
    d3.Setting = HashType.GasLiquid
    d4.Setting = ReEntryProfile.High
    d5.Setting = NodeType.LaunchPad
    db.Setting = TransmitterMode.Active + FiltrationMode.Active + DaylightSensorMode.Vertical + SettingDisplayMode.String
}`
	code := mustCompile(t, src)
	for _, want := range []string{
		"s d0 Mode 0",    // AirCon.Cold
		"s d1 Mode 2",    // AirControl.Pressure
		"s d2 Mode 1",    // ElevatorMode.Upward
		"s d3 Mode 1",    // RobotMode.Follow
		"s d4 Mode 3",    // RocketMode.Survey
		"s d5 Mode 5",    // ShuttleType.Large
		"s db Mode 1",    // Vent.Inward
		"s d0 Setting 1", // GasType.Oxygen
		"s d1 Setting 1", // LogicSlotType.Occupied
		"s d2 Setting 3", // EntityState.Decay
		"s d3 Setting 1", // HashType.GasLiquid
		"s d4 Setting 3", // ReEntryProfile.High
		"s d5 Setting 4", // NodeType.LaunchPad
		"s db Setting 5", // 1 + 1 + 2 + 1
	} {
		if !strings.Contains(code, want) {
			t.Errorf("code missing %q:\n%s", want, code)
		}
	}
}

// TestMathConstants checks the game numeric constants are emitted verbatim.
func TestMathConstants(t *testing.T) {
	src := "func main() { d0.Setting = pi * deg2rad + rad2deg - epsilon }\n"
	code := mustCompile(t, src)
	for _, want := range []string{"pi", "deg2rad", "rad2deg", "epsilon"} {
		if !strings.Contains(code, want) {
			t.Errorf("code missing %q:\n%s", want, code)
		}
	}
}

// TestBatchModeCount checks the game's Count(4) batch mode resolves to 4.
func TestBatchModeCount(t *testing.T) {
	src := `func main() { d0.Setting = batch.read(hash("StructureBattery"), "Ratio", "Count") }`
	code := mustCompile(t, src)
	if !strings.Contains(code, "lb ") || !strings.Contains(code, " 4") {
		t.Errorf("code = %q, want an lb with batch mode 4", code)
	}
}
