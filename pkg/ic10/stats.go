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
func StatsOf(code string) Stats {
	s := Stats{Bytes: len(code)}
	trimmed := strings.TrimSuffix(code, "\n")
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
