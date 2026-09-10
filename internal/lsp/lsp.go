// Package lsp implements a minimal Language Server for .icg files: document
// synchronisation with diagnostics and completion.
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"ic10go/internal/builtin"
	"ic10go/pkg/ic10"
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// Server is a single-connection LSP server.
type Server struct {
	docs map[string]string
}

// New returns a Server.
func New() *Server {
	return &Server{docs: map[string]string{}}
}

// Run serves the connection until EOF or an exit notification.
func (s *Server) Run(r io.Reader, w io.Writer) error {
	reader := bufio.NewReader(r)
	writer := bufio.NewWriter(w)
	for {
		data, err := readMessage(reader)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		var msg message
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Method {
		case "initialize":
			reply(writer, msg.ID, map[string]any{
				"capabilities": map[string]any{
					"textDocumentSync": 1, // full
					"completionProvider": map[string]any{
						"triggerCharacters": []string{"."},
					},
				},
				"serverInfo": map[string]any{"name": "ic10c", "version": "0.1.0"},
			})
		case "initialized":
			// no-op
		case "shutdown":
			reply(writer, msg.ID, nil)
		case "exit":
			return nil
		case "textDocument/didOpen":
			s.didOpen(writer, msg.Params)
		case "textDocument/didChange":
			s.didChange(writer, msg.Params)
		case "textDocument/completion":
			reply(writer, msg.ID, completionItems())
		default:
			if len(msg.ID) > 0 && string(msg.ID) != "null" {
				replyError(writer, msg.ID, -32601, "method not found: "+msg.Method)
			}
		}
	}
}

type didOpenParams struct {
	TextDocument struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	} `json:"textDocument"`
}

type didChangeParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

func (s *Server) didOpen(w *bufio.Writer, params json.RawMessage) {
	var p didOpenParams
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	s.docs[p.TextDocument.URI] = p.TextDocument.Text
	s.publish(w, p.TextDocument.URI)
}

func (s *Server) didChange(w *bufio.Writer, params json.RawMessage) {
	var p didChangeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	if len(p.ContentChanges) > 0 {
		s.docs[p.TextDocument.URI] = p.ContentChanges[len(p.ContentChanges)-1].Text
	}
	s.publish(w, p.TextDocument.URI)
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspDiagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity"`
	Source   string   `json:"source"`
	Message  string   `json:"message"`
}

func (s *Server) publish(w *bufio.Writer, uri string) {
	text, ok := s.docs[uri]
	if !ok {
		return
	}
	_, diags, err := ic10.Compile(uri, []byte(text))
	var items []lspDiagnostic
	for _, d := range diags.Diags {
		line := d.Pos.Line - 1
		if line < 0 {
			line = 0
		}
		ch := d.Pos.Col - 1
		if ch < 0 {
			ch = 0
		}
		items = append(items, lspDiagnostic{
			Range:    lspRange{Start: lspPosition{line, ch}, End: lspPosition{line, ch}},
			Severity: severity(int(d.Severity)),
			Source:   "ic10c",
			Message:  d.Msg,
		})
	}
	if err != nil {
		items = append(items, lspDiagnostic{
			Range:    lspRange{},
			Severity: 1,
			Source:   "ic10c",
			Message:  err.Error(),
		})
	}
	notify(w, "textDocument/publishDiagnostics", map[string]any{
		"uri":         uri,
		"diagnostics": items,
	})
}

func severity(s int) int {
	switch s {
	case 0:
		return 1 // Error
	case 1:
		return 2 // Warning
	default:
		return 3 // Information
	}
}

type completionItem struct {
	Label  string `json:"label"`
	Kind   int    `json:"kind"`
	Detail string `json:"detail,omitempty"`
}

func completionItems() []completionItem {
	items := []completionItem{
		{"const", 14, "declaration"}, {"var", 14, "declaration"}, {"func", 3, "declaration"},
		{"if", 14, ""}, {"else", 14, ""}, {"for", 14, ""}, {"switch", 14, ""},
		{"case", 14, ""}, {"default", 14, ""}, {"break", 14, ""}, {"continue", 14, ""},
		{"return", 14, ""}, {"true", 12, ""}, {"false", 12, ""},
		{"nan", 12, ""}, {"pinf", 12, ""}, {"ninf", 12, ""},
		{"d0", 6, "device"}, {"d1", 6, "device"}, {"d2", 6, "device"},
		{"d3", 6, "device"}, {"d4", 6, "device"}, {"d5", 6, "device"}, {"db", 6, "device"},
		{"batch", 9, "batch IO"},
	}
	for name := range builtin.Funcs {
		items = append(items, completionItem{name, 3, "builtin"})
	}
	for name := range builtin.LogicTypes {
		items = append(items, completionItem{name, 21, "logic type"})
	}
	for name := range builtin.SlotTypes {
		items = append(items, completionItem{name, 21, "slot type"})
	}
	return items
}

// ---------------------------------------------------------------------------
// JSON-RPC framing
// ---------------------------------------------------------------------------

func readMessage(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			v := strings.TrimSpace(line[len("content-length:"):])
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("lsp: bad content-length %q", v)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("lsp: missing content-length")
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func send(w *bufio.Writer, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(data))
	w.Write(data)
	w.Flush()
}

func reply(w *bufio.Writer, id json.RawMessage, result any) {
	send(w, response{JSONRPC: "2.0", ID: id, Result: result})
}

func replyError(w *bufio.Writer, id json.RawMessage, code int, msg string) {
	send(w, response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func notify(w *bufio.Writer, method string, params any) {
	send(w, notification{JSONRPC: "2.0", Method: method, Params: params})
}
