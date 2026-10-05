package token

import (
	"math"
	"testing"
)

func TestAngleUnits(t *testing.T) {
	if v, ok := ConvertUnit("180deg"); !ok || math.Abs(v-math.Pi) > 1e-9 {
		t.Fatalf("180deg = %v (ok=%v), want pi", v, ok)
	}
	if v, ok := ConvertUnit("1rad"); !ok || v != 1 {
		t.Fatalf("1rad = %v (ok=%v), want 1", v, ok)
	}
}
