package decomp

import (
	"strings"

	"ic10go/internal/builtin"
)

// fallbackArity covers mnemonics that are *ours* rather than the game's.
//
// It used to list a dozen native mnemonics (rmap, clr, clamp, lerp, sgn, ret, …)
// under the comment "covers native mnemonics that the generated game metadata
// table omits". That was true only while tools/gengamedata read just the
// ScriptCommand* family of the game's localization: 24 mnemonics are localized
// only under ScriptDescription*, so they never reached the table. The generator
// now reads both and IC10Instructions mirrors the game's set exactly, which left
// those entries dead (arity() returns from the table first) and the stale comment
// inviting the wrong conclusion the next time one went missing. They are gone.
//
// What is left is `ret`: the game has no record for it at all. It is the mnemonic
// the decompiler *emits* for `j ra` (translate.go), so reading back that output
// needs an arity even though the game never sees the opcode.
var fallbackArity = map[string]int{
	"ret": 0,
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
