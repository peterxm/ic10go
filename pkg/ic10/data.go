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
	info, err := analyze(name, src)
	if err != nil {
		return "", err
	}
	if len(info.Data) == 0 {
		return "", nil
	}
	write := func(addr int, v float64) string {
		if opts.DataAccessStack {
			return fmt.Sprintf("poke %d %s\n", addr, formatDataFloat(v))
		}
		return fmt.Sprintf("put db %d %s\n", addr, formatDataFloat(v))
	}
	var b strings.Builder
	b.WriteString(write(info.Sentinel, info.DataVersion))
	for _, t := range info.Data {
		for i, v := range t.Values {
			b.WriteString(write(t.Base+i, v))
		}
	}
	if n := strings.Count(b.String(), "\n"); n > codegen.MaxLines {
		return "", fmt.Errorf("data loader has %d lines, exceeding the %d line limit; split the table", n, codegen.MaxLines)
	}
	return b.String(), nil
}

// HasData reports whether the source declares any `data` tables.
func HasData(name string, src []byte) bool {
	info, err := analyze(name, src)
	return err == nil && len(info.Data) > 0
}

// DataStats returns the data-segment layout (first slot and size) and a
// potential-conflict warning. base is -1 when the source has no data segment.
//
// The data segment sits at the top of the stack; push grows sp upward and poke
// writes arbitrary addresses, so a program that uses either may clobber it.
func DataStats(name string, src []byte) (base, size int, warn string) {
	info, err := analyze(name, src)
	if err != nil || len(info.Data) == 0 {
		return -1, 0, ""
	}
	if sourceUsesPushPoke(src) {
		warn = fmt.Sprintf("data segment occupies stack slots [%d..%d]; push/poke must stay below %d",
			info.Sentinel, sema.StackSize-1, info.Sentinel)
	}
	return info.Sentinel, info.DataSize, warn
}

// sourceUsesPushPoke reports whether the source calls push or poke.
func sourceUsesPushPoke(src []byte) bool {
	file := source.NewFile("", src)
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)
	for i, t := range toks {
		if t.Kind == token.Ident && (t.Text == "push" || t.Text == "poke") {
			if i+1 < len(toks) && toks[i+1].Kind == token.LParen {
				return true
			}
		}
	}
	return false
}

func analyze(name string, src []byte) (*sema.Info, error) {
	file := source.NewFile(name, src)
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)
	tree := parser.Parse(file, toks, diags)
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse errors")
	}
	info := sema.Check(tree, diags)
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
