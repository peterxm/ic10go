// Package ic10 is the public interface to the .icg compiler.
package ic10

import (
	"os"

	"ic10go/internal/codegen"
	"ic10go/internal/diag"
	"ic10go/internal/lexer"
	"ic10go/internal/lower"
	"ic10go/internal/opt"
	"ic10go/internal/parser"
	"ic10go/internal/regalloc"
	"ic10go/internal/sema"
	"ic10go/internal/source"
)

// NumRegs is the number of general-purpose IC10 CPU registers.
const NumRegs = 16

// Options controls compilation.
type Options struct {
	// StableInsOrder emits IC10 "ins" with the argument order used by the
	// stable game branch (offset length field) instead of the documented
	// (field offset length). The beta branch uses the documented order.
	StableInsOrder bool
	// NoDataCheck disables the runtime check that the persistent data segment
	// is installed before main runs.
	NoDataCheck bool
}

// Compile compiles .icg source into IC10 code.
//
// On success it returns the generated code and a (possibly non-empty) bag of
// warnings. If the source has errors, the returned diagnostics contain them and
// code is empty. A non-nil error indicates a backend failure such as exceeding
// the IC10 limits.
func Compile(name string, src []byte) (string, *diag.Bag, error) {
	return CompileWithOptions(name, src, Options{})
}

// CompileWithOptions is Compile with explicit options.
func CompileWithOptions(name string, src []byte, opts Options) (string, *diag.Bag, error) {
	file := source.NewFile(name, src)
	diags := &diag.Bag{}

	toks := lexer.Tokenize(file, diags)
	tree := parser.Parse(file, toks, diags)
	if diags.HasErrors() {
		return "", diags, nil
	}

	info := sema.Check(tree, diags)
	if info.Main == nil {
		diags.Errorf(source.Pos{File: name, Line: 1, Col: 1}, "no main function found")
	}
	if diags.HasErrors() {
		return "", diags, nil
	}

	fn := lower.Lower(info, diags, lower.Options{
		StableInsOrder: opts.StableInsOrder,
		DataCheck:      !opts.NoDataCheck,
	})
	if diags.HasErrors() {
		return "", diags, nil
	}

	if os.Getenv("IC10C_NO_OPT") == "" {
		opt.Optimize(fn)
	}
	colors, err := regalloc.AllocateReserved(fn, NumRegs, info.DataSize)
	if err != nil {
		return "", diags, err
	}

	code, err := codegen.Generate(fn, colors)
	if err != nil {
		return "", diags, err
	}
	return code, diags, nil
}
