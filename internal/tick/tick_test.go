package tick

import (
	"strings"
	"testing"
)

func TestStraightSegments(t *testing.T) {
	rep, err := Analyze("move r0 1\nyield\nmove r0 2\nmove r0 3\nyield\n", 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Segments) != 2 {
		t.Fatalf("segments = %d, want 2 (%s)", len(rep.Segments), rep)
	}
	if rep.Segments[0].Cost != 2 || rep.Segments[1].Cost != 3 {
		t.Fatalf("costs = %d,%d want 2,3", rep.Segments[0].Cost, rep.Segments[1].Cost)
	}
}

func TestLoopBottomTestedFits(t *testing.T) {
	// r0 = 0; do { r0 += 1 } while r0 < 4; yield
	rep, err := Analyze("move r0 0\nadd r0 r0 1\nblt r0 4 1\nyield\n", 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Loops) != 1 {
		t.Fatalf("loops = %d, want 1 (%s)", len(rep.Loops), rep)
	}
	if rep.Loops[0].Trips != 4 {
		t.Fatalf("trips = %d, want 4", rep.Loops[0].Trips)
	}
	if rep.Segments[0].Cost != 10 || rep.Segments[0].Exceeds {
		t.Fatalf("cost = %d exceeds=%v, want 10 false", rep.Segments[0].Cost, rep.Segments[0].Exceeds)
	}
}

func TestLoopHeaderTestedFits(t *testing.T) {
	// r0 = 0; while r0 < 5 { r0 += 1 }; yield  (check at header)
	rep, err := Analyze("move r0 0\nbge r0 5 4\nadd r0 r0 1\nj 1\nyield\n", 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Loops) != 1 || rep.Loops[0].Trips != 5 {
		t.Fatalf("loops = %v, want 1 with trips 5 (%s)", rep.Loops, rep)
	}
}

func TestLoopExceeds(t *testing.T) {
	// 8 iterations of a 20-instruction body: 8*20 = 160 > 128.
	var b strings.Builder
	b.WriteString("move r0 0\n")   // 0
	b.WriteString("bge r0 8 22\n") // 1 header/exit -> 22
	for i := 0; i < 18; i++ {      // 2..19 body filler
		b.WriteString("add r1 r1 1\n")
	}
	b.WriteString("add r0 r0 1\n") // 20
	b.WriteString("j 1\n")         // 21
	b.WriteString("yield\n")       // 22
	rep, err := Analyze(b.String(), 128)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Segments[0].Exceeds {
		t.Fatalf("expected segment to exceed 128 (%s)", rep)
	}
}

func TestTickLoopReported(t *testing.T) {
	// yield; r0=0; do { r0+=1 } while r0<4; j 0
	// The outer loop spans the yield (the tick loop) and is listed with Spans=true;
	// the inner counting loop is listed too.
	src := "yield\nmove r0 0\nadd r0 r0 1\nblt r0 4 2\nj 0\n"
	rep, err := Analyze(src, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Loops) != 2 {
		t.Fatalf("loops = %d, want 2 (outer tick loop + inner):\n%s", len(rep.Loops), rep)
	}
	var inner, outer *Loop
	for i := range rep.Loops {
		switch rep.Loops[i].Header {
		case 0:
			outer = &rep.Loops[i]
		case 2:
			inner = &rep.Loops[i]
		}
	}
	if outer == nil || !outer.Spans {
		t.Fatalf("outer tick loop missing or not Spans: %+v", rep.Loops)
	}
	if inner == nil || inner.Trips != 4 {
		t.Fatalf("inner loop = %+v, want header 2 trips 4", rep.Loops)
	}
	if rep.Segments[1].Exceeds {
		t.Fatalf("segment should fit:\n%s", rep)
	}
}

func TestSourceMapAndDominant(t *testing.T) {
	src := "yield\nmove r0 0\nadd r0 r0 1\nblt r0 4 2\nj 0\n"
	// 1-based IC10 line -> source line.
	lineMap := []int{0, 10, 11, 12, 13, 14}
	rep, err := AnalyzeOpts(src, Options{Limit: 128, LineMap: lineMap})
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.Segments[1].Source; got != 11 {
		t.Fatalf("segment source = %d, want 11", got)
	}
	var inner *Loop
	for i := range rep.Loops {
		if rep.Loops[i].Header == 2 {
			inner = &rep.Loops[i]
		}
	}
	if inner == nil || inner.Source != 12 {
		t.Fatalf("loop source = %+v, want a loop with header source 12", rep.Loops)
	}
	if inner.SourceStart != 12 || inner.SourceEnd < inner.SourceStart {
		t.Fatalf("loop source range = %d..%d, want start 12 and end >= start", inner.SourceStart, inner.SourceEnd)
	}
	if rep.Segments[1].Dominant != 2 || rep.Segments[1].DominantSource != 12 {
		t.Fatalf("dominant = %d (src %d), want 2 (src 12)",
			rep.Segments[1].Dominant, rep.Segments[1].DominantSource)
	}
	if len(rep.Segments[1].SourcePath) != len(rep.Segments[1].Path) {
		t.Fatalf("sourcePath length %d != path %d", len(rep.Segments[1].SourcePath), len(rep.Segments[1].Path))
	}
}

func TestDominantOnExceed(t *testing.T) {
	var b strings.Builder
	b.WriteString("yield\n")       // 0
	b.WriteString("move r0 0\n")   // 1
	b.WriteString("bge r0 8 23\n") // 2 header/exit -> 23 (hcf)
	for i := 0; i < 18; i++ {
		b.WriteString("add r1 r1 1\n")
	}
	b.WriteString("add r0 r0 1\n") // 21
	b.WriteString("j 2\n")         // 22
	b.WriteString("hcf\n")         // 23
	rep, err := Analyze(b.String(), 128)
	if err != nil {
		t.Fatal(err)
	}
	seg := rep.Segments[1]
	if !seg.Exceeds {
		t.Fatalf("expected exceed:\n%s", rep)
	}
	if seg.Dominant != 2 || seg.DominantCost <= 100 {
		t.Fatalf("dominant = %d cost %d, want header 2 and a large cost", seg.Dominant, seg.DominantCost)
	}
}

// A `j ra` must not connect to a return site that sits inside its own
// continuation: the compiled `jal f` / `move ra r` / `j ra` chain would
// otherwise invent a loop that never executes (the `ra` value has changed).
func TestNoPhantomReturnLoop(t *testing.T) {
	src := "yield\n" + // 0
		"jal 6\n" + // 1  (ra = 2)
		"move ra 0\n" + // 2
		"s d0 Setting 1\n" + // 3
		"j ra\n" + // 4  (ra = 0)
		"hcf\n" + // 5
		"s d1 Setting 1\n" + // 6
		"j ra\n" // 7  (ra = 2)
	rep, err := Analyze(src, 128)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range rep.Loops {
		if l.Header == 2 {
			t.Fatalf("phantom return loop hdr=2 from an unreachable return: %+v", rep.Loops)
		}
	}
	found := false
	for _, l := range rep.Loops {
		if l.Header == 0 {
			found = true // the real, yield-spanning loop
		}
	}
	if !found {
		t.Fatalf("real yield loop missing: %+v", rep.Loops)
	}
}

// TestMultiCallSiteSummary pins the fix for an outlined function called from
// several sites: the `ra` analysis is context-insensitive, so `j ra` used to
// connect to every return site and invent a phantom loop, reporting the segment
// as unbounded. Each callee is summarised now, so a call is one step that costs
// the callee and returns to the site after the call.
func TestMultiCallSiteSummary(t *testing.T) {
	src := "yield\n" + // 0
		"jal 7\n" + // 1  (ra = 2)
		"jal 7\n" + // 2  (ra = 3)
		"j 0\n" + // 3
		"hcf\n" + // 4
		"hcf\n" + // 5
		"hcf\n" + // 6
		"s d0 Setting 1\n" + // 7  (callee)
		"j ra\n" // 8
	rep, err := Analyze(src, 128)
	if err != nil {
		t.Fatal(err)
	}
	seg := rep.Segments[len(rep.Segments)-1]
	if seg.Exceeds {
		t.Fatalf("multi-call segment reported unbounded:\n%s", rep)
	}
	// Two calls (each 1 + the 2-instruction callee) + `j 0` + the yield itself.
	if seg.Cost != 8 {
		t.Fatalf("segment cost = %d, want 8:\n%s", seg.Cost, rep)
	}
	for _, l := range rep.Loops {
		if l.Header == 7 {
			t.Fatalf("phantom loop at the callee:\n%s", rep)
		}
	}
}

// TestLoopCalleeSummary checks that a callee containing a counting loop is
// summarised with the same trip counts the direct analysis uses: two calls cost
// twice what a single call costs (the single call is computed directly, so it is
// the reference).
func TestLoopCalleeSummary(t *testing.T) {
	one := "yield\n" + // 0
		"jal 7\n" + // 1
		"j 0\n" + // 2
		"hcf\nhcf\nhcf\nhcf\n" + // 3..6
		"move r0 0\n" + // 7  (callee: r0 = 0; r0++ while r0 < 3)
		"add r0 r0 1\n" + // 8
		"blt r0 3 8\n" + // 9
		"j ra\n" // 10
	rep, err := Analyze(one, 128)
	if err != nil {
		t.Fatal(err)
	}
	single := rep.Segments[len(rep.Segments)-1]
	if single.Exceeds || single.Cost != 11 {
		t.Fatalf("single call = %+v, want 11 fits:\n%s", single, rep)
	}

	two := "yield\n" + // 0
		"jal 8\n" + // 1
		"jal 8\n" + // 2
		"j 0\n" + // 3
		"hcf\nhcf\nhcf\nhcf\n" + // 4..7
		"move r0 0\n" + // 8
		"add r0 r0 1\n" + // 9
		"blt r0 3 9\n" + // 10
		"j ra\n" // 11
	rep, err = Analyze(two, 128)
	if err != nil {
		t.Fatal(err)
	}
	double := rep.Segments[len(rep.Segments)-1]
	if double.Exceeds {
		t.Fatalf("looped callee reported unbounded:\n%s", rep)
	}
	// one yield + two calls (each 1 + the 8-instruction callee) + the `j 0`.
	if double.Cost != 20 {
		t.Fatalf("two calls = %d, want 20:\n%s", double.Cost, rep)
	}
}

func TestUnknownTripIsStillBounded(t *testing.T) {
	// A self-loop with no detectable induction still must terminate the DP.
	rep, err := Analyze("yield\nmove r0 1\nj 1\n", 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(rep.Segments))
	}
	if !rep.Segments[1].Exceeds {
		t.Fatalf("self-loop segment should exceed (%s)", rep)
	}
}
