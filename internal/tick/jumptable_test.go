package tick_test

import (
	"os"
	"strings"
	"testing"

	"ic10go/internal/tick"
	"ic10go/pkg/ic10"
)

// TestJumpTableIdiomAnalyzed pins P1 from docs/fast-statemachine.md: the
// verified codegen jump-table idiom (bounds check + `add/jr` + run of `j`
// entries) is recognised, so the analyzer reports an honest per-tick cost
// instead of the old conservative `EXCEEDS 129` false alarm. The recognized
// bound must not undercut the measured dynamic worst (10 instructions/tick,
// `ic10c tick --run 40` on the same build).
func TestJumpTableIdiomAnalyzed(t *testing.T) {
	src, err := os.ReadFile("../../testdata/bench/fast/var12_1.icg")
	if err != nil {
		t.Skip(err)
	}
	code, diags, err := ic10.CompileWithOptions("jt.icg", src, ic10.Options{JumpTable: true})
	if err != nil || diags.HasErrors() {
		t.Fatalf("compile: %v %v", err, diags.Diags)
	}
	if !strings.Contains(code, "jr ") {
		t.Fatalf("expected a jump table build:\n%s", code)
	}
	rep, err := tick.Analyze(code, tick.DefaultLimit)
	if err != nil {
		t.Fatal(err)
	}
	worst := 0
	for _, seg := range rep.Segments {
		if seg.Cost > worst {
			worst = seg.Cost
		}
		if seg.Exceeds {
			t.Errorf("segment L%d still reported as exceeding:\n%s", seg.Start, rep)
		}
	}
	if rep.Indirect {
		t.Errorf("table build still flagged indirect:\n%s", rep)
	}
	if worst > 12 {
		// dispatch (sub/bounds×4/add/jr) + entry + body + back jump ≈ 10-11
		t.Errorf("worst tick %d above the expected honest bound:\n%s", worst, rep)
	}
	if worst < 8 {
		t.Errorf("worst tick %d implausibly low (underestimate?):\n%s", worst, rep)
	}
}

// TestJumpTableUnprovenStaysConservative: a `jr rX` without the provable
// bounds idiom must keep the old widened/indirect treatment — the analyzer
// stays an upper bound, never a quiet underestimate.
func TestJumpTableUnprovenStaysConservative(t *testing.T) {
	const unproven = `
l r2 d0 Setting
add r2 r2 1
jr r2
j 7
s d0 On 1
j 8
s d0 On 2
hcf
`
	rep, err := tick.Analyze(unproven, tick.DefaultLimit)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Indirect {
		t.Errorf("unproven jr must stay indirect:\n%s", rep)
	}
}

// TestJumpTableHoledNotRecognized: a label line inside the entry run breaks
// the raw-line adjacency the game's relative `jr` depends on; the idiom must
// then not be recognized (conservative path).
func TestJumpTableHoledNotRecognized(t *testing.T) {
	const holed = `
l r2 d0 Setting
sltz r1 r2
sgt r0 r2 2
or r0 r1 r0
bnez r0 10
add r2 r2 1
jr r2
j 11
lbl:
s d0 On 1
s d0 On 2
`
	rep, err := tick.Analyze(holed, tick.DefaultLimit)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Indirect {
		t.Errorf("table with a label hole must not be recognized:\n%s", rep)
	}
}
