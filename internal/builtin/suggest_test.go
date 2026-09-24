package builtin

import "testing"

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"abc", "ab", 1},
		{"abc", "", 3},
		{"Temperatur", "Temperature", 1},
		{"kitten", "sitting", 3},
	}
	for _, c := range cases {
		if got := Levenshtein(c.a, c.b); got != c.want {
			t.Errorf("Levenshtein(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestClosest(t *testing.T) {
	cands := []string{"Alpha", "Beta", "Gamma"}
	if got := Closest("Alhpa", cands); got != "Alpha" {
		t.Errorf("Closest = %q, want Alpha", got)
	}
	if got := Closest("Zzzzzzzzz", cands); got != "" {
		t.Errorf("far misspelling should suggest nothing, got %q", got)
	}
	if got := Closest("Alpha", cands); got != "" {
		t.Errorf("an exact match is not a correction, got %q", got)
	}
}

func TestClosestIsDeterministic(t *testing.T) {
	// "Ab" is one edit from both "Abc" and "Abd"; the lexicographically
	// smaller must win so the diagnostic is stable across runs.
	for i := 0; i < 10; i++ {
		if got := Closest("Ab", []string{"Abd", "Abc"}); got != "Abc" {
			t.Fatalf("Closest = %q, want Abc", got)
		}
	}
}

func TestClosestLogicType(t *testing.T) {
	if got := ClosestLogicType("Temperatur"); got != "Temperature" {
		t.Errorf("ClosestLogicType = %q, want Temperature", got)
	}
	if got := ClosestLogicType("On"); got != "" {
		t.Errorf("a valid logic type needs no correction, got %q", got)
	}
}

func TestClosestSlotType(t *testing.T) {
	if got := ClosestSlotType("Quantit"); got != "Quantity" {
		t.Errorf("ClosestSlotType = %q, want Quantity", got)
	}
}

func TestClosestEnumMember(t *testing.T) {
	if got := ClosestEnumMember("Color.Blck"); got != "Color.Black" {
		t.Errorf("ClosestEnumMember = %q, want Color.Black", got)
	}
	if got := ClosestEnumMember("GasType.Oxygn"); got != "GasType.Oxygen" {
		t.Errorf("ClosestEnumMember = %q, want GasType.Oxygen", got)
	}
	if got := ClosestEnumMember("Foo.Bar"); got != "" {
		t.Errorf("an unknown receiver should suggest nothing, got %q", got)
	}
}
