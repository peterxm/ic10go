package main

// Regression test for docs/review-2026-10-14.md T7: an unrecognised `--flag` to
// `run` used to be taken as the input filename, producing the misleading
// "open --rel-jump: no such file". It must be reported as an unknown flag.

import "testing"

func TestReviewT7RunUnknownFlag(t *testing.T) {
	for _, args := range [][]string{
		{"--rel-jump", "x.icg"},
		{"--no-such-flag", "x.icg"},
	} {
		if rc := cmdRun(args); rc != 2 {
			t.Errorf("run %v: exit code %d, want 2 (unknown flag)", args, rc)
		}
	}
	// A genuine file that does not exist is still a file error, not a flag error.
	if rc := cmdRun([]string{"definitely-missing.icg"}); rc == 2 {
		t.Errorf("a missing file should not be reported as an unknown flag")
	}
}
