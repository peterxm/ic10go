package ic10

import (
	"fmt"
	"sort"

	"ic10go/internal/codegen"
	"ic10go/internal/diag"
	"ic10go/internal/ir"
	"ic10go/internal/lower"
	"ic10go/internal/opt"
	"ic10go/internal/regalloc"
	"ic10go/internal/sema"
)

// SizeReport is the compiled program's IC10 line budget broken down by source
// function. A function inlined at several call sites contributes its body once
// per call site, so a high count marks a good candidate for simplification or
// for letting the compiler outline it.
type SizeReport struct {
	Total    int            // total IC10 lines
	Limit    int            // the IC10 line limit in effect (default 128)
	ByFunc   map[string]int // source function -> lines ("" = main)
	Outlined []string       // functions emitted once as subroutines
	PeakLive int            // max virtual registers live at any program point
	Spills   int            // stack slots used for register spilling
	Stack    StackReport    // user vs compiler stack usage
}

// StackReport splits the persistent stack into the region user code may address
// and the region the compiler owns. push/pop and explicit absolute accesses
// (db.stack[addr], poke, get/put db) live in the user region
// [0, UserLimit-1]; the data segment and register spills live in the compiler
// region [UserLimit, Total-1].
type StackReport struct {
	Total         int  // stack slots (512)
	Dynamic       bool // the user limit is the dynamic compiler boundary
	UserLimit     int  // size of the user region
	UserUsed      int  // number of distinct user slots used
	UserMax       int  // highest user slot touched + 1
	UserPush      int  // push/pop depth
	UserManual    int  // highest explicit db.stack[]/poke address + 1
	CompilerBase  int  // first slot of the compiler region (dynamic mode)
	CompilerUsed  int  // data segment + register spills
	DataSlots     int
	SpillSlots    int
	UserUnbounded bool
	UserDynamic   bool // a user stack address is not a compile-time constant
}

// Size compiles the source and returns a per-function line breakdown. It uses
// the same inlined/outlined selection as Compile.
func Size(name string, src []byte, opts Options) (*SizeReport, error) {
	opts = stackEnv(opts)
	info, diags, private := parseAndCheck(name, src, opts)
	if info == nil || diags.HasErrors() {
		return nil, fmt.Errorf("compile failed")
	}
	opts.PrivateStack = private

	noCheck, noOutline, noOpt := envSwitches()
	plan := lower.PlanOutlines(info, noOutline)
	bestTotal := -1
	var best *SizeReport
	try := func(outline map[string]bool, noFold, noMem2Reg bool) {
		o := opts
		o.NoFoldDataReads = noFold
		o.NoMem2Reg = noMem2Reg
		fn := lowerAndOptimize(info, o, outline, noCheck, noOpt, diags)
		if fn == nil {
			return
		}
		reserved := info.DataSize
		if fixedDataBase(o) > 0 {
			reserved = 0
		}
		spillMode := regalloc.SpillDB
		if o.SpillStack {
			spillMode = regalloc.SpillStack
		}
		colors, spillCount, err := regalloc.AllocateReservedSpillsMode(fn, NumRegs, reserved, spillMode)
		if err != nil {
			return
		}
		if o.MergeRenamedTails {
			if opt.MergeTailsRenamed(fn, colors) {
				fn.BuildCFG()
			}
		} else if opt.MergeTailsColored(fn, colors) {
			fn.BuildCFG()
		}
		_, rep, _ := codegen.GenerateReportWithOptions(fn, colors, codegen.Options{SpillDB: !o.SpillStack, Limits: o.editorLimits()})
		if rep == nil {
			return
		}
		if bestTotal == -1 || rep.Total < bestTotal {
			bestTotal = rep.Total
			best = &SizeReport{
				Total:    rep.Total,
				Limit:    o.editorLimits().Lines,
				ByFunc:   rep.ByFunc,
				PeakLive: maxPressure(fn),
				Spills:   spillCount,
				Stack:    stackReport(fn, info, spillCount, o),
			}
			if outline != nil {
				for n := range outline {
					best.Outlined = append(best.Outlined, n)
				}
				sort.Strings(best.Outlined)
			}
		}
	}
	outlines := []map[string]bool{nil}
	if len(plan) > 0 {
		outlines = append(outlines, plan)
	}
	folds := []bool{false}
	if info.DataSize > 0 {
		folds = append(folds, true)
	}
	mem2regs := []bool{false}
	if probe := lowerAndOptimize(info, opts, nil, noCheck, true, &diag.Bag{}); probe != nil &&
		probe.PrivateStack && probe.UserStackManual > 0 && !probe.UserStackDynamic {
		mem2regs = append(mem2regs, true)
	}
	for _, outline := range outlines {
		for _, noFold := range folds {
			for _, noMem2Reg := range mem2regs {
				try(outline, noFold, noMem2Reg)
			}
		}
	}
	if best == nil {
		return nil, fmt.Errorf("compile failed")
	}
	return best, nil
}

// compilerBase returns the first stack slot the compiler owns: the data segment
// and register spills sit at the top, so the base is the lowest slot either
// occupies. User code may address [0, base-1].
func compilerBase(info *sema.Info, spillSlots int, opts Options) int {
	if fixedDataBase(opts) > 0 {
		// "middle" layout: data starts at FixedDataBase and spills grow down
		// from the top; the region between is compiler-owned too.
		return sema.FixedDataBase
	}
	base := sema.StackSize - info.DataSize - spillSlots
	if base < 0 {
		base = 0
	}
	return base
}

// userLimit is the size of the user stack region. By default it is the fixed
// UserStackLimit (DefaultUserStack when unset); with DynamicStack it is the
// boundary below the compiler's data/spill region.
func userLimit(info *sema.Info, spillSlots int, opts Options) int {
	if opts.DynamicStack {
		return compilerBase(info, spillSlots, opts)
	}
	n := opts.UserStackLimit
	if n <= 0 {
		n = DefaultUserStack
	}
	if n > sema.StackSize {
		n = sema.StackSize
	}
	return n
}

// stackReport combines the CFG push depth with explicit absolute accesses and
// the compiler's data/spill usage into one report.
func stackReport(fn *ir.Function, info *sema.Info, spillSlots int, opts Options) StackReport {
	push, unbounded := analyzeDepth(fn)
	// UserUsed counts the distinct slots the user touches: the push/pop range
	// [0, push-1] plus each explicit db.stack[]/poke slot.
	used := map[int]bool{}
	if !unbounded {
		for i := 0; i < push; i++ {
			used[i] = true
		}
	}
	for _, u := range fn.UserStackUses {
		if !u.Dynamic {
			used[u.Slot] = true
		}
	}
	max := 0
	for slot := range used {
		if slot+1 > max {
			max = slot + 1
		}
	}
	base := compilerBase(info, spillSlots, opts)
	return StackReport{
		Total:         sema.StackSize,
		Dynamic:       opts.DynamicStack,
		UserLimit:     userLimit(info, spillSlots, opts),
		UserUsed:      len(used),
		UserMax:       max,
		UserPush:      push,
		UserManual:    fn.UserStackManual,
		CompilerBase:  base,
		CompilerUsed:  info.DataSize + spillSlots,
		DataSlots:     info.DataSize,
		SpillSlots:    spillSlots,
		UserUnbounded: unbounded,
		UserDynamic:   fn.UserStackDynamic,
	}
}

// maxPressure returns the maximum number of virtual registers live at any
// program point — the register pressure the allocator must fit into 16.
func maxPressure(fn *ir.Function) int {
	_, out := ir.Liveness(fn)
	peak := 0
	for _, b := range fn.Blocks {
		live := map[*ir.Reg]bool{}
		for r := range out[b] {
			live[r] = true
		}
		for _, r := range ir.TermUses(b.Term) {
			live[r] = true
		}
		if len(live) > peak {
			peak = len(live)
		}
		for i := len(b.Instrs) - 1; i >= 0; i-- {
			u, d := ir.DefUse(b.Instrs[i])
			for _, r := range d {
				delete(live, r)
			}
			for _, r := range u {
				live[r] = true
			}
			if len(live) > peak {
				peak = len(live)
			}
		}
	}
	return peak
}
