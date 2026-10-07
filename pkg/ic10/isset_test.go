package ic10_test

import (
	"strings"
	"testing"

	"ic10go/pkg/ic10"
)

// `if isSet(d)` fuses into the IC10 bdse/bdns branch instead of a separate
// sdse and a register branch.
func TestFuseIsSetBranch(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"func main() {\n    if isSet(d0) { d1.On = 1 }\n}\n", "bdns d0 "},
		{"func main() {\n    if isUnset(d0) { d1.On = 1 }\n}\n", "bdse d0 "},
		{"func main() {\n    if !isSet(d0) { d1.On = 1 }\n}\n", "bdse d0 "},
		{"func main() {\n    if !isUnset(d0) { d1.On = 1 }\n}\n", "bdns d0 "},
	}
	for _, c := range cases {
		code, diags, err := ic10.Compile("t.icg", []byte(c.src))
		if err != nil || diags.HasErrors() {
			t.Fatalf("compile %q: err=%v diags=%v", c.src, err, diags.Diags)
		}
		if !strings.Contains(code, c.want) {
			t.Fatalf("%q: want %q:\n%s", c.src, c.want, code)
		}
		if strings.Contains(code, "sdse") || strings.Contains(code, "sdns") {
			t.Fatalf("%q: isSet not fused (sdse/sdns remains):\n%s", c.src, code)
		}
	}
}
