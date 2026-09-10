package ic10

import (
	"ic10go/internal/ast"
	"ic10go/internal/diag"
	"ic10go/internal/lexer"
	"ic10go/internal/parser"
	"ic10go/internal/source"
)

// Format parses and re-prints .icg source in canonical form.
func Format(name string, src []byte) (string, *diag.Bag, error) {
	file := source.NewFile(name, src)
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)
	tree := parser.Parse(file, toks, diags)
	if diags.HasErrors() {
		return "", diags, nil
	}
	return ast.Format(tree), diags, nil
}
