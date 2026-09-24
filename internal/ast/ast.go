package ast

import (
	"ic10go/internal/source"
	"ic10go/internal/token"
)

// NodeBase is embedded in every node to provide a position.
type NodeBase struct {
	Pos_ source.Pos
}

func (n NodeBase) Pos() source.Pos { return n.Pos_ }

type Node interface {
	Pos() source.Pos
}

type Decl interface {
	Node
	declNode()
}

type Stmt interface {
	Node
	stmtNode()
}

type Expr interface {
	Node
	exprNode()
}

// File is a parsed source file.
type File struct {
	NodeBase
	Decls []Decl
}

// ---------------------------------------------------------------------------
// Declarations
// ---------------------------------------------------------------------------

type Param struct {
	NodeBase
	Name *Ident
	Type string
}

type ConstDecl struct {
	NodeBase
	Name  *Ident
	Value Expr
	Group bool // declared inside a `const ( ... )` block
}

// DataDecl is a top-level `data Name = [ ... ]` table. Its elements are
// compile-time constants stored in the persistent IC10 stack.
type DataDecl struct {
	NodeBase
	Name   *Ident
	Values []Expr
}

type VarDecl struct {
	NodeBase
	Name  *Ident
	Value Expr // may be nil
	Group bool // declared inside a `var ( ... )` block
}

type FuncDecl struct {
	NodeBase
	Name   *Ident
	Params []*Param
	Result string // "" means no result
	Body   *BlockStmt
}

// ChipDecl is a `chip Name { ... }` block: a separate IC10 program with its own
// scope, main function and 128-line budget. Top-level declarations outside any
// chip are shared by every chip.
type ChipDecl struct {
	NodeBase
	Name  *Ident
	Decls []Decl // const / data / var / func
}

// BusDecl is a `bus Name { slot type ... }` block: a named set of network
// channels shared between chips. Slots map to Channel0.. in order. How each
// chip reaches the network is declared with a per-chip UseDecl.
type BusDecl struct {
	NodeBase
	Name  *Ident
	Slots []*BusSlot
}

// BusSlot is one channel of a BusDecl; its position is the channel number.
type BusSlot struct {
	NodeBase
	Name *Ident
	Type string // "num", "bool" or "str"
}

// UseDecl is a `use Bus on dev:conn[, dev:conn ...]` inside a chip: it binds the
// bus's channels to this chip's device connection(s), 8 channels each.
type UseDecl struct {
	NodeBase
	Bus      *Ident
	Bindings []*ConnRef
}

// ImportDecl is `import "path"`: another file's const/data/func declarations
// merged into this compilation unit, resolved before checking.
type ImportDecl struct {
	NodeBase
	Path *StringLit
}

// ConnRef is a `dev:conn` access point: a device port or alias and a connection.
type ConnRef struct {
	NodeBase
	Device string // "db", "d0", ... or a device alias
	Conn   int
}

func (*ConstDecl) declNode()  {}
func (*DataDecl) declNode()   {}
func (*VarDecl) declNode()    {}
func (*FuncDecl) declNode()   {}
func (*ChipDecl) declNode()   {}
func (*BusDecl) declNode()    {}
func (*UseDecl) declNode()    {}
func (*ImportDecl) declNode() {}

// ---------------------------------------------------------------------------
// Statements
// ---------------------------------------------------------------------------

type BlockStmt struct {
	NodeBase
	List []Stmt
}

type ExprStmt struct {
	NodeBase
	X Expr
}

type AssignStmt struct {
	NodeBase
	Lhs Expr
	Op  token.Kind // Assign, Define, or a compound assignment
	Rhs Expr
}

type IncDecStmt struct {
	NodeBase
	X  Expr
	Op token.Kind // PlusPlus or MinusMinus
}

type IfStmt struct {
	NodeBase
	Init Stmt // may be nil
	Cond Expr
	Then *BlockStmt
	Else Stmt // *BlockStmt, *IfStmt, or nil
}

type ForStmt struct {
	NodeBase
	Init Stmt // may be nil
	Cond Expr // may be nil
	Post Stmt // may be nil
	Body *BlockStmt
}

// RangeStmt is `for key [, value] := range X { ... }`. X is an integer count
// (0..X-1) or a `data` table (value binds Table[key]).
type RangeStmt struct {
	NodeBase
	Key   *Ident
	Value *Ident // may be nil
	X     Expr
	Body  *BlockStmt
}

type CaseClause struct {
	NodeBase
	Exprs   []Expr // empty when Default
	Default bool
	Body    []Stmt
}

type SwitchStmt struct {
	NodeBase
	Init  Stmt // may be nil
	Tag   Expr // may be nil
	Table bool // `switch tag table { ... }`: auto-table into the data segment
	Cases []*CaseClause
}

type BreakStmt struct {
	NodeBase
	Label *Ident // may be nil
}

type ContinueStmt struct {
	NodeBase
	Label *Ident // may be nil
}

// DeclStmt wraps a const/var declaration used inside a block.
type DeclStmt struct {
	NodeBase
	Decl Decl
}

// LabelStmt marks a jump target.
type LabelStmt struct {
	NodeBase
	Name *Ident
}

// GotoStmt jumps to a label.
type GotoStmt struct {
	NodeBase
	Name *Ident
}

// CallStmt sets the return address and jumps to a label.
type CallStmt struct {
	NodeBase
	Name *Ident
}

// RetStmt jumps to the return address register.
type RetStmt struct{ NodeBase }

type ReturnStmt struct {
	NodeBase
	Result Expr // may be nil
}

func (*BlockStmt) stmtNode()    {}
func (*ExprStmt) stmtNode()     {}
func (*AssignStmt) stmtNode()   {}
func (*IncDecStmt) stmtNode()   {}
func (*IfStmt) stmtNode()       {}
func (*ForStmt) stmtNode()      {}
func (*RangeStmt) stmtNode()    {}
func (*SwitchStmt) stmtNode()   {}
func (*BreakStmt) stmtNode()    {}
func (*ContinueStmt) stmtNode() {}
func (*ReturnStmt) stmtNode()   {}
func (*DeclStmt) stmtNode()     {}
func (*LabelStmt) stmtNode()    {}
func (*GotoStmt) stmtNode()     {}
func (*CallStmt) stmtNode()     {}
func (*RetStmt) stmtNode()      {}

// ---------------------------------------------------------------------------
// Expressions
// ---------------------------------------------------------------------------

type Ident struct {
	NodeBase
	Name string
}

type NumberLit struct {
	NodeBase
	Value float64
	Text  string
}

type StringLit struct {
	NodeBase
	Value string
}

type BoolLit struct {
	NodeBase
	Value bool
}

// DeviceLit is a device port: d0..d5 or db.
type DeviceLit struct {
	NodeBase
	Name string
}

// SpecialLit is nan, pinf or ninf.
type SpecialLit struct {
	NodeBase
	Name string
}

type UnaryExpr struct {
	NodeBase
	Op token.Kind
	X  Expr
}

type BinaryExpr struct {
	NodeBase
	Op   token.Kind
	X, Y Expr
}

type ParenExpr struct {
	NodeBase
	X Expr
}

type CallExpr struct {
	NodeBase
	Fun  Expr
	Args []Expr
}

type SelectorExpr struct {
	NodeBase
	X   Expr
	Sel *Ident
}

type IndexExpr struct {
	NodeBase
	X     Expr
	Index Expr
}

type TernaryExpr struct {
	NodeBase
	Cond Expr
	Then Expr
	Else Expr
}

// RangeExpr is a `lo..hi` inclusive interval, only valid as a switch case value.
type RangeExpr struct {
	NodeBase
	Lo Expr
	Hi Expr
}

func (*Ident) exprNode()        {}
func (*NumberLit) exprNode()    {}
func (*StringLit) exprNode()    {}
func (*BoolLit) exprNode()      {}
func (*DeviceLit) exprNode()    {}
func (*SpecialLit) exprNode()   {}
func (*UnaryExpr) exprNode()    {}
func (*BinaryExpr) exprNode()   {}
func (*ParenExpr) exprNode()    {}
func (*CallExpr) exprNode()     {}
func (*SelectorExpr) exprNode() {}
func (*IndexExpr) exprNode()    {}
func (*TernaryExpr) exprNode()  {}
func (*RangeExpr) exprNode()    {}
