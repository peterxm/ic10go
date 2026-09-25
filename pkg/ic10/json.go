package ic10

import (
	"strings"

	"ic10go/internal/diag"
	"ic10go/internal/source"
)

// APIVersion is the version of the machine-readable interface (the JSON
// document returned by BuildJSON). It is bumped only on breaking changes.
const APIVersion = 1

// Limits describes the fixed IC10 editor budget.
type Limits struct {
	Lines   int `json:"lines"`
	Bytes   int `json:"bytes"`
	MaxLine int `json:"maxLine"`
	Regs    int `json:"regs"`
}

// LimitsOf returns the default IC10 editor limits enforced by the compiler.
func LimitsOf() Limits {
	return limitsOf(Options{})
}

// LimitsFor returns the IC10 editor limits in effect for opts, after applying
// the defaults and any IC10C_MAX_* environment overrides. Tools that
// report a budget without compiling use it.
func LimitsFor(opts Options) Limits {
	return limitsOf(stackEnv(opts))
}

// limitsOf converts the (already env-merged) options into the public Limits.
func limitsOf(opts Options) Limits {
	lim := opts.editorLimits()
	return Limits{Lines: lim.Lines, Bytes: lim.Bytes, MaxLine: lim.LineLen, Regs: NumRegs}
}

// Position is a 1-based source position.
type Position struct {
	Line   int `json:"line"`
	Col    int `json:"col"`
	Offset int `json:"offset"`
}

// Range is a source span. End equals Start when the compiler does not track the
// span's end.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Diagnostic is a machine-readable diagnostic. Code is stable and never
// localised; Message follows the compiler's output language.
type Diagnostic struct {
	Severity string `json:"severity"`
	Code     string `json:"code,omitempty"`
	File     string `json:"file,omitempty"`
	Range    Range  `json:"range"`
	Message  string `json:"message"`
}

// DataSegment describes the one-time loader and, if the program has one, the
// persistent stack data segment. When Needed is true the host must run Loader
// once before installing the runtime Code. Setup is set when the loader also
// carries hoisted one-time device writes (modes / switches / settings) rather
// than only data-segment writes.
type DataSegment struct {
	Needed bool   `json:"needed"`
	Setup  bool   `json:"setup,omitempty"`
	Loader string `json:"loader,omitempty"`
	// Loaders splits Loader into chunks that each fit the chip editor; run them
	// in order. Omitted for a single chunk (Loader holds it).
	Loaders  []string `json:"loaders,omitempty"`
	Start    int      `json:"start"`
	End      int      `json:"end"`
	Sentinel int      `json:"sentinel"`
	Access   string   `json:"access"`
	Layout   string   `json:"layout"`
}

// ChipJSON is one chip's output in a multi-chip build.
type ChipJSON struct {
	// Name is the chip block's name; empty for a single-chip program.
	Name   string   `json:"name,omitempty"`
	Code   string   `json:"code"`
	Lines  []string `json:"lines"`
	Stats  Stats    `json:"stats"`
	Loader string   `json:"loader,omitempty"`
	// Loaders splits Loader into chip-sized chunks; run them in order.
	Loaders []string `json:"loaders,omitempty"`
	// Setup reports whether Loader carries hoisted one-time device writes.
	Setup bool `json:"setup,omitempty"`
}

// BuildResult is the JSON document emitted by `ic10c build --json`. It is a
// superset of `ic10c stats` and carries the compiled code, the optional data
// loader and any diagnostics.
type BuildResult struct {
	APIVersion  int          `json:"apiVersion"`
	OK          bool         `json:"ok"`
	Code        string       `json:"code"`
	Lines       []string     `json:"lines"`
	Data        DataSegment  `json:"data"`
	Chips       []ChipJSON   `json:"chips"`
	Stats       Stats        `json:"stats"`
	Limits      Limits       `json:"limits"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// BuildJSON compiles src and returns a machine-readable result.
//
// Source-level problems are reported in Diagnostics with OK=false; the
// returned error is only for unexpected backend failures (for example exceeding
// an IC10 limit) and is also mirrored into Diagnostics, so callers that only
// inspect the result never need to check it.
func BuildJSON(name string, src []byte, opts Options) (BuildResult, error) {
	res := BuildResult{
		APIVersion:  APIVersion,
		Lines:       []string{},
		Chips:       []ChipJSON{},
		Diagnostics: []Diagnostic{},
		Limits:      LimitsFor(opts),
		Data:        DataSegment{Access: accessName(opts), Layout: layoutName(opts)},
	}

	compiled, diags, err := CompileResult(name, src, opts)
	if diags != nil {
		diags.Sort()
		for _, d := range diags.Diags {
			res.Diagnostics = append(res.Diagnostics, convertDiag(d))
		}
	}
	// The top-level data/loader mirror the first chip for single-chip consumers;
	// chips[] is authoritative for multi-chip programs.
	multi := false
	for _, ch := range compiled.Chips {
		if ch.Name != "" {
			multi = true
		}
		res.Chips = append(res.Chips, ChipJSON{
			Name:    ch.Name,
			Code:    ch.Code,
			Lines:   splitLines(ch.Code),
			Stats:   StatsOf(ch.Code),
			Loader:  ch.Loader,
			Loaders: ch.Loaders,
			Setup:   ch.Setup,
		})
	}
	if compiled.Loader != "" {
		res.Data.Needed = true
		res.Data.Loader = compiled.Loader
		res.Data.Loaders = compiled.Loaders
		res.Data.Setup = compiled.Setup
	}
	// The data segment range can be inspected independently of code generation,
	// so report it even when compilation fails (for example on a line overrun).
	if !multi {
		if base, size, _, _ := DataStats(name, src, opts); base >= 0 {
			res.Data.Needed = true
			res.Data.Sentinel = base
			res.Data.Start = base
			res.Data.End = base + size - 1
		}
	}
	if err != nil {
		res.Diagnostics = append(res.Diagnostics, Diagnostic{
			Severity: "error",
			Code:     "codegen-error",
			File:     name,
			Range:    pointRange(source.Pos{File: name, Line: 1, Col: 1}),
			Message:  err.Error(),
		})
		return res, err
	}
	if diags != nil && diags.HasErrors() {
		return res, nil
	}

	res.OK = true
	res.Code = compiled.Code
	res.Lines = splitLines(compiled.Code)
	res.Stats = StatsOf(compiled.Code)
	return res, nil
}

// BuildJSONSource is BuildJSON for a string source.
func BuildJSONSource(name, src string, opts Options) (BuildResult, error) {
	return BuildJSON(name, []byte(src), opts)
}

func convertDiag(d diag.Diagnostic) Diagnostic {
	out := Diagnostic{
		Severity: d.Severity.String(),
		Code:     d.Code,
		File:     d.Pos.File,
		Range:    pointRange(d.Pos),
		Message:  d.Msg,
	}
	if d.End.IsValid() {
		out.Range.End = position(d.End)
	}
	return out
}

func pointRange(p source.Pos) Range {
	pos := position(p)
	return Range{Start: pos, End: pos}
}

func position(p source.Pos) Position {
	return Position{Line: p.Line, Col: p.Col, Offset: p.Offset}
}

func splitLines(code string) []string {
	if code == "" {
		return []string{}
	}
	return strings.Split(strings.TrimSuffix(code, "\n"), "\n")
}

func accessName(opts Options) string {
	if opts.DataAccessStack {
		return "stack"
	}
	return "get"
}

func layoutName(opts Options) string {
	if opts.DataLayout == "middle" {
		return "middle"
	}
	return "top"
}
