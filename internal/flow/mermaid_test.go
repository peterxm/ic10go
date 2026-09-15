package flow_test

import (
	"strings"
	"testing"

	"ic10go/internal/flow"
	"ic10go/pkg/ic10"
)

func render(t *testing.T, src string, opts flow.Options) string {
	t.Helper()
	tree, diags, err := ic10.Flow("t.icg", []byte(src), ic10.Options{})
	if err != nil || diags.HasErrors() {
		t.Fatalf("flow: diags=%v err=%v", diags.Diags, err)
	}
	if tree == nil {
		t.Fatal("no AST")
	}
	return flow.Mermaid(tree, opts)
}

func TestFlowIfElse(t *testing.T) {
	out := render(t, `func main() {
    if d0.On { d1.On = 1 } else { d1.On = 0 }
}`, flow.Options{ShowLine: true, Coalesce: true})
	for _, want := range []string{"subgraph", "if d0.On", "-->|true|", "-->|false|", "d1.On = 1", "d1.On = 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestFlowLoopBackEdge(t *testing.T) {
	out := render(t, "func main() {\n  for {\n    yield()\n  }\n}\n", flow.Options{Coalesce: true})
	if !strings.Contains(out, "for {}") {
		t.Fatalf("no loop node:\n%s", out)
	}
	// The loop header must be both a source and a target (back edge).
	if !hasBackEdge(out) {
		t.Errorf("no back edge:\n%s", out)
	}
}

func TestFlowSwitch(t *testing.T) {
	out := render(t, `func main() {
    switch d0.Setting {
    case 1: d1.On = 1
    case 2, 3: d1.On = 0
    default: d1.On = 2
    }
}`, flow.Options{})
	for _, want := range []string{"switch d0.Setting", "case 1", "case 2, 3", "default"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestFlowBreakContinue(t *testing.T) {
	out := render(t, `func main() {
    for {
        if d0.On { break }
        if d1.On { continue }
    }
}`, flow.Options{})
	if !strings.Contains(out, "break") || !strings.Contains(out, "continue") {
		t.Errorf("missing break/continue:\n%s", out)
	}
}

func TestFlowCoalesce(t *testing.T) {
	coalesced := render(t, "func main() {\n  d0.On = 1\n  d1.On = 0\n}\n", flow.Options{Coalesce: true})
	if !strings.Contains(coalesced, "d0.On = 1<br/>d1.On = 0") {
		t.Errorf("straight-line statements not coalesced:\n%s", coalesced)
	}
	full := render(t, "func main() {\n  d0.On = 1\n  d1.On = 0\n}\n", flow.Options{Coalesce: false})
	if strings.Contains(full, "d0.On = 1<br/>d1.On = 0") {
		t.Errorf("--full should not coalesce:\n%s", full)
	}
}

func TestFlowFuncFilter(t *testing.T) {
	src := "func foo() { d0.On = 1 }\nfunc main() { foo() }\n"
	all := render(t, src, flow.Options{})
	if !strings.Contains(all, "subgraph") || strings.Count(all, "subgraph") != 2 {
		t.Errorf("want both functions:\n%s", all)
	}
	only := render(t, src, flow.Options{Func: "main"})
	if strings.Count(only, "subgraph") != 1 || strings.Contains(only, "foo]") {
		t.Errorf("--func main should draw only main:\n%s", only)
	}
}

func hasBackEdge(out string) bool {
	sources := map[string]bool{}
	targets := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		i := strings.Index(line, " -->")
		if i < 0 {
			continue
		}
		from := line[:i]
		rest := strings.TrimSpace(line[i+4:])
		// strip an edge label |...|
		if strings.HasPrefix(rest, "|") {
			if j := strings.Index(rest[1:], "|"); j >= 0 {
				rest = strings.TrimSpace(rest[j+2:])
			}
		}
		to := strings.Fields(rest)
		if len(to) == 0 {
			continue
		}
		sources[from] = true
		targets[to[0]] = true
	}
	for id := range sources {
		if targets[id] {
			return true
		}
	}
	return false
}
