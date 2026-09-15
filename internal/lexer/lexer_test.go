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
	toks, diags := lex(t, "d0 d5 db d6 datum db2")
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

func TestUnitSuffix(t *testing.T) {
	toks, diags := lex(t, "20c 68f 300k 1.5C 20.1MPa 101.3kPa 101325Pa 1bar 1.5kW 2MW 3W 500ms 2min 1h 90s 180deg 1rad 50% 30pct 0x1f 0b10")
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags.Diags)
	}
	var texts []string
	for _, tk := range toks {
		if tk.Kind == token.Number {
			texts = append(texts, tk.Text)
		}
	}
	want := []string{
		"20c", "68f", "300k", "1.5C", "20.1MPa", "101.3kPa", "101325Pa", "1bar",
		"1.5kW", "2MW", "3W", "500ms", "2min", "1h", "90s", "180deg", "1rad", "50%", "30pct",
		"0x1f", "0b10",
	}
	if len(texts) != len(want) {
		t.Fatalf("number texts = %v, want %v", texts, want)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Fatalf("number texts = %v, want %v", texts, want)
		}
	}
}

func TestUnitSuffixBoundary(t *testing.T) {
	// A suffix must not swallow the start of a following identifier.
	toks, _ := lex(t, "20count 5price")
	want := []token.Kind{
		token.Number, token.Ident, token.Number, token.Ident, token.Semicolon, token.EOF,
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

func TestPercentVsModulo(t *testing.T) {
	// `50%` is a percent literal, but `50%2` / `50 % 2` are modulo because the
	// `%` is followed by (or separated from) an operand.
	cases := []struct {
		in   string
		want []token.Kind
	}{
		{"50%", []token.Kind{token.Number, token.Semicolon, token.EOF}},
		{"50%2", []token.Kind{token.Number, token.Percent, token.Number, token.Semicolon, token.EOF}},
		{"50 % 2", []token.Kind{token.Number, token.Percent, token.Number, token.Semicolon, token.EOF}},
		{"a%2", []token.Kind{token.Ident, token.Percent, token.Number, token.Semicolon, token.EOF}},
	}
	for _, c := range cases {
		toks, _ := lex(t, c.in)
		got := kinds(toks)
		if len(got) != len(c.want) {
			t.Fatalf("%q: kinds = %v, want %v", c.in, got, c.want)
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Fatalf("%q: kinds = %v, want %v", c.in, got, c.want)
			}
		}
	}
}
