// Package ic10 is the public interface to the .icg compiler.
package ic10

import (
	"fmt"
	"os"

	"ic10go/internal/codegen"
	"ic10go/internal/diag"
	"ic10go/internal/ir"
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
	// Unsafe enables risky size optimisations. Currently it implies
	// NoDataCheck: the runtime no longer verifies the data segment, so the
	// program must be paired with a loader that was run first.
	Unsafe bool
	// DataAccessStack reads/writes the data segment through the local stack
	// (poke/peek) instead of get/put db, so it also works on a device host.
	DataAccessStack bool
	// DataLayout selects the data-segment placement: "" or "top" puts it at
	// the top of the stack (spills below it); "middle" puts it at a fixed
	// middle slot (sema.FixedDataBase) and leaves the high slots for spills.
	DataLayout string
	// AutoTable tables eligible plain switches into the data segment, without
	// needing the `table` marker. Off by default; the runtime then needs the
	// data loader installed.
	AutoTable bool
}

// fixedDataBase returns the fixed data base for the selected layout, or 0 for
// the default top-of-stack layout.
func fixedDataBase(opts Options) int {
	if opts.DataLayout == "middle" {
		return sema.FixedDataBase
	}
	return 0
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
	fn, info, diags := compileIR(name, src, opts)
	if fn == nil {
		return "", diags, nil
	}
	reserved := info.DataSize
	if fixedDataBase(opts) > 0 {
		reserved = 0
	}
	colors, spillCount, err := regalloc.AllocateReservedSpills(fn, NumRegs, reserved)
	if err != nil {
		return "", diags, err
	}
	if fixedDataBase(opts) > 0 && spillCount > 0 {
		dataEnd := info.Sentinel + info.DataSize - 1
		if bottom := sema.StackSize - spillCount; bottom <= dataEnd {
			return "", diags, fmt.Errorf("register spills (%d slots, down to %d) overlap the data segment [%d..%d]",
				spillCount, bottom, info.Sentinel, dataEnd)
		}
	}

	code, err := codegen.Generate(fn, colors)
	if err != nil {
		return "", diags, err
	}
	return code, diags, nil
}

// compileIR parses, checks and lowers the source to IR. When diags.HasErrors()
// the returned function is nil.
func compileIR(name string, src []byte, opts Options) (*ir.Function, *sema.Info, *diag.Bag) {
	file := source.NewFile(name, src)
	diags := &diag.Bag{}

	toks := lexer.Tokenize(file, diags)
	tree := parser.Parse(file, toks, diags)
	if diags.HasErrors() {
		return nil, nil, diags
	}

	info := sema.CheckWithOptions(tree, diags, sema.Options{
		FixedDataBase: fixedDataBase(opts),
		AutoTable:     opts.AutoTable,
	})
	if info.Main == nil {
		diags.Errorf(source.Pos{File: name, Line: 1, Col: 1}, "no main function found")
	}
	if info.DataSize > 0 && info.Sentinel+info.DataSize > sema.StackSize {
		diags.Errorf(source.Pos{File: name, Line: 1, Col: 1},
			"data segment [%d..%d] exceeds the %d-slot stack", info.Sentinel, info.Sentinel+info.DataSize-1, sema.StackSize)
	}
	if diags.HasErrors() {
		return nil, nil, diags
	}

	fn := lower.Lower(info, diags, lower.Options{
		StableInsOrder:  opts.StableInsOrder,
		DataCheck:       !opts.NoDataCheck && !opts.Unsafe,
		DataAccessStack: opts.DataAccessStack,
	})
	if diags.HasErrors() {
		return nil, info, diags
	}

	if os.Getenv("IC10C_NO_OPT") == "" {
		opt.Optimize(fn)
	}
	return fn, info, diags
}
