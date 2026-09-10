package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func frame(body string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

func runServer(t *testing.T, messages ...string) string {
	t.Helper()
	input := strings.Join(messages, "")
	var out bytes.Buffer
	if err := New().Run(strings.NewReader(input), &out); err != nil {
		t.Fatalf("server: %v", err)
	}
	return out.String()
}

func TestDiagnostics(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"test.icg","text":"func main() { x := foo }"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`),
	)
	if !strings.Contains(out, "publishDiagnostics") {
		t.Errorf("no diagnostics published:\n%s", out)
	}
	if !strings.Contains(out, "undefined variable") {
		t.Errorf("expected an undefined variable diagnostic:\n%s", out)
	}
}

func TestCompletion(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/completion","params":{"textDocument":{"uri":"test.icg"},"position":{"line":0,"character":0}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	for _, want := range []string{"batch", "Temperature", "yield", "d0"} {
		if !strings.Contains(out, want) {
			t.Errorf("completion missing %q:\n%s", want, out)
		}
	}
}

func TestFormatting(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"f.icg","text":"func main(){d0.On=1}"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/formatting","params":{"textDocument":{"uri":"f.icg"}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, "newText") || !strings.Contains(out, "d0.On = 1") {
		t.Errorf("formatting did not return an edit:\n%s", out)
	}
}

func TestHover(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"h.icg","text":"func main() { x := d1.Temperature }"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"textDocument":{"uri":"h.icg"},"position":{"line":0,"character":25}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, "logic type") {
		t.Errorf("hover did not describe the logic type:\n%s", out)
	}
}

func TestDefinition(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"d.icg","text":"func foo() {}\nfunc main() { foo() }"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/definition","params":{"textDocument":{"uri":"d.icg"},"position":{"line":1,"character":15}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, `"line":0`) {
		t.Errorf("definition did not point at the declaration:\n%s", out)
	}
}

func TestCleanDocumentHasNoDiagnostics(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"ok.icg","text":"func main() { d0.On = 1 }"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`),
	)
	if strings.Contains(out, "undefined variable") {
		t.Errorf("unexpected diagnostic:\n%s", out)
	}
}

func TestCompletionContext(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		pos     string
		want    string
		notWant string
	}{
		{"device logic type", "d0.", `{"line":0,"character":3}`, "Temperature", `"label":"func"`},
		{"batch method", "batch.", `{"line":0,"character":6}`, "readName", "Temperature"},
		{"enum member", "SorterInstruction.", `{"line":0,"character":18}`, "FilterPrefabHashEquals", `"label":"func"`},
		{"slot type", "d0.slot[0].", `{"line":0,"character":11}`, "Occupied", `"label":"func"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := runServer(t,
				frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
				frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"c.icg","text":`+jsonString(c.text)+`}}}`),
				frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/completion","params":{"textDocument":{"uri":"c.icg"},"position":`+c.pos+`}}`),
				frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
			)
			if !strings.Contains(out, c.want) {
				t.Errorf("completion missing %q:\n%s", c.want, out)
			}
			if c.notWant != "" && strings.Contains(out, c.notWant) {
				t.Errorf("completion unexpectedly contains %q:\n%s", c.notWant, out)
			}
		})
	}
}

func TestCompletionUserSymbols(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"u.icg","text":"func helper() {}\nconst MaxTemp = 1\nhelper"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/completion","params":{"textDocument":{"uri":"u.icg"},"position":{"line":2,"character":6}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	for _, want := range []string{"helper", "MaxTemp"} {
		if !strings.Contains(out, want) {
			t.Errorf("completion missing user symbol %q:\n%s", want, out)
		}
	}
}

func TestIncrementalChange(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"i.icg","text":"func main() { d0.On = 1 }"}}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"i.icg"},"contentChanges":[{"range":{"start":{"line":0,"character":22},"end":{"line":0,"character":23}},"text":"bar"}]}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`),
	)
	if !strings.Contains(out, "undefined variable") {
		t.Errorf("incremental edit not applied:\n%s", out)
	}
}

func jsonString(s string) string {
	b, _ := jsonMarshal(s)
	return string(b)
}

func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func openAndRequest(t *testing.T, uri, text, method, extra string) string {
	t.Helper()
	return runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"`+uri+`","text":`+jsonString(text)+`}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"`+method+`","params":{"textDocument":{"uri":"`+uri+`"}`+extra+`}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
}

func TestDocumentSymbol(t *testing.T) {
	out := openAndRequest(t, "s.icg", "func main() {\n    x := 1\n}\nconst A = 2", "textDocument/documentSymbol", "")
	for _, want := range []string{`"name":"main"`, `"name":"x"`, `"name":"A"`} {
		if !strings.Contains(out, want) {
			t.Errorf("documentSymbol missing %s:\n%s", want, out)
		}
	}
}

func TestFoldingRange(t *testing.T) {
	out := openAndRequest(t, "f.icg", "func main() {\n    x := 1\n    y := 2\n}", "textDocument/foldingRange", "")
	if !strings.Contains(out, `"startLine":0`) || !strings.Contains(out, `"endLine":3`) {
		t.Errorf("foldingRange missing function body:\n%s", out)
	}
}

func TestReferencesAndRename(t *testing.T) {
	text := "func main() {\n    x := 1\n    y := x\n}"
	pos := `"position":{"line":1,"character":4}`
	refs := openAndRequest(t, "r.icg", text, "textDocument/references", ","+pos)
	if strings.Count(refs, `"range"`) < 2 {
		t.Errorf("references should find both x uses:\n%s", refs)
	}
	ren := openAndRequest(t, "r.icg", text, "textDocument/rename", ","+pos+`,"newName":"total"`)
	if !strings.Contains(ren, `"newText":"total"`) {
		t.Errorf("rename did not produce edits:\n%s", ren)
	}
}

func TestSignatureHelp(t *testing.T) {
	text := "func add(a num, b num) num { return a + b }\nfunc main() { add( }"
	out := openAndRequest(t, "sig.icg", text, "textDocument/signatureHelp", `,"position":{"line":1,"character":18}`)
	if !strings.Contains(out, "add(a num, b num)") {
		t.Errorf("signature help missing function signature:\n%s", out)
	}
}

func TestCodeActionDidYouMean(t *testing.T) {
	diag := `"context":{"diagnostics":[{"range":{"start":{"line":0,"character":13},"end":{"line":0,"character":23}},"message":"unknown logic type \"Temperatur\"","severity":2,"source":"ic10c"}]}`
	out := openAndRequest(t, "ca.icg", "func main() { d0.Temperatur = 1 }", "textDocument/codeAction", ","+diag)
	if !strings.Contains(out, "Temperature") {
		t.Errorf("code action did not suggest the closest logic type:\n%s", out)
	}
}

func TestSemanticTokens(t *testing.T) {
	out := openAndRequest(t, "st.icg", "func main() { d0.On = 1 }", "textDocument/semanticTokens/full", "")
	if !strings.Contains(out, `"data":[`) || strings.Contains(out, `"data":[]`) {
		t.Errorf("semantic tokens should be non-empty:\n%s", out)
	}
}

func TestInlayHintBudget(t *testing.T) {
	out := openAndRequest(t, "ih.icg", "func main() { d0.On = 1 }", "textDocument/inlayHint", "")
	if !strings.Contains(out, "IC10:") {
		t.Errorf("inlay hint should show the budget:\n%s", out)
	}
}
