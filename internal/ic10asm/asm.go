// Package ic10asm holds small shared helpers for reading IC10 assembly text:
// comment stripping, quote-aware tokenisation, label detection and branch
// mnemonic parsing.
package ic10asm

import "strings"

// StripComment removes a trailing # comment, ignoring # inside strings.
func StripComment(line string) string {
	code, _ := SplitComment(line)
	return code
}

// SplitComment returns the code before a # comment and the comment text
// (including the leading #), ignoring # inside strings.
func SplitComment(line string) (code, comment string) {
	inStr := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inStr = !inStr
		case '#':
			if !inStr {
				return line[:i], line[i:]
			}
		}
	}
	return line, ""
}

// Tokenize splits a line into whitespace-separated tokens, keeping quoted
// strings intact so that HASH("Ingot Dial") stays a single token.
func Tokenize(line string) []string {
	var toks []string
	var cur strings.Builder
	inStr := false
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '"':
			inStr = !inStr
			cur.WriteByte(c)
		case !inStr && (c == ' ' || c == '\t'):
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return toks
}

// IsLabel reports whether a line is a bare label definition ("name:").
func IsLabel(line string) bool {
	if !strings.HasSuffix(line, ":") {
		return false
	}
	return !strings.ContainsAny(line[:len(line)-1], " \t")
}

// Conditions understood by BranchInfo.
var conditions = map[string]bool{
	"eq": true, "ne": true, "lt": true, "le": true, "gt": true, "ge": true,
	"eqz": true, "nez": true, "ltz": true, "lez": true, "gtz": true, "gez": true,
	"nan": true, "ap": true, "na": true, "apz": true, "naz": true,
	"dns": true, "dse": true, "dnvl": true, "dnvs": true,
}

// BranchInfo splits a branch mnemonic into its condition and flags.
//
// It understands the plain forms (beq), the -al forms (beqal) and the relative
// forms (breq), where the relative forms drop the leading "b" after "br".
func BranchInfo(op string) (cond string, relative, withRA, ok bool) {
	s := op
	if strings.HasSuffix(s, "al") {
		withRA = true
		s = strings.TrimSuffix(s, "al")
	}
	switch {
	case strings.HasPrefix(s, "br"):
		relative = true
		cond = strings.TrimPrefix(s, "br")
	case strings.HasPrefix(s, "b"):
		cond = strings.TrimPrefix(s, "b")
	default:
		return "", false, false, false
	}
	return cond, relative, withRA, conditions[cond]
}

// TargetIndex returns the argument index holding the jump target for a branch
// condition.
func TargetIndex(cond string) int {
	switch cond {
	case "eq", "ne", "lt", "le", "gt", "ge":
		return 2
	case "ap", "na":
		return 3
	case "apz", "naz":
		return 2
	default:
		return 1
	}
}

// IsBinaryCond reports whether a branch condition compares two operands.
func IsBinaryCond(cond string) bool {
	switch cond {
	case "eq", "ne", "lt", "le", "gt", "ge":
		return true
	}
	return false
}
