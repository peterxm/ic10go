// Package ic10 is the public interface to the .icg compiler.
package ic10

import (
	"fmt"
	"os"
	"strings"

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
	// JumpTable lowers dense integer switches (>=8 cases) to a computed jump
	// through a table of `j` instructions, saving about one line per case.
	// Off by default.
	JumpTable bool
	// Fast prefers runtime speed over size: it unrolls more loops (at the cost
	// of program size). Off by default.
	Fast bool
	// RelJump emits relative jumps (jr / br*) instead of absolute ones to save
	// bytes. Off by default; requires the game's relative-jump base to match
	// the VM (relative to the jump's own line).
	RelJump bool
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
//
// It compiles twice when outlining is possible (once inlined, once with the
// repeated functions outlined) and keeps the shorter result. Inlining exposes
// constant folding and CSE across call sites; outlining saves lines when a
// function is called several times, so the better choice depends on the
// program.
func CompileWithOptions(name string, src []byte, opts Options) (string, *diag.Bag, error) {
	info, diags := parseAndCheck(name, src, opts)
	if info == nil || diags.HasErrors() {
		return "", diags, nil
	}

	noCheck, noOutline, noOpt := envSwitches()
	plan := lower.PlanOutlines(info, noOutline)
	best := ""
	var bestErr error
	run := func(outline map[string]bool) {
		fn := lowerAndOptimize(info, opts, outline, noCheck, noOpt, diags)
		if fn == nil || diags.HasErrors() {
			return
		}
		code, err := generate(fn, info, opts)
		if err != nil {
			bestErr = err
			return
		}
		if best == "" || better(code, best) {
			best, bestErr = code, nil
		}
	}
	run(nil)
	if len(plan) > 0 {
		run(plan)
	}
	if best == "" {
		return "", diags, bestErr
	}
	return best, diags, nil
}

// better reports whether candidate a is preferable to b: fewer lines first
// (the tighter IC10 budget), then fewer bytes.
func better(a, b string) bool {
	la, lb := strings.Count(a, "\n"), strings.Count(b, "\n")
	if la != lb {
		return la < lb
	}
	return len(a) < len(b)
}

// generate runs register allocation and code generation for a lowered function.
func generate(fn *ir.Function, info *sema.Info, opts Options) (string, error) {
	code, _, err := generateColored(fn, info, opts)
	return code, err
}

// generateColored is generate plus the register colouring, which the
// control-flow graph needs to render instruction text.
func generateColored(fn *ir.Function, info *sema.Info, opts Options) (string, map[*ir.Reg]int, error) {
	reserved := info.DataSize
	if fixedDataBase(opts) > 0 {
		reserved = 0
	}
	colors, spillCount, err := regalloc.AllocateReservedSpills(fn, NumRegs, reserved)
	if err != nil {
		return "", nil, err
	}
	if fixedDataBase(opts) > 0 && spillCount > 0 {
		dataEnd := info.Sentinel + info.DataSize - 1
		if bottom := sema.StackSize - spillCount; bottom <= dataEnd {
			return "", nil, fmt.Errorf("register spills (%d slots, down to %d) overlap the data segment [%d..%d]",
				spillCount, bottom, info.Sentinel, dataEnd)
		}
	}
	if opt.MergeTailsColored(fn, colors) {
		fn.BuildCFG()
	}
	code, err := codegen.GenerateWithOptions(fn, colors, codegen.Options{RelJump: opts.RelJump})
	return code, colors, err
}

// parseAndCheck lexes, parses and type-checks the source. It returns a nil Info
// when there are errors.
func parseAndCheck(name string, src []byte, opts Options) (*sema.Info, *diag.Bag) {
	file := source.NewFile(name, src)
	diags := &diag.Bag{}

	toks := lexer.Tokenize(file, diags)
	tree := parser.Parse(file, toks, diags)
	if diags.HasErrors() {
		return nil, diags
	}

	info := sema.CheckWithOptions(tree, diags, sema.Options{
		FixedDataBase: fixedDataBase(opts),
		AutoTable:     opts.AutoTable,
	})
	if info.Main == nil {
		diags.ErrorfCode("no-main", source.Pos{File: name, Line: 1, Col: 1}, "no main function found")
	}
	if info.DataSize > 0 && info.Sentinel+info.DataSize > sema.StackSize {
		diags.ErrorfCode("data-too-large", source.Pos{File: name, Line: 1, Col: 1},
			"data segment [%d..%d] exceeds the %d-slot stack", info.Sentinel, info.Sentinel+info.DataSize-1, sema.StackSize)
	}
	if diags.HasErrors() {
		return nil, diags
	}
	return info, diags
}

// envSwitches reads the legacy environment switches once at the public API
// boundary. Compiler internals take explicit options.
func envSwitches() (noCheck, noOutline, noOpt bool) {
	return os.Getenv("IC10C_NO_CHECK") != "",
		os.Getenv("IC10C_NO_OUTLINE") != "",
		os.Getenv("IC10C_NO_OPT") != ""
}

// lowerAndOptimize lowers a checked program to IR and runs the optimiser.
func lowerAndOptimize(info *sema.Info, opts Options, outline map[string]bool, noCheck, noOpt bool, diags *diag.Bag) *ir.Function {
	fn := lower.Lower(info, diags, lower.Options{
		StableInsOrder:  opts.StableInsOrder,
		DataCheck:       !opts.NoDataCheck && !opts.Unsafe,
		DataAccessStack: opts.DataAccessStack,
		Outline:         outline,
		JumpTable:       opts.JumpTable,
		Fast:            opts.Fast,
		NoCheck:         noCheck,
	})
	if diags.HasErrors() {
		return nil
	}
	if !noOpt {
		if err := opt.Optimize(fn); err != nil {
			diags.Errorf(info.Main.Pos(), "internal error: %v", err)
			return nil
		}
	}
	return fn
}
