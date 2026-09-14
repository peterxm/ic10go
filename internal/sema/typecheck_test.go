package sema

import (
	"strings"
	"testing"

	"ic10go/internal/diag"
	"ic10go/internal/lexer"
	"ic10go/internal/parser"
	"ic10go/internal/source"
)

func check(t *testing.T, src string) (*Info, *diag.Bag) {
	t.Helper()
	file := source.NewFile("t.icg", []byte(src))
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)
	tree := parser.Parse(file, toks, diags)
	info := Check(tree, diags)
	return info, diags
}

func hasDiag(diags *diag.Bag, substr string) bool {
	for _, d := range diags.Diags {
		if strings.Contains(d.Msg, substr) {
			return true
		}
	}
	return false
}

func TestTypeCheckAcceptsValid(t *testing.T) {
	src := `const M = str("Ready!")
data T = [1, 2, 3]
func clamp(x num, lo num, hi num) num {
    if x < lo { return lo }
    if x > hi { return hi }
    return x
}
func occupied(dev, first, last) num {
    n := 0
    for i := first; i < last; i++ { n += dev.slot[i].Occupied }
    return n
}
func main() {
    d0.Setting = clamp(d1.Temperature, 0, 100)
    d1.Setting = M
    d2.Setting = str("Ready!")
    x := T[0] + occupied(d0, 2, 4)
    d3.On = x > 0
}`
	_, diags := check(t, src)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags.Diags)
	}
}

func TestTypeCheckUnknownType(t *testing.T) {
	_, diags := check(t, "func f(x foo) num { return 1 }\nfunc main() {}\n")
	if !hasDiag(diags, `unknown type "foo"`) {
		t.Errorf("expected an unknown-type error, got %v", diags.Diags)
	}
}

func TestTypeCheckDuplicateLocal(t *testing.T) {
	_, diags := check(t, "func main() { x := 1\n x := 2\n d0.On = x }\n")
	if !hasDiag(diags, `"x" redeclared`) {
		t.Errorf("expected a redeclaration error, got %v", diags.Diags)
	}
}

func TestTypeCheckMissingReturnValue(t *testing.T) {
	_, diags := check(t, "func f() num { return }\nfunc main() {}\n")
	if !hasDiag(diags, "missing return value") {
		t.Errorf("expected a missing-return-value error, got %v", diags.Diags)
	}
}

func TestTypeCheckMissingReturnWarning(t *testing.T) {
	_, diags := check(t, "func f() num { d0.On = 1 }\nfunc main() { d1.On = f() }\n")
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags.Diags)
	}
	if !hasDiag(diags, "missing return at end") {
		t.Errorf("expected a missing-return warning, got %v", diags.Diags)
	}
}

func TestTypeCheckDeviceAsNumber(t *testing.T) {
	_, diags := check(t, "func main() { x := d0 + 1\n d1.On = x }\n")
	if !hasDiag(diags, "expected a number, found device") {
		t.Errorf("expected a device-as-number error, got %v", diags.Diags)
	}
}

func TestTypeCheckNoFalsePositiveOnStr(t *testing.T) {
	_, diags := check(t, "func main() { d0.Setting = str(\"Ready!\") }\n")
	if diags.HasErrors() {
		t.Fatalf("str display string must not be an error: %v", diags.Diags)
	}
}

func TestTypeCheckAnnotatesExpressions(t *testing.T) {
	info, diags := check(t, "func main() { x := d1.Temperature\n d0.On = x > 0 }\n")
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags.Diags)
	}
	if len(info.ExprTypes) == 0 {
		t.Fatal("ExprTypes is empty")
	}
	var sawNum, sawBool bool
	for _, ty := range info.ExprTypes {
		switch ty {
		case Num:
			sawNum = true
		case Bool:
			sawBool = true
		}
	}
	if !sawNum || !sawBool {
		t.Errorf("expected both num and bool annotations, got num=%v bool=%v", sawNum, sawBool)
	}
}
