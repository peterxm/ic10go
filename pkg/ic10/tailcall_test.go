package ic10_test

import (
	"strings"
	"testing"

	"ic10go/pkg/ic10"
)

// An infinite-style loop whose last statement is an outlined device helper, so
// the call is a tail call: the compiler sets `ra` to the loop head and jumps to
// the body instead of `jal` + a return-block jump. The loop is bounded so the
// write sequence is deterministic.
const tailCallSrc = `
func control(src, dst, lo, hi) {
    v := src.Setting
    if v < lo { v = lo }
    if v > hi { v = hi }
    scaled := v * 3
    offset := scaled - 30
    dst.On = abs(offset) > 5
}

func main() {
    i := 0
    for i < 6 {
        i++
        control(d4, d0, 0, 100)
        control(d0, d3, 0, 100)
    }
}
`

// TestTailCall checks that a call whose return block only jumps onward becomes a
// tail call (`move ra <cont>; j <body>`), saving the `jal` plus the return jump,
// and that it behaves exactly like the unoptimised build.
func TestTailCall(t *testing.T) {
	t.Setenv("IC10C_NO_OPT", "")
	t.Setenv("IC10C_NO_OUTLINE", "")
	out, diags, err := ic10.CompileResult("t.icg", []byte(tailCallSrc), ic10.Options{})
	if err != nil || diags.HasErrors() {
		t.Fatalf("default compile failed: %v %v", diags.Diags, err)
	}
	if !strings.Contains(out.Code, "move ra") {
		t.Fatalf("no tail call (move ra) in:\n%s", out.Code)
	}
	// The first call stays a `jal`; the tail call does not.
	if n := strings.Count(out.Code, "jal "); n != 1 {
		t.Fatalf("expected exactly one jal, got %d:\n%s", n, out.Code)
	}

	t.Setenv("IC10C_NO_OPT", "1")
	raw, diags, err := ic10.CompileResult("t.icg", []byte(tailCallSrc), ic10.Options{})
	if err != nil || diags.HasErrors() {
		t.Fatalf("unoptimised compile failed: %v %v", diags.Diags, err)
	}
	lines := func(s string) int { return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1 }
	if lines(out.Code) >= lines(raw.Code) {
		t.Fatalf("tail call did not shrink (%d vs %d lines)\n--- tail ---\n%s\n--- raw ---\n%s",
			lines(out.Code), lines(raw.Code), out.Code, raw.Code)
	}

	init := deviceInit(42)
	want, wantErr := runWrites(raw.Code, init)
	got, gotErr := runWrites(out.Code, init)
	if gotErr != wantErr || strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("tail call changed behaviour (err %v vs %v)\n--- tail ---\n%v\n--- raw ---\n%v\n--- tail code ---\n%s",
			gotErr, wantErr, got, want, out.Code)
	}
}
