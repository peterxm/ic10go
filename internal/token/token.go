package token

import "ic10go/internal/source"

type Kind int

const (
	EOF Kind = iota
	Ident
	Number
	String
	Device // d0..d5, db

	// Keywords
	Const
	Data
	Var
	Func
	If
	Else
	For
	Break
	Continue
	Return
	Switch
	Case
	Default
	Label
	Goto
	Call
	Ret
	True
	False
	NaN
	PInf
	NInf

	// Operators and punctuation
	Plus
	Minus
	Star
	Slash
	Percent
	Amp
	Pipe
	Caret
	Tilde
	Shl
	Shr
	Eq
	Ne
	Lt
	Le
	Gt
	Ge
	And
	Or
	Not
	Assign
	Define // :=
	PlusAssign
	MinusAssign
	StarAssign
	SlashAssign
	PercentAssign
	AmpAssign
	PipeAssign
	CaretAssign
	ShlAssign
	ShrAssign
	PlusPlus
	MinusMinus
	Question
	Colon
	LParen
	RParen
	LBrace
	RBrace
	LBracket
	RBracket
	Comma
	Dot
	Semicolon
)

var keywords = map[string]Kind{
	"const":    Const,
	"data":     Data,
	"var":      Var,
	"func":     Func,
	"if":       If,
	"else":     Else,
	"for":      For,
	"break":    Break,
	"continue": Continue,
	"return":   Return,
	"switch":   Switch,
	"case":     Case,
	"default":  Default,
	"label":    Label,
	"goto":     Goto,
	"call":     Call,
	"ret":      Ret,
	"true":     True,
	"false":    False,
	"nan":      NaN,
	"pinf":     PInf,
	"ninf":     NInf,
}

// Lookup maps an identifier to its keyword kind, or Ident.
func Lookup(ident string) Kind {
	if k, ok := keywords[ident]; ok {
		return k
	}
	return Ident
}

type Token struct {
	Kind Kind
	Text string
	Pos  source.Pos
}

var kindNames = map[Kind]string{
	EOF:           "EOF",
	Ident:         "identifier",
	Number:        "number",
	String:        "string",
	Device:        "device",
	Const:         "const",
	Var:           "var",
	Func:          "func",
	If:            "if",
	Else:          "else",
	For:           "for",
	Break:         "break",
	Continue:      "continue",
	Return:        "return",
	Switch:        "switch",
	Case:          "case",
	Default:       "default",
	Label:         "label",
	Goto:          "goto",
	Call:          "call",
	Ret:           "ret",
	True:          "true",
	False:         "false",
	NaN:           "nan",
	PInf:          "pinf",
	NInf:          "ninf",
	Plus:          "+",
	Minus:         "-",
	Star:          "*",
	Slash:         "/",
	Percent:       "%",
	Amp:           "&",
	Pipe:          "|",
	Caret:         "^",
	Tilde:         "~",
	Shl:           "<<",
	Shr:           ">>",
	Eq:            "==",
	Ne:            "!=",
	Lt:            "<",
	Le:            "<=",
	Gt:            ">",
	Ge:            ">=",
	And:           "&&",
	Or:            "||",
	Not:           "!",
	Assign:        "=",
	Define:        ":=",
	PlusAssign:    "+=",
	MinusAssign:   "-=",
	StarAssign:    "*=",
	SlashAssign:   "/=",
	PercentAssign: "%=",
	AmpAssign:     "&=",
	PipeAssign:    "|=",
	CaretAssign:   "^=",
	ShlAssign:     "<<=",
	ShrAssign:     ">>=",
	PlusPlus:      "++",
	MinusMinus:    "--",
	Question:      "?",
	Colon:         ":",
	LParen:        "(",
	RParen:        ")",
	LBrace:        "{",
	RBrace:        "}",
	LBracket:      "[",
	RBracket:      "]",
	Comma:         ",",
	Dot:           ".",
	Semicolon:     ";",
}

func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return "unknown"
}

// EndsStatement reports whether a token of this kind may terminate a statement,
// used for automatic semicolon insertion.
func (k Kind) EndsStatement() bool {
	switch k {
	case Ident, Number, String, Device, True, False, NaN, PInf, NInf,
		RParen, RBracket, RBrace, Return, Break, Continue, PlusPlus, MinusMinus:
		return true
	}
	return false
}
