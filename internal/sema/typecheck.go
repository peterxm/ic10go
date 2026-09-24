package sema

import (
	"strings"

	"ic10go/internal/ast"
	"ic10go/internal/builtin"
	"ic10go/internal/diag"
	"ic10go/internal/source"
	"ic10go/internal/token"
)

// Type is the static type of an expression. IC10 has a single runtime type
// (double), so these exist only to catch obvious mistakes early and to power
// editor features such as hover. Any is the unknown/uncatchable type and is
// compatible with everything.
type Type int

const (
	Any Type = iota
	Num
	Bool
	Device
	Data
	Str
	Void
)

func (t Type) String() string {
	switch t {
	case Num:
		return "num"
	case Bool:
		return "bool"
	case Device:
		return "device"
	case Data:
		return "data"
	case Str:
		return "str"
	case Void:
		return "void"
	default:
		return "any"
	}
}

// validTypeName reports whether a declared parameter or result type is known.
func validTypeName(s string) bool {
	switch s {
	case "num", "bool", "device", "void", "str":
		return true
	}
	return false
}

func typeOfName(s string) Type {
	switch s {
	case "num":
		return Num
	case "bool":
		return Bool
	case "device":
		return Device
	case "str":
		return Str
	case "void":
		return Void
	default:
		return Any
	}
}

type binding struct {
	typ Type
	pos source.Pos
}

type typeChecker struct {
	info   *Info
	diags  *diag.Bag
	scopes []map[string]binding
	result string // declared result type of the function being checked
	// resultCount is how many values the function returns (0 void, 1 single,
	// >1 for `func f() (num, num)`).
	resultCount int
}

// checkBodies type-checks every function body. It runs only when the
// declaration-level checks passed, so it never piles onto existing errors.
func checkBodies(info *Info, diags *diag.Bag) {
	if diags.HasErrors() {
		return
	}
	for _, fi := range info.Funcs {
		c := &typeChecker{info: info, diags: diags}
		c.checkFunc(fi)
	}
}

func (c *typeChecker) checkFunc(fi *FuncInfo) {
	d := fi.Decl
	for _, p := range d.Params {
		if p.Type != "" && !validTypeName(p.Type) {
			c.diags.Errorf(p.Pos(), "unknown type %q", p.Type)
		}
	}
	if d.Result != "" && !validTypeName(d.Result) {
		c.diags.Errorf(d.Pos(), "unknown type %q", d.Result)
	}
	for _, r := range d.Results {
		if !validTypeName(r) {
			c.diags.Errorf(d.Pos(), "unknown type %q", r)
		}
	}
	c.result = d.Result
	c.resultCount = fi.Results
	if d.Body == nil {
		return
	}

	c.pushScope() // globals
	globals := c.scopes[len(c.scopes)-1]
	for name := range c.info.Consts {
		globals[name] = binding{typ: Num}
	}
	for name := range c.info.Devices {
		globals[name] = binding{typ: Device}
	}
	for name := range c.info.DataIndex {
		globals[name] = binding{typ: Data}
	}
	for name := range c.info.RawConsts {
		globals[name] = binding{typ: Str}
	}

	c.pushScope() // parameters
	for _, p := range d.Params {
		c.declare(p.Name, typeOfName(p.Type))
	}
	c.checkStmts(d.Body.List)
	c.popScope()
	c.popScope()

	if d.Result != "" && !terminates(d.Body) {
		c.diags.Warnf(d.Name.Pos(), "missing return at end of %q", d.Name.Name)
	}
	if len(d.Results) > 0 && !terminates(d.Body) {
		c.diags.Warnf(d.Name.Pos(), "missing return at end of %q", d.Name.Name)
	}
}

// ---------------------------------------------------------------------------
// Scopes
// ---------------------------------------------------------------------------

func (c *typeChecker) pushScope() { c.scopes = append(c.scopes, map[string]binding{}) }
func (c *typeChecker) popScope()  { c.scopes = c.scopes[:len(c.scopes)-1] }

func (c *typeChecker) declare(id *ast.Ident, t Type) {
	if id == nil {
		return
	}
	scope := c.scopes[len(c.scopes)-1]
	if _, exists := scope[id.Name]; exists {
		c.diags.Errorf(id.Pos(), "%q redeclared in this block", id.Name)
		return
	}
	scope[id.Name] = binding{typ: t, pos: id.Pos()}
	c.info.VarTypes[id] = t
	c.info.DeclTypes[id] = t
}

func (c *typeChecker) lookup(name string) (Type, bool) {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if b, ok := c.scopes[i][name]; ok {
			return b.typ, true
		}
	}
	return Any, false
}

// ---------------------------------------------------------------------------
// Statements
// ---------------------------------------------------------------------------

func (c *typeChecker) checkStmts(list []ast.Stmt) {
	for _, s := range list {
		c.checkStmt(s)
	}
}

func (c *typeChecker) checkStmt(s ast.Stmt) {
	switch v := s.(type) {
	case *ast.BlockStmt:
		c.pushScope()
		c.checkStmts(v.List)
		c.popScope()
	case *ast.DeclStmt:
		c.checkDecl(v.Decl)
	case *ast.ExprStmt:
		c.expr(v.X)
	case *ast.AssignStmt:
		c.checkAssign(v)
	case *ast.IncDecStmt:
		c.expr(v.X)
	case *ast.IfStmt:
		c.pushScope()
		c.checkStmt(v.Init)
		if v.Cond != nil {
			c.expr(v.Cond)
		}
		c.checkStmt(v.Then)
		c.checkStmt(v.Else)
		c.popScope()
	case *ast.ForStmt:
		c.pushScope()
		c.checkStmt(v.Init)
		if v.Cond != nil {
			c.expr(v.Cond)
		}
		c.checkStmt(v.Post)
		c.checkStmt(v.Body)
		c.popScope()
	case *ast.RangeStmt:
		c.pushScope()
		c.expr(v.X)
		c.declare(v.Key, Num)
		c.declare(v.Value, Num)
		c.checkStmt(v.Body)
		c.popScope()
	case *ast.SwitchStmt:
		c.pushScope()
		c.checkStmt(v.Init)
		if v.Tag != nil {
			c.expr(v.Tag)
		}
		for _, cc := range v.Cases {
			for _, e := range cc.Exprs {
				c.expr(e)
			}
			c.checkStmts(cc.Body)
		}
		c.popScope()
	case *ast.ReturnStmt:
		switch {
		case len(v.Results) > 0:
			for _, r := range v.Results {
				c.expr(r)
			}
			if c.resultCount != len(v.Results) {
				c.diags.Errorf(v.Pos(), "function returns %d values, but this returns %d", c.resultCount, len(v.Results))
			}
		case v.Result != nil:
			c.expr(v.Result)
			if c.resultCount > 1 {
				c.diags.Errorf(v.Pos(), "function returns %d values, but this returns 1", c.resultCount)
			}
		case c.result != "":
			c.diags.Errorf(v.Pos(), "missing return value in %q", c.result)
		case c.resultCount > 0:
			c.diags.Errorf(v.Pos(), "missing return value")
		}
	}
}

func (c *typeChecker) checkDecl(d ast.Decl) {
	switch v := d.(type) {
	case *ast.VarDecl:
		t := Num
		if v.Value != nil {
			t = c.expr(v.Value)
		}
		c.declare(v.Name, t)
	case *ast.ConstDecl:
		t := Num
		if v.Value != nil {
			t = c.expr(v.Value)
		}
		c.declare(v.Name, t)
	}
}

func (c *typeChecker) checkAssign(s *ast.AssignStmt) {
	if tup, ok := s.Lhs.(*ast.TupleExpr); ok {
		c.checkTupleAssign(s, tup)
		return
	}
	rhs := c.expr(s.Rhs)
	if id, ok := s.Lhs.(*ast.Ident); ok {
		if s.Op == token.Define {
			c.declare(id, rhs)
			return
		}
		c.expr(s.Lhs)
		if rhs == Void || rhs == Str {
			c.diags.Errorf(s.Rhs.Pos(), "cannot assign a %s value", rhs)
		}
		return
	}
	// Device, slot or channel target: the value must be a number. A display
	// string (str("...")) is also allowed; a raw string literal is rejected by
	// the lowerer.
	c.expr(s.Lhs)
	switch rhs {
	case Device, Data, Void:
		c.diags.Errorf(s.Rhs.Pos(), "expected a number, found %s", rhs)
	}
}

// checkTupleAssign handles `x, y := f()`: the right side must be a direct call
// to a function returning that many values, and every name is a number.
func (c *typeChecker) checkTupleAssign(s *ast.AssignStmt, tup *ast.TupleExpr) {
	call, ok := s.Rhs.(*ast.CallExpr)
	if !ok {
		c.diags.Errorf(s.Rhs.Pos(), "multiple assignment needs a call returning several values")
	} else if id, ok := call.Fun.(*ast.Ident); ok {
		if fi, isFunc := c.info.Funcs[id.Name]; isFunc {
			c.checkCallArgs(call, fi)
			if fi.Results != len(tup.Elems) {
				c.diags.Errorf(call.Pos(), "%s returns %d values, but %d names are assigned", id.Name, fi.Results, len(tup.Elems))
			}
		} else {
			c.diags.Errorf(call.Pos(), "%s does not return several values", id.Name)
		}
	} else {
		c.diags.Errorf(call.Pos(), "multiple assignment needs a direct function call")
	}
	for _, el := range tup.Elems {
		id, ok := el.(*ast.Ident)
		if !ok {
			c.expr(el)
			continue
		}
		if s.Op == token.Define {
			c.declare(id, Num)
		} else {
			c.expr(id)
		}
	}
}

// ---------------------------------------------------------------------------
// Expressions
// ---------------------------------------------------------------------------
func (c *typeChecker) expr(e ast.Expr) Type {
	if e == nil {
		return Any
	}
	t := c.infer(e)
	c.info.ExprTypes[e] = t
	return t
}

func (c *typeChecker) infer(e ast.Expr) Type {
	switch v := e.(type) {
	case *ast.NumberLit, *ast.SpecialLit:
		return Num
	case *ast.BoolLit:
		return Bool
	case *ast.StringLit:
		return Str
	case *ast.DeviceLit:
		return Device
	case *ast.ParenExpr:
		return c.expr(v.X)
	case *ast.Ident:
		t, _ := c.lookup(v.Name)
		c.info.VarTypes[v] = t
		return t
	case *ast.UnaryExpr:
		t := c.expr(v.X)
		c.requireNum(v.X, t)
		if v.Op == token.Not {
			return Bool
		}
		return Num
	case *ast.BinaryExpr:
		tx := c.expr(v.X)
		ty := c.expr(v.Y)
		c.requireNum(v.X, tx)
		c.requireNum(v.Y, ty)
		if isBoolOp(v.Op) {
			return Bool
		}
		return Num
	case *ast.TernaryExpr:
		c.requireNum(v.Cond, c.expr(v.Cond))
		tt := c.expr(v.Then)
		te := c.expr(v.Else)
		return join(tt, te)
	case *ast.SelectorExpr:
		return c.inferSelector(v)
	case *ast.IndexExpr:
		return c.inferIndex(v)
	case *ast.CallExpr:
		return c.inferCall(v)
	case *ast.RangeExpr:
		return Num
	}
	return Any
}

func (c *typeChecker) inferSelector(v *ast.SelectorExpr) Type {
	switch x := v.X.(type) {
	case *ast.DeviceLit:
		return Num
	case *ast.Ident:
		if _, ok := c.info.Buses[x.Name]; ok {
			return Num
		}
		if t, _ := c.lookup(x.Name); t == Device || t == Data {
			return Num
		}
	case *ast.IndexExpr:
		return Num
	}
	return Any
}

func (c *typeChecker) inferIndex(v *ast.IndexExpr) Type {
	if sel, ok := v.X.(*ast.SelectorExpr); ok {
		if sel.Sel.Name == "slot" || sel.Sel.Name == "channel" {
			return Num
		}
	}
	// Bus.slot[dev][conn]: the inner index selects a bus slot.
	if inner, ok := v.X.(*ast.IndexExpr); ok {
		if sel, ok := inner.X.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok {
				if _, isBus := c.info.Buses[id.Name]; isBus {
					return Num
				}
			}
		}
	}
	if id, ok := v.X.(*ast.Ident); ok {
		if t, _ := c.lookup(id.Name); t == Data {
			return Num
		}
	}
	return Any
}

func (c *typeChecker) inferCall(v *ast.CallExpr) Type {
	if id, ok := v.Fun.(*ast.Ident); ok {
		if fi, isFunc := c.info.Funcs[id.Name]; isFunc {
			c.checkCallArgs(v, fi)
			return typeOfName(fi.Decl.Result)
		}
		return builtinResultType(id.Name)
	}
	if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name != "" {
		if strings.HasPrefix(sel.Sel.Name, "write") {
			return Void
		}
		return Num
	}
	return Any
}

// checkCallArgs checks arguments against a user function's typed parameters.
// Untyped parameters (device/data) are left unconstrained.
func (c *typeChecker) checkCallArgs(call *ast.CallExpr, fi *FuncInfo) {
	for i, p := range fi.Decl.Params {
		if i >= len(call.Args) {
			break
		}
		if p.Type == "num" || p.Type == "bool" {
			c.requireNum(call.Args[i], c.expr(call.Args[i]))
			continue
		}
		c.expr(call.Args[i])
	}
	for i := len(fi.Decl.Params); i < len(call.Args); i++ {
		c.expr(call.Args[i])
	}
}

func builtinResultType(name string) Type {
	switch name {
	case "hash", "read", "readDev", "ireg":
		return Num
	case "str":
		return Str
	case "write", "writeDev", "setIreg", "jump", "reserveRegs":
		return Void
	case "isSet", "isUnset", "isNaN", "isNotNaN",
		"approx", "approxZero", "notApprox", "notApproxZero",
		"isLoadValid", "isStoreValid":
		return Bool
	}
	if f, ok := builtin.Funcs[name]; ok {
		if f.Result {
			return Num
		}
		return Void
	}
	return Any
}

func isBoolOp(op token.Kind) bool {
	switch op {
	case token.Eq, token.Ne, token.Lt, token.Le, token.Gt, token.Ge, token.And, token.Or:
		return true
	}
	return false
}

func join(a, b Type) Type {
	if a == b || b == Any {
		return a
	}
	if a == Any {
		return b
	}
	if a == Num || b == Num {
		return Num
	}
	return a
}

// requireNum reports when e is definitely not usable as a number.
func (c *typeChecker) requireNum(e ast.Expr, t Type) {
	switch t {
	case Device, Data, Str, Void:
		c.diags.Errorf(e.Pos(), "expected a number, found %s", t)
	}
}

// ---------------------------------------------------------------------------
// Reachability (for the missing-return warning)
// ---------------------------------------------------------------------------

func terminates(s ast.Stmt) bool {
	switch v := s.(type) {
	case *ast.ReturnStmt, *ast.GotoStmt, *ast.CallStmt, *ast.RetStmt:
		return true
	case *ast.BlockStmt:
		return len(v.List) > 0 && terminates(v.List[len(v.List)-1])
	case *ast.IfStmt:
		return v.Else != nil && terminates(v.Then) && terminates(v.Else)
	case *ast.ForStmt:
		return v.Cond == nil && !hasBreak(v.Body)
	case *ast.SwitchStmt:
		return switchTerminates(v)
	}
	return false
}

// hasBreak reports whether a loop body can break out of the loop itself. A
// break inside a nested loop or switch belongs to that construct, so the walk
// does not descend into them.
func hasBreak(b *ast.BlockStmt) bool {
	found := false
	var walk func(ast.Stmt)
	walk = func(s ast.Stmt) {
		switch v := s.(type) {
		case *ast.BreakStmt:
			found = true
		case *ast.BlockStmt:
			for _, st := range v.List {
				walk(st)
			}
		case *ast.IfStmt:
			walk(v.Then)
			walk(v.Else)
		}
	}
	walk(b)
	return found
}

func switchTerminates(s *ast.SwitchStmt) bool {
	hasDefault := false
	for _, cc := range s.Cases {
		if cc.Default {
			hasDefault = true
		}
		if len(cc.Body) == 0 || !terminates(cc.Body[len(cc.Body)-1]) {
			return false
		}
	}
	return hasDefault
}
