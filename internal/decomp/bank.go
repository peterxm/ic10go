package decomp

import (
	"sort"
	"strconv"
	"strings"

	"ic10go/internal/ic10asm"
)

// Indirect register access (IC10 `rrN`) names a register at runtime. The .icg
// translator models registers as variables, and the allocator is free to put a
// variable in any register, so a register written through a pointer and later
// read directly would be read from the wrong place. To translate such a program
// faithfully those registers must stay physical: reserveRegs(lo, hi) keeps the
// allocator out of the range and the accesses become ireg(N) / setIreg(N, v).
//
// This file finds the range. It is a small abstract interpretation over the
// parsed instruction list: a register holds a known integer or is unknown, sp
// is tracked as register 16 (so a counter-driven loop terminates), and a branch
// whose condition is unknown explores both successors. Every dynamic indirect
// access must have a known pointer, otherwise the range cannot be bounded and
// the caller falls back to the variable translation (unchanged behaviour).
const (
	bankStepBudget  = 50000
	bankStateBudget = 20000
	// bankSp is the pseudo-register index of the stack pointer.
	bankSp = 16
	// bankMaxRange caps the reserved range: a wider one leaves too few
	// registers for the compiler to allocate efficiently.
	bankMaxRange = 12
)

// noBank marks "no indirect bank".
const noBank = -1

// dynamicIndirect reports whether a program accesses a register through a
// runtime pointer (an rrN operand), and the line of the first such access.
func dynamicIndirect(lines []icLine) (int, bool) {
	for _, l := range lines {
		for _, a := range l.args {
			if isIndirect(a) {
				return l.num, true
			}
		}
	}
	return 0, false
}

// indirectBank returns the register range a program's dynamic indirect accesses
// can name, or ok=false when it cannot be bounded (no dynamic access, an
// unknown pointer value, or the exploration budget is exhausted).
func (d *decompiler) indirectBank(lines []icLine) (int, int, bool) {
	for _, l := range lines {
		if l.op == "jal" || (l.op == "j" && len(l.args) > 0 && d.resolve(l.args[0]) == "ra") {
			// Calls transfer control in a way this value analysis does not
			// model; leave those programs to the variable translation.
			return 0, 0, false
		}
	}
	byNum := make(map[int]int, len(lines))
	for i, l := range lines {
		byNum[l.num] = i
	}

	type state struct {
		pc   int
		regs map[int]int64
	}
	lo, hi := 1<<30, -1
	seen := map[string]bool{}
	queue := []state{{pc: 0, regs: map[int]int64{bankSp: 0}}}
	steps := 0
	for len(queue) > 0 {
		st := queue[0]
		queue = queue[1:]
		if steps++; steps > bankStepBudget || len(seen) > bankStateBudget {
			return 0, 0, false
		}
		if st.pc < 0 || st.pc >= len(lines) {
			continue // ran off the end of the program
		}
		key := bankStateKey(st.pc, st.regs)
		if seen[key] {
			continue
		}
		seen[key] = true

		l := lines[st.pc]
		regs := cloneBankRegs(st.regs)
		if l.op == "" { // a label
			queue = append(queue, state{pc: st.pc + 1, regs: regs})
			continue
		}
		targets, ok := d.bankStep(l, regs)
		if !ok {
			return 0, 0, false
		}
		for _, t := range targets {
			if t < 0 || t > 15 {
				return 0, 0, false // names sp/ra or is outside the register file
			}
			if t < lo {
				lo = t
			}
			if t > hi {
				hi = t
			}
		}
		for _, s := range d.bankSuccessors(l, byNum, st.pc, len(lines), regs) {
			queue = append(queue, state{pc: s, regs: cloneBankRegs(regs)})
		}
	}
	if hi < 0 {
		return 0, 0, false // no dynamic indirect access
	}
	if hi-lo+1 > bankMaxRange {
		// A pointer that sweeps most of the register file leaves too few
		// registers for the compiler to allocate; report rather than emit a
		// program that is pathological to compile.
		return 0, 0, false
	}
	return lo, hi, true
}

// bankStep applies one instruction's effect to the known values, returning the
// register indices any indirect operand names. It reports false when a dynamic
// indirect operand's pointer is not known, which makes the range unbounded.
func (d *decompiler) bankStep(l icLine, regs map[int]int64) ([]int, bool) {
	var targets []int
	for _, a := range l.args {
		s := d.resolve(a)
		if !isIndirect(s) {
			continue
		}
		ptr := strings.TrimPrefix(s, "r") // rrN -> rN
		idx, ok := bankRegIndex(d.resolve(ptr))
		if !ok {
			return nil, false
		}
		v, known := regs[idx]
		if !known {
			return nil, false
		}
		targets = append(targets, int(v))
	}
	// A write through a pointer changes the pointed-to registers' values.
	for _, t := range targets {
		delete(regs, t)
	}
	// push/pop move sp without naming it as an operand.
	switch l.op {
	case "pop":
		regs[bankSp]--
	case "push":
		regs[bankSp]++
	}
	dst := d.bankDest(l)
	if dst < 0 {
		return targets, true
	}
	// Evaluate before overwriting: `add r1 r1 1` reads its destination.
	v, known := d.bankResult(l, regs)
	delete(regs, dst)
	if known {
		regs[dst] = v
	}
	return targets, true
}

// bankDest returns the register index an instruction writes, or -1. Calls are
// rejected earlier, so only register destinations remain.
func (d *decompiler) bankDest(l icLine) int {
	switch l.op {
	case "move", "add", "sub", "mul", "div", "mod",
		"and", "or", "xor", "sll", "sra", "nor", "not", "seqz", "snez",
		"sltz", "slez", "sgtz", "sgez", "seq", "sne", "slt", "sle", "sgt", "sge",
		"select", "sap", "sna", "sapz", "snaz", "snan", "snanz",
		"min", "max", "abs", "sgn", "sqrt", "exp", "log", "floor", "ceil",
		"round", "trunc", "sin", "cos", "tan", "asin", "acos", "atan", "pow",
		"atan2", "clamp", "lerp", "ext", "ins", "rol", "ror", "sla", "srl",
		"pop", "peek", "l", "ld", "ls", "lr", "get", "getd",
		"lb", "lbn", "lbs", "lbns", "rand", "rmap", "sdse", "sdns":
		if len(l.args) > 0 {
			if i, ok := bankRegIndex(d.resolve(l.args[0])); ok {
				return i
			}
		}
	}
	return -1
}

// bankResult evaluates the value an instruction writes when all its inputs are
// known. Anything else (a math builtin, a load) leaves the destination unknown,
// which is always sound.
func (d *decompiler) bankResult(l icLine, regs map[int]int64) (int64, bool) {
	if l.op == "move" {
		return d.bankValue(l, 1, regs)
	}
	if len(l.args) < 3 {
		return 0, false
	}
	a, ok1 := d.bankValue(l, 1, regs)
	b, ok2 := d.bankValue(l, 2, regs)
	if !ok1 || !ok2 {
		return 0, false
	}
	switch l.op {
	case "add":
		return a + b, true
	case "sub":
		return a - b, true
	case "mul":
		return a * b, true
	}
	return 0, false
}

// bankValue returns the value of a value operand: a numeric literal, a known
// register (including sp), or unknown.
func (d *decompiler) bankValue(l icLine, i int, regs map[int]int64) (int64, bool) {
	if i >= len(l.args) {
		return 0, false
	}
	s := d.resolve(l.args[i])
	if idx, ok := bankRegIndex(s); ok {
		v, known := regs[idx]
		return v, known
	}
	if v, ok := bankLiteral(s); ok {
		return v, true
	}
	return 0, false
}

// bankSuccessors returns the instruction indices control reaches next. An
// unconditional jump follows its target; a conditional branch follows the
// target and the fall-through unless its condition is decidable; anything else
// (including a return) ends the path.
func (d *decompiler) bankSuccessors(l icLine, byNum map[int]int, pc, n int, regs map[int]int64) []int {
	idx := jumpTargetIndex(l.op)
	branch := isBranchOp(l.op)
	if idx < 0 {
		if branch {
			return nil // an unrecognised branch: do not guess
		}
		if pc+1 < n {
			return []int{pc + 1}
		}
		return nil
	}
	target := -1
	if line, ok := d.bankTargetLine(l, idx); ok {
		if i, ok := byNum[line]; ok {
			target = i
		} else {
			target = n // past the last instruction: the program halts
		}
	} else if branch {
		return nil // a computed target cannot be followed
	}
	if l.op == "j" {
		return []int{target}
	}
	if !branch {
		return nil // jal / jr / j ra: not modelled
	}
	fall := pc + 1
	if take, ok := d.bankCond(l, regs); ok {
		if take {
			return []int{target}
		}
		return []int{fall}
	}
	return []int{target, fall}
}

// bankCond evaluates a conditional branch when its operands are known.
func (d *decompiler) bankCond(l icLine, regs map[int]int64) (bool, bool) {
	cond, _, withRA, ok := ic10asm.BranchInfo(l.op)
	if !ok || withRA {
		return false, false
	}
	a, okA := d.bankValue(l, 0, regs)
	if !okA {
		return false, false
	}
	b, okB := d.bankValue(l, 1, regs)
	switch cond {
	case "eqz":
		return a == 0, true
	case "nez":
		return a != 0, true
	case "ltz":
		return a < 0, true
	case "lez":
		return a <= 0, true
	case "gtz":
		return a > 0, true
	case "gez":
		return a >= 0, true
	}
	if !okB {
		return false, false
	}
	switch cond {
	case "eq":
		return a == b, true
	case "ne":
		return a != b, true
	case "lt":
		return a < b, true
	case "le":
		return a <= b, true
	case "gt":
		return a > b, true
	case "ge":
		return a >= b, true
	}
	return false, false
}

// bankTargetLine resolves a branch target operand to an IC10 line number,
// mirroring the translator's absolute (b<cond>) vs relative (br<cond>/jr)
// distinction.
func (d *decompiler) bankTargetLine(l icLine, idx int) (int, bool) {
	_, relative, _, _ := ic10asm.BranchInfo(l.op)
	if l.op == "jr" {
		relative = true
	}
	if idx >= len(l.args) {
		return 0, false
	}
	s := d.resolve(l.args[idx])
	if n, err := strconv.Atoi(s); err == nil {
		if relative {
			return l.num + n, true
		}
		return n, true
	}
	if line, ok := d.nameToLine[s]; ok {
		return line, true
	}
	return 0, false
}

func isBranchOp(op string) bool {
	if op == "j" {
		return false
	}
	_, _, _, ok := ic10asm.BranchInfo(op)
	return ok
}

// bankRegIndex parses a register operand into its index: r0..r15, sp (16) or ra
// (17).
func bankRegIndex(s string) (int, bool) {
	switch s {
	case "sp":
		return bankSp, true
	case "ra":
		return 17, true
	}
	if len(s) < 2 || s[0] != 'r' {
		return 0, false
	}
	n, err := strconv.Atoi(s[1:])
	if err != nil || n < 0 || n > 15 {
		return 0, false
	}
	return n, true
}

// bankLiteral parses a numeric operand (the forms IC10 accepts).
func bankLiteral(s string) (int64, bool) {
	if strings.HasPrefix(s, "$") {
		v, err := strconv.ParseInt(s[1:], 16, 64)
		return v, err == nil
	}
	if strings.HasPrefix(s, "%") {
		v, err := strconv.ParseInt(strings.ReplaceAll(s[1:], "_", ""), 2, 64)
		return v, err == nil
	}
	if v, err := strconv.ParseInt(s, 0, 64); err == nil {
		return v, true
	}
	return 0, false
}

func cloneBankRegs(m map[int]int64) map[int]int64 {
	c := make(map[int]int64, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// bankStateKey renders the exploration state so a repeated one is skipped,
// which is what stops an unbounded (device-driven) loop.
func bankStateKey(pc int, regs map[int]int64) string {
	idx := make([]int, 0, len(regs))
	for k := range regs {
		idx = append(idx, k)
	}
	sort.Ints(idx)
	var b strings.Builder
	b.WriteString(strconv.Itoa(pc))
	for _, k := range idx {
		b.WriteString(":")
		b.WriteString(strconv.Itoa(k))
		b.WriteString("=")
		b.WriteString(strconv.FormatInt(regs[k], 10))
	}
	return b.String()
}

// bankReg reports whether a register name (rN) is inside the reserved bank.
func (d *decompiler) bankReg(name string) bool {
	if d.bankLo < 0 {
		return false
	}
	i, ok := bankRegIndex(name)
	return ok && i >= d.bankLo && i <= d.bankHi
}
