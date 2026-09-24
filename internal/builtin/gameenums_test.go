package builtin

import (
	"strings"
	"testing"
)

// GameEnums is generated from the game's Assembly-CSharp.dll by tools/genenums.
// The tables in builtin.go are derived from it, so these tests are the guard:
// they fail when the generated data is missing or wrong, or when the derivation
// stops matching the game. See docs/target-ic10.md.

// TestGameEnumsSane pins a few known values so an empty, truncated or wrongly
// parsed generated file is caught before it silently disables checks.
func TestGameEnumsSane(t *testing.T) {
	if len(GameEnums) < 25 {
		t.Fatalf("GameEnums has %d groups, want 25+ (regenerate with tools/genenums)", len(GameEnums))
	}
	cases := map[string]map[string]int64{
		"Color":             {"Black": 7, "Purple": 11, "Blue": 0},
		"GasType":           {"Oxygen": 1, "CarbonDioxide": 4},
		"LogicSlotType":     {"Quantity": 3, "Occupied": 1},
		"LogicType":         {"On": 28, "Setting": 12, "Pressure": 5},
		"SorterInstruction": {"FilterPrefabHashEquals": 1},
		"Sound":             {"Alarm1": 45, "StormIncoming": 18},
		"SlotClass":         {"ProgrammableChip": 26},
	}
	for group, members := range cases {
		g, ok := GameEnums[group]
		if !ok {
			t.Errorf("GameEnums missing group %q", group)
			continue
		}
		for name, want := range members {
			if got, ok := g[name]; !ok || got != want {
				t.Errorf("GameEnums[%s][%s] = %d (present %v), want %d", group, name, got, ok, want)
			}
		}
	}
}

// TestLogicTypesDerived checks the device property set is exactly the game's
// LogicType enum minus None.
func TestLogicTypesDerived(t *testing.T) {
	want := setMinus(GameEnums["LogicType"], "None")
	diffSets(t, "LogicTypes", LogicTypes, want)
}

// TestSlotTypesDerived checks the slot property set is exactly the game's
// LogicSlotType enum minus None.
func TestSlotTypesDerived(t *testing.T) {
	want := setMinus(GameEnums["LogicSlotType"], "None")
	diffSets(t, "SlotTypes", SlotTypes, want)
}

// TestEnumConstantsDerived checks EnumConstants is exactly the game's groups
// (minus LogicType, whose members are device properties) plus enumExtras.
func TestEnumConstantsDerived(t *testing.T) {
	want := map[string]float64{}
	for group, members := range GameEnums {
		if group == "LogicType" {
			continue
		}
		for name, value := range members {
			want[group+"."+name] = float64(value)
		}
	}
	for k, v := range enumExtras {
		want[k] = v
	}

	for k, wv := range want {
		if got, ok := EnumConstants[k]; !ok || got != wv {
			t.Errorf("EnumConstants[%s] = %v (present %v), want %v", k, got, ok, wv)
		}
	}
	for k := range EnumConstants {
		if _, ok := want[k]; !ok {
			t.Errorf("EnumConstants has %s, which is neither a game enum nor an extra", k)
		}
	}
	if _, ok := enumExtras["SorterInstruction.NOP"]; !ok {
		t.Error("SorterInstruction.NOP should stay as a documented legacy alias")
	}
}

func setMinus(m map[string]int64, remove string) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		if k != remove {
			out[k] = true
		}
	}
	return out
}

func diffSets(t *testing.T, label string, got, want map[string]bool) {
	t.Helper()
	var missing, extra []string
	for k := range want {
		if !got[k] {
			missing = append(missing, k)
		}
	}
	for k := range got {
		if !want[k] {
			extra = append(extra, k)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%s missing from the game: %s", label, strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		t.Errorf("%s has names the game does not: %s", label, strings.Join(extra, ", "))
	}
}
