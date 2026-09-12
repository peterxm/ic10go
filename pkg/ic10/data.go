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
)

// DataLoader compiles the one-time loader that installs the source's persistent
// data segment into the IC housing's stack. It returns "" when the source has
// no `data` tables.
//
// The loader is plain IC10 (a sequence of `put db <addr> <value>` lines); run
// it once, then replace the chip's code with the compiled runtime.
func DataLoader(name string, src []byte) (string, error) {
	info, err := analyze(name, src)
	if err != nil {
		return "", err
	}
	if len(info.Data) == 0 {
		return "", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "put db %d %s\n", info.Sentinel, formatDataFloat(info.DataVersion))
	for _, t := range info.Data {
		for i, v := range t.Values {
			fmt.Fprintf(&b, "put db %d %s\n", t.Base+i, formatDataFloat(v))
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
