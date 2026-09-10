// Package disasm renders IC10 assembly as an annotated listing. It does not
// attempt full decompilation; it resolves jump targets to labels so that old
// scripts are easier to read and migrate.
package disasm

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"ic10go/internal/ic10asm"
)

type instr struct {
	line int
	op   string
	args []string
	text string
}

// Disassemble returns an annotated listing of IC10 source.
func Disassemble(src string) string {
	rawLines := strings.Split(src, "\n")
	var insts []*instr
	labelsByName := map[string]int{}

	for i, raw := range rawLines {
		line := strings.TrimSpace(ic10asm.StripComment(raw))
		if line == "" {
			continue
		}
		if ic10asm.IsLabel(line) {
			name := strings.TrimSuffix(line, ":")
			labelsByName[name] = i
			insts = append(insts, &instr{line: i, text: line})
			continue
		}
		fields := ic10asm.Tokenize(line)
		insts = append(insts, &instr{
			line: i,
			op:   strings.ToLower(fields[0]),
			args: fields[1:],
			text: line,
		})
	}

	targets := map[int]bool{}
	for _, in := range insts {
		if t, ok := branchTarget(in, labelsByName); ok {
			targets[t] = true
		}
	}
	var sortedTargets []int
	for t := range targets {
		sortedTargets = append(sortedTargets, t)
	}
	sort.Ints(sortedTargets)
	labels := map[int]string{}
	for i, t := range sortedTargets {
		labels[t] = fmt.Sprintf("L%d", i)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "; ic10 disassembly: %d lines\n", len(insts))
	if len(sortedTargets) > 0 {
		b.WriteString("; targets:")
		for _, t := range sortedTargets {
			fmt.Fprintf(&b, " %s=%d", labels[t], t)
		}
		b.WriteByte('\n')
	}
	for _, in := range insts {
		ann := ""
		if lbl, ok := labels[in.line]; ok {
			ann = "  ; " + lbl + ":"
		}
		fmt.Fprintf(&b, "%4d  %s%s\n", in.line, in.text, ann)
	}
	return b.String()
}

func branchTarget(in *instr, labels map[string]int) (int, bool) {
	switch in.op {
	case "j", "jal":
		if len(in.args) == 1 {
			return resolve(in.args[0], labels)
		}
	}
	cond, relative, _, ok := ic10asm.BranchInfo(in.op)
	if !ok || relative {
		return 0, false
	}
	idx := ic10asm.TargetIndex(cond)
	if len(in.args) > idx {
		return resolve(in.args[idx], labels)
	}
	return 0, false
}

func resolve(arg string, labels map[string]int) (int, bool) {
	if line, ok := labels[arg]; ok {
		return line, true
	}
	if n, err := strconv.Atoi(arg); err == nil {
		return n, true
	}
	return 0, false
}
