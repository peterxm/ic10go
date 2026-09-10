package lsp

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestReproRaw(t *testing.T) {
	body := func(s string) string { return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(s), s) }
	text := "func main() { batch.read(1, \"On\", \"Sum\") }"
	msgs := []string{
		body(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		body(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"b.icg","text":` + fmt.Sprintf("%q", text) + `}}}`),
		body(`{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"textDocument":{"uri":"b.icg"},"position":{"line":0,"character":14}}}`),
		body(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`),
	}
	var out bytes.Buffer
	_ = New().Run(strings.NewReader(strings.Join(msgs, "")), &out)
	t.Logf("raw: %s", out.String())
}
