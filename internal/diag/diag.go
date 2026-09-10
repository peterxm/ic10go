package diag

import (
	"fmt"
	"sort"

	"ic10go/internal/source"
)

type Severity int

const (
	Error Severity = iota
	Warning
	Note
)

func (s Severity) String() string {
	switch s {
	case Error:
		return "error"
	case Warning:
		return "warning"
	default:
		return "note"
	}
}

type Diagnostic struct {
	Severity Severity
	Pos      source.Pos
	Msg      string
}

func (d Diagnostic) String() string {
	if d.Pos.IsValid() {
		return fmt.Sprintf("%s: %s: %s", d.Pos, d.Severity, d.Msg)
	}
	return fmt.Sprintf("%s: %s", d.Severity, d.Msg)
}

// Bag collects diagnostics during compilation.
type Bag struct {
	Diags []Diagnostic
}

func (b *Bag) Add(sev Severity, pos source.Pos, format string, args ...any) {
	b.Diags = append(b.Diags, Diagnostic{Severity: sev, Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

func (b *Bag) Errorf(pos source.Pos, format string, args ...any) {
	b.Add(Error, pos, format, args...)
}

func (b *Bag) Warnf(pos source.Pos, format string, args ...any) {
	b.Add(Warning, pos, format, args...)
}

func (b *Bag) HasErrors() bool {
	for _, d := range b.Diags {
		if d.Severity == Error {
			return true
		}
	}
	return false
}

func (b *Bag) Len() int { return len(b.Diags) }

// Sort orders diagnostics by position.
func (b *Bag) Sort() {
	sort.SliceStable(b.Diags, func(i, j int) bool {
		a, c := b.Diags[i].Pos, b.Diags[j].Pos
		if a.Offset != c.Offset {
			return a.Offset < c.Offset
		}
		return b.Diags[i].Severity < b.Diags[j].Severity
	})
}
