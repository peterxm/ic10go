// Package lower translates a type-checked AST into IR. Functions are inlined at
// this stage, so the resulting IR contains no calls.
package lower

import (
	"math"
	"os"
	"strconv"

	"ic10go/internal/ast"
	"ic10go/internal/builtin"
	"ic10go/internal/diag"
	"ic10go/internal/ir"
	"ic10go/internal/sema"
	"ic10go/internal/source"
	"ic10go/internal/token"
)

// Lower compiles the program's main function into an IR function.
func Lower(info *sema.Info, diags *diag.Bag) *ir.Function {
	l := &lowerer{
		b:        ir.NewBuilder("main"),
		info:     info,
		diags:    diags,
		labels:   map[string]*ir.Block{},
		labelDef: map[string]source.Pos{},
		labelUse: map[string]source.Pos{},
		noCheck:  os.Getenv("IC10C_NO_CHECK") != "",
	}
	scope := map[string]ir.Value{}
	for name, v := range info.Consts {
		scope[name] = &ir.Const{V: v}
	}
	for name, raw := range info.RawConsts {
		scope[name] = &ir.Const{Raw: raw}
	}
	l.scopes = append(l.scopes, scope)

	end := l.b.NewBlock()
	l.inline = append(l.inline, inlineCtx{end: end})

	l.lowerStmts(info.Main.Body.List)
	if l.b.Cur().Term == nil {
		l.b.SetTerm(&ir.Jmp{Target: end})
	}
	l.b.SetBlock(end)
	l.b.SetTerm(&ir.Ret{})

	for name, pos := range l.labelUse {
		if _, ok := l.labelDef[name]; !ok {
			l.diags.Errorf(pos, "undefined label %q", name)
		}
	}

	fn := l.b.Fn()
	for _, blk := range fn.Blocks {
		if blk.Term == nil {
			blk.Term = &ir.Ret{}
		}
	}
	fn.BuildCFG()
	simplify(fn)
	return fn
}

// simplify redirects jumps through empty blocks so that later stages can avoid
// emitting redundant branches.
func simplify(fn *ir.Function) {
	resolve := func(b *ir.Block) *ir.Block {
		seen := map[*ir.Block]bool{}
		for b != nil && !seen[b] {
			seen[b] = true
			if len(b.Instrs) != 0 {
				return b
			}
			j, ok := b.Term.(*ir.Jmp)
			if !ok {
				return b
			}
			b = j.Target
		}
		return b
	}
	for _, b := range fn.Blocks {
		switch t := b.Term.(type) {
		case *ir.Jmp:
			t.Target = resolve(t.Target)
		case *ir.Br:
			t.Then = resolve(t.Then)
			t.Else = resolve(t.Else)
		}
	}
	fn.BuildCFG()
}

type inlineCtx struct {
	result *ir.Reg
	end    *ir.Block
}

type loopCtx struct {
	breakB    *ir.Block
	continueB *ir.Block
}

type lowerer struct {
	b     *ir.Builder
	info  *sema.Info
	diags *diag.Bag

	scopes []map[string]ir.Value
	inline []inlineCtx
	loops  []loopCtx
	stack  []string // names of functions currently being inlined
	labels map[string]*ir.Block
	// labelDef and labelUse track low-level label definitions and references.
	labelDef map[string]source.Pos
	labelUse map[string]source.Pos
	noCheck  bool
}

// ---------------------------------------------------------------------------
// Statements
// ---------------------------------------------------------------------------

func (l *lowerer) ensure() {
	if l.b.Cur().Term != nil {
		l.b.SetBlock(l.b.NewBlock())
	}
}

func (l *lowerer) lowerStmts(list []ast.Stmt) {
	for _, s := range list {
		l.ensure()
		l.lowerStmt(s)
	}
}

func (l *lowerer) lowerBlock(b *ast.BlockStmt) {
	l.pushScope()
	l.lowerStmts(b.List)
	l.popScope()
}

func (l *lowerer) lowerStmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.BlockStmt:
		l.lowerBlock(s)
	case *ast.DeclStmt:
		l.lowerDecl(s.Decl)
	case *ast.ExprStmt:
		l.lowerCallExpr(s.X, false)
	case *ast.AssignStmt:
		l.lowerAssign(s)
	case *ast.IncDecStmt:
		l.lowerIncDec(s)
	case *ast.IfStmt:
		l.lowerIf(s)
	case *ast.ForStmt:
		l.lowerFor(s)
	case *ast.SwitchStmt:
		l.lowerSwitch(s)
	case *ast.BreakStmt:
		if len(l.loops) == 0 {
			l.diags.Errorf(s.Pos(), "break outside of loop or switch")
			return
		}
		l.b.SetTerm(&ir.Jmp{Target: l.loops[len(l.loops)-1].breakB})
	case *ast.ContinueStmt:
		if len(l.loops) == 0 || l.loops[len(l.loops)-1].continueB == nil {
			l.diags.Errorf(s.Pos(), "continue outside of loop")
			return
		}
		l.b.SetTerm(&ir.Jmp{Target: l.loops[len(l.loops)-1].continueB})
	case *ast.ReturnStmt:
		l.lowerReturn(s)
	case *ast.LabelStmt:
		l.lowerLabel(s)
	case *ast.GotoStmt:
		l.b.SetTerm(&ir.Goto{Target: l.useLabel(s.Name.Name, s.Name.Pos())})
	case *ast.CallStmt:
		target := l.useLabel(s.Name.Name, s.Name.Pos())
		ret := l.b.NewBlock()
		l.b.SetTerm(&ir.Call{Target: target, Return: ret})
		l.b.SetBlock(ret)
	case *ast.RetStmt:
		l.b.SetTerm(&ir.JmpRA{})
	}
}

// useLabel records a reference to a label and returns its block.
func (l *lowerer) useLabel(name string, pos source.Pos) *ir.Block {
	if _, ok := l.labelUse[name]; !ok {
		l.labelUse[name] = pos
	}
	return l.labelBlock(name)
}

// labelBlock returns (creating if needed) the block associated with a label.
func (l *lowerer) labelBlock(name string) *ir.Block {
	if b, ok := l.labels[name]; ok {
		return b
	}
	b := l.b.NewBlock()
	l.labels[name] = b
	return b
}

func (l *lowerer) lowerLabel(s *ast.LabelStmt) {
	name := s.Name.Name
	if prev, ok := l.labelDef[name]; ok {
		l.diags.Errorf(s.Name.Pos(), "label %q already defined at %s", name, prev)
		return
	}
	l.labelDef[name] = s.Name.Pos()

	cur := l.b.Cur()
	if b, ok := l.labels[name]; ok {
		if b == cur {
			return
		}
		if cur.Term == nil {
			l.b.SetTerm(&ir.Jmp{Target: b})
		}
		l.b.SetBlock(b)
		return
	}
	if cur.Term == nil && len(cur.Instrs) == 0 {
		l.labels[name] = cur
		return
	}
	b := l.b.NewBlock()
	if cur.Term == nil {
		l.b.SetTerm(&ir.Jmp{Target: b})
	}
	l.labels[name] = b
	l.b.SetBlock(b)
}

func (l *lowerer) lowerDecl(d ast.Decl) {
	switch d := d.(type) {
	case *ast.ConstDecl:
		if raw, ok := sema.EvalRaw(d.Value); ok {
			l.bind(d.Name.Name, &ir.Const{Raw: raw})
			return
		}
		v, ok := sema.Eval(d.Value, l.constEnv())
		if !ok {
			l.diags.Errorf(d.Value.Pos(), "constant %q is not a compile-time expression", d.Name.Name)
			return
		}
		l.bind(d.Name.Name, &ir.Const{V: v})
	case *ast.VarDecl:
		r := l.b.NewReg(d.Name.Name)
		var init ir.Value = &ir.Const{V: 0}
		if d.Value != nil {
			init = l.lowerExpr(d.Value)
		}
		l.b.Emit(&ir.Assign{Dst: r, Src: init})
		l.bind(d.Name.Name, r)
	}
}

func (l *lowerer) lowerAssign(s *ast.AssignStmt) {
	// Compound assignment is desugared into load-op-store.
	if s.Op != token.Assign && s.Op != token.Define {
		op, ok := compoundBinOp(s.Op)
		if !ok {
			l.diags.Errorf(s.Pos(), "unsupported assignment operator %s", s.Op)
			return
		}
		cur := l.lowerExpr(s.Lhs)
		rhs := l.lowerExpr(s.Rhs)
		res := l.b.NewReg("assign")
		l.emitBin(op, res, cur, rhs)
		l.storeTo(s.Lhs, res)
		return
	}

	if s.Op == token.Define {
		id, ok := s.Lhs.(*ast.Ident)
		if !ok {
			l.diags.Errorf(s.Lhs.Pos(), "left side of := must be an identifier")
			return
		}
		r := l.b.NewReg(id.Name)
		l.b.Emit(&ir.Assign{Dst: r, Src: l.lowerExpr(s.Rhs)})
		l.bind(id.Name, r)
		return
	}

	// Plain assignment.
	if id, ok := s.Lhs.(*ast.Ident); ok {
		v, ok := l.lookup(id.Name)
		if !ok {
			if isSpecialReg(id.Name) {
				l.b.Emit(&ir.StoreSpecial{Name: id.Name, Src: l.lowerExpr(s.Rhs)})
				return
			}
			l.diags.Errorf(id.Pos(), "undefined variable %q", id.Name)
			return
		}
		if _, isConst := v.(*ir.Const); isConst {
			l.diags.Errorf(id.Pos(), "cannot assign to constant %q", id.Name)
			return
		}
		r := v.(*ir.Reg)
		l.b.Emit(&ir.Assign{Dst: r, Src: l.lowerExpr(s.Rhs)})
		return
	}
	l.storeTo(s.Lhs, l.lowerExpr(s.Rhs))
}

func (l *lowerer) lowerIncDec(s *ast.IncDecStmt) {
	op := ir.Add
	if s.Op == token.MinusMinus {
		op = ir.Sub
	}
	cur := l.lowerExpr(s.X)
	res := l.b.NewReg("incdec")
	l.emitBin(op, res, cur, &ir.Const{V: 1})
	l.storeTo(s.X, res)
}

// storeTo writes a value to an assignment target (identifier, device or slot).
func (l *lowerer) storeTo(target ast.Expr, val ir.Value) {
	switch t := target.(type) {
	case *ast.Ident:
		v, ok := l.lookup(t.Name)
		if !ok {
			if isSpecialReg(t.Name) {
				l.b.Emit(&ir.StoreSpecial{Name: t.Name, Src: val})
				return
			}
			l.diags.Errorf(t.Pos(), "undefined variable %q", t.Name)
			return
		}
		r, ok := v.(*ir.Reg)
		if !ok {
			l.diags.Errorf(t.Pos(), "cannot assign to constant %q", t.Name)
			return
		}
		l.b.Emit(&ir.Assign{Dst: r, Src: val})
	case *ast.SelectorExpr:
		if dev, ok := deviceOf(t.X); ok {
			l.checkLogic(t.Sel.Pos(), t.Sel.Name)
			l.b.Emit(&ir.Store{Dev: dev, Logic: t.Sel.Name, Src: val})
			return
		}
		if dev, idx, ok := slotOf(t.X); ok {
			l.checkSlot(t.Sel.Pos(), t.Sel.Name)
			l.b.Emit(&ir.StoreSlot{Dev: dev, Index: l.lowerExpr(idx), Logic: t.Sel.Name, Src: val})
			return
		}
		l.diags.Errorf(t.Pos(), "unsupported assignment target")
	case *ast.IndexExpr:
		if dev, conn, ch, ok := l.channelOf(t); ok {
			l.b.Emit(&ir.Store{Dev: channelDev(dev, conn), Logic: "Channel" + itoa(ch), Src: val})
			return
		}
		l.diags.Errorf(t.Pos(), "unsupported assignment target")
	default:
		l.diags.Errorf(target.Pos(), "unsupported assignment target")
	}
}

func (l *lowerer) lowerReturn(s *ast.ReturnStmt) {
	ctx := l.inline[len(l.inline)-1]
	if s.Result != nil {
		v := l.lowerExpr(s.Result)
		if ctx.result != nil {
			l.b.Emit(&ir.Assign{Dst: ctx.result, Src: v})
		} else {
			l.diags.Errorf(s.Pos(), "return with a value in a void function")
		}
	}
	l.b.SetTerm(&ir.Jmp{Target: ctx.end})
}

func (l *lowerer) lowerIf(s *ast.IfStmt) {
	thenB := l.b.NewBlock()
	elseB := l.b.NewBlock()
	endB := l.b.NewBlock()
	l.branchCond(s.Cond, thenB, elseB)

	l.b.SetBlock(thenB)
	l.lowerBlock(s.Then)
	if l.b.Cur().Term == nil {
		l.b.SetTerm(&ir.Jmp{Target: endB})
	}

	l.b.SetBlock(elseB)
	if s.Else != nil {
		l.lowerStmt(s.Else)
	}
	if l.b.Cur().Term == nil {
		l.b.SetTerm(&ir.Jmp{Target: endB})
	}

	l.b.SetBlock(endB)
}

func (l *lowerer) lowerFor(s *ast.ForStmt) {
	l.pushScope()
	if s.Init != nil {
		l.lowerStmt(s.Init)
	}
	condB := l.b.NewBlock()
	bodyB := l.b.NewBlock()
	postB := l.b.NewBlock()
	endB := l.b.NewBlock()

	l.b.SetTerm(&ir.Jmp{Target: condB})

	l.b.SetBlock(condB)
	if s.Cond != nil {
		l.branchCond(s.Cond, bodyB, endB)
	} else {
		l.b.SetTerm(&ir.Jmp{Target: bodyB})
	}

	l.b.SetBlock(bodyB)
	l.loops = append(l.loops, loopCtx{breakB: endB, continueB: postB})
	l.lowerBlock(s.Body)
	l.loops = l.loops[:len(l.loops)-1]
	if l.b.Cur().Term == nil {
		l.b.SetTerm(&ir.Jmp{Target: postB})
	}

	l.b.SetBlock(postB)
	if s.Post != nil {
		l.lowerStmt(s.Post)
	}
	if l.b.Cur().Term == nil {
		l.b.SetTerm(&ir.Jmp{Target: condB})
	}

	l.b.SetBlock(endB)
	l.popScope()
}

func (l *lowerer) lowerSwitch(s *ast.SwitchStmt) {
	endB := l.b.NewBlock()
	l.loops = append(l.loops, loopCtx{breakB: endB})
	defer func() { l.loops = l.loops[:len(l.loops)-1] }()

	var tag ir.Value
	if s.Tag != nil {
		tag = l.lowerExpr(s.Tag)
	}

	bodyBlocks := make([]*ir.Block, len(s.Cases))
	for i := range s.Cases {
		bodyBlocks[i] = l.b.NewBlock()
	}

	defaultIdx := -1
	for i, c := range s.Cases {
		if c.Default {
			defaultIdx = i
		}
	}

	for i, c := range s.Cases {
		if c.Default {
			continue
		}
		for j, ce := range c.Exprs {
			l.ensure()
			last := j == len(c.Exprs)-1
			var next *ir.Block
			if last {
				next = l.b.NewBlock() // fallthrough target, patched below
			} else {
				next = l.b.NewBlock()
			}
			if s.Tag != nil {
				v := l.lowerExpr(ce)
				cmp := l.b.NewReg("swcmp")
				l.b.Emit(&ir.Cmp{Cond: ir.Eq, Dst: cmp, A: tag, B: v})
				l.b.SetTerm(&ir.Br{Cond: ir.NonZero, A: cmp, Then: bodyBlocks[i], Else: next})
			} else {
				v := l.lowerExpr(ce)
				l.b.SetTerm(&ir.Br{Cond: ir.NonZero, A: v, Then: bodyBlocks[i], Else: next})
			}
			l.b.SetBlock(next)
		}
	}

	// Fallthrough after all tests.
	l.ensure()
	if defaultIdx >= 0 {
		l.b.SetTerm(&ir.Jmp{Target: bodyBlocks[defaultIdx]})
	} else {
		l.b.SetTerm(&ir.Jmp{Target: endB})
	}

	for i, c := range s.Cases {
		l.b.SetBlock(bodyBlocks[i])
		l.lowerStmts(c.Body)
		if l.b.Cur().Term == nil {
			l.b.SetTerm(&ir.Jmp{Target: endB})
		}
	}

	l.b.SetBlock(endB)
}

// ---------------------------------------------------------------------------
// Expressions
// ---------------------------------------------------------------------------

func (l *lowerer) lowerExpr(e ast.Expr) ir.Value {
	switch e := e.(type) {
	case *ast.NumberLit:
		return &ir.Const{V: e.Value}
	case *ast.BoolLit:
		if e.Value {
			return &ir.Const{V: 1}
		}
		return &ir.Const{V: 0}
	case *ast.SpecialLit:
		return &ir.Const{Special: e.Name}
	case *ast.ParenExpr:
		return l.lowerExpr(e.X)
	case *ast.Ident:
		v, ok := l.lookup(e.Name)
		if !ok {
			if isSpecialReg(e.Name) {
				r := l.b.NewReg(e.Name)
				l.b.Emit(&ir.LoadSpecial{Dst: r, Name: e.Name})
				return r
			}
			l.diags.Errorf(e.Pos(), "undefined variable %q", e.Name)
			return &ir.Const{V: 0}
		}
		return v
	case *ast.UnaryExpr:
		return l.lowerUnary(e)
	case *ast.BinaryExpr:
		return l.lowerBinary(e)
	case *ast.TernaryExpr:
		return l.lowerTernary(e)
	case *ast.CallExpr:
		return l.lowerCallExpr(e, true)
	case *ast.SelectorExpr:
		return l.lowerDeviceRead(e)
	case *ast.IndexExpr:
		if dev, conn, ch, ok := l.channelOf(e); ok {
			r := l.b.NewReg("channel")
			l.b.Emit(&ir.Load{Dst: r, Dev: channelDev(dev, conn), Logic: "Channel" + itoa(ch)})
			return r
		}
		l.diags.Errorf(e.Pos(), "index expression is not a value")
		return &ir.Const{V: 0}
	case *ast.DeviceLit:
		l.diags.Errorf(e.Pos(), "device %q cannot be used as a value", e.Name)
		return &ir.Const{V: 0}
	default:
		l.diags.Errorf(e.Pos(), "unsupported expression")
		return &ir.Const{V: 0}
	}
}

// lowerCond lowers a condition into a branch condition, fusing comparisons so
// that a separate comparison instruction is not materialised.
// branchCond emits a branch for a condition, recognising the device load/store
// validity builtins (isLoadValid / isStoreValid).
func (l *lowerer) branchCond(e ast.Expr, thenB, elseB *ir.Block) {
	neg := false
	inner := e
	if un, ok := e.(*ast.UnaryExpr); ok && un.Op == token.Not {
		neg = true
		inner = un.X
	}
	if call, ok := inner.(*ast.CallExpr); ok {
		if id, ok := call.Fun.(*ast.Ident); ok && (id.Name == "isLoadValid" || id.Name == "isStoreValid") {
			dev, logic, ok := l.validArgs(call)
			if !ok {
				return
			}
			valid, invalid := thenB, elseB
			if neg {
				valid, invalid = elseB, thenB
			}
			l.b.SetTerm(&ir.BrValid{
				Dev:     dev,
				Logic:   logic,
				Store:   id.Name == "isStoreValid",
				Valid:   valid,
				Invalid: invalid,
			})
			return
		}
	}
	cond, a, b := l.lowerCond(e)
	l.b.SetTerm(&ir.Br{Cond: cond, A: a, B: b, Then: thenB, Else: elseB})
}

// validArgs parses (device, "logicType") for the validity builtins.
func (l *lowerer) validArgs(call *ast.CallExpr) (string, string, bool) {
	if len(call.Args) != 2 {
		l.diags.Errorf(call.Pos(), "expected a device and a logic type")
		return "", "", false
	}
	dev, ok := call.Args[0].(*ast.DeviceLit)
	if !ok {
		l.diags.Errorf(call.Args[0].Pos(), "expected a device as the first argument")
		return "", "", false
	}
	logic, ok := call.Args[1].(*ast.StringLit)
	if !ok {
		l.diags.Errorf(call.Args[1].Pos(), "expected a logic type string as the second argument")
		return "", "", false
	}
	return dev.Name, logic.Value, true
}

func (l *lowerer) lowerCond(e ast.Expr) (ir.Cond, ir.Value, ir.Value) {
	if bin, ok := e.(*ast.BinaryExpr); ok {
		if c, ok := condOf(bin.Op); ok {
			a := l.lowerExpr(bin.X)
			b := l.lowerExpr(bin.Y)
			return c, a, b
		}
	}
	v := l.lowerExpr(e)
	return ir.NonZero, v, nil
}

func (l *lowerer) lowerUnary(e *ast.UnaryExpr) ir.Value {
	x := l.lowerExpr(e.X)
	if c, ok := x.(*ir.Const); ok {
		if f, ok := foldUnary(e.Op, c); ok {
			return f
		}
	}
	r := l.b.NewReg("un")
	switch e.Op {
	case token.Minus:
		l.b.Emit(&ir.Un{Op: ir.Neg, Dst: r, A: x})
	case token.Tilde:
		l.b.Emit(&ir.Un{Op: ir.BitNot, Dst: r, A: x})
	case token.Not:
		l.b.Emit(&ir.Cmp{Cond: ir.Zero, Dst: r, A: x})
	case token.Plus:
		return x
	}
	return r
}

func (l *lowerer) lowerBinary(e *ast.BinaryExpr) ir.Value {
	switch e.Op {
	case token.And:
		return l.lowerShortCircuit(e, true)
	case token.Or:
		return l.lowerShortCircuit(e, false)
	}

	a := l.lowerExpr(e.X)
	b := l.lowerExpr(e.Y)

	if ca, ok := a.(*ir.Const); ok {
		if cb, ok := b.(*ir.Const); ok {
			if cond, ok := condOf(e.Op); ok {
				if f, ok := foldCmp(cond, ca, cb); ok {
					return f
				}
			}
			if op, ok := binOpOf(e.Op); ok {
				if f, ok := foldBin(op, ca, cb); ok {
					return f
				}
			}
		}
	}

	r := l.b.NewReg("bin")
	if cond, ok := condOf(e.Op); ok {
		l.b.Emit(&ir.Cmp{Cond: cond, Dst: r, A: a, B: b})
		return r
	}
	op, ok := binOpOf(e.Op)
	if !ok {
		l.diags.Errorf(e.Pos(), "unsupported operator %s", e.Op)
		return &ir.Const{V: 0}
	}
	l.emitBin(op, r, a, b)
	return r
}

func (l *lowerer) emitBin(op ir.BinOp, dst *ir.Reg, a, b ir.Value) {
	if ca, ok := a.(*ir.Const); ok {
		if cb, ok := b.(*ir.Const); ok {
			if f, ok := foldBin(op, ca, cb); ok {
				l.b.Emit(&ir.Assign{Dst: dst, Src: f})
				return
			}
		}
	}
	l.b.Emit(&ir.Bin{Op: op, Dst: dst, A: a, B: b})
}

func (l *lowerer) lowerShortCircuit(e *ast.BinaryExpr, isAnd bool) ir.Value {
	// When both operands are free of side effects, IC10's min/max directly
	// implement logical AND/OR and avoid branches.
	if isPure(e.X) && isPure(e.Y) {
		a := l.lowerExpr(e.X)
		b := l.lowerExpr(e.Y)
		op := ir.Min
		if !isAnd {
			op = ir.Max
		}
		r := l.b.NewReg("logic")
		l.emitBin(op, r, a, b)
		return r
	}

	a := l.lowerExpr(e.X)
	res := l.b.NewReg("logic")
	endB := l.b.NewBlock()
	contB := l.b.NewBlock()
	if isAnd {
		l.b.Emit(&ir.Assign{Dst: res, Src: &ir.Const{V: 0}})
		l.b.SetTerm(&ir.Br{Cond: ir.NonZero, A: a, Then: contB, Else: endB})
	} else {
		l.b.Emit(&ir.Assign{Dst: res, Src: a})
		l.b.SetTerm(&ir.Br{Cond: ir.Zero, A: a, Then: contB, Else: endB})
	}
	l.b.SetBlock(contB)
	b := l.lowerExpr(e.Y)
	op := ir.Min
	if !isAnd {
		op = ir.Max
	}
	l.b.Emit(&ir.Bin{Op: op, Dst: res, A: a, B: b})
	l.b.SetTerm(&ir.Jmp{Target: endB})
	l.b.SetBlock(endB)
	return res
}

func (l *lowerer) lowerTernary(e *ast.TernaryExpr) ir.Value {
	cond, a, b := l.lowerCond(e.Cond)
	res := l.b.NewReg("tern")
	thenB := l.b.NewBlock()
	elseB := l.b.NewBlock()
	endB := l.b.NewBlock()
	l.b.SetTerm(&ir.Br{Cond: cond, A: a, B: b, Then: thenB, Else: elseB})

	l.b.SetBlock(thenB)
	l.b.Emit(&ir.Assign{Dst: res, Src: l.lowerExpr(e.Then)})
	l.b.SetTerm(&ir.Jmp{Target: endB})

	l.b.SetBlock(elseB)
	l.b.Emit(&ir.Assign{Dst: res, Src: l.lowerExpr(e.Else)})
	l.b.SetTerm(&ir.Jmp{Target: endB})

	l.b.SetBlock(endB)
	return res
}

func (l *lowerer) lowerDeviceRead(e *ast.SelectorExpr) ir.Value {
	if dev, ok := deviceOf(e.X); ok {
		l.checkLogic(e.Sel.Pos(), e.Sel.Name)
		r := l.b.NewReg(e.Sel.Name)
		l.b.Emit(&ir.Load{Dst: r, Dev: dev, Logic: e.Sel.Name})
		return r
	}
	if dev, idx, ok := slotOf(e.X); ok {
		l.checkSlot(e.Sel.Pos(), e.Sel.Name)
		r := l.b.NewReg(e.Sel.Name)
		l.b.Emit(&ir.LoadSlot{Dst: r, Dev: dev, Index: l.lowerExpr(idx), Logic: e.Sel.Name})
		return r
	}
	l.diags.Errorf(e.Pos(), "unsupported device access")
	return &ir.Const{V: 0}
}

// checkLogic warns about a logic type that is not in the built-in table.
func (l *lowerer) checkLogic(pos source.Pos, name string) {
	if l.noCheck || builtin.LogicTypes[name] {
		return
	}
	l.diags.Warnf(pos, "unknown logic type %q", name)
}

// checkSlot warns about a slot type that is not in the built-in table.
func (l *lowerer) checkSlot(pos source.Pos, name string) {
	if l.noCheck || builtin.SlotTypes[name] {
		return
	}
	l.diags.Warnf(pos, "unknown slot type %q", name)
}

// ---------------------------------------------------------------------------
// Calls and inlining
// ---------------------------------------------------------------------------

func (l *lowerer) lowerCallExpr(e ast.Expr, needResult bool) ir.Value {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		if needResult {
			return l.lowerExpr(e)
		}
		l.lowerExpr(e)
		return &ir.Const{V: 0}
	}
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if base, ok := sel.X.(*ast.Ident); ok && base.Name == "batch" {
			return l.lowerBatchCall(call, sel.Sel.Name, needResult)
		}
		l.diags.Errorf(call.Pos(), "unsupported call target")
		return &ir.Const{V: 0}
	}

	id, ok := call.Fun.(*ast.Ident)
	if !ok {
		l.diags.Errorf(call.Pos(), "unsupported call target")
		return &ir.Const{V: 0}
	}

	// hash("...") is a compile-time constant.
	if id.Name == "hash" {
		if len(call.Args) != 1 {
			l.diags.Errorf(call.Pos(), "hash expects one string argument")
			return &ir.Const{V: 0}
		}
		s, ok := call.Args[0].(*ast.StringLit)
		if !ok {
			l.diags.Errorf(call.Args[0].Pos(), "hash expects a string literal")
			return &ir.Const{V: 0}
		}
		return &ir.Const{V: float64(builtin.Hash(s.Value))}
	}

	// str("...") produces a display string hash.
	if id.Name == "str" {
		if len(call.Args) != 1 {
			l.diags.Errorf(call.Pos(), "str expects one string argument")
			return &ir.Const{V: 0}
		}
		s, ok := call.Args[0].(*ast.StringLit)
		if !ok {
			l.diags.Errorf(call.Args[0].Pos(), "str expects a string literal")
			return &ir.Const{V: 0}
		}
		return &ir.Const{Raw: "STR(" + strconv.Quote(s.Value) + ")"}
	}

	// isLoadValid / isStoreValid are condition-only builtins.
	if id.Name == "isLoadValid" || id.Name == "isStoreValid" {
		l.diags.Errorf(call.Pos(), "%s can only be used in an if/for condition", id.Name)
		return &ir.Const{V: 0}
	}

	// read(dev, lt) / write(dev, lt, v) use a runtime logic type (IC10 "l r? d? rN").
	if id.Name == "read" {
		if len(call.Args) != 2 {
			l.diags.Errorf(call.Pos(), "read expects a device and a logic type")
			return &ir.Const{V: 0}
		}
		dev, ok := call.Args[0].(*ast.DeviceLit)
		if !ok {
			l.diags.Errorf(call.Args[0].Pos(), "read expects a device as its first argument")
			return &ir.Const{V: 0}
		}
		logic := l.lowerExpr(call.Args[1])
		r := l.b.NewReg("read")
		l.b.Emit(&ir.LoadDyn{Dst: r, Dev: dev.Name, Logic: logic})
		return r
	}
	if id.Name == "write" {
		if len(call.Args) != 3 {
			l.diags.Errorf(call.Pos(), "write expects a device, a logic type and a value")
			return &ir.Const{V: 0}
		}
		dev, ok := call.Args[0].(*ast.DeviceLit)
		if !ok {
			l.diags.Errorf(call.Args[0].Pos(), "write expects a device as its first argument")
			return &ir.Const{V: 0}
		}
		logic := l.lowerExpr(call.Args[1])
		src := l.lowerExpr(call.Args[2])
		l.b.Emit(&ir.StoreDyn{Dev: dev.Name, Logic: logic, Src: src})
		return &ir.Const{V: 0}
	}

	// jump(expr) performs a computed jump (IC10 "j r0").
	if id.Name == "jump" {
		if len(call.Args) != 1 {
			l.diags.Errorf(call.Pos(), "jump expects one argument")
			return &ir.Const{V: 0}
		}
		v := l.lowerExpr(call.Args[0])
		l.b.SetTerm(&ir.JmpDyn{Target: v})
		return &ir.Const{V: 0}
	}

	// ireg(ptr) / setIreg(ptr, v) access indirect registers (IC10 rrN).
	if id.Name == "ireg" {
		if len(call.Args) != 1 {
			l.diags.Errorf(call.Pos(), "ireg expects one argument")
			return &ir.Const{V: 0}
		}
		dst := l.b.NewReg("ireg")
		l.b.Emit(&ir.LoadIndirect{Dst: dst, Ptr: l.lowerExpr(call.Args[0])})
		return dst
	}
	if id.Name == "setIreg" {
		if len(call.Args) != 2 {
			l.diags.Errorf(call.Pos(), "setIreg expects two arguments")
			return &ir.Const{V: 0}
		}
		ptr := l.lowerExpr(call.Args[0])
		src := l.lowerExpr(call.Args[1])
		l.b.Emit(&ir.StoreIndirect{Ptr: ptr, Src: src})
		return &ir.Const{V: 0}
	}

	// User-defined functions take precedence over built-ins.
	if fi, ok := l.info.Funcs[id.Name]; ok {
		return l.inlineCall(id, fi, call.Args, needResult)
	}

	if f, ok := builtin.Funcs[id.Name]; ok {
		if needResult && !f.Result {
			l.diags.Errorf(call.Pos(), "%s does not return a value", id.Name)
			return &ir.Const{V: 0}
		}
		if len(call.Args) != f.Args {
			l.diags.Errorf(call.Pos(), "%s expects %d arguments, got %d", id.Name, f.Args, len(call.Args))
			return &ir.Const{V: 0}
		}
		args := make([]ir.Value, len(call.Args))
		for i, a := range call.Args {
			if i == 0 && deviceFirstArg[id.Name] {
				d, ok := a.(*ast.DeviceLit)
				if !ok {
					l.diags.Errorf(a.Pos(), "%s expects a device as its first argument", id.Name)
					return &ir.Const{V: 0}
				}
				args[i] = &ir.Device{Name: d.Name}
				continue
			}
			args[i] = l.lowerExpr(a)
		}
		b := &ir.Builtin{Name: id.Name, Args: args}
		if f.Result {
			b.Dst = l.b.NewReg(id.Name)
		}
		l.b.Emit(b)
		if b.Dst != nil {
			return b.Dst
		}
		return &ir.Const{V: 0}
	}

	l.diags.Errorf(call.Pos(), "undefined function %q", id.Name)
	return &ir.Const{V: 0}
}

var batchModes = map[string]float64{
	"Average": 0,
	"Sum":     1,
	"Minimum": 2,
	"Maximum": 3,
}

// deviceFirstArg lists built-ins whose first argument is a device port.
var deviceFirstArg = map[string]bool{
	"isSet": true, "isUnset": true, "rmap": true,
	"get": true, "put": true, "clr": true,
}

// lowerBatchCall handles the batch.read / batch.write family.
func (l *lowerer) lowerBatchCall(call *ast.CallExpr, method string, needResult bool) ir.Value {
	n := len(call.Args)
	switch method {
	case "read", "readName", "readSlot", "readNameSlot":
		var want int
		switch method {
		case "read":
			want = 3 // device, logic, mode
		case "readName":
			want = 4 // device, name, logic, mode
		case "readSlot":
			want = 4 // device, slot, logic, mode
		case "readNameSlot":
			want = 5 // device, name, slot, logic, mode
		}
		if n != want {
			l.diags.Errorf(call.Pos(), "batch.%s expects %d arguments, got %d", method, want, n)
			return &ir.Const{V: 0}
		}
		dst := l.b.NewReg("batch")
		b := &ir.Batch{Dst: dst, Device: l.lowerExpr(call.Args[0])}
		idx := 1
		if method == "readName" || method == "readNameSlot" {
			b.Name = l.lowerExpr(call.Args[idx])
			idx++
		}
		if method == "readSlot" || method == "readNameSlot" {
			b.Slot = l.lowerExpr(call.Args[idx])
			idx++
		}
		logic, ok := l.logicName(call.Args[idx])
		if !ok {
			l.diags.Errorf(call.Args[idx].Pos(), "expected a logic type name")
			return &ir.Const{V: 0}
		}
		b.Logic = logic
		idx++
		mode, ok := l.modeValue(call.Args[idx])
		if !ok {
			l.diags.Errorf(call.Args[idx].Pos(), "invalid batch mode")
			return &ir.Const{V: 0}
		}
		b.Mode = mode
		switch method {
		case "read":
			b.Kind = ir.BatchLoad
		case "readName":
			b.Kind = ir.BatchLoadName
		case "readSlot":
			b.Kind = ir.BatchLoadSlot
		case "readNameSlot":
			b.Kind = ir.BatchLoadNameSlot
		}
		l.b.Emit(b)
		return dst
	case "write", "writeName", "writeSlot":
		var want int
		switch method {
		case "write":
			want = 3 // device, logic, value
		case "writeName":
			want = 4 // device, name, logic, value
		case "writeSlot":
			want = 4 // device, slot, logic, value
		}
		if n != want {
			l.diags.Errorf(call.Pos(), "batch.%s expects %d arguments, got %d", method, want, n)
			return &ir.Const{V: 0}
		}
		b := &ir.Batch{Device: l.lowerExpr(call.Args[0])}
		idx := 1
		if method == "writeName" {
			b.Name = l.lowerExpr(call.Args[idx])
			idx++
		}
		if method == "writeSlot" {
			b.Slot = l.lowerExpr(call.Args[idx])
			idx++
		}
		logic, ok := l.logicName(call.Args[idx])
		if !ok {
			l.diags.Errorf(call.Args[idx].Pos(), "expected a logic type name")
			return &ir.Const{V: 0}
		}
		b.Logic = logic
		idx++
		b.Src = l.lowerExpr(call.Args[idx])
		switch method {
		case "write":
			b.Kind = ir.BatchStore
		case "writeName":
			b.Kind = ir.BatchStoreName
		case "writeSlot":
			b.Kind = ir.BatchStoreSlot
		}
		l.b.Emit(b)
		return &ir.Const{V: 0}
	}
	l.diags.Errorf(call.Pos(), "unknown batch function %q", method)
	return &ir.Const{V: 0}
}

// logicName extracts a logic type name from a string literal or identifier.
func (l *lowerer) logicName(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.StringLit:
		return x.Value, true
	case *ast.Ident:
		return x.Name, true
	}
	return "", false
}

// modeValue resolves a batch mode to a numeric constant when possible.
func (l *lowerer) modeValue(e ast.Expr) (ir.Value, bool) {
	switch x := e.(type) {
	case *ast.StringLit:
		v, ok := batchModes[x.Value]
		return &ir.Const{V: v}, ok
	case *ast.Ident:
		if v, ok := batchModes[x.Name]; ok {
			return &ir.Const{V: v}, true
		}
	case *ast.NumberLit:
		return &ir.Const{V: x.Value}, true
	}
	return l.lowerExpr(e), true
}

func (l *lowerer) inlineCall(id *ast.Ident, fi *sema.FuncInfo, args []ast.Expr, needResult bool) ir.Value {
	for _, n := range l.stack {
		if n == id.Name {
			l.diags.Errorf(id.Pos(), "recursion is not supported (function %q)", id.Name)
			return &ir.Const{V: 0}
		}
	}
	if len(args) != len(fi.Decl.Params) {
		l.diags.Errorf(id.Pos(), "%s expects %d arguments, got %d", id.Name, len(fi.Decl.Params), len(args))
		return &ir.Const{V: 0}
	}
	if needResult && fi.Decl.Result == "" {
		l.diags.Errorf(id.Pos(), "function %q does not return a value", id.Name)
		return &ir.Const{V: 0}
	}

	vals := make([]ir.Value, len(args))
	for i, a := range args {
		vals[i] = l.lowerExpr(a)
	}

	end := l.b.NewBlock()
	ctx := inlineCtx{end: end}
	if fi.Decl.Result != "" {
		ctx.result = l.b.NewReg(id.Name + "$ret")
	}
	l.inline = append(l.inline, ctx)
	l.stack = append(l.stack, id.Name)

	scope := map[string]ir.Value{}
	for i, p := range fi.Decl.Params {
		r := l.b.NewReg(id.Name + "$" + p.Name.Name)
		l.b.Emit(&ir.Assign{Dst: r, Src: vals[i]})
		scope[p.Name.Name] = r
	}
	l.scopes = append(l.scopes, scope)

	l.lowerStmts(fi.Decl.Body.List)
	if l.b.Cur().Term == nil {
		l.b.SetTerm(&ir.Jmp{Target: end})
	}

	l.scopes = l.scopes[:len(l.scopes)-1]
	l.stack = l.stack[:len(l.stack)-1]
	l.inline = l.inline[:len(l.inline)-1]
	l.b.SetBlock(end)

	if ctx.result != nil {
		return ctx.result
	}
	return &ir.Const{V: 0}
}

// ---------------------------------------------------------------------------
// Scopes
// ---------------------------------------------------------------------------

func (l *lowerer) pushScope() { l.scopes = append(l.scopes, map[string]ir.Value{}) }
func (l *lowerer) popScope()  { l.scopes = l.scopes[:len(l.scopes)-1] }

func (l *lowerer) bind(name string, v ir.Value) {
	l.scopes[len(l.scopes)-1][name] = v
}

func (l *lowerer) lookup(name string) (ir.Value, bool) {
	for i := len(l.scopes) - 1; i >= 0; i-- {
		if v, ok := l.scopes[i][name]; ok {
			return v, true
		}
	}
	return nil, false
}

func (l *lowerer) constEnv() map[string]float64 {
	env := map[string]float64{}
	for _, scope := range l.scopes {
		for name, v := range scope {
			if c, ok := v.(*ir.Const); ok {
				env[name] = c.V
			}
		}
	}
	return env
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// isSpecialReg reports whether a name is a special IC10 register.
func isSpecialReg(name string) bool { return name == "ra" || name == "sp" }

func deviceOf(e ast.Expr) (string, bool) {
	if d, ok := e.(*ast.DeviceLit); ok {
		return d.Name, true
	}
	return "", false
}

// isPure reports whether an expression has no observable side effects, so it is
// safe to evaluate unconditionally.
func isPure(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.NumberLit, *ast.BoolLit, *ast.SpecialLit, *ast.Ident, *ast.DeviceLit:
		return true
	case *ast.ParenExpr:
		return isPure(x.X)
	case *ast.UnaryExpr:
		return isPure(x.X)
	case *ast.BinaryExpr:
		return isPure(x.X) && isPure(x.Y)
	case *ast.TernaryExpr:
		return isPure(x.Cond) && isPure(x.Then) && isPure(x.Else)
	case *ast.SelectorExpr:
		return isPure(x.X)
	case *ast.IndexExpr:
		return isPure(x.X) && isPure(x.Index)
	case *ast.CallExpr:
		id, ok := x.Fun.(*ast.Ident)
		if !ok {
			return false
		}
		f, ok := builtin.Funcs[id.Name]
		if !ok {
			return false // user function: conservatively impure
		}
		switch f.Name {
		case "yield", "sleep", "hcf":
			return false
		}
		for _, a := range x.Args {
			if !isPure(a) {
				return false
			}
		}
		return true
	}
	return false
}

// slotOf recognises d.slot[i] and returns the device and index expression.
func slotOf(e ast.Expr) (dev string, index ast.Expr, ok bool) {
	idx, isIdx := e.(*ast.IndexExpr)
	if !isIdx {
		return "", nil, false
	}
	sel, isSel := idx.X.(*ast.SelectorExpr)
	if !isSel || sel.Sel.Name != "slot" {
		return "", nil, false
	}
	d, isDev := sel.X.(*ast.DeviceLit)
	if !isDev {
		return "", nil, false
	}
	return d.Name, idx.Index, true
}

// channelOf recognises d.channel[conn][ch] where both indices are compile-time
// constants.
func (l *lowerer) channelOf(e ast.Expr) (dev string, conn, ch float64, ok bool) {
	chIdx, isIdx := e.(*ast.IndexExpr)
	if !isIdx {
		return "", 0, 0, false
	}
	connIdx, isIdx := chIdx.X.(*ast.IndexExpr)
	if !isIdx {
		return "", 0, 0, false
	}
	sel, isSel := connIdx.X.(*ast.SelectorExpr)
	if !isSel || sel.Sel.Name != "channel" {
		return "", 0, 0, false
	}
	d, isDev := sel.X.(*ast.DeviceLit)
	if !isDev {
		return "", 0, 0, false
	}
	env := l.constEnv()
	conn, ok = sema.Eval(connIdx.Index, env)
	if !ok {
		return "", 0, 0, false
	}
	ch, ok = sema.Eval(chIdx.Index, env)
	if !ok {
		return "", 0, 0, false
	}
	return d.Name, conn, ch, true
}

func channelDev(dev string, conn float64) string {
	return dev + ":" + strconv.FormatInt(int64(conn), 10)
}

func itoa(v float64) string {
	return strconv.FormatInt(int64(v), 10)
}

func binOpOf(k token.Kind) (ir.BinOp, bool) {
	switch k {
	case token.Plus:
		return ir.Add, true
	case token.Minus:
		return ir.Sub, true
	case token.Star:
		return ir.Mul, true
	case token.Slash:
		return ir.Div, true
	case token.Percent:
		return ir.Mod, true
	case token.Amp:
		return ir.BitAnd, true
	case token.Pipe:
		return ir.BitOr, true
	case token.Caret:
		return ir.BitXor, true
	case token.Shl:
		return ir.Shl, true
	case token.Shr:
		return ir.Shr, true
	}
	return 0, false
}

func condOf(k token.Kind) (ir.Cond, bool) {
	switch k {
	case token.Eq:
		return ir.Eq, true
	case token.Ne:
		return ir.Ne, true
	case token.Lt:
		return ir.Lt, true
	case token.Le:
		return ir.Le, true
	case token.Gt:
		return ir.Gt, true
	case token.Ge:
		return ir.Ge, true
	}
	return 0, false
}

func compoundBinOp(k token.Kind) (ir.BinOp, bool) {
	switch k {
	case token.PlusAssign:
		return ir.Add, true
	case token.MinusAssign:
		return ir.Sub, true
	case token.StarAssign:
		return ir.Mul, true
	case token.SlashAssign:
		return ir.Div, true
	case token.PercentAssign:
		return ir.Mod, true
	case token.AmpAssign:
		return ir.BitAnd, true
	case token.PipeAssign:
		return ir.BitOr, true
	case token.CaretAssign:
		return ir.BitXor, true
	case token.ShlAssign:
		return ir.Shl, true
	case token.ShrAssign:
		return ir.Shr, true
	}
	return 0, false
}

func foldUnary(op token.Kind, c *ir.Const) (*ir.Const, bool) {
	if c.Special != "" {
		return nil, false
	}
	switch op {
	case token.Minus:
		return &ir.Const{V: -c.V}, true
	case token.Plus:
		return c, true
	case token.Tilde:
		return &ir.Const{V: float64(^int64(c.V))}, true
	}
	return nil, false
}

// ic10Mod matches IC10's mod instruction: the result sign follows the divisor.
func ic10Mod(x, y float64) float64 {
	r := math.Mod(x, y)
	if r != 0 && (r < 0) != (y < 0) {
		r += y
	}
	return r
}

func foldBin(op ir.BinOp, a, b *ir.Const) (*ir.Const, bool) {
	if a.Special != "" || b.Special != "" {
		return nil, false
	}
	x, y := a.V, b.V
	switch op {
	case ir.Add:
		return &ir.Const{V: x + y}, true
	case ir.Sub:
		return &ir.Const{V: x - y}, true
	case ir.Mul:
		return &ir.Const{V: x * y}, true
	case ir.Div:
		return &ir.Const{V: x / y}, true
	case ir.Mod:
		return &ir.Const{V: ic10Mod(x, y)}, true
	case ir.BitAnd:
		return &ir.Const{V: float64(int64(x) & int64(y))}, true
	case ir.BitOr:
		return &ir.Const{V: float64(int64(x) | int64(y))}, true
	case ir.BitXor:
		return &ir.Const{V: float64(int64(x) ^ int64(y))}, true
	case ir.Shl:
		return &ir.Const{V: float64(int64(x) << uint(int64(y)&63))}, true
	case ir.Shr:
		return &ir.Const{V: float64(int64(x) >> uint(int64(y)&63))}, true
	case ir.Min:
		return &ir.Const{V: math.Min(x, y)}, true
	case ir.Max:
		return &ir.Const{V: math.Max(x, y)}, true
	}
	return nil, false
}

func foldCmp(c ir.Cond, a, b *ir.Const) (*ir.Const, bool) {
	if a.Special != "" || b.Special != "" {
		return nil, false
	}
	x, y := a.V, b.V
	var r bool
	switch c {
	case ir.Eq:
		r = x == y
	case ir.Ne:
		r = x != y
	case ir.Lt:
		r = x < y
	case ir.Le:
		r = x <= y
	case ir.Gt:
		r = x > y
	case ir.Ge:
		r = x >= y
	default:
		return nil, false
	}
	if r {
		return &ir.Const{V: 1}, true
	}
	return &ir.Const{V: 0}, true
}
