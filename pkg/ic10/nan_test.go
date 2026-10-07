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
