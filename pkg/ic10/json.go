package ic10

import (
	"fmt"
	"strings"

	"ic10go/internal/diag"
	"ic10go/internal/source"
	"ic10go/internal/tick"
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
	// LineMap maps a 1-based runtime IC10 line to the 1-based .icg source line
	// it came from (0 when unknown), so an editor can follow execution.
	LineMap []int `json:"lineMap,omitempty"`
	// Vars is the debugger watch table: which source variable is in which
	// register (r0..r15) at each source line.
	Vars []Var `json:"vars,omitempty"`
}

// TickInfo is the per-tick instruction budget analysis (see `ic10c tick`): the
// worst-case instructions between two yield/sleep boundaries and whether any
// segment exceeds the budget. The worst chip wins for a multi-chip program.
type TickInfo struct {
	Limit    int  `json:"limit"`
	Cost     int  `json:"cost"`
	Exceeds  bool `json:"exceeds"`
	Loops    int  `json:"loops"`
	Segments int  `json:"segments"`
	// TicksPerRound is how many ticks the loop that spans ticks needs per pass
	// (1 = it finishes within a tick). See tick.Report.TicksPerRound.
	TicksPerRound int `json:"ticksPerRound"`
}

// Score grades a build on both hard limits: the line count and the worst per-tick
// instruction count. The grade comes from the **worse** axis on purpose -- a
// weighted average would hide a hard failure and would let saving lines pay for a
// tick regression. D means a limit is exceeded (a line overrun does not upload);
// A/B/C are headroom bands of the worse axis.
type Score struct {
	Lines         int     `json:"lines"`
	LineLimit     int     `json:"lineLimit"`
	LineUsed      float64 `json:"lineUsed"` // lines / lineLimit
	TickWorst     int     `json:"tickWorst"`
	TickLimit     int     `json:"tickLimit"`
	TickUsed      float64 `json:"tickUsed"` // tickWorst / tickLimit
	TicksPerRound int     `json:"ticksPerRound"`
	Grade         string  `json:"grade"`
}

// ScoreFor grades one compiled runtime against the line limit (the tick limit is
// tick.DefaultLimit). It is what `ic10c build --score` prints.
func ScoreFor(code string, lineLimit int) Score {
	if lineLimit <= 0 {
		lineLimit = 128
	}
	tickLimit := tick.DefaultLimit
	sc := Score{Lines: len(splitLines(code)), LineLimit: lineLimit, TickLimit: tickLimit}
	sc.LineUsed = float64(sc.Lines) / float64(lineLimit)
	if t := tickInfo(code); t != nil {
		sc.TickWorst = t.Cost
		sc.TicksPerRound = t.TicksPerRound
	}
	sc.TickUsed = float64(sc.TickWorst) / float64(tickLimit)
	worst := sc.LineUsed
	if sc.TickUsed > worst {
		worst = sc.TickUsed
	}
	switch {
	case sc.Lines > lineLimit || sc.TickWorst > tickLimit:
		sc.Grade = "D"
	case worst >= 0.75:
		sc.Grade = "C"
	case worst >= 0.5:
		sc.Grade = "B"
	default:
		sc.Grade = "A"
	}
	return sc
}

// ScoreOf grades the built result. It uses the static worst per-tick cost; run
// `ic10c tick --run N` for the measured steady state, which is often much lower
// (an overrun on a rarely-taken path).
func ScoreOf(res BuildResult) Score {
	lineLimit := res.Limits.Lines
	if lineLimit <= 0 {
		lineLimit = 128
	}
	tickLimit := tick.DefaultLimit
	sc := Score{Lines: len(res.Lines), LineLimit: lineLimit, TickLimit: tickLimit}
	sc.LineUsed = float64(sc.Lines) / float64(lineLimit)
	if res.Tick != nil {
		sc.TickWorst = res.Tick.Cost
		sc.TicksPerRound = res.Tick.TicksPerRound
	}
	sc.TickUsed = float64(sc.TickWorst) / float64(tickLimit)
	worst := sc.LineUsed
	if sc.TickUsed > worst {
		worst = sc.TickUsed
	}
	switch {
	case sc.Lines > lineLimit || sc.TickWorst > tickLimit:
		sc.Grade = "D"
	case worst >= 0.75:
		sc.Grade = "C"
	case worst >= 0.5:
		sc.Grade = "B"
	default:
		sc.Grade = "A"
	}
	return sc
}

// String renders the score as one line for a terminal or CI log.
func (s Score) String() string {
	round := ""
	if s.TicksPerRound > 1 {
		round = fmt.Sprintf(" · round >=%d ticks", s.TicksPerRound)
	}
	return fmt.Sprintf("%s: lines %d/%d (%.0f%%) · worst tick %d/%d (%.0f%%)%s",
		s.Grade, s.Lines, s.LineLimit, s.LineUsed*100, s.TickWorst, s.TickLimit, s.TickUsed*100, round)
}

// BuildResult is the JSON document emitted by `ic10c build --json`. It is a
// superset of `ic10c stats` and carries the compiled code, the optional data
// loader and any diagnostics.
type BuildResult struct {
	APIVersion int         `json:"apiVersion"`
	OK         bool        `json:"ok"`
	Code       string      `json:"code"`
	Lines      []string    `json:"lines"`
	Data       DataSegment `json:"data"`
	Chips      []ChipJSON  `json:"chips"`
	Stats      Stats       `json:"stats"`
	Limits     Limits      `json:"limits"`
	Tick       *TickInfo   `json:"tick,omitempty"`
	Score      *Score      `json:"score,omitempty"`
	// LineMap maps a 1-based runtime IC10 line to the 1-based .icg source line
	// it came from (0 when unknown). It mirrors the first chip.
	LineMap []int `json:"lineMap,omitempty"`
	// Vars is the debugger watch table, mirroring the first chip.
	Vars        []Var        `json:"vars,omitempty"`
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
			LineMap: ch.LineMap,
			Vars:    ch.Vars,
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
	res.LineMap = compiled.LineMap
	res.Vars = compiled.Vars
	res.Tick = tickInfo(compiled.Code)
	if multi {
		for _, ch := range compiled.Chips {
			if t := tickInfo(ch.Code); t != nil && (res.Tick == nil || t.Cost > res.Tick.Cost || (t.Exceeds && !res.Tick.Exceeds)) {
				res.Tick = t
			}
		}
	}
	sc := ScoreOf(res)
	res.Score = &sc
	return res, nil
}

// tickInfo analyses one program's per-tick budget, or nil on error.
func tickInfo(code string) *TickInfo {
	rep, err := tick.Analyze(code, tick.DefaultLimit)
	if err != nil {
		return nil
	}
	info := &TickInfo{Limit: rep.Limit, Loops: len(rep.Loops), Segments: len(rep.Segments), TicksPerRound: rep.TicksPerRound}
	for _, s := range rep.Segments {
		if s.Cost > info.Cost {
			info.Cost = s.Cost
		}
		if s.Exceeds {
			info.Exceeds = true
		}
	}
	return info
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
