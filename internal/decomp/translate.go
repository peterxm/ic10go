package decomp

import (
	"fmt"
	"strconv"
	"strings"

	"ic10go/internal/ic10asm"
)

var binOps = map[string]string{
	"add": "+", "sub": "-", "mul": "*", "div": "/", "mod": "%",
	"and": "&", "or": "|", "xor": "^", "sll": "<<", "sra": ">>",
}

var cmpOps = map[string]string{
	"seq": "==", "sne": "!=", "slt": "<", "sle": "<=", "sgt": ">", "sge": ">=",
}

var branchCmpOps = map[string]string{
	"eq": "==", "ne": "!=", "lt": "<", "le": "<=", "gt": ">", "ge": ">=",
}

var unCmpZero = map[string]string{
	"seqz": "== 0", "snez": "!= 0", "sltz": "< 0",
	"slez": "<= 0", "sgtz": "> 0", "sgez": ">= 0",
}

var branchUnCmpZero = map[string]string{
	"eqz": "== 0", "nez": "!= 0", "ltz": "< 0",
	"lez": "<= 0", "gtz": "> 0", "gez": ">= 0",
}

var mathFuncs = map[string]bool{
	"abs": true, "sgn": true, "sqrt": true, "exp": true, "log": true,
	"floor": true, "ceil": true, "round": true, "trunc": true,
	"sin": true, "cos": true, "tan": true, "asin": true, "acos": true, "atan": true,
	"pow": true, "atan2": true, "min": true, "max": true,
	"sla": true, "srl": true, "rol": true, "ror": true,
	"clamp": true, "lerp": true, "ext": true, "ins": true,
}

var modeNames = []string{"Average", "Sum", "Minimum", "Maximum"}

func (d *decompiler) a(l icLine, i int) string { return d.operand(l.args[i]) }

func (d *decompiler) assignDst(dstArg, expr string) string {
	dst := d.resolve(dstArg)
	if isIndirect(dst) {
		tmp := d.newTmp()
		ptr := strings.TrimPrefix(dst, "r")
		return fmt.Sprintf("%s := %s; setIreg(%s, %s)", tmp, expr, ptr, tmp)
	}
	op := " = "
	if isDirectReg(dst) && !d.declared[dst] {
		op = " := "
		d.declared[dst] = true
	}
	return dst + op + expr
}

func (d *decompiler) translate(l icLine) []string {
	switch l.op {
	case "move":
		return []string{d.assignDst(l.args[0], d.a(l, 1))}
	case "l":
		if d.isDynLogic(l.args[2]) {
			if reg, ok := d.deviceRegArg(l.args[1]); ok {
				return []string{d.assignDst(l.args[0], fmt.Sprintf("readDev(%s, %s)", reg, d.a(l, 2)))}
			}
			return []string{d.assignDst(l.args[0], fmt.Sprintf("read(%s, %s)", d.resolve(l.args[1]), d.a(l, 2)))}
		}
		return []string{d.assignDst(l.args[0], d.deviceRead(l.args[1], l.args[2]))}
	case "lr":
		if reg, ok := d.deviceRegArg(l.args[1]); ok {
			return []string{d.assignDst(l.args[0], fmt.Sprintf("readReagent(%s, %s, %s)",
				reg, reagentMode(l.args[2]), d.a(l, 3)))}
		}
		return []string{d.assignDst(l.args[0], fmt.Sprintf("readReagent(%s, %s, %s)",
			d.resolve(l.args[1]), reagentMode(l.args[2]), d.a(l, 3)))}
	case "s":
		if d.isDynLogic(l.args[1]) {
			if reg, ok := d.deviceRegArg(l.args[0]); ok {
				return []string{fmt.Sprintf("writeDev(%s, %s, %s)", reg, d.a(l, 1), d.a(l, 2))}
			}
			return []string{fmt.Sprintf("write(%s, %s, %s)", d.resolve(l.args[0]), d.a(l, 1), d.a(l, 2))}
		}
		return []string{d.deviceWrite(l.args[0], l.args[1], d.a(l, 2))}
	case "ld":
		return []string{d.assignDst(l.args[0], fmt.Sprintf("readById(%s, %s)", d.a(l, 1), d.a(l, 2)))}
	case "sd":
		return []string{fmt.Sprintf("writeById(%s, %s, %s)", d.a(l, 0), d.a(l, 1), d.a(l, 2))}
	case "ls":
		if reg, ok := d.deviceRegArg(l.args[1]); ok {
			return []string{d.assignDst(l.args[0], fmt.Sprintf("readDevSlot(%s, %s, %s)", reg, d.a(l, 2), l.args[3]))}
		}
		dst := d.assignDst(l.args[0], fmt.Sprintf("%s.slot[%s].%s", d.resolve(l.args[1]), d.a(l, 2), l.args[3]))
		return []string{dst}
	case "ss":
		if reg, ok := d.deviceRegArg(l.args[0]); ok {
			return []string{fmt.Sprintf("writeDevSlot(%s, %s, %s, %s)", reg, d.a(l, 1), l.args[2], d.a(l, 3))}
		}
		return []string{fmt.Sprintf("%s.slot[%s].%s = %s", d.resolve(l.args[0]), d.a(l, 1), l.args[2], d.a(l, 3))}
	case "lb", "lbn", "lbs", "lbns":
		return []string{d.assignDst(l.args[0], d.batchLoad(l))}
	case "sb", "sbn", "sbs":
		return []string{d.batchStore(l)}
	case "yield":
		return []string{"yield()"}
	case "sleep":
		return []string{"sleep(" + d.a(l, 0) + ")"}
	case "hcf":
		return []string{"hcf()"}
	case "push":
		return []string{"push(" + d.a(l, 0) + ")"}
	case "pop":
		return []string{d.assignDst(l.args[0], "pop()")}
	case "peek":
		return []string{d.assignDst(l.args[0], "peek()")}
	case "poke":
		return []string{fmt.Sprintf("poke(%s, %s)", d.a(l, 0), d.a(l, 1))}
	case "sdse":
		return []string{d.assignDst(l.args[0], "isSet("+d.resolve(l.args[1])+")")}
	case "sdns":
		return []string{d.assignDst(l.args[0], "isUnset("+d.resolve(l.args[1])+")")}
	case "rmap":
		return []string{d.assignDst(l.args[0], fmt.Sprintf("rmap(%s, %s)", d.resolve(l.args[1]), d.a(l, 2)))}
	case "get":
		if isPortOperand(d.resolve(l.args[1])) {
			return []string{d.assignDst(l.args[0], fmt.Sprintf("get(%s, %s)", d.resolve(l.args[1]), d.a(l, 2)))}
		}
		return []string{d.assignDst(l.args[0], fmt.Sprintf("getd(%s, %s)", d.a(l, 1), d.a(l, 2)))}
	case "put":
		if isPortOperand(d.resolve(l.args[0])) {
			return []string{fmt.Sprintf("put(%s, %s, %s)", d.resolve(l.args[0]), d.a(l, 1), d.a(l, 2))}
		}
		return []string{fmt.Sprintf("putd(%s, %s, %s)", d.a(l, 0), d.a(l, 1), d.a(l, 2))}
	case "getd":
		return []string{d.assignDst(l.args[0], fmt.Sprintf("getd(%s, %s)", d.a(l, 1), d.a(l, 2)))}
	case "putd":
		return []string{fmt.Sprintf("putd(%s, %s, %s)", d.a(l, 0), d.a(l, 1), d.a(l, 2))}
	case "clr":
		return []string{"clr(" + d.resolve(l.args[0]) + ")"}
	case "clrd":
		return []string{"clrById(" + d.a(l, 0) + ")"}
	case "select":
		return []string{d.assignDst(l.args[0], fmt.Sprintf("(%s != 0 ? %s : %s)", d.a(l, 1), d.a(l, 2), d.a(l, 3)))}
	case "not":
		return []string{d.assignDst(l.args[0], "~"+d.a(l, 1))}
	case "nor":
		return []string{d.assignDst(l.args[0], fmt.Sprintf("logicalNor(%s, %s)", d.a(l, 1), d.a(l, 2)))}
	}

	if op, ok := binOps[l.op]; ok {
		return []string{d.assignDst(l.args[0], fmt.Sprintf("(%s %s %s)", d.a(l, 1), op, d.a(l, 2)))}
	}
	if op, ok := cmpOps[l.op]; ok {
		return []string{d.assignDst(l.args[0], fmt.Sprintf("(%s %s %s)", d.a(l, 1), op, d.a(l, 2)))}
	}
	if op, ok := unCmpZero[l.op]; ok {
		return []string{d.assignDst(l.args[0], fmt.Sprintf("(%s %s)", d.a(l, 1), op))}
	}
	switch l.op {
	case "snan":
		return []string{d.assignDst(l.args[0], "isNaN("+d.a(l, 1)+")")}
	case "snanz":
		return []string{d.assignDst(l.args[0], "!isNaN("+d.a(l, 1)+")")}
	case "sap":
		return []string{d.assignDst(l.args[0], fmt.Sprintf("approx(%s, %s, %s)", d.a(l, 1), d.a(l, 2), d.a(l, 3)))}
	case "sna":
		return []string{d.assignDst(l.args[0], fmt.Sprintf("!approx(%s, %s, %s)", d.a(l, 1), d.a(l, 2), d.a(l, 3)))}
	case "sapz":
		return []string{d.assignDst(l.args[0], fmt.Sprintf("approxZero(%s, %s)", d.a(l, 1), d.a(l, 2)))}
	case "snaz":
		return []string{d.assignDst(l.args[0], fmt.Sprintf("!approxZero(%s, %s)", d.a(l, 1), d.a(l, 2)))}
	case "rand":
		return []string{d.assignDst(l.args[0], "rand()")}
	}
	if mathFuncs[l.op] {
		return []string{d.assignDst(l.args[0], l.op+"("+d.argList(l, 1)+")")}
	}

	// Control flow.
	switch l.op {
	case "j":
		if d.resolve(l.args[0]) == "ra" {
			return []string{"ret"}
		}
		return []string{d.jumpStatement(l, 0, false)}
	case "jal":
		return []string{d.jumpStatement(l, 0, true)}
	case "jr":
		return []string{d.jumpStatement(l, 0, false)}
	}

	if cond, _, withRA, ok := ic10asm.BranchInfo(l.op); ok {
		expr, ok := d.branchExpr(cond, l)
		if !ok {
			return d.unsupported(l)
		}
		stmt := d.jumpStatement(l, ic10asm.TargetIndex(cond), withRA)
		return []string{fmt.Sprintf("if %s { %s }", expr, stmt)}
	}

	return d.unsupported(l)
}

// jumpStatement renders the jump performed by an instruction: a label goto/call
// for constant targets, or a computed jump(expr) for register targets.
func (d *decompiler) jumpStatement(l icLine, idx int, withRA bool) string {
	_, relative, _, _ := ic10asm.BranchInfo(l.op)
	if l.op == "jr" {
		relative = true
	}
	if relative {
		if off, err := strconv.Atoi(d.resolve(l.args[idx])); err == nil {
			line := l.num + off
			if withRA {
				return "call " + d.labelOf(line)
			}
			return "goto " + d.labelOf(line)
		}
	} else if t, ok := d.absolute(l, idx); ok {
		if withRA {
			return "call " + d.labelOf(t)
		}
		return "goto " + d.labelOf(t)
	}
	return "jump(" + d.operand(l.args[idx]) + ")"
}

func (d *decompiler) argList(l icLine, start int) string {
	parts := make([]string, 0, len(l.args)-start)
	for i := start; i < len(l.args); i++ {
		parts = append(parts, d.operand(l.args[i]))
	}
	return strings.Join(parts, ", ")
}

func (d *decompiler) targetLabel(l icLine, idx int) string {
	if t, ok := d.jumpTarget(l); ok {
		return d.labelOf(t)
	}
	if len(l.args) > idx {
		return d.resolve(l.args[idx])
	}
	return "?"
}

func (d *decompiler) branchExpr(cond string, l icLine) (string, bool) {
	if op, ok := branchCmpOps[cond]; ok {
		if len(l.args) < 2 {
			return "", false
		}
		return fmt.Sprintf("%s %s %s", d.a(l, 0), op, d.a(l, 1)), true
	}
	if op, ok := branchUnCmpZero[cond]; ok {
		return fmt.Sprintf("%s %s", d.a(l, 0), op), true
	}
	switch cond {
	case "nan":
		return "isNaN(" + d.a(l, 0) + ")", true
	case "ap":
		return fmt.Sprintf("approx(%s, %s, %s)", d.a(l, 0), d.a(l, 1), d.a(l, 2)), true
	case "na":
		return fmt.Sprintf("!approx(%s, %s, %s)", d.a(l, 0), d.a(l, 1), d.a(l, 2)), true
	case "apz":
		return fmt.Sprintf("approxZero(%s, %s)", d.a(l, 0), d.a(l, 1)), true
	case "naz":
		return fmt.Sprintf("!approxZero(%s, %s)", d.a(l, 0), d.a(l, 1)), true
	case "dns":
		return "isUnset(" + d.resolve(l.args[0]) + ")", true
	case "dse":
		return "isSet(" + d.resolve(l.args[0]) + ")", true
	case "dnvl":
		return fmt.Sprintf("!isLoadValid(%s, %q)", d.resolve(l.args[0]), l.args[1]), true
	case "dvl":
		return fmt.Sprintf("isLoadValid(%s, %q)", d.resolve(l.args[0]), l.args[1]), true
	case "dnvs":
		return fmt.Sprintf("!isStoreValid(%s, %q)", d.resolve(l.args[0]), l.args[1]), true
	case "dvs":
		return fmt.Sprintf("isStoreValid(%s, %q)", d.resolve(l.args[0]), l.args[1]), true
	}
	return "", false
}

// isDynLogic reports whether a logic type operand is a runtime value (a
// register or number) rather than a logic type name.
func (d *decompiler) isDynLogic(s string) bool {
	s = d.resolve(s)
	if isReg(s) || isIndirect(s) {
		return true
	}
	_, err := strconv.Atoi(s)
	return err == nil
}

// deviceRegArg recognises an IC10 device-register operand (dr15, drr0) and
// returns the .icg expression for the register that holds the port index.
func (d *decompiler) deviceRegArg(devArg string) (string, bool) {
	dev := d.resolve(devArg)
	if len(dev) >= 3 && dev[0] == 'd' && dev[1] == 'r' {
		return d.operand(dev[1:]), true
	}
	return "", false
}

func (d *decompiler) deviceRead(devArg, logic string) string {
	dev := d.resolve(devArg)
	if ch, ok := channelAccess(dev, logic); ok {
		return ch
	}
	if reg, ok := d.deviceRegArg(devArg); ok {
		return fmt.Sprintf("readDev(%s, LogicType.%s)", reg, logic)
	}
	return dev + "." + logic
}

func (d *decompiler) deviceWrite(devArg, logic, val string) string {
	dev := d.resolve(devArg)
	if ch, ok := channelAccess(dev, logic); ok {
		return ch + " = " + val
	}
	if reg, ok := d.deviceRegArg(devArg); ok {
		return fmt.Sprintf("writeDev(%s, LogicType.%s, %s)", reg, logic, val)
	}
	return dev + "." + logic + " = " + val
}

func (d *decompiler) batchLoad(l icLine) string {
	hash := d.a(l, 1)
	switch l.op {
	case "lb":
		return fmt.Sprintf("batch.read(%s, %q, %q)", hash, l.args[2], modeText(l.args[3]))
	case "lbn":
		return fmt.Sprintf("batch.readName(%s, %s, %q, %q)", hash, d.a(l, 2), l.args[3], modeText(l.args[4]))
	case "lbs":
		return fmt.Sprintf("batch.readSlot(%s, %s, %q, %q)", hash, d.a(l, 2), l.args[3], modeText(l.args[4]))
	case "lbns":
		return fmt.Sprintf("batch.readNameSlot(%s, %s, %s, %q, %q)", hash, d.a(l, 2), d.a(l, 3), l.args[4], modeText(l.args[5]))
	}
	return ""
}

func (d *decompiler) batchStore(l icLine) string {
	hash := d.a(l, 0)
	switch l.op {
	case "sb":
		return fmt.Sprintf("batch.write(%s, %q, %s)", hash, l.args[1], d.a(l, 2))
	case "sbn":
		return fmt.Sprintf("batch.writeName(%s, %s, %q, %s)", hash, d.a(l, 1), l.args[2], d.a(l, 3))
	case "sbs":
		return fmt.Sprintf("batch.writeSlot(%s, %s, %q, %s)", hash, d.a(l, 1), l.args[2], d.a(l, 3))
	}
	return ""
}

func (d *decompiler) unsupported(l icLine) []string {
	d.warnings = append(d.warnings, Warning{Line: l.num, Text: l.raw})
	return []string{"// unsupported: " + l.raw}
}

func channelAccess(dev, logic string) (string, bool) {
	if !strings.HasPrefix(logic, "Channel") {
		return "", false
	}
	idx := strings.TrimPrefix(logic, "Channel")
	if _, err := strconv.Atoi(idx); err != nil {
		return "", false
	}
	parts := strings.SplitN(dev, ":", 2)
	if len(parts) != 2 {
		return "", false
	}
	return fmt.Sprintf("%s.channel[%s][%s]", parts[0], parts[1], idx), true
}

// isPortOperand reports whether a device operand is a fixed port name
// (d0..d5 / db) rather than a device id.
func isPortOperand(s string) bool {
	if s == "db" {
		return true
	}
	return len(s) == 2 && s[0] == 'd' && s[1] >= '0' && s[1] <= '5'
}

func modeText(s string) string {
	if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && v >= 0 && v < len(modeNames) {
		return modeNames[v]
	}
	return strings.Trim(s, "\"")
}

// reagentMode renders the IC10 lr mode operand as a ReagentMode enum name.
func reagentMode(s string) string {
	switch strings.TrimSpace(s) {
	case "0", "Contents", "ReagentMode.Contents", "LogicReagentMode.Contents":
		return "ReagentMode.Contents"
	case "1", "Required", "ReagentMode.Required", "LogicReagentMode.Required":
		return "ReagentMode.Required"
	case "2", "Recipe", "ReagentMode.Recipe", "LogicReagentMode.Recipe":
		return "ReagentMode.Recipe"
	case "3", "TotalContents", "ReagentMode.TotalContents", "LogicReagentMode.TotalContents":
		return "ReagentMode.TotalContents"
	}
	return s
}
