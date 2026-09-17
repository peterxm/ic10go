package decomp

import (
	"strings"

	"ic10go/internal/builtin"
)

// fallbackArity covers native mnemonics that the generated game metadata table
// omits. Counts match internal/vm's opArity.
var fallbackArity = map[string]int{
	"rmap": 3, "clr": 1, "clrd": 1, "sgn": 2, "pow": 3,
	"rol": 3, "ror": 3, "clamp": 4, "lerp": 4, "ext": 4, "ins": 4, "ret": 0,
}

// arity returns the exact operand count of a native IC10 instruction. It uses
// the game instruction metadata (one operand per signature field), falling back
// to a small table for the few mnemonics the metadata omits.
func arity(op string) (int, bool) {
	if ins, ok := builtin.IC10Instructions[op]; ok {
		return len(strings.Fields(ins.Sig)), true
	}
	if n, ok := fallbackArity[op]; ok {
		return n, true
	}
	return 0, false
}
