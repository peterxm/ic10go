package cfg_test

import (
	"strings"
	"testing"

	"ic10go/internal/cfg"
	"ic10go/pkg/ic10"
)

func TestMermaid(t *testing.T) {
	src := []byte("func main() {\n  for {\n    yield()\n    if d0.On { d1.On = 1 } else { d1.On = 0 }\n  }\n}\n")
	res, diags, err := ic10.Graph("t.icg", src, ic10.Options{})
	if err != nil || diags.HasErrors() {
		t.Fatalf("graph: diags=%v err=%v", diags.Diags, err)
	}
	out := cfg.Mermaid(res.Fn, res.Order, res.Start, res.Colors, cfg.Options{ShowLines: true, MaxInstr: 8})
	if !strings.HasPrefix(out, "flowchart TD\n") {
		t.Fatalf("missing flowchart header:\n%s", out)
	}
	if !strings.Contains(out, "-->|then|") || !strings.Contains(out, "-->|else|") {
		t.Errorf("branch edges not labelled:\n%s", out)
	}
	if !strings.Contains(out, "yield") || !strings.Contains(out, "l r") {
		t.Errorf("instruction text missing:\n%s", out)
	}
	if !strings.Contains(out, "L") {
		t.Errorf("line annotation missing:\n%s", out)
	}
}

func TestMermaidNoLines(t *testing.T) {
	src := []byte("func main() { d0.On = 1 }\n")
	res, diags, _ := ic10.Graph("t.icg", src, ic10.Options{})
	if res == nil || diags.HasErrors() {
		t.Fatalf("graph failed: %v", diags.Diags)
	}
	out := cfg.Mermaid(res.Fn, res.Order, res.Start, res.Colors, cfg.Options{ShowLines: false})
	if strings.Contains(out, " · L") {
		t.Errorf("--no-lines should hide line annotations:\n%s", out)
	}
}

func TestMermaidEmpty(t *testing.T) {
	if got := cfg.Mermaid(nil, nil, nil, nil, cfg.Options{}); got != "flowchart TD\n" {
		t.Errorf("empty graph = %q", got)
	}
}
