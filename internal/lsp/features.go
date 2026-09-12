package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strings"

	"ic10go/internal/ast"
	"ic10go/internal/builtin"
	"ic10go/internal/codegen"
	"ic10go/internal/diag"
	"ic10go/internal/lexer"
	"ic10go/internal/parser"
	"ic10go/internal/sema"
	"ic10go/internal/source"
	"ic10go/internal/token"
	"ic10go/pkg/ic10"
)

// offsetToLSP converts a byte offset to a 0-based line / UTF-16 column position.
func offsetToLSP(text string, offset int) lspPosition {
	if offset > len(text) {
		offset = len(text)
	}
	line := strings.Count(text[:offset], "\n")
	lineStart := strings.LastIndexByte(text[:offset], '\n') + 1
	return lspPosition{Line: line, Character: utf16Len(text[lineStart:offset])}
}

func parseText(text string) *ast.File {
	file := source.NewFile("", []byte(text))
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)
	return parser.Parse(file, toks, diags)
}

func matchBrace(text string, open int) int {
	depth := 0
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(text)
}

// ---------------------------------------------------------------------------
// Document symbols
// ---------------------------------------------------------------------------

type documentSymbol struct {
	Name           string           `json:"name"`
	Kind           int              `json:"kind"`
	Range          lspRange         `json:"range"`
	SelectionRange lspRange         `json:"selectionRange"`
	Children       []documentSymbol `json:"children,omitempty"`
}

func (s *Server) documentSymbol(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var p textDocumentOnlyParams
	if err := json.Unmarshal(params, &p); err != nil {
		reply(w, id, []any{})
		return
	}
	reply(w, id, documentSymbolsFor(s.docs[p.TextDocument.URI]))
}

func documentSymbolsFor(text string) []documentSymbol {
	tree := parseText(text)
	if tree == nil {
		return nil
	}
	var out []documentSymbol
	for _, d := range tree.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			sym := documentSymbol{
				Name:           d.Name.Name,
				Kind:           12, // Function
				Range:          funcRange(text, d),
				SelectionRange: nameRange(text, d.Name.Name, d.Name.Pos().Offset),
			}
			if d.Body != nil {
				sym.Children = localSymbols(text, d.Body.List)
			}
			out = append(out, sym)
		case *ast.ConstDecl:
			out = append(out, documentSymbol{Name: d.Name.Name, Kind: 14, Range: lineRange(text, d.Pos().Offset), SelectionRange: nameRange(text, d.Name.Name, d.Name.Pos().Offset)})
		case *ast.VarDecl:
			out = append(out, documentSymbol{Name: d.Name.Name, Kind: 13, Range: lineRange(text, d.Pos().Offset), SelectionRange: nameRange(text, d.Name.Name, d.Name.Pos().Offset)})
		}
	}
	return out
}

func localSymbols(text string, stmts []ast.Stmt) []documentSymbol {
	var out []documentSymbol
	for _, st := range stmts {
		switch st := st.(type) {
		case *ast.DeclStmt:
			switch d := st.Decl.(type) {
			case *ast.VarDecl:
				out = append(out, documentSymbol{Name: d.Name.Name, Kind: 13, Range: lineRange(text, d.Pos().Offset), SelectionRange: nameRange(text, d.Name.Name, d.Name.Pos().Offset)})
			case *ast.ConstDecl:
				out = append(out, documentSymbol{Name: d.Name.Name, Kind: 14, Range: lineRange(text, d.Pos().Offset), SelectionRange: nameRange(text, d.Name.Name, d.Name.Pos().Offset)})
			}
		case *ast.AssignStmt:
			if id, ok := st.Lhs.(*ast.Ident); ok && st.Op == token.Define {
				out = append(out, documentSymbol{Name: id.Name, Kind: 13, Range: lineRange(text, id.Pos().Offset), SelectionRange: nameRange(text, id.Name, id.Pos().Offset)})
			}
		case *ast.LabelStmt:
			out = append(out, documentSymbol{Name: st.Name.Name, Kind: 20, Range: lineRange(text, st.Name.Pos().Offset), SelectionRange: nameRange(text, st.Name.Name, st.Name.Pos().Offset)})
		case *ast.IfStmt:
			if st.Then != nil {
				out = append(out, localSymbols(text, st.Then.List)...)
			}
			switch e := st.Else.(type) {
			case *ast.BlockStmt:
				out = append(out, localSymbols(text, e.List)...)
			case *ast.IfStmt:
				out = append(out, localSymbols(text, []ast.Stmt{e})...)
			}
		case *ast.ForStmt:
			if st.Body != nil {
				out = append(out, localSymbols(text, st.Body.List)...)
			}
		case *ast.SwitchStmt:
			for _, c := range st.Cases {
				out = append(out, localSymbols(text, c.Body)...)
			}
		case *ast.BlockStmt:
			out = append(out, localSymbols(text, st.List)...)
		}
	}
	return out
}

func funcRange(text string, d *ast.FuncDecl) lspRange {
	start := offsetToLSP(text, d.Pos().Offset)
	end := start
	if d.Body != nil {
		end = offsetToLSP(text, matchBrace(text, d.Body.Pos().Offset))
	}
	return lspRange{Start: start, End: end}
}

func nameRange(text, name string, offset int) lspRange {
	start := offsetToLSP(text, offset)
	return lspRange{Start: start, End: lspPosition{Line: start.Line, Character: start.Character + utf16Len(name)}}
}

func lineRange(text string, offset int) lspRange {
	start := offsetToLSP(text, offset)
	lineStart := strings.LastIndexByte(text[:offset], '\n') + 1
	lineEnd := len(text)
	if i := strings.IndexByte(text[offset:], '\n'); i >= 0 {
		lineEnd = offset + i
	}
	_ = lineStart
	return lspRange{Start: start, End: offsetToLSP(text, lineEnd)}
}

// ---------------------------------------------------------------------------
// Folding ranges
// ---------------------------------------------------------------------------

type foldingRange struct {
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Kind      string `json:"kind,omitempty"`
}

func (s *Server) foldingRange(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var p textDocumentOnlyParams
	if err := json.Unmarshal(params, &p); err != nil {
		reply(w, id, []any{})
		return
	}
	reply(w, id, foldingRangesFor(s.docs[p.TextDocument.URI]))
}

func foldingRangesFor(text string) []foldingRange {
	file := source.NewFile("", []byte(text))
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)
	var out []foldingRange
	var stack []token.Token
	for i, t := range toks {
		switch t.Kind {
		case token.LBrace:
			stack = append(stack, t)
		case token.LParen:
			// Only fold const (...) blocks, not expression parentheses.
			if i > 0 && toks[i-1].Kind == token.Const {
				stack = append(stack, t)
			}
		case token.RBrace, token.RParen:
			if len(stack) == 0 {
				continue
			}
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if open.Kind == token.LBrace && t.Kind != token.RBrace {
				continue
			}
			if open.Kind == token.LParen && t.Kind != token.RParen {
				continue
			}
			start := open.Pos.Line - 1
			end := t.Pos.Line - 1
			if end > start+1 {
				out = append(out, foldingRange{StartLine: start, EndLine: end, Kind: "region"})
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// References / rename
// ---------------------------------------------------------------------------

func (s *Server) references(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Position lspPosition `json:"position"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		reply(w, id, []any{})
		return
	}
	text := s.docs[p.TextDocument.URI]
	word := wordAt(text, p.Position)
	if word == "" {
		reply(w, id, []any{})
		return
	}
	var locs []any
	for _, r := range identifierRanges(text, word) {
		locs = append(locs, map[string]any{"uri": p.TextDocument.URI, "range": r})
	}
	reply(w, id, locs)
}

func identifierRanges(text, word string) []lspRange {
	file := source.NewFile("", []byte(text))
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)
	var out []lspRange
	for i, t := range toks {
		if t.Kind != token.Ident || t.Text != word {
			continue
		}
		if i > 0 && toks[i-1].Kind == token.Dot {
			continue // member access, not a variable reference
		}
		start := offsetToLSP(text, t.Pos.Offset)
		out = append(out, lspRange{Start: start, End: lspPosition{Line: start.Line, Character: start.Character + utf16Len(word)}})
	}
	return out
}

func (s *Server) rename(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Position lspPosition `json:"position"`
		NewName  string      `json:"newName"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		reply(w, id, nil)
		return
	}
	if !validIdentifier(p.NewName) {
		replyError(w, id, -32602, "invalid identifier: "+p.NewName)
		return
	}
	text := s.docs[p.TextDocument.URI]
	word := wordAt(text, p.Position)
	if word == "" {
		reply(w, id, nil)
		return
	}
	var edits []any
	for _, r := range identifierRanges(text, word) {
		edits = append(edits, map[string]any{"range": r, "newText": p.NewName})
	}
	if len(edits) == 0 {
		reply(w, id, nil)
		return
	}
	reply(w, id, map[string]any{
		"changes": map[string]any{p.TextDocument.URI: edits},
	})
}

func validIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return token.Lookup(s) == token.Ident
}

// ---------------------------------------------------------------------------
// Signature help
// ---------------------------------------------------------------------------

func (s *Server) signatureHelp(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var p textDocumentParams
	if err := json.Unmarshal(params, &p); err != nil {
		reply(w, id, nil)
		return
	}
	text := s.docs[p.TextDocument.URI]
	reply(w, id, signatureHelpFor(text, p.Position))
}

func signatureHelpFor(text string, pos lspPosition) any {
	off := posToOffset(text, pos)
	file := source.NewFile("", []byte(text))
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)

	// Find the innermost unmatched '(' before the cursor.
	depth := 0
	open := -1
	for i := len(toks) - 1; i >= 0; i-- {
		if toks[i].Pos.Offset >= off {
			continue
		}
		switch toks[i].Kind {
		case token.RParen:
			depth++
		case token.LParen:
			if depth == 0 {
				open = i
			} else {
				depth--
			}
		}
		if open >= 0 {
			break
		}
	}
	if open <= 0 || toks[open-1].Kind != token.Ident {
		return nil
	}
	name := toks[open-1].Text

	// Count commas between '(' and the cursor at depth 0.
	active := 0
	d := 0
	for i := open + 1; i < len(toks) && toks[i].Pos.Offset < off; i++ {
		switch toks[i].Kind {
		case token.LParen:
			d++
		case token.RParen:
			if d > 0 {
				d--
			}
		case token.Comma:
			if d == 0 {
				active++
			}
		}
	}

	label, params := signatureFor(text, name)
	if label == "" {
		return nil
	}
	return map[string]any{
		"signatures": []any{map[string]any{
			"label":      label,
			"parameters": params,
		}},
		"activeSignature": 0,
		"activeParameter": active,
	}
}

func signatureFor(text, name string) (string, []any) {
	if tree := parseText(text); tree != nil {
		for _, d := range tree.Decls {
			if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == name {
				var parts []string
				var params []any
				for i, p := range f.Params {
					parts = append(parts, p.Name.Name+" "+p.Type)
					start := 0
					for j := 0; j < i; j++ {
						start += len(parts[j]) + 2
					}
					params = append(params, map[string]any{
						"label": [2]int{start, start + len(p.Name.Name+" "+p.Type)},
					})
				}
				label := name + "(" + strings.Join(parts, ", ") + ")"
				if f.Result != "" {
					label += " " + f.Result
				}
				return label, params
			}
		}
	}
	if f, ok := builtin.Funcs[name]; ok {
		var parts []string
		for i := 0; i < f.Args; i++ {
			parts = append(parts, fmt.Sprintf("arg%d", i+1))
		}
		return name + "(" + strings.Join(parts, ", ") + ")", nil
	}
	return "", nil
}

// ---------------------------------------------------------------------------
// Code actions
// ---------------------------------------------------------------------------

func (s *Server) codeAction(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Context struct {
			Diagnostics []lspDiagnostic `json:"diagnostics"`
		} `json:"context"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		reply(w, id, []any{})
		return
	}
	uri := p.TextDocument.URI
	var actions []any
	for _, d := range p.Context.Diagnostics {
		switch {
		case strings.Contains(d.Message, "unknown logic type"):
			if name := quoted(d.Message); name != "" {
				if fix := closest(name, builtin.LogicTypes); fix != "" {
					actions = append(actions, replaceAction(uri, "Change logic type to "+fix, d.Range, fix))
				}
			}
		case strings.Contains(d.Message, "unknown slot type"):
			if name := quoted(d.Message); name != "" {
				if fix := closest(name, builtin.SlotTypes); fix != "" {
					actions = append(actions, replaceAction(uri, "Change slot type to "+fix, d.Range, fix))
				}
			}
		case strings.Contains(d.Message, "no main function"):
			actions = append(actions, map[string]any{
				"title": "Add a main function",
				"kind":  "quickfix",
				"edit": map[string]any{
					"changes": map[string]any{uri: []any{map[string]any{
						"range":   lspRange{Start: lspPosition{0, 0}, End: lspPosition{0, 0}},
						"newText": "func main() {\n    for {\n        yield()\n    }\n}\n\n",
					}}},
				},
			})
		}
	}
	reply(w, id, actions)
}

func replaceAction(uri, title string, r lspRange, text string) any {
	return map[string]any{
		"title": title,
		"kind":  "quickfix",
		"edit": map[string]any{
			"changes": map[string]any{uri: []any{map[string]any{"range": r, "newText": text}}},
		},
	}
}

func quoted(msg string) string {
	i := strings.IndexByte(msg, '"')
	if i < 0 {
		return ""
	}
	j := strings.IndexByte(msg[i+1:], '"')
	if j < 0 {
		return ""
	}
	return msg[i+1 : i+1+j]
}

func closest(name string, set map[string]bool) string {
	best := ""
	bestDist := 1 << 30
	for cand := range set {
		d := levenshtein(name, cand)
		if d < bestDist {
			bestDist = d
			best = cand
		}
	}
	if best == "" || bestDist > 4 {
		return ""
	}
	return best
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// ---------------------------------------------------------------------------
// Semantic tokens
// ---------------------------------------------------------------------------

// semanticTokenTypes is the legend; custom types (device/logicType/builtin)
// are appended after the standard ones.
var semanticTokenTypes = []string{
	"namespace", "type", "class", "enum", "interface", "struct", "typeParameter",
	"parameter", "variable", "property", "enumMember", "event", "function", "method",
	"macro", "keyword", "modifier", "comment", "string", "number", "regexp", "operator",
	"decorator", "device", "logicType", "builtin",
}

var semanticTokenIndex = func() map[string]int {
	m := map[string]int{}
	for i, t := range semanticTokenTypes {
		m[t] = i
	}
	return m
}()

func (s *Server) semanticTokens(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var p textDocumentOnlyParams
	if err := json.Unmarshal(params, &p); err != nil {
		reply(w, id, map[string]any{"data": []int{}})
		return
	}
	reply(w, id, map[string]any{"data": semanticTokensFor(s.docs[p.TextDocument.URI])})
}

func semanticTokensFor(text string) []int {
	file := source.NewFile("", []byte(text))
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)
	var data []int
	prevLine, prevChar := 0, 0
	emit := func(pos lspPosition, length, typ int) {
		if length <= 0 {
			return
		}
		dl := pos.Line - prevLine
		dc := pos.Character
		if dl == 0 {
			dc = pos.Character - prevChar
		}
		data = append(data, dl, dc, length, typ, 0)
		prevLine, prevChar = pos.Line, pos.Character
	}
	for i, t := range toks {
		pos := offsetToLSP(text, t.Pos.Offset)
		length := utf16Len(t.Text)
		typ := -1
		switch {
		case t.Kind == token.Device:
			typ = semanticTokenIndex["device"]
		case t.Kind == token.Number:
			typ = semanticTokenIndex["number"]
		case t.Kind == token.String:
			typ = semanticTokenIndex["string"]
		case t.Kind == token.Ident:
			if _, ok := builtin.Funcs[t.Text]; ok {
				typ = semanticTokenIndex["builtin"]
			} else if builtin.LogicTypes[t.Text] {
				typ = semanticTokenIndex["logicType"]
			} else if builtin.SlotTypes[t.Text] {
				typ = semanticTokenIndex["logicType"]
			} else if i+1 < len(toks) && toks[i+1].Kind == token.LParen {
				typ = semanticTokenIndex["function"]
			} else if i > 0 && toks[i-1].Kind == token.Dot {
				typ = semanticTokenIndex["property"]
			} else {
				typ = semanticTokenIndex["variable"]
			}
		case t.Kind >= token.Const && t.Kind <= token.NInf:
			typ = semanticTokenIndex["keyword"]
		default:
			typ = semanticTokenIndex["operator"]
		}
		emit(pos, length, typ)
	}
	return data
}

// ---------------------------------------------------------------------------
// Inlay hints
// ---------------------------------------------------------------------------

func (s *Server) inlayHint(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var p textDocumentOnlyParams
	if err := json.Unmarshal(params, &p); err != nil {
		reply(w, id, []any{})
		return
	}
	text := s.docs[p.TextDocument.URI]
	code, diags, err := ic10.Compile(p.TextDocument.URI, []byte(text))
	if diags.HasErrors() || err != nil {
		reply(w, id, []any{})
		return
	}
	st := ic10.StatsOf(code)
	label := fmt.Sprintf("  IC10: %d/%d 行 · %d/%d 字节 · %d/%d 寄存器", st.Lines, codegen.MaxLines, st.Bytes, codegen.MaxBytes, st.RegsUsed, 16)
	if base, size, _, _ := ic10.DataStats(p.TextDocument.URI, []byte(text), ic10.Options{}); base >= 0 {
		label += fmt.Sprintf(" · data %d..%d", base, base+size-1)
	}
	lastLine := strings.Count(text, "\n")
	lastStart := strings.LastIndexByte(text, '\n') + 1
	hint := map[string]any{
		"position": lspPosition{Line: lastLine, Character: utf16Len(text[lastStart:])},
		"label":    label,
		"kind":     1,
	}
	reply(w, id, []any{hint})
}

// publishStats notifies the client of the compiled program's budget so it can
// show a persistent status indicator.
func (s *Server) publishStats(w *bufio.Writer, uri, text, code string, err error, diags *diag.Bag) {
	if err != nil || diags.HasErrors() {
		notify(w, "icg/stats", map[string]any{"uri": uri, "error": true})
		return
	}
	st := ic10.StatsOf(code)
	payload := map[string]any{
		"uri":        uri,
		"lines":      st.Lines,
		"bytes":      st.Bytes,
		"maxLineLen": st.MaxLineLen,
		"regs":       st.RegsUsed,
		"maxLines":   codegen.MaxLines,
		"maxBytes":   codegen.MaxBytes,
		"maxLineMax": codegen.MaxLineLen,
		"maxRegs":    16,
	}
	if base, size, autoTabled, warn := ic10.DataStats(uri, []byte(text), ic10.Options{}); base >= 0 {
		payload["dataBase"] = base
		payload["dataSize"] = size
		payload["dataEnd"] = base + size - 1
		payload["autoTabled"] = autoTabled
		if warn != "" {
			payload["dataWarn"] = warn
		}
		if depth, unbounded, err := ic10.MaxStackDepth(uri, []byte(text), ic10.Options{}); err == nil {
			payload["stackDepth"] = depth
			payload["stackUnbounded"] = unbounded
		}
	}
	notify(w, "icg/stats", payload)
}

// deviceAliasOf returns the device port a name aliases via `const NAME = dN`.
func deviceAliasOf(text, word string) string {
	tree := parseText(text)
	if tree == nil {
		return ""
	}
	info := sema.Check(tree, &diag.Bag{})
	return info.Devices[word]
}
