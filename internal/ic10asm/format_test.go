package ic10asm

import "testing"

func TestFormatNormalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"move   r0    1\n", "move r0 1\n"},
		{"  loop:\n", "loop:\n"},
		{"move r0 1   # hi\n", "move r0 1 # hi\n"},
		{"a\n\n\n\nb\n", "a\n\nb\n"},
		{"\n\nmove r0 1\n\n\n", "move r0 1\n"},
		{"", ""},
	}
	for _, c := range cases {
		if got := Format(c.in); got != c.want {
			t.Errorf("Format(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatAligned(t *testing.T) {
	in := "move r0 1\nadd r1 r2 r3\nyield\n"
	want := "move  r0 1\nadd   r1 r2 r3\nyield\n"
	if got := FormatAligned(in); got != want {
		t.Errorf("FormatAligned = %q, want %q", got, want)
	}
}

func TestFormatIdempotent(t *testing.T) {
	in := "alias s d0\n\n  move r0  1   # c\nadd r1 r2 r3\nL:\nblt r0 5 L\n"
	once := FormatAligned(in)
	if twice := FormatAligned(once); twice != once {
		t.Errorf("not idempotent:\n%s\n---\n%s", once, twice)
	}
}

func TestFormatKeepsQuotes(t *testing.T) {
	in := "move r0 HASH(\"Ingot  Dial\")\n"
	if got := Format(in); got != in {
		t.Errorf("Format = %q, want %q", got, in)
	}
}
