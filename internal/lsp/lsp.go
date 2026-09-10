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
					"documentFormattingProvider": true,
					"hoverProvider":              true,
					"definitionProvider":         true,
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
		case "textDocument/formatting":
			s.formatting(writer, msg.ID, msg.Params)
		case "textDocument/hover":
			s.hover(writer, msg.ID, msg.Params)
		case "textDocument/definition":
			s.definition(writer, msg.ID, msg.Params)
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
		{"read", 3, "runtime logic type"},
		{"write", 3, "runtime logic type"},
		{"isLoadValid", 3, "condition only"},
		{"isStoreValid", 3, "condition only"},
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
// Formatting / hover / definition
// ---------------------------------------------------------------------------

type textDocumentParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position lspPosition `json:"position"`
}

func (s *Server) formatting(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var p textDocumentParams
	if err := json.Unmarshal(params, &p); err != nil {
		reply(w, id, []any{})
		return
	}
	text, ok := s.docs[p.TextDocument.URI]
	if !ok {
		reply(w, id, []any{})
		return
	}
	out, diags, err := ic10.Format(p.TextDocument.URI, []byte(text))
	if diags.HasErrors() || err != nil {
		reply(w, id, []any{})
		return
	}
	reply(w, id, []any{map[string]any{
		"range":   lspRange{Start: lspPosition{0, 0}, End: endPosition(text)},
		"newText": out,
	}})
}

func (s *Server) hover(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var p textDocumentParams
	if err := json.Unmarshal(params, &p); err != nil {
		reply(w, id, nil)
		return
	}
	content := hoverFor(wordAt(s.docs[p.TextDocument.URI], p.Position))
	if content == "" {
		reply(w, id, nil)
		return
	}
	reply(w, id, map[string]any{
		"contents": map[string]any{"kind": "markdown", "value": content},
	})
}

func (s *Server) definition(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var p textDocumentParams
	if err := json.Unmarshal(params, &p); err != nil {
		reply(w, id, nil)
		return
	}
	text := s.docs[p.TextDocument.URI]
	reply(w, id, findDefinition(p.TextDocument.URI, text, wordAt(text, p.Position)))
}

func endPosition(text string) lspPosition {
	lines := strings.Split(text, "\n")
	return lspPosition{Line: len(lines) - 1, Character: len(lines[len(lines)-1])}
}

func wordAt(text string, pos lspPosition) string {
	lines := strings.Split(text, "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return ""
	}
	line := lines[pos.Line]
	i := pos.Character
	if i > len(line) {
		i = len(line)
	}
	start := i
	for start > 0 && isWordByte(line[start-1]) {
		start--
	}
	end := i
	for end < len(line) && isWordByte(line[end]) {
		end++
	}
	return line[start:end]
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func hoverFor(word string) string {
	switch word {
	case "func", "const", "var", "if", "else", "for", "switch", "case", "default",
		"break", "continue", "return", "label", "goto", "call", "ret":
		return "keyword `" + word + "`"
	case "true", "false", "nan", "pinf", "ninf":
		return "literal `" + word + "`"
	}
	if word == "db" || (len(word) == 2 && word[0] == 'd' && word[1] >= '0' && word[1] <= '5') {
		return "device port `" + word + "`"
	}
	if f, ok := builtin.Funcs[word]; ok {
		return "builtin `" + f.Mnemonic + "`"
	}
	if builtin.LogicTypes[word] {
		return "logic type `" + word + "`"
	}
	if builtin.SlotTypes[word] {
		return "slot type `" + word + "`"
	}
	return ""
}

func findDefinition(uri, text, word string) any {
	if word == "" {
		return nil
	}
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if hasDecl(trimmed, "func ", word) || hasDecl(trimmed, "const ", word) ||
			hasDecl(trimmed, "var ", word) {
			return location(uri, i, line, word)
		}
		if strings.HasPrefix(trimmed, "label "+word+":") {
			return location(uri, i, line, word)
		}
		if strings.Contains(line, word+" :=") {
			return location(uri, i, line, word)
		}
	}
	return nil
}

func hasDecl(line, prefix, word string) bool {
	rest, ok := strings.CutPrefix(line, prefix)
	if !ok || !strings.HasPrefix(rest, word) {
		return false
	}
	return len(rest) == len(word) || !isWordByte(rest[len(word)])
}

func location(uri string, lineIdx int, line, word string) any {
	col := strings.Index(line, word)
	if col < 0 {
		col = 0
	}
	return map[string]any{
		"uri": uri,
		"range": lspRange{
			Start: lspPosition{lineIdx, col},
			End:   lspPosition{lineIdx, col + len(word)},
		},
	}
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
