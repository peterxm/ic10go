package ast_test

// Regression test for docs/review-2026-10-14.md L2: the printer rendered
// adjacent unary minuses as `--5`, which does not re-parse (it reads as a
// decrement of a literal). It must keep a separator, be idempotent, and the
// output must re-parse to the same value.

import (
	"strings"
	"testing"

	"ic10go/internal/ast"
	"ic10go/internal/diag"
	"ic10go/internal/lexer"
	"ic10go/internal/parser"
	"ic10go/internal/source"
)

func parsePrinterSrc(t *testing.T, src string) *ast.File {
	t.Helper()
	file := source.NewFile("test.icg", []byte(src))
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)
	tree := parser.Parse(file, toks, diags)
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags.Diags)
	}
	return tree
}

func TestPrinterKeepsUnarySignsReparseable(t *testing.T) {
	const src = "func main() {\n\td0.Setting = - -5\n\td1.Setting = -x\n\td2.On = 1\n}\n"
	first := ast.Format(parsePrinterSrc(t, src))
	if !strings.Contains(first, "- -5") {
		t.Errorf("output must keep a separator between unary signs:\n%s", first)
	}
	if strings.Contains(first, "--5") {
		t.Errorf("output must not contain `--5` (it does not re-parse):\n%s", first)
	}
	// Idempotent: formatting the formatted text gives the same text.
	second := ast.Format(parsePrinterSrc(t, first))
	if first != second {
		t.Errorf("formatting is not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	// And the output re-parses (a `--5` would fail here).
	parsePrinterSrc(t, first)
}
