package minify

import (
	"strings"
	"testing"
)

func TestCommentsBlankLinesAndLabels(t *testing.T) {
	src := "# header\n\nalias x d0\nloop:\n  l r0 x Ratio\n  j loop\n"
	got, err := Minify(src, Options{DeadCode: true})
	if err != nil {
		t.Fatal(err)
	}
	want := "l r0 d0 Ratio\nj 0\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestKeepFlags(t *testing.T) {
	src := "alias x d0\nloop:\nl r0 x Ratio\nj loop\n"
	got, err := Minify(src, Options{KeepDefines: true, KeepLabels: true, DeadCode: true})
	if err != nil {
		t.Fatal(err)
	}
	want := "alias x d0\nloop:\nl r0 x Ratio\nj loop\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestNumericTargetRenumbered(t *testing.T) {
	src := "move r0 1\n# comment\nj 3\nmove r0 2\n"
	got, err := Minify(src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := "move r0 1\nj 2\nmove r0 2\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDeadCodeRemoved(t *testing.T) {
	src := "move r0 1\nj 3\nmove r0 2\nmove r0 3\n"
	got, err := Minify(src, Options{DeadCode: true})
	if err != nil {
		t.Fatal(err)
	}
	want := "move r0 1\nj 2\nmove r0 3\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDynamicBranchDisablesDeadCode(t *testing.T) {
	src := "move r0 1\nj ra\nmove r0 2\n"
	got, err := Minify(src, Options{DeadCode: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "move r0 2") {
		t.Errorf("unreachable code removed despite dynamic branch:\n%s", got)
	}
}

func TestLongLineRejected(t *testing.T) {
	long := strings.Repeat("9", 100)
	src := "define X " + long + "\nmove r0 X\n"
	if _, err := Minify(src, Options{}); err == nil {
		t.Fatal("expected a line-length error")
	}
}

func TestKeepsDefinesWhenInliningOverflows(t *testing.T) {
	// Inlining X twice would exceed the line limit, so Minify keeps the define.
	src := "define X HASH(\"ItemPureIceVolatiles\")\nor X X SorterInstruction.FilterPrefabHashNotEquals\n"
	got, err := Minify(src, Options{})
	if err != nil {
		t.Fatalf("Minify: %v", err)
	}
	if !strings.Contains(got, "define X") {
		t.Errorf("define was not kept to stay under the line limit:\n%s", got)
	}
	for _, ln := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if len(ln) > MaxLineLen {
			t.Errorf("line over limit (%d): %q", len(ln), ln)
		}
	}
}
