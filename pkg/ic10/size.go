package ic10

import (
	"fmt"
	"sort"

	"ic10go/internal/codegen"
	"ic10go/internal/ir"
	"ic10go/internal/lower"
	"ic10go/internal/opt"
	"ic10go/internal/regalloc"
)

// SizeReport is the compiled program's IC10 line budget broken down by source
// function. A function inlined at several call sites contributes its body once
// per call site, so a high count marks a good candidate for simplification or
// for letting the compiler outline it.
type SizeReport struct {
	Total    int            // total IC10 lines
	Limit    int            // the IC10 line limit (128)
	ByFunc   map[string]int // source function -> lines ("" = main)
	Outlined []string       // functions emitted once as subroutines
	PeakLive int            // max virtual registers live at any program point
	Spills   int            // stack slots used for register spilling
}

// Size compiles the source and returns a per-function line breakdown. It uses
// the same inlined/outlined selection as Compile.
func Size(name string, src []byte, opts Options) (*SizeReport, error) {
	info, diags := parseAndCheck(name, src, opts)
	if info == nil || diags.HasErrors() {
		return nil, fmt.Errorf("compile failed")
	}

	noCheck, noOutline, noOpt := envSwitches()
	plan := lower.PlanOutlines(info, noOutline)
	bestTotal := -1
	var best *SizeReport
	try := func(outline map[string]bool) {
		fn := lowerAndOptimize(info, opts, outline, noCheck, noOpt, diags)
		if fn == nil {
			return
		}
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
			return
		}
		if opt.MergeTailsColored(fn, colors) {
			fn.BuildCFG()
		}
		_, rep, _ := codegen.GenerateReportWithOptions(fn, colors, codegen.Options{SpillDB: !opts.SpillStack})
		if rep == nil {
			return
		}
		if bestTotal == -1 || rep.Total < bestTotal {
			bestTotal = rep.Total
			best = &SizeReport{
				Total:    rep.Total,
				Limit:    codegen.MaxLines,
				ByFunc:   rep.ByFunc,
				PeakLive: maxPressure(fn),
				Spills:   spillCount,
			}
			if outline != nil {
				for n := range outline {
					best.Outlined = append(best.Outlined, n)
				}
				sort.Strings(best.Outlined)
			}
		}
	}
	try(nil)
	if len(plan) > 0 {
		try(plan)
	}
	if best == nil {
		return nil, fmt.Errorf("compile failed")
	}
	return best, nil
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
