// Package sema performs name resolution and compile-time constant evaluation.
package sema

import (
	"fmt"
	"hash/crc32"
	"math"
	"strconv"

	"ic10go/internal/ast"
	"ic10go/internal/builtin"
	"ic10go/internal/diag"
	"ic10go/internal/source"
	"ic10go/internal/token"
)

// StackSize is the number of slots in an IC10 chip's persistent stack.
const StackSize = 512

// FixedDataBase is the first slot of the data segment in the "middle" layout:
// the segment sits at a fixed address and the high slots stay free for
// register spills.
const FixedDataBase = 256

// Options controls semantic analysis.
type Options struct {
	// FixedDataBase, when non-zero, places the data segment at that fixed slot
	// (the "middle" layout) instead of at the top of the stack.
	FixedDataBase int
	// AutoTable tables eligible plain switches into the data segment (no
	// `table` marker needed). AutoTableMax caps the table size (default 64).
	AutoTable    bool
	AutoTableMax int
}

type FuncInfo struct {
	Decl   *ast.FuncDecl
	Params []string
}

// BusInfo is a `bus Name { ... }` declaration: a named set of network channels
// shared between chips. Slots map to Channel0.. in order.
type BusInfo struct {
	Name  string
	Slots map[string]int // slot name -> channel index
}

// BusConn is one of a chip's access points to a bus network: a device port (or
// alias) and a connection, giving 8 consecutive channels.
type BusConn struct {
	Device string
	Conn   int
}

// DataTable is a compile-time `data` table stored in the persistent stack.
// Values are rendered IC10 literals (numbers or game enum names such as
// LogicType.Open), emitted verbatim by the loader.
type DataTable struct {
	Name   string
	Values []string
	Base   int // first stack slot
}

// TableSwitch describes a `switch tag table { ... }` that is lowered to data
// tables (one per assignment target) instead of a comparison chain.
type TableSwitch struct {
	Low, High int
	Targets   []TableSwitchTarget
}

// TableSwitchTarget is one assignment target of a table switch, with the
// per-index values in Table.
type TableSwitchTarget struct {
	Assign *ast.AssignStmt
	Table  *DataTable
}

type Info struct {
	Consts    map[string]float64
	RawConsts map[string]string
	Devices   map[string]string // const NAME = dN (device alias)
	Funcs     map[string]*FuncInfo
	Main      *ast.FuncDecl
	// Buses maps a `bus` name to its channel layout.
	Buses map[string]*BusInfo
	// BusBindings maps a `bus` name to this chip's default access point
	// (from `use Bus on dev:conn`).
	BusBindings map[string]BusConn

	// Data segment (top-level `data` tables). All zero when there is none.
	Data        []*DataTable
	DataIndex   map[string]*DataTable
	DataSize    int     // sentinel (if any) + all elements
	Sentinel    int     // stack slot holding the version, -1 if no data
	DataVersion float64 // version written by the loader and checked at runtime

	// TableSwitches maps a `switch ... table` node to its generated tables.
	TableSwitches map[*ast.SwitchStmt]*TableSwitch
	// AutoTabled counts plain switches tabled by AutoTable.
	AutoTabled int

	// ExprTypes annotates every checked expression with its static type, and
	// VarTypes every identifier with the type it resolved to. DeclTypes covers
	// declarations only (for type inlay hints). They are best-effort (used by
	// the editor); the compiler itself treats every runtime value as a double.
	ExprTypes map[ast.Expr]Type
	VarTypes  map[*ast.Ident]Type
	DeclTypes map[*ast.Ident]Type
}

// Check resolves declarations and evaluates constants.
func Check(file *ast.File, diags *diag.Bag) *Info {
	return CheckWithOptions(file, diags, Options{})
}

// CheckWithOptions is Check with explicit options.
func CheckWithOptions(file *ast.File, diags *diag.Bag, opts Options) *Info {
	info := &Info{
		Consts:        map[string]float64{},
		RawConsts:     map[string]string{},
		Devices:       map[string]string{},
		Funcs:         map[string]*FuncInfo{},
		Buses:         map[string]*BusInfo{},
		BusBindings:   map[string]BusConn{},
		DataIndex:     map[string]*DataTable{},
		Sentinel:      -1,
		TableSwitches: map[*ast.SwitchStmt]*TableSwitch{},
		ExprTypes:     map[ast.Expr]Type{},
		VarTypes:      map[*ast.Ident]Type{},
		DeclTypes:     map[*ast.Ident]Type{},
	}

	var pendingConsts []pendingConst
	var pendingTables []pendingTable

	for _, d := range file.Decls {
		switch d := d.(type) {
		case *ast.ConstDecl:
			if _, exists := info.Consts[d.Name.Name]; exists {
				diags.Errorf(d.Name.Pos(), "constant %q redeclared", d.Name.Name)
				continue
			}
			if _, exists := info.RawConsts[d.Name.Name]; exists {
				diags.Errorf(d.Name.Pos(), "constant %q redeclared", d.Name.Name)
				continue
			}
			if _, exists := info.Devices[d.Name.Name]; exists {
				diags.Errorf(d.Name.Pos(), "constant %q redeclared", d.Name.Name)
				continue
			}
			if _, exists := info.Funcs[d.Name.Name]; exists {
				diags.Errorf(d.Name.Pos(), "constant %q conflicts with a function", d.Name.Name)
				continue
			}
			if _, exists := info.DataIndex[d.Name.Name]; exists {
				diags.Errorf(d.Name.Pos(), "constant %q conflicts with a data table", d.Name.Name)
				continue
			}
			// const NAME = dN / db aliases a device port; const NAME = other
			// aliases a previously declared device alias.
			if dev, ok := deviceAlias(d.Value, info.Devices); ok {
				info.Devices[d.Name.Name] = dev
				continue
			}
			if raw, ok := EvalRaw(d.Value); ok {
				info.RawConsts[d.Name.Name] = raw
				continue
			}
			// A numeric constant is evaluated after every declaration is known
			// (see evalPending), so it may call pure user functions.
			pendingConsts = append(pendingConsts, pendingConst{d.Name.Name, d.Value})
		case *ast.DataDecl:
			if _, exists := info.DataIndex[d.Name.Name]; exists {
				diags.Errorf(d.Name.Pos(), "data table %q redeclared", d.Name.Name)
				continue
			}
			if _, exists := info.Consts[d.Name.Name]; exists {
				diags.Errorf(d.Name.Pos(), "data table %q conflicts with a constant", d.Name.Name)
				continue
			}
			if _, exists := info.RawConsts[d.Name.Name]; exists {
				diags.Errorf(d.Name.Pos(), "data table %q conflicts with a constant", d.Name.Name)
				continue
			}
			if _, exists := info.Devices[d.Name.Name]; exists {
				diags.Errorf(d.Name.Pos(), "data table %q conflicts with a constant", d.Name.Name)
				continue
			}
			if _, exists := info.Funcs[d.Name.Name]; exists {
				diags.Errorf(d.Name.Pos(), "data table %q conflicts with a function", d.Name.Name)
				continue
			}
			// Register the table now (so name conflicts are still caught) and
			// fill its elements after the constants are resolved, so an element
			// may call a pure user function.
			t := &DataTable{Name: d.Name.Name}
			info.Data = append(info.Data, t)
			info.DataIndex[d.Name.Name] = t
			pendingTables = append(pendingTables, pendingTable{table: t, decl: d})
		case *ast.FuncDecl:
			if _, exists := info.Funcs[d.Name.Name]; exists {
				diags.Errorf(d.Name.Pos(), "function %q redeclared", d.Name.Name)
				continue
			}
			if _, exists := info.Consts[d.Name.Name]; exists {
				diags.Errorf(d.Name.Pos(), "function %q conflicts with a constant", d.Name.Name)
				continue
			}
			if _, exists := info.Devices[d.Name.Name]; exists {
				diags.Errorf(d.Name.Pos(), "function %q conflicts with a constant", d.Name.Name)
				continue
			}
			if _, exists := info.DataIndex[d.Name.Name]; exists {
				diags.Errorf(d.Name.Pos(), "function %q conflicts with a data table", d.Name.Name)
				continue
			}
			fi := &FuncInfo{Decl: d}
			seen := map[string]bool{}
			for _, p := range d.Params {
				if seen[p.Name.Name] {
					diags.Errorf(p.Name.Pos(), "duplicate parameter %q", p.Name.Name)
					continue
				}
				seen[p.Name.Name] = true
				fi.Params = append(fi.Params, p.Name.Name)
			}
			info.Funcs[d.Name.Name] = fi
			if d.Name.Name == "main" {
				if len(d.Params) != 0 {
					diags.Errorf(d.Name.Pos(), "main must not take parameters")
				}
				if d.Result != "" {
					diags.Errorf(d.Name.Pos(), "main must not return a value")
				}
				info.Main = d
			}
		case *ast.BusDecl:
			if _, exists := info.Buses[d.Name.Name]; exists {
				diags.Errorf(d.Name.Pos(), "bus %q redeclared", d.Name.Name)
				continue
			}
			if _, exists := info.Consts[d.Name.Name]; exists {
				diags.Errorf(d.Name.Pos(), "bus %q conflicts with a constant", d.Name.Name)
				continue
			}
			if len(d.Slots) > 8 {
				diags.Errorf(d.Name.Pos(),
					"bus %q has %d slots; a network connection has only 8 channels — split it into another bus",
					d.Name.Name, len(d.Slots))
			}
			bi := &BusInfo{Name: d.Name.Name, Slots: map[string]int{}}
			for i, s := range d.Slots {
				if _, dup := bi.Slots[s.Name.Name]; dup {
					diags.Errorf(s.Name.Pos(), "bus slot %q redeclared", s.Name.Name)
					continue
				}
				switch s.Type {
				case "num", "bool", "str":
				default:
					diags.Errorf(s.Name.Pos(), "unknown bus slot type %q (want num, bool or str)", s.Type)
				}
				bi.Slots[s.Name.Name] = i
			}
			info.Buses[d.Name.Name] = bi
		case *ast.UseDecl:
			if _, dup := info.BusBindings[d.Bus.Name]; dup {
				diags.Errorf(d.Bus.Pos(), "bus %q bound more than once in this chip", d.Bus.Name)
				continue
			}
			if _, ok := info.Buses[d.Bus.Name]; !ok {
				diags.Errorf(d.Bus.Pos(), "unknown bus %q", d.Bus.Name)
				continue
			}
			if len(d.Bindings) != 1 {
				diags.Errorf(d.Bus.Pos(), "use %s takes exactly one connection (dev:conn)", d.Bus.Name)
				continue
			}
			b := d.Bindings[0]
			dev := b.Device
			if alias, isAlias := info.Devices[dev]; isAlias {
				dev = alias
			} else if !isDevicePort(dev) {
				diags.Errorf(b.Pos(), "unknown device %q", b.Device)
				continue
			}
			info.BusBindings[d.Bus.Name] = BusConn{Device: dev, Conn: b.Conn}
		}
	}

	evalPending(info, diags, pendingConsts, pendingTables)

	collectTableSwitches(info, diags, opts)
	assignData(info, opts.FixedDataBase)
	checkBodies(info, diags)
	return info
}

// assignData lays the data tables out and derives the version sentinel. With
// fixedBase > 0 the segment starts there ("middle" layout); otherwise it sits
// at the top of the stack.
func assignData(info *Info, fixedBase int) {
	if len(info.Data) == 0 {
		return
	}
	size := 1 // version sentinel
	for _, t := range info.Data {
		size += len(t.Values)
	}
	info.DataSize = size
	base := StackSize - size
	if fixedBase > 0 {
		base = fixedBase
	}
	info.Sentinel = base
	cur := base + 1
	for _, t := range info.Data {
		t.Base = cur
		cur += len(t.Values)
	}
	info.DataVersion = dataVersion(info.Data)
}

// dataVersion derives a stable version number from the table contents so a
// stale stack can be detected at runtime.
func dataVersion(tables []*DataTable) float64 {
	h := crc32.NewIEEE()
	for _, t := range tables {
		fmt.Fprintf(h, "%s:%d", t.Name, len(t.Values))
		for _, v := range t.Values {
			fmt.Fprintf(h, ",%s", v)
		}
	}
	return float64(int32(h.Sum32()))
}

// dataLiteral renders a data/table element as an IC10 literal: a number, or a
// game enum name such as LogicType.Open (emitted verbatim, resolved by the
// game assembler).
func dataLiteral(e ast.Expr, consts map[string]float64) (string, bool) {
	if v, ok := Eval(e, consts); ok {
		return formatDataValue(v), true
	}
	if sel, ok := e.(*ast.SelectorExpr); ok {
		if id, ok := sel.X.(*ast.Ident); ok {
			return id.Name + "." + sel.Sel.Name, true
		}
	}
	return "", false
}

// formatDataValue renders a numeric data value as an IC10 literal.
func formatDataValue(v float64) string {
	switch {
	case math.IsNaN(v):
		return "nan"
	case math.IsInf(v, 1):
		return "pinf"
	case math.IsInf(v, -1):
		return "ninf"
	}
	const maxExact = 1 << 53
	if v == math.Trunc(v) && v >= -maxExact && v <= maxExact {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// collectTableSwitches turns `switch ... table` statements into data tables,
// and (when AutoTable is on) also eligible plain switches.
func collectTableSwitches(info *Info, diags *diag.Bag, opts Options) {
	maxSize := opts.AutoTableMax
	if maxSize <= 0 {
		maxSize = 64
	}
	for _, fi := range info.Funcs {
		if fi.Decl.Body != nil {
			walkStmts(fi.Decl.Body.List, func(s *ast.SwitchStmt) {
				switch {
				case s.Table:
					buildTableSwitch(info, s, diags, true, 0)
				case opts.AutoTable:
					if buildTableSwitch(info, s, diags, false, maxSize) {
						info.AutoTabled++
						diags.Warnf(s.Pos(), "switch auto-tabled into the data segment; install the data loader first")
					}
				}
			})
		}
	}
}

func walkStmts(stmts []ast.Stmt, visit func(*ast.SwitchStmt)) {
	for _, s := range stmts {
		switch s := s.(type) {
		case *ast.BlockStmt:
			walkStmts(s.List, visit)
		case *ast.IfStmt:
			walkStmts(s.Then.List, visit)
			if s.Else != nil {
				walkStmts([]ast.Stmt{s.Else}, visit)
			}
		case *ast.ForStmt:
			walkStmts(s.Body.List, visit)
		case *ast.RangeStmt:
			walkStmts(s.Body.List, visit)
		case *ast.SwitchStmt:
			visit(s)
			for _, c := range s.Cases {
				walkStmts(c.Body, visit)
			}
		}
	}
}

// buildTableSwitch validates a table switch and generates one data table per
// assignment target: all cases must be dense integers assigning constants to
// the same targets in the same order. It returns false when the switch is not
// eligible; in auto mode (explicit=false) it stays silent and enforces a
// minimum case count and a maximum table size.
func buildTableSwitch(info *Info, s *ast.SwitchStmt, diags *diag.Bag, explicit bool, maxSize int) bool {
	fail := func(pos source.Pos, format string, args ...any) bool {
		if explicit {
			diags.Errorf(pos, format, args...)
		}
		return false
	}
	if s.Tag == nil {
		return fail(s.Pos(), "table switch requires a tag")
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
			return fail(c.Pos(), "table switch case must have one value")
		}
		v, ok := Eval(c.Exprs[0], info.Consts)
		if !ok || v != math.Trunc(v) {
			return fail(c.Exprs[0].Pos(), "table switch case value must be an integer constant")
		}
		entries = append(entries, entry{int(v), c.Body})
	}
	if len(entries) == 0 {
		return fail(s.Pos(), "table switch has no cases")
	}
	if !explicit && len(entries) < 5 {
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
		return fail(s.Pos(), "table switch cases must be dense (%d..%d)", lo, hi)
	}
	if maxSize > 0 && hi-lo+1 > maxSize {
		return fail(s.Pos(), "table switch table is too large (%d > %d)", hi-lo+1, maxSize)
	}
	n := len(entries[0].body)
	if n == 0 {
		return fail(s.Pos(), "table switch case body must assign a constant")
	}
	targets := make([]*ast.AssignStmt, n)
	for j, st := range entries[0].body {
		as, ok := st.(*ast.AssignStmt)
		if !ok || as.Op != token.Assign {
			return fail(st.Pos(), "table switch case must be `target = constant`")
		}
		targets[j] = as
	}
	values := make([][]string, n)
	for j := range values {
		values[j] = make([]string, hi-lo+1)
	}
	for _, e := range entries {
		if len(e.body) != n {
			return fail(s.Pos(), "table switch cases must assign the same targets")
		}
		for j, st := range e.body {
			as, ok := st.(*ast.AssignStmt)
			if !ok || as.Op != token.Assign || !sameTarget(targets[j].Lhs, as.Lhs) {
				return fail(st.Pos(), "table switch cases must assign the same targets")
			}
			cv, ok := dataLiteral(as.Rhs, info.Consts)
			if !ok {
				return fail(as.Rhs.Pos(), "table switch value must be a constant")
			}
			values[j][e.val-lo] = cv
		}
	}
	ts := &TableSwitch{Low: lo, High: hi}
	for j := range targets {
		t := &DataTable{Name: fmt.Sprintf("_sw%d_%d", s.Pos().Offset, j), Values: values[j]}
		info.Data = append(info.Data, t)
		ts.Targets = append(ts.Targets, TableSwitchTarget{Assign: targets[j], Table: t})
	}
	info.TableSwitches[s] = ts
	return true
}

// sameTarget reports whether two assignment targets have the same shape (the
// same variable, or the same device.logic).
func sameTarget(a, b ast.Expr) bool {
	switch x := a.(type) {
	case *ast.Ident:
		y, ok := b.(*ast.Ident)
		return ok && x.Name == y.Name
	case *ast.SelectorExpr:
		y, ok := b.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		return sameDevice(x.X, y.X) && x.Sel.Name == y.Sel.Name
	}
	return false
}

// sameDevice reports whether two device operands name the same port (a device
// literal or an alias identifier).
func sameDevice(a, b ast.Expr) bool {
	switch x := a.(type) {
	case *ast.Ident:
		y, ok := b.(*ast.Ident)
		return ok && x.Name == y.Name
	case *ast.DeviceLit:
		y, ok := b.(*ast.DeviceLit)
		return ok && x.Name == y.Name
	}
	return false
}

// evalState folds compile-time expressions. With funcs set it also interprets
// pure user functions, so `const X = helper(3)` works when helper touches
// nothing but its arguments.
type evalState struct {
	consts map[string]float64
	funcs  map[string]*FuncInfo
	devs   map[string]string
	data   map[string]*DataTable
	pure   map[string]bool
	scope  []map[string]float64
	steps  int
	depth  int
}

// maxEvalSteps bounds compile-time interpretation so a non-terminating loop in
// a `const` reports an error instead of hanging the compiler. Compile-time
// helpers are expected to be tiny (a handful of table values).
const maxEvalSteps = 200000

// maxEvalDepth bounds compile-time call nesting (the language has no recursion,
// so this only guards a malformed program).
const maxEvalDepth = 64

// Eval evaluates an expression to a compile-time constant. It returns false if
// the expression is not constant. User functions are not interpreted; the
// declaration walk uses evalPending, which is.
func Eval(e ast.Expr, consts map[string]float64) (float64, bool) {
	return (&evalState{consts: consts}).expr(e)
}

func (ev *evalState) expr(e ast.Expr) (float64, bool) {
	if ev.steps++; ev.steps > maxEvalSteps {
		return 0, false
	}
	switch e := e.(type) {
	case *ast.NumberLit:
		return e.Value, true
	case *ast.BoolLit:
		return boolToNum(e.Value), true
	case *ast.SpecialLit:
		switch e.Name {
		case "nan":
			return math.NaN(), true
		case "pinf":
			return math.Inf(1), true
		case "ninf":
			return math.Inf(-1), true
		}
		return 0, false
	case *ast.ParenExpr:
		return ev.expr(e.X)
	case *ast.Ident:
		return ev.lookup(e.Name)
	case *ast.UnaryExpr:
		return ev.unary(e)
	case *ast.BinaryExpr:
		return ev.binary(e)
	case *ast.TernaryExpr:
		c, ok := ev.expr(e.Cond)
		if !ok {
			return 0, false
		}
		if c != 0 {
			return ev.expr(e.Then)
		}
		return ev.expr(e.Else)
	case *ast.CallExpr:
		return ev.call(e)
	}
	return 0, false
}

func (ev *evalState) unary(e *ast.UnaryExpr) (float64, bool) {
	x, ok := ev.expr(e.X)
	if !ok {
		return 0, false
	}
	switch e.Op {
	case token.Minus:
		return -x, true
	case token.Plus:
		return x, true
	case token.Not:
		return boolToNum(x == 0), true
	case token.Tilde:
		return float64(^int64(x)), true
	}
	return 0, false
}

func (ev *evalState) binary(e *ast.BinaryExpr) (float64, bool) {
	x, ok := ev.expr(e.X)
	if !ok {
		return 0, false
	}
	// Short-circuit logical operators.
	if e.Op == token.And {
		if x == 0 {
			return 0, true
		}
		y, ok := ev.expr(e.Y)
		if !ok {
			return 0, false
		}
		return boolToNum(y != 0), true
	}
	if e.Op == token.Or {
		if x != 0 {
			return 1, true
		}
		y, ok := ev.expr(e.Y)
		if !ok {
			return 0, false
		}
		return boolToNum(y != 0), true
	}
	y, ok := ev.expr(e.Y)
	if !ok {
		return 0, false
	}
	return applyOp(e.Op, x, y)
}

// applyOp applies a pure binary operator. And/Or short-circuit and are handled
// by the caller.
func applyOp(op token.Kind, x, y float64) (float64, bool) {
	switch op {
	case token.Plus:
		return x + y, true
	case token.Minus:
		return x - y, true
	case token.Star:
		return x * y, true
	case token.Slash:
		return x / y, true
	case token.Percent:
		return ic10Mod(x, y), true
	case token.Amp:
		return float64(int64(x) & int64(y)), true
	case token.Pipe:
		return float64(int64(x) | int64(y)), true
	case token.Caret:
		return float64(int64(x) ^ int64(y)), true
	case token.Shl:
		return float64(int64(x) << uint(int64(y)&63)), true
	case token.Shr:
		return float64(int64(x) >> uint(int64(y)&63)), true
	case token.Eq:
		return boolToNum(x == y), true
	case token.Ne:
		return boolToNum(x != y), true
	case token.Lt:
		return boolToNum(x < y), true
	case token.Le:
		return boolToNum(x <= y), true
	case token.Gt:
		return boolToNum(x > y), true
	case token.Ge:
		return boolToNum(x >= y), true
	}
	return 0, false
}

// assignToBinary maps a compound assignment to the operator it applies.
func assignToBinary(op token.Kind) (token.Kind, bool) {
	switch op {
	case token.PlusAssign:
		return token.Plus, true
	case token.MinusAssign:
		return token.Minus, true
	case token.StarAssign:
		return token.Star, true
	case token.SlashAssign:
		return token.Slash, true
	case token.PercentAssign:
		return token.Percent, true
	case token.AmpAssign:
		return token.Amp, true
	case token.PipeAssign:
		return token.Pipe, true
	case token.CaretAssign:
		return token.Caret, true
	case token.ShlAssign:
		return token.Shl, true
	case token.ShrAssign:
		return token.Shr, true
	}
	return 0, false
}

func (ev *evalState) call(e *ast.CallExpr) (float64, bool) {
	id, ok := e.Fun.(*ast.Ident)
	if !ok {
		return 0, false
	}
	// hash("...") is a compile-time CRC-32; handle before evaluating args.
	if id.Name == "hash" {
		if len(e.Args) == 1 {
			if s, ok := evalString(e.Args[0]); ok {
				return float64(int32(builtin.Hash(s))), true
			}
		}
		return 0, false
	}
	if ev.funcs != nil {
		if _, isUser := ev.funcs[id.Name]; isUser {
			return ev.userCall(id.Name, e)
		}
	}
	args := make([]float64, len(e.Args))
	for i, a := range e.Args {
		v, ok := ev.expr(a)
		if !ok {
			return 0, false
		}
		args[i] = v
	}
	return foldBuiltin(id.Name, args)
}

// foldBuiltin folds a pure builtin over constant arguments. Only builtins with
// an exact arithmetic meaning are folded; anything with device/stack access or
// nondeterministic output is left alone.
func foldBuiltin(name string, a []float64) (float64, bool) {
	switch len(a) {
	case 1:
		switch name {
		case "abs":
			return math.Abs(a[0]), true
		case "sgn":
			if a[0] > 0 {
				return 1, true
			} else if a[0] < 0 {
				return -1, true
			}
			return 0, true
		case "sqrt":
			return math.Sqrt(a[0]), true
		case "exp":
			return math.Exp(a[0]), true
		case "log":
			return math.Log(a[0]), true
		case "floor":
			return math.Floor(a[0]), true
		case "ceil":
			return math.Ceil(a[0]), true
		case "round":
			return math.Round(a[0]), true
		case "trunc":
			return math.Trunc(a[0]), true
		case "sin":
			return math.Sin(a[0]), true
		case "cos":
			return math.Cos(a[0]), true
		case "tan":
			return math.Tan(a[0]), true
		case "asin":
			return math.Asin(a[0]), true
		case "acos":
			return math.Acos(a[0]), true
		case "atan":
			return math.Atan(a[0]), true
		case "isNaN":
			return boolToNum(math.IsNaN(a[0])), true
		case "isNotNaN":
			return boolToNum(!math.IsNaN(a[0])), true
		}
	case 2:
		switch name {
		case "pow":
			return math.Pow(a[0], a[1]), true
		case "atan2":
			return math.Atan2(a[0], a[1]), true
		case "min":
			return math.Min(a[0], a[1]), true
		case "max":
			return math.Max(a[0], a[1]), true
		}
	case 3:
		switch name {
		case "clamp":
			// IC10: clamp a into [min, max]; NaN bounds give NaN.
			return ic10Clamp(a[0], a[1], a[2]), true
		case "lerp":
			// IC10: interpolate a..b by t, t clamped to 0..1.
			t := ic10Clamp(a[2], 0, 1)
			return a[0] + (a[1]-a[0])*t, true
		}
	}
	return 0, false
}

// ic10Clamp matches IC10's `clamp`: NaN in either bound yields NaN.
func ic10Clamp(x, lo, hi float64) float64 {
	if math.IsNaN(lo) || math.IsNaN(hi) {
		return math.NaN()
	}
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

// ---------------------------------------------------------------------------
// Interpreting pure user functions
// ---------------------------------------------------------------------------

// evalFlow is how a statement leaves a compile-time function body.
type evalFlow int

const (
	flowNormal evalFlow = iota
	flowReturn
	flowBreak
	flowContinue
	flowFail // not evaluable at compile time
)

func (ev *evalState) lookup(name string) (float64, bool) {
	for i := len(ev.scope) - 1; i >= 0; i-- {
		if v, ok := ev.scope[i][name]; ok {
			return v, true
		}
	}
	v, ok := ev.consts[name]
	return v, ok
}

func (ev *evalState) declare(name string, v float64) {
	if len(ev.scope) == 0 {
		return
	}
	ev.scope[len(ev.scope)-1][name] = v
}

func (ev *evalState) assign(name string, v float64) bool {
	for i := len(ev.scope) - 1; i >= 0; i-- {
		if _, ok := ev.scope[i][name]; ok {
			ev.scope[i][name] = v
			return true
		}
	}
	return false
}

// userCall interprets a pure user function over constant arguments.
func (ev *evalState) userCall(name string, call *ast.CallExpr) (float64, bool) {
	if !ev.isPure(name) {
		return 0, false
	}
	fi := ev.funcs[name]
	if fi == nil || fi.Decl == nil || fi.Decl.Body == nil {
		return 0, false
	}
	if len(call.Args) != len(fi.Params) {
		return 0, false
	}
	args := make([]float64, len(call.Args))
	for i, a := range call.Args {
		v, ok := ev.expr(a)
		if !ok {
			return 0, false
		}
		args[i] = v
	}
	if ev.depth >= maxEvalDepth {
		return 0, false
	}
	ev.depth++
	defer func() { ev.depth-- }()

	scope := make(map[string]float64, len(fi.Params))
	for i, p := range fi.Params {
		scope[p] = args[i]
	}
	ev.scope = append(ev.scope, scope)
	defer func() { ev.scope = ev.scope[:len(ev.scope)-1] }()

	v, has, fl := ev.execStmts(fi.Decl.Body.List)
	if fl == flowFail {
		return 0, false
	}
	if fl == flowReturn && has {
		return v, true
	}
	if fi.Decl.Result != "" {
		return 0, false // declared a result but no return was reached
	}
	return 0, true // void function
}

func (ev *evalState) execBlock(b *ast.BlockStmt) (float64, bool, evalFlow) {
	ev.scope = append(ev.scope, map[string]float64{})
	defer func() { ev.scope = ev.scope[:len(ev.scope)-1] }()
	return ev.execStmts(b.List)
}

func (ev *evalState) execStmts(list []ast.Stmt) (float64, bool, evalFlow) {
	for _, s := range list {
		v, has, fl := ev.execStmt(s)
		if fl != flowNormal {
			return v, has, fl
		}
	}
	return 0, false, flowNormal
}

func (ev *evalState) execStmt(s ast.Stmt) (float64, bool, evalFlow) {
	if ev.steps++; ev.steps > maxEvalSteps {
		return 0, false, flowFail
	}
	switch s := s.(type) {
	case *ast.BlockStmt:
		return ev.execBlock(s)

	case *ast.DeclStmt:
		switch d := s.Decl.(type) {
		case *ast.VarDecl:
			v := 0.0
			if d.Value != nil {
				var ok bool
				v, ok = ev.expr(d.Value)
				if !ok {
					return 0, false, flowFail
				}
			}
			ev.declare(d.Name.Name, v)
			return 0, false, flowNormal
		case *ast.ConstDecl:
			v, ok := ev.expr(d.Value)
			if !ok {
				return 0, false, flowFail
			}
			ev.declare(d.Name.Name, v)
			return 0, false, flowNormal
		}
		return 0, false, flowFail

	case *ast.AssignStmt:
		id, ok := s.Lhs.(*ast.Ident)
		if !ok {
			return 0, false, flowFail
		}
		if s.Op == token.Define {
			v, ok := ev.expr(s.Rhs)
			if !ok {
				return 0, false, flowFail
			}
			ev.declare(id.Name, v)
			return 0, false, flowNormal
		}
		if bin, ok := assignToBinary(s.Op); ok {
			old, ok := ev.lookup(id.Name)
			if !ok {
				return 0, false, flowFail
			}
			y, ok := ev.expr(s.Rhs)
			if !ok {
				return 0, false, flowFail
			}
			v, ok := applyOp(bin, old, y)
			if !ok || !ev.assign(id.Name, v) {
				return 0, false, flowFail
			}
			return 0, false, flowNormal
		}
		v, ok := ev.expr(s.Rhs)
		if !ok || !ev.assign(id.Name, v) {
			return 0, false, flowFail
		}
		return 0, false, flowNormal

	case *ast.IncDecStmt:
		id, ok := s.X.(*ast.Ident)
		if !ok {
			return 0, false, flowFail
		}
		v, ok := ev.lookup(id.Name)
		if !ok {
			return 0, false, flowFail
		}
		if s.Op == token.PlusPlus {
			v++
		} else {
			v--
		}
		if !ev.assign(id.Name, v) {
			return 0, false, flowFail
		}
		return 0, false, flowNormal

	case *ast.IfStmt:
		return ev.execIf(s)

	case *ast.ForStmt:
		return ev.execFor(s)

	case *ast.RangeStmt:
		return ev.execRange(s)

	case *ast.ReturnStmt:
		if s.Result == nil {
			return 0, false, flowReturn
		}
		v, ok := ev.expr(s.Result)
		if !ok {
			return 0, false, flowFail
		}
		return v, true, flowReturn

	case *ast.BreakStmt:
		if s.Label != nil {
			return 0, false, flowFail
		}
		return 0, false, flowBreak

	case *ast.ContinueStmt:
		if s.Label != nil {
			return 0, false, flowFail
		}
		return 0, false, flowContinue

	case *ast.ExprStmt:
		// Only a call is worth evaluating; it must be pure to have got here.
		if _, ok := s.X.(*ast.CallExpr); !ok {
			return 0, false, flowFail
		}
		if _, ok := ev.expr(s.X); !ok {
			return 0, false, flowFail
		}
		return 0, false, flowNormal
	}
	// switch, labels, goto, call and ret are not evaluable.
	return 0, false, flowFail
}

func (ev *evalState) execIf(s *ast.IfStmt) (float64, bool, evalFlow) {
	ev.scope = append(ev.scope, map[string]float64{})
	defer func() { ev.scope = ev.scope[:len(ev.scope)-1] }()

	if s.Init != nil {
		if _, _, fl := ev.execStmt(s.Init); fl != flowNormal {
			return 0, false, fl
		}
	}
	c, ok := ev.expr(s.Cond)
	if !ok {
		return 0, false, flowFail
	}
	if c != 0 {
		return ev.execBlock(s.Then)
	}
	if s.Else != nil {
		switch e := s.Else.(type) {
		case *ast.BlockStmt:
			return ev.execBlock(e)
		case *ast.IfStmt:
			return ev.execIf(e)
		}
		return 0, false, flowFail
	}
	return 0, false, flowNormal
}

func (ev *evalState) execFor(s *ast.ForStmt) (float64, bool, evalFlow) {
	ev.scope = append(ev.scope, map[string]float64{})
	defer func() { ev.scope = ev.scope[:len(ev.scope)-1] }()

	if s.Init != nil {
		if _, _, fl := ev.execStmt(s.Init); fl != flowNormal {
			return 0, false, fl
		}
	}
	for {
		if s.Cond != nil {
			c, ok := ev.expr(s.Cond)
			if !ok {
				return 0, false, flowFail
			}
			if c == 0 {
				break
			}
		}
		v, has, fl := ev.execBlock(s.Body)
		switch fl {
		case flowReturn:
			return v, has, flowReturn
		case flowFail:
			return 0, false, flowFail
		case flowBreak:
			return 0, false, flowNormal
		}
		if s.Post != nil {
			if _, _, fl := ev.execStmt(s.Post); fl != flowNormal {
				return 0, false, fl
			}
		}
	}
	return 0, false, flowNormal
}

func (ev *evalState) execRange(s *ast.RangeStmt) (float64, bool, evalFlow) {
	// Only `for i := range count`; a data-table range is not numeric.
	if s.Value != nil {
		return 0, false, flowFail
	}
	n, ok := ev.expr(s.X)
	if !ok || n < 0 || n != math.Trunc(n) || n > maxEvalSteps {
		return 0, false, flowFail
	}
	ev.scope = append(ev.scope, map[string]float64{})
	defer func() { ev.scope = ev.scope[:len(ev.scope)-1] }()

	for i := 0.0; i < n; i++ {
		ev.declare(s.Key.Name, i)
		v, has, fl := ev.execBlock(s.Body)
		switch fl {
		case flowReturn:
			return v, has, flowReturn
		case flowFail:
			return 0, false, flowFail
		case flowBreak:
			return 0, false, flowNormal
		}
	}
	return 0, false, flowNormal
}

// ---------------------------------------------------------------------------
// Purity
// ---------------------------------------------------------------------------

func (ev *evalState) isPure(name string) bool {
	if ev.pure == nil {
		ev.pure = pureFuncs(ev.funcs, ev.devs, ev.data)
	}
	return ev.pure[name]
}

// pureFuncs returns, for every function, whether it can be run at compile time:
// no device access, no side-effecting or device builtin, no data-table read, and
// no call to an impure function.
func pureFuncs(funcs map[string]*FuncInfo, devs map[string]string, data map[string]*DataTable) map[string]bool {
	impure := map[string]bool{}
	for name, fi := range funcs {
		if fi.Decl == nil || fi.Decl.Body == nil || bodyImpure(fi.Decl.Body, funcs, devs, data) {
			impure[name] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for name, fi := range funcs {
			if impure[name] {
				continue
			}
			bad := false
			walkBodyCalls(fi.Decl.Body, func(c *ast.CallExpr) {
				if id, ok := c.Fun.(*ast.Ident); ok {
					if _, isFunc := funcs[id.Name]; isFunc && impure[id.Name] {
						bad = true
					}
				}
			})
			if bad {
				impure[name] = true
				changed = true
			}
		}
	}
	pure := make(map[string]bool, len(funcs))
	for name := range funcs {
		pure[name] = !impure[name]
	}
	return pure
}

// bodyImpure reports whether a function body directly touches a device, the
// stack, a data table, or a side-effecting/unknown call.
func bodyImpure(body *ast.BlockStmt, funcs map[string]*FuncInfo, devs map[string]string, data map[string]*DataTable) bool {
	bad := false
	walkBody(body,
		func(e ast.Expr) {
			switch x := e.(type) {
			case *ast.SelectorExpr:
				if isDeviceExpr(x.X, devs) {
					bad = true
				}
			case *ast.IndexExpr:
				if id, ok := x.X.(*ast.Ident); ok {
					if _, isData := data[id.Name]; isData {
						bad = true
					}
				}
				if isDeviceExpr(x.X, devs) {
					bad = true
				}
			case *ast.CallExpr:
				id, ok := x.Fun.(*ast.Ident)
				if !ok {
					bad = true
					return
				}
				if _, isFunc := funcs[id.Name]; isFunc {
					return // handled by the call-graph propagation
				}
				switch id.Name {
				case "str", "raw", "yield", "sleep", "hcf":
					bad = true
					return
				}
				if s := builtin.SemOf(id.Name); s.SideEffect || s.ReadsDev || s.WritesDev {
					bad = true
				}
			}
		},
		func(s ast.Stmt) {
			switch s.(type) {
			case *ast.LabelStmt, *ast.GotoStmt, *ast.CallStmt, *ast.RetStmt:
				bad = true
			}
		})
	return bad
}

func isDeviceExpr(e ast.Expr, devs map[string]string) bool {
	switch x := e.(type) {
	case *ast.DeviceLit:
		return true
	case *ast.Ident:
		_, ok := devs[x.Name]
		return ok
	}
	return false
}

// walkBody visits every expression and statement in a function body. The two
// callbacks are kept separate so a caller can match on one kind.
func walkBody(body *ast.BlockStmt, funcExpr func(ast.Expr), funcStmt func(ast.Stmt)) {
	var expr func(ast.Expr)
	var stmt func(ast.Stmt)
	expr = func(e ast.Expr) {
		if e == nil {
			return
		}
		funcExpr(e)
		switch v := e.(type) {
		case *ast.UnaryExpr:
			expr(v.X)
		case *ast.BinaryExpr:
			expr(v.X)
			expr(v.Y)
		case *ast.ParenExpr:
			expr(v.X)
		case *ast.CallExpr:
			expr(v.Fun)
			for _, a := range v.Args {
				expr(a)
			}
		case *ast.SelectorExpr:
			expr(v.X)
		case *ast.IndexExpr:
			expr(v.X)
			expr(v.Index)
		case *ast.TernaryExpr:
			expr(v.Cond)
			expr(v.Then)
			expr(v.Else)
		case *ast.RangeExpr:
			expr(v.Lo)
			expr(v.Hi)
		}
	}
	stmt = func(s ast.Stmt) {
		if s == nil {
			return
		}
		funcStmt(s)
		switch v := s.(type) {
		case *ast.BlockStmt:
			for _, st := range v.List {
				stmt(st)
			}
		case *ast.ExprStmt:
			expr(v.X)
		case *ast.AssignStmt:
			expr(v.Lhs)
			expr(v.Rhs)
		case *ast.IncDecStmt:
			expr(v.X)
		case *ast.IfStmt:
			stmt(v.Init)
			expr(v.Cond)
			stmt(v.Then)
			stmt(v.Else)
		case *ast.ForStmt:
			stmt(v.Init)
			expr(v.Cond)
			stmt(v.Post)
			stmt(v.Body)
		case *ast.RangeStmt:
			expr(v.X)
			stmt(v.Body)
		case *ast.SwitchStmt:
			stmt(v.Init)
			expr(v.Tag)
			for _, c := range v.Cases {
				for _, ce := range c.Exprs {
					expr(ce)
				}
				for _, cs := range c.Body {
					stmt(cs)
				}
			}
		case *ast.ReturnStmt:
			expr(v.Result)
		case *ast.DeclStmt:
			switch d := v.Decl.(type) {
			case *ast.VarDecl:
				expr(d.Value)
			case *ast.ConstDecl:
				expr(d.Value)
			}
		}
	}
	for _, s := range body.List {
		stmt(s)
	}
}

func walkBodyCalls(body *ast.BlockStmt, visit func(*ast.CallExpr)) {
	walkBody(body, func(e ast.Expr) {
		if c, ok := e.(*ast.CallExpr); ok {
			visit(c)
		}
	}, func(ast.Stmt) {})
}

// ---------------------------------------------------------------------------
// Deferred declaration evaluation
// ---------------------------------------------------------------------------

// pendingConst is a `const NAME = expr` whose value is evaluated after every
// declaration is known, so the expression may call user functions.
type pendingConst struct {
	name string
	expr ast.Expr
}

// pendingTable is a `data` table whose elements are evaluated after the consts.
type pendingTable struct {
	table *DataTable
	decl  *ast.DataDecl
}

// maxDataGen bounds a `data` comprehension so a huge range is an error rather
// than a stack overflow. The persistent stack is 512 slots.
const maxDataGen = StackSize

// evalPending resolves the deferred consts (in declaration order, so a const
// can reference an earlier one) and then the deferred data tables.
func evalPending(info *Info, diags *diag.Bag, consts []pendingConst, tables []pendingTable) {
	ev := &evalState{
		consts: info.Consts,
		funcs:  info.Funcs,
		devs:   info.Devices,
		data:   info.DataIndex,
	}
	for _, pc := range consts {
		v, ok := ev.expr(pc.expr)
		if !ok {
			diags.Errorf(pc.expr.Pos(), "constant %q is not a compile-time expression", pc.name)
			continue
		}
		info.Consts[pc.name] = v
	}
	for _, pt := range tables {
		if pt.decl.Comp != nil {
			vals, ok := ev.dataComp(pt.decl.Comp)
			if !ok {
				diags.Errorf(pt.decl.Comp.Pos(), "data table %q is not a compile-time expression", pt.decl.Name.Name)
				continue
			}
			pt.table.Values = append(pt.table.Values, vals...)
			continue
		}
		for _, e := range pt.decl.Values {
			lit, ok := ev.dataLiteral(e)
			if !ok {
				diags.Errorf(e.Pos(), "data element is not a compile-time expression")
				continue
			}
			pt.table.Values = append(pt.table.Values, lit)
		}
	}
}

// dataLiteral renders a data/table element as an IC10 literal: a number, or a
// game enum name such as LogicType.Open (emitted verbatim, resolved by the game
// assembler).
func (ev *evalState) dataLiteral(e ast.Expr) (string, bool) {
	if v, ok := ev.expr(e); ok {
		return formatDataValue(v), true
	}
	if sel, ok := e.(*ast.SelectorExpr); ok {
		if id, ok := sel.X.(*ast.Ident); ok {
			return id.Name + "." + sel.Sel.Name, true
		}
	}
	return "", false
}

// dataComp evaluates a `data` comprehension `[ expr for i in lo..hi ]` at
// compile time. The bounds must fold to integers and the result must fit the
// persistent stack.
func (ev *evalState) dataComp(c *ast.DataComp) ([]string, bool) {
	lo, ok := ev.expr(c.Lo)
	if !ok {
		return nil, false
	}
	hi, ok := ev.expr(c.Hi)
	if !ok {
		return nil, false
	}
	if lo != math.Trunc(lo) || hi != math.Trunc(hi) || hi < lo || hi-lo+1 > maxDataGen {
		return nil, false
	}
	ev.scope = append(ev.scope, map[string]float64{})
	defer func() { ev.scope = ev.scope[:len(ev.scope)-1] }()

	out := make([]string, 0, int(hi-lo)+1)
	for i := lo; i <= hi; i++ {
		ev.scope[len(ev.scope)-1][c.Var.Name] = i
		lit, ok := ev.dataLiteral(c.Expr)
		if !ok {
			return nil, false
		}
		out = append(out, lit)
	}
	return out, true
}

// EvalRaw evaluates an expression to a raw IC10 constant: str("...") for a
// display string, or raw("...") for a verbatim operand (the escape hatch for
// game constants the compiler does not know).
func EvalRaw(e ast.Expr) (string, bool) {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	id, ok := call.Fun.(*ast.Ident)
	if !ok || len(call.Args) != 1 {
		return "", false
	}
	s, ok := evalString(call.Args[0])
	if !ok {
		return "", false
	}
	switch id.Name {
	case "str":
		return "STR(" + strconv.Quote(s) + ")", true
	case "raw":
		return s, true
	}
	return "", false
}

// evalString folds a compile-time string expression: a literal, or a `+`
// concatenation of them. It is what lets `hash("a" + "b")` and `str("a" + "b")`
// fold without a runtime string type.
func evalString(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.StringLit:
		return x.Value, true
	case *ast.ParenExpr:
		return evalString(x.X)
	case *ast.BinaryExpr:
		if x.Op != token.Plus {
			return "", false
		}
		a, ok := evalString(x.X)
		if !ok {
			return "", false
		}
		b, ok := evalString(x.Y)
		if !ok {
			return "", false
		}
		return a + b, true
	}
	return "", false
}

func boolToNum(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// ic10Mod matches IC10's mod instruction: the result sign follows the divisor.
func ic10Mod(x, y float64) float64 {
	r := math.Mod(x, y)
	if r != 0 && (r < 0) != (y < 0) {
		r += y
	}
	return r
}

// deviceAlias reports whether e is a device literal (d0..d5, db) or a name that
// already aliases one, and returns the device name.
func deviceAlias(e ast.Expr, devices map[string]string) (string, bool) {
	if d, ok := e.(*ast.DeviceLit); ok {
		return d.Name, true
	}
	if id, ok := e.(*ast.Ident); ok {
		if dev, ok := devices[id.Name]; ok {
			return dev, true
		}
	}
	return "", false
}

// isDevicePort reports whether s is a literal device port (db or d0..d5).
func isDevicePort(s string) bool {
	if s == "db" {
		return true
	}
	return len(s) == 2 && s[0] == 'd' && s[1] >= '0' && s[1] <= '5'
}
