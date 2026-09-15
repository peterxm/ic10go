package ic10asm

import "strings"

// Format reflows native IC10 source into a canonical layout: one space between
// tokens, labels at column 0, comments kept after the code, runs of blank lines
// collapsed to one, and a single trailing newline. Quoted strings and `# KEEP`
// style comments are preserved.
func Format(src string) string {
	return format(src, false)
}

// FormatAligned is Format plus column alignment of contiguous instruction
// blocks (mnemonic and operand columns padded to a common width).
func FormatAligned(src string) string {
	return format(src, true)
}

const (
	fBlank = iota
	fComment
	fLabel
	fInstr
)

type fmtEntry struct {
	kind    int
	label   string
	toks    []string
	comment string
}

func format(src string, align bool) string {
	raw := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	entries := make([]fmtEntry, 0, len(raw))
	for _, l := range raw {
		code, comment := SplitComment(l)
		code = strings.TrimSpace(code)
		comment = strings.TrimSpace(comment)
		switch {
		case code == "" && comment == "":
			entries = append(entries, fmtEntry{kind: fBlank})
		case code == "":
			entries = append(entries, fmtEntry{kind: fComment, comment: comment})
		case IsLabel(code):
			entries = append(entries, fmtEntry{kind: fLabel, label: strings.TrimSuffix(code, ":"), comment: comment})
		default:
			entries = append(entries, fmtEntry{kind: fInstr, toks: Tokenize(code), comment: comment})
		}
	}
	if align {
		alignEntries(entries)
	}

	var out []string
	prevBlank := false
	for _, e := range entries {
		var s string
		switch e.kind {
		case fBlank:
			s = ""
		case fComment:
			s = e.comment
		case fLabel:
			s = e.label + ":"
			if e.comment != "" {
				s += " " + e.comment
			}
		case fInstr:
			s = strings.TrimRight(strings.Join(e.toks, " "), " ")
			if e.comment != "" {
				s += " " + e.comment
			}
		}
		if s == "" {
			if prevBlank || len(out) == 0 {
				continue
			}
			prevBlank = true
		} else {
			prevBlank = false
		}
		out = append(out, s)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n") + "\n"
}

// alignEntries pads the token columns of each contiguous instruction block.
func alignEntries(entries []fmtEntry) {
	for i := 0; i < len(entries); {
		if entries[i].kind != fInstr {
			i++
			continue
		}
		j := i
		for j < len(entries) && entries[j].kind == fInstr {
			j++
		}
		var widths []int
		for k := i; k < j; k++ {
			for c, t := range entries[k].toks {
				if c >= len(widths) {
					widths = append(widths, 0)
				}
				if len(t) > widths[c] {
					widths[c] = len(t)
				}
			}
		}
		for k := i; k < j; k++ {
			for c := range entries[k].toks {
				entries[k].toks[c] += strings.Repeat(" ", widths[c]-len(entries[k].toks[c]))
			}
		}
		i = j
	}
}
