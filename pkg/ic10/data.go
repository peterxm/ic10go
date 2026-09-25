package ic10

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"ic10go/internal/codegen"
	"ic10go/internal/diag"
	"ic10go/internal/lexer"
	"ic10go/internal/parser"
	"ic10go/internal/sema"
	"ic10go/internal/source"
	"ic10go/internal/token"
)

// StackSize is the number of slots in an IC10 chip's persistent stack.
const StackSize = sema.StackSize

// DataLoader compiles the one-time loader that installs the source's persistent
// data segment into the IC housing's stack. It returns "" when the source has
// no `data` tables.
//
// The loader is plain IC10 (a sequence of `put db <addr> <value>` lines); run
// it once, then replace the chip's code with the compiled runtime.
func DataLoader(name string, src []byte) (string, error) {
	return DataLoaderWithOptions(name, src, Options{})
}

// DataLoaderWithOptions is DataLoader with explicit options. With
// DataAccessStack the loader uses `poke` (the chip's own stack), matching a
// runtime compiled the same way.
func DataLoaderWithOptions(name string, src []byte, opts Options) (string, error) {
	info, err := analyzeWithOptions(name, src, opts)
	if err != nil {
		return "", err
	}
	return dataLoaderFor(info, opts)
}

// dataLoaderFor renders the persistent-stack loader for a checked program's
// data segment. It returns "" when the program has no data tables.
func dataLoaderFor(info *sema.Info, opts Options) (string, error) {
	if len(info.Data) == 0 {
		return "", nil
	}
	write := func(addr int, v string) string {
		if opts.DataAccessStack {
			return fmt.Sprintf("poke %d %s\n", addr, v)
		}
		return fmt.Sprintf("put db %d %s\n", addr, v)
	}
	var b strings.Builder
	b.WriteString(write(info.Sentinel, formatDataFloat(info.DataVersion)))
	for _, t := range info.Data {
		for i, v := range t.Values {
			b.WriteString(write(t.Base+i, v))
		}
	}
	return b.String(), nil
}

// SplitLoader splits a one-time loader into chunks of at most codegen.MaxLines
// lines so each fits the chip editor; the chunks must be run in order. A loader
// that already fits is returned as a single chunk, and an empty loader yields
// nil.
func SplitLoader(loader string) []string {
	return SplitLoaderLines(loader, codegen.MaxLines)
}

// SplitLoaderLines is SplitLoader with an explicit line limit per chunk. A
// maxLines <= 0 falls back to the default codegen.MaxLines.
func SplitLoaderLines(loader string, maxLines int) []string {
	if loader == "" {
		return nil
	}
	if maxLines <= 0 {
		maxLines = codegen.MaxLines
	}
	lines := strings.Split(strings.TrimSuffix(loader, "\n"), "\n")
	var chunks []string
	for len(lines) > 0 {
		n := maxLines
		if n > len(lines) {
			n = len(lines)
		}
		chunks = append(chunks, strings.Join(lines[:n], "\n")+"\n")
		lines = lines[n:]
	}
	return chunks
}

// HasData reports whether the source declares any `data` tables.
func HasData(name string, src []byte) bool {
	info, err := analyze(name, src)
	return err == nil && len(info.Data) > 0
}

// DataStats returns the data-segment layout (first slot and size), the number
// of auto-tabled switches, and a potential-conflict warning. base is -1 when
// the source has no data segment.
//
// The data segment sits at the top of the stack; poke writes arbitrary
// addresses, so a program that uses it may clobber the segment. (push is
// checked precisely by MaxStackDepth.)
func DataStats(name string, src []byte, opts Options) (base, size, autoTabled int, warn string) {
	info, err := analyzeWithOptions(name, src, opts)
	if err != nil || len(info.Data) == 0 {
		return -1, 0, 0, ""
	}
	if sourceUsesPoke(src) {
		warn = fmt.Sprintf("data segment occupies stack slots [%d..%d]; poke must stay below %d",
			info.Sentinel, sema.StackSize-1, info.Sentinel)
	}
	return info.Sentinel, info.DataSize, info.AutoTabled, warn
}

// sourceUsesPoke reports whether the source calls poke.
func sourceUsesPoke(src []byte) bool {
	file := source.NewFile("", src)
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)
	for i, t := range toks {
		if t.Kind == token.Ident && t.Text == "poke" {
			if i+1 < len(toks) && toks[i+1].Kind == token.LParen {
				return true
			}
		}
	}
	return false
}

func analyze(name string, src []byte) (*sema.Info, error) {
	return analyzeWithOptions(name, src, Options{})
}

func analyzeWithOptions(name string, src []byte, opts Options) (*sema.Info, error) {
	file := source.NewFile(name, src)
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)
	tree := parser.Parse(file, toks, diags)
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse errors")
	}
	info := sema.CheckWithOptions(tree, diags, sema.Options{
		FixedDataBase: fixedDataBase(opts),
		AutoTable:     opts.AutoTable,
	})
	if diags.HasErrors() {
		return nil, fmt.Errorf("semantic errors")
	}
	return info, nil
}

// formatDataFloat renders a data value as an IC10 literal.
func formatDataFloat(v float64) string {
	switch {
	case math.IsNaN(v):
		return "nan"
	case math.IsInf(v, 1):
		return "pinf"
	case math.IsInf(v, -1):
		return "ninf"
	}
	const maxExact = 1 << 53
	if v == math.Trunc(v) && v >= -maxExact && v <= maxExact {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}
