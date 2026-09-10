package ic10_test

import (
	"os"
	"path/filepath"
	"testing"

	"ic10go/pkg/ic10"
)

func TestFormatIdempotent(t *testing.T) {
	programs, err := filepath.Glob("../../testdata/programs/*.icg")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range programs {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		once, diags, err := ic10.Format(p, src)
		if diags.HasErrors() || err != nil {
			t.Fatalf("%s: first pass: diags=%v err=%v", p, diags.Diags, err)
		}
		twice, diags2, err := ic10.Format(p, []byte(once))
		if diags2.HasErrors() || err != nil {
			t.Fatalf("%s: second pass: diags=%v err=%v", p, diags2.Diags, err)
		}
		if once != twice {
			t.Errorf("%s: formatting is not idempotent:\n--- once ---\n%s\n--- twice ---\n%s", p, once, twice)
		}
	}
}

func TestStatsOf(t *testing.T) {
	s := ic10.StatsOf("move r0 1\ns d0 On r0\n")
	if s.Lines != 2 {
		t.Errorf("Lines = %d, want 2", s.Lines)
	}
	if s.RegsUsed != 1 {
		t.Errorf("RegsUsed = %d, want 1", s.RegsUsed)
	}
	if s.Bytes != len("move r0 1\ns d0 On r0\n") {
		t.Errorf("Bytes = %d", s.Bytes)
	}
}
