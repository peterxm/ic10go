package ic10

import (
	"regexp"
	"strconv"
	"strings"
)

// Stats summarises a generated IC10 program.
type Stats struct {
	Lines      int `json:"lines"`
	Bytes      int `json:"bytes"`
	MaxLineLen int `json:"maxLine"`
	RegsUsed   int `json:"regs"`
}

var regRe = regexp.MustCompile(`\br([0-9]+)\b`)

// StatsOf computes statistics for IC10 code.
//
// A trailing newline is not part of the program the chip stores: the upload
// path trims it and the game counts it as an empty last line, so the byte and
// line counts ignore trailing newlines (and CRLFs) too. Without this the editor
// reports one byte more than the chip shows.
func StatsOf(code string) Stats {
	trimmed := strings.TrimRight(code, "\r\n")
	s := Stats{Bytes: len(trimmed)}
	if trimmed != "" {
		lines := strings.Split(trimmed, "\n")
		s.Lines = len(lines)
		for _, ln := range lines {
			if len(ln) > s.MaxLineLen {
				s.MaxLineLen = len(ln)
			}
		}
	}
	seen := map[int]bool{}
	for _, m := range regRe.FindAllStringSubmatch(code, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			seen[n] = true
		}
	}
	s.RegsUsed = len(seen)
	return s
}
