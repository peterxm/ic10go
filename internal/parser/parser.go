package parser

import (
	"strconv"
	"strings"

	"ic10go/internal/ast"
	"ic10go/internal/diag"
	"ic10go/internal/source"
	"ic10go/internal/token"
)

// Parse parses a token stream into a File. It always returns a File, which may
// be partial when diagnostics were produced.
func Parse(file *source.File, toks []token.Token, diags *diag.Bag) *ast.File {
	p := &parser{toks: toks, diags: diags, file: file}
	f := p.parseFile()
	return f
}

type parser struct {
	toks  []token.Token
	pos   int
	diags *diag.Bag
	file  *source.File
}

type bailout struct{}

func (p *parser) cur() token.Token { return p.toks[p.pos] }

func (p *parser) at(k token.Kind) bool { return p.cur().Kind == k }

func (p *parser) advance() token.Token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *parser) errorf(pos source.Pos, format string, args ...any) {
	p.diags.Errorf(pos, format, args...)
	panic(bailout{})
}

func (p *parser) expect(k token.Kind) token.Token {
	if !p.at(k) {
		p.errorf(p.cur().Pos, "expected %s, found %s", k, describe(p.cur()))
	}
	return p.advance()
}

func describe(t token.Token) string {
	if t.Kind == token.EOF {
		return "end of file"
	}
	if t.Text != "" {
		return strconv.Quote(t.Text)
	}
	return t.Kind.String()
}

func (p *parser) skipSemis() {
	for p.at(token.Semicolon) {
		p.advance()
	}
}

// ---------------------------------------------------------------------------
// File and declarations
// ---------------------------------------------------------------------------

func (p *parser) parseFile() *ast.File {
	f := &ast.File{}
	for {
		p.skipSemis()
		if p.at(token.EOF) {
			break
		}
		p.recoverDecl(f)
	}
	return f
}

func (p *parser) recoverDecl(f *ast.File) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); ok {
				p.syncTop()
				return
			}
			panic(r)
		}
	}()
	decls := p.parseTopDecl()
	f.Decls = append(f.Decls, decls...)
}

func (p *parser) parseTopDecl() []ast.Decl {
	switch p.cur().Kind {
	case token.Const:
		return p.parseConstDecl()
	case token.Data:
		return p.parseDataDecl()
	case token.Var:
		return p.parseVarDecls()
	case token.Func:
		return []ast.Decl{p.parseFuncDecl()}
	case token.Chip:
		return []ast.Decl{p.parseChipDecl()}
	case token.Bus:
		return []ast.Decl{p.parseBusDecl()}
	case token.Import:
		return p.parseImportDecl()
	default:
		p.errorf(p.cur().Pos, "expected declaration, found %s", describe(p.cur()))
		return nil
	}
}

func (p *parser) parseConstDecl() []ast.Decl {
	kw := p.expect(token.Const)
	if p.at(token.LParen) {
		p.advance()
		var decls []ast.Decl
		for {
			p.skipSemis()
			if p.at(token.RParen) || p.at(token.EOF) {
				break
			}
			pos := p.cur().Pos
			name := p.parseIdent()
			opTok := p.expect(token.Assign)
			val := p.parseAssignRHS(opTok)
			decls = append(decls, &ast.ConstDecl{NodeBase: base(pos), Name: name, Value: val, Group: true})
		}
		p.expect(token.RParen)
		return decls
	}
	name := p.parseIdent()
	opTok := p.expect(token.Assign)
	val := p.parseAssignRHS(opTok)
	return []ast.Decl{&ast.ConstDecl{NodeBase: base(kw.Pos), Name: name, Value: val}}
}

// parseDataDecl parses `data Name = [ expr, ... ]`, a compile-time constant
// table stored in the persistent IC10 stack.
func (p *parser) parseDataDecl() []ast.Decl {
	kw := p.expect(token.Data)
	name := p.parseIdent()
	p.expect(token.Assign)
	p.expect(token.LBracket)
	if p.at(token.RBracket) {
		p.advance()
		return []ast.Decl{&ast.DataDecl{NodeBase: base(kw.Pos), Name: name}}
	}
	first := p.parseExpr()
	// Comprehension: `[ expr for i in lo..hi ]`.
	if p.at(token.For) {
		p.advance()
		v := p.parseIdent()
		inTok := p.expect(token.Ident)
		if inTok.Text != "in" {
			p.errorf(inTok.Pos, "expected 'in', found %q", inTok.Text)
		}
		lo := p.parseExpr()
		p.expect(token.DotDot)
		hi := p.parseExpr()
		p.expect(token.RBracket)
		return []ast.Decl{&ast.DataDecl{
			NodeBase: base(kw.Pos),
			Name:     name,
			Comp: &ast.DataComp{
				NodeBase: base(first.Pos()),
				Expr:     first,
				Var:      v,
				Lo:       lo,
				Hi:       hi,
			},
		}}
	}
	vals := []ast.Expr{first}
	for p.at(token.Comma) && !p.at(token.EOF) {
		p.advance()
		if p.at(token.RBracket) {
			break
		}
		vals = append(vals, p.parseExpr())
	}
	p.expect(token.RBracket)
	return []ast.Decl{&ast.DataDecl{NodeBase: base(kw.Pos), Name: name, Values: vals}}
}

func (p *parser) parseVarDecls() []ast.Decl {
	kw := p.expect(token.Var)
	if p.at(token.LParen) {
		p.advance()
		var decls []ast.Decl
		for {
			p.skipSemis()
			if p.at(token.RParen) || p.at(token.EOF) {
				break
			}
			pos := p.cur().Pos
			name := p.parseIdent()
			d := &ast.VarDecl{NodeBase: base(pos), Name: name, Group: true}
			if p.at(token.Assign) {
				opTok := p.advance()
				d.Value = p.parseAssignRHS(opTok)
			}
			decls = append(decls, d)
		}
		p.expect(token.RParen)
		return decls
	}
	name := p.parseIdent()
	d := &ast.VarDecl{NodeBase: base(kw.Pos), Name: name}
	if p.at(token.Assign) {
		opTok := p.advance()
		d.Value = p.parseAssignRHS(opTok)
	}
	return []ast.Decl{d}
}

func (p *parser) parseFuncDecl() *ast.FuncDecl {
	kw := p.expect(token.Func)
	name := p.parseIdent()
	p.expect(token.LParen)
	var params []*ast.Param
	for !p.at(token.RParen) {
		ppos := p.cur().Pos
		pname := p.parseIdent()
		typ := ""
		if p.at(token.Ident) {
			typ = p.advance().Text
		}
		params = append(params, &ast.Param{NodeBase: base(ppos), Name: pname, Type: typ})
		if p.at(token.Comma) {
			p.advance()
			continue
		}
		break
	}
	p.expect(token.RParen)
	result := ""
	if p.at(token.Ident) {
		result = p.advance().Text
	}
	body := p.parseBlock()
	return &ast.FuncDecl{NodeBase: base(kw.Pos), Name: name, Params: params, Result: result, Body: body}
}

func (p *parser) parseIdent() *ast.Ident {
	t := p.expect(token.Ident)
	return &ast.Ident{NodeBase: base(t.Pos), Name: t.Text}
}

// parseChipDecl parses `chip Name { const|data|var|func ... }`, a block that
// compiles to a separate IC10 program. Chips cannot be nested.
func (p *parser) parseChipDecl() *ast.ChipDecl {
	kw := p.expect(token.Chip)
	name := p.parseIdent()
	p.expect(token.LBrace)
	var decls []ast.Decl
	for {
		p.skipSemis()
		if p.at(token.RBrace) || p.at(token.EOF) {
			break
		}
		decls = append(decls, p.parseChipMember()...)
	}
	p.expect(token.RBrace)
	return &ast.ChipDecl{NodeBase: base(kw.Pos), Name: name, Decls: decls}
}

// parseChipMember parses a declaration allowed inside a `chip` block.
func (p *parser) parseChipMember() []ast.Decl {
	switch p.cur().Kind {
	case token.Const:
		return p.parseConstDecl()
	case token.Data:
		return p.parseDataDecl()
	case token.Var:
		return p.parseVarDecls()
	case token.Func:
		return []ast.Decl{p.parseFuncDecl()}
	case token.Use:
		return []ast.Decl{p.parseUseDecl()}
	case token.Chip:
		p.errorf(p.cur().Pos, "chips cannot be nested")
		p.advance()
		return nil
	default:
		p.errorf(p.cur().Pos, "expected const, data, var, func or use, found %s", describe(p.cur()))
		return nil
	}
}

// parseBusDecl parses `bus Name { slot type ... }`. Slots map to Channel0.. in
// declaration order (at most 8 per connection).
func (p *parser) parseBusDecl() *ast.BusDecl {
	kw := p.expect(token.Bus)
	name := p.parseIdent()
	p.expect(token.LBrace)
	var slots []*ast.BusSlot
	for {
		p.skipSemis()
		if p.at(token.RBrace) || p.at(token.EOF) {
			break
		}
		spos := p.cur().Pos
		sname := p.parseIdent()
		typ := "num"
		if p.at(token.Ident) {
			typ = p.advance().Text
		}
		slots = append(slots, &ast.BusSlot{NodeBase: base(spos), Name: sname, Type: typ})
	}
	p.expect(token.RBrace)
	return &ast.BusDecl{NodeBase: base(kw.Pos), Name: name, Slots: slots}
}

// parseUseDecl parses `use Bus on dev:conn[, dev:conn ...]`, a chip's access
// points to a bus's network(s).
func (p *parser) parseUseDecl() *ast.UseDecl {
	kw := p.expect(token.Use)
	bus := p.parseIdent()
	on := p.expect(token.Ident)
	if on.Text != "on" {
		p.errorf(on.Pos, "expected 'on', found %q", on.Text)
	}
	var binds []*ast.ConnRef
	for {
		bpos := p.cur().Pos
		var dev string
		switch p.cur().Kind {
		case token.Device, token.Ident:
			dev = p.advance().Text
		default:
			p.errorf(p.cur().Pos, "expected a device, found %s", describe(p.cur()))
		}
		p.expect(token.Colon)
		connTok := p.expect(token.Number)
		conn, _ := strconv.Atoi(connTok.Text)
		binds = append(binds, &ast.ConnRef{NodeBase: base(bpos), Device: dev, Conn: conn})
		if p.at(token.Comma) {
			p.advance()
			continue
		}
		break
	}
	return &ast.UseDecl{NodeBase: base(kw.Pos), Bus: bus, Bindings: binds}
}

// parseImportDecl parses `import "path"`: the declarations of another file,
// merged in before checking.
func (p *parser) parseImportDecl() []ast.Decl {
	kw := p.expect(token.Import)
	if !p.at(token.String) {
		p.errorf(p.cur().Pos, "import expects a quoted path, found %s", describe(p.cur()))
		return nil
	}
	str := p.advance()
	return []ast.Decl{&ast.ImportDecl{
		NodeBase: base(kw.Pos),
		Path:     &ast.StringLit{NodeBase: base(str.Pos), Value: str.Text},
	}}
}

// parseOptLabel parses an optional label after break/continue.
func (p *parser) parseOptLabel() *ast.Ident {
	if p.at(token.Ident) {
		return p.parseIdent()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Statements
// ---------------------------------------------------------------------------

func (p *parser) parseBlock() *ast.BlockStmt {
	lb := p.expect(token.LBrace)
	b := &ast.BlockStmt{NodeBase: base(lb.Pos)}
	for {
		p.skipSemis()
		if p.at(token.RBrace) || p.at(token.EOF) {
			break
		}
		p.recoverStmt(b)
	}
	p.expect(token.RBrace)
	return b
}

func (p *parser) recoverStmt(b *ast.BlockStmt) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); ok {
				p.syncStmt()
				return
			}
			panic(r)
		}
	}()
	if s := p.parseStmt(); s != nil {
		b.List = append(b.List, s)
	}
}

func (p *parser) parseStmt() ast.Stmt {
	switch p.cur().Kind {
	case token.LBrace:
		return p.parseBlock()
	case token.Const:
		decls := p.parseConstDecl()
		return &ast.DeclStmt{NodeBase: base(decls[0].Pos()), Decl: decls[0]}
	case token.Var:
		decls := p.parseVarDecls()
		return &ast.DeclStmt{NodeBase: base(decls[0].Pos()), Decl: decls[0]}
	case token.If:
		return p.parseIf()
	case token.For:
		return p.parseFor()
	case token.Switch:
		return p.parseSwitch()
	case token.Break:
		t := p.advance()
		return &ast.BreakStmt{NodeBase: base(t.Pos), Label: p.parseOptLabel()}
	case token.Continue:
		t := p.advance()
		return &ast.ContinueStmt{NodeBase: base(t.Pos), Label: p.parseOptLabel()}
	case token.Return:
		return p.parseReturn()
	case token.Label:
		t := p.advance()
		name := p.parseIdent()
		p.expect(token.Colon)
		return &ast.LabelStmt{NodeBase: base(t.Pos), Name: name}
	case token.Goto:
		t := p.advance()
		return &ast.GotoStmt{NodeBase: base(t.Pos), Name: p.parseIdent()}
	case token.Call:
		t := p.advance()
		return &ast.CallStmt{NodeBase: base(t.Pos), Name: p.parseIdent()}
	case token.Ret:
		t := p.advance()
		return &ast.RetStmt{NodeBase: base(t.Pos)}
	default:
		return p.parseSimpleStmt()
	}
}

// ---------------------------------------------------------------------------
// Statements
// ---------------------------------------------------------------------------

func (p *parser) parseSimpleStmt() ast.Stmt {
	pos := p.cur().Pos
	x := p.parseExpr()
	switch p.cur().Kind {
	case token.Assign, token.Define,
		token.PlusAssign, token.MinusAssign, token.StarAssign, token.SlashAssign,
		token.PercentAssign, token.AmpAssign, token.PipeAssign, token.CaretAssign,
		token.ShlAssign, token.ShrAssign:
		opTok := p.advance()
		rhs := p.parseAssignRHS(opTok)
		return &ast.AssignStmt{NodeBase: base(pos), Lhs: x, Op: opTok.Kind, Rhs: rhs}
	case token.PlusPlus, token.MinusMinus:
		op := p.advance().Kind
		return &ast.IncDecStmt{NodeBase: base(pos), X: x, Op: op}
	}
	return &ast.ExprStmt{NodeBase: base(pos), X: x}
}

// parseAssignRHS parses the right-hand side of an assignment. When the next
// token cannot begin an expression (e.g. the RHS was left empty and the next
// line starts a new statement), it reports the error at the assignment operator
// rather than at that unrelated token.
func (p *parser) parseAssignRHS(op token.Token) ast.Expr {
	if !startsExpr(p.cur().Kind) {
		p.errorf(op.Pos, "expected expression after %q", op.Text)
	}
	return p.parseExpr()
}

// startsExpr reports whether a token of this kind can begin an expression.
func startsExpr(k token.Kind) bool {
	switch k {
	case token.Ident, token.Number, token.String, token.Device,
		token.True, token.False, token.NaN, token.PInf, token.NInf,
		token.LParen, token.Not, token.Tilde, token.Minus, token.Plus:
		return true
	}
	return false
}

func (p *parser) parseIf() ast.Stmt {
	kw := p.expect(token.If)
	if p.at(token.LBrace) {
		p.errorf(p.cur().Pos, "expected condition, found %s", describe(p.cur()))
	}
	var init ast.Stmt
	var cond ast.Expr
	st := p.parseSimpleStmt()
	if p.at(token.Semicolon) {
		p.advance()
		init = st
		cond = p.parseExpr()
	} else {
		es, ok := st.(*ast.ExprStmt)
		if !ok {
			p.errorf(st.Pos(), "expected condition expression in if statement")
		}
		cond = es.X
	}
	then := p.parseBlock()
	var els ast.Stmt
	if p.at(token.Else) {
		p.advance()
		if p.at(token.If) {
			els = p.parseIf()
		} else {
			els = p.parseBlock()
		}
	}
	return &ast.IfStmt{NodeBase: base(kw.Pos), Init: init, Cond: cond, Then: then, Else: els}
}

func (p *parser) parseFor() ast.Stmt {
	kw := p.expect(token.For)
	if p.at(token.LBrace) {
		body := p.parseBlock()
		return &ast.ForStmt{NodeBase: base(kw.Pos), Body: body}
	}

	if r := p.tryParseRange(kw); r != nil {
		return r
	}

	var first ast.Stmt
	if !p.at(token.Semicolon) {
		first = p.parseSimpleStmt()
	}
	if p.at(token.Semicolon) {
		p.advance()
		var cond ast.Expr
		if !p.at(token.Semicolon) {
			cond = p.parseExpr()
		}
		p.expect(token.Semicolon)
		var post ast.Stmt
		if !p.at(token.LBrace) {
			post = p.parseSimpleStmt()
		}
		body := p.parseBlock()
		return &ast.ForStmt{NodeBase: base(kw.Pos), Init: first, Cond: cond, Post: post, Body: body}
	}

	// while form: the first part must be an expression.
	var cond ast.Expr
	if first != nil {
		es, ok := first.(*ast.ExprStmt)
		if !ok {
			p.errorf(first.Pos(), "expected condition expression in for loop")
		}
		cond = es.X
	}
	body := p.parseBlock()
	return &ast.ForStmt{NodeBase: base(kw.Pos), Cond: cond, Body: body}
}

// tryParseRange recognizes `for key [, value] := range X { ... }`. It backtracks
// when the tokens do not form a range clause (e.g. a three-part for).
func (p *parser) tryParseRange(kw token.Token) *ast.RangeStmt {
	if !p.at(token.Ident) {
		return nil
	}
	save := p.pos
	key := p.parseIdent()
	var val *ast.Ident
	if p.at(token.Comma) {
		p.advance()
		if !p.at(token.Ident) {
			p.pos = save
			return nil
		}
		val = p.parseIdent()
	}
	if !p.at(token.Define) {
		p.pos = save
		return nil
	}
	p.advance()
	if !p.at(token.Range) {
		p.pos = save
		return nil
	}
	p.advance()
	x := p.parseExpr()
	body := p.parseBlock()
	return &ast.RangeStmt{NodeBase: base(kw.Pos), Key: key, Value: val, X: x, Body: body}
}

func (p *parser) parseSwitch() ast.Stmt {
	kw := p.expect(token.Switch)
	var init ast.Stmt
	var tag ast.Expr
	if !p.at(token.LBrace) {
		st := p.parseSimpleStmt()
		if p.at(token.Semicolon) {
			p.advance()
			init = st
			if !p.at(token.LBrace) && !(p.at(token.Ident) && p.cur().Text == "table") {
				tag = p.parseExpr()
			}
		} else {
			es, ok := st.(*ast.ExprStmt)
			if !ok {
				p.errorf(st.Pos(), "expected switch expression")
			}
			tag = es.X
		}
	}
	table := false
	if p.at(token.Ident) && p.cur().Text == "table" {
		p.advance()
		table = true
	}
	p.expect(token.LBrace)
	sw := &ast.SwitchStmt{NodeBase: base(kw.Pos), Init: init, Tag: tag, Table: table}
	for {
		p.skipSemis()
		switch p.cur().Kind {
		case token.Case:
			cpos := p.advance().Pos
			c := &ast.CaseClause{NodeBase: base(cpos)}
			c.Exprs = append(c.Exprs, p.parseCaseValue())
			for p.at(token.Comma) {
				p.advance()
				c.Exprs = append(c.Exprs, p.parseCaseValue())
			}
			p.expect(token.Colon)
			c.Body = p.parseCaseBody()
			sw.Cases = append(sw.Cases, c)
		case token.Default:
			dpos := p.advance().Pos
			c := &ast.CaseClause{NodeBase: base(dpos), Default: true}
			p.expect(token.Colon)
			c.Body = p.parseCaseBody()
			sw.Cases = append(sw.Cases, c)
		case token.RBrace, token.EOF:
			p.expect(token.RBrace)
			return sw
		default:
			p.errorf(p.cur().Pos, "expected case or default, found %s", describe(p.cur()))
		}
	}
}

// parseCaseValue parses a case value, which may be an inclusive interval
// `lo..hi`.
func (p *parser) parseCaseValue() ast.Expr {
	lo := p.parseExpr()
	if p.at(token.DotDot) {
		pos := p.advance().Pos
		hi := p.parseExpr()
		return &ast.RangeExpr{NodeBase: base(pos), Lo: lo, Hi: hi}
	}
	return lo
}

func (p *parser) parseCaseBody() []ast.Stmt {
	var body []ast.Stmt
	for {
		p.skipSemis()
		switch p.cur().Kind {
		case token.Case, token.Default, token.RBrace, token.EOF:
			return body
		}
		body = append(body, p.parseStmt())
	}
}

func (p *parser) parseReturn() ast.Stmt {
	kw := p.expect(token.Return)
	r := &ast.ReturnStmt{NodeBase: base(kw.Pos)}
	if !p.at(token.Semicolon) && !p.at(token.RBrace) && !p.at(token.EOF) {
		r.Result = p.parseExpr()
	}
	return r
}

// ---------------------------------------------------------------------------
// Expressions
// ---------------------------------------------------------------------------

var precedence = map[token.Kind]int{
	token.Or:      1,
	token.And:     2,
	token.Eq:      3,
	token.Ne:      3,
	token.Lt:      3,
	token.Le:      3,
	token.Gt:      3,
	token.Ge:      3,
	token.Pipe:    4,
	token.Caret:   5,
	token.Amp:     6,
	token.Shl:     7,
	token.Shr:     7,
	token.Plus:    8,
	token.Minus:   8,
	token.Star:    9,
	token.Slash:   9,
	token.Percent: 9,
}

func (p *parser) parseExpr() ast.Expr {
	x := p.parseBinary(1)
	if p.at(token.Question) {
		pos := p.cur().Pos
		p.advance()
		then := p.parseExpr()
		p.expect(token.Colon)
		els := p.parseExpr()
		return &ast.TernaryExpr{NodeBase: base(pos), Cond: x, Then: then, Else: els}
	}
	return x
}

func (p *parser) parseBinary(minPrec int) ast.Expr {
	x := p.parseUnary()
	for {
		op := p.cur().Kind
		prec, ok := precedence[op]
		if !ok || prec < minPrec {
			return x
		}
		pos := p.cur().Pos
		p.advance()
		y := p.parseBinary(prec + 1)
		x = &ast.BinaryExpr{NodeBase: base(pos), Op: op, X: x, Y: y}
	}
}

func (p *parser) parseUnary() ast.Expr {
	switch p.cur().Kind {
	case token.Minus, token.Plus:
		t := p.advance()
		// Fold the sign into a numeric literal so unit conversion sees it:
		// "-45c" must convert to -45C (228.15K), not negate 45C (=-318.15K).
		if p.cur().Kind == token.Number {
			n := p.advance()
			text := t.Text + n.Text
			return &ast.NumberLit{NodeBase: base(t.Pos), Value: parseNumber(text), Text: text}
		}
		x := p.parseUnary()
		return &ast.UnaryExpr{NodeBase: base(t.Pos), Op: t.Kind, X: x}
	case token.Not, token.Tilde:
		t := p.advance()
		x := p.parseUnary()
		return &ast.UnaryExpr{NodeBase: base(t.Pos), Op: t.Kind, X: x}
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() ast.Expr {
	x := p.parsePrimary()
	for {
		switch p.cur().Kind {
		case token.Dot:
			pos := p.advance().Pos
			sel := p.parseIdent()
			x = &ast.SelectorExpr{NodeBase: base(pos), X: x, Sel: sel}
		case token.LBracket:
			pos := p.advance().Pos
			idx := p.parseExpr()
			p.expect(token.RBracket)
			x = &ast.IndexExpr{NodeBase: base(pos), X: x, Index: idx}
		case token.LParen:
			pos := p.advance().Pos
			var args []ast.Expr
			for !p.at(token.RParen) {
				args = append(args, p.parseExpr())
				if p.at(token.Comma) {
					p.advance()
					continue
				}
				break
			}
			p.expect(token.RParen)
			x = &ast.CallExpr{NodeBase: base(pos), Fun: x, Args: args}
		default:
			return x
		}
	}
}

func (p *parser) parsePrimary() ast.Expr {
	t := p.cur()
	switch t.Kind {
	case token.Ident:
		p.advance()
		return &ast.Ident{NodeBase: base(t.Pos), Name: t.Text}
	case token.Number:
		p.advance()
		return &ast.NumberLit{NodeBase: base(t.Pos), Value: parseNumber(t.Text), Text: t.Text}
	case token.String:
		p.advance()
		return &ast.StringLit{NodeBase: base(t.Pos), Value: t.Text}
	case token.Device:
		p.advance()
		return &ast.DeviceLit{NodeBase: base(t.Pos), Name: t.Text}
	case token.True:
		p.advance()
		return &ast.BoolLit{NodeBase: base(t.Pos), Value: true}
	case token.False:
		p.advance()
		return &ast.BoolLit{NodeBase: base(t.Pos), Value: false}
	case token.NaN, token.PInf, token.NInf:
		p.advance()
		return &ast.SpecialLit{NodeBase: base(t.Pos), Name: t.Text}
	case token.LParen:
		p.advance()
		x := p.parseExpr()
		p.expect(token.RParen)
		return &ast.ParenExpr{NodeBase: base(t.Pos), X: x}
	default:
		p.errorf(t.Pos, "expected expression, found %s", describe(t))
		return nil
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func base(pos source.Pos) ast.NodeBase { return ast.NodeBase{Pos_: pos} }

func parseNumber(text string) float64 {
	s := strings.ReplaceAll(text, "_", "")
	if v, ok := token.ConvertUnit(s); ok {
		return v
	}
	neg := false
	switch {
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	case strings.HasPrefix(s, "-"):
		neg = true
		s = s[1:]
	}
	var v float64
	switch {
	case strings.HasPrefix(s, "0x"), strings.HasPrefix(s, "0X"):
		if u, err := strconv.ParseUint(s[2:], 16, 64); err == nil {
			v = float64(u)
		}
	case strings.HasPrefix(s, "0b"), strings.HasPrefix(s, "0B"):
		if u, err := strconv.ParseUint(s[2:], 2, 64); err == nil {
			v = float64(u)
		}
	default:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			v = f
		}
	}
	if neg {
		v = -v
	}
	return v
}

// syncTop skips tokens until the next likely top-level declaration.
func (p *parser) syncTop() {
	depth := 0
	for !p.at(token.EOF) {
		switch p.cur().Kind {
		case token.LBrace:
			depth++
		case token.RBrace:
			if depth > 0 {
				depth--
			}
		case token.Func, token.Var, token.Const, token.Data, token.Chip, token.Import:
			if depth == 0 {
				return
			}
		}
		p.advance()
	}
}

// syncStmt skips tokens until the end of the current statement or block.
func (p *parser) syncStmt() {
	depth := 0
	for !p.at(token.EOF) {
		switch p.cur().Kind {
		case token.LBrace:
			depth++
		case token.RBrace:
			if depth == 0 {
				return
			}
			depth--
		case token.Semicolon:
			if depth == 0 {
				p.advance()
				return
			}
		}
		p.advance()
	}
}
