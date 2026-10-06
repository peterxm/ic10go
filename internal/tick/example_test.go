package tick_test

import (
	"os"
	"testing"

	"ic10go/internal/tick"
	"ic10go/pkg/ic10"
)

// TestPrinterExample compiles the table-driven printer controller and checks the
// analyzer against its known shape: the fastStep loop runs 8 iterations of a
// 26-instruction worst-case body, so its worst-case tick exceeds 128.
func TestPrinterExample(t *testing.T) {
	const path = "../../examples/自动打印机产量控制-多机-排料保护-表驱动.icg"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Skip("example not present:", err)
	}
	code, diags, err := ic10.Compile(path, src)
	if err != nil || diags.HasErrors() {
		t.Fatalf("compile: err=%v diags=%v", err, diags.Diags)
	}
	rep, err := tick.Analyze(code, tick.DefaultLimit)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + rep.String())

	last := rep.Segments[len(rep.Segments)-1]
	if !last.Exceeds {
		t.Fatalf("main segment should exceed %d:\n%s", tick.DefaultLimit, rep)
	}
	found := false
	for _, l := range rep.Loops {
		if l.Trips == 8 && l.Body > 20 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the fastStep loop (trips=8, body>20):\n%s", rep)
	}
}
