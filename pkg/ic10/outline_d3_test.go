package ic10_test

import (
	"strings"
	"testing"

	"ic10go/pkg/ic10"
)

// A non-leaf helper (step calls clamp3) used by three control loops. D3a must
// outline step once with clamp3 inlined, instead of duplicating step three times.
const outlineNonLeafSrc = `
const Kp = 2.5
const Ki = 0.25

func clamp3(x num, lo num, hi num) num {
    if x < lo { return lo }
    if x > hi { return hi }
    return x
}

func step(error num) num {
    p := error * Kp
    i := error * Ki
    raw := p + i
    if raw > 5.0 { raw = 5.0 }
    if raw < -5.0 { raw = -5.0 }
    return clamp3(raw, -100.0, 100.0)
}

func main() {
    for i := 0; i < 5; i++ {
        d0.Setting = step(d1.Temperature - 10.0 - i)
        d2.Setting = step(d3.Temperature - 20.0 + i)
        d4.Setting = step(d5.Temperature - 30.0)
    }
}
`

// TestOutlineNonLeaf checks D3a: a non-leaf helper called several times is
// outlined (its callees inlined into the body), which shrinks the program while
// producing the same device write sequence as the all-inlined build.
func TestOutlineNonLeaf(t *testing.T) {
	t.Setenv("IC10C_NO_OPT", "")
	t.Setenv("IC10C_NO_OUTLINE", "")
	incl, diags, err := ic10.CompileResult("t.icg", []byte(outlineNonLeafSrc), ic10.Options{})
	if err != nil || diags.HasErrors() {
		t.Fatalf("default compile failed: %v %v", diags.Diags, err)
	}

	t.Setenv("IC10C_NO_OUTLINE", "1")
	plain, diags, err := ic10.CompileResult("t.icg", []byte(outlineNonLeafSrc), ic10.Options{})
	if err != nil || diags.HasErrors() {
		t.Fatalf("no-outline compile failed: %v %v", diags.Diags, err)
	}

	if !strings.Contains(incl.Code, "jal ") {
		t.Fatalf("non-leaf step was not outlined:\n%s", incl.Code)
	}
	lines := func(s string) int { return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1 }
	if lines(incl.Code) >= lines(plain.Code) {
		t.Fatalf("outlining did not shrink (%d vs %d lines)\n--- outlined ---\n%s\n--- inlined ---\n%s",
			lines(incl.Code), lines(plain.Code), incl.Code, plain.Code)
	}

	init := deviceInit(11)
	want, wantErr := runWrites(plain.Code, init)
	got, gotErr := runWrites(incl.Code, init)
	if gotErr != wantErr || strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("outlined and inlined differ (err %v vs %v)\n--- outlined ---\n%v\n--- inlined ---\n%v\n--- outlined code ---\n%s",
			gotErr, wantErr, got, want, incl.Code)
	}
}
