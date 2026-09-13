// Package builtin holds compile-time tables: IC10 logic types, slot types and
// built-in function signatures.
package builtin

import (
	"hash/crc32"
	"sort"
)

// Hash returns the CRC-32 checksum used by IC10's HASH() function.
func Hash(s string) uint32 { return crc32.ChecksumIEEE([]byte(s)) }

// EnumConstants are game enum constants that IC10 source may reference by their
// dotted name (e.g. SorterInstruction.FilterPrefabHashEquals). Values follow
// the Stationeers sorter instruction encoding and the reference doc; verify
// against the game before relying on them for new work.
var EnumConstants = map[string]float64{
	"SorterInstruction.FilterPrefabHashEquals":    1,
	"SorterInstruction.FilterPrefabHashNotEquals": 2,
	"SorterInstruction.FilterSlotTypeCompare":     3,
	"SorterInstruction.FilterSortingClassCompare": 4,
	"SlotClass.Battery":                           1,
	"SortingClass.Ores":                           1,
	// ReagentMode: Contents / Required / Recipe = 0 / 1 / 2 (reference doc).
	"ReagentMode.Contents": 0,
	"ReagentMode.Required": 1,
	"ReagentMode.Recipe":   2,
	// PrinterInstruction: 8-bit OP codes; verify against the game.
	"PrinterInstruction.ExecuteRecipe":      1,
	"PrinterInstruction.WaitUntilNextValid": 2,
	// Color: LogicType.Color device color (game enum ColorType). Values above 11
	// clamp to Purple and values below 0 clamp to Blue.
	"Color.Blue":   0,
	"Color.Gray":   1,
	"Color.Green":  2,
	"Color.Orange": 3,
	"Color.Red":    4,
	"Color.Yellow": 5,
	"Color.White":  6,
	"Color.Black":  7,
	"Color.Brown":  8,
	"Color.Khaki":  9,
	"Color.Pink":   10,
	"Color.Purple": 11,
	// PowerMode: Area Power Controller charge state.
	"PowerMode.Idle":        0,
	"PowerMode.Discharged":  1,
	"PowerMode.Discharging": 2,
	"PowerMode.Charging":    3,
	"PowerMode.Charged":     4,
	// DisplayMode: LED display readout mode (LogicDisplay.DisplayMode).
	"DisplayMode.Default":    0,
	"DisplayMode.Percent":    1,
	"DisplayMode.Power":      2,
	"DisplayMode.Kelvin":     3,
	"DisplayMode.Celsius":    4,
	"DisplayMode.Meters":     5,
	"DisplayMode.Credits":    6,
	"DisplayMode.Seconds":    7,
	"DisplayMode.Minutes":    8,
	"DisplayMode.Days":       9,
	"DisplayMode.String":     10,
	"DisplayMode.Fahrenheit": 11,
	"DisplayMode.Litres":     12,
	"DisplayMode.Mol":        13,
	"DisplayMode.Pa":         14,
	"DisplayMode.Newtons":    15,
	"DisplayMode.Degrees":    16,
	// Sound: speaker / klaxon alert (game enum SoundAlert, IC10 prefix Sound).
	"Sound.None":                0,
	"Sound.Alarm2":              1,
	"Sound.Alarm3":              2,
	"Sound.Alarm4":              3,
	"Sound.Alarm5":              4,
	"Sound.Alarm6":              5,
	"Sound.Alarm7":              6,
	"Sound.Music1":              7,
	"Sound.Music2":              8,
	"Sound.Music3":              9,
	"Sound.Alarm8":              10,
	"Sound.Alarm9":              11,
	"Sound.Alarm10":             12,
	"Sound.Alarm11":             13,
	"Sound.Alarm12":             14,
	"Sound.Danger":              15,
	"Sound.Warning":             16,
	"Sound.Alert":               17,
	"Sound.StormIncoming":       18,
	"Sound.IntruderAlert":       19,
	"Sound.Depressurising":      20,
	"Sound.Pressurising":        21,
	"Sound.AirlockCycling":      22,
	"Sound.PowerLow":            23,
	"Sound.SystemFailure":       24,
	"Sound.Welcome":             25,
	"Sound.MalfunctionDetected": 26,
	"Sound.HaltWhoGoesThere":    27,
	"Sound.FireFireFire":        28,
	"Sound.One":                 29,
	"Sound.Two":                 30,
	"Sound.Three":               31,
	"Sound.Four":                32,
	"Sound.Five":                33,
	"Sound.Floor":               34,
	"Sound.RocketLaunching":     35,
	"Sound.LiftOff":             36,
	"Sound.TraderIncoming":      37,
	"Sound.TraderLanded":        38,
	"Sound.PressureHigh":        39,
	"Sound.PressureLow":         40,
	"Sound.TemperatureHigh":     41,
	"Sound.TemperatureLow":      42,
	"Sound.PollutantsDetected":  43,
	"Sound.HighCarbonDioxide":   44,
	"Sound.Alarm1":              45,
}

// LogicTypes is the set of device logic type names understood by IC10. It
// mirrors the game's LogicType enum (minus None); update it as the game adds
// members.
var LogicTypes = map[string]bool{
	"Acceleration": true, "Activate": true, "AirRelease": true, "AlignmentError": true,
	"Altitude": true, "Apex": true, "AutoLand": true, "AutoShutOff": true,
	"BestContactFilter": true, "Bpm": true, "BurnTimeRemaining": true, "CelestialHash": true,
	"CelestialParentHash": true, "Channel0": true, "Channel1": true, "Channel2": true,
	"Channel3": true, "Channel4": true, "Channel5": true, "Channel6": true,
	"Channel7": true, "Charge": true, "Chart": true, "ChartedNavPoints": true,
	"ClearMemory": true, "CollectableGoods": true, "Color": true, "Combustion": true,
	"CombustionInput": true, "CombustionInput2": true, "CombustionLimiter": true, "CombustionOutput": true,
	"CombustionOutput2": true, "CompletionRatio": true, "ContactTypeId": true, "CurrentCode": true,
	"CurrentResearchPodType": true, "Density": true, "DerivativeGain": true, "DestinationCode": true,
	"Discover": true, "DistanceAu": true, "DistanceKm": true, "DrillCondition": true,
	"DryMass": true, "Eccentricity": true, "ElevatorLevel": true, "ElevatorSpeed": true,
	"EntityState": true, "EnvironmentEfficiency": true, "Error": true, "ExhaustVelocity": true,
	"ExportCount": true, "ExportQuantity": true, "ExportSlotHash": true, "ExportSlotOccupant": true,
	"Extended": true, "Filtration": true, "FlightControlRule": true, "Flush": true,
	"ForceWrite": true, "ForwardX": true, "ForwardY": true, "ForwardZ": true,
	"Fuel": true, "Harvest": true, "Horizontal": true, "HorizontalRatio": true,
	"Idle": true, "ImportCount": true, "ImportQuantity": true, "ImportSlotHash": true,
	"ImportSlotOccupant": true, "Inclination": true, "Index": true, "IntegralGain": true,
	"InterrogationProgress": true, "LineNumber": true, "Lock": true, "ManualResearchRequiredPod": true,
	"Mass": true, "Maximum": true, "MineablesInQueue": true, "MineablesInVicinity": true,
	"MinedQuantity": true, "Minimum": true, "MinimumWattsToContact": true, "Mode": true,
	"NameHash": true, "NavPoints": true, "NetworkFault": true, "NextWeatherEventTime": true,
	"NextWeatherHash": true, "On": true, "Open": true, "OperationalTemperatureEfficiency": true,
	"OrbitPeriod": true, "Orientation": true, "Output": true, "PassedMoles": true,
	"Plant": true, "PlantEfficiency1": true, "PlantEfficiency2": true, "PlantEfficiency3": true,
	"PlantEfficiency4": true, "PlantGrowth1": true, "PlantGrowth2": true, "PlantGrowth3": true,
	"PlantGrowth4": true, "PlantHash1": true, "PlantHash2": true, "PlantHash3": true,
	"PlantHash4": true, "PlantHealth1": true, "PlantHealth2": true, "PlantHealth3": true,
	"PlantHealth4": true, "PositionX": true, "PositionY": true, "PositionZ": true,
	"Power": true, "PowerActual": true, "PowerGeneration": true, "PowerPotential": true,
	"PowerRequired": true, "PrefabHash": true, "Pressure": true, "PressureEfficiency": true,
	"PressureExternal": true, "PressureInput": true, "PressureInput2": true, "PressureInternal": true,
	"PressureOutput": true, "PressureOutput2": true, "PressureSetting": true, "Progress": true,
	"ProportionalGain": true, "Quantity": true, "Ratio": true, "RatioCarbonDioxide": true,
	"RatioCarbonDioxideInput": true, "RatioCarbonDioxideInput2": true, "RatioCarbonDioxideOutput": true, "RatioCarbonDioxideOutput2": true,
	"RatioHydrogen": true, "RatioLiquidCarbonDioxide": true, "RatioLiquidCarbonDioxideInput": true, "RatioLiquidCarbonDioxideInput2": true,
	"RatioLiquidCarbonDioxideOutput": true, "RatioLiquidCarbonDioxideOutput2": true, "RatioLiquidHydrogen": true, "RatioLiquidNitrogen": true,
	"RatioLiquidNitrogenInput": true, "RatioLiquidNitrogenInput2": true, "RatioLiquidNitrogenOutput": true, "RatioLiquidNitrogenOutput2": true,
	"RatioLiquidNitrousOxide": true, "RatioLiquidNitrousOxideInput": true, "RatioLiquidNitrousOxideInput2": true, "RatioLiquidNitrousOxideOutput": true,
	"RatioLiquidNitrousOxideOutput2": true, "RatioLiquidOxygen": true, "RatioLiquidOxygenInput": true, "RatioLiquidOxygenInput2": true,
	"RatioLiquidOxygenOutput": true, "RatioLiquidOxygenOutput2": true, "RatioLiquidPollutant": true, "RatioLiquidPollutantInput": true,
	"RatioLiquidPollutantInput2": true, "RatioLiquidPollutantOutput": true, "RatioLiquidPollutantOutput2": true, "RatioLiquidVolatiles": true,
	"RatioLiquidVolatilesInput": true, "RatioLiquidVolatilesInput2": true, "RatioLiquidVolatilesOutput": true, "RatioLiquidVolatilesOutput2": true,
	"RatioNitrogen": true, "RatioNitrogenInput": true, "RatioNitrogenInput2": true, "RatioNitrogenOutput": true,
	"RatioNitrogenOutput2": true, "RatioNitrousOxide": true, "RatioNitrousOxideInput": true, "RatioNitrousOxideInput2": true,
	"RatioNitrousOxideOutput": true, "RatioNitrousOxideOutput2": true, "RatioOxygen": true, "RatioOxygenInput": true,
	"RatioOxygenInput2": true, "RatioOxygenOutput": true, "RatioOxygenOutput2": true, "RatioPollutant": true,
	"RatioPollutantInput": true, "RatioPollutantInput2": true, "RatioPollutantOutput": true, "RatioPollutantOutput2": true,
	"RatioPollutedWater": true, "RatioSteam": true, "RatioSteamInput": true, "RatioSteamInput2": true,
	"RatioSteamOutput": true, "RatioSteamOutput2": true, "RatioVolatiles": true, "RatioVolatilesInput": true,
	"RatioVolatilesInput2": true, "RatioVolatilesOutput": true, "RatioVolatilesOutput2": true, "RatioWater": true,
	"RatioWaterInput": true, "RatioWaterInput2": true, "RatioWaterOutput": true, "RatioWaterOutput2": true,
	"Reagents": true, "RecipeHash": true, "ReEntryAltitude": true, "ReferenceId": true,
	"RequestHash": true, "RequiredPower": true, "Reset": true, "ReturnFuelCost": true,
	"Richness": true, "Rpm": true, "SemiMajorAxis": true, "Setpoint": true,
	"Setting": true, "SettingInput": true, "SettingOutput": true, "SignalID": true,
	"SignalStrength": true, "Sites": true, "Size": true, "SizeX": true,
	"SizeY": true, "SizeZ": true, "SolarAngle": true, "SolarIrradiance": true,
	"SoundAlert": true, "StackSize": true, "Stress": true, "Survey": true,
	"TargetPadIndex": true, "TargetPrefabHash": true, "TargetSlotIndex": true, "TargetX": true,
	"TargetY": true, "TargetZ": true, "Temperature": true, "TemperatureDifferentialEfficiency": true,
	"TemperatureExternal": true, "TemperatureInput": true, "TemperatureInput2": true, "TemperatureOutput": true,
	"TemperatureOutput2": true, "TemperatureSetting": true, "Throttle": true, "Thrust": true,
	"ThrustToWeight": true, "Time": true, "TimeToDestination": true, "TotalMoles": true,
	"TotalMolesInput": true, "TotalMolesInput2": true, "TotalMolesOutput": true, "TotalMolesOutput2": true,
	"TotalQuantity": true, "TrueAnomaly": true, "VelocityMagnitude": true, "VelocityRelativeX": true,
	"VelocityRelativeY": true, "VelocityRelativeZ": true, "VelocityX": true, "VelocityY": true,
	"VelocityZ": true, "Vertical": true, "VerticalRatio": true, "Volume": true,
	"VolumeOfLiquid": true, "WattsReachingContact": true, "Weight": true, "WorkingGasEfficiency": true,
}

// LogicTypeIDs maps "LogicType.X" member names to stable integer ids used by
// the test VM to model dynamic logic reads and writes. The game assigns its own
// enum values; the compiler emits the symbolic name verbatim, so these ids are
// only meaningful inside the VM. Ids start at 1 so 0 stays "unset".
var LogicTypeIDs = map[string]int{}

// LogicTypeNames is the reverse of LogicTypeIDs.
var LogicTypeNames = map[int]string{}

func init() {
	names := make([]string, 0, len(LogicTypes))
	for n := range LogicTypes {
		names = append(names, n)
	}
	sort.Strings(names)
	for i, n := range names {
		id := i + 1
		LogicTypeIDs[n] = id
		LogicTypeNames[id] = n
	}
}

// SlotTypes is the set of slot logic type names. It mirrors the game's
// LogicSlotType enum (minus None); update it as the game adds members.
var SlotTypes = map[string]bool{
	"Charge": true, "ChargeRatio": true, "Class": true, "Damage": true,
	"Efficiency": true, "FilterType": true, "FreeSlots": true, "Growth": true,
	"HarvestedHash": true, "Health": true, "LineNumber": true, "Lock": true,
	"Mature": true, "MaturityRatio": true, "MaxQuantity": true, "Mode": true,
	"OccupantHash": true, "Occupied": true, "On": true, "Open": true,
	"PrefabHash": true, "Pressure": true, "PressureAir": true, "PressureWaste": true,
	"Quantity": true, "ReferenceId": true, "Seeding": true, "SeedingRatio": true,
	"SortingClass": true, "Temperature": true, "TotalSlots": true, "Volume": true,
}

// Func describes a built-in function.
type Func struct {
	Name     string
	Args     int  // number of arguments
	Result   bool // whether it produces a value
	Mnemonic string
}

// Funcs maps built-in function names to their descriptors.
var Funcs = map[string]Func{}

func add(name string, args int, result bool, mnemonic string) {
	Funcs[name] = Func{Name: name, Args: args, Result: result, Mnemonic: mnemonic}
}

func init() {
	add("yield", 0, false, "yield")
	add("sleep", 1, false, "sleep")
	add("hcf", 0, false, "hcf")

	add("abs", 1, true, "abs")
	add("sgn", 1, true, "sgn")
	add("sqrt", 1, true, "sqrt")
	add("exp", 1, true, "exp")
	add("log", 1, true, "log")
	add("floor", 1, true, "floor")
	add("ceil", 1, true, "ceil")
	add("round", 1, true, "round")
	add("trunc", 1, true, "trunc")
	add("rand", 0, true, "rand")
	add("sin", 1, true, "sin")
	add("cos", 1, true, "cos")
	add("tan", 1, true, "tan")
	add("asin", 1, true, "asin")
	add("acos", 1, true, "acos")
	add("atan", 1, true, "atan")
	add("isNaN", 1, true, "snan")

	add("pow", 2, true, "pow")
	add("atan2", 2, true, "atan2")
	add("min", 2, true, "min")
	add("max", 2, true, "max")
	add("sla", 2, true, "sla")
	add("srl", 2, true, "srl")
	add("rol", 2, true, "rol")
	add("ror", 2, true, "ror")

	add("ext", 3, true, "ext")
	add("ins", 3, true, "ins")

	add("clamp", 3, true, "clamp")
	add("lerp", 3, true, "lerp")

	// Stack
	add("push", 1, false, "push")
	add("pop", 0, true, "pop")
	add("peek", 0, true, "peek")
	add("poke", 2, false, "poke")

	// Comparison helpers
	add("approx", 3, true, "sap")
	add("approxZero", 2, true, "sapz")

	// Device stack and pin helpers (first argument is a device)
	add("isSet", 1, true, "sdse")
	add("isUnset", 1, true, "sdns")
	add("rmap", 2, true, "rmap")
	add("get", 2, true, "get")
	add("put", 3, false, "put")
	add("clr", 1, false, "clr")
	add("getd", 2, true, "getd")
	add("putd", 3, false, "putd")
}
