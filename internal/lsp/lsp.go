// Package lsp implements a minimal Language Server for .icg files: document
// synchronisation with diagnostics and completion.
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"ic10go/internal/ast"
	"ic10go/internal/builtin"
	"ic10go/internal/diag"
	"ic10go/internal/lexer"
	"ic10go/internal/parser"
	"ic10go/internal/sema"
	"ic10go/internal/source"
	"ic10go/internal/version"
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
	zh   bool   // documentation language
	root string // workspace root (filesystem path), for workspace symbols
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
			var init struct {
				Locale     string `json:"locale"`
				RootURI    string `json:"rootUri"`
				RootPath   string `json:"rootPath"`
				Workspaces []struct {
					URI string `json:"uri"`
				} `json:"workspaceFolders"`
			}
			_ = json.Unmarshal(msg.Params, &init)
			s.zh = strings.HasPrefix(strings.ToLower(init.Locale), "zh")
			switch {
			case len(init.Workspaces) > 0:
				s.root = fileURIToPath(init.Workspaces[0].URI)
			case init.RootURI != "":
				s.root = fileURIToPath(init.RootURI)
			default:
				s.root = init.RootPath
			}
			reply(writer, msg.ID, map[string]any{
				"capabilities": map[string]any{
					"textDocumentSync": map[string]any{
						"openClose": true,
						"change":    1, // full
					},
					"completionProvider": map[string]any{
						"triggerCharacters": []string{"."},
						"resolveProvider":   true,
					},
					"documentFormattingProvider": true,
					"hoverProvider":              true,
					"definitionProvider":         true,
					"documentSymbolProvider":     true,
					"documentHighlightProvider":  true,
					"selectionRangeProvider":     true,
					"workspaceSymbolProvider":    true,
					"foldingRangeProvider":       true,
					"referencesProvider":         true,
					"renameProvider":             map[string]any{"prepareProvider": true},
					"codeActionProvider":         true,
					"codeLensProvider":           map[string]any{},
					"documentLinkProvider":       map[string]any{},
					"inlayHintProvider":          true,
					"colorProvider":              true,
					"signatureHelpProvider": map[string]any{
						"triggerCharacters": []string{"(", ","},
					},
					"semanticTokensProvider": map[string]any{
						"legend": map[string]any{
							"tokenTypes":     semanticTokenTypes,
							"tokenModifiers": []string{},
						},
						"full":  true,
						"range": true,
					},
				},
				"serverInfo": map[string]any{"name": "ic10c", "version": version.Short()},
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
		case "textDocument/didClose":
			s.didClose(writer, msg.Params)
		case "textDocument/completion":
			s.completion(writer, msg.ID, msg.Params)
		case "textDocument/formatting":
			s.formatting(writer, msg.ID, msg.Params)
		case "textDocument/hover":
			s.hover(writer, msg.ID, msg.Params)
		case "textDocument/definition":
			s.definition(writer, msg.ID, msg.Params)
		case "textDocument/documentSymbol":
			s.documentSymbol(writer, msg.ID, msg.Params)
		case "textDocument/codeLens":
			s.codeLens(writer, msg.ID, msg.Params)
		case "textDocument/documentLink":
			s.documentLink(writer, msg.ID, msg.Params)
		case "textDocument/documentHighlight":
			s.documentHighlight(writer, msg.ID, msg.Params)
		case "textDocument/selectionRange":
			s.selectionRange(writer, msg.ID, msg.Params)
		case "workspace/symbol":
			s.workspaceSymbol(writer, msg.ID, msg.Params)
		case "textDocument/foldingRange":
			s.foldingRange(writer, msg.ID, msg.Params)
		case "textDocument/references":
			s.references(writer, msg.ID, msg.Params)
		case "textDocument/rename":
			s.rename(writer, msg.ID, msg.Params)
		case "textDocument/signatureHelp":
			s.signatureHelp(writer, msg.ID, msg.Params)
		case "textDocument/codeAction":
			s.codeAction(writer, msg.ID, msg.Params)
		case "textDocument/semanticTokens/full":
			s.semanticTokens(writer, msg.ID, msg.Params)
		case "textDocument/semanticTokens/range":
			s.semanticTokensRange(writer, msg.ID, msg.Params)
		case "textDocument/inlayHint":
			s.inlayHint(writer, msg.ID, msg.Params)
		case "textDocument/prepareRename":
			s.prepareRename(writer, msg.ID, msg.Params)
		case "completionItem/resolve":
			s.resolveCompletion(writer, msg.ID, msg.Params)
		case "textDocument/documentColor":
			s.documentColor(writer, msg.ID, msg.Params)
		case "textDocument/colorPresentation":
			s.colorPresentation(writer, msg.ID, msg.Params)
		case "workspace/didChangeConfiguration":
			// Settings are applied at process start (env); nothing to do here.
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
	ContentChanges []contentChange `json:"contentChanges"`
}

type contentChange struct {
	Range *lspRange `json:"range"`
	Text  string    `json:"text"`
}

type textDocumentOnlyParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
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
	uri := p.TextDocument.URI
	text := s.docs[uri]
	for _, c := range p.ContentChanges {
		if c.Range == nil {
			text = c.Text
			continue
		}
		text = applyChange(text, *c.Range, c.Text)
	}
	s.docs[uri] = text
	s.publish(w, uri)
}

func (s *Server) didClose(w *bufio.Writer, params json.RawMessage) {
	var p textDocumentOnlyParams
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	delete(s.docs, p.TextDocument.URI)
	notify(w, "textDocument/publishDiagnostics", map[string]any{
		"uri":         p.TextDocument.URI,
		"diagnostics": []any{},
	})
}

// applyChange applies one LSP content change (incremental or full).
func applyChange(text string, r lspRange, replacement string) string {
	start := posToOffset(text, r.Start)
	end := posToOffset(text, r.End)
	if start < 0 || end > len(text) || start > end {
		return text
	}
	return text[:start] + replacement + text[end:]
}

// posToOffset converts an LSP position (0-based line, UTF-16 code unit column)
// to a byte offset in text.
func posToOffset(text string, pos lspPosition) int {
	offset := 0
	for line := 0; line < pos.Line; line++ {
		i := strings.IndexByte(text[offset:], '\n')
		if i < 0 {
			return len(text)
		}
		offset += i + 1
	}
	col := 0
	for offset < len(text) && text[offset] != '\n' && col < pos.Character {
		r, size := utf8.DecodeRuneInString(text[offset:])
		if r > 0xFFFF {
			col += 2
		} else {
			col++
		}
		offset += size
	}
	return offset
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
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
	Range              lspRange      `json:"range"`
	Severity           int           `json:"severity"`
	Source             string        `json:"source"`
	Code               string        `json:"code,omitempty"`
	CodeDescription    *codeDesc     `json:"codeDescription,omitempty"`
	Tags               []int         `json:"tags,omitempty"`
	RelatedInformation []relatedInfo `json:"relatedInformation,omitempty"`
	Message            string        `json:"message"`
}

type codeDesc struct {
	Href string `json:"href"`
}

type relatedInfo struct {
	Location map[string]any `json:"location"`
	Message  string         `json:"message"`
}

// codeDocURL returns a documentation link for a stable diagnostic code.
func codeDocURL(code string) string {
	if code == "" {
		return ""
	}
	return "https://github.com/peterxm/ic10go/blob/main/docs/spec.md#" + code
}

func (s *Server) publish(w *bufio.Writer, uri string) {
	text, ok := s.docs[uri]
	if !ok {
		return
	}
	code, diags, err := ic10.Compile(uri, []byte(text))
	items := []lspDiagnostic{}
	for _, d := range diags.Diags {
		line := d.Pos.Line - 1
		if line < 0 {
			line = 0
		}
		ch := d.Pos.Col - 1
		if ch < 0 {
			ch = 0
		}
		end := lspPosition{line, ch}
		if d.End.IsValid() {
			el := d.End.Line - 1
			if el < 0 {
				el = 0
			}
			ec := d.End.Col - 1
			if ec < 0 {
				ec = 0
			}
			end = lspPosition{el, ec}
		}
		item := lspDiagnostic{
			Range:    lspRange{Start: lspPosition{line, ch}, End: end},
			Severity: severity(int(d.Severity)),
			Source:   "ic10c",
			Code:     d.Code,
			Message:  d.Msg,
		}
		if url := codeDocURL(d.Code); url != "" {
			item.CodeDescription = &codeDesc{Href: url}
		}
		items = append(items, item)
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
	s.publishStats(w, uri, text, code, err, diags)
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
	Label         string         `json:"label"`
	Kind          int            `json:"kind"`
	Detail        string         `json:"detail,omitempty"`
	Documentation *markupContent `json:"documentation,omitempty"`
	Data          any            `json:"data,omitempty"`
}

// ci builds a completion item (positional literals no longer compile with the
// documentation field).
func ci(label string, kind int, detail string) completionItem {
	return completionItem{Label: label, Kind: kind, Detail: detail}
}

type markupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// attachDetail adds the builtin signature (detail) and a resolve payload to
// completion items. The full markdown documentation is filled in lazily by
// completionItem/resolve, keeping the initial list small.
func attachDetail(items []completionItem) []completionItem {
	for i := range items {
		items[i].Data = map[string]any{"label": items[i].Label}
		if items[i].Detail == "batch IO" {
			if d, ok := builtin.BatchDocs[items[i].Label]; ok {
				items[i].Detail = d.Signature
			}
			continue
		}
		if d, ok := builtin.Docs[items[i].Label]; ok {
			items[i].Detail = d.Signature
		}
	}
	return items
}

// resolveCompletion fills in the documentation for a completion item.
func (s *Server) resolveCompletion(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var item completionItem
	if err := json.Unmarshal(params, &item); err != nil {
		reply(w, id, nil)
		return
	}
	label := item.Label
	if m, ok := item.Data.(map[string]any); ok {
		if l, ok := m["label"].(string); ok {
			label = l
		}
	}
	switch {
	case hasDoc(builtin.Docs, label):
		d, _ := builtin.Docs[label]
		item.Detail = d.Signature
		item.Documentation = &markupContent{Kind: "markdown", Value: docText(d, s.zh)}
	case hasDoc(builtin.BatchDocs, label):
		d, _ := builtin.BatchDocs[label]
		item.Detail = d.Signature
		item.Documentation = &markupContent{Kind: "markdown", Value: docText(d, s.zh)}
	case hasDoc(builtin.KeywordDocs, label):
		d, _ := builtin.KeywordDocs[label]
		item.Documentation = &markupContent{Kind: "markdown", Value: docText(d, s.zh)}
	case hasDoc(builtin.LogicTypeDocs, label):
		d, _ := builtin.LogicTypeDocs[label]
		item.Documentation = &markupContent{Kind: "markdown", Value: docText(d, s.zh)}
	}
	reply(w, id, item)
}

func hasDoc(m map[string]builtin.Doc, key string) bool {
	_, ok := m[key]
	return ok
}

// ---------------------------------------------------------------------------
// Completion
// ---------------------------------------------------------------------------

func (s *Server) completion(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var p textDocumentParams
	if err := json.Unmarshal(params, &p); err != nil {
		reply(w, id, []any{})
		return
	}
	reply(w, id, attachDetail(completionItemsFor(s.docs[p.TextDocument.URI], p.Position)))
}

// completionItemsFor returns context-aware completion items: after a device
// port it offers logic types, after "batch" the batch methods, after an enum
// name its members, and elsewhere keywords plus the document's own symbols.
func completionItemsFor(text string, pos lspPosition) []completionItem {
	off := posToOffset(text, pos)
	i := off
	for i > 0 && isWordByte(text[i-1]) {
		i--
	}
	if i > 0 && text[i-1] == '.' {
		if i >= 2 && text[i-2] == ']' {
			return slotTypeItems()
		}
		j := i - 1
		for j > 0 && isWordByte(text[j-1]) {
			j--
		}
		recv := text[j : i-1]
		switch {
		case recv == "batch":
			return batchMethodItems()
		case isDevicePort(recv):
			return logicTypeItems()
		case enumReceiver(recv):
			return enumItems(recv)
		default:
			return logicTypeItems()
		}
	}
	items := baseCompletionItems()
	for _, r := range enumReceivers() {
		items = append(items, completionItem{Label: r, Kind: 9, Detail: "enum"})
	}
	items = append(items, batchMethodItems()...)
	items = append(items, documentSymbols("", text)...)
	return items
}

func baseCompletionItems() []completionItem {
	items := []completionItem{
		ci("const", 14, "declaration"), ci("data", 14, "data table"), ci("var", 14, "declaration"), ci("func", 3, "declaration"),
		ci("if", 14, ""), ci("else", 14, ""), ci("for", 14, ""), ci("switch", 14, ""),
		ci("case", 14, ""), ci("default", 14, ""), ci("break", 14, ""), ci("continue", 14, ""),
		ci("range", 14, "for range loop"),
		ci("return", 14, ""), ci("label", 14, ""), ci("goto", 14, ""), ci("call", 14, ""), ci("ret", 14, ""),
		ci("table", 14, "switch modifier"),
		ci("true", 12, ""), ci("false", 12, ""), ci("nan", 12, ""), ci("pinf", 12, ""), ci("ninf", 12, ""),
		ci("d0", 6, "device"), ci("d1", 6, "device"), ci("d2", 6, "device"),
		ci("d3", 6, "device"), ci("d4", 6, "device"), ci("d5", 6, "device"), ci("db", 6, "device"),
		ci("batch", 9, "batch IO"),
		ci("read", 3, "runtime logic type"),
		ci("write", 3, "runtime logic type"),
		ci("readDev", 3, "runtime device + logic type"),
		ci("writeDev", 3, "runtime device + logic type"),
		ci("isLoadValid", 3, "condition only"),
		ci("isStoreValid", 3, "condition only"),
	}
	for name := range builtin.Funcs {
		items = append(items, completionItem{Label: name, Kind: 3, Detail: "builtin"})
	}
	for name := range builtin.LogicTypes {
		items = append(items, completionItem{Label: name, Kind: 21, Detail: "logic type"})
	}
	for name := range builtin.SlotTypes {
		items = append(items, completionItem{Label: name, Kind: 21, Detail: "slot type"})
	}
	return items
}

func logicTypeItems() []completionItem {
	var items []completionItem
	for name := range builtin.LogicTypes {
		items = append(items, completionItem{Label: name, Kind: 21, Detail: "logic type"})
	}
	sortItems(items)
	return items
}

func slotTypeItems() []completionItem {
	var items []completionItem
	for name := range builtin.SlotTypes {
		items = append(items, completionItem{Label: name, Kind: 21, Detail: "slot type"})
	}
	sortItems(items)
	return items
}

func batchMethodItems() []completionItem {
	names := []string{"read", "readName", "readSlot", "readNameSlot", "write", "writeName", "writeSlot"}
	items := make([]completionItem, 0, len(names))
	for _, n := range names {
		items = append(items, completionItem{Label: n, Kind: 3, Detail: "batch IO"})
	}
	return items
}

func enumReceivers() []string {
	seen := map[string]bool{}
	var out []string
	for k := range builtin.EnumConstants {
		if i := strings.IndexByte(k, '.'); i > 0 {
			r := k[:i]
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	// LogicType members are modelled separately from EnumConstants.
	if !seen["LogicType"] {
		out = append(out, "LogicType")
	}
	sort.Strings(out)
	return out
}

func enumReceiver(s string) bool {
	if s == "LogicType" {
		return true
	}
	for k := range builtin.EnumConstants {
		if strings.HasPrefix(k, s+".") {
			return true
		}
	}
	return false
}

func enumItems(recv string) []completionItem {
	var items []completionItem
	if recv == "LogicType" {
		for name := range builtin.LogicTypeIDs {
			items = append(items, completionItem{Label: name, Kind: 21, Detail: "logic type"})
		}
		sortItems(items)
		return items
	}
	for k := range builtin.EnumConstants {
		if member, ok := strings.CutPrefix(k, recv+"."); ok {
			items = append(items, completionItem{Label: member, Kind: 21, Detail: "enum"})
		}
	}
	sortItems(items)
	return items
}

func isDevicePort(s string) bool {
	if s == "db" {
		return true
	}
	return len(s) == 2 && s[0] == 'd' && s[1] >= '0' && s[1] <= '5'
}

func sortItems(items []completionItem) {
	sort.Slice(items, func(i, j int) bool { return items[i].Label < items[j].Label })
}

// documentSymbols collects the user-defined functions, constants, variables and
// labels of a document for completion.
func documentSymbols(name, text string) []completionItem {
	file := source.NewFile(name, []byte(text))
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)
	tree := parser.Parse(file, toks, diags)
	if tree == nil {
		return nil
	}
	var items []completionItem
	seen := map[string]bool{}
	add := func(n string, kind int, detail string) {
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		items = append(items, completionItem{Label: n, Kind: kind, Detail: detail})
	}
	for _, d := range tree.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			add(d.Name.Name, 3, "function")
		case *ast.ConstDecl:
			add(d.Name.Name, 21, "constant")
		case *ast.DataDecl:
			add(d.Name.Name, 21, "data table")
		case *ast.VarDecl:
			add(d.Name.Name, 6, "variable")
		}
	}
	for _, d := range tree.Decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Body != nil {
			collectStmtSymbols(f.Body.List, add)
		}
	}
	return items
}

func collectStmtSymbols(stmts []ast.Stmt, add func(string, int, string)) {
	for _, s := range stmts {
		switch s := s.(type) {
		case *ast.DeclStmt:
			switch d := s.Decl.(type) {
			case *ast.VarDecl:
				add(d.Name.Name, 6, "variable")
			case *ast.ConstDecl:
				add(d.Name.Name, 21, "constant")
			}
		case *ast.AssignStmt:
			if id, ok := s.Lhs.(*ast.Ident); ok {
				add(id.Name, 6, "variable")
			}
		case *ast.LabelStmt:
			add(s.Name.Name, 2, "label")
		case *ast.IfStmt:
			if s.Init != nil {
				collectStmtSymbols([]ast.Stmt{s.Init}, add)
			}
			if s.Then != nil {
				collectStmtSymbols(s.Then.List, add)
			}
			switch e := s.Else.(type) {
			case *ast.BlockStmt:
				collectStmtSymbols(e.List, add)
			case *ast.IfStmt:
				collectStmtSymbols([]ast.Stmt{e}, add)
			}
		case *ast.ForStmt:
			if s.Body != nil {
				collectStmtSymbols(s.Body.List, add)
			}
		case *ast.RangeStmt:
			if s.Body != nil {
				collectStmtSymbols(s.Body.List, add)
			}
		case *ast.SwitchStmt:
			if s.Init != nil {
				collectStmtSymbols([]ast.Stmt{s.Init}, add)
			}
			for _, c := range s.Cases {
				collectStmtSymbols(c.Body, add)
			}
		case *ast.BlockStmt:
			collectStmtSymbols(s.List, add)
		}
	}
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
	text := s.docs[p.TextDocument.URI]
	if recv, member := enumMemberAt(text, p.Position); recv != "" {
		if content := s.enumMemberHover(recv, member); content != "" {
			reply(w, id, map[string]any{
				"contents": map[string]any{"kind": "markdown", "value": content},
			})
			return
		}
	}
	word, start := wordAtOffset(text, p.Position)
	content := s.hoverFor(text, word)
	if t := exprTypeAt(text, word, start); t != sema.Any {
		if content != "" {
			content += "\n\n"
		}
		if s.zh {
			content += "类型: `" + t.String() + "`"
		} else {
			content += "type: `" + t.String() + "`"
		}
	}
	if content == "" {
		reply(w, id, nil)
		return
	}
	reply(w, id, map[string]any{
		"contents": map[string]any{"kind": "markdown", "value": content},
	})
}

// exprTypeAt returns the static type of the identifier named word starting at
// byte offset start, if the type checker resolved it.
func exprTypeAt(text, word string, start int) sema.Type {
	if word == "" {
		return sema.Any
	}
	tree := parseText(text)
	if tree == nil {
		return sema.Any
	}
	info := sema.Check(tree, &diag.Bag{})
	for id, t := range info.VarTypes {
		if id.Name == word && id.Pos().Offset == start {
			return t
		}
	}
	return sema.Any
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
	w, _ := wordAtOffset(text, pos)
	return w
}

// wordAtOffset returns the word under pos and its starting byte offset.
func wordAtOffset(text string, pos lspPosition) (string, int) {
	off := posToOffset(text, pos)
	start := off
	for start > 0 && isWordByte(text[start-1]) {
		start--
	}
	end := off
	for end < len(text) && isWordByte(text[end]) {
		end++
	}
	return text[start:end], start
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// docText renders a Doc as markdown in the requested language.
func docText(d builtin.Doc, zh bool) string {
	desc := d.EN
	if zh {
		desc = d.ZH
	}
	if d.Signature != "" {
		return "```icg\n" + d.Signature + "\n```\n\n" + desc
	}
	return desc
}

// enumMemberAt returns the enum receiver and member when pos sits on a member
// access such as `Color.Purple`.
func enumMemberAt(text string, pos lspPosition) (string, string) {
	off := posToOffset(text, pos)
	start := off
	for start > 0 && isWordByte(text[start-1]) {
		start--
	}
	end := off
	for end < len(text) && isWordByte(text[end]) {
		end++
	}
	if start == 0 || text[start-1] != '.' {
		return "", ""
	}
	member := text[start:end]
	j := start - 1
	rEnd := j
	for j > 0 && isWordByte(text[j-1]) {
		j--
	}
	recv := text[j:rEnd]
	if recv == "" || member == "" {
		return "", ""
	}
	return recv, member
}

// enumMemberHover renders `Receiver.Member = value` plus any documented text.
func (s *Server) enumMemberHover(recv, member string) string {
	key := recv + "." + member
	v, ok := builtin.EnumConstants[key]
	if !ok {
		return ""
	}
	var b strings.Builder
	b.WriteString("```icg\n")
	b.WriteString(key)
	b.WriteString(" = ")
	b.WriteString(strconv.FormatFloat(v, 'g', -1, 64))
	b.WriteString("\n```")
	if d, ok := builtin.EnumMemberDocs[key]; ok {
		b.WriteString("\n\n")
		b.WriteString(pickLang(d, s.zh))
	} else if d, ok := builtin.EnumDocs[recv]; ok {
		b.WriteString("\n\n")
		b.WriteString(pickLang(d, s.zh))
	}
	return b.String()
}

func pickLang(d builtin.Doc, zh bool) string {
	if zh {
		return d.ZH
	}
	return d.EN
}

func (s *Server) hoverFor(text, word string) string {
	if dev := deviceAliasOf(text, word); dev != "" {
		if s.zh {
			return "设备别名 `" + word + "` = `" + dev + "`"
		}
		return "device alias `" + word + "` = `" + dev + "`"
	}
	if d, ok := builtin.Docs[word]; ok {
		return docText(d, s.zh)
	}
	if d, ok := builtin.KeywordDocs[word]; ok {
		return docText(d, s.zh)
	}
	if d, ok := builtin.LogicTypeDocs[word]; ok {
		return docText(d, s.zh)
	}
	switch word {
	case "true", "false", "nan", "pinf", "ninf":
		return "literal `" + word + "`"
	}
	if word == "db" || isDevicePort(word) {
		if s.zh {
			return "设备端口 `" + word + "`"
		}
		return "device port `" + word + "`"
	}
	if builtin.LogicTypes[word] {
		if s.zh {
			return "logic type `" + word + "`（设备逻辑属性）"
		}
		return "logic type `" + word + "` (device property)"
	}
	if builtin.SlotTypes[word] {
		if s.zh {
			return "slot type `" + word + "`（槽位属性）"
		}
		return "slot type `" + word + "` (slot property)"
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
	start := utf16Len(line[:col])
	return map[string]any{
		"uri": uri,
		"range": lspRange{
			Start: lspPosition{lineIdx, start},
			End:   lspPosition{lineIdx, start + utf16Len(word)},
		},
	}
}

// ---------------------------------------------------------------------------
// JSON-RPC framing
// ---------------------------------------------------------------------------

// fileURIToPath converts a file:// URI to a filesystem path.
func fileURIToPath(uri string) string {
	if uri == "" {
		return ""
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	p := u.Path
	if p == "" {
		p = u.Opaque
	}
	if decoded, err := url.PathUnescape(p); err == nil {
		p = decoded
	}
	return p
}

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
	// Always include "result" (even when null): clients match responses by id
	// and some rely on the field being present.
	send(w, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func replyError(w *bufio.Writer, id json.RawMessage, code int, msg string) {
	send(w, response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func notify(w *bufio.Writer, method string, params any) {
	send(w, notification{JSONRPC: "2.0", Method: method, Params: params})
}
