// Package builtin holds compile-time tables: IC10 logic types, slot types and
// built-in function signatures.
//
// The logic type, slot type and game enum tables are not hand-kept: they are
// derived from GameEnums, which tools/genenums generates from the game's
// Assembly-CSharp.dll. After a Stationeers update run `go run ./tools/genenums`
// and commit the regenerated gameenums_gen.go; tests fail when the two drift.
package builtin

import (
	"hash/crc32"
	"sort"
)

// Hash returns the CRC-32 checksum used by IC10's HASH() function.
func Hash(s string) uint32 { return crc32.ChecksumIEEE([]byte(s)) }

// EnumConstants are the game enum constants that IC10 source may reference by
// name. Dotted names (e.g. SorterInstruction.FilterPrefabHashEquals) are
// resolved as selectors; the CONDOP names are also accepted bare
// (Equals/Greater/Less/NotEquals), matching the game assembler. Values are
// emitted as numbers, so they do not depend on the game resolving the symbolic
// name.
//
// It is filled at init from GameEnums (generated from the game; see
// gameenums_gen.go) plus enumExtras, so the values follow the game rather than a
// hand-kept list.
var EnumConstants = map[string]float64{}

// RawConstants are IC10 numeric constants (the game's
// ProgrammableChip.AllConstants). The compiler emits the *name* verbatim so the
// game resolves its exact double value; the test VM uses these numeric values.
// `nan`, `pinf` and `ninf` are handled as special literals by the lexer.
var RawConstants = map[string]float64{
	"pi":      3.141592653589793,
	"deg2rad": 0.0174532923847437,
	"rad2deg": 57.2957801818848,
	"epsilon": 2.220446049250313e-16,
}

// BatchModes maps a batch aggregation mode name to its IC10 operand. It is the
// single source of truth for `batch.*` mode arguments (strings, bare names and
// `LogicBatchMethod.*` constants all resolve through it).
var BatchModes = map[string]float64{
	"Average": 0,
	"Sum":     1,
	"Minimum": 2,
	"Maximum": 3,
	"Count":   4,
}

// LogicTypes is the set of device logic type names understood by IC10. It is
// derived at init from GameEnums (the game's LogicType enum, minus None), so it
// follows the game instead of a hand-kept copy. Regenerate GameEnums with
// `go run ./tools/genenums` after a Stationeers update.
var LogicTypes = map[string]bool{}

// SlotTypes is the set of slot logic type names, derived from the game's
// LogicSlotType enum (minus None).
var SlotTypes = map[string]bool{}

// LogicTypeIDs maps "LogicType.X" member names to stable integer ids used by
// the test VM to model dynamic logic reads and writes. The game assigns its own
// enum values; the compiler emits the symbolic name verbatim, so these ids are
// only meaningful inside the VM. Ids start at 1 so 0 stays "unset".
var LogicTypeIDs = map[string]int{}

// LogicTypeNames is the reverse of LogicTypeIDs.
var LogicTypeNames = map[int]string{}

// enumExtras are constants the compiler adds on top of the game's enums:
// legacy aliases kept for existing scripts, and stack conveniences that are not
// game enums. Everything else in EnumConstants comes from GameEnums.
var enumExtras = map[string]float64{
	// Logic Sorter NOP is a legacy alias for None.
	"SorterInstruction.NOP": 0,
	// ReagentMode is the legacy prefix for LogicReagentMode.
	"ReagentMode.Contents":      0,
	"ReagentMode.Required":      1,
	"ReagentMode.Recipe":        2,
	"ReagentMode.TotalContents": 3,
	// The condition operations are also accepted bare, matching the game.
	"Equals":    0,
	"Greater":   1,
	"Less":      2,
	"NotEquals": 3,
	// Stack sizes / fixed addresses (compiler conveniences, not game enums).
	"Stack.Size":                        512, // IC chip persistent stack
	"SorterStack.Size":                  32,  // Logic Sorter: 32 x 8-byte entries
	"PrinterStack.Size":                 64,  // Printer stack entries
	"PrinterStack.StackPointer":         63,  // PrinterInstruction.StackPointer address
	"PrinterStack.MissingRecipeReagent": 54,  // first MissingRecipeReagent address
}

func init() {
	deriveBoolSet(LogicTypes, GameEnums["LogicType"], "None")
	deriveBoolSet(SlotTypes, GameEnums["LogicSlotType"], "None")

	// Enum constants: every game group except LogicType (whose members are
	// device properties, tracked by LogicTypes and emitted symbolically) plus
	// the compiler's extras.
	for group, members := range GameEnums {
		if group == "LogicType" {
			continue
		}
		for name, value := range members {
			EnumConstants[group+"."+name] = float64(value)
		}
	}
	for k, v := range enumExtras {
		EnumConstants[k] = v
	}

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

// deriveBoolSet fills dst with every key of src except skip.
func deriveBoolSet(dst map[string]bool, src map[string]int64, skip string) {
	for name := range src {
		if name != skip {
			dst[name] = true
		}
	}
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
	add("clrById", 1, false, "clrd")
	// The unified get/put accept a device port, register or id; emit them for
	// the by-id forms too (the standalone getd/putd are deprecated in game).
	add("getd", 2, true, "get")
	add("putd", 3, false, "put")

	// Device reagent read (first argument is a device): lr r? device mode key.
	add("readReagent", 3, true, "lr")
	// readReagentById(reg, mode, key): lr r? rN mode key (device by ReferenceId).
	add("readReagentById", 3, true, "lr")

	// Bitwise and approximate comparison forms that IC10 has but Go-like
	// operators do not express in a single instruction.
	add("logicalNor", 2, true, "nor")
	add("notApprox", 3, true, "sna")
	add("notApproxZero", 2, true, "snaz")
	add("isNotNaN", 1, true, "snanz")
}
