// Package flow renders the source-level control flow of a .icg file as a
// Mermaid flowchart. It works on the AST, so nodes show the original source
// text and line numbers rather than the compiler's register-allocated IR.
package flow

import (
	"fmt"
	"sort"
	"strings"

	"ic10go/internal/ast"
)

// Options controls Mermaid rendering.
type Options struct {
	Func     string // render only this function ("" = all)
	ShowLine bool   // annotate nodes with source line numbers
	Coalesce bool   // merge consecutive straight-line statements into one node
}

const (
	kindStmt = iota
	kindCond
	kindTerm
	kindStart
)

type fnode struct {
	id      string
	kind    int
	line    int
	lineEnd int
	texts   []string
}

type fedge struct {
	from, to string
	label    string
}

type breakCtx struct{ label, target string }
type loopCtx struct{ label, head string }

type builder struct {
	prefix string
	opts   Options
	nodes  []*fnode
	byID   map[string]*fnode
	edges  []*fedge
	seq    int
	breaks []breakCtx
	loops  []loopCtx
	labels map[string]string
	gotos  []gotoRef
	end    string
}

type gotoRef struct{ from, label string }

// Mermaid renders the control flow of every (or the selected) function as a
// Mermaid `flowchart TD` with one `subgraph` per function.
func Mermaid(file *ast.File, opts Options) string {
	var b strings.Builder
	b.WriteString("flowchart TD\n")
	idx := 0
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if opts.Func != "" && fd.Name.Name != opts.Func {
			continue
		}
		prefix := fmt.Sprintf("f%d", idx)
		idx++
		fmt.Fprintf(&b, "    subgraph %s[%s]\n", prefix, fd.Name.Name)
		g := &builder{prefix: prefix, opts: opts, byID: map[string]*fnode{}, labels: map[string]string{}}
		g.buildFunc(fd)
		b.WriteString(g.render())
		b.WriteString("    end\n")
	}
	return b.String()
}

func (b *builder) newNode(kind, line, lineEnd int, texts ...string) *fnode {
	n := &fnode{id: fmt.Sprintf("%s_%d", b.prefix, b.seq), kind: kind, line: line, lineEnd: lineEnd, texts: texts}
	b.seq++
	b.nodes = append(b.nodes, n)
	b.byID[n.id] = n
	return n
}

func (b *builder) edge(from, to, label string) {
	if from == "" || to == "" {
		return
	}
	b.edges = append(b.edges, &fedge{from: from, to: to, label: label})
}

func (b *builder) buildFunc(fd *ast.FuncDecl) {
	start := b.newNode(kindStart, fd.Pos().Line, fd.Pos().Line, fd.Name.Name)
	b.end = b.newNode(kindTerm, 0, 0, "end").id
	var entry string
	if fd.Body != nil {
		entry = b.build(fd.Body.List, b.end)
	} else {
		entry = b.end
	}
	b.edge(start.id, entry, "")
	for _, g := range b.gotos {
		if t, ok := b.labels[g.label]; ok {
			b.edge(g.from, t, "")
		} else {
			b.edge(g.from, b.end, "goto")
		}
	}
	if b.opts.Coalesce {
		b.coalesce()
	}
}

// build builds a statement sequence that falls through to next and returns its
// entry node. It walks backwards so each statement knows its successor.
func (b *builder) build(stmts []ast.Stmt, next string) string {
	cur := next
	for i := len(stmts) - 1; i >= 0; i-- {
		cur = b.stmt(stmts[i], cur)
	}
	return cur
}

func (b *builder) stmt(s ast.Stmt, next string) string {
	switch s := s.(type) {
	case *ast.BlockStmt:
		return b.build(s.List, next)
	case *ast.ExprStmt, *ast.AssignStmt, *ast.IncDecStmt, *ast.DeclStmt:
		n := b.newNode(kindStmt, s.Pos().Line, s.Pos().Line, ast.StmtString(s))
		b.edge(n.id, next, "")
		return n.id
	case *ast.IfStmt:
		elseEntry := next
		if s.Else != nil {
			elseEntry = b.stmt(s.Else, next)
		}
		thenEntry := b.build(s.Then.List, next)
		cond := b.newNode(kindCond, s.Pos().Line, s.Pos().Line, "if "+ast.ExprString(s.Cond))
		b.edge(cond.id, thenEntry, "true")
		b.edge(cond.id, elseEntry, "false")
		if s.Init != nil {
			return b.stmt(s.Init, cond.id)
		}
		return cond.id
	case *ast.ForStmt:
		head := b.newNode(kindCond, s.Pos().Line, s.Pos().Line, forText(s))
		b.breaks = append(b.breaks, breakCtx{"", next})
		b.loops = append(b.loops, loopCtx{"", head.id})
		bodyNext := head.id
		if s.Post != nil {
			bodyNext = b.stmt(s.Post, head.id)
		}
		bodyEntry := b.build(s.Body.List, bodyNext)
		b.loops = b.loops[:len(b.loops)-1]
		b.breaks = b.breaks[:len(b.breaks)-1]
		b.edge(head.id, bodyEntry, "true")
		if s.Cond != nil {
			b.edge(head.id, next, "false")
		}
		if s.Init != nil {
			return b.stmt(s.Init, head.id)
		}
		return head.id
	case *ast.RangeStmt:
		head := b.newNode(kindCond, s.Pos().Line, s.Pos().Line, rangeText(s))
		b.breaks = append(b.breaks, breakCtx{"", next})
		b.loops = append(b.loops, loopCtx{"", head.id})
		bodyEntry := b.build(s.Body.List, head.id)
		b.loops = b.loops[:len(b.loops)-1]
		b.breaks = b.breaks[:len(b.breaks)-1]
		b.edge(head.id, bodyEntry, "true")
		b.edge(head.id, next, "false")
		return head.id
	case *ast.SwitchStmt:
		sw := b.newNode(kindCond, s.Pos().Line, s.Pos().Line, switchText(s))
		b.breaks = append(b.breaks, breakCtx{"", next})
		hasDefault := false
		for _, c := range s.Cases {
			entry := b.build(c.Body, next)
			label := "default"
			if c.Default {
				hasDefault = true
			} else {
				parts := make([]string, len(c.Exprs))
				for i, e := range c.Exprs {
					parts[i] = ast.ExprString(e)
				}
				label = "case " + strings.Join(parts, ", ")
			}
			b.edge(sw.id, entry, label)
		}
		b.breaks = b.breaks[:len(b.breaks)-1]
		if !hasDefault {
			b.edge(sw.id, next, "no match")
		}
		if s.Init != nil {
			return b.stmt(s.Init, sw.id)
		}
		return sw.id
	case *ast.BreakStmt:
		label := ""
		if s.Label != nil {
			label = s.Label.Name
		}
		n := b.newNode(kindTerm, s.Pos().Line, s.Pos().Line, "break"+labelSuffix(label))
		b.edge(n.id, b.breakTarget(label), "")
		return n.id
	case *ast.ContinueStmt:
		label := ""
		if s.Label != nil {
			label = s.Label.Name
		}
		n := b.newNode(kindTerm, s.Pos().Line, s.Pos().Line, "continue"+labelSuffix(label))
		b.edge(n.id, b.continueTarget(label), "")
		return n.id
	case *ast.ReturnStmt:
		text := "return"
		if s.Result != nil {
			text += " " + ast.ExprString(s.Result)
		}
		n := b.newNode(kindTerm, s.Pos().Line, s.Pos().Line, text)
		b.edge(n.id, b.end, "")
		return n.id
	case *ast.RetStmt:
		n := b.newNode(kindTerm, s.Pos().Line, s.Pos().Line, "ret")
		b.edge(n.id, b.end, "")
		return n.id
	case *ast.LabelStmt:
		n := b.newNode(kindStmt, s.Pos().Line, s.Pos().Line, s.Name.Name+":")
		b.labels[s.Name.Name] = n.id
		b.edge(n.id, next, "")
		return n.id
	case *ast.GotoStmt:
		n := b.newNode(kindTerm, s.Pos().Line, s.Pos().Line, "goto "+s.Name.Name)
		b.gotos = append(b.gotos, gotoRef{n.id, s.Name.Name})
		return n.id
	case *ast.CallStmt:
		n := b.newNode(kindTerm, s.Pos().Line, s.Pos().Line, "call "+s.Name.Name)
		b.edge(n.id, next, "")
		return n.id
	}
	return next
}

func (b *builder) breakTarget(label string) string {
	for i := len(b.breaks) - 1; i >= 0; i-- {
		if label == "" || b.breaks[i].label == label {
			return b.breaks[i].target
		}
	}
	return b.end
}

func (b *builder) continueTarget(label string) string {
	for i := len(b.loops) - 1; i >= 0; i-- {
		if label == "" || b.loops[i].label == label {
			return b.loops[i].head
		}
	}
	return b.end
}

func labelSuffix(label string) string {
	if label == "" {
		return ""
	}
	return " " + label
}

func forText(s *ast.ForStmt) string {
	switch {
	case s.Init == nil && s.Cond == nil && s.Post == nil:
		return "for {}"
	case s.Init == nil && s.Post == nil:
		return "for " + ast.ExprString(s.Cond)
	default:
		text := "for "
		if s.Init != nil {
			text += ast.StmtString(s.Init)
		}
		text += "; "
		if s.Cond != nil {
			text += ast.ExprString(s.Cond)
		}
		text += "; "
		if s.Post != nil {
			text += ast.StmtString(s.Post)
		}
		return text
	}
}

func rangeText(s *ast.RangeStmt) string {
	text := "for " + s.Key.Name
	if s.Value != nil {
		text += ", " + s.Value.Name
	}
	return text + " := range " + ast.ExprString(s.X)
}

func switchText(s *ast.SwitchStmt) string {
	text := "switch"
	if s.Init != nil {
		text += " " + ast.StmtString(s.Init) + ";"
	}
	if s.Tag != nil {
		text += " " + ast.ExprString(s.Tag)
	}
	if s.Table {
		text += " table"
	}
	return text
}

// coalesce merges a straight-line node into its single predecessor when both
// are plain statements and the edge between them is unconditional.
func (b *builder) coalesce() {
	for {
		merged := false
		for _, e := range b.edges {
			a, c := b.byID[e.from], b.byID[e.to]
			if a == nil || c == nil || a.kind != kindStmt || c.kind != kindStmt || e.label != "" {
				continue
			}
			if b.outCount(a.id) != 1 || b.inCount(c.id) != 1 {
				continue
			}
			a.texts = append(a.texts, c.texts...)
			if c.lineEnd > a.lineEnd {
				a.lineEnd = c.lineEnd
			}
			for _, x := range b.edges {
				if x.from == c.id {
					x.from = a.id
				}
			}
			b.removeNode(c.id)
			b.removeEdge(a.id, c.id)
			merged = true
			break
		}
		if !merged {
			return
		}
	}
}

func (b *builder) outCount(id string) int {
	n := 0
	for _, e := range b.edges {
		if e.from == id {
			n++
		}
	}
	return n
}

func (b *builder) inCount(id string) int {
	n := 0
	for _, e := range b.edges {
		if e.to == id {
			n++
		}
	}
	return n
}

func (b *builder) removeNode(id string) {
	for i, n := range b.nodes {
		if n.id == id {
			b.nodes = append(b.nodes[:i], b.nodes[i+1:]...)
			break
		}
	}
	delete(b.byID, id)
}

func (b *builder) removeEdge(from, to string) {
	for i, e := range b.edges {
		if e.from == from && e.to == to {
			b.edges = append(b.edges[:i], b.edges[i+1:]...)
			return
		}
	}
}

func (b *builder) render() string {
	nodes := append([]*fnode(nil), b.nodes...)
	sort.SliceStable(nodes, func(i, j int) bool {
		li, lj := nodes[i].line, nodes[j].line
		if li == 0 {
			li = 1 << 30
		}
		if lj == 0 {
			lj = 1 << 30
		}
		return li < lj
	})
	var sb strings.Builder
	for _, n := range nodes {
		fmt.Fprintf(&sb, "        %s%s\n", n.id, n.shape(b.opts.ShowLine))
	}
	for _, e := range b.edges {
		if e.label != "" {
			fmt.Fprintf(&sb, "        %s -->|%s| %s\n", e.from, sanitize(e.label), e.to)
		} else {
			fmt.Fprintf(&sb, "        %s --> %s\n", e.from, e.to)
		}
	}
	return sb.String()
}

func (n *fnode) shape(showLine bool) string {
	label := n.label(showLine)
	switch n.kind {
	case kindStart, kindTerm:
		return `(["` + label + `"])`
	case kindCond:
		return `{"` + label + `"}`
	default:
		return `["` + label + `"]`
	}
}

func (n *fnode) label(showLine bool) string {
	var parts []string
	if showLine && n.line > 0 {
		if n.lineEnd > n.line {
			parts = append(parts, fmt.Sprintf("L%d-%d", n.line, n.lineEnd))
		} else {
			parts = append(parts, fmt.Sprintf("L%d", n.line))
		}
	}
	parts = append(parts, n.texts...)
	for i := range parts {
		parts[i] = strings.ReplaceAll(parts[i], `"`, "#quot;")
	}
	return strings.Join(parts, "<br/>")
}

func sanitize(s string) string {
	s = strings.ReplaceAll(s, `"`, "#quot;")
	s = strings.ReplaceAll(s, "|", "#124;")
	return s
}
