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

func TestHoverBareEnum(t *testing.T) {
	src := "func main() { d0.Setting = Equals }"
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"be.icg","text":`+jsonString(src)+`}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"textDocument":{"uri":"be.icg"},"position":{"line":0,"character":29}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, "Equals") || !strings.Contains(out, "0") {
		t.Errorf("hover did not describe the bare enum constant:\n%s", out)
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

func TestPrepareRename(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"p.icg","text":"func main() { x := 1\n d0.On = x }"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/prepareRename","params":{"textDocument":{"uri":"p.icg"},"position":{"line":0,"character":15}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, `"placeholder":"x"`) {
		t.Errorf("prepareRename did not return the identifier:\n%s", out)
	}
}

func TestDocumentColor(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"c.icg","text":"func main() { d0.Color = Color.Red }"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/documentColor","params":{"textDocument":{"uri":"c.icg"}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, `"red"`) || !strings.Contains(out, `"green"`) {
		t.Errorf("documentColor did not return a swatch:\n%s", out)
	}
}

func TestInlayTypeHints(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"ih.icg","text":"func main() { x := d0.Temperature\n d1.On = x > 0 }"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/inlayHint","params":{"textDocument":{"uri":"ih.icg"}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, `": num"`) || !strings.Contains(out, `"kind":2`) {
		t.Errorf("inlay hints did not include the variable type:\n%s", out)
	}
}

func TestDiagnosticCodeDescription(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"dc.icg","text":"func main() { d0.Bogus = 1 }"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`),
	)
	if !strings.Contains(out, `"unknown-logic-type"`) || !strings.Contains(out, `"codeDescription"`) {
		t.Errorf("diagnostic did not carry a code description:\n%s", out)
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
		frame(`{"jsonrpc":"2.0","id":3,"method":"completionItem/resolve","params":{"label":"sqrt","data":{"label":"sqrt"}}}`),
		frame(`{"jsonrpc":"2.0","id":4,"method":"shutdown"}`),
	)
	if !strings.Contains(out, "Square root") {
		t.Errorf("resolved completion did not include documentation:\n%s", out)
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
		{"battery output power", "d2.", `{"line":0,"character":3}`, "PowerActual", `"label":"func"`},
		{"battery input power", "d2.", `{"line":0,"character":3}`, "PowerPotential", `"label":"func"`},
		{"generator power", "d1.", `{"line":0,"character":3}`, "PowerGeneration", `"label":"func"`},
		{"batch method", "batch.", `{"line":0,"character":6}`, "readName", "Temperature"},
		{"sorter method", "sorter.", `{"line":0,"character":7}`, "filterSortingClass", "Temperature"},
		{"printer method", "printer.", `{"line":0,"character":8}`, "executeRecipe", "Temperature"},
		{"read by id builtin", "readBy", `{"line":0,"character":6}`, "readById", ""},
		{"write by id builtin", "writeBy", `{"line":0,"character":7}`, "writeById", ""},
		{"bare enum member", "Equals", `{"line":0,"character":6}`, "Equals", ""},
		{"raw constant", "deg2rad", `{"line":0,"character":7}`, "deg2rad", ""},
		{"special builtin hash", "has", `{"line":0,"character":3}`, `"label":"hash"`, ""},
		{"batch mode name", "Sum", `{"line":0,"character":3}`, "Sum", ""},
		{"special register", "ra", `{"line":0,"character":2}`, `"label":"ra"`, ""},
		{"stack size enum", "SorterStack.", `{"line":0,"character":12}`, "Size", `"label":"func"`},
		{"condition operation enum", "ConditionOperation.", `{"line":0,"character":19}`, "Equals", `"label":"func"`},
		{"enum member", "SorterInstruction.", `{"line":0,"character":18}`, "FilterPrefabHashEquals", `"label":"func"`},
		{"logic type member", "LogicType.", `{"line":0,"character":10}`, "Temperature", `"label":"func"`},
		{"display mode member", "DisplayMode.", `{"line":0,"character":12}`, "Percent", `"label":"func"`},
		{"sound member", "Sound.", `{"line":0,"character":6}`, "Alarm1", `"label":"func"`},
		{"power mode member", "PowerMode.", `{"line":0,"character":10}`, "Charging", `"label":"func"`},
		{"color member", "Color.", `{"line":0,"character":6}`, "Purple", `"label":"func"`},
		{"slot type", "d0.slot[0].", `{"line":0,"character":11}`, "Occupied", `"label":"func"`},
		{"batch arg device logic", "batch.write(d0.", `{"line":0,"character":15}`, "Temperature", "StructureBattery"},
		{"batch arg hash string", `batch.write(hash("Iro`, `{"line":0,"character":20}`, "ItemIronOre", ""},
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

func TestCompletionArgPosition(t *testing.T) {
	cases := []struct {
		name    string
		uri     string
		text    string
		want    string
		newText string
	}{
		{"batch typeHash", "a.icg", "batch.write(Str", "StructureBattery", `"newText":"hash(\"StructureBattery\")"`},
		{"batch write logic", "b.icg", `batch.write(hash("StructureBattery"), `, "Temperature", `"newText":"\"Temperature\""`},
		{"batch read logic", "c.icg", `batch.read(hash("StructureBattery"), `, "Temperature", `"newText":"\"Temperature\""`},
		{"batch read mode", "d.icg", `batch.read(hash("StructureBattery"), "Temperature", `, "Average", `"newText":"\"Average\""`},
		{"batch writeName logic", "n.icg", `batch.writeName(hash("StructureBattery"), hash("Bank 1"), `, "Temperature", `"newText":"\"Temperature\""`},
		{"batch logic in string", "e.icg", `batch.write(hash("StructureBattery"), "Tem`, "Temperature", `"newText":"Temperature"`},
		{"sorter prefab", "f.icg", "sorter.filterPrefabHash(", "ItemIronOre", `"newText":"hash(\"ItemIronOre\")"`},
		{"printer prefab", "g.icg", "printer.executeRecipe(50, ", "ItemIronOre", `"newText":"hash(\"ItemIronOre\")"`},
		{"readReagent hash", "h.icg", "readReagent(d0, 0, ", "ItemIronOre", `"newText":"hash(\"ItemIronOre\")"`},
		{"readById logic", "i.icg", "readById(1, ", "Temperature", `"newText":"\"Temperature\""`},
		{"native sbn type", "j.ic", "sbn ", "StructureBattery", `"newText":"HASH(\"StructureBattery\")"`},
		{"native lb type", "k.ic", "lb r0 ", "StructureBattery", `"newText":"HASH(\"StructureBattery\")"`},
		{"native lb logic", "l.ic", `lb r0 HASH("StructureBattery") `, "Temperature", `"newText":"Temperature"`},
		{"native lb mode", "m.ic", `lb r0 HASH("StructureBattery") Temperature `, "Average", `"newText":"Average"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pos := fmt.Sprintf(`{"line":0,"character":%d}`, len(c.text))
			out := openAndRequest(t, c.uri, c.text, "textDocument/completion", `,"position":`+pos)
			if !strings.Contains(out, c.want) {
				t.Errorf("completion missing %q:\n%s", c.want, out)
			}
			if c.newText != "" && !strings.Contains(out, c.newText) {
				t.Errorf("completion missing textEdit %q:\n%s", c.newText, out)
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

// TestSemanticTokensForNewFeatures checks the namespaces/builtins/enums added
// for device-stack programming are classified distinctly.
func TestSemanticTokensForNewFeatures(t *testing.T) {
	src := "func main() { put(d0, 0, sorter.filterSortingClass(Equals, SortingClass.Ores)); " +
		"put(d1, 0, printer.executeRecipe(1, 2)); x := readById(1, LogicType.On); " +
		"y := SorterInstruction.FilterPrefabHashEquals; z := pi; w := Sum }"
	got := map[string]string{}
	for _, tk := range semanticTokensFor(src) {
		if tk.char+tk.length <= len(src) {
			got[src[tk.char:tk.char+tk.length]] = semanticTokenTypes[tk.typ]
		}
	}
	want := map[string]string{
		"sorter":                 "namespace",
		"printer":                "namespace",
		"filterSortingClass":     "property",
		"executeRecipe":          "property",
		"readById":               "builtin",
		"Equals":                 "enumMember", // bare CONDOP name
		"pi":                     "number",     // raw game constant
		"Sum":                    "enumMember", // bare batch mode name
		"SorterInstruction":      "enum",
		"FilterPrefabHashEquals": "enumMember",
		"LogicType":              "enum",
		"On":                     "logicType", // logic types win over enumMember
	}
	for tok, typ := range want {
		if got[tok] != typ {
			t.Errorf("token %q type = %q, want %q", tok, got[tok], typ)
		}
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

func TestCompletionPrefabHashArg(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"p.icg","text":"hash(\"Iron\")"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/completion","params":{"textDocument":{"uri":"p.icg"},"position":{"line":0,"character":10}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, "ItemIronOre") {
		t.Errorf("hash(\"...\") completion missing prefabs:\n%s", out)
	}
	if !strings.Contains(out, `"textEdit"`) {
		t.Errorf("prefab completion should use a textEdit:\n%s", out)
	}
}

func TestHoverPrefabName(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"pn.icg","text":"hash(\"ItemIronOre\")"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"textDocument":{"uri":"pn.icg"},"position":{"line":0,"character":9}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, "ItemIronOre") || !strings.Contains(out, "1758427767") {
		t.Errorf("hover did not describe the prefab:\n%s", out)
	}
}

func TestHoverPrefabHash(t *testing.T) {
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"ph.icg","text":"func main() { d0.Setting = 1758427767 }"}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"textDocument":{"uri":"ph.icg"},"position":{"line":0,"character":30}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, "ItemIronOre") {
		t.Errorf("hover on a prefab hash did not resolve the name:\n%s", out)
	}
}

func TestIC10Completion(t *testing.T) {
	out := openAndRequest(t, "t.ic", "add r0 1 2\n", "textDocument/completion",
		`,"position":{"line":0,"character":2}`)
	if !strings.Contains(out, `"label":"abs"`) || !strings.Contains(out, `"label":"HASH"`) {
		t.Errorf("native IC10 completion missing instructions:\n%s", out)
	}
}

func TestIC10Hover(t *testing.T) {
	out := openAndRequest(t, "t.ic", "abs r0 -5\n", "textDocument/hover",
		`,"position":{"line":0,"character":1}`)
	if !strings.Contains(out, "absolute value") {
		t.Errorf("native IC10 hover missing instruction description:\n%s", out)
	}
}

func TestIC10UnknownInstruction(t *testing.T) {
	out := openAndRequest(t, "t.ic", "foobar r0 1\n", "textDocument/hover",
		`,"position":{"line":0,"character":1}`)
	if !strings.Contains(out, "unknown IC10 instruction") {
		t.Errorf("native IC10 diagnostics missing unknown-instruction error:\n%s", out)
	}
}

func TestIC10PrefabInHash(t *testing.T) {
	out := openAndRequest(t, "t.ic", `HASH("Iron")`, "textDocument/completion",
		`,"position":{"line":0,"character":10}`)
	if !strings.Contains(out, "ItemIronOre") {
		t.Errorf("native IC10 HASH(\"...\") completion missing prefabs:\n%s", out)
	}
}

func TestDocumentLinkPrefab(t *testing.T) {
	out := openAndRequest(t, "d.icg", `func main() { d0.Setting = hash("ItemIronOre") }`, "textDocument/documentLink", "")
	if !strings.Contains(out, "stationeers-wiki.com") || !strings.Contains(out, "search=") {
		t.Errorf("hash(\"...\") should link to the wiki:\n%s", out)
	}
	out = openAndRequest(t, "d2.icg", "func main() { d0.Setting = 1758427767 }", "textDocument/documentLink", "")
	if !strings.Contains(out, "stationeers-wiki.com") {
		t.Errorf("a numeric prefab hash should link to the wiki:\n%s", out)
	}
}

func TestIC10Formatting(t *testing.T) {
	out := openAndRequest(t, "f.ic", "move   r0   1\nadd r1 2 3\n", "textDocument/formatting", "")
	if !strings.Contains(out, `"newText"`) || !strings.Contains(out, "move r0 1") {
		t.Errorf("native IC10 formatting failed:\n%s", out)
	}
}

func TestHoverBatteryPower(t *testing.T) {
	src := "func main() { d2.Setting = d2.PowerActual + d2.PowerPotential }"
	out := runServer(t,
		frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		frame(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"bp.icg","text":`+jsonString(src)+`}}}`),
		frame(`{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"textDocument":{"uri":"bp.icg"},"position":{"line":0,"character":34}}}`),
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	)
	if !strings.Contains(out, "PowerActual") {
		t.Errorf("hover did not describe PowerActual:\n%s", out)
	}
}

func TestCompletionChipScope(t *testing.T) {
	src := "const Shared = 1\n" +
		"chip a {\n    const LocalA = 2\n    func main() { x := 0 }\n}\n" +
		"chip b {\n    const LocalB = 3\n    func main() { }\n}\n"
	labels := func(pos lspPosition) string {
		var out []string
		for _, it := range completionItemsFor(src, pos) {
			out = append(out, it.Label)
		}
		return strings.Join(out, ",")
	}
	// Inside chip a: sees Shared and LocalA, not LocalB.
	a := labels(lspPosition{Line: 3, Character: 20})
	if !strings.Contains(a, "LocalA") || !strings.Contains(a, "Shared") {
		t.Errorf("chip a completion missing Shared/LocalA:\n%s", a)
	}
	if strings.Contains(a, "LocalB") {
		t.Errorf("chip a completion should not offer LocalB:\n%s", a)
	}
	// Inside chip b: sees LocalB, not LocalA.
	b := labels(lspPosition{Line: 6, Character: 20})
	if !strings.Contains(b, "LocalB") {
		t.Errorf("chip b completion missing LocalB:\n%s", b)
	}
	if strings.Contains(b, "LocalA") {
		t.Errorf("chip b completion should not offer LocalA:\n%s", b)
	}
}

func TestBusEditorSupport(t *testing.T) {
	// Bus.slot completion.
	src := "bus B {\n    x num\n    y num\n}\nchip c {\n    func main() { B. }\n}\n"
	var labels []string
	for _, it := range completionItemsFor(src, lspPosition{Line: 5, Character: 20}) {
		labels = append(labels, it.Label)
	}
	joined := strings.Join(labels, ",")
	if !strings.Contains(joined, "x") || !strings.Contains(joined, "y") {
		t.Errorf("Bus. completion missing slots:\n%s", joined)
	}

	// Hover on an inline bus slot access shows its channel.
	hoverSrc := "bus B {\n    x num\n}\nchip c {\n    func main() { B.x[d5][1] = 1 }\n}\n"
	out := openAndRequest(t, "b.icg", hoverSrc, "textDocument/hover", `,"position":{"line":4,"character":20}`)
	if !strings.Contains(out, "Channel0") {
		t.Errorf("hover on a bus slot missing the channel:\n%s", out)
	}
}
