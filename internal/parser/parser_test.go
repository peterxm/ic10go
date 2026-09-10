package parser

import (
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
