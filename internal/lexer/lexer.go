package lexer

import (
	"unicode/utf8"

	"ic10go/internal/diag"
	"ic10go/internal/source"
	"ic10go/internal/token"
)

type Lexer struct {
	file     *source.File
	src      []byte
	off      int
	diags    *diag.Bag
	comments []source.Comment
}

func New(file *source.File, diags *diag.Bag) *Lexer {
	l := &Lexer{file: file, src: file.Src, diags: diags}
	// Skip a UTF-8 byte order mark at the start of the file.
	if len(l.src) >= 3 && l.src[0] == 0xEF && l.src[1] == 0xBB && l.src[2] == 0xBF {
		l.off = 3
	}
	return l
}

// Tokenize scans the whole file and returns tokens with semicolons inserted.
func Tokenize(file *source.File, diags *diag.Bag) []token.Token {
	toks, _ := TokenizeWithComments(file, diags)
	return toks
}

// TokenizeWithComments also returns the comments found in the file.
func TokenizeWithComments(file *source.File, diags *diag.Bag) ([]token.Token, []source.Comment) {
	l := New(file, diags)
	toks := l.run()
	return toks, l.comments
}

func (l *Lexer) pos() source.Pos { return l.file.PosAt(l.off) }

func (l *Lexer) peek() byte {
	if l.off < len(l.src) {
		return l.src[l.off]
	}
	return 0
}

func (l *Lexer) peekAt(n int) byte {
	if l.off+n < len(l.src) {
		return l.src[l.off+n]
	}
	return 0
}

func (l *Lexer) advance() byte {
	b := l.src[l.off]
	l.off++
	return b
}

func (l *Lexer) run() []token.Token {
	var toks []token.Token
	insertSemi := false
	for l.off < len(l.src) {
		c := l.peek()
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			l.off++
		case c == '\n':
			if insertSemi {
				toks = append(toks, token.Token{Kind: token.Semicolon, Text: "\n", Pos: l.pos()})
				insertSemi = false
			}
			l.off++
		case c == '/' && l.peekAt(1) == '/':
			l.recordLineComment()
		case c == '/' && l.peekAt(1) == '*':
			l.recordBlockComment()
		default:
			tok := l.scan()
			toks = append(toks, tok)
			insertSemi = tok.Kind.EndsStatement()
		}
	}
	if insertSemi {
		toks = append(toks, token.Token{Kind: token.Semicolon, Text: "\n", Pos: l.pos()})
	}
	toks = append(toks, token.Token{Kind: token.EOF, Pos: l.pos()})
	return toks
}

func (l *Lexer) recordLineComment() {
	start := l.off
	line := l.pos().Line
	for l.off < len(l.src) && l.src[l.off] != '\n' {
		l.off++
	}
	trailing := false
	for j := start - 1; j >= 0 && l.src[j] != '\n'; j-- {
		if c := l.src[j]; c != ' ' && c != '\t' && c != '\r' {
			trailing = true
			break
		}
	}
	l.comments = append(l.comments, source.Comment{
		Line:     line,
		Text:     string(l.src[start:l.off]),
		Trailing: trailing,
	})
}

func (l *Lexer) recordBlockComment() {
	start := l.off
	pos := l.pos()
	l.off += 2
	closed := false
	for l.off < len(l.src) {
		if l.peek() == '*' && l.peekAt(1) == '/' {
			l.off += 2
			closed = true
			break
		}
		l.off++
	}
	if !closed {
		l.diags.Errorf(pos, "unterminated block comment")
	}
	l.comments = append(l.comments, source.Comment{
		Line: pos.Line,
		Text: string(l.src[start:l.off]),
	})
}

func (l *Lexer) scan() token.Token {
	start := l.pos()
	c := l.peek()
	switch {
	case isLetter(c) || c == '_':
		return l.scanIdent(start)
	case isDigit(c):
		return l.scanNumber(start)
	case c == '"':
		return l.scanString(start)
	}

	// Operators and punctuation.
	mk := func(k token.Kind, text string, n int) token.Token {
		l.off += n
		return token.Token{Kind: k, Text: text, Pos: start}
	}
	switch c {
	case '+':
		switch l.peekAt(1) {
		case '+':
			return mk(token.PlusPlus, "++", 2)
		case '=':
			return mk(token.PlusAssign, "+=", 2)
		}
		return mk(token.Plus, "+", 1)
	case '-':
		switch l.peekAt(1) {
		case '-':
			return mk(token.MinusMinus, "--", 2)
		case '=':
			return mk(token.MinusAssign, "-=", 2)
		}
		return mk(token.Minus, "-", 1)
	case '*':
		if l.peekAt(1) == '=' {
			return mk(token.StarAssign, "*=", 2)
		}
		return mk(token.Star, "*", 1)
	case '/':
		if l.peekAt(1) == '=' {
			return mk(token.SlashAssign, "/=", 2)
		}
		return mk(token.Slash, "/", 1)
	case '%':
		if l.peekAt(1) == '=' {
			return mk(token.PercentAssign, "%=", 2)
		}
		return mk(token.Percent, "%", 1)
	case '&':
		switch l.peekAt(1) {
		case '&':
			return mk(token.And, "&&", 2)
		case '=':
			return mk(token.AmpAssign, "&=", 2)
		}
		return mk(token.Amp, "&", 1)
	case '|':
		switch l.peekAt(1) {
		case '|':
			return mk(token.Or, "||", 2)
		case '=':
			return mk(token.PipeAssign, "|=", 2)
		}
		return mk(token.Pipe, "|", 1)
	case '^':
		if l.peekAt(1) == '=' {
			return mk(token.CaretAssign, "^=", 2)
		}
		return mk(token.Caret, "^", 1)
	case '~':
		return mk(token.Tilde, "~", 1)
	case '!':
		if l.peekAt(1) == '=' {
			return mk(token.Ne, "!=", 2)
		}
		return mk(token.Not, "!", 1)
	case '<':
		switch l.peekAt(1) {
		case '<':
			if l.peekAt(2) == '=' {
				return mk(token.ShlAssign, "<<=", 3)
			}
			return mk(token.Shl, "<<", 2)
		case '=':
			return mk(token.Le, "<=", 2)
		}
		return mk(token.Lt, "<", 1)
	case '>':
		switch l.peekAt(1) {
		case '>':
			if l.peekAt(2) == '=' {
				return mk(token.ShrAssign, ">>=", 3)
			}
			return mk(token.Shr, ">>", 2)
		case '=':
			return mk(token.Ge, ">=", 2)
		}
		return mk(token.Gt, ">", 1)
	case '=':
		if l.peekAt(1) == '=' {
			return mk(token.Eq, "==", 2)
		}
		return mk(token.Assign, "=", 1)
	case ':':
		if l.peekAt(1) == '=' {
			return mk(token.Define, ":=", 2)
		}
		return mk(token.Colon, ":", 1)
	case '?':
		return mk(token.Question, "?", 1)
	case '(':
		return mk(token.LParen, "(", 1)
	case ')':
		return mk(token.RParen, ")", 1)
	case '{':
		return mk(token.LBrace, "{", 1)
	case '}':
		return mk(token.RBrace, "}", 1)
	case '[':
		return mk(token.LBracket, "[", 1)
	case ']':
		return mk(token.RBracket, "]", 1)
	case ',':
		return mk(token.Comma, ",", 1)
	case '.':
		if l.peekAt(1) == '.' {
			return mk(token.DotDot, "..", 2)
		}
		return mk(token.Dot, ".", 1)
	case ';':
		return mk(token.Semicolon, ";", 1)
	}

	r, _ := utf8.DecodeRune(l.src[l.off:])
	l.diags.Errorf(start, "unexpected character %q", r)
	l.off++
	return token.Token{Kind: token.Ident, Text: string(r), Pos: start}
}

func (l *Lexer) scanIdent(start source.Pos) token.Token {
	for l.off < len(l.src) && (isLetter(l.peek()) || isDigit(l.peek()) || l.peek() == '_') {
		l.off++
	}
	text := string(l.src[start.Offset:l.off])
	if isDevice(text) {
		return token.Token{Kind: token.Device, Text: text, Pos: start}
	}
	return token.Token{Kind: token.Lookup(text), Text: text, Pos: start}
}

func (l *Lexer) scanNumber(start source.Pos) token.Token {
	if l.peek() == '0' && (l.peekAt(1) == 'x' || l.peekAt(1) == 'X') {
		l.off += 2
		for isHexDigit(l.peek()) || l.peek() == '_' {
			l.off++
		}
	} else if l.peek() == '0' && (l.peekAt(1) == 'b' || l.peekAt(1) == 'B') {
		l.off += 2
		for l.peek() == '0' || l.peek() == '1' || l.peek() == '_' {
			l.off++
		}
	} else {
		for isDigit(l.peek()) || l.peek() == '_' {
			l.off++
		}
		if l.peek() == '.' && isDigit(l.peekAt(1)) {
			l.off++
			for isDigit(l.peek()) || l.peek() == '_' {
				l.off++
			}
		}
		if l.peek() == 'e' || l.peek() == 'E' {
			n := 1
			if l.peekAt(1) == '+' || l.peekAt(1) == '-' {
				n = 2
			}
			if isDigit(l.peekAt(n)) {
				l.off += n
				for isDigit(l.peek()) {
					l.off++
				}
			}
		}
		// Optional unit suffix on a decimal literal: 20c, 20.1MPa, 101.3kPa.
		if n := l.unitSuffix(); n > 0 {
			l.off += n
		}
	}
	text := string(l.src[start.Offset:l.off])
	return token.Token{Kind: token.Number, Text: text, Pos: start}
}

// unitSuffix returns the length of a recognized unit suffix at the current
// position, or 0. The suffix must be followed by a non-identifier byte so that
// e.g. "20count" is not read as "20c" + "ount".
func (l *Lexer) unitSuffix() int {
	for _, u := range token.Units {
		n := len(u.Name)
		if l.off+n > len(l.src) || string(l.src[l.off:l.off+n]) != u.Name {
			continue
		}
		if c := l.peekAt(n); isLetter(c) || isDigit(c) || c == '_' {
			continue
		}
		return n
	}
	return 0
}

func (l *Lexer) scanString(start source.Pos) token.Token {
	l.off++ // opening quote
	var buf []byte
	for {
		if l.off >= len(l.src) || l.peek() == '\n' {
			l.diags.Errorf(start, "unterminated string literal")
			break
		}
		c := l.peek()
		if c == '"' {
			l.off++
			break
		}
		if c == '\\' {
			l.off++
			if l.off >= len(l.src) {
				l.diags.Errorf(start, "unterminated string literal")
				break
			}
			switch l.advance() {
			case 'n':
				buf = append(buf, '\n')
			case 't':
				buf = append(buf, '\t')
			case 'r':
				buf = append(buf, '\r')
			case '\\':
				buf = append(buf, '\\')
			case '"':
				buf = append(buf, '"')
			case '0':
				buf = append(buf, 0)
			default:
				buf = append(buf, l.src[l.off-1])
			}
			continue
		}
		buf = append(buf, c)
		l.off++
	}
	return token.Token{Kind: token.String, Text: string(buf), Pos: start}
}

func isDevice(s string) bool {
	if s == "db" {
		return true
	}
	if len(s) == 2 && s[0] == 'd' && s[1] >= '0' && s[1] <= '5' {
		return true
	}
	return false
}

func isLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHexDigit(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
