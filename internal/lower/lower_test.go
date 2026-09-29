package lower

import (
	"strings"
	"testing"

	"ic10go/internal/diag"
	"ic10go/internal/lexer"
	"ic10go/internal/parser"
	"ic10go/internal/sema"
	"ic10go/internal/source"
)

// planOf type-checks src and returns PlanOutlines for it.
func planOf(t *testing.T, src string) map[string]bool {
	t.Helper()
	file := source.NewFile("t.icg", []byte(src))
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)
	tree := parser.Parse(file, toks, diags)
	info := sema.Check(tree, diags)
	if info == nil || diags.HasErrors() {
		t.Fatalf("check failed: %v", diags.Diags)
	}
	return PlanOutlines(info, false)
}

// TestPlanOutlinesNonLeaf checks D3a: a non-leaf helper called repeatedly is a
// candidate, as long as no function in its reachable call graph has a low-level
// label/goto/call/ret (which would put a `jal` inside the outlined body).
func TestPlanOutlinesNonLeaf(t *testing.T) {
	plan := planOf(t, `
func clamp(x num, lo num, hi num) num {
    if x < lo { return lo }
    if x > hi { return hi }
    return x
}
func step(e num) num { return clamp(e, -10.0, 10.0) }

func raw(x num) num {
    if x > 0 { goto done }
    x = x + 1
    label done:
    return x
}
func mid(x num) num { return raw(x) + raw(x) }

func main() {
    for {
        yield()
        d0.Setting = step(d1.Setting)
        d2.Setting = step(d3.Setting)
        d4.Setting = mid(d5.Setting)
        d0.On = mid(d1.On)
    }
}
`)
	if !plan["step"] {
		t.Errorf("non-leaf step should be outlined, plan=%v", plan)
	}
	if plan["mid"] {
		t.Errorf("mid calls a labeled function, must not be outlined, plan=%v", plan)
	}
	if plan["raw"] {
		t.Errorf("raw contains a label, must not be outlined, plan=%v", plan)
	}
}

// TestDataConstLineLimit verifies that the fold threshold follows the
// configured per-line character limit: a literal is folded only when it still
// fits an emitted line.
func TestDataConstLineLimit(t *testing.T) {
	long := strings.Repeat("7", 100) // not a float, so it stays a raw literal
	if _, ok := dataConst(long, 90); ok {
		t.Errorf("100-char literal folded at maxLineLen=90 (threshold 40)")
	}
	if _, ok := dataConst(long, 180); !ok {
		t.Errorf("100-char literal not folded at maxLineLen=180 (threshold 130)")
	}
	if _, ok := dataConst("12345", 90); !ok {
		t.Errorf("short literal should fold at maxLineLen=90")
	}
	// Zero keeps the historical conservative threshold of 40.
	if _, ok := dataConst(strings.Repeat("7", 41), 0); ok {
		t.Errorf("41-char literal should not fold with the default threshold")
	}
}
