package ic10

import (
	"strings"

	"ic10go/internal/codegen"
	"ic10go/internal/diag"
	"ic10go/internal/ir"
	"ic10go/internal/lower"
)

// GraphResult is the control-flow graph of the chosen lowering: the IR function
// (basic blocks + terminators), the codegen block order, each block's 0-based
// start line in the emitted IC10, and the register colouring needed to render
// instruction text.
type GraphResult struct {
	Fn     *ir.Function
	Order  []*ir.Block
	Start  map[*ir.Block]int
	Colors map[*ir.Reg]int
}

// Graph compiles src and returns the control-flow graph of the same lowering
// CompileWithOptions would choose (inlined vs outlined, whichever is shorter).
func Graph(name string, src []byte, opts Options) (*GraphResult, *diag.Bag, error) {
	info, diags := parseAndCheck(name, src, opts)
	if info == nil || diags.HasErrors() {
		return nil, diags, nil
	}
	noCheck, noOutline, noOpt := envSwitches()
	plan := lower.PlanOutlines(info, noOutline)

	var best *GraphResult
	bestLines := -1
	try := func(outline map[string]bool) {
		fn := lowerAndOptimize(info, opts, outline, noCheck, noOpt, diags)
		if fn == nil || diags.HasErrors() {
			return
		}
		code, colors, err := generateColored(fn, info, opts)
		if err != nil {
			return
		}
		order, start := codegen.Layout(fn, colors)
		if n := strings.Count(code, "\n"); best == nil || n < bestLines {
			best = &GraphResult{Fn: fn, Order: order, Start: start, Colors: colors}
			bestLines = n
		}
	}
	try(nil)
	if len(plan) > 0 {
		try(plan)
	}
	if best == nil {
		return nil, diags, nil
	}
	return best, diags, nil
}
