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

// TestSorterEnumConstants pins the Logic Sorter encoding against the game
// manual: OP codes 1..6, CONDOP 0..3, and the SlotClass/SortingClass operands
// used by FilterSlotTypeCompare / FilterSortingClassCompare.
func TestSorterEnumConstants(t *testing.T) {
	cases := map[string]float64{
		"SorterInstruction.None":                      0,
		"SorterInstruction.NOP":                       0,
		"SorterInstruction.FilterPrefabHashEquals":    1,
		"SorterInstruction.FilterPrefabHashNotEquals": 2,
		"SorterInstruction.FilterSortingClassCompare": 3,
		"SorterInstruction.FilterSlotTypeCompare":     4,
		"SorterInstruction.FilterQuantityCompare":     5,
		"SorterInstruction.LimitNextExecutionByCount": 6,
		"Equals":                                  0,
		"Greater":                                 1,
		"Less":                                    2,
		"NotEquals":                               3,
		"ConditionOperation.Equals":               0,
		"ConditionOperation.NotEquals":            3,
		"SlotClass.Battery":                       14,
		"SlotClass.AutoInjector":                  43,
		"SortingClass.Ores":                       9,
		"SortingClass.Ices":                       10,
		"LogicReagentMode.Recipe":                 2,
		"PrinterInstruction.None":                 0,
		"PrinterInstruction.StackPointer":         1,
		"PrinterInstruction.ExecuteRecipe":        2,
		"PrinterInstruction.WaitUntilNextValid":   3,
		"PrinterInstruction.MissingRecipeReagent": 9,
		"TraderInstruction.FilterGasNotContains":  18,
		"Stack.Size":                              512,
		"SorterStack.Size":                        32,
		"PrinterStack.StackPointer":               63,
		"PrinterStack.MissingRecipeReagent":       54,
		// Device mode / misc enums synced from the live game.
		"AirCon.Cold":                 0,
		"AirControl.Pressure":         2,
		"ElevatorMode.Upward":         1,
		"EntityState.Decay":           3,
		"FiltrationMode.Active":       1,
		"GasType.Oxygen":              1,
		"HashType.GasLiquid":          1,
		"LogicBatchMethod.Count":      4,
		"LogicSlotType.Occupied":      1,
		"NodeType.LaunchPad":          4,
		"ReEntryProfile.High":         3,
		"RobotMode.Follow":            1,
		"RocketMode.Survey":           3,
		"SettingDisplayMode.String":   1,
		"ShuttleType.Large":           5,
		"TransmitterMode.Active":      1,
		"Vent.Inward":                 1,
		"DaylightSensorMode.Vertical": 2,
	}
	for k, v := range cases {
		if got := EnumConstants[k]; got != v {
			t.Errorf("EnumConstants[%q] = %v, want %v", k, got, v)
		}
	}
	for _, name := range []string{"pi", "deg2rad", "rad2deg", "epsilon"} {
		if !RawConstants[name] {
			t.Errorf("RawConstants missing %q", name)
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
