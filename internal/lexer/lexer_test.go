package lexer

import (
	"testing"

	"ic10go/internal/diag"
	"ic10go/internal/source"
	"ic10go/internal/token"
)

func lex(t *testing.T, src string) ([]token.Token, *diag.Bag) {
	t.Helper()
	file := source.NewFile("test.icg", []byte(src))
	diags := &diag.Bag{}
	toks := Tokenize(file, diags)
	return toks, diags
}

func kinds(toks []token.Token) []token.Kind {
	var ks []token.Kind
	for _, tk := range toks {
		ks = append(ks, tk.Kind)
	}
	return ks
}

func TestDeviceClassification(t *testing.T) {
	toks, diags := lex(t, "d0 d5 db d6 data db2")
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags.Diags)
	}
	want := []token.Kind{
		token.Device, token.Device, token.Device,
		token.Ident, token.Ident, token.Ident,
		token.Semicolon, token.EOF,
	}
	got := kinds(toks)
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", got, want)
		}
	}
}

func TestSemicolonInsertion(t *testing.T) {
	toks, _ := lex(t, "a = 1\nb = 2\n")
	want := []token.Kind{
		token.Ident, token.Assign, token.Number, token.Semicolon,
		token.Ident, token.Assign, token.Number, token.Semicolon,
		token.EOF,
	}
	got := kinds(toks)
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", got, want)
		}
	}
}

func TestNoSemicolonAfterOperator(t *testing.T) {
	toks, _ := lex(t, "a = 1 +\n2")
	want := []token.Kind{
		token.Ident, token.Assign, token.Number, token.Plus, token.Number, token.Semicolon, token.EOF,
	}
	got := kinds(toks)
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", got, want)
		}
	}
}

func TestCommentsAndStrings(t *testing.T) {
	toks, diags := lex(t, "// hi\n\"abc\\n\" /* block */ x")
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags.Diags)
	}
	if toks[0].Kind != token.String || toks[0].Text != "abc\n" {
		t.Fatalf("string = %q kind %v", toks[0].Text, toks[0].Kind)
	}
	if toks[1].Kind != token.Ident || toks[1].Text != "x" {
		t.Fatalf("ident = %q kind %v", toks[1].Text, toks[1].Kind)
	}
}

func TestOperators(t *testing.T) {
	toks, _ := lex(t, "+= -= *= /= %= &= |= ^= <<= >>= ++ -- := == != <= >= && || << >>")
	want := []token.Kind{
		token.PlusAssign, token.MinusAssign, token.StarAssign, token.SlashAssign,
		token.PercentAssign, token.AmpAssign, token.PipeAssign, token.CaretAssign,
		token.ShlAssign, token.ShrAssign, token.PlusPlus, token.MinusMinus,
		token.Define, token.Eq, token.Ne, token.Le, token.Ge, token.And, token.Or,
		token.Shl, token.Shr, token.EOF,
	}
	got := kinds(toks)
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", got, want)
		}
	}
}
