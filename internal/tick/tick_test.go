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

func TestTickLoopNotReported(t *testing.T) {
	// yield; r0=0; do { r0+=1 } while r0<4; j 0
	// The outer loop spans the yield (the tick loop) and must not be listed; the
	// inner counting loop is.
	src := "yield\nmove r0 0\nadd r0 r0 1\nblt r0 4 2\nj 0\n"
	rep, err := Analyze(src, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Loops) != 1 {
		t.Fatalf("loops = %d, want 1 (only the inner loop):\n%s", len(rep.Loops), rep)
	}
	if rep.Loops[0].Header != 2 || rep.Loops[0].Trips != 4 {
		t.Fatalf("loop = %+v, want header 2 trips 4", rep.Loops[0])
	}
	if rep.Segments[1].Exceeds {
		t.Fatalf("segment should fit:\n%s", rep)
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
