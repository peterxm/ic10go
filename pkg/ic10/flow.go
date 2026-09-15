package ic10

import (
	"ic10go/internal/ast"
	"ic10go/internal/diag"
	"ic10go/internal/lexer"
	"ic10go/internal/parser"
	"ic10go/internal/sema"
	"ic10go/internal/source"
)

// Flow parses and checks src and returns its AST. It backs the source-level
// control-flow graph (`ic10c graph --level source`).
func Flow(name string, src []byte, opts Options) (*ast.File, *diag.Bag, error) {
	file := source.NewFile(name, src)
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)
	tree := parser.Parse(file, toks, diags)
	if diags.HasErrors() {
		return nil, diags, nil
	}
	sema.CheckWithOptions(tree, diags, sema.Options{
		FixedDataBase: fixedDataBase(opts),
		AutoTable:     opts.AutoTable,
	})
	if diags.HasErrors() {
		return nil, diags, nil
	}
	return tree, diags, nil
}
