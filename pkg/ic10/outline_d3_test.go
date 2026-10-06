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

// A helper outlined and called twice, with BOTH results combined. The first
// result must survive the second call: the outlined call defines its shared
// result register, so a value copied out of it interferes with the next call
// and must not coalesce into it. Regression for the D3a result-clobber bug.
const outlineResultSumSrc = `
func f(x num) num {
    return x*2 + x*3 + x*5 + 1
}

func main() {
    for i := 0; i < 4; i++ {
        d0.Setting = f(d1.Setting) + f(d1.Setting + 1)
    }
}
`

func TestOutlineResultAcrossCalls(t *testing.T) {
	t.Setenv("IC10C_NO_OPT", "")
	t.Setenv("IC10C_NO_OUTLINE", "")
	out, diags, err := ic10.CompileResult("t.icg", []byte(outlineResultSumSrc), ic10.Options{})
	if err != nil || diags.HasErrors() {
		t.Fatalf("default compile failed: %v %v", diags.Diags, err)
	}
	if !strings.Contains(out.Code, "jal ") {
		t.Fatalf("f was not outlined:\n%s", out.Code)
	}
	t.Setenv("IC10C_NO_OUTLINE", "1")
	plain, diags, err := ic10.CompileResult("t.icg", []byte(outlineResultSumSrc), ic10.Options{})
	if err != nil || diags.HasErrors() {
		t.Fatalf("no-outline compile failed: %v %v", diags.Diags, err)
	}
	init := deviceInit(7)
	want, wantErr := runWrites(plain.Code, init)
	got, gotErr := runWrites(out.Code, init)
	if gotErr != wantErr || strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("outlined result was clobbered (err %v vs %v)\n--- outlined ---\n%v\n--- inlined ---\n%v\n--- outlined code ---\n%s",
			gotErr, wantErr, got, want, out.Code)
	}
}

// A device-parameter helper called with different device constants. Outlining
// shares one body (ports passed as numbers, IC10 drN) instead of inlining it at
// each call site.
const outlineDeviceSrc = `
func control(src, dst, lo, hi) {
    v := src.Setting
    if v < lo { v = lo }
    if v > hi { v = hi }
    scaled := v * 3
    offset := scaled - 30
    dst.On = abs(offset) > 5
}

func main() {
    for i := 0; i < 6; i++ {
        control(d4, d0, 0, 100)
        control(d0, d3, 0, 100)
    }
}
`

// TestOutlineDeviceParams checks that a helper taking device ports is outlined
// (the ports become runtime registers read through drN) and produces the same
// device writes as the all-inlined build.
func TestOutlineDeviceParams(t *testing.T) {
	t.Setenv("IC10C_NO_OPT", "")
	t.Setenv("IC10C_NO_OUTLINE", "")
	out, diags, err := ic10.CompileResult("t.icg", []byte(outlineDeviceSrc), ic10.Options{})
	if err != nil || diags.HasErrors() {
		t.Fatalf("default compile failed: %v %v", diags.Diags, err)
	}
	if !strings.Contains(out.Code, "jal ") {
		t.Fatalf("device-parameter helper was not outlined:\n%s", out.Code)
	}
	if !strings.Contains(out.Code, "dr") {
		t.Fatalf("outlined body did not use a register-selected device:\n%s", out.Code)
	}

	t.Setenv("IC10C_NO_OUTLINE", "1")
	plain, diags, err := ic10.CompileResult("t.icg", []byte(outlineDeviceSrc), ic10.Options{})
	if err != nil || diags.HasErrors() {
		t.Fatalf("no-outline compile failed: %v %v", diags.Diags, err)
	}
	lines := func(s string) int { return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1 }
	if lines(out.Code) >= lines(plain.Code) {
		t.Fatalf("outlining did not shrink (%d vs %d lines)\n--- outlined ---\n%s\n--- inlined ---\n%s",
			lines(out.Code), lines(plain.Code), out.Code, plain.Code)
	}

	init := deviceInit(23)
	want, wantErr := runWrites(plain.Code, init)
	got, gotErr := runWrites(out.Code, init)
	if gotErr != wantErr || strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("outlined and inlined differ (err %v vs %v)\n--- outlined ---\n%v\n--- inlined ---\n%v\n--- outlined code ---\n%s",
			gotErr, wantErr, got, want, out.Code)
	}
}
