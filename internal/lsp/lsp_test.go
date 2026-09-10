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
