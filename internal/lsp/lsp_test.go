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

func TestDiagnosticCode(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"c.icg","text":"func main() { x := d0.NotARealType }"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`),
	)
	if !strings.Contains(out, "unknown-logic-type") {
		t.Errorf("diagnostic code not published:\n%s", out)
	}
}

func TestDocumentHighlight(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"h.icg","text":"func main() { x := 1\n    d0.Setting = x }"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/documentHighlight","params":{"textDocument":{"uri":"h.icg"},"position":{"line":0,"character":14}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, `"kind":1`) {
		t.Errorf("no document highlights returned:\n%s", out)
	}
}

func TestSelectionRange(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"s.icg","text":"func main() {\n    x := 1\n}"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/selectionRange","params":{"textDocument":{"uri":"s.icg"},"positions":[{"line":1,"character":5}]}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, `"parent"`) {
		t.Errorf("selection range has no parent chain:\n%s", out)
	}
}

func TestWorkspaceSymbol(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"w.icg","text":"func helper() {}\nfunc main() { helper() }"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"workspace/symbol","params":{"query":"help"}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, "helper") {
		t.Errorf("workspace symbol missing:\n%s", out)
	}
}

func TestSemanticTokensRange(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"t.icg","text":"func main() { d0.On = 1 }"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/semanticTokens/range","params":{"textDocument":{"uri":"t.icg"},"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":22}}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, `"data"`) {
		t.Errorf("no range semantic tokens returned:\n%s", out)
	}
}

func TestCodeLens(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"l.icg","text":"func main() { yield() }"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/codeLens","params":{"textDocument":{"uri":"l.icg"}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, "icg.compile") {
		t.Errorf("no code lens returned:\n%s", out)
	}
}

func TestDocumentLink(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"d.icg","text":"data T = [1, 2]\nfunc helper() {}\nfunc main() { d0.Setting = T[0]\n    helper() }"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/documentLink","params":{"textDocument":{"uri":"d.icg"}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, "#L1") || !strings.Contains(out, "#L2") {
		t.Errorf("document links missing:\n%s", out)
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
	if !strings.Contains(out, "Kelvin") {
		t.Errorf("hover did not describe the logic type:\n%s", out)
	}
}

func TestHoverEnumMember(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"e.icg","text":"func main() { d0.Color = Color.Purple }"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"textDocument":{"uri":"e.icg"},"position":{"line":0,"character":33}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, "Color.Purple") || !strings.Contains(out, "11") {
		t.Errorf("hover did not describe the enum member:\n%s", out)
	}
}

func TestHoverVariableType(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"v.icg","text":"func main() {\n    flag := d1.Temperature > 0\n    d0.On = flag\n}"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"textDocument":{"uri":"v.icg"},"position":{"line":2,"character":13}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, "type: `bool`") {
		t.Errorf("hover did not show the variable type:\n%s", out)
	}
}

func TestHoverBuiltin(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"hb.icg","text":"func main() { d0.Setting = sqrt(2) }"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"textDocument":{"uri":"hb.icg"},"position":{"line":0,"character":27}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, "sqrt(x)") || !strings.Contains(out, "Square root") {
		t.Errorf("hover did not document the builtin:\n%s", out)
	}
}

func TestCompletionDocs(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"cd.icg","text":"func main() { d0.Setting = sq"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/completion","params":{"textDocument":{"uri":"cd.icg"},"position":{"line":0,"character":29}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, `"documentation"`) || !strings.Contains(out, "Square root") {
		t.Errorf("completion did not include documentation:\n%s", out)
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
		{"logic type member", "LogicType.", `{"line":0,"character":10}`, "Temperature", `"label":"func"`},
		{"display mode member", "DisplayMode.", `{"line":0,"character":12}`, "Percent", `"label":"func"`},
		{"sound member", "Sound.", `{"line":0,"character":6}`, "Alarm1", `"label":"func"`},
		{"power mode member", "PowerMode.", `{"line":0,"character":10}`, "Charging", `"label":"func"`},
		{"color member", "Color.", `{"line":0,"character":6}`, "Purple", `"label":"func"`},
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

func TestFullTextChange(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"ft.icg","text":"func main() { d0.On = 1 }"}}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"ft.icg"},"contentChanges":[{"text":"func main() { x := foo }"}]}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`),
	)
	if !strings.Contains(out, "undefined variable") {
		t.Errorf("full-text change not applied:\n%s", out)
	}
}

func TestHoverUnknownHasResult(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"u.icg","text":"func main() { x := 1 }"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"textDocument":{"uri":"u.icg"},"position":{"line":0,"character":15}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, `"result":null`) {
		t.Errorf("hover response must include a result field (even null):\n%s", out)
	}
}

func TestStatsNotification(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"s.icg","text":"func main() { d0.On = 1 }"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`),
	)
	if !strings.Contains(out, `"icg/stats"`) || !strings.Contains(out, `"maxLineLen"`) {
		t.Errorf("expected an icg/stats notification with the budget:\n%s", out)
	}
}

func TestHoverDeviceAlias(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"da.icg","text":"const sensor = d0\nfunc main() { d1.Setting = sensor.Temperature }"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"textDocument":{"uri":"da.icg"},"position":{"line":1,"character":29}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, "device alias") {
		t.Errorf("hover should describe the device alias:\n%s", out)
	}
}
