package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

// flatDecls expands `chip` blocks so editor features see chip-local functions,
// constants and data tables as well as the shared top-level ones.
func flatDecls(decls []ast.Decl) []ast.Decl {
	var out []ast.Decl
	for _, d := range decls {
		if ch, ok := d.(*ast.ChipDecl); ok {
			out = append(out, flatDecls(ch.Decls)...)
			continue
		}
		out = append(out, d)
	}
	return out
}

// busNamesIn returns the names declared with `bus`, so the editor can classify
// bus accesses (`Display.slot`) as namespaces.
func busNamesIn(text string) map[string]bool {
	tree := parseText(text)
	if tree == nil {
		return nil
	}
	var out map[string]bool
	for _, d := range flatDecls(tree.Decls) {
		if bus, ok := d.(*ast.BusDecl); ok {
			if out == nil {
				out = map[string]bool{}
			}
			out[bus.Name.Name] = true
		}
	}
	return out
}

// declsAtOffset returns the declarations visible at off: the shared top-level
// ones plus, when off is inside a chip, that chip's own declarations (so other
// chips' locals are not offered).
func declsAtOffset(tree *ast.File, off int) []ast.Decl {
	common := make([]ast.Decl, 0, len(tree.Decls))
	for _, d := range tree.Decls {
		if _, ok := d.(*ast.ChipDecl); !ok {
			common = append(common, d)
		}
	}
	for i, d := range tree.Decls {
		ch, ok := d.(*ast.ChipDecl)
		if !ok {
			continue
		}
		end := 1 << 30
		if i+1 < len(tree.Decls) {
			end = tree.Decls[i+1].Pos().Offset
		}
		if off >= ch.Pos().Offset && off < end {
			out := make([]ast.Decl, 0, len(common)+len(ch.Decls))
			out = append(out, common...)
			return append(out, ch.Decls...)
		}
	}
	return common
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

// codeLens offers a "Compile to IC10" action above every function.
func (s *Server) codeLens(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var p textDocumentOnlyParams
	if err := json.Unmarshal(params, &p); err != nil {
		reply(w, id, []any{})
		return
	}
	text := s.docs[p.TextDocument.URI]
	var out []any
	for _, sym := range documentSymbolsFor(text) {
		if sym.Kind != 12 { // Function
			continue
		}
		out = append(out, map[string]any{
			"range": sym.SelectionRange,
			"command": map[string]any{
				"title":   "Compile to IC10",
				"command": "icg.compile",
			},
		})
	}
	reply(w, id, out)
}

// documentLink links function calls and data-table references to the line of
// their declaration in the same document.
func (s *Server) documentLink(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var p textDocumentOnlyParams
	if err := json.Unmarshal(params, &p); err != nil {
		reply(w, id, []any{})
		return
	}
	links := documentLinksFor(p.TextDocument.URI, s.docs[p.TextDocument.URI])
	links = append(links, prefabLinksFor(s.docs[p.TextDocument.URI])...)
	reply(w, id, links)
}

// wikiURL links a prefab display title to a community-wiki search.
func wikiURL(title string) string {
	return "https://stationeers-wiki.com/index.php?search=" + url.QueryEscape(title)
}

// prefabLinksFor links prefab names inside hash("…") / HASH("…") and numeric
// prefab hashes to the community wiki.
func prefabLinksFor(text string) []any {
	var out []any
	for _, fn := range []string{`hash("`, `HASH("`} {
		idx := 0
		for {
			i := strings.Index(text[idx:], fn)
			if i < 0 {
				break
			}
			contentStart := idx + i + len(fn)
			end := strings.IndexByte(text[contentStart:], '"')
			if end < 0 {
				break
			}
			name := text[contentStart : contentStart+end]
			idx = contentStart + end
			title, ok := builtin.Prefabs[name]
			if !ok {
				continue
			}
			start := offsetToLSP(text, contentStart)
			stop := offsetToLSP(text, contentStart+end)
			out = append(out, map[string]any{
				"range":  lspRange{Start: start, End: stop},
				"target": wikiURL(title),
			})
		}
	}
	file := source.NewFile("", []byte(text))
	diags := &diag.Bag{}
	for _, t := range lexer.Tokenize(file, diags) {
		if t.Kind != token.Number {
			continue
		}
		h, ok := parseNumToken(t.Text)
		if !ok {
			continue
		}
		name, ok := builtin.PrefabByHash[h]
		if !ok {
			continue
		}
		start := offsetToLSP(text, t.Pos.Offset)
		end := lspPosition{Line: start.Line, Character: start.Character + utf16Len(t.Text)}
		out = append(out, map[string]any{
			"range":  lspRange{Start: start, End: end},
			"target": wikiURL(builtin.Prefabs[name]),
		})
	}
	return out
}

// parseNumToken parses an IC10 numeric literal (decimal / $hex / %binary) into
// its unsigned 32-bit form.
func parseNumToken(s string) (uint32, bool) {
	var v uint64
	var err error
	switch {
	case strings.HasPrefix(s, "$"):
		v, err = strconv.ParseUint(s[1:], 16, 64)
	case strings.HasPrefix(s, "%"):
		v, err = strconv.ParseUint(strings.ReplaceAll(s[1:], "_", ""), 2, 64)
	default:
		v, err = strconv.ParseUint(s, 10, 64)
	}
	if err != nil {
		return 0, false
	}
	return uint32(v), true
}

func documentLinksFor(uri, text string) []any {
	tree := parseText(text)
	if tree == nil {
		return nil
	}
	decls := map[string]int{} // name -> 0-based declaration line
	for _, d := range flatDecls(tree.Decls) {
		switch d := d.(type) {
		case *ast.FuncDecl:
			decls[d.Name.Name] = d.Name.Pos().Line - 1
		case *ast.DataDecl:
			decls[d.Name.Name] = d.Name.Pos().Line - 1
		}
	}
	if len(decls) == 0 {
		return nil
	}
	file := source.NewFile("", []byte(text))
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)
	var out []any
	for i, t := range toks {
		if t.Kind != token.Ident {
			continue
		}
		if i > 0 && (toks[i-1].Kind == token.Dot || toks[i-1].Kind == token.Func || toks[i-1].Kind == token.Data) {
			continue
		}
		line, ok := decls[t.Text]
		if !ok {
			continue
		}
		isCall := i+1 < len(toks) && toks[i+1].Kind == token.LParen
		isIndex := i+1 < len(toks) && toks[i+1].Kind == token.LBracket
		if !isCall && !isIndex {
			continue
		}
		start := offsetToLSP(text, t.Pos.Offset)
		end := lspPosition{Line: start.Line, Character: start.Character + utf16Len(t.Text)}
		out = append(out, map[string]any{
			"range":  lspRange{Start: start, End: end},
			"target": fmt.Sprintf("%s#L%d", uri, line+1),
		})
	}
	return out
}

func documentSymbolsFor(text string) []documentSymbol {
	tree := parseText(text)
	if tree == nil {
		return nil
	}
	var out []documentSymbol
	for _, d := range flatDecls(tree.Decls) {
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
			if st.Init != nil {
				out = append(out, localSymbols(text, []ast.Stmt{st.Init})...)
			}
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
		case *ast.RangeStmt:
			if st.Body != nil {
				out = append(out, localSymbols(text, st.Body.List)...)
			}
		case *ast.SwitchStmt:
			if st.Init != nil {
				out = append(out, localSymbols(text, []ast.Stmt{st.Init})...)
			}
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

// documentHighlight highlights every occurrence of the identifier under the
// cursor in the current document.
func (s *Server) documentHighlight(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
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
	var out []any
	for _, r := range identifierRanges(text, word) {
		out = append(out, map[string]any{"range": r, "kind": 1}) // Text
	}
	reply(w, id, out)
}

// selectionRange expands the selection from the identifier under the cursor out
// to the line, enclosing braces and the whole document.
func (s *Server) selectionRange(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Positions []lspPosition `json:"positions"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		reply(w, id, []any{})
		return
	}
	text := s.docs[p.TextDocument.URI]
	out := make([]any, 0, len(p.Positions))
	for _, pos := range p.Positions {
		out = append(out, selectionRangeFor(text, pos))
	}
	reply(w, id, out)
}

func selectionRangeFor(text string, pos lspPosition) any {
	off := posToOffset(text, pos)
	if off > len(text) {
		off = len(text)
	}
	var ranges []lspRange

	// Identifier under the cursor.
	start, end := off, off
	for start > 0 && isWordByte(text[start-1]) {
		start--
	}
	for end < len(text) && isWordByte(text[end]) {
		end++
	}
	if end > start {
		ranges = append(ranges, lspRange{offsetToLSP(text, start), offsetToLSP(text, end)})
	}

	// Whole line.
	lineStart := strings.LastIndexByte(text[:off], '\n') + 1
	lineEnd := len(text)
	if i := strings.IndexByte(text[off:], '\n'); i >= 0 {
		lineEnd = off + i
	}
	if lineEnd > lineStart {
		ranges = append(ranges, lspRange{offsetToLSP(text, lineStart), offsetToLSP(text, lineEnd)})
	}

	// Enclosing brace pairs, innermost first.
	ranges = append(ranges, enclosingBraceRanges(text, off)...)

	// Whole document.
	ranges = append(ranges, lspRange{offsetToLSP(text, 0), offsetToLSP(text, len(text))})

	// Build the parent chain: ranges[0] is the innermost, its parent the next.
	var node any
	for i := len(ranges) - 1; i >= 0; i-- {
		n := map[string]any{"range": ranges[i]}
		if node != nil {
			n["parent"] = node
		}
		node = n
	}
	return node
}

// enclosingBraceRanges returns the `{...}` spans containing off, innermost
// first.
func enclosingBraceRanges(text string, off int) []lspRange {
	var stack []int
	var out []lspRange
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '{':
			stack = append(stack, i)
		case '}':
			if len(stack) == 0 {
				continue
			}
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if open <= off && off <= i {
				out = append(out, lspRange{offsetToLSP(text, open), offsetToLSP(text, i+1)})
			}
		}
	}
	return out
}

// workspaceSymbol searches the symbols of every open document.
func (s *Server) workspaceSymbol(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var p struct {
		Query string `json:"query"`
	}
	_ = json.Unmarshal(params, &p)
	query := strings.ToLower(p.Query)
	var out []any
	seen := map[string]bool{}
	collect := func(uri, text string) {
		if seen[uri] {
			return
		}
		seen[uri] = true
		for _, sym := range documentSymbolsFor(text) {
			if query != "" && !strings.Contains(strings.ToLower(sym.Name), query) {
				continue
			}
			out = append(out, map[string]any{
				"name": sym.Name,
				"kind": sym.Kind,
				"location": map[string]any{
					"uri":   uri,
					"range": sym.SelectionRange,
				},
			})
		}
	}
	for uri, text := range s.docs {
		if strings.HasSuffix(uri, ".icg") {
			collect(uri, text)
		}
	}
	if s.root != "" {
		_ = filepath.WalkDir(s.root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".icg") {
				return nil
			}
			if strings.Contains(p, string(filepath.Separator)+".") {
				return nil
			}
			data, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			collect("file://"+filepath.ToSlash(p), string(data))
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].(map[string]any)["name"].(string) < out[j].(map[string]any)["name"].(string)
	})
	reply(w, id, out)
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

// prepareRename validates that the position names a renamable identifier and
// returns its range plus placeholder.
func (s *Server) prepareRename(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var p textDocumentParams
	if err := json.Unmarshal(params, &p); err != nil {
		reply(w, id, nil)
		return
	}
	text := s.docs[p.TextDocument.URI]
	word := wordAt(text, p.Position)
	if !validIdentifier(word) || len(identifierRanges(text, word)) == 0 {
		reply(w, id, nil)
		return
	}
	reply(w, id, map[string]any{
		"range":       wordRangeAt(text, p.Position),
		"placeholder": word,
	})
}

// wordRangeAt returns the range of the word under pos.
func wordRangeAt(text string, pos lspPosition) lspRange {
	off := posToOffset(text, pos)
	start := off
	for start > 0 && isWordByte(text[start-1]) {
		start--
	}
	end := off
	for end < len(text) && isWordByte(text[end]) {
		end++
	}
	return lspRange{Start: offsetToLSP(text, start), End: offsetToLSP(text, end)}
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

// callContext locates the innermost call enclosing off. It returns the callee
// name (qualified as "batch.writeName" / "sorter.x" / "printer.x" when the call
// is a method), the 0-based argument index and the byte offset where that
// argument starts.
func callContext(text string, off int) (string, int, int, bool) {
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
		return "", 0, 0, false
	}
	name := toks[open-1].Text
	if open >= 3 && toks[open-2].Kind == token.Dot && toks[open-3].Kind == token.Ident {
		name = toks[open-3].Text + "." + name
	}

	// Count commas between '(' and the cursor at depth 0 to find the argument.
	argIndex := 0
	argStart := toks[open].Pos.Offset + 1
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
				argIndex++
				argStart = toks[i].Pos.Offset + 1
			}
		}
	}
	return name, argIndex, argStart, true
}

func signatureHelpFor(text string, pos lspPosition) any {
	off := posToOffset(text, pos)
	name, active, _, ok := callContext(text, off)
	if !ok || strings.Contains(name, ".") {
		return nil
	}
	label, params := signatureFor(text, off, name)
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

func signatureFor(text string, off int, name string) (string, []any) {
	if tree := parseText(text); tree != nil {
		for _, d := range declsAtOffset(tree, off) {
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
		"title":       title,
		"kind":        "quickfix",
		"isPreferred": true,
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
	reply(w, id, map[string]any{"data": encodeSemanticTokens(semanticTokensFor(s.docs[p.TextDocument.URI]))})
}

func (s *Server) semanticTokensRange(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Range lspRange `json:"range"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		reply(w, id, map[string]any{"data": []int{}})
		return
	}
	var filtered []semanticToken
	for _, t := range semanticTokensFor(s.docs[p.TextDocument.URI]) {
		if tokenInRange(t, p.Range) {
			filtered = append(filtered, t)
		}
	}
	reply(w, id, map[string]any{"data": encodeSemanticTokens(filtered)})
}

type semanticToken struct {
	line, char, length, typ int
}

// encodeSemanticTokens turns absolute tokens into the LSP delta encoding.
func encodeSemanticTokens(toks []semanticToken) []int {
	var data []int
	prevLine, prevChar := 0, 0
	for _, t := range toks {
		if t.length <= 0 {
			continue
		}
		dl := t.line - prevLine
		dc := t.char
		if dl == 0 {
			dc = t.char - prevChar
		}
		data = append(data, dl, dc, t.length, t.typ, 0)
		prevLine, prevChar = t.line, t.char
	}
	return data
}

func tokenInRange(t semanticToken, r lspRange) bool {
	if t.line < r.Start.Line || t.line > r.End.Line {
		return false
	}
	if t.line == r.Start.Line && t.char < r.Start.Character {
		return false
	}
	if t.line == r.End.Line && t.char > r.End.Character {
		return false
	}
	return true
}

// specialBuiltins are builtins lowered outside builtin.Funcs (handled specially
// by the lowerer); they still deserve the "builtin" semantic token.
var specialBuiltins = map[string]bool{
	"hash": true, "str": true, "raw": true,
	"read": true, "write": true, "readDev": true, "writeDev": true,
	"readById": true, "writeById": true, "readDevSlot": true, "writeDevSlot": true,
	"jump": true, "ireg": true, "setIreg": true,
	"isLoadValid": true, "isStoreValid": true,
}

func isBuiltinName(name string) bool {
	if _, ok := builtin.Funcs[name]; ok {
		return true
	}
	return specialBuiltins[name]
}

// enumReceiverSet is the set of dotted namespaces that resolve to game enum
// members (e.g. SorterInstruction, PrinterInstruction, ConditionOperation).
var enumReceiverSet = func() map[string]bool {
	m := map[string]bool{"LogicType": true}
	for k := range builtin.EnumConstants {
		if i := strings.IndexByte(k, '.'); i > 0 {
			m[k[:i]] = true
		}
	}
	return m
}()

func semanticTokensFor(text string) []semanticToken {
	file := source.NewFile("", []byte(text))
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)
	busNames := busNamesIn(text)
	var out []semanticToken
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
			switch {
			case isBuiltinName(t.Text):
				typ = semanticTokenIndex["builtin"]
			case isBareEnum(t.Text):
				typ = semanticTokenIndex["enumMember"]
			case isRawConst(t.Text):
				typ = semanticTokenIndex["number"]
			case isBatchMode(t.Text):
				typ = semanticTokenIndex["enumMember"]
			case t.Text == "batch" || t.Text == "sorter" || t.Text == "printer":
				typ = semanticTokenIndex["namespace"]
			case busNames[t.Text]:
				typ = semanticTokenIndex["namespace"]
			case i > 0 && (toks[i-1].Kind == token.Chip || toks[i-1].Kind == token.Bus || toks[i-1].Kind == token.Use):
				typ = semanticTokenIndex["namespace"]
			case builtin.LogicTypes[t.Text]:
				typ = semanticTokenIndex["logicType"]
			case builtin.SlotTypes[t.Text]:
				typ = semanticTokenIndex["logicType"]
			case i+1 < len(toks) && toks[i+1].Kind == token.Dot && enumReceiverSet[t.Text]:
				typ = semanticTokenIndex["enum"]
			case i > 0 && toks[i-1].Kind == token.Dot:
				if i >= 2 && toks[i-2].Kind == token.Ident && enumReceiverSet[toks[i-2].Text] {
					typ = semanticTokenIndex["enumMember"]
				} else {
					typ = semanticTokenIndex["property"]
				}
			case i+1 < len(toks) && toks[i+1].Kind == token.LParen:
				typ = semanticTokenIndex["function"]
			default:
				typ = semanticTokenIndex["variable"]
			}
		case t.Kind >= token.Const && t.Kind <= token.Use:
			typ = semanticTokenIndex["keyword"]
		default:
			typ = semanticTokenIndex["operator"]
		}
		if length > 0 {
			out = append(out, semanticToken{line: pos.Line, char: pos.Character, length: length, typ: typ})
		}
	}
	return out
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
	var hints []any

	// Variable/parameter type hints from the sema type checker.
	if tree := parseText(text); tree != nil {
		info := sema.Check(tree, &diag.Bag{})
		for ident, t := range info.DeclTypes {
			if t == sema.Any {
				continue
			}
			hints = append(hints, map[string]any{
				"position": offsetToLSP(text, ident.Pos().Offset+len(ident.Name)),
				"label":    ": " + t.String(),
				"kind":     2, // Type
			})
		}
	}

	// Budget hint at the end of the file.
	code, diags, err := ic10.Compile(p.TextDocument.URI, []byte(text))
	if err == nil && !diags.HasErrors() {
		st := ic10.StatsOf(code)
		label := fmt.Sprintf("  IC10: %d/%d 行 · %d/%d 字节 · %d/%d 寄存器", st.Lines, codegen.MaxLines, st.Bytes, codegen.MaxBytes, st.RegsUsed, 16)
		if base, size, _, _ := ic10.DataStats(p.TextDocument.URI, []byte(text), ic10.Options{}); base >= 0 {
			label += fmt.Sprintf(" · data %d..%d", base, base+size-1)
		}
		if depth, unbounded, derr := ic10.MaxStackDepth(p.TextDocument.URI, []byte(text), ic10.Options{}); derr == nil {
			switch {
			case unbounded:
				label += " · 栈 无界"
			case depth > 0:
				label += fmt.Sprintf(" · 栈 %d", depth)
			}
		}
		lastLine := strings.Count(text, "\n")
		lastStart := strings.LastIndexByte(text, '\n') + 1
		hints = append(hints, map[string]any{
			"position": lspPosition{Line: lastLine, Character: utf16Len(text[lastStart:])},
			"label":    label,
			"kind":     1,
		})
	}

	sort.SliceStable(hints, func(i, j int) bool {
		a := hints[i].(map[string]any)["position"].(lspPosition)
		b := hints[j].(map[string]any)["position"].(lspPosition)
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Character < b.Character
	})
	reply(w, id, hints)
}

// colorNames maps the IC10 `Color` enum members to their RGB values (see
// docs/target-ic10.md §5.5).
var colorNames = []struct {
	name    string
	r, g, b float64
}{
	{"Blue", 0x21, 0x2A, 0xA5},
	{"Gray", 0x7B, 0x7B, 0x7B},
	{"Green", 0x3F, 0x9B, 0x39},
	{"Orange", 0xFF, 0x66, 0x2B},
	{"Red", 0xE7, 0x02, 0x00},
	{"Yellow", 0xFF, 0xBC, 0x1B},
	{"White", 0xE7, 0xE7, 0xE7},
	{"Black", 0x08, 0x09, 0x08},
	{"Brown", 0x63, 0x3C, 0x2B},
	{"Khaki", 0x63, 0x63, 0x3F},
	{"Pink", 0xE4, 0x1C, 0x99},
	{"Purple", 0x73, 0x2C, 0xA7},
}

func colorRGB(name string) ([3]float64, bool) {
	for _, c := range colorNames {
		if c.name == name {
			return [3]float64{c.r / 255, c.g / 255, c.b / 255}, true
		}
	}
	return [3]float64{}, false
}

// documentColor reports the `Color.<Name>` enum members as color swatches.
func (s *Server) documentColor(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var p textDocumentOnlyParams
	if err := json.Unmarshal(params, &p); err != nil {
		reply(w, id, []any{})
		return
	}
	text := s.docs[p.TextDocument.URI]
	file := source.NewFile("", []byte(text))
	diags := &diag.Bag{}
	toks := lexer.Tokenize(file, diags)
	var out []any
	for i := 0; i+2 < len(toks); i++ {
		if toks[i].Kind != token.Ident || toks[i].Text != "Color" ||
			toks[i+1].Kind != token.Dot || toks[i+2].Kind != token.Ident {
			continue
		}
		rgb, ok := colorRGB(toks[i+2].Text)
		if !ok {
			continue
		}
		out = append(out, map[string]any{
			"range": lspRange{
				Start: offsetToLSP(text, toks[i].Pos.Offset),
				End:   offsetToLSP(text, toks[i+2].Pos.Offset+len(toks[i+2].Text)),
			},
			"color": map[string]any{"red": rgb[0], "green": rgb[1], "blue": rgb[2], "alpha": 1.0},
		})
	}
	reply(w, id, out)
}

// colorPresentation maps a picked colour back to the closest Color.<Name>.
func (s *Server) colorPresentation(w *bufio.Writer, id json.RawMessage, params json.RawMessage) {
	var p struct {
		Color struct {
			Red   float64 `json:"red"`
			Green float64 `json:"green"`
			Blue  float64 `json:"blue"`
		} `json:"color"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		reply(w, id, []any{})
		return
	}
	best := ""
	bestDist := math.MaxFloat64
	for _, c := range colorNames {
		r, g, b := c.r/255, c.g/255, c.b/255
		d := (r-p.Color.Red)*(r-p.Color.Red) + (g-p.Color.Green)*(g-p.Color.Green) + (b-p.Color.Blue)*(b-p.Color.Blue)
		if d < bestDist {
			bestDist = d
			best = c.name
		}
	}
	if best == "" {
		reply(w, id, []any{})
		return
	}
	reply(w, id, []any{map[string]any{"label": "Color." + best}})
}

// publishStats notifies the client of the compiled program's budget so it can
// show a persistent status indicator.
func (s *Server) publishStats(w *bufio.Writer, uri, text string, compiled ic10.Result, err error, diags *diag.Bag) {
	if err != nil || diags.HasErrors() {
		notify(w, "icg/stats", map[string]any{"uri": uri, "error": true})
		return
	}
	st := ic10.StatsOf(compiled.Code)
	multi := len(compiled.Chips) > 1
	if multi {
		// Report the tightest budget across the chips.
		for _, ch := range compiled.Chips {
			cs := ic10.StatsOf(ch.Code)
			if cs.Lines > st.Lines {
				st.Lines = cs.Lines
			}
			if cs.Bytes > st.Bytes {
				st.Bytes = cs.Bytes
			}
			if cs.MaxLineLen > st.MaxLineLen {
				st.MaxLineLen = cs.MaxLineLen
			}
			if cs.RegsUsed > st.RegsUsed {
				st.RegsUsed = cs.RegsUsed
			}
		}
	}
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
	if multi {
		payload["chips"] = len(compiled.Chips)
	}
	if !multi {
		if rep, err := ic10.Size(uri, []byte(text), ic10.Options{}); err == nil {
			stack := rep.Stack
			payload["stackUser"] = stack.UserUsed
			payload["stackUserLimit"] = stack.UserLimit
			payload["stackUserMax"] = stack.UserMax
			payload["stackDynamic"] = stack.Dynamic
			payload["stackPush"] = stack.UserPush
			payload["stackManual"] = stack.UserManual
			payload["stackUnbounded"] = stack.UserUnbounded
			payload["stackCompiler"] = stack.CompilerUsed
			payload["stackCompilerBase"] = stack.CompilerBase
			payload["stackData"] = stack.DataSlots
			payload["stackSpills"] = stack.SpillSlots
		}
		if base, size, autoTabled, warn := ic10.DataStats(uri, []byte(text), ic10.Options{}); base >= 0 {
			payload["dataBase"] = base
			payload["dataSize"] = size
			payload["dataEnd"] = base + size - 1
			payload["autoTabled"] = autoTabled
			if warn != "" {
				payload["dataWarn"] = warn
			}
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
