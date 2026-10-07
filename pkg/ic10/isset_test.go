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

// `if isSet(x)` also fuses when x is a runtime device operand: the register
// holds a ReferenceId and IC10's device operand is `d?|r?|id`.
func TestFuseIsSetBranchRuntime(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"func main() {\n    d := pop()\n    if isSet(d) { d1.On = 1 }\n}\n", "bdns r"},
		{"func main() {\n    d := pop()\n    if isUnset(d) { d1.On = 1 }\n}\n", "bdse r"},
		{"func main() {\n    d := pop()\n    if !isSet(d) { d1.On = 1 }\n}\n", "bdse r"},
		{"func main() {\n    d := pop()\n    if !isUnset(d) { d1.On = 1 }\n}\n", "bdns r"},
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

// The `x = isSet(d); if x` fusion also handles a runtime device operand.
func TestFuseIsSetValueBranchRuntime(t *testing.T) {
	src := "func main() {\n    d := pop()\n    x := isSet(d)\n    if x { d1.On = 1 }\n}\n"
	code, diags, err := ic10.Compile("t.icg", []byte(src))
	if err != nil || diags.HasErrors() {
		t.Fatalf("compile: err=%v diags=%v", err, diags.Diags)
	}
	if !strings.Contains(code, "bdns r") {
		t.Fatalf("want fused bdns r:\n%s", code)
	}
	if strings.Contains(code, "sdse") {
		t.Fatalf("isSet not fused (sdse remains):\n%s", code)
	}
}

// isLoadValid / isStoreValid accept a runtime device operand too: IC10's
// bdnvl/bdnvs take `device(d?|r?|id)`.
func TestValidityRuntimeDevice(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"func main() {\n    d := pop()\n    if isLoadValid(d, \"Temperature\") { d0.Setting = 1 }\n}\n", "bdnvl r"},
		{"func main() {\n    d := pop()\n    if isStoreValid(d, \"On\") { d0.Setting = 1 }\n}\n", "bdnvs r"},
		{"func main() {\n    d := pop()\n    if !isLoadValid(d, \"Temperature\") { d0.Setting = 1 }\n}\n", "bdnvl r"},
	}
	for _, c := range cases {
		code, diags, err := ic10.Compile("t.icg", []byte(c.src))
		if err != nil || diags.HasErrors() {
			t.Fatalf("compile %q: err=%v diags=%v", c.src, err, diags.Diags)
		}
		if !strings.Contains(code, c.want) {
			t.Fatalf("%q: want %q:\n%s", c.src, c.want, code)
		}
	}
}
