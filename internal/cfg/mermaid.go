// Package cfg renders the compiler's IR control-flow graph as Mermaid.
package cfg

import (
	"fmt"
	"strings"

	"ic10go/internal/codegen"
	"ic10go/internal/ir"
)

// Options controls Mermaid rendering.
type Options struct {
	ShowLines bool // annotate each node with its IC10 start line
	MaxInstr  int  // max instructions shown per node (0 = all)
}

// Mermaid renders the control-flow graph as a Mermaid `flowchart TD`.
func Mermaid(fn *ir.Function, order []*ir.Block, start map[*ir.Block]int, colors map[*ir.Reg]int, opts Options) string {
	var b strings.Builder
	b.WriteString("flowchart TD\n")
	for _, blk := range order {
		if blk == nil {
			continue
		}
		fmt.Fprintf(&b, "    b%d%s\n", blk.ID, nodeLabel(blk, start, colors, opts))
	}
	for _, blk := range order {
		if blk == nil || blk.Term == nil {
			continue
		}
		for _, e := range edgesOf(blk) {
			if e.to == nil {
				continue
			}
			if e.label == "" {
				fmt.Fprintf(&b, "    b%d --> b%d\n", blk.ID, e.to.ID)
			} else {
				fmt.Fprintf(&b, "    b%d -->|%s| b%d\n", blk.ID, e.label, e.to.ID)
			}
		}
	}
	return b.String()
}

func nodeLabel(blk *ir.Block, start map[*ir.Block]int, colors map[*ir.Reg]int, opts Options) string {
	head := fmt.Sprintf("b%d", blk.ID)
	if blk.Func != "" {
		head += " · " + blk.Func
	}
	if opts.ShowLines {
		if s, ok := start[blk]; ok {
			head += fmt.Sprintf(" · L%d", s+1)
		}
	}
	lines := []string{head}
	for i, ins := range blk.Instrs {
		if opts.MaxInstr > 0 && i >= opts.MaxInstr {
			lines = append(lines, "…")
			break
		}
		if text, ok := codegen.InstrText(ins, colors); ok {
			lines = append(lines, text)
		}
	}
	if s := termSummary(blk.Term); s != "" {
		lines = append(lines, s)
	}
	for i := range lines {
		lines[i] = strings.ReplaceAll(lines[i], `"`, "#quot;")
	}
	return `["` + strings.Join(lines, "<br/>") + `"]`
}

type edge struct {
	to    *ir.Block
	label string
}

func edgesOf(blk *ir.Block) []edge {
	switch t := blk.Term.(type) {
	case *ir.Jmp:
		return []edge{{t.Target, ""}}
	case *ir.Goto:
		return []edge{{t.Target, ""}}
	case *ir.Call:
		return []edge{{t.Target, "call"}, {t.Return, "return"}}
	case *ir.Br:
		return []edge{{t.Then, "then"}, {t.Else, "else"}}
	case *ir.BrCall:
		return []edge{{t.Target, "call"}, {t.Return, "fallthrough"}}
	case *ir.BrValid:
		return []edge{{t.Valid, "valid"}, {t.Invalid, "invalid"}}
	case *ir.BrApprox:
		return []edge{{t.Then, "then"}, {t.Else, "else"}}
	case *ir.BrApproxZero:
		return []edge{{t.Then, "then"}, {t.Else, "else"}}
	case *ir.JmpDyn:
		var out []edge
		for i, tb := range t.Table {
			out = append(out, edge{tb, fmt.Sprintf("case %d", i)})
		}
		return out
	}
	return nil
}

func termSummary(t ir.Term) string {
	switch x := t.(type) {
	case *ir.Br:
		return "br " + x.Cond.String()
	case *ir.Jmp, *ir.Goto:
		return "j"
	case *ir.Call:
		return "jal"
	case *ir.BrCall:
		return "b" + x.Cond.String() + "al"
	case *ir.BrValid:
		if x.Store {
			return "bdnvs"
		}
		return "bdnvl"
	case *ir.BrApprox:
		if x.Negate {
			return "bna"
		}
		return "bap"
	case *ir.BrApproxZero:
		if x.Negate {
			return "bnaz"
		}
		return "bapz"
	case *ir.JmpDyn:
		if len(x.Table) > 0 {
			return "jump table"
		}
		return "j r?"
	case *ir.JmpRA:
		return "j ra"
	case *ir.Ret:
		return "ret"
	}
	return ""
}
