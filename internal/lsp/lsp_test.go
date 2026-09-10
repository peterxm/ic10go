package lsp

import (
	"bytes"
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
