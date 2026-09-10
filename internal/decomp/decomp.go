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

	"ic10go/internal/ic10asm"
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
	tmpN       int
	warnings   []Warning
}

// Decompile converts IC10 source into .icg source.
func Decompile(src string) (string, []Warning, error) {
	d := &decompiler{
		symbols:    map[string]string{},
		labelsAt:   map[int][]string{},
		nameToLine: map[string]int{},
		labelAt:    map[int]string{},
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
			d.labelsAt[i] = append(d.labelsAt[i], name)
			d.nameToLine[name] = i
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
		} else {
			d.labelAt[t] = fmt.Sprintf("L%d", t)
		}
	}

	var b strings.Builder
	b.WriteString("func main() {\n")
	for _, r := range d.usedRegisters(lines) {
		fmt.Fprintf(&b, "    var %s = 0\n", r)
	}
	for _, l := range lines {
		if names := d.labelsAt[l.num]; len(names) > 0 {
			for _, n := range names {
				fmt.Fprintf(&b, "    label %s:\n", n)
			}
		} else if name, ok := d.labelAt[l.num]; ok {
			fmt.Fprintf(&b, "    label %s:\n", name)
		}
		if l.op == "" {
			continue
		}
		stmts := d.translate(l)
		for _, s := range stmts {
			fmt.Fprintf(&b, "    %s\n", s)
		}
	}
	b.WriteString("}\n")
	return b.String(), d.warnings, nil
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
	return normalizeCall(s)
}

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

// usedRegisters returns the direct registers referenced by the program, sorted.
func (d *decompiler) usedRegisters(lines []icLine) []string {
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
