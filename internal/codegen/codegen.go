// Package codegen turns IR into IC10 assembly text.
package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"ic10go/internal/builtin"
	"ic10go/internal/ir"
)

// IC10 editor limits.
const (
	MaxLines   = 128
	MaxBytes   = 4096
	MaxLineLen = 90
)

type line struct {
	text   string
	target *ir.Block
}

// Generate renders a function to IC10 code and validates the result against the
// IC10 editor limits.
func Generate(fn *ir.Function, colors map[*ir.Reg]int) (string, error) {
	blocks := rpo(fn)
	var lines []line
	start := map[*ir.Block]int{}

	for i, b := range blocks {
		start[b] = len(lines)
		for _, ins := range b.Instrs {
			if text, ok := renderInstr(ins, colors); ok {
				lines = append(lines, line{text: text})
			}
		}
		var next *ir.Block
		if i+1 < len(blocks) {
			next = blocks[i+1]
		}
		switch t := b.Term.(type) {
		case *ir.Jmp:
			if t.Target != next {
				lines = append(lines, line{text: "j ", target: t.Target})
			}
		case *ir.Goto:
			if t.Target != next {
				lines = append(lines, line{text: "j ", target: t.Target})
			}
		case *ir.Call:
			lines = append(lines, line{text: "jal ", target: t.Target})
		case *ir.JmpRA:
			lines = append(lines, line{text: "j ra"})
		case *ir.Br:
			thenNext := t.Then == next
			elseNext := t.Else == next
			switch {
			case elseNext:
				lines = append(lines, line{text: branchText(t.Cond, t.A, t.B, colors) + " ", target: t.Then})
			case thenNext:
				lines = append(lines, line{text: branchText(t.Cond.Invert(), t.A, t.B, colors) + " ", target: t.Else})
			default:
				lines = append(lines, line{text: branchText(t.Cond, t.A, t.B, colors) + " ", target: t.Then})
				lines = append(lines, line{text: "j ", target: t.Else})
			}
		}
	}

	// A branch to a block that produced no line would resolve past the end.
	needNop := false
	for _, ln := range lines {
		if ln.target != nil && start[ln.target] >= len(lines) {
			needNop = true
			break
		}
	}
	if needNop {
		lines = append(lines, line{text: "move r0 r0"})
	}

	var sb strings.Builder
	for _, ln := range lines {
		sb.WriteString(ln.text)
		if ln.target != nil {
			sb.WriteString(strconv.Itoa(start[ln.target]))
		}
		sb.WriteByte('\n')
	}
	code := sb.String()

	if err := Validate(code); err != nil {
		return "", err
	}
	return code, nil
}

// Validate checks the IC10 editor limits.
func Validate(code string) error {
	if len(code) > MaxBytes {
		return fmt.Errorf("script is %d bytes, exceeding the %d byte limit", len(code), MaxBytes)
	}
	trimmed := strings.TrimSuffix(code, "\n")
	if trimmed == "" {
		return nil
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) > MaxLines {
		return fmt.Errorf("script has %d lines, exceeding the %d line limit", len(lines), MaxLines)
	}
	for i, ln := range lines {
		if len(ln) > MaxLineLen {
			return fmt.Errorf("line %d is %d characters, exceeding the %d character limit", i, len(ln), MaxLineLen)
		}
	}
	return nil
}

func regName(r *ir.Reg, colors map[*ir.Reg]int) string {
	if r == nil {
		return "r0"
	}
	c, ok := colors[r]
	if !ok {
		return "r0"
	}
	return "r" + strconv.Itoa(c)
}

func valueText(v ir.Value, colors map[*ir.Reg]int) string {
	if r, ok := v.(*ir.Reg); ok {
		return regName(r, colors)
	}
	return v.String()
}

// rpo returns the reachable blocks in reverse post-order, which tends to place
// branch targets so that fall-through is possible.
func rpo(fn *ir.Function) []*ir.Block {
	var order []*ir.Block
	visited := map[*ir.Block]bool{}
	var dfs func(b *ir.Block)
	dfs = func(b *ir.Block) {
		if b == nil || visited[b] {
			return
		}
		visited[b] = true
		switch t := b.Term.(type) {
		case *ir.Jmp:
			dfs(t.Target)
		case *ir.Goto:
			dfs(t.Target)
		case *ir.Call:
			// Visit the callee first so that the return block ends up laid out
			// immediately after the call; the IC10 return address (pc+1) then
			// points at it.
			dfs(t.Target)
			dfs(t.Return)
		case *ir.Br:
			// Visit the false edge first so that the true target ends up as the
			// fall-through block after reversing.
			dfs(t.Else)
			dfs(t.Then)
		}
		order = append(order, b)
	}
	dfs(fn.Entry)
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	return order
}

func renderInstr(ins ir.Instr, colors map[*ir.Reg]int) (string, bool) {
	switch v := ins.(type) {
	case *ir.Assign:
		if r, ok := v.Src.(*ir.Reg); ok && regName(r, colors) == regName(v.Dst, colors) {
			return "", false
		}
		return "move " + regName(v.Dst, colors) + " " + valueText(v.Src, colors), true
	case *ir.Bin:
		return v.Op.IC10() + " " + regName(v.Dst, colors) + " " +
			valueText(v.A, colors) + " " + valueText(v.B, colors), true
	case *ir.Un:
		switch v.Op {
		case ir.Neg:
			return "sub " + regName(v.Dst, colors) + " 0 " + valueText(v.A, colors), true
		case ir.BitNot:
			return "not " + regName(v.Dst, colors) + " " + valueText(v.A, colors), true
		case ir.Seqz:
			return "seqz " + regName(v.Dst, colors) + " " + valueText(v.A, colors), true
		}
	case *ir.Cmp:
		m := cmpMnemonic(v.Cond)
		if v.B == nil {
			return m + " " + regName(v.Dst, colors) + " " + valueText(v.A, colors), true
		}
		return m + " " + regName(v.Dst, colors) + " " + valueText(v.A, colors) +
			" " + valueText(v.B, colors), true
	case *ir.Select:
		return "select " + regName(v.Dst, colors) + " " + valueText(v.Cond, colors) +
			" " + valueText(v.Then, colors) + " " + valueText(v.Else, colors), true
	case *ir.Load:
		return "l " + regName(v.Dst, colors) + " " + v.Dev + " " + v.Logic, true
	case *ir.Store:
		return "s " + v.Dev + " " + v.Logic + " " + valueText(v.Src, colors), true
	case *ir.LoadSlot:
		return "ls " + regName(v.Dst, colors) + " " + v.Dev + " " +
			valueText(v.Index, colors) + " " + v.Logic, true
	case *ir.StoreSlot:
		return "ss " + v.Dev + " " + valueText(v.Index, colors) + " " +
			v.Logic + " " + valueText(v.Src, colors), true
	case *ir.Builtin:
		return renderBuiltin(v, colors), true
	case *ir.Batch:
		return renderBatch(v, colors), true
	case *ir.LoadSpecial:
		return "move " + regName(v.Dst, colors) + " " + v.Name, true
	case *ir.StoreSpecial:
		return "move " + v.Name + " " + valueText(v.Src, colors), true
	case *ir.LoadIndirect:
		return "move " + regName(v.Dst, colors) + " " + indirectName(v.Ptr, colors), true
	case *ir.StoreIndirect:
		return "move " + indirectName(v.Ptr, colors) + " " + valueText(v.Src, colors), true
	}
	return "", false
}

// indirectName renders the IC10 indirect register operand rrN.
func indirectName(ptr ir.Value, colors map[*ir.Reg]int) string {
	if r, ok := ptr.(*ir.Reg); ok {
		return "r" + regName(r, colors)
	}
	return "r0"
}

func renderBatch(v *ir.Batch, colors map[*ir.Reg]int) string {
	var parts []string
	switch v.Kind {
	case ir.BatchLoad:
		parts = []string{"lb", regName(v.Dst, colors), valueText(v.Device, colors), v.Logic, valueText(v.Mode, colors)}
	case ir.BatchLoadName:
		parts = []string{"lbn", regName(v.Dst, colors), valueText(v.Device, colors), valueText(v.Name, colors), v.Logic, valueText(v.Mode, colors)}
	case ir.BatchLoadSlot:
		parts = []string{"lbs", regName(v.Dst, colors), valueText(v.Device, colors), valueText(v.Slot, colors), v.Logic, valueText(v.Mode, colors)}
	case ir.BatchLoadNameSlot:
		parts = []string{"lbns", regName(v.Dst, colors), valueText(v.Device, colors), valueText(v.Name, colors), valueText(v.Slot, colors), v.Logic, valueText(v.Mode, colors)}
	case ir.BatchStore:
		parts = []string{"sb", valueText(v.Device, colors), v.Logic, valueText(v.Src, colors)}
	case ir.BatchStoreName:
		parts = []string{"sbn", valueText(v.Device, colors), valueText(v.Name, colors), v.Logic, valueText(v.Src, colors)}
	case ir.BatchStoreSlot:
		parts = []string{"sbs", valueText(v.Device, colors), valueText(v.Slot, colors), v.Logic, valueText(v.Src, colors)}
	}
	return strings.Join(parts, " ")
}

func renderBuiltin(v *ir.Builtin, colors map[*ir.Reg]int) string {
	f := builtin.Funcs[v.Name]
	parts := []string{f.Mnemonic}
	if v.Dst != nil {
		parts = append(parts, regName(v.Dst, colors))
	}
	for _, a := range v.Args {
		parts = append(parts, valueText(a, colors))
	}
	return strings.Join(parts, " ")
}

func branchText(c ir.Cond, a, b ir.Value, colors map[*ir.Reg]int) string {
	m := branchMnemonic(c)
	if b == nil {
		return m + " " + valueText(a, colors)
	}
	return m + " " + valueText(a, colors) + " " + valueText(b, colors)
}

func cmpMnemonic(c ir.Cond) string {
	switch c {
	case ir.Eq:
		return "seq"
	case ir.Ne:
		return "sne"
	case ir.Lt:
		return "slt"
	case ir.Le:
		return "sle"
	case ir.Gt:
		return "sgt"
	case ir.Ge:
		return "sge"
	case ir.NonZero:
		return "snez"
	case ir.Zero:
		return "seqz"
	}
	return "seq"
}

func branchMnemonic(c ir.Cond) string {
	switch c {
	case ir.Eq:
		return "beq"
	case ir.Ne:
		return "bne"
	case ir.Lt:
		return "blt"
	case ir.Le:
		return "ble"
	case ir.Gt:
		return "bgt"
	case ir.Ge:
		return "bge"
	case ir.NonZero:
		return "bnez"
	case ir.Zero:
		return "beqz"
	}
	return "beq"
}
