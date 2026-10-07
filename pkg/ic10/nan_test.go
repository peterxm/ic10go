package ic10_test

import (
	"strings"
	"testing"

	"ic10go/pkg/ic10"
)

// `if isNaN(x)` / `isNotNaN(x)` / `!isNaN(x)` fold into IC10's `bnan` branch
// instead of materialising `snan`/`snanz` and branching on the register.
func TestFuseNaN(t *testing.T) {
	srcs := []string{
		"func main() {\n    x := pop()\n    if isNaN(x) { d0.On = 1 }\n}\n",
		"func main() {\n    x := pop()\n    if isNaN(x) { d0.On = 1 } else { d0.On = 0 }\n}\n",
		"func main() {\n    x := pop()\n    if isNotNaN(x) { d0.On = 1 }\n}\n",
		"func main() {\n    x := pop()\n    if !isNaN(x) { d0.On = 1 }\n}\n",
		"func main() {\n    x := pop()\n    v := isNaN(x)\n    if v { d0.On = 1 }\n}\n",
	}
	for _, src := range srcs {
		code, diags, err := ic10.Compile("t.icg", []byte(src))
		if err != nil || diags.HasErrors() {
			t.Fatalf("compile %q: err=%v diags=%v", src, err, diags.Diags)
		}
		if !strings.Contains(code, "bnan ") {
			t.Fatalf("%q: want a bnan branch:\n%s", src, code)
		}
		if strings.Contains(code, "snan") || strings.Contains(code, "snanz") {
			t.Fatalf("%q: isNaN not fused (snan/snanz remains):\n%s", src, code)
		}
	}
}

// `if isNaN(a) || isNaN(b)` short-circuits into two `bnan` branches instead of
// `snan; snan; max; branch`.
func TestFuseNaNOr(t *testing.T) {
	src := "func main() {\n    a := pop()\n    b := pop()\n    if isNaN(a) || isNaN(b) { d0.On = 1 }\n}\n"
	code, diags, err := ic10.Compile("t.icg", []byte(src))
	if err != nil || diags.HasErrors() {
		t.Fatalf("compile: err=%v diags=%v", err, diags.Diags)
	}
	if strings.Count(code, "bnan ") != 2 {
		t.Fatalf("want two bnan branches:\n%s", code)
	}
	if strings.Contains(code, "snan") || strings.Contains(code, "max") {
		t.Fatalf("|| of isNaN not short-circuited:\n%s", code)
	}
}

// An ordering comparison whose operands may be NaN must not be negated:
// IC10's `bge` is not `!(blt)` when an operand is NaN (the game is IEEE). A
// batched read can be NaN (no matching device), a `nan` literal is NaN; a
// single-device read or an integer loop counter is a number and keeps the
// shorter negated form.
func TestOrderingBranchNaNSafe(t *testing.T) {
	opts := ic10.Options{NaNSafe: true}
	for _, src := range []string{
		"func main() {\n    for {\n        yield()\n        if batch.read(hash(\"X\"), \"Setting\", \"Sum\") > 100 { d1.On = 1 }\n    }\n}\n",
		"func main() {\n    for {\n        yield()\n        if nan < 1 { d1.On = 1 }\n    }\n}\n",
	} {
		code, diags, err := ic10.CompileWithOptions("t.icg", []byte(src), opts)
		if err != nil || diags.HasErrors() {
			t.Fatalf("compile %q: err=%v diags=%v", src, err, diags.Diags)
		}
		if strings.Contains(code, "ble ") || strings.Contains(code, "bge ") {
			t.Fatalf("%q: ordering branch negated for a possibly-NaN operand:\n%s", src, code)
		}
	}

	// An integer loop counter is provably not NaN, so the negated form is exact.
	code, diags, err := ic10.CompileWithOptions("t.icg", []byte(
		"func main() {\n    for i := 0; i < 5; i++ {\n        d0.Setting = i\n        yield()\n    }\n}\n"), opts)
	if err != nil || diags.HasErrors() {
		t.Fatalf("compile: err=%v diags=%v", err, diags.Diags)
	}
	if !strings.Contains(code, "bge ") {
		t.Fatalf("want the negated bge for an integer loop condition:\n%s", code)
	}
}

// With NaNSafe off (the default) the codegen negates ordering branches freely,
// which is shorter but only correct when no operand is NaN.
func TestOrderingBranchNaNDefault(t *testing.T) {
	code, diags, err := ic10.Compile("t.icg", []byte(
		"func main() {\n    for {\n        yield()\n        if d0.Temperature > 100 { d1.On = 1 }\n    }\n}\n"))
	if err != nil || diags.HasErrors() {
		t.Fatalf("compile: err=%v diags=%v", err, diags.Diags)
	}
	if !strings.Contains(code, "ble ") {
		t.Fatalf("want the shorter negated form by default:\n%s", code)
	}
}

// `if isNaN(x) { body }` must run the body when x is NaN, in both modes: a NaN
// branch is never negated (there is no branch-if-not-NaN).
func TestFuseNaNBodyRunsOnNaN(t *testing.T) {
	for _, opts := range []ic10.Options{{}, {NaNSafe: true}} {
		res, diags, err := ic10.CompileResult("t.icg", []byte(
			"func main() {\n    x := nan\n    r := 0\n    if isNaN(x) { r = r + 10 }\n    d0.On = r\n    yield()\n}\n"), opts)
		if err != nil || diags.HasErrors() {
			t.Fatalf("opts %+v: compile: err=%v diags=%v", opts, err, diags.Diags)
		}
		got, runErr := runWrites(res.Code, nil)
		if runErr {
			t.Fatalf("opts %+v: run error:\n%s", opts, res.Code)
		}
		found := false
		for _, w := range got {
			if w == "d0.On=10" {
				found = true
			}
		}
		if !found {
			t.Fatalf("opts %+v: isNaN(nan) body did not run; writes=%v\n%s", opts, got, res.Code)
		}
	}
}
