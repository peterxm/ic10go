package decomp

import (
	"strconv"
	"strings"

	"ic10go/internal/builtin"
	"ic10go/internal/ic10asm"
)

// knownOperandNames is the set of bare identifiers that may appear as a value
// operand: logic/slot type names, batch modes, game enum members and raw
// constants (plus the bare reagent modes the game accepts).
var knownOperandNames = func() map[string]bool {
	m := map[string]bool{}
	for k := range builtin.LogicTypes {
		m[k] = true
	}
	for k := range builtin.SlotTypes {
		m[k] = true
	}
	for k := range builtin.BatchModes {
		m[k] = true
	}
	for k := range builtin.EnumConstants {
		m[k] = true
	}
	for k := range builtin.RawConstants {
		m[k] = true
	}
	for _, n := range []string{"Contents", "Required", "Recipe", "TotalContents"} {
		m[n] = true
	}
	return m
}()

// knownOperandPrefixes are namespace-qualified operands the compiler accepts.
var knownOperandPrefixes = []string{
	"LogicType.", "LogicReagentMode.", "LogicBatchMethod.", "ReagentMode.",
}

// fallbackKinds gives operand kinds for native mnemonics the metadata omits.
var fallbackKinds = map[string][]string{
	"rmap":  {"REGISTER", "DEVICE", "VALUE"},
	"clr":   {"DEVICE"},
	"ext":   {"REGISTER", "VALUE", "VALUE", "VALUE"},
	"ins":   {"REGISTER", "VALUE", "VALUE", "VALUE"},
	"rol":   {"REGISTER", "VALUE", "VALUE"},
	"ror":   {"REGISTER", "VALUE", "VALUE"},
	"pow":   {"REGISTER", "VALUE", "VALUE"},
	"sgn":   {"REGISTER", "VALUE"},
	"clamp": {"REGISTER", "VALUE", "VALUE", "VALUE"},
	"lerp":  {"REGISTER", "VALUE", "VALUE", "VALUE"},
}

// operandKinds returns the operand kinds of a native instruction, or false
// when the mnemonic is unknown (in which case operands are not validated).
func operandKinds(op string) ([]string, bool) {
	if ins, ok := builtin.IC10Instructions[op]; ok {
		return strings.Fields(ins.Sig), true
	}
	if k, ok := fallbackKinds[op]; ok {
		return k, true
	}
	return nil, false
}

// isPortName reports whether s is a fixed device port (d0..d5/db) or a port
// register (drN).
func isPortName(s string) bool {
	if s == "db" {
		return true
	}
	if len(s) == 2 && s[0] == 'd' && s[1] >= '0' && s[1] <= '5' {
		return true
	}
	return len(s) >= 3 && s[0] == 'd' && s[1] == 'r'
}

// isNumberOperand reports whether s is a numeric literal the compiler accepts,
// including IC10's $hex / %bin forms and nan/inf constants.
func isNumberOperand(s string) bool {
	switch s {
	case "nan", "pinf", "ninf":
		return true
	}
	if strings.HasPrefix(s, "$") {
		_, err := strconv.ParseUint(s[1:], 16, 64)
		return err == nil
	}
	if strings.HasPrefix(s, "%") {
		_, err := strconv.ParseUint(strings.ReplaceAll(s[1:], "_", ""), 2, 64)
		return err == nil
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func isStringLiteral(s string) bool { return strings.HasPrefix(s, "\"") }

func isHashCall(s string) bool {
	return strings.HasPrefix(s, "HASH(") || strings.HasPrefix(s, "hash(")
}

func isCallLike(s string) bool {
	for _, p := range []string{"HASH(", "hash(", "STR(", "str(", "raw("} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// valueOperandOK reports whether s is acceptable where the game expects a
// numeric value: a number, register, game constant/enum, hash/str call or
// string literal. A bare unknown identifier (e.g. a reagent name like `Iron`)
// is rejected.
func (d *decompiler) valueOperandOK(s string) bool {
	if isNumberOperand(s) || isReg(s) || isIndirect(s) || isCallLike(s) || isStringLiteral(s) {
		return true
	}
	if knownOperandNames[s] {
		return true
	}
	for _, p := range knownOperandPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// operandOK reports whether an operand of the given kind is something the .icg
// compiler understands. Logic/slot type names are accepted verbatim (the
// compiler warns about unknown ones but does not reject them).
func (d *decompiler) operandOK(kind, raw string) bool {
	s := d.resolve(raw)
	switch kind {
	case "REGISTER":
		return isReg(s) || isIndirect(s)
	case "DEVICE":
		return isPortName(s) || isReg(s) || isIndirect(s) || isNumberOperand(s)
	case "DEVICE_ID":
		return isReg(s) || isIndirect(s) || isNumberOperand(s)
	case "DEVICE_TYPE":
		return isNumberOperand(s) || isHashCall(s) || isReg(s) || isIndirect(s)
	case "DEVICE_NAME":
		return isNumberOperand(s) || isHashCall(s) || isStringLiteral(s) || isReg(s) || isIndirect(s)
	case "ADDRESS", "INDEX":
		return isNumberOperand(s) || isReg(s) || isIndirect(s)
	case "LOGIC_TYPE", "SLOT_LOGIC_TYPE", "NAME", "REGISTER_DEVICE":
		return true
	case "BATCH_MODE", "REAGENT_MODE":
		return isNumberOperand(s) || isStringLiteral(s) || isReg(s) || isIndirect(s) || knownOperandNames[s]
	default: // VALUE
		return d.valueOperandOK(s)
	}
}

// jumpTargetIndex returns the argument index holding a jump target for a
// control-transfer instruction, or -1 when the instruction has no target.
func jumpTargetIndex(op string) int {
	switch op {
	case "j", "jal", "jr":
		return 0
	}
	if cond, _, _, ok := ic10asm.BranchInfo(op); ok {
		return ic10asm.TargetIndex(cond)
	}
	return -1
}

// validOperands reports whether every non-target operand of an instruction is
// a known token. Jump targets are labels or line numbers and are not validated
// here; instructions with an unknown mnemonic are left alone.
func (d *decompiler) validOperands(l icLine) bool {
	kinds, ok := operandKinds(l.op)
	if !ok {
		return true
	}
	target := jumpTargetIndex(l.op)
	for i, a := range l.args {
		if i == target {
			continue
		}
		kind := "VALUE"
		if i < len(kinds) {
			kind = kinds[i]
		}
		if !d.operandOK(kind, a) {
			return false
		}
	}
	return true
}

// jumpTargetOK reports whether an instruction's jump target is a known label
// or line number, or a computed register/value. An unresolved bare identifier
// (e.g. a misspelled or missing label) is rejected.
func (d *decompiler) jumpTargetOK(l icLine) bool {
	if _, ok := d.jumpTarget(l); ok {
		return true
	}
	idx := jumpTargetIndex(l.op)
	if idx < 0 || idx >= len(l.args) {
		return false
	}
	s := d.resolve(l.args[idx])
	return isReg(s) || isIndirect(s) || isNumberOperand(s)
}

// dropped reports whether an instruction will be emitted as a comment instead
// of translated: a malformed operand count or an operand the compiler cannot
// express.
func (d *decompiler) dropped(l icLine) bool {
	if n, ok := arity(l.op); ok && len(l.args) != n {
		return true
	}
	return !d.validOperands(l)
}
