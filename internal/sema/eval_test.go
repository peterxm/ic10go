package sema

import "testing"

func TestConstCallsPureFunction(t *testing.T) {
	info, diags := check(t, `
func triple(x num) num { return x * 3 }
func pick(x num) num {
	if x > 2 { return 10 }
	return 20
}
const K = triple(3)
const P = pick(triple(1))
`)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %+v", diags.Diags)
	}
	if got := info.Consts["K"]; got != 9 {
		t.Errorf("K = %v, want 9", got)
	}
	if got := info.Consts["P"]; got != 10 {
		t.Errorf("P = %v, want 10", got)
	}
}

func TestConstFoldsBuiltins(t *testing.T) {
	// sin(0)=0, clamp(5,0,3)=3, lerp(0,10,0.5)=5, sgn(-4)=-1, max(2,7)=7 → 14
	info, diags := check(t, `
const S = sin(0) + clamp(5, 0, 3) + lerp(0, 10, 0.5) + sgn(-4) + max(2, 7)
const L = lerp(10, 0, 2)     // t clamped to 1 → 0
`)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %+v", diags.Diags)
	}
	if got := info.Consts["S"]; got != 14 {
		t.Errorf("S = %v, want 14", got)
	}
	if got := info.Consts["L"]; got != 0 {
		t.Errorf("L = %v, want 0 (t is clamped to 1)", got)
	}
}

func TestConstRejectsImpureFunction(t *testing.T) {
	cases := []string{
		"func f(x num) num { yield()\n return x }\nconst K = f(1)",
		"func f() num { return d0.Temperature }\nconst K = f()",
		"func f() num { n := 0\n for { n = n + 1 }\n return n }\nconst K = f()",
		"func g() num { return 1 }\nfunc f() num { return d0.On + g() }\nconst K = f()",
	}
	for _, src := range cases {
		_, diags := check(t, src)
		if !diags.HasErrors() {
			t.Errorf("expected a compile-time error for:\n%s", src)
		}
	}
}

func TestDataFromPureFunction(t *testing.T) {
	info, diags := check(t, `
func square(x num) num { return x * x }
data T = [square(0), square(1), square(2)]
`)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %+v", diags.Diags)
	}
	tbl := info.DataIndex["T"]
	if tbl == nil {
		t.Fatal("data table T missing")
	}
	want := []string{"0", "1", "4"}
	if len(tbl.Values) != len(want) {
		t.Fatalf("T = %v, want %v", tbl.Values, want)
	}
	for i, w := range want {
		if tbl.Values[i] != w {
			t.Errorf("T[%d] = %q, want %q", i, tbl.Values[i], w)
		}
	}
}

// TestConstLoopFolds checks the interpreter handles for/assign/incdec.
func TestConstLoopFolds(t *testing.T) {
	info, diags := check(t, `
func sumTo(n num) num {
	s := 0
	for i := 0; i < n; i++ { s += i }
	return s
}
func countTo(n num) num {
	i := 0
	for {
		if i >= n { break }
		i++
	}
	return i
}
const A = sumTo(5)
const B = countTo(7)
`)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %+v", diags.Diags)
	}
	if got := info.Consts["A"]; got != 10 {
		t.Errorf("A = %v, want 10", got)
	}
	if got := info.Consts["B"]; got != 7 {
		t.Errorf("B = %v, want 7", got)
	}
}

func TestDataComprehension(t *testing.T) {
	info, diags := check(t, `
func sq(i num) num { return i * i }
data Squares = [sq(i) for i in 0..4]
data Doubles = [i * 2 for i in 1..3]
`)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %+v", diags.Diags)
	}
	checkValues(t, info, "Squares", []string{"0", "1", "4", "9", "16"})
	checkValues(t, info, "Doubles", []string{"2", "4", "6"})
}

func TestDataComprehensionRejectsNonConstantBounds(t *testing.T) {
	_, diags := check(t, `
func main() { d0.On = 1 }
data T = [i for i in 0..n]
`)
	if !diags.HasErrors() {
		t.Error("expected an error for a non-constant comprehension bound")
	}
}

func checkValues(t *testing.T, info *Info, name string, want []string) {
	t.Helper()
	tbl := info.DataIndex[name]
	if tbl == nil {
		t.Fatalf("data table %s missing", name)
	}
	if len(tbl.Values) != len(want) {
		t.Fatalf("%s = %v, want %v", name, tbl.Values, want)
	}
	for i, w := range want {
		if tbl.Values[i] != w {
			t.Errorf("%s[%d] = %q, want %q", name, i, tbl.Values[i], w)
		}
	}
}
