// Package lower translates a type-checked AST into IR. Functions are inlined at
// this stage, so the resulting IR contains no calls.
package lower

import (
	"math"
	"os"
	"strconv"
	"strings"

	"ic10go/internal/ast"
	"ic10go/internal/builtin"
	"ic10go/internal/diag"
	"ic10go/internal/ir"
	"ic10go/internal/sema"
	"ic10go/internal/source"
	"ic10go/internal/token"
)

// Options controls lowering.
type Options struct {
	// StableInsOrder emits IC10 "ins" with the stable branch's argument order
	// (offset length field) instead of the documented (field offset length).
	StableInsOrder bool
	// DataCheck emits a runtime check that the persistent data segment is
	// installed (version sentinel matches) before running main.
	DataCheck bool
	// DataAccessStack reads the data segment through the local stack
	// (poke/peek with sp save/restore) instead of get/put db. This works on a
	// device host, where db is the device rather than the chip housing.
	DataAccessStack bool
	// Outline lists user functions to emit once as subroutines instead of
	// inlining them at every call site. See PlanOutlines.
	Outline map[string]bool
	// JumpTable lowers dense integer switches to a computed jump through a
	// table of `j` instructions. Off by default.
	JumpTable bool
	// Fast prefers runtime speed over size: it unrolls more loops.
	Fast bool
}

// emitDataCheck verifies the persistent data segment is installed: it reads the
// version sentinel and halts the chip when it does not match.
func (l *lowerer) emitDataCheck() {
	body := l.newBlock()
	halt := l.newBlock()
	r := l.emitDataRead(&ir.Const{V: float64(l.info.Sentinel)})
	c := l.b.NewReg("datachk")
	l.b.Emit(&ir.Cmp{Cond: ir.Ne, Dst: c, A: r, B: &ir.Const{V: l.info.DataVersion}})
	l.b.SetTerm(&ir.Br{Cond: ir.NonZero, A: c, Then: halt, Else: body})
	l.b.SetBlock(halt)
	l.b.SetTerm(&ir.JmpDyn{Target: &ir.Const{V: 9999}})
	l.b.SetBlock(body)
}

// emitDataRead reads one data-segment slot. In get mode it is a single
// get(db, addr); in stack mode it saves sp, points sp just past addr, peeks,
// then restores sp (device-host compatible).
func (l *lowerer) emitDataRead(addr ir.Value) ir.Value {
	r := l.b.NewReg("data")
	if !l.opts.DataAccessStack {
		l.b.Emit(&ir.Builtin{Name: "get", Dst: r, Args: []ir.Value{&ir.Device{Name: "db"}, addr}})
		return r
	}
	saved := l.b.NewReg("spsave")
	l.b.Emit(&ir.LoadSpecial{Dst: saved, Name: "sp"})
	top := l.b.NewReg("sptop")
	l.emitBin(ir.Add, top, addr, &ir.Const{V: 1})
	l.b.Emit(&ir.StoreSpecial{Name: "sp", Src: top})
	l.b.Emit(&ir.Builtin{Name: "peek", Dst: r})
	l.b.Emit(&ir.StoreSpecial{Name: "sp", Src: saved})
	return r
}

// Lower compiles the program's main function into an IR function.
func Lower(info *sema.Info, diags *diag.Bag, opts Options) *ir.Function {
	l := &lowerer{
		b:            ir.NewBuilder("main"),
		info:         info,
		diags:        diags,
		labels:       map[string]*ir.Block{},
		devices:      info.Devices,
		labelDef:     map[string]source.Pos{},
		labelUse:     map[string]source.Pos{},
		noCheck:      os.Getenv("IC10C_NO_CHECK") != "",
		opts:         opts,
		outline:      opts.Outline,
		outlined:     map[string]*outlinedFunc{},
		pureFuncs:    computePureFuncs(info),
		labeledFuncs: computeLabeledFuncs(info),
	}
	scope := map[string]ir.Value{}
	for name, v := range info.Consts {
		scope[name] = &ir.Const{V: v}
	}
	for name, raw := range info.RawConsts {
		scope[name] = &ir.Const{Raw: raw}
	}
	l.scopes = append(l.scopes, scope)

	end := l.newBlock()
	l.inline = append(l.inline, inlineCtx{end: end})

	if len(info.Data) > 0 && opts.DataCheck {
		l.emitDataCheck()
	}

	l.lowerStmts(info.Main.Body.List)
	if l.b.Cur().Term == nil {
		l.b.SetTerm(&ir.Jmp{Target: end})
	}
	l.b.SetBlock(end)
	l.b.SetTerm(&ir.Ret{})

	// Emit the bodies of outlined functions once, after the main flow.
	for _, name := range l.pending {
		l.lowerOutlined(name)
	}

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
		// Only branch-like terminators are collapsed. Call/BrCall are excluded:
		// codegen relies on their return block being laid out immediately after
		// the call, so collapsing it through an empty trampoline would move it.
		switch t := b.Term.(type) {
		case *ir.Jmp:
			t.Target = resolve(t.Target)
		case *ir.Br:
			t.Then = resolve(t.Then)
			t.Else = resolve(t.Else)
		case *ir.BrApprox:
			t.Then = resolve(t.Then)
			t.Else = resolve(t.Else)
		case *ir.BrApproxZero:
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
	label     string // name of the loop label (from a preceding `label Name:`)
}

type lowerer struct {
	b     *ir.Builder
	info  *sema.Info
	diags *diag.Bag

	scopes []map[string]ir.Value
	inline []inlineCtx
	loops  []loopCtx
	stack  []string // names of functions currently being inlined
	// pendingLoopLabel is the label of the loop about to be lowered (a
	// `label Name:` immediately preceding a for/range/switch).
	pendingLoopLabel string
	// outline marks functions emitted once as subroutines instead of inlined.
	outline  map[string]bool
	outlined map[string]*outlinedFunc
	pending  []string
	// pureFuncs marks user functions free of observable side effects.
	pureFuncs map[string]bool
	// labeledFuncs marks functions containing low-level labels (or calling
	// such a function); they must not be inlined or unrolled twice.
	labeledFuncs map[string]bool
	labels       map[string]*ir.Block
	// devices maps a const device alias to its port (d0..d5 / db).
	devices map[string]string
	// devScopes and dataScopes bind inlined function parameters that receive a
	// device port or a data table (scoped so the same function can be inlined
	// with different arguments).
	devScopes  []map[string]string
	dataScopes []map[string]*sema.DataTable
	// labelDef and labelUse track low-level label definitions and references.
	labelDef map[string]source.Pos
	labelUse map[string]source.Pos
	noCheck  bool
	opts     Options
}

// funcName is the source function currently being lowered ("" for main).
func (l *lowerer) funcName() string {
	if len(l.stack) > 0 {
		return l.stack[len(l.stack)-1]
	}
	return ""
}

// newBlock creates a block tagged with the function currently being lowered, so
// a size report can attribute emitted lines to source functions.
func (l *lowerer) newBlock() *ir.Block {
	b := l.b.NewBlock()
	b.Func = l.funcName()
	return b
}

// ---------------------------------------------------------------------------
// Statements
// ---------------------------------------------------------------------------

func (l *lowerer) ensure() {
	if l.b.Cur().Term != nil {
		l.b.SetBlock(l.newBlock())
	}
}

func (l *lowerer) lowerStmts(list []ast.Stmt) {
	for i, s := range list {
		l.ensure()
		l.noteLoopLabel(list, i)
		l.lowerStmt(s)
		l.pendingLoopLabel = ""
	}
}

func (l *lowerer) lowerBlock(b *ast.BlockStmt) {
	l.pushScope()
	l.lowerStmts(b.List)
	l.popScope()
}

// lowerStmtsCont lowers a statement list whose fall-through continuation is
// cont. Only the final statement can diverge (an if/else or a nested block).
func (l *lowerer) lowerStmtsCont(list []ast.Stmt, cont *ir.Block) {
	for i, s := range list {
		l.ensure()
		l.noteLoopLabel(list, i)
		if i == len(list)-1 {
			l.lowerStmtCont(s, cont)
			l.pendingLoopLabel = ""
			return
		}
		l.lowerStmt(s)
		l.pendingLoopLabel = ""
	}
	if l.b.Cur().Term == nil {
		l.b.SetTerm(&ir.Jmp{Target: cont})
	}
}

// noteLoopLabel records a `label Name:` that immediately precedes a loop, so
// the loop registers the label for `break Name` / `continue Name`.
func (l *lowerer) noteLoopLabel(list []ast.Stmt, i int) {
	if i == 0 {
		return
	}
	lbl, ok := list[i-1].(*ast.LabelStmt)
	if !ok {
		return
	}
	switch list[i].(type) {
	case *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt:
		l.pendingLoopLabel = lbl.Name.Name
	}
}

// lowerStmtCont lowers a statement whose fall-through target is cont. Plain
// statements simply fall through; only control-flow statements need cont.
func (l *lowerer) lowerStmtCont(s ast.Stmt, cont *ir.Block) {
	switch v := s.(type) {
	case *ast.BlockStmt:
		l.pushScope()
		l.lowerStmtsCont(v.List, cont)
		l.popScope()
	case *ast.IfStmt:
		l.lowerIfCont(v, cont)
	default:
		l.lowerStmt(s)
		if l.b.Cur().Term == nil {
			l.b.SetTerm(&ir.Jmp{Target: cont})
		}
	}
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
	case *ast.RangeStmt:
		l.lowerRange(s)
	case *ast.SwitchStmt:
		l.lowerSwitch(s)
	case *ast.BreakStmt:
		if b := l.breakTarget(s.Label, s.Pos()); b != nil {
			l.b.SetTerm(&ir.Jmp{Target: b})
		}
	case *ast.ContinueStmt:
		if b := l.continueTarget(s.Label, s.Pos()); b != nil {
			l.b.SetTerm(&ir.Jmp{Target: b})
		}
	case *ast.ReturnStmt:
		l.lowerReturn(s)
	case *ast.LabelStmt:
		l.lowerLabel(s)
	case *ast.GotoStmt:
		l.b.SetTerm(&ir.Goto{Target: l.useLabel(s.Name.Name, s.Name.Pos())})
	case *ast.CallStmt:
		target := l.useLabel(s.Name.Name, s.Name.Pos())
		ret := l.newBlock()
		l.b.SetTerm(&ir.Call{Target: target, Return: ret})
		l.b.SetBlock(ret)
	case *ast.RetStmt:
		l.b.SetTerm(&ir.JmpRA{})
	}
}

// breakTarget resolves a break to the innermost enclosing loop/switch, or to
// the one named by the optional label.
func (l *lowerer) breakTarget(label *ast.Ident, pos source.Pos) *ir.Block {
	if label == nil {
		if len(l.loops) == 0 {
			l.diags.Errorf(pos, "break outside of loop or switch")
			return nil
		}
		return l.loops[len(l.loops)-1].breakB
	}
	for i := len(l.loops) - 1; i >= 0; i-- {
		if l.loops[i].label == label.Name {
			return l.loops[i].breakB
		}
	}
	l.diags.Errorf(pos, "no enclosing loop labeled %q", label.Name)
	return nil
}

// continueTarget resolves a continue to the innermost enclosing loop, or to
// the one named by the optional label.
func (l *lowerer) continueTarget(label *ast.Ident, pos source.Pos) *ir.Block {
	if label == nil {
		if len(l.loops) == 0 || l.loops[len(l.loops)-1].continueB == nil {
			l.diags.Errorf(pos, "continue outside of loop")
			return nil
		}
		return l.loops[len(l.loops)-1].continueB
	}
	for i := len(l.loops) - 1; i >= 0; i-- {
		if l.loops[i].label == label.Name {
			if l.loops[i].continueB == nil {
				l.diags.Errorf(pos, "cannot continue a switch labeled %q", label.Name)
				return nil
			}
			return l.loops[i].continueB
		}
	}
	l.diags.Errorf(pos, "no enclosing loop labeled %q", label.Name)
	return nil
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
	b := l.newBlock()
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
	b := l.newBlock()
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
		if l.lowerInsInto(r, s.Rhs) {
			l.bind(id.Name, r)
			return
		}
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
		// ins reads its destination, so it must be lowered directly into the
		// assignment target rather than into a fresh temporary.
		if l.lowerInsInto(r, s.Rhs) {
			return
		}
		l.b.Emit(&ir.Assign{Dst: r, Src: l.lowerExpr(s.Rhs)})
		return
	}
	l.storeTo(s.Lhs, l.lowerExpr(s.Rhs))
}

// lowerInsInto lowers an ins(...) call directly into dst, whose previous value
// is the base the field is inserted into (IC10 read-modify-write). It returns
// false when e is not an ins call.
func (l *lowerer) lowerInsInto(dst *ir.Reg, e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := call.Fun.(*ast.Ident)
	if !ok || id.Name != "ins" {
		return false
	}
	if len(call.Args) != 3 {
		l.diags.Errorf(call.Pos(), "ins expects 3 arguments, got %d", len(call.Args))
		return true
	}
	field := l.lowerExpr(call.Args[0])
	off := l.lowerExpr(call.Args[1])
	length := l.lowerExpr(call.Args[2])
	args := []ir.Value{field, off, length}
	if l.opts.StableInsOrder {
		args = []ir.Value{off, length, field}
	}
	l.b.Emit(&ir.Builtin{Name: "ins", Dst: dst, Args: args})
	return true
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
		if dev, ok := l.deviceName(t.X); ok {
			l.checkLogic(t.Sel.Pos(), t.Sel.Name)
			l.b.Emit(&ir.Store{Dev: dev, Logic: t.Sel.Name, Src: val})
			return
		}
		if dev, idx, ok := l.slotOf(t.X); ok {
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
	if s.Else == nil && s.Init == nil && l.tryLowerCondCall(s) {
		return
	}
	endB := l.newBlock()
	l.lowerIfCont(s, endB)
	l.b.SetBlock(endB)
}

// tryLowerCondCall lowers `if cond { call L }` (no else) to a single
// conditional call (IC10 b<cond>al). It reports false when the shape does not
// match or the condition is a branch-only builtin.
func (l *lowerer) tryLowerCondCall(s *ast.IfStmt) bool {
	if len(s.Then.List) != 1 {
		return false
	}
	call, ok := s.Then.List[0].(*ast.CallStmt)
	if !ok {
		return false
	}
	if !condCallSupported(s.Cond) {
		return false
	}
	endB := l.newBlock()
	target := l.useLabel(call.Name.Name, call.Name.Pos())
	cond, a, b := l.lowerCond(s.Cond)
	l.b.SetTerm(&ir.BrCall{Cond: cond, A: a, B: b, Target: target, Return: endB})
	l.b.SetBlock(endB)
	return true
}

// condCallSupported reports whether a condition can be lowered with lowerCond
// (i.e. it is not one of the branch-only validity builtins).
func condCallSupported(e ast.Expr) bool {
	inner := e
	if un, ok := e.(*ast.UnaryExpr); ok && un.Op == token.Not {
		inner = un.X
	}
	if call, ok := inner.(*ast.CallExpr); ok {
		if id, ok := call.Fun.(*ast.Ident); ok {
			if id.Name == "isLoadValid" || id.Name == "isStoreValid" {
				return false
			}
		}
	}
	return true
}

// lowerIfCont lowers an if/else-if chain so that every branch converges on the
// shared endB rather than each nested if allocating its own join. Flattening the
// chain lets identical branch tails (such as a call inlined into several
// branches) share one terminator, which tail merging can then factor.
func (l *lowerer) lowerIfCont(s *ast.IfStmt, endB *ir.Block) {
	if s.Init != nil {
		l.pushScope()
		defer l.popScope()
		l.lowerStmt(s.Init)
	}
	thenB := l.newBlock()
	elseB := l.newBlock()
	l.branchCond(s.Cond, thenB, elseB)

	l.b.SetBlock(thenB)
	l.pushScope()
	l.lowerStmtsCont(s.Then.List, endB)
	l.popScope()

	l.b.SetBlock(elseB)
	if s.Else == nil {
		l.b.SetTerm(&ir.Jmp{Target: endB})
		return
	}
	if nested, ok := s.Else.(*ast.IfStmt); ok {
		l.lowerIfCont(nested, endB)
		return
	}
	l.lowerStmtCont(s.Else, endB)
}

// tryUnrollFor unrolls a small constant `for i := lo; i < hi; i++` loop whose
// body neither modifies i nor jumps out. The loop variable becomes a constant,
// so table reads `T[i]` fold to a single get with a constant address. Returns
// false when the loop is not a safe candidate.
func (l *lowerer) tryUnrollFor(s *ast.ForStmt) bool {
	name, lo, ok := l.loopInit(s.Init)
	if !ok {
		return false
	}
	hi, ok := l.loopBound(s.Cond, name)
	maxTrip, maxBody := 4, 2
	if l.opts.Fast {
		maxTrip, maxBody = 8, 4
	}
	if !ok || hi <= lo || hi-lo > maxTrip {
		return false
	}
	id, ok := s.Post.(*ast.IncDecStmt)
	if !ok || id.Op != token.PlusPlus {
		return false
	}
	if pid, ok := id.X.(*ast.Ident); !ok || pid.Name != name {
		return false
	}
	if unrollUnsafe(s.Body, name) || countStatements(s.Body) > maxBody || l.bodyCallsLabeled(s.Body) {
		return false
	}
	if !l.opts.Fast && l.bodyCallsUserFunc(s.Body) {
		return false
	}
	l.pushScope()
	for k := lo; k < hi; k++ {
		l.bind(name, &ir.Const{V: float64(k)})
		l.lowerBlock(s.Body)
	}
	l.popScope()
	return true
}

// bodyCallsLabeled reports whether a body calls a function containing a label.
// Duplicating such a call would define the label twice.
func (l *lowerer) bodyCallsLabeled(body *ast.BlockStmt) bool {
	found := false
	forEachCall(body, func(c *ast.CallExpr) {
		if id, ok := c.Fun.(*ast.Ident); ok && l.labeledFuncs[id.Name] {
			found = true
		}
	})
	return found
}

// bodyCallsUserFunc reports whether a body calls a user function. Unrolling
// such a loop would duplicate the (inlined) body, which usually costs more
// lines than the loop it replaces.
func (l *lowerer) bodyCallsUserFunc(body *ast.BlockStmt) bool {
	found := false
	forEachCall(body, func(c *ast.CallExpr) {
		if id, ok := c.Fun.(*ast.Ident); ok {
			if _, isFunc := l.info.Funcs[id.Name]; isFunc {
				found = true
			}
		}
	})
	return found
}

// loopInit extracts the loop variable and its constant start from a for-init.
func (l *lowerer) loopInit(init ast.Stmt) (string, int, bool) {
	var name string
	var rhs ast.Expr
	switch v := init.(type) {
	case *ast.AssignStmt:
		if v.Op != token.Define {
			return "", 0, false
		}
		id, ok := v.Lhs.(*ast.Ident)
		if !ok {
			return "", 0, false
		}
		name, rhs = id.Name, v.Rhs
	case *ast.DeclStmt:
		vd, ok := v.Decl.(*ast.VarDecl)
		if !ok || vd.Value == nil {
			return "", 0, false
		}
		name, rhs = vd.Name.Name, vd.Value
	default:
		return "", 0, false
	}
	f, ok := sema.Eval(rhs, l.info.Consts)
	if !ok || f != math.Trunc(f) {
		return "", 0, false
	}
	return name, int(f), true
}

// loopBound extracts the exclusive upper bound from `i < hi` / `i <= hi`.
func (l *lowerer) loopBound(cond ast.Expr, name string) (int, bool) {
	be, ok := cond.(*ast.BinaryExpr)
	if !ok || (be.Op != token.Lt && be.Op != token.Le) {
		return 0, false
	}
	id, ok := be.X.(*ast.Ident)
	if !ok || id.Name != name {
		return 0, false
	}
	f, ok := sema.Eval(be.Y, l.info.Consts)
	if !ok || f != math.Trunc(f) {
		return 0, false
	}
	if be.Op == token.Le {
		return int(f) + 1, true
	}
	return int(f), true
}

// unrollUnsafe reports whether a loop body prevents unrolling: it assigns to
// the loop variable, or contains control flow that could escape the body.
func unrollUnsafe(body *ast.BlockStmt, name string) bool {
	unsafe := false
	walkStmt(body, func(s ast.Stmt) {
		switch v := s.(type) {
		case *ast.AssignStmt:
			if id, ok := v.Lhs.(*ast.Ident); ok && id.Name == name {
				unsafe = true
			}
		case *ast.IncDecStmt:
			if id, ok := v.X.(*ast.Ident); ok && id.Name == name {
				unsafe = true
			}
		case *ast.BreakStmt, *ast.ContinueStmt, *ast.GotoStmt, *ast.CallStmt,
			*ast.RetStmt, *ast.ReturnStmt, *ast.ForStmt, *ast.RangeStmt:
			unsafe = true
		}
	})
	return unsafe
}

func (l *lowerer) lowerFor(s *ast.ForStmt) {
	label := l.pendingLoopLabel
	l.pendingLoopLabel = ""
	if l.tryUnrollFor(s) {
		return
	}
	l.pushScope()
	if s.Init != nil {
		l.lowerStmt(s.Init)
	}
	condB := l.newBlock()
	bodyB := l.newBlock()
	postB := l.newBlock()
	endB := l.newBlock()

	l.b.SetTerm(&ir.Jmp{Target: condB})

	l.b.SetBlock(condB)
	if s.Cond != nil {
		l.branchCond(s.Cond, bodyB, endB)
	} else {
		l.b.SetTerm(&ir.Jmp{Target: bodyB})
	}

	l.b.SetBlock(bodyB)
	l.loops = append(l.loops, loopCtx{breakB: endB, continueB: postB, label: label})
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

// lowerRange lowers `for key [, value] := range X`. X may be a `data` table
// (value binds Table[key]) or a runtime count (0..X-1). It desugars to an
// equivalent three-part for loop so the existing optimizer (small-loop
// unrolling, Table[i] constant folding) applies unchanged.
func (l *lowerer) lowerRange(s *ast.RangeStmt) {
	base := ast.NodeBase{Pos_: s.Pos()}
	bound := s.X
	var valueExpr ast.Expr
	if t, ok := l.dataTable(s.X); ok {
		bound = &ast.NumberLit{NodeBase: base, Value: float64(len(t.Values))}
		if s.Value != nil {
			valueExpr = &ast.IndexExpr{
				NodeBase: base,
				X:        s.X,
				Index:    &ast.Ident{NodeBase: ast.NodeBase{Pos_: s.Key.Pos()}, Name: s.Key.Name},
			}
		}
	}
	body := s.Body
	if valueExpr != nil {
		valueAssign := &ast.AssignStmt{
			NodeBase: ast.NodeBase{Pos_: s.Value.Pos()},
			Lhs:      s.Value,
			Op:       token.Define,
			Rhs:      valueExpr,
		}
		body = &ast.BlockStmt{NodeBase: base, List: append([]ast.Stmt{valueAssign}, s.Body.List...)}
	}
	l.lowerFor(&ast.ForStmt{
		NodeBase: base,
		Init: &ast.AssignStmt{
			NodeBase: base,
			Lhs:      s.Key,
			Op:       token.Define,
			Rhs:      &ast.NumberLit{NodeBase: base, Value: 0},
		},
		Cond: &ast.BinaryExpr{NodeBase: base, Op: token.Lt, X: s.Key, Y: bound},
		Post: &ast.IncDecStmt{NodeBase: base, X: s.Key, Op: token.PlusPlus},
		Body: body,
	})
}

func (l *lowerer) lowerSwitch(s *ast.SwitchStmt) {
	label := l.pendingLoopLabel
	l.pendingLoopLabel = ""
	if s.Init != nil {
		l.pushScope()
		defer l.popScope()
		l.lowerStmt(s.Init)
	}
	if ts, ok := l.info.TableSwitches[s]; ok {
		l.lowerTableSwitch(s, ts, label)
		return
	}
	if l.opts.JumpTable && l.lowerJumpTable(s, label) {
		return
	}
	endB := l.newBlock()
	l.loops = append(l.loops, loopCtx{breakB: endB, label: label})
	defer func() { l.loops = l.loops[:len(l.loops)-1] }()

	var tag ir.Value
	if s.Tag != nil {
		tag = l.lowerExpr(s.Tag)
	}

	bodyBlocks := make([]*ir.Block, len(s.Cases))
	for i := range s.Cases {
		bodyBlocks[i] = l.newBlock()
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
		for _, ce := range c.Exprs {
			l.ensure()
			next := l.newBlock()
			if s.Tag != nil {
				if re, ok := ce.(*ast.RangeExpr); ok {
					lo := l.lowerExpr(re.Lo)
					hi := l.lowerExpr(re.Hi)
					lt := l.b.NewReg("swlt")
					l.b.Emit(&ir.Cmp{Cond: ir.Lt, Dst: lt, A: tag, B: lo})
					mid := l.newBlock()
					l.b.SetTerm(&ir.Br{Cond: ir.NonZero, A: lt, Then: next, Else: mid})
					l.b.SetBlock(mid)
					gt := l.b.NewReg("swgt")
					l.b.Emit(&ir.Cmp{Cond: ir.Gt, Dst: gt, A: tag, B: hi})
					l.b.SetTerm(&ir.Br{Cond: ir.NonZero, A: gt, Then: next, Else: bodyBlocks[i]})
				} else {
					v := l.lowerExpr(ce)
					cmp := l.b.NewReg("swcmp")
					l.b.Emit(&ir.Cmp{Cond: ir.Eq, Dst: cmp, A: tag, B: v})
					l.b.SetTerm(&ir.Br{Cond: ir.NonZero, A: cmp, Then: bodyBlocks[i], Else: next})
				}
			} else {
				if _, ok := ce.(*ast.RangeExpr); ok {
					l.diags.Errorf(ce.Pos(), "range case requires a switch tag")
					l.b.SetBlock(next)
					continue
				}
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

// jumpTableMin is the smallest dense switch worth a jump table.
const jumpTableMin = 8

// lowerJumpTable lowers a dense integer switch to a computed jump through a
// table of `j` instructions. It returns false when the switch is not a
// candidate.
func (l *lowerer) lowerJumpTable(s *ast.SwitchStmt, label string) bool {
	if s.Tag == nil {
		return false
	}
	type entry struct {
		val  int
		body []ast.Stmt
	}
	var entries []entry
	for _, c := range s.Cases {
		if c.Default {
			continue
		}
		if len(c.Exprs) != 1 {
			return false
		}
		v, ok := sema.Eval(c.Exprs[0], l.info.Consts)
		if !ok || v != math.Trunc(v) {
			return false
		}
		entries = append(entries, entry{int(v), c.Body})
	}
	if len(entries) < jumpTableMin {
		return false
	}
	lo, hi := entries[0].val, entries[0].val
	for _, e := range entries {
		if e.val < lo {
			lo = e.val
		}
		if e.val > hi {
			hi = e.val
		}
	}
	if hi-lo+1 != len(entries) {
		return false // must be dense
	}
	// Only simple case bodies: a single assignment or call, with no control
	// flow. More complex bodies create blocks that the jump-table layout does
	// not order safely.
	for _, e := range entries {
		if len(e.body) != 1 {
			return false
		}
		switch b := e.body[0].(type) {
		case *ast.AssignStmt:
		case *ast.ExprStmt:
			if _, isCall := b.X.(*ast.CallExpr); !isCall {
				return false
			}
		default:
			return false
		}
	}

	endB := l.newBlock()
	l.loops = append(l.loops, loopCtx{breakB: endB, label: label})
	defer func() { l.loops = l.loops[:len(l.loops)-1] }()

	tag := l.lowerExpr(s.Tag)
	idx := l.b.NewReg("jidx")
	l.emitBin(ir.Sub, idx, tag, &ir.Const{V: float64(lo)})
	lt := l.b.NewReg("jlt")
	l.b.Emit(&ir.Cmp{Cond: ir.Lt, Dst: lt, A: idx, B: &ir.Const{V: 0}})
	gt := l.b.NewReg("jgt")
	l.b.Emit(&ir.Cmp{Cond: ir.Gt, Dst: gt, A: idx, B: &ir.Const{V: float64(hi - lo)}})
	bad := l.b.NewReg("jbad")
	l.emitBin(ir.BitOr, bad, lt, gt)
	defaultB := l.newBlock()
	tableB := l.newBlock()
	l.b.SetTerm(&ir.Br{Cond: ir.NonZero, A: bad, Then: defaultB, Else: tableB})

	l.b.SetBlock(defaultB)
	for _, c := range s.Cases {
		if c.Default {
			l.lowerStmts(c.Body)
			break
		}
	}
	if l.b.Cur().Term == nil {
		l.b.SetTerm(&ir.Jmp{Target: endB})
	}

	l.b.SetBlock(tableB)
	caseBlocks := make([]*ir.Block, hi-lo+1)
	for i := range caseBlocks {
		caseBlocks[i] = l.newBlock()
	}
	l.b.SetTerm(&ir.JmpDyn{Target: idx, Table: caseBlocks})

	for _, e := range entries {
		l.b.SetBlock(caseBlocks[e.val-lo])
		l.lowerStmts(e.body)
		if l.b.Cur().Term == nil {
			l.b.SetTerm(&ir.Jmp{Target: endB})
		}
	}
	l.b.SetBlock(endB)
	return true
}

// lowerTableSwitch lowers a `switch tag table` into a bounds check plus one
// table read per assignment target.
func (l *lowerer) lowerTableSwitch(s *ast.SwitchStmt, ts *sema.TableSwitch, label string) {
	endB := l.newBlock()
	l.loops = append(l.loops, loopCtx{breakB: endB, label: label})
	defer func() { l.loops = l.loops[:len(l.loops)-1] }()

	inB := l.newBlock()
	outB := l.newBlock()

	tag := l.lowerExpr(s.Tag)
	lt := l.b.NewReg("swlt")
	l.b.Emit(&ir.Cmp{Cond: ir.Lt, Dst: lt, A: tag, B: &ir.Const{V: float64(ts.Low)}})
	gt := l.b.NewReg("swgt")
	l.b.Emit(&ir.Cmp{Cond: ir.Gt, Dst: gt, A: tag, B: &ir.Const{V: float64(ts.High)}})
	bad := l.b.NewReg("swbad")
	l.emitBin(ir.BitOr, bad, lt, gt)
	l.b.SetTerm(&ir.Br{Cond: ir.NonZero, A: bad, Then: outB, Else: inB})

	// Out of range: run the default body (if any), then skip.
	l.b.SetBlock(outB)
	for _, c := range s.Cases {
		if c.Default {
			l.lowerStmts(c.Body)
			break
		}
	}
	if l.b.Cur().Term == nil {
		l.b.SetTerm(&ir.Jmp{Target: endB})
	}

	// In range: read each generated table.
	l.b.SetBlock(inB)
	idx := l.b.NewReg("swidx")
	l.emitBin(ir.Sub, idx, tag, &ir.Const{V: float64(ts.Low)})
	for _, tt := range ts.Targets {
		addr := l.b.NewReg("swaddr")
		l.emitBin(ir.Add, addr, &ir.Const{V: float64(tt.Table.Base)}, idx)
		l.storeTo(tt.Assign.Lhs, l.emitDataRead(addr))
	}
	l.b.SetTerm(&ir.Jmp{Target: endB})

	l.b.SetBlock(endB)
}

// lowerExpr lowers an expression into an IR value.
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
		if t, ok := l.dataTable(e.X); ok {
			idx := l.lowerExpr(e.Index)
			addr := l.b.NewReg("dataaddr")
			l.emitBin(ir.Add, addr, &ir.Const{V: float64(t.Base)}, idx)
			return l.emitDataRead(addr)
		}
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
		if id, ok := call.Fun.(*ast.Ident); ok {
			switch id.Name {
			case "isLoadValid", "isStoreValid":
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
			case "approx", "notApprox":
				if len(call.Args) == 3 {
					l.b.SetTerm(&ir.BrApprox{
						A:      l.lowerExpr(call.Args[0]),
						B:      l.lowerExpr(call.Args[1]),
						Tol:    l.lowerExpr(call.Args[2]),
						Negate: neg != (id.Name == "notApprox"),
						Then:   thenB,
						Else:   elseB,
					})
					return
				}
			case "approxZero", "notApproxZero":
				if len(call.Args) == 2 {
					l.b.SetTerm(&ir.BrApproxZero{
						A:      l.lowerExpr(call.Args[0]),
						Tol:    l.lowerExpr(call.Args[1]),
						Negate: neg != (id.Name == "notApproxZero"),
						Then:   thenB,
						Else:   elseB,
					})
					return
				}
			}
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
	dev, ok := l.deviceName(call.Args[0])
	if !ok {
		l.diags.Errorf(call.Args[0].Pos(), "expected a device as the first argument")
		return "", "", false
	}
	logic, ok := call.Args[1].(*ast.StringLit)
	if !ok {
		l.diags.Errorf(call.Args[1].Pos(), "expected a logic type string as the second argument")
		return "", "", false
	}
	return dev, logic.Value, true
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
	if l.isPure(e.X) && l.isPure(e.Y) {
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
	endB := l.newBlock()
	contB := l.newBlock()
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
	thenB := l.newBlock()
	elseB := l.newBlock()
	endB := l.newBlock()
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
	if dev, ok := l.deviceName(e.X); ok {
		l.checkLogic(e.Sel.Pos(), e.Sel.Name)
		r := l.b.NewReg(e.Sel.Name)
		l.b.Emit(&ir.Load{Dst: r, Dev: dev, Logic: e.Sel.Name})
		return r
	}
	if dev, idx, ok := l.slotOf(e.X); ok {
		l.checkSlot(e.Sel.Pos(), e.Sel.Name)
		r := l.b.NewReg(e.Sel.Name)
		l.b.Emit(&ir.LoadSlot{Dst: r, Dev: dev, Index: l.lowerExpr(idx), Logic: e.Sel.Name})
		return r
	}
	// Game enum constants such as SorterInstruction.FilterPrefabHashEquals.
	if id, ok := e.X.(*ast.Ident); ok {
		if v, ok := builtin.EnumConstants[id.Name+"."+e.Sel.Name]; ok {
			return &ir.Const{V: v}
		}
		// LogicType members are emitted verbatim; the game assembler resolves
		// them, so the compiler does not need their numeric values.
		if id.Name == "LogicType" {
			return &ir.Const{Raw: "LogicType." + e.Sel.Name}
		}
	}
	l.diags.Errorf(e.Pos(), "unsupported device access")
	return &ir.Const{V: 0}
}

// checkLogic warns about a logic type that is not in the built-in table.
func (l *lowerer) checkLogic(pos source.Pos, name string) {
	if l.noCheck || builtin.LogicTypes[name] {
		return
	}
	l.diags.WarnfCode("unknown-logic-type", pos, "unknown logic type %q", name)
}

// checkSlot warns about a slot type that is not in the built-in table.
func (l *lowerer) checkSlot(pos source.Pos, name string) {
	if l.noCheck || builtin.SlotTypes[name] {
		return
	}
	l.diags.WarnfCode("unknown-slot-type", pos, "unknown slot type %q", name)
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
		return &ir.Const{V: float64(int32(builtin.Hash(s.Value)))}
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
		dev, ok := l.deviceName(call.Args[0])
		if !ok {
			l.diags.Errorf(call.Args[0].Pos(), "read expects a device as its first argument")
			return &ir.Const{V: 0}
		}
		logic := l.dynamicLogic(call.Args[1])
		r := l.b.NewReg("read")
		l.b.Emit(&ir.LoadDyn{Dst: r, Dev: dev, Logic: logic})
		return r
	}
	if id.Name == "write" {
		if len(call.Args) != 3 {
			l.diags.Errorf(call.Pos(), "write expects a device, a logic type and a value")
			return &ir.Const{V: 0}
		}
		dev, ok := l.deviceName(call.Args[0])
		if !ok {
			l.diags.Errorf(call.Args[0].Pos(), "write expects a device as its first argument")
			return &ir.Const{V: 0}
		}
		logic := l.dynamicLogic(call.Args[1])
		src := l.lowerExpr(call.Args[2])
		l.b.Emit(&ir.StoreDyn{Dev: dev, Logic: logic, Src: src})
		return &ir.Const{V: 0}
	}

	// readDev(reg, lt) / writeDev(reg, lt, v) select the device port from a
	// register at runtime (IC10 "l r? drN rM" / "s drN rM r?").
	if id.Name == "readDev" {
		if len(call.Args) != 2 {
			l.diags.Errorf(call.Pos(), "readDev expects a register and a logic type")
			return &ir.Const{V: 0}
		}
		ptr := l.lowerExpr(call.Args[0])
		logic := l.dynamicLogic(call.Args[1])
		r := l.b.NewReg("read")
		l.b.Emit(&ir.LoadDyn{Dst: r, DevPtr: ptr, Logic: logic})
		return r
	}
	if id.Name == "writeDev" {
		if len(call.Args) != 3 {
			l.diags.Errorf(call.Pos(), "writeDev expects a register, a logic type and a value")
			return &ir.Const{V: 0}
		}
		ptr := l.lowerExpr(call.Args[0])
		logic := l.dynamicLogic(call.Args[1])
		src := l.lowerExpr(call.Args[2])
		l.b.Emit(&ir.StoreDyn{DevPtr: ptr, Logic: logic, Src: src})
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
		if l.outline[id.Name] && !l.hasDeviceOrDataArg(call.Args) {
			return l.outlineCall(id, fi, call.Args, needResult)
		}
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
				d, ok := l.deviceName(a)
				if !ok {
					l.diags.Errorf(a.Pos(), "%s expects a device as its first argument", id.Name)
					return &ir.Const{V: 0}
				}
				args[i] = &ir.Device{Name: d}
				continue
			}
			args[i] = l.lowerExpr(a)
		}
		// The stable game branch emits "ins" as offset-length-field.
		if id.Name == "ins" && l.opts.StableInsOrder && len(args) == 3 {
			args = []ir.Value{args[1], args[2], args[0]}
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
	"readReagent": true,
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
	devArgs := make([]string, len(args))
	dataArgs := make([]*sema.DataTable, len(args))
	isDev := make([]bool, len(args))
	isData := make([]bool, len(args))
	for i, a := range args {
		if dev, ok := l.deviceName(a); ok {
			devArgs[i], isDev[i] = dev, true
			vals[i] = &ir.Const{V: 0}
			continue
		}
		if t, ok := l.dataTable(a); ok {
			dataArgs[i], isData[i] = t, true
			vals[i] = &ir.Const{V: 0}
			continue
		}
		vals[i] = l.lowerExpr(a)
	}

	end := l.newBlock()
	ctx := inlineCtx{end: end}
	if fi.Decl.Result != "" {
		ctx.result = l.b.NewReg(id.Name + "$ret")
	}
	l.inline = append(l.inline, ctx)
	l.stack = append(l.stack, id.Name)

	scope := map[string]ir.Value{}
	for i, p := range fi.Decl.Params {
		if isDev[i] || isData[i] {
			continue
		}
		r := l.b.NewReg(id.Name + "$" + p.Name.Name)
		l.b.Emit(&ir.Assign{Dst: r, Src: vals[i]})
		scope[p.Name.Name] = r
	}
	l.scopes = append(l.scopes, scope)
	l.pushDevScope()
	l.pushDataScope()
	for i, p := range fi.Decl.Params {
		if isDev[i] {
			l.bindDev(p.Name.Name, devArgs[i])
		}
		if isData[i] {
			l.bindData(p.Name.Name, dataArgs[i])
		}
	}

	l.lowerStmtsCont(fi.Decl.Body.List, end)

	l.popDataScope()
	l.popDevScope()
	l.scopes = l.scopes[:len(l.scopes)-1]
	l.stack = l.stack[:len(l.stack)-1]
	l.inline = l.inline[:len(l.inline)-1]
	l.b.SetBlock(end)

	if ctx.result != nil {
		return ctx.result
	}
	return &ir.Const{V: 0}
}

// outlineCall emits a call to an outlined function: arguments are moved into the
// function's parameter registers, then a jal is emitted. The body itself is
// emitted once later, by lowerOutlined.
func (l *lowerer) outlineCall(id *ast.Ident, fi *sema.FuncInfo, args []ast.Expr, needResult bool) ir.Value {
	// Specialize constant-argument calls: inlining lets the optimizer fold the
	// body, so outline only the calls that cannot be folded.
	if l.argsConstant(args) {
		return l.inlineCall(id, fi, args, needResult)
	}
	if len(args) != len(fi.Decl.Params) {
		l.diags.Errorf(id.Pos(), "%s expects %d arguments, got %d", id.Name, len(fi.Decl.Params), len(args))
		return &ir.Const{V: 0}
	}
	if needResult && fi.Decl.Result == "" {
		l.diags.Errorf(id.Pos(), "function %q does not return a value", id.Name)
		return &ir.Const{V: 0}
	}

	of := l.outlined[id.Name]
	if of == nil {
		of = &outlinedFunc{fi: fi, entry: l.newBlock(), epilogue: l.newBlock()}
		for _, p := range fi.Decl.Params {
			of.params = append(of.params, l.b.NewReg(id.Name+"$"+p.Name.Name))
		}
		if fi.Decl.Result != "" {
			of.result = l.b.NewReg(id.Name + "$ret")
		}
		l.outlined[id.Name] = of
		l.pending = append(l.pending, id.Name)
	}

	// Evaluate every argument before writing any parameter register: an
	// argument may itself call this function and clobber the parameters.
	vals := make([]ir.Value, len(args))
	for i, a := range args {
		vals[i] = l.lowerExpr(a)
	}
	for i := range of.params {
		l.b.Emit(&ir.Assign{Dst: of.params[i], Src: vals[i]})
	}
	ret := l.newBlock()
	l.b.SetTerm(&ir.Call{Target: of.entry, Return: ret})
	l.b.SetBlock(ret)
	if needResult {
		// Copy the result out of the function's fixed result register: a later
		// call would otherwise clobber a result the caller still needs.
		tmp := l.b.NewReg(id.Name + "$res")
		l.b.Emit(&ir.Assign{Dst: tmp, Src: of.result})
		return tmp
	}
	return &ir.Const{V: 0}
}

// argsConstant reports whether every argument is a compile-time constant, so an
// inlined call would fold.
func (l *lowerer) argsConstant(args []ast.Expr) bool {
	for _, a := range args {
		if _, ok := sema.Eval(a, l.info.Consts); !ok {
			return false
		}
	}
	return true
}

// dynamicLogic lowers a runtime logic-type operand. IC10's dynamic form
// (l r? d? rN) requires a register, so a constant id is materialised into one.
func (l *lowerer) dynamicLogic(e ast.Expr) ir.Value {
	v := l.lowerExpr(e)
	if _, isConst := v.(*ir.Const); isConst {
		r := l.b.NewReg("lt")
		l.b.Emit(&ir.Assign{Dst: r, Src: v})
		return r
	}
	return v
}

// lowerOutlined emits the body of an outlined function once. Only leaf
// functions are outlined, so a single return-address register is safe.
func (l *lowerer) lowerOutlined(name string) {
	of := l.outlined[name]
	of.entry.Func = name
	of.epilogue.Func = name
	l.b.SetBlock(of.entry)

	scope := map[string]ir.Value{}
	for i, p := range of.fi.Decl.Params {
		scope[p.Name.Name] = of.params[i]
	}
	l.scopes = append(l.scopes, scope)
	l.inline = append(l.inline, inlineCtx{end: of.epilogue, result: of.result})
	l.stack = append(l.stack, name)

	l.lowerStmts(of.fi.Decl.Body.List)
	if l.b.Cur().Term == nil {
		l.b.SetTerm(&ir.Jmp{Target: of.epilogue})
	}

	l.stack = l.stack[:len(l.stack)-1]
	l.inline = l.inline[:len(l.inline)-1]
	l.scopes = l.scopes[:len(l.scopes)-1]
	l.b.SetBlock(of.epilogue)
	l.b.SetTerm(&ir.JmpRA{})
}

// ---------------------------------------------------------------------------
// Scopes
// ---------------------------------------------------------------------------

func (l *lowerer) pushScope() { l.scopes = append(l.scopes, map[string]ir.Value{}) }
func (l *lowerer) popScope()  { l.scopes = l.scopes[:len(l.scopes)-1] }

// Device and data-table parameter scopes, pushed around inlined function bodies.
func (l *lowerer) pushDevScope() { l.devScopes = append(l.devScopes, map[string]string{}) }
func (l *lowerer) popDevScope()  { l.devScopes = l.devScopes[:len(l.devScopes)-1] }

func (l *lowerer) bindDev(name, dev string) { l.devScopes[len(l.devScopes)-1][name] = dev }

func (l *lowerer) lookupDev(name string) (string, bool) {
	for i := len(l.devScopes) - 1; i >= 0; i-- {
		if d, ok := l.devScopes[i][name]; ok {
			return d, true
		}
	}
	return "", false
}

func (l *lowerer) pushDataScope() {
	l.dataScopes = append(l.dataScopes, map[string]*sema.DataTable{})
}
func (l *lowerer) popDataScope() { l.dataScopes = l.dataScopes[:len(l.dataScopes)-1] }

func (l *lowerer) bindData(name string, t *sema.DataTable) {
	l.dataScopes[len(l.dataScopes)-1][name] = t
}

func (l *lowerer) lookupData(name string) (*sema.DataTable, bool) {
	for i := len(l.dataScopes) - 1; i >= 0; i-- {
		if t, ok := l.dataScopes[i][name]; ok {
			return t, true
		}
	}
	return nil, false
}

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

// deviceName resolves an expression to a device port: a device literal
// (d0..d5 / db) or a const device alias.
func (l *lowerer) deviceName(e ast.Expr) (string, bool) {
	if d, ok := e.(*ast.DeviceLit); ok {
		return d.Name, true
	}
	if id, ok := e.(*ast.Ident); ok {
		if dev, ok := l.lookupDev(id.Name); ok {
			return dev, true
		}
		if dev, ok := l.devices[id.Name]; ok {
			return dev, true
		}
	}
	return "", false
}

// hasDeviceOrDataArg reports whether any call argument is a device port or a
// data table. Such arguments cannot be passed in registers, so the call must be
// inlined even when the function is otherwise outlined.
func (l *lowerer) hasDeviceOrDataArg(args []ast.Expr) bool {
	for _, a := range args {
		if _, ok := l.deviceName(a); ok {
			return true
		}
		if _, ok := l.dataTable(a); ok {
			return true
		}
	}
	return false
}

// isPure reports whether an expression has no observable side effects, so it is
// safe to evaluate unconditionally.
func (l *lowerer) isPure(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.NumberLit, *ast.BoolLit, *ast.SpecialLit, *ast.Ident, *ast.DeviceLit:
		return true
	case *ast.ParenExpr:
		return l.isPure(x.X)
	case *ast.UnaryExpr:
		return l.isPure(x.X)
	case *ast.BinaryExpr:
		return l.isPure(x.X) && l.isPure(x.Y)
	case *ast.TernaryExpr:
		return l.isPure(x.Cond) && l.isPure(x.Then) && l.isPure(x.Else)
	case *ast.SelectorExpr:
		return l.isPure(x.X)
	case *ast.IndexExpr:
		return l.isPure(x.X) && l.isPure(x.Index)
	case *ast.CallExpr:
		id, ok := x.Fun.(*ast.Ident)
		if !ok {
			// batch.read* are pure reads; batch.write* are not.
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
				return !strings.HasPrefix(sel.Sel.Name, "write")
			}
			return false
		}
		if _, isFunc := l.info.Funcs[id.Name]; isFunc {
			return l.pureFuncs[id.Name]
		}
		f, ok := builtin.Funcs[id.Name]
		if !ok {
			return false
		}
		switch f.Name {
		case "yield", "sleep", "hcf":
			return false
		}
		for _, a := range x.Args {
			if !l.isPure(a) {
				return false
			}
		}
		return true
	}
	return false
}

// slotOf recognises d.slot[i] and returns the device and index expression.
func (l *lowerer) slotOf(e ast.Expr) (dev string, index ast.Expr, ok bool) {
	idx, isIdx := e.(*ast.IndexExpr)
	if !isIdx {
		return "", nil, false
	}
	sel, isSel := idx.X.(*ast.SelectorExpr)
	if !isSel || sel.Sel.Name != "slot" {
		return "", nil, false
	}
	d, isDev := l.deviceName(sel.X)
	if !isDev {
		return "", nil, false
	}
	return d, idx.Index, true
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
	d, isDev := l.deviceName(sel.X)
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
	return d, conn, ch, true
}

// dataTable reports whether e names a top-level `data` table (not shadowed by a
// local variable).
func (l *lowerer) dataTable(e ast.Expr) (*sema.DataTable, bool) {
	id, ok := e.(*ast.Ident)
	if !ok {
		return nil, false
	}
	if t, ok := l.lookupData(id.Name); ok {
		return t, true
	}
	if _, bound := l.lookup(id.Name); bound {
		return nil, false
	}
	t, ok := l.info.DataIndex[id.Name]
	return t, ok
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
