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
	"ic10go/internal/token"
)

// StackSize is the number of slots in an IC10 chip's persistent stack.
const StackSize = 512

type FuncInfo struct {
	Decl   *ast.FuncDecl
	Params []string
}

// DataTable is a compile-time `data` table stored in the persistent stack.
type DataTable struct {
	Name   string
	Values []float64
	Base   int // first stack slot
}

type Info struct {
	Consts    map[string]float64
	RawConsts map[string]string
	Devices   map[string]string // const NAME = dN (device alias)
	Funcs     map[string]*FuncInfo
	Main      *ast.FuncDecl

	// Data segment (top-level `data` tables). All zero when there is none.
	Data        []*DataTable
	DataIndex   map[string]*DataTable
	DataSize    int     // sentinel (if any) + all elements
	Sentinel    int     // stack slot holding the version, -1 if no data
	DataVersion float64 // version written by the loader and checked at runtime
}

// Check resolves declarations and evaluates constants.
func Check(file *ast.File, diags *diag.Bag) *Info {
	info := &Info{
		Consts:    map[string]float64{},
		RawConsts: map[string]string{},
		Devices:   map[string]string{},
		Funcs:     map[string]*FuncInfo{},
		DataIndex: map[string]*DataTable{},
		Sentinel:  -1,
	}

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
			v, ok := Eval(d.Value, info.Consts)
			if !ok {
				diags.Errorf(d.Value.Pos(), "constant %q is not a compile-time expression", d.Name.Name)
				continue
			}
			info.Consts[d.Name.Name] = v
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
			t := &DataTable{Name: d.Name.Name}
			for _, v := range d.Values {
				c, ok := Eval(v, info.Consts)
				if !ok {
					diags.Errorf(v.Pos(), "data element is not a compile-time expression")
					continue
				}
				t.Values = append(t.Values, c)
			}
			info.Data = append(info.Data, t)
			info.DataIndex[d.Name.Name] = t
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
		}
	}

	assignData(info)
	return info
}

// assignData lays the data tables out at the top of the stack (below the
// register-spill region) and derives the version sentinel.
func assignData(info *Info) {
	if len(info.Data) == 0 {
		return
	}
	size := 1 // version sentinel
	for _, t := range info.Data {
		size += len(t.Values)
	}
	info.DataSize = size
	base := StackSize - size
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
			fmt.Fprintf(h, ",%v", v)
		}
	}
	return float64(int32(h.Sum32()))
}

// Eval evaluates an expression to a compile-time constant. It returns false if
// the expression is not constant.
func Eval(e ast.Expr, consts map[string]float64) (float64, bool) {
	switch e := e.(type) {
	case *ast.NumberLit:
		return e.Value, true
	case *ast.BoolLit:
		if e.Value {
			return 1, true
		}
		return 0, true
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
		return Eval(e.X, consts)
	case *ast.Ident:
		v, ok := consts[e.Name]
		return v, ok
	case *ast.UnaryExpr:
		x, ok := Eval(e.X, consts)
		if !ok {
			return 0, false
		}
		switch e.Op {
		case token.Minus:
			return -x, true
		case token.Plus:
			return x, true
		case token.Not:
			if x == 0 {
				return 1, true
			}
			return 0, true
		case token.Tilde:
			return float64(^int64(x)), true
		}
		return 0, false
	case *ast.BinaryExpr:
		return evalBinary(e, consts)
	case *ast.TernaryExpr:
		c, ok := Eval(e.Cond, consts)
		if !ok {
			return 0, false
		}
		if c != 0 {
			return Eval(e.Then, consts)
		}
		return Eval(e.Else, consts)
	case *ast.CallExpr:
		return evalCall(e, consts)
	}
	return 0, false
}

// EvalRaw evaluates an expression to a raw IC10 constant such as STR("...").
func EvalRaw(e ast.Expr) (string, bool) {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	id, ok := call.Fun.(*ast.Ident)
	if !ok || id.Name != "str" || len(call.Args) != 1 {
		return "", false
	}
	s, ok := call.Args[0].(*ast.StringLit)
	if !ok {
		return "", false
	}
	return "STR(" + strconv.Quote(s.Value) + ")", true
}

func evalBinary(e *ast.BinaryExpr, consts map[string]float64) (float64, bool) {
	x, ok := Eval(e.X, consts)
	if !ok {
		return 0, false
	}
	// Short-circuit logical operators.
	if e.Op == token.And {
		if x == 0 {
			return 0, true
		}
		y, ok := Eval(e.Y, consts)
		if !ok {
			return 0, false
		}
		return boolToNum(y != 0), true
	}
	if e.Op == token.Or {
		if x != 0 {
			return 1, true
		}
		y, ok := Eval(e.Y, consts)
		if !ok {
			return 0, false
		}
		return boolToNum(y != 0), true
	}
	y, ok := Eval(e.Y, consts)
	if !ok {
		return 0, false
	}
	switch e.Op {
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

func evalCall(e *ast.CallExpr, consts map[string]float64) (float64, bool) {
	id, ok := e.Fun.(*ast.Ident)
	if !ok {
		return 0, false
	}
	// hash("...") is a compile-time CRC-32; handle before evaluating args.
	if id.Name == "hash" {
		if len(e.Args) == 1 {
			if s, ok := e.Args[0].(*ast.StringLit); ok {
				return float64(int32(builtin.Hash(s.Value))), true
			}
		}
		return 0, false
	}
	args := make([]float64, len(e.Args))
	for i, a := range e.Args {
		v, ok := Eval(a, consts)
		if !ok {
			return 0, false
		}
		args[i] = v
	}
	switch id.Name {
	case "abs":
		return math.Abs(args[0]), len(args) == 1
	case "sqrt":
		return math.Sqrt(args[0]), len(args) == 1
	case "floor":
		return math.Floor(args[0]), len(args) == 1
	case "ceil":
		return math.Ceil(args[0]), len(args) == 1
	case "round":
		return math.Round(args[0]), len(args) == 1
	case "trunc":
		return math.Trunc(args[0]), len(args) == 1
	case "pow":
		return math.Pow(args[0], args[1]), len(args) == 2
	case "min":
		return math.Min(args[0], args[1]), len(args) == 2
	case "max":
		return math.Max(args[0], args[1]), len(args) == 2
	}
	return 0, false
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
