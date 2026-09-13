package builtin

import "testing"

func TestLogicTypesSynced(t *testing.T) {
	for _, n := range []string{"NameHash", "Channel0", "Channel7", "TemperatureSetting"} {
		if !LogicTypes[n] {
			t.Errorf("LogicTypes missing %q", n)
		}
	}
	if LogicTypes["None"] {
		t.Error("LogicTypes should not contain None")
	}
	if LogicTypes["DataNetwork"] || LogicTypes["TemperatureSettings"] {
		t.Error("LogicTypes contains stale entries")
	}
}

func TestSlotTypesSynced(t *testing.T) {
	for _, n := range []string{"PrefabHash", "SortingClass", "Mode", "FreeSlots", "TotalSlots"} {
		if !SlotTypes[n] {
			t.Errorf("SlotTypes missing %q", n)
		}
	}
}

func TestModeEnumConstants(t *testing.T) {
	cases := map[string]float64{
		"DisplayMode.Percent": 1,
		"DisplayMode.Degrees": 16,
		"PowerMode.Charging":  3,
		"Sound.Alarm1":        45,
		"Sound.StormIncoming": 18,
		"Color.Blue":          0,
		"Color.Purple":        11,
	}
	for k, v := range cases {
		if got := EnumConstants[k]; got != v {
			t.Errorf("EnumConstants[%q] = %v, want %v", k, got, v)
		}
	}
}

func TestLogicTypeIDsCoverChannels(t *testing.T) {
	for i := 0; i < 8; i++ {
		name := "Channel" + string(rune('0'+i))
		if LogicTypeIDs[name] == 0 {
			t.Errorf("LogicTypeIDs missing %q", name)
		}
	}
}
