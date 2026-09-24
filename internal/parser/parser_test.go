package parser

import (
	"strings"
	"testing"

	"ic10go/internal/ast"
	"ic10go/internal/diag"
	"ic10go/internal/lexer"
	"ic10go/internal/source"
	"ic10go/internal/token"
)

func parse(t *testing.T, src string) (*ast.File, *diag.Bag) {
	t.Helper()
	file := source.NewFile("test.icg", []byte(src))
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)
	tree := Parse(file, toks, diags)
	return tree, diags
}

func TestGroupedConst(t *testing.T) {
	tree, diags := parse(t, "const (\n A = 1\n B = 2\n)\n")
	if diags.HasErrors() {
		t.Fatalf("errors: %v", diags.Diags)
	}
	if len(tree.Decls) != 2 {
		t.Fatalf("decls = %d, want 2", len(tree.Decls))
	}
}

func TestPrecedence(t *testing.T) {
	tree, diags := parse(t, "func f() { x := 1 + 2 * 3 }\n")
	if diags.HasErrors() {
		t.Fatalf("errors: %v", diags.Diags)
	}
	fn := tree.Decls[0].(*ast.FuncDecl)
	assign := fn.Body.List[0].(*ast.AssignStmt)
	bin := assign.Rhs.(*ast.BinaryExpr)
	if bin.Op != token.Plus {
		t.Fatalf("top op = %v, want +", bin.Op)
	}
	if _, ok := bin.Y.(*ast.BinaryExpr); !ok {
		t.Fatalf("rhs should be a binary expression, got %T", bin.Y)
	}
}

func TestDeviceAssignment(t *testing.T) {
	tree, diags := parse(t, "func f() { d0.On = d1.Temperature > 300 }\n")
	if diags.HasErrors() {
		t.Fatalf("errors: %v", diags.Diags)
	}
	fn := tree.Decls[0].(*ast.FuncDecl)
	assign := fn.Body.List[0].(*ast.AssignStmt)
	sel, ok := assign.Lhs.(*ast.SelectorExpr)
	if !ok {
		t.Fatalf("lhs = %T, want *SelectorExpr", assign.Lhs)
	}
	if sel.Sel.Name != "On" {
		t.Fatalf("selector = %q, want On", sel.Sel.Name)
	}
}

func TestSlotChain(t *testing.T) {
	tree, diags := parse(t, "func f() { d2.slot[0].Mature = 1 }\n")
	if diags.HasErrors() {
		t.Fatalf("errors: %v", diags.Diags)
	}
	fn := tree.Decls[0].(*ast.FuncDecl)
	assign := fn.Body.List[0].(*ast.AssignStmt)
	sel, ok := assign.Lhs.(*ast.SelectorExpr)
	if !ok {
		t.Fatalf("lhs = %T, want *SelectorExpr", assign.Lhs)
	}
	if _, ok := sel.X.(*ast.IndexExpr); !ok {
		t.Fatalf("selector base = %T, want *IndexExpr", sel.X)
	}
}

func TestForThreePart(t *testing.T) {
	tree, diags := parse(t, "func f() { for i := 0; i < 10; i++ { d0.On = 1 } }\n")
	if diags.HasErrors() {
		t.Fatalf("errors: %v", diags.Diags)
	}
	fn := tree.Decls[0].(*ast.FuncDecl)
	loop := fn.Body.List[0].(*ast.ForStmt)
	if loop.Init == nil || loop.Cond == nil || loop.Post == nil {
		t.Fatalf("for clause incomplete: %+v", loop)
	}
}

func TestParseErrors(t *testing.T) {
	_, diags := parse(t, "func f( { }\n")
	if !diags.HasErrors() {
		t.Fatal("expected errors")
	}
}

func TestForRange(t *testing.T) {
	cases := []struct {
		src   string
		key   string
		value string
	}{
		{"func f() { for i := range 5 {} }\n", "i", ""},
		{"func f() { for i := range T {} }\n", "i", ""},
		{"func f() { for i, v := range T {} }\n", "i", "v"},
		{"func f() { for _, v := range T {} }\n", "_", "v"},
	}
	for _, c := range cases {
		tree, diags := parse(t, c.src)
		if diags.HasErrors() {
			t.Fatalf("%q: errors: %v", c.src, diags.Diags)
		}
		fn := tree.Decls[0].(*ast.FuncDecl)
		r, ok := fn.Body.List[0].(*ast.RangeStmt)
		if !ok {
			t.Fatalf("%q: stmt = %T, want *ast.RangeStmt", c.src, fn.Body.List[0])
		}
		if r.Key.Name != c.key {
			t.Errorf("%q: key = %q, want %q", c.src, r.Key.Name, c.key)
		}
		if c.value == "" && r.Value != nil {
			t.Errorf("%q: value = %q, want nil", c.src, r.Value.Name)
		}
		if c.value != "" && (r.Value == nil || r.Value.Name != c.value) {
			t.Errorf("%q: value = %v, want %q", c.src, r.Value, c.value)
		}
	}
}

func TestForThreePartStillParses(t *testing.T) {
	tree, diags := parse(t, "func f() { for i := 0; i < 10; i++ {} }\n")
	if diags.HasErrors() {
		t.Fatalf("errors: %v", diags.Diags)
	}
	fn := tree.Decls[0].(*ast.FuncDecl)
	if _, ok := fn.Body.List[0].(*ast.ForStmt); !ok {
		t.Fatalf("stmt = %T, want *ast.ForStmt", fn.Body.List[0])
	}
}

func TestCaseRange(t *testing.T) {
	tree, diags := parse(t, "func f() { switch x { case 1..5: d0.On = 1\n case 6, 7: d0.On = 0 } }\n")
	if diags.HasErrors() {
		t.Fatalf("errors: %v", diags.Diags)
	}
	fn := tree.Decls[0].(*ast.FuncDecl)
	sw := fn.Body.List[0].(*ast.SwitchStmt)
	if _, ok := sw.Cases[0].Exprs[0].(*ast.RangeExpr); !ok {
		t.Fatalf("case 0 expr = %T, want *ast.RangeExpr", sw.Cases[0].Exprs[0])
	}
	if len(sw.Cases[1].Exprs) != 2 {
		t.Fatalf("case 1 exprs = %d, want 2", len(sw.Cases[1].Exprs))
	}
}

func TestIfAndSwitchInit(t *testing.T) {
	tree, diags := parse(t, "func f() { if x := 1; x > 0 { d0.On = 1 } }\n")
	if diags.HasErrors() {
		t.Fatalf("if init errors: %v", diags.Diags)
	}
	fn := tree.Decls[0].(*ast.FuncDecl)
	ifs := fn.Body.List[0].(*ast.IfStmt)
	if ifs.Init == nil {
		t.Fatal("if init = nil, want a statement")
	}

	tree, diags = parse(t, "func f() { switch y := 1; y { case 1: d0.On = 1 } }\n")
	if diags.HasErrors() {
		t.Fatalf("switch init errors: %v", diags.Diags)
	}
	fn = tree.Decls[0].(*ast.FuncDecl)
	sw := fn.Body.List[0].(*ast.SwitchStmt)
	if sw.Init == nil {
		t.Fatal("switch init = nil, want a statement")
	}
	if sw.Tag == nil {
		t.Fatal("switch tag = nil, want an expression")
	}
}

func TestLabeledBreakContinue(t *testing.T) {
	tree, diags := parse(t, "func f() { for { break Outer\n continue Inner } }\n")
	if diags.HasErrors() {
		t.Fatalf("errors: %v", diags.Diags)
	}
	fn := tree.Decls[0].(*ast.FuncDecl)
	loop := fn.Body.List[0].(*ast.ForStmt)
	br := loop.Body.List[0].(*ast.BreakStmt)
	if br.Label == nil || br.Label.Name != "Outer" {
		t.Fatalf("break label = %v, want Outer", br.Label)
	}
	co := loop.Body.List[1].(*ast.ContinueStmt)
	if co.Label == nil || co.Label.Name != "Inner" {
		t.Fatalf("continue label = %v, want Inner", co.Label)
	}
}

func TestAssignMissingRHS(t *testing.T) {
	cases := []struct {
		name string
		src  string
		line int
	}{
		{"assign", "func main() {\n    d0.Mode =\n    for {\n        yield()\n    }\n}\n", 2},
		{"define", "func main() {\n    x :=\n}\n", 2},
		{"var", "var g =\nfunc main() {}\n", 1},
		{"const", "const C =\nfunc main() {}\n", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, diags := parse(t, c.src)
			if !diags.HasErrors() {
				t.Fatal("expected an error")
			}
			d := diags.Diags[0]
			if !strings.Contains(d.Msg, "expected expression after") {
				t.Errorf("msg = %q, want missing-RHS message", d.Msg)
			}
			if d.Pos.Line != c.line {
				t.Errorf("error reported on line %d, want %d", d.Pos.Line, c.line)
			}
		})
	}
}

func TestParseUnits(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		// temperature -> K
		{"20c", 293.15},
		{"68f", 293.15},
		{"300k", 300},
		{"0c", 273.15},
		{"32F", 273.15},
		{"1.5C", 274.65},
		// pressure -> kPa
		{"20.1MPa", 20100},
		{"101.3kPa", 101.3},
		{"101325Pa", 101.325},
		{"1bar", 100},
		{"1MPa", 1000},
		// power -> W
		{"1.5kW", 1500},
		{"2MW", 2e6},
		{"3W", 3},
		// time -> s
		{"500ms", 0.5},
		{"2min", 120},
		{"1h", 3600},
		{"90s", 90},
		// angle: no conversion
		{"180deg", 180},
		{"1rad", 1},
		// ratio
		{"50%", 0.5},
		{"30pct", 0.3},
		// plain numbers
		{"42", 42},
		{"0x1f", 31},
		{"0b10", 2},
	}
	for _, c := range cases {
		if got := parseNumber(c.in); got != c.want {
			t.Errorf("parseNumber(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseImport(t *testing.T) {
	tree, diags := parse(t, "import \"lib.icg\"\n")
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %+v", diags.Diags)
	}
	if len(tree.Decls) != 1 {
		t.Fatalf("decls = %d, want 1", len(tree.Decls))
	}
	imp, ok := tree.Decls[0].(*ast.ImportDecl)
	if !ok {
		t.Fatalf("decl = %T, want *ast.ImportDecl", tree.Decls[0])
	}
	if imp.Path.Value != "lib.icg" {
		t.Errorf("path = %q, want lib.icg", imp.Path.Value)
	}
}

func TestParseDataComprehension(t *testing.T) {
	tree, diags := parse(t, "data T = [i * 2 for i in 0..9]\n")
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %+v", diags.Diags)
	}
	d, ok := tree.Decls[0].(*ast.DataDecl)
	if !ok || d.Comp == nil {
		t.Fatalf("decl = %#v, want a comprehension data table", tree.Decls[0])
	}
	if d.Comp.Var.Name != "i" {
		t.Errorf("comprehension variable = %q, want i", d.Comp.Var.Name)
	}
}

func TestParseMultiReturn(t *testing.T) {
	tree, diags := parse(t, "func f() (num, num) { return 1, 2 }\n")
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %+v", diags.Diags)
	}
	fd, ok := tree.Decls[0].(*ast.FuncDecl)
	if !ok || len(fd.Results) != 2 {
		t.Fatalf("decl = %#v, want two results", tree.Decls[0])
	}
	r, ok := fd.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(r.Results) != 2 {
		t.Fatalf("return = %#v, want two results", fd.Body.List[0])
	}
}
