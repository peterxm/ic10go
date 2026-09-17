// Package decomp translates IC10 assembly into .icg source.
//
// It is a best-effort, faithful translation: control flow becomes label/goto/
// call/ret, registers become variables named r0..r15, aliases and defines are
// substituted, and the remaining instructions map onto .icg operators and
// builtins. Unsupported instructions are reported as warnings and emitted as
// comments.
package decomp

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"ic10go/internal/ic10asm"
	"ic10go/internal/token"
)

// Warning describes an instruction that could not be translated.
type Warning struct {
	Line int
	Text string
}

type icLine struct {
	num  int
	op   string
	args []string
	raw  string
}

type decompiler struct {
	symbols    map[string]string
	labelsAt   map[int][]string
	nameToLine map[string]int
	labelAt    map[int]string
	endLabels  []string
	declared   map[string]bool
	usedLabels map[string]bool
	tmpN       int
	warnings   []Warning
}

// Decompile converts IC10 source into .icg source. Control flow is expressed
// with label/goto/call/ret.
func Decompile(src string) (string, []Warning, error) {
	return decompile(src, false)
}

// DecompileStructured is like Decompile but attempts to recover structured
// control flow (if/else/for). It is best-effort and falls back to goto for
// patterns it cannot recognise.
func DecompileStructured(src string) (string, []Warning, error) {
	return decompile(src, true)
}

func decompile(src string, structured bool) (string, []Warning, error) {
	d := &decompiler{
		symbols:    map[string]string{},
		labelsAt:   map[int][]string{},
		nameToLine: map[string]int{},
		labelAt:    map[int]string{},
		usedLabels: map[string]bool{},
	}

	raw := strings.Split(src, "\n")
	var lines []icLine
	for i, l := range raw {
		text := strings.TrimSpace(ic10asm.StripComment(l))
		if text == "" {
			continue
		}
		if ic10asm.IsLabel(text) {
			name := strings.TrimSuffix(text, ":")
			clean := d.cleanLabel(name)
			d.labelsAt[i] = append(d.labelsAt[i], clean)
			d.nameToLine[name] = i
			d.nameToLine[clean] = i
			lines = append(lines, icLine{num: i, raw: text})
			continue
		}
		fields := ic10asm.Tokenize(text)
		op := strings.ToLower(fields[0])
		args := fields[1:]
		switch op {
		case "define":
			if len(args) >= 2 {
				d.symbols[args[0]] = strings.Join(args[1:], " ")
			}
			continue
		case "alias":
			if len(args) >= 2 {
				d.symbols[args[0]] = args[1]
			}
			continue
		}
		lines = append(lines, icLine{num: i, op: op, args: args, raw: text})
	}

	// Assign labels to every jump target.
	targets := map[int]bool{}
	for _, l := range lines {
		if t, ok := d.jumpTarget(l); ok {
			targets[t] = true
		}
	}
	for t := range targets {
		if names := d.labelsAt[t]; len(names) > 0 {
			d.labelAt[t] = names[0]
			continue
		}
		name := d.cleanLabel(fmt.Sprintf("L%d", t))
		if next, ok := d.nextLine(lines, t); ok && next == t {
			d.labelAt[t] = name
			continue
		} else if ok {
			// The target does not land on an emitted instruction (a directive
			// or blank line): attach the label to the next instruction.
			d.labelsAt[next] = append(d.labelsAt[next], name)
			continue
		}
		// Past the last instruction: a jump here ends the program.
		d.endLabels = append(d.endLabels, name)
	}

	var b strings.Builder
	b.WriteString("func main() {\n")
	d.declared = map[string]bool{}
	regs := d.readFirstRegisters(lines)
	if structured {
		// Structuring may reorder emission, so declare every register up front.
		regs = d.allRegisters(lines)
	}
	for _, r := range regs {
		d.declared[r] = true
		fmt.Fprintf(&b, "    var %s = 0\n", r)
	}
	if structured {
		b.WriteString(d.structure(lines))
	} else {
		d.flat(&b, lines)
	}
	for _, n := range d.endLabels {
		fmt.Fprintf(&b, "    label %s:\n", n)
	}
	if len(d.endLabels) > 0 {
		// A jump past the last instruction halts the IC.
		b.WriteString("    jump(9999)\n")
	}
	b.WriteString("}\n")
	return b.String(), d.warnings, nil
}

// flat emits the program with explicit labels and gotos.
func (d *decompiler) flat(b *strings.Builder, lines []icLine) {
	for _, l := range lines {
		if names := d.labelsAt[l.num]; len(names) > 0 {
			for _, n := range names {
				fmt.Fprintf(b, "    label %s:\n", n)
			}
		} else if name, ok := d.labelAt[l.num]; ok {
			fmt.Fprintf(b, "    label %s:\n", name)
		}
		if l.op == "" {
			continue
		}
		for _, s := range d.translate(l) {
			fmt.Fprintf(b, "    %s\n", s)
		}
	}
}

// ---------------------------------------------------------------------------
// Parsing helpers
// ---------------------------------------------------------------------------

func (d *decompiler) resolve(s string) string {
	for i := 0; i < 10; i++ {
		v, ok := d.symbols[s]
		if !ok {
			return s
		}
		s = v
	}
	return s
}

func (d *decompiler) operand(s string) string {
	s = d.resolve(s)
	if isIndirect(s) {
		return "ireg(" + strings.TrimPrefix(s, "r") + ")"
	}
	return normalizeCall(normalizeNumber(s))
}

// normalizeNumber rewrites IC10 float literals that .icg's lexer rejects:
// leading-dot (.85) and trailing-dot (1.) forms.
func normalizeNumber(s string) string {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	rest := s[i:]
	if rest == "" {
		return s
	}
	if rest[0] == '.' {
		if len(rest) > 1 && isDigitByte(rest[1]) {
			return s[:i] + "0" + rest
		}
		return s
	}
	if rest[len(rest)-1] == '.' {
		for j := 0; j < len(rest)-1; j++ {
			if !isDigitByte(rest[j]) {
				return s
			}
		}
		return s[:len(s)-1]
	}
	return s
}

func isDigitByte(c byte) bool { return c >= '0' && c <= '9' }

// normalizeCall lower-cases the IC10 HASH()/STR() functions to the .icg names.
func normalizeCall(s string) string {
	upper := strings.ToUpper(s)
	switch {
	case strings.HasPrefix(upper, "HASH("):
		return "hash(" + s[len("HASH("):]
	case strings.HasPrefix(upper, "STR("):
		return "str(" + s[len("STR("):]
	}
	return s
}

func (d *decompiler) newTmp() string {
	d.tmpN++
	return fmt.Sprintf("t%d", d.tmpN)
}

func isReg(s string) bool {
	if s == "ra" || s == "sp" {
		return true
	}
	if len(s) >= 2 && s[0] == 'r' {
		if n, err := strconv.Atoi(s[1:]); err == nil && n >= 0 && n < 16 {
			return true
		}
	}
	return false
}

func isIndirect(s string) bool {
	return strings.HasPrefix(s, "rr") && isReg(s[1:])
}

func isDirectReg(s string) bool {
	if len(s) >= 2 && s[0] == 'r' {
		if n, err := strconv.Atoi(s[1:]); err == nil && n >= 0 && n < 16 {
			return true
		}
	}
	return false
}

func regNum(s string) int {
	n, _ := strconv.Atoi(s[1:])
	return n
}

// destOps lists instructions whose first argument is a register destination.
var destOps = map[string]bool{
	"move": true, "l": true, "lr": true, "ls": true, "lb": true, "lbn": true, "lbs": true, "lbns": true,
	"pop": true, "peek": true, "get": true, "getd": true, "rmap": true, "sdse": true, "sdns": true,
	"not": true, "abs": true, "sgn": true, "sqrt": true, "exp": true, "log": true,
	"floor": true, "ceil": true, "round": true, "trunc": true, "rand": true,
	"sin": true, "cos": true, "tan": true, "asin": true, "acos": true, "atan": true,
	"add": true, "sub": true, "mul": true, "div": true, "mod": true, "pow": true,
	"atan2": true, "min": true, "max": true, "sla": true, "srl": true, "rol": true,
	"ror": true, "and": true, "or": true, "xor": true, "nor": true, "sll": true, "sra": true,
	"seq": true, "sne": true, "slt": true, "sle": true, "sgt": true, "sge": true,
	"seqz": true, "snez": true, "sltz": true, "slez": true, "sgtz": true, "sgez": true,
	"snan": true, "snanz": true, "sap": true, "sna": true, "sapz": true, "snaz": true,
	"select": true, "clamp": true, "lerp": true, "ext": true, "ins": true,
}

// allRegisters returns every direct register referenced by the program, sorted.
func (d *decompiler) allRegisters(lines []icLine) []string {
	set := map[string]bool{}
	for _, l := range lines {
		for _, a := range l.args {
			r := d.resolve(a)
			if isDirectReg(r) {
				set[r] = true
			} else if isIndirect(r) {
				if ptr := strings.TrimPrefix(r, "r"); isDirectReg(ptr) {
					set[ptr] = true
				}
			}
		}
	}
	regs := make([]string, 0, len(set))
	for r := range set {
		regs = append(regs, r)
	}
	sort.Slice(regs, func(i, j int) bool { return regNum(regs[i]) < regNum(regs[j]) })
	return regs
}

// readFirstRegisters returns the direct registers that are read before being
// written, sorted. Only these need an explicit declaration; other registers
// are declared with := at their first assignment.
func (d *decompiler) readFirstRegisters(lines []icLine) []string {
	written := map[string]bool{}
	readFirst := map[string]bool{}
	for _, l := range lines {
		if l.op == "" {
			continue
		}
		hasDest := destOps[l.op]
		// Uses are read before the destination is written. "ins" reads its
		// destination as well (IC10 read-modify-write), so it is not skipped.
		for i, a := range l.args {
			if hasDest && i == 0 && l.op != "ins" {
				continue
			}
			r := d.resolve(a)
			if isDirectReg(r) && !written[r] {
				readFirst[r] = true
			}
		}
		if hasDest {
			if len(l.args) > 0 {
				if r := d.resolve(l.args[0]); isDirectReg(r) {
					written[r] = true
				}
			}
		}
	}
	regs := make([]string, 0, len(readFirst))
	for r := range readFirst {
		regs = append(regs, r)
	}
	sort.Slice(regs, func(i, j int) bool { return regNum(regs[i]) < regNum(regs[j]) })
	return regs
}

// ---------------------------------------------------------------------------
// Jump targets
// ---------------------------------------------------------------------------

func (d *decompiler) jumpTarget(l icLine) (int, bool) {
	switch l.op {
	case "j":
		return d.absolute(l, 0)
	case "jal":
		return d.absolute(l, 0)
	case "jr":
		if len(l.args) == 1 {
			if v, err := strconv.Atoi(d.resolve(l.args[0])); err == nil {
				return l.num + v, true
			}
		}
		return 0, false
	}
	cond, relative, _, ok := ic10asm.BranchInfo(l.op)
	if !ok {
		return 0, false
	}
	idx := ic10asm.TargetIndex(cond)
	if len(l.args) <= idx {
		return 0, false
	}
	if relative {
		if v, err := strconv.Atoi(d.resolve(l.args[idx])); err == nil {
			return l.num + v, true
		}
		return 0, false
	}
	return d.absolute(l, idx)
}

func (d *decompiler) absolute(l icLine, idx int) (int, bool) {
	if len(l.args) <= idx {
		return 0, false
	}
	arg := d.resolve(l.args[idx])
	if line, ok := d.nameToLine[arg]; ok {
		return line, true
	}
	if v, err := strconv.Atoi(arg); err == nil {
		return v, true
	}
	return 0, false
}

func (d *decompiler) labelOf(line int) string {
	if name, ok := d.labelAt[line]; ok {
		return name
	}
	return fmt.Sprintf("L%d", line)
}

// cleanLabel turns an IC10 label name into a valid, unique .icg identifier.
// IC10 labels may contain characters such as '-' or '.' that .icg rejects.
func (d *decompiler) cleanLabel(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	s := b.String()
	if s == "" {
		s = "L"
	}
	if r := rune(s[0]); !(r == '_' || unicode.IsLetter(r)) {
		s = "L" + s
	}
	// A label must not collide with a .icg keyword (e.g. an IC10 label named
	// "return" or "for"): suffix it so the decompiled source still parses.
	if token.Lookup(s) != token.Ident {
		s += "_"
	}
	base := s
	for i := 2; d.usedLabels[s]; i++ {
		s = fmt.Sprintf("%s_%d", base, i)
	}
	d.usedLabels[s] = true
	return s
}

// nextLine returns the smallest emitted instruction line at or after t.
func (d *decompiler) nextLine(lines []icLine, t int) (int, bool) {
	best := -1
	for _, l := range lines {
		if l.num >= t && (best == -1 || l.num < best) {
			best = l.num
		}
	}
	return best, best != -1
}
