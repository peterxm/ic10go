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
	return dst + " = " + expr
}

func (d *decompiler) translate(l icLine) []string {
	switch l.op {
	case "move":
		return []string{d.assignDst(l.args[0], d.a(l, 1))}
	case "l":
		return []string{d.assignDst(l.args[0], d.deviceRead(l.args[1], l.args[2]))}
	case "s":
		return []string{d.deviceWrite(l.args[0], l.args[1], d.a(l, 2))}
	case "ls":
		dst := d.assignDst(l.args[0], fmt.Sprintf("%s.slot[%s].%s", d.resolve(l.args[1]), d.a(l, 2), l.args[3]))
		return []string{dst}
	case "ss":
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
		return []string{d.assignDst(l.args[0], fmt.Sprintf("get(%s, %s)", d.resolve(l.args[1]), d.a(l, 2)))}
	case "put":
		return []string{fmt.Sprintf("put(%s, %s, %s)", d.resolve(l.args[0]), d.a(l, 1), d.a(l, 2))}
	case "getd":
		return []string{d.assignDst(l.args[0], fmt.Sprintf("getd(%s, %s)", d.a(l, 1), d.a(l, 2)))}
	case "putd":
		return []string{fmt.Sprintf("putd(%s, %s, %s)", d.a(l, 0), d.a(l, 1), d.a(l, 2))}
	case "clr":
		return []string{"clr(" + d.resolve(l.args[0]) + ")"}
	case "select":
		return []string{d.assignDst(l.args[0], fmt.Sprintf("(%s != 0 ? %s : %s)", d.a(l, 1), d.a(l, 2), d.a(l, 3)))}
	case "not":
		return []string{d.assignDst(l.args[0], "~"+d.a(l, 1))}
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
		target := d.resolve(l.args[0])
		if target == "ra" {
			return []string{"ret"}
		}
		return []string{"goto " + d.targetLabel(l, 0)}
	case "jal":
		return []string{"call " + d.targetLabel(l, 0)}
	case "jr":
		return []string{"goto " + d.targetLabel(l, 0)}
	}

	if cond, _, withRA, ok := ic10asm.BranchInfo(l.op); ok {
		expr, ok := d.branchExpr(cond, l)
		if !ok {
			return d.unsupported(l)
		}
		action := "goto "
		if withRA {
			action = "call "
		}
		return []string{fmt.Sprintf("if %s { %s%s }", expr, action, d.targetLabel(l, ic10asm.TargetIndex(cond)))}
	}

	return d.unsupported(l)
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
	}
	return "", false
}

func (d *decompiler) deviceRead(devArg, logic string) string {
	dev := d.resolve(devArg)
	if ch, ok := channelAccess(dev, logic); ok {
		return ch
	}
	return dev + "." + logic
}

func (d *decompiler) deviceWrite(devArg, logic, val string) string {
	dev := d.resolve(devArg)
	if ch, ok := channelAccess(dev, logic); ok {
		return ch + " = " + val
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

func modeText(s string) string {
	if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && v >= 0 && v < len(modeNames) {
		return modeNames[v]
	}
	return strings.Trim(s, "\"")
}
