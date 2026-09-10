package ic10asm

import (
	"reflect"
	"testing"
)

func TestTokenizeQuotes(t *testing.T) {
	got := Tokenize(`sbn Dial HASH("Ingot Dial") Mode 17`)
	want := []string{"sbn", "Dial", `HASH("Ingot Dial")`, "Mode", "17"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize = %#v, want %#v", got, want)
	}
}

func TestStripComment(t *testing.T) {
	if got := StripComment(`s db Setting 5 # comment`); got != "s db Setting 5 " {
		t.Errorf("StripComment = %q", got)
	}
	if got := StripComment(`s db Setting HASH("a#b")`); got != `s db Setting HASH("a#b")` {
		t.Errorf("StripComment mangled a string: %q", got)
	}
}

func TestBranchInfo(t *testing.T) {
	cases := []struct {
		op, cond     string
		relative, ra bool
		ok           bool
	}{
		{"beq", "eq", false, false, true},
		{"beqal", "eq", false, true, true},
		{"breq", "eq", true, false, true},
		{"brnez", "nez", true, false, true},
		{"bapz", "apz", false, false, true},
		{"bgtz", "gtz", false, false, true},
		{"add", "", false, false, false},
	}
	for _, c := range cases {
		cond, rel, ra, ok := BranchInfo(c.op)
		if cond != c.cond || rel != c.relative || ra != c.ra || ok != c.ok {
			t.Errorf("BranchInfo(%q) = %q,%v,%v,%v; want %q,%v,%v,%v",
				c.op, cond, rel, ra, ok, c.cond, c.relative, c.ra, c.ok)
		}
	}
}

func TestTargetIndex(t *testing.T) {
	if TargetIndex("eq") != 2 || TargetIndex("eqz") != 1 || TargetIndex("ap") != 3 {
		t.Errorf("unexpected target indices")
	}
}
