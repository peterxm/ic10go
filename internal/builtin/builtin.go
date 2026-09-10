// Package builtin holds compile-time tables: IC10 logic types, slot types and
// built-in function signatures.
package builtin

import "hash/crc32"

// Hash returns the CRC-32 checksum used by IC10's HASH() function.
func Hash(s string) uint32 { return crc32.ChecksumIEEE([]byte(s)) }

// LogicTypes is the set of device logic type names understood by IC10.
var LogicTypes = map[string]bool{
	"Activate": true, "AirRelease": true, "Charge": true, "ClearMemory": true,
	"Color": true, "CompletionRatio": true, "DataNetwork": true, "ElevatorLevel": true,
	"ElevatorSpeed": true, "Error": true, "ExportCount": true, "Filtration": true,
	"Harvest": true, "Horizontal": true, "HorizontalRatio": true, "Idle": true,
	"ImportCount": true, "Lock": true, "Maximum": true, "Mode": true, "On": true,
	"Open": true, "Output": true, "Plant": true, "PositionX": true,
	"PositionY": true, "PositionZ": true, "Power": true, "PowerActual": true,
	"PowerPotential": true, "PowerRequired": true, "PrefabHash": true,
	"Pressure": true, "PressureExternal": true, "PressureInternal": true,
	"PressureOutput": true, "PressureSetting": true, "Quantity": true,
	"Ratio": true, "RatioCarbonDioxide": true, "RatioNitrogen": true,
	"RatioOxygen": true, "RatioPollutant": true, "RatioVolatiles": true,
	"RatioWater": true, "Reagents": true, "RecipeHash": true, "ReferenceId": true,
	"RequestHash": true, "RequiredPower": true, "Setting": true,
	"SettingInput": true, "SettingOutput": true,
	"SolarAngle": true, "Temperature": true, "TemperatureSettings": true,
	"TotalMoles": true, "VelocityMagnitude": true, "VelocityRelativeX": true,
	"VelocityRelativeY": true, "VelocityRelativeZ": true, "Vertical": true,
	"VerticalRatio": true, "Volume": true,
}

// SlotTypes is the set of slot logic type names.
var SlotTypes = map[string]bool{
	"Occupied": true, "OccupantHash": true, "Quantity": true, "Damage": true,
	"Efficiency": true, "FilterType": true, "Health": true, "Growth": true,
	"Pressure": true, "Temperature": true, "Charge": true, "ChargeRatio": true,
	"Class": true, "PressureWaste": true, "PressureAir": true,
	"MaxQuantity": true, "Mature": true, "ReferenceId": true, "Seeding": true,
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
