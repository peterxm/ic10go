package ast

import (
	"strconv"
	"strings"

	"ic10go/internal/source"
)

// Format renders a file back to .icg source. It is intended for debugging.
func Format(f *File) string {
	return FormatComments(f, nil)
}

// FormatComments renders a file and interleaves the given comments.
func FormatComments(f *File, comments []source.Comment) string {
	p := &printer{comments: comments}
	p.file(f)
	return p.b.String()
}

type printer struct {
	b        strings.Builder
	indent   int
	comments []source.Comment
	ci       int
}

func (p *printer) write(s string) { p.b.WriteString(s) }

func (p *printer) nl() {
	if p.b.Len() == 0 {
		return
	}
	p.b.WriteByte('\n')
	for i := 0; i < p.indent; i++ {
		p.b.WriteString("    ")
	}
}

// leading emits comments that appear before the given source line and reports
// whether any were written.
func (p *printer) leading(line int) bool {
	wrote := false
	for p.ci < len(p.comments) && p.comments[p.ci].Line < line {
		c := p.comments[p.ci]
		p.ci++
		p.nl()
		p.write(c.Text)
		wrote = true
	}
	return wrote
}

// trailing appends trailing comments from the given source line.
func (p *printer) trailing(line int) {
	for p.ci < len(p.comments) && p.comments[p.ci].Line == line && p.comments[p.ci].Trailing {
		c := p.comments[p.ci]
		p.ci++
		p.write("  " + c.Text)
	}
}

func (p *printer) flushComments() {
	for p.ci < len(p.comments) {
		c := p.comments[p.ci]
		p.ci++
		p.nl()
		p.write(c.Text)
	}
}

func (p *printer) file(f *File) {
	for i, d := range f.Decls {
		had := p.leading(d.Pos().Line)
		if i > 0 {
			p.b.WriteByte('\n')
		}
		if had {
			p.nl()
		}
		p.decl(d)
		p.trailing(d.Pos().Line)
	}
	p.flushComments()
	p.b.WriteByte('\n')
}

func (p *printer) decl(d Decl) {
	switch d := d.(type) {
	case *ConstDecl:
		p.write("const ")
		p.write(d.Name.Name)
		p.write(" = ")
		p.expr(d.Value)
	case *DataDecl:
		p.write("data ")
		p.write(d.Name.Name)
		if len(d.Values) == 0 {
			p.write(" = []")
			return
		}
		p.write(" = [")
		p.indent++
		for _, v := range d.Values {
			p.leading(v.Pos().Line)
			p.nl()
			p.expr(v)
			p.write(",")
			p.trailing(v.Pos().Line)
		}
		p.indent--
		p.nl()
		p.write("]")
	case *VarDecl:
		p.write("var ")
		p.write(d.Name.Name)
		if d.Value != nil {
			p.write(" = ")
			p.expr(d.Value)
		}
	case *FuncDecl:
		p.write("func ")
		p.write(d.Name.Name)
		p.write("(")
		for i, param := range d.Params {
			if i > 0 {
				p.write(", ")
			}
			p.write(param.Name.Name)
			if param.Type != "" {
				p.write(" ")
				p.write(param.Type)
			}
		}
		p.write(")")
		if d.Result != "" {
			p.write(" ")
			p.write(d.Result)
		}
		p.write(" ")
		p.block(d.Body)
	}
}

func (p *printer) block(b *BlockStmt) {
	p.write("{")
	p.indent++
	for _, s := range b.List {
		p.leading(s.Pos().Line)
		p.nl()
		p.stmt(s)
		p.trailing(s.Pos().Line)
	}
	p.indent--
	p.nl()
	p.write("}")
}

func (p *printer) stmt(s Stmt) {
	switch s := s.(type) {
	case *DeclStmt:
		p.decl(s.Decl)
	case *BlockStmt:
		p.block(s)
	case *ExprStmt:
		p.expr(s.X)
	case *AssignStmt:
		p.expr(s.Lhs)
		p.write(" ")
		p.write(s.Op.String())
		p.write(" ")
		p.expr(s.Rhs)
	case *IncDecStmt:
		p.expr(s.X)
		p.write(s.Op.String())
	case *IfStmt:
		p.write("if ")
		p.expr(s.Cond)
		p.write(" ")
		p.block(s.Then)
		if s.Else != nil {
			p.write(" else ")
			if _, ok := s.Else.(*IfStmt); ok {
				p.stmt(s.Else)
			} else {
				p.stmt(s.Else)
			}
		}
	case *ForStmt:
		p.write("for")
		if s.Init == nil && s.Cond == nil && s.Post == nil {
			// infinite loop: for {}
		} else if s.Init != nil || s.Post != nil {
			p.write(" ")
			if s.Init != nil {
				p.stmt(s.Init)
			}
			p.write("; ")
			if s.Cond != nil {
				p.expr(s.Cond)
			}
			p.write("; ")
			if s.Post != nil {
				p.stmt(s.Post)
			}
		} else {
			p.write(" ")
			p.expr(s.Cond)
		}
		p.write(" ")
		p.block(s.Body)
	case *SwitchStmt:
		p.write("switch")
		if s.Tag != nil {
			p.write(" ")
			p.expr(s.Tag)
		}
		p.write(" {")
		p.indent++
		for _, c := range s.Cases {
			p.nl()
			if c.Default {
				p.write("default:")
			} else {
				p.write("case ")
				for i, e := range c.Exprs {
					if i > 0 {
						p.write(", ")
					}
					p.expr(e)
				}
				p.write(":")
			}
			p.indent++
			for _, cs := range c.Body {
				p.nl()
				p.stmt(cs)
			}
			p.indent--
		}
		p.indent--
		p.nl()
		p.write("}")
	case *BreakStmt:
		p.write("break")
	case *ContinueStmt:
		p.write("continue")
	case *LabelStmt:
		p.write("label ")
		p.write(s.Name.Name)
		p.write(":")
	case *GotoStmt:
		p.write("goto ")
		p.write(s.Name.Name)
	case *CallStmt:
		p.write("call ")
		p.write(s.Name.Name)
	case *RetStmt:
		p.write("ret")
	case *ReturnStmt:
		p.write("return")
		if s.Result != nil {
			p.write(" ")
			p.expr(s.Result)
		}
	}
}

func (p *printer) expr(e Expr) {
	switch e := e.(type) {
	case *Ident:
		p.write(e.Name)
	case *NumberLit:
		if e.Text != "" {
			p.write(e.Text)
		} else {
			p.write(strconv.FormatFloat(e.Value, 'g', -1, 64))
		}
	case *StringLit:
		p.write(strconv.Quote(e.Value))
	case *BoolLit:
		if e.Value {
			p.write("true")
		} else {
			p.write("false")
		}
	case *DeviceLit:
		p.write(e.Name)
	case *SpecialLit:
		p.write(e.Name)
	case *UnaryExpr:
		p.write(e.Op.String())
		p.expr(e.X)
	case *BinaryExpr:
		p.expr(e.X)
		p.write(" ")
		p.write(e.Op.String())
		p.write(" ")
		p.expr(e.Y)
	case *ParenExpr:
		p.write("(")
		p.expr(e.X)
		p.write(")")
	case *CallExpr:
		p.expr(e.Fun)
		p.write("(")
		for i, a := range e.Args {
			if i > 0 {
				p.write(", ")
			}
			p.expr(a)
		}
		p.write(")")
	case *SelectorExpr:
		p.expr(e.X)
		p.write(".")
		p.write(e.Sel.Name)
	case *IndexExpr:
		p.expr(e.X)
		p.write("[")
		p.expr(e.Index)
		p.write("]")
	case *TernaryExpr:
		p.expr(e.Cond)
		p.write(" ? ")
		p.expr(e.Then)
		p.write(" : ")
		p.expr(e.Else)
	}
}
