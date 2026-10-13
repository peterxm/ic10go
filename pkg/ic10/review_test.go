package ic10_test

// Regression tests for docs/review-2026-10-14.md. Each case is that report's
// minimal reproduction, so a regression names itself instead of being found
// again by hand.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"ic10go/internal/diag"
	"ic10go/pkg/ic10"
)

// wantsError compiles src and requires at least one error mentioning want ("" =
// any error).
func wantsError(t *testing.T, src, want string) {
	t.Helper()
	_, diags, err := ic10.Compile("t.icg", []byte(src))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !diags.HasErrors() {
		t.Errorf("expected an error mentioning %q, got none:\n%s", want, src)
		return
	}
	if want != "" {
		found := false
		for _, d := range diags.Diags {
			if strings.Contains(d.Msg, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no diagnostic mentioning %q: %v", want, diags.Diags)
		}
	}
}

// wantsClean compiles src and requires no errors.
func wantsClean(t *testing.T, src string) string {
	t.Helper()
	code, diags, err := ic10.Compile("t.icg", []byte(src))
	if err != nil || diags.HasErrors() {
		t.Fatalf("expected a clean compile: %v %v\n%s", diags.Diags, err, src)
	}
	return code
}

// TestReviewC1NaNDoesNotHang: folding a NaN constant used to make constProp/CSE
// never converge (`a.V == b.V` is false for NaN), freezing both the compiler and
// the LSP on the same program. Compiling must finish promptly.
func TestReviewC1NaNDoesNotHang(t *testing.T) {
	for _, src := range []string{
		"func main() {\n\tB := 1/0\n\td0.Setting = B\n}\n",
		"func main() {\n\tB := 0.0/0.0\n\td0.Setting = B\n}\n",
		"func main() {\n\tB := 1%0\n\td0.Setting = B\n}\n",
		"func main() {\n\tB := sqrt(0-1)\n\td0.Setting = B\n}\n",
	} {
		done := make(chan string, 1)
		go func() {
			code, _, _ := ic10.Compile("t.icg", []byte(src))
			done <- code
		}()
		select {
		case code := <-done:
			if code == "" {
				t.Errorf("%s: empty output", src)
			}
		case <-time.After(15 * time.Second):
			t.Fatalf("%s: compile did not finish (C1 regression: a NaN breaks the optimizer's fixed point)", src)
		}
	}
}

// TestReviewC4IEEEFolding: div/mod by zero follows IEEE on every path, and the
// two folding paths agree. 1/0 is +Inf everywhere (`pinf`); 0/0 and x%0 are NaN
// and must stay runtime instructions -- a folded NaN constant is what froze the
// compiler (C1), and a bare NaN literal written to a device halts the chip.
func TestReviewC4IEEEFolding(t *testing.T) {
	// The const path and the local path must produce the same operand.
	constForm := "const X = 1/0\nfunc main() {\n\td0.Setting = X\n}\n"
	localForm := "func main() {\n\tX := 1/0\n\td0.Setting = X\n}\n"
	constCode := wantsClean(t, constForm)
	localCode := wantsClean(t, localForm)
	for _, c := range []string{constCode, localCode} {
		if !strings.Contains(c, "s d0 Setting pinf") {
			t.Errorf("1/0 should fold to +Inf on both paths:\n%s", c)
		}
	}
	if _, diags, _ := ic10.Compile("t.icg", []byte(localForm)); !hasMsg(diags, "division by zero") {
		t.Errorf("the local 1/0 path should warn about division by zero: %v", diags.Diags)
	}
	code := wantsClean(t, "func main() {\n\tB := 0/0\n\td0.Setting = B\n}\n")
	if !strings.Contains(code, "div ") {
		t.Errorf("0/0 must stay a runtime division:\n%s", code)
	}
	if _, diags, _ := ic10.Compile("t.icg", []byte("func main() {\n\tB := 0/0\n\td0.Setting = B\n}\n")); !hasMsg(diags, "NaN") {
		t.Errorf("0/0 should warn about NaN: %v", diags.Diags)
	}
	code = wantsClean(t, "func main() {\n\tB := 1%0\n\td0.Setting = B\n}\n")
	if !strings.Contains(code, "mod ") {
		t.Errorf("1%%0 must stay a runtime modulo:\n%s", code)
	}
}

// hasMsg reports whether any diagnostic message contains want.
func hasMsg(diags *diag.Bag, want string) bool {
	if diags == nil {
		return false
	}
	for _, d := range diags.Diags {
		if strings.Contains(d.Msg, want) {
			return true
		}
	}
	return false
}

// TestReviewC2MalformedLiterals: 0b12 / 0b2 / 0x / 1e999 / a 65-bit hex literal
// used to compile silently to 0 or a truncated value.
func TestReviewC2MalformedLiterals(t *testing.T) {
	for _, lit := range []string{"0b12", "0b2", "0x", "1e999", "0xFFFFFFFFFFFFFFFFF"} {
		wantsError(t, "func main() {\n\td0.Setting = "+lit+"\n}\n", "")
	}
}

// TestReviewC3StrayExpressionStatement: a value expression used as a statement
// was accepted and silently dropped (`d1.Setting = 1 2` lost the 2).
func TestReviewC3StrayExpressionStatement(t *testing.T) {
	wantsError(t, "func main() {\n\td1.Setting = 1 2\n}\n", "")
	wantsError(t, "func main() {\n\t1 + 1\n}\n", "no effect")
}

// TestReviewM2DuplicateDefault: two default clauses silently kept the last one.
func TestReviewM2DuplicateDefault(t *testing.T) {
	wantsError(t, "func main() {\n\tswitch d0.Setting {\n\tdefault:\n\t\td1.On = 1\n\tdefault:\n\t\td1.On = 0\n\t}\n}\n", "multiple default")
}

// TestReviewM3NameConflicts: the conflict matrix missed function-vs-raw/str
// constants and duplicate constants.
func TestReviewM3NameConflicts(t *testing.T) {
	wantsError(t, "const Msg = str(\"hi\")\nfunc Msg() {\n\td0.On = 1\n}\nfunc main() {\n\td0.On = 1\n}\n", "conflicts")
	wantsError(t, "const Msg = 5\nconst Msg = 6\nfunc main() {\n\td0.On = 1\n}\n", "redeclared")
}

// TestReviewM4SwitchCaseScope: each case body is its own block (as in Go), so
// the same name in two cases is not a redeclaration.
func TestReviewM4SwitchCaseScope(t *testing.T) {
	wantsClean(t, "func main() {\n\tswitch d0.Setting {\n\tcase 1:\n\t\ty := 1\n\t\td1.On = y\n\tcase 2:\n\t\ty := 2\n\t\td1.On = y\n\t}\n}\n")
}

// TestReviewM5ChannelRange: d0.channel[0][9] used to emit Channel9, which the
// game rejects at runtime.
func TestReviewM5ChannelRange(t *testing.T) {
	wantsError(t, "func main() {\n\td0.channel[0][9] = 1\n}\n", "")
}

// TestReviewL1TopLevelVar: a top-level var was ignored until it was referenced.
func TestReviewL1TopLevelVar(t *testing.T) {
	wantsError(t, "var Ghost = 5\nfunc main() {\n\td0.On = 1\n}\n", "")
}

// TestReviewT6Recursion: recursive programs were accepted or rejected depending
// on the outlining switch. It must be a static error either way.
func TestReviewT6Recursion(t *testing.T) {
	src := "func f(n) {\n\treturn f(n)\n}\nfunc main() {\n\td0.Setting = f(1)\n}\n"
	wantsError(t, src, "recursive")
	t.Setenv("IC10C_NO_OUTLINE", "1")
	wantsError(t, src, "recursive")
}

// TestReviewC5DynamicStackFull: a full dynamic data segment computed the
// sentinel slot as -1 and emitted `put db -1`, which halts the chip on the real
// machine. The boundary is 512 items (512 + 1 sentinel > 512 slots).
func TestReviewC5DynamicStackFull(t *testing.T) {
	table := func(n int) string {
		var b strings.Builder
		b.WriteString("data tbl = [\n")
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "\t%d,\n", i)
		}
		b.WriteString("]\nfunc main() {\n\tfor {\n\t\tyield()\n\t\td0.Setting = tbl[d1.Setting]\n\t}\n}\n")
		return b.String()
	}
	if _, diags, _ := ic10.CompileWithOptions("t.icg", []byte(table(512)), ic10.Options{DynamicStack: true}); !diags.HasErrors() {
		t.Errorf("512 items (+1 sentinel) must be rejected: %v", diags.Diags)
	}
	if _, diags, _ := ic10.CompileWithOptions("t.icg", []byte(table(511)), ic10.Options{DynamicStack: true}); diags.HasErrors() {
		t.Errorf("511 items should fit: %v", diags.Diags)
	}
}
