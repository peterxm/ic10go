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
