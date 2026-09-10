package builtin

import "testing"

// These values were read from the in-game HASH() function (and from device
// NameHash fields) and confirm that IC10's hash is CRC-32/IEEE interpreted as a
// signed 32-bit integer. The empty string is rejected by the game editor, so it
// is not covered.
func TestHashMatchesGame(t *testing.T) {
	cases := map[string]int32{
		"a":                       -390611389,
		"A":                       -740712821,
		"a b":                     -2140381997,
		"café":                    -1733475659,
		"温度":                      -272818494,
		"Hello, World!":           -330644528,
		"!@#$%^&*()":              -1365075048,
		"StructureSolarPanelDual": -539224550,
		"StructureBattery":        -400115994,
	}
	for s, want := range cases {
		if got := int32(Hash(s)); got != want {
			t.Errorf("Hash(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestHashKnownConstants(t *testing.T) {
	// Cross-checked against the constants used in the real ic10code scripts.
	if got := int32(Hash("StructureSolarPanelDual")); got != -539224550 {
		t.Errorf("solar panel hash = %d", got)
	}
	if got := int32(Hash("StructureBattery")); got != -400115994 {
		t.Errorf("battery hash = %d", got)
	}
}
