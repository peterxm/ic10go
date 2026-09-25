// Package ic10 is the public interface to the .icg compiler.
package ic10

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"ic10go/internal/ast"
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

	// SpillStack keeps register spills in the IC stack with a peek/poke
	// save-restore sequence (5 lines per load, reserves r15 as scratch). The
	// default is false: spills use get/put db (1 line per load, no scratch).
	SpillStack bool

	// DynamicStack sizes the user stack region from the compiler's actual
	// data/spill usage instead of the fixed UserStackLimit. Off by default: the
	// user region is the low UserStackLimit slots. IC10C_DYNAMIC_STACK=1 turns
	// it on.
	DynamicStack bool
	// UserStackLimit is the fixed user stack size. 0 means DefaultUserStack.
	// IC10C_USER_STACK overrides it.
	UserStackLimit int
	// NoFoldDataReads disables folding constant-index data-table reads to
	// literals. Off by default; the compiler still compares both variants and
	// keeps the shorter runtime.
	NoFoldDataReads bool
	// PrivateStack marks the user stack slots as private to this program, so
	// the optimizer may keep them in registers. Set automatically: single-chip
	// programs default to private, multi-chip to shared, and the
	// `// icg: private-stack` / `// icg: shared-stack` pragma overrides.
	PrivateStack bool
	// NoMem2Reg disables user-stack promotion. Off by default; the compiler
	// compares both variants when the source has constant user slots and keeps
	// the shorter runtime.
	NoMem2Reg bool
	// RedundantDeviceWrites removes a constant device write that repeats the
	// previous write to the same device+logic, saving lines but changing the
	// observable write sequence. Off by default.
	// IC10C_REDUNDANT_DEVICE_WRITES=1 also turns it on.
	RedundantDeviceWrites bool
	// MergeRenamedTails additionally merges structurally-identical tails whose
	// registers were allocated differently, when the renaming is safe. Off by
	// default: it is experimental and can change register usage. See
	// docs/tail-merge.md. IC10C_MERGE_RENAMED_TAILS=1 also turns it on.
	MergeRenamedTails bool

	// MaxLines, MaxBytes and MaxLineLen override the IC10 editor limits the
	// generated program is validated against (and the line count a one-time
	// loader is split at). Zero means the default (128 lines / 4096 bytes /
	// 90 characters). IC10C_MAX_LINES, IC10C_MAX_BYTES and IC10C_MAX_LINE
	// override them too, so the toolchain can follow the game if its limits
	// change.
	MaxLines   int
	MaxBytes   int
	MaxLineLen int

	// Imports resolves `import "path"` declarations by reading the referenced
	// files and merging their const/data/func declarations. The editor leaves
	// it off (it checks a single buffer); the CLI turns it on.
	Imports bool
	// LibDirs are extra directories searched for imports, after the importing
	// file's own directory.
	LibDirs []string

	// recordBus, when set, records every `Bus.slot` read/write (with its access
	// point) while compiling, so CompileResult can check writers and wire a VM.
	recordBus func(bus, slot, devConn string, write bool)
}

// resolvePrivateStack decides whether the program's user stack slots are
// private. Single-chip programs default to private; multi-chip to shared. The
// `// icg: private-stack` / `// icg: shared-stack` pragma overrides.
func resolvePrivateStack(src []byte, multiChip bool) bool {
	if v, ok := stackPragma(src); ok {
		return v
	}
	return !multiChip
}

// stackPragma reads a `// icg: private-stack` / `// icg: shared-stack` file
// pragma and reports (value, present).
func stackPragma(src []byte) (bool, bool) {
	for _, line := range strings.Split(string(src), "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "//") {
			continue
		}
		t = strings.TrimSpace(strings.TrimPrefix(t, "//"))
		if !strings.HasPrefix(t, "icg:") {
			continue
		}
		switch strings.TrimSpace(strings.TrimPrefix(t, "icg:")) {
		case "private-stack":
			return true, true
		case "shared-stack":
			return false, true
		}
	}
	return false, false
}

// DefaultUserStack is the default fixed user stack size. It leaves the
// compiler 384 slots for the data segment (loader-capped at the line limit) and
// register spills.
const DefaultUserStack = 128

// stackEnv merges the stack-related environment switches into opts, so the LSP
// (spawned with the editor's settings in its environment) behaves like the CLI.
func stackEnv(opts Options) Options {
	if !opts.DynamicStack && os.Getenv("IC10C_DYNAMIC_STACK") != "" {
		opts.DynamicStack = true
	}
	if opts.UserStackLimit == 0 {
		if v := os.Getenv("IC10C_USER_STACK"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				opts.UserStackLimit = n
			}
		}
	}
	if !opts.RedundantDeviceWrites && os.Getenv("IC10C_REDUNDANT_DEVICE_WRITES") != "" {
		opts.RedundantDeviceWrites = true
	}
	if !opts.MergeRenamedTails && os.Getenv("IC10C_MERGE_RENAMED_TAILS") != "" {
		opts.MergeRenamedTails = true
	}
	if opts.MaxLines == 0 {
		opts.MaxLines = positiveEnv("IC10C_MAX_LINES")
	}
	if opts.MaxBytes == 0 {
		opts.MaxBytes = positiveEnv("IC10C_MAX_BYTES")
	}
	if opts.MaxLineLen == 0 {
		opts.MaxLineLen = positiveEnv("IC10C_MAX_LINE")
	}
	return opts
}

// positiveEnv parses a positive integer from the environment, returning 0 when
// it is unset or invalid.
func positiveEnv(key string) int {
	v := os.Getenv(key)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return n
	}
	return 0
}

// editorLimits returns the codegen limits selected by opts, after applying the
// defaults for any unset field.
func (o Options) editorLimits() codegen.Limits {
	return codegen.Limits{Lines: o.MaxLines, Bytes: o.MaxBytes, LineLen: o.MaxLineLen}.Resolve()
}

// fixedDataBase returns the fixed data base for the selected layout, or 0 for
// the default top-of-stack layout.
func fixedDataBase(opts Options) int {
	if opts.DataLayout == "middle" {
		return sema.FixedDataBase
	}
	return 0
}

// ChipResult is one chip's compiled output in a multi-chip program.
type ChipResult struct {
	// Name is the chip block's name.
	Name string
	// Code is the chip's runtime program.
	Code string
	// Loader is the chip's one-time loader (data segment and/or hoisted setup),
	// or "" when the chip needs none.
	Loader string
	// Loaders splits Loader into chunks that each fit the chip editor; run them
	// in order. Empty when Loader is empty, and a single chunk when it fits.
	Loaders []string
	// Setup reports whether Loader includes hoisted one-time device writes
	// (modes / switches / constant settings) rather than only data-segment writes.
	Setup bool
	// BusAccess maps "Bus.slot" to the access points ("dev:conn") this chip
	// uses for it, used to wire a multi-chip VM run. Nil when the chip uses no
	// bus.
	BusAccess map[string][]string
}

// Result is a compiled program: the runtime code plus an optional one-time
// loader.
type Result struct {
	// Code is the runtime program installed on the chip.
	Code string
	// Loader is a one-time program that must run before Code, or "" when the
	// program needs none. It installs device modes / switches / constant
	// settings that the compiler hoisted out of the runtime to fit the line
	// budget (see CompileResult).
	Loader string
	// Loaders splits Loader into chunks that each fit the chip editor; run them
	// in order. Empty when Loader is empty, and a single chunk when it fits.
	Loaders []string
	// Chips holds every chip when the source declares `chip` blocks. For a
	// single-chip program it holds one entry mirroring Code/Loader, so callers
	// can always iterate Chips.
	Chips []ChipResult
	// Setup reports whether Loader includes hoisted one-time device writes.
	Setup bool
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
	res, diags, err := CompileResult(name, src, opts)
	return res.Code, diags, err
}

// CompileResult is Compile plus the optional one-time setup loader.
//
// When the runtime would exceed an IC10 limit, the compiler hoists constant
// device writes from the straight-line prologue (device modes, On/Off switches,
// constant settings) into Result.Loader. Device state persists, so the loader
// only has to run once; the chip is then overwritten with the runtime.
func CompileResult(name string, src []byte, opts Options) (Result, *diag.Bag, error) {
	opts = stackEnv(opts)
	file := source.NewFile(name, src)
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)
	tree := parser.Parse(file, toks, diags)
	if diags.HasErrors() {
		return Result{}, diags, nil
	}
	if opts.Imports {
		expandImports(tree, name, opts.LibDirs, diags)
		if diags.HasErrors() {
			return Result{}, diags, nil
		}
	}

	common, chips := splitChips(tree)
	opts.PrivateStack = resolvePrivateStack(src, len(chips) > 0)

	// Track which chip reads/writes each bus slot (producer/consumer check) and
	// which access points each chip uses (for VM wiring).
	busUses := map[string]*busUse{}
	chipAccess := map[string]map[string]map[string]bool{}
	currentChip := ""
	opts.recordBus = func(bus, slot, devConn string, write bool) {
		k := bus + "." + slot
		u := busUses[k]
		if u == nil {
			u = &busUse{readers: map[string]bool{}, writers: map[string]bool{}}
			busUses[k] = u
		}
		if write {
			u.writers[currentChip] = true
		} else {
			u.readers[currentChip] = true
		}
		if chipAccess[currentChip] == nil {
			chipAccess[currentChip] = map[string]map[string]bool{}
		}
		if chipAccess[currentChip][k] == nil {
			chipAccess[currentChip][k] = map[string]bool{}
		}
		chipAccess[currentChip][k][devConn] = true
	}

	if len(chips) == 0 {
		info := checkInfo(tree, name, diags, opts)
		if info == nil || diags.HasErrors() {
			return Result{}, diags, nil
		}
		if info.Main == nil {
			diags.ErrorfCode("no-main", source.Pos{File: name, Line: 1, Col: 1}, "no main function found")
		}
		if diags.HasErrors() {
			return Result{}, diags, nil
		}
		res, err := compileInfo(info, opts, diags)
		checkBusUse(common, busUses, diags)
		if err != nil {
			return res, diags, err
		}
		dl, derr := dataLoaderFor(info, opts)
		if derr != nil {
			return Result{}, diags, derr
		}
		res.Loader = dl + res.Loader
		res.Loaders = SplitLoaderLines(res.Loader, opts.editorLimits().Lines)
		res.Chips = []ChipResult{{Code: res.Code, Loader: res.Loader, Loaders: res.Loaders, Setup: res.Setup, BusAccess: chipBusAccess(chipAccess, "")}}
		return res, diags, nil
	}

	// Multi-chip: each chip is compiled independently against the shared
	// top-level declarations. The top-level `main` is not allowed.
	if top := topMain(common); top != nil {
		diags.Errorf(top.Pos(), "top-level main cannot be mixed with chip blocks; move it into a chip")
	}
	var results []ChipResult
	var firstErr error
	for _, ch := range chips {
		currentChip = ch.Name.Name
		cdiags := &diag.Bag{}
		info := checkInfo(&ast.File{Decls: mergeDecls(common, ch.Decls)}, name, cdiags, opts)
		if info == nil {
			mergeDiags(diags, cdiags)
			continue
		}
		if info.Main == nil {
			cdiags.ErrorfCode("no-main", ch.Name.Pos(), "chip %q has no main function", ch.Name.Name)
		}
		if cdiags.HasErrors() {
			mergeDiags(diags, cdiags)
			continue
		}
		res, err := compileInfo(info, opts, cdiags)
		mergeDiags(diags, cdiags)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		dl, derr := dataLoaderFor(info, opts)
		if derr != nil {
			if firstErr == nil {
				firstErr = derr
			}
			continue
		}
		loader := dl + res.Loader
		results = append(results, ChipResult{Name: ch.Name.Name, Code: res.Code, Loader: loader, Loaders: SplitLoaderLines(loader, opts.editorLimits().Lines), Setup: res.Setup, BusAccess: chipBusAccess(chipAccess, ch.Name.Name)})
	}
	checkBusUse(common, busUses, diags)
	if len(results) == 0 {
		return Result{}, diags, firstErr
	}
	return Result{Code: results[0].Code, Loader: results[0].Loader, Loaders: results[0].Loaders, Chips: results, Setup: results[0].Setup}, diags, firstErr
}

// busUse records which chips read and write one bus slot.
type busUse struct {
	readers map[string]bool
	writers map[string]bool
}

// checkBusUse enforces that every bus slot is written by exactly one chip when
// it is used; a slot nobody touches is fine.
func checkBusUse(common []ast.Decl, uses map[string]*busUse, diags *diag.Bag) {
	for _, d := range common {
		bus, ok := d.(*ast.BusDecl)
		if !ok {
			continue
		}
		for _, s := range bus.Slots {
			u := uses[bus.Name.Name+"."+s.Name.Name]
			readers, writers := 0, 0
			if u != nil {
				readers, writers = len(u.readers), len(u.writers)
			}
			switch {
			case writers > 1:
				diags.Errorf(s.Name.Pos(), "bus slot %s.%s is written by multiple chips", bus.Name.Name, s.Name.Name)
			case readers > 0 && writers == 0:
				diags.Errorf(s.Name.Pos(), "bus slot %s.%s is read but never written", bus.Name.Name, s.Name.Name)
			}
		}
	}
}

// compileInfo runs the lower/optimize/codegen pipeline for one checked chip.
func compileInfo(info *sema.Info, opts Options, diags *diag.Bag) (Result, error) {
	noCheck, noOutline, noOpt := envSwitches()
	plan := lower.PlanOutlines(info, noOutline)
	var best Result
	haveBest := false
	var bestErr error
	consider := func(r Result) {
		if !haveBest || better(r.Code, best.Code) {
			best, haveBest = r, true
		}
	}
	run := func(outline map[string]bool, noFold, noMem2Reg bool) {
		o := opts
		o.NoFoldDataReads = noFold
		o.NoMem2Reg = noMem2Reg
		fn := lowerAndOptimize(info, o, outline, noCheck, noOpt, diags)
		if fn == nil || diags.HasErrors() {
			return
		}
		code, spills, err := generate(fn, info, o)
		if err == nil {
			checkStackRegion(fn, info, spills, o, diags)
		}
		// Split out one-time setup writes when the runtime is over a limit, or
		// when the program already needs a one-time loader (data segment): in
		// that case moving setup writes into the existing loader is free.
		if err == nil && info.DataSize == 0 {
			consider(Result{Code: code})
			return
		}
		setup := opt.SplitSetup(fn)
		if setup == nil {
			if err == nil {
				consider(Result{Code: code})
			} else {
				bestErr = err
			}
			return
		}
		if oerr := opt.Optimize(fn); oerr != nil {
			if err == nil {
				consider(Result{Code: code})
			} else {
				bestErr = err
			}
			return
		}
		runtime, _, rerr := generate(fn, info, o)
		if rerr != nil {
			if err == nil {
				consider(Result{Code: code})
			} else {
				bestErr = rerr
			}
			return
		}
		loader, _, lerr := generate(setup, info, o)
		if lerr != nil {
			if err == nil {
				consider(Result{Code: code})
			} else {
				bestErr = rerr
			}
			return
		}
		consider(Result{Code: runtime, Loader: loader, Setup: true})
	}
	// Try each outline plan with and without constant data-read folding; the
	// shortest runtime wins (folding is usually shorter, but not always).
	// Try several outline plans and keep the shortest: all inlined, the planned
	// set, and (when it has more than one member) each function alone, since
	// outlining one function may pay off when the whole set does not.
	outlines := []map[string]bool{nil}
	if len(plan) > 0 {
		outlines = append(outlines, plan)
		if len(plan) > 1 {
			names := make([]string, 0, len(plan))
			for name := range plan {
				names = append(names, name)
			}
			sort.Strings(names)
			const maxSinglePlans = 2
			for i, name := range names {
				if i >= maxSinglePlans {
					break
				}
				outlines = append(outlines, map[string]bool{name: true})
			}
		}
	}
	// Data-read folding only matters when there is a data segment; without one
	// the two variants are identical, so skip the extra compile.
	folds := []bool{false}
	if info.DataSize > 0 {
		folds = append(folds, true)
	}
	// User-stack promotion only applies to private, constant user slots; probe
	// with a cheap lowering (no optimisation) to avoid extra compiles.
	mem2regs := []bool{false}
	if probe := lowerAndOptimize(info, opts, nil, noCheck, true, &diag.Bag{}); probe != nil &&
		probe.PrivateStack && probe.UserStackManual > 0 && !probe.UserStackDynamic {
		mem2regs = append(mem2regs, true)
	}
	for _, outline := range outlines {
		for _, noFold := range folds {
			for _, noMem2Reg := range mem2regs {
				run(outline, noFold, noMem2Reg)
			}
		}
	}
	// Each candidate re-runs lowering, so warnings can repeat; keep one copy.
	dedupeDiags(diags)
	if !haveBest {
		return Result{}, bestErr
	}
	return best, nil
}

// dedupeDiags removes duplicate diagnostics in place, preserving order.
func dedupeDiags(b *diag.Bag) {
	seen := map[string]bool{}
	kept := b.Diags[:0]
	for _, d := range b.Diags {
		k := diagKey(d)
		if seen[k] {
			continue
		}
		seen[k] = true
		kept = append(kept, d)
	}
	b.Diags = kept
}

// splitChips separates top-level chip blocks from the shared declarations.
func splitChips(f *ast.File) (common []ast.Decl, chips []*ast.ChipDecl) {
	for _, d := range f.Decls {
		if ch, ok := d.(*ast.ChipDecl); ok {
			chips = append(chips, ch)
			continue
		}
		common = append(common, d)
	}
	return common, chips
}

// mergeDecls returns the shared declarations followed by the chip's own, with
// any shared declaration shadowed by a chip-local one of the same name dropped.
func mergeDecls(common, chip []ast.Decl) []ast.Decl {
	shadow := map[string]bool{}
	for _, d := range chip {
		if n := declName(d); n != "" {
			shadow[n] = true
		}
	}
	out := make([]ast.Decl, 0, len(common)+len(chip))
	for _, d := range common {
		if n := declName(d); n != "" && shadow[n] {
			continue
		}
		out = append(out, d)
	}
	return append(out, chip...)
}

// topMain returns the top-level `func main`, if any.
func topMain(decls []ast.Decl) *ast.FuncDecl {
	for _, d := range decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == "main" {
			return f
		}
	}
	return nil
}

// chipBusAccess renders the access points a chip used for each "Bus.slot".
func chipBusAccess(byChip map[string]map[string]map[string]bool, chip string) map[string][]string {
	slots := byChip[chip]
	if len(slots) == 0 {
		return nil
	}
	out := make(map[string][]string, len(slots))
	for k, set := range slots {
		conns := make([]string, 0, len(set))
		for devConn := range set {
			conns = append(conns, devConn)
		}
		sort.Strings(conns)
		out[k] = conns
	}
	return out
}

func declName(d ast.Decl) string {
	switch d := d.(type) {
	case *ast.ConstDecl:
		return d.Name.Name
	case *ast.DataDecl:
		return d.Name.Name
	case *ast.VarDecl:
		return d.Name.Name
	case *ast.FuncDecl:
		return d.Name.Name
	}
	return ""
}

// mergeDiags appends src diagnostics to dst, skipping duplicates (the shared
// declarations are checked once per chip, so their diagnostics would repeat).
func mergeDiags(dst, src *diag.Bag) {
	seen := map[string]bool{}
	for _, d := range dst.Diags {
		seen[diagKey(d)] = true
	}
	for _, d := range src.Diags {
		k := diagKey(d)
		if seen[k] {
			continue
		}
		seen[k] = true
		dst.Diags = append(dst.Diags, d)
	}
}

func diagKey(d diag.Diagnostic) string {
	return fmt.Sprintf("%d:%d:%d:%s", d.Severity, d.Pos.Offset, d.End.Offset, d.Msg)
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
// It returns the number of register spill slots used.
func generate(fn *ir.Function, info *sema.Info, opts Options) (string, int, error) {
	code, _, spills, err := generateColored(fn, info, opts)
	return code, spills, err
}

// generateColored is generate plus the register colouring, which the
// control-flow graph needs to render instruction text.
func generateColored(fn *ir.Function, info *sema.Info, opts Options) (string, map[*ir.Reg]int, int, error) {
	reserved := info.DataSize
	if fixedDataBase(opts) > 0 {
		reserved = 0
	}
	spillMode := regalloc.SpillDB
	if opts.SpillStack {
		spillMode = regalloc.SpillStack
	}
	colors, spillCount, err := regalloc.AllocateReservedSpillsMode(fn, NumRegs, reserved, spillMode)
	if err != nil {
		return "", nil, 0, err
	}
	if spillCount > 0 && fixedDataBase(opts) > 0 {
		bottom := 511 - reserved - spillCount + 1
		dataEnd := info.Sentinel + info.DataSize - 1
		if bottom <= dataEnd {
			return "", nil, 0, fmt.Errorf("register spills (%d slots, down to %d) overlap the data segment [%d..%d]",
				spillCount, bottom, info.Sentinel, dataEnd)
		}
	}
	if opts.MergeRenamedTails {
		if opt.MergeTailsRenamed(fn, colors) {
			fn.BuildCFG()
		}
	} else if opt.MergeTailsColored(fn, colors) {
		fn.BuildCFG()
	}
	code, err := codegen.GenerateWithOptions(fn, colors, codegen.Options{
		RelJump: opts.RelJump,
		SpillDB: !opts.SpillStack,
		Limits:  opts.editorLimits(),
	})
	return code, colors, spillCount, err
}

// checkStackRegion rejects user stack accesses that reach into the compiler's
// data/spill region. Dynamic addresses and unbounded push are reported by the
// stack report instead: IC10 programs use them, so they are not compile errors.
func checkStackRegion(fn *ir.Function, info *sema.Info, spillCount int, opts Options, diags *diag.Bag) {
	base := userLimit(info, spillCount, opts)
	// Dynamic mode puts the boundary just below the compiler's data/spills, so
	// they always fit; the fixed mode must check that they still do.
	if !opts.DynamicStack {
		if need := info.DataSize + spillCount; need > sema.StackSize-base {
			diags.ErrorfCode("stack-compiler-overflow", info.Main.Pos(),
				"the compiler needs %d stack slots but only %d are above the %d-slot user stack",
				need, sema.StackSize-base, base)
			return
		}
	}
	for _, u := range fn.UserStackUses {
		if u.Dynamic || u.Slot < base {
			continue
		}
		diags.ErrorfCode("stack-overlap", u.Pos,
			"stack slot %d is above the %d-slot user stack [0..%d]; raise --user-stack or use --dynamic-stack",
			u.Slot, base, base-1)
	}
	if d, unbounded := analyzeDepth(fn); !unbounded && d > base {
		diags.ErrorfCode("stack-overlap", info.Main.Pos(),
			"push depth %d exceeds the %d-slot user stack; raise --user-stack or use --dynamic-stack",
			d, base)
	}
}

// parseAndCheck lexes, parses and type-checks the source. For multi-chip files
// it returns the first chip's info; the graph/size/stack tools use it.
func parseAndCheck(name string, src []byte, opts Options) (*sema.Info, *diag.Bag, bool) {
	file := source.NewFile(name, src)
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)
	tree := parser.Parse(file, toks, diags)
	if diags.HasErrors() {
		return nil, diags, false
	}
	common, chips := splitChips(tree)
	private := resolvePrivateStack(src, len(chips) > 0)
	target := tree
	if len(chips) > 0 {
		target = &ast.File{Decls: mergeDecls(common, chips[0].Decls)}
	}
	info := checkInfo(target, name, diags, opts)
	if info.Main == nil {
		diags.ErrorfCode("no-main", source.Pos{File: name, Line: 1, Col: 1}, "no main function found")
	}
	if diags.HasErrors() {
		return nil, diags, private
	}
	return info, diags, private
}

// checkInfo type-checks one already-parsed program (a single-chip file, or a
// chip merged with the shared declarations). It returns a nil Info only when
// semantic checking failed to produce one.
func checkInfo(tree *ast.File, name string, diags *diag.Bag, opts Options) *sema.Info {
	info := sema.CheckWithOptions(tree, diags, sema.Options{
		FixedDataBase: fixedDataBase(opts),
		AutoTable:     opts.AutoTable,
	})
	if info.DataSize > 0 && info.Sentinel+info.DataSize > sema.StackSize {
		diags.ErrorfCode("data-too-large", source.Pos{File: name, Line: 1, Col: 1},
			"data segment [%d..%d] exceeds the %d-slot stack", info.Sentinel, info.Sentinel+info.DataSize-1, sema.StackSize)
	}
	return info
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
		// Folding a data read removes the stack load, so it would also bypass
		// --unsafe/--no-data-check (which the tests use to observe the raw
		// stack). Keep the load in that mode.
		FoldData:  !opts.NoFoldDataReads && !opts.NoDataCheck && !opts.Unsafe,
		RecordBus: opts.recordBus,
		// The folding decision depends on the effective per-line limit.
		MaxLineLen: opts.editorLimits().LineLen,
	})
	if diags.HasErrors() {
		return nil
	}
	fn.UserLimit = userLimit(info, 0, opts)
	fn.DataBase = compilerBase(info, 0, opts)
	fn.PrivateStack = opts.PrivateStack
	fn.NoMem2Reg = opts.NoMem2Reg
	fn.RedundantDeviceWrites = opts.RedundantDeviceWrites
	if !noOpt {
		if err := opt.Optimize(fn); err != nil {
			diags.Errorf(info.Main.Pos(), "internal error: %v", err)
			return nil
		}
	}
	return fn
}
