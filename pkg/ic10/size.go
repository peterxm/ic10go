package ic10

import (
	"fmt"
	"sort"

	"ic10go/internal/codegen"
	"ic10go/internal/lower"
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
}

// Size compiles the source and returns a per-function line breakdown. It uses
// the same inlined/outlined selection as Compile.
func Size(name string, src []byte, opts Options) (*SizeReport, error) {
	info, diags := parseAndCheck(name, src, opts)
	if info == nil || diags.HasErrors() {
		return nil, fmt.Errorf("compile failed")
	}

	plan := lower.PlanOutlines(info)
	bestTotal := -1
	var best *SizeReport
	try := func(outline map[string]bool) {
		fn := lowerAndOptimize(info, opts, outline, diags)
		if fn == nil {
			return
		}
		reserved := info.DataSize
		if fixedDataBase(opts) > 0 {
			reserved = 0
		}
		colors, _, err := regalloc.AllocateReservedSpills(fn, NumRegs, reserved)
		if err != nil {
			return
		}
		_, rep, _ := codegen.GenerateReport(fn, colors)
		if rep == nil {
			return
		}
		if bestTotal == -1 || rep.Total < bestTotal {
			bestTotal = rep.Total
			best = &SizeReport{Total: rep.Total, Limit: codegen.MaxLines, ByFunc: rep.ByFunc}
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
