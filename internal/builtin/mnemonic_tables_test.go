package builtin

import "testing"

// TestSemanticsNamesAreInstructionsOrOurs: semantics is the optimizer's and
// lowerer's single source of truth, and its doc comment says adding a builtin
// must classify it here. Every key therefore has to be either a game instruction
// — so the instruction table and the optimizer agree on the vocabulary — or one
// of the handful the .icg front end lowers itself. The list below is that claim:
// when one of these becomes a real game mnemonic it belongs in IC10Instructions,
// and this guard fails until the list is updated.
//
// The class of bug it catches is the one that hid bdnvl/bdnvs: a table that knows
// a mnemonic while IC10Instructions does not (the editor then calls it unknown,
// the decompiler guesses its arity, and tick cannot name its operands).
func TestSemanticsNamesAreInstructionsOrOurs(t *testing.T) {
	ours := map[string]bool{
		// Device access whose device / logic / slot operand is chosen at run
		// time (the game's own forms take the operands in registers, which is
		// why these have no opcode of their own).
		"read": true, "write": true,
		"readById": true, "writeById": true,
		"readDev": true, "writeDev": true,
		"readDevSlot": true, "writeDevSlot": true,
		"readByIdSlot": true, "writeByIdSlot": true,
		"clrById": true, "readReagent": true, "readReagentById": true,
		// Predicates the lowerer folds into one branch (isSet → bdse).
		"isSet": true, "isUnset": true,
		// The .icg indirect-register pair, and the absolute computed jump.
		"ireg": true, "setIreg": true, "jump": true,
		// Pure .icg spellings of the approximate-comparison family (they fold
		// to sap/sna/sapz/..., which are the table's), plus a compile-time-only
		// len(). These arrive through the pureBuiltins list that init() folds
		// into semantics, so they are easy to miss when reading the literal map.
		"approx": true, "approxZero": true, "notApprox": true, "notApproxZero": true,
		"isNaN": true, "isNotNaN": true, "logicalNor": true, "len": true,
	}
	for name := range semantics {
		if _, ok := IC10Instructions[name]; ok {
			continue
		}
		if !ours[name] {
			t.Errorf("semantics[%q] is neither a game instruction nor one of the builtins listed as ours", name)
		}
	}
}
