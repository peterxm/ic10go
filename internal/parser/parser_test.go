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
