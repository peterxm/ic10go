// Package ir defines the intermediate representation used by the compiler.
//
// The IR is a non-SSA three-address code organised into basic blocks. Virtual
// registers are unlimited; register allocation maps them onto the sixteen IC10
// CPU registers.
package ir

import (
	"fmt"
	"math"
	"strconv"
)

// ---------------------------------------------------------------------------
// Values
// ---------------------------------------------------------------------------

type Value interface {
	isValue()
	String() string
}

// Const is a compile-time numeric constant.
type Const struct {
	V float64
	// Special is set for nan, pinf or ninf; V is ignored then.
	Special string
	// Raw is emitted verbatim when set (e.g. STR("text")).
	Raw string
}

func (*Const) isValue() {}

func (c *Const) String() string {
	if c.Raw != "" {
		return c.Raw
	}
	if c.Special != "" {
		return c.Special
	}
	return formatFloat(c.V)
}

// Reg is a virtual register.
type Reg struct {
	ID   int
	Name string
}

func (*Reg) isValue() {}

func (r *Reg) String() string {
	if r.Name != "" {
		return fmt.Sprintf("v%d(%s)", r.ID, r.Name)
	}
	return fmt.Sprintf("v%d", r.ID)
}

// Device is a device port operand (d0..d5 or db).
type Device struct{ Name string }

func (*Device) isValue() {}

func (d *Device) String() string { return d.Name }

func formatFloat(v float64) string {
	switch {
	case math.IsNaN(v):
		return "nan"
	case math.IsInf(v, 1):
		return "pinf"
	case math.IsInf(v, -1):
		return "ninf"
	}
	const maxExact = 1 << 53
	if v == math.Trunc(v) && v >= -maxExact && v <= maxExact {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// ---------------------------------------------------------------------------
// Operators
// ---------------------------------------------------------------------------

type BinOp int

const (
	Add BinOp = iota
	Sub
	Mul
	Div
	Mod
	BitAnd
	BitOr
	BitXor
	Shl
	Shr
	Min
	Max
)

var binOpNames = map[BinOp]string{
	Add: "add", Sub: "sub", Mul: "mul", Div: "div", Mod: "mod",
	BitAnd: "and", BitOr: "or", BitXor: "xor", Shl: "sll", Shr: "sra",
	Min: "min", Max: "max",
}

func (o BinOp) String() string { return binOpNames[o] }

// IC10 returns the IC10 mnemonic for the operator.
func (o BinOp) IC10() string { return binOpNames[o] }

type UnOp int

const (
	Neg UnOp = iota
	BitNot
	Seqz
)

var unOpNames = map[UnOp]string{Neg: "neg", BitNot: "not", Seqz: "seqz"}

func (o UnOp) String() string { return unOpNames[o] }

// ---------------------------------------------------------------------------
// Instructions
// ---------------------------------------------------------------------------

type Instr interface{ isInstr() }

type Assign struct {
	Dst *Reg
	Src Value
}

type Bin struct {
	Op   BinOp
	Dst  *Reg
	A, B Value
}

type Un struct {
	Op  UnOp
	Dst *Reg
	A   Value
}

// Cmp materialises a comparison result (0 or 1) into Dst.
type Cmp struct {
	Cond Cond
	Dst  *Reg
	A, B Value // B is nil for unary conditions
}

// Select chooses Then when Cond is non-zero, otherwise Else.
type Select struct {
	Dst              *Reg
	Cond, Then, Else Value
}

type Load struct {
	Dst   *Reg
	Dev   string
	Logic string
}

type Store struct {
	Dev   string
	Logic string
	Src   Value
}

type LoadSlot struct {
	Dst   *Reg
	Dev   string
	Index Value
	Logic string
}

type StoreSlot struct {
	Dev   string
	Index Value
	Logic string
	Src   Value
}

// Builtin is a call to a built-in function, e.g. yield, sleep or sqrt.
type Builtin struct {
	Dst  *Reg // may be nil
	Name string
	Args []Value
}

// BatchKind identifies a batched device instruction.
type BatchKind int

const (
	BatchLoad         BatchKind = iota // lb
	BatchLoadName                      // lbn
	BatchLoadSlot                      // lbs
	BatchLoadNameSlot                  // lbns
	BatchStore                         // sb
	BatchStoreName                     // sbn
	BatchStoreSlot                     // sbs
)

// Batch is a batched device read or write.
type Batch struct {
	Kind   BatchKind
	Dst    *Reg   // loads only
	Device Value  // deviceHash
	Name   Value  // nameHash (named variants)
	Slot   Value  // slot index (slot variants)
	Logic  string // logicType or slotType
	Mode   Value  // batch mode (loads)
	Src    Value  // value to store (stores)
}

func (*Assign) isInstr()    {}
func (*Bin) isInstr()       {}
func (*Un) isInstr()        {}
func (*Cmp) isInstr()       {}
func (*Select) isInstr()    {}
func (*Load) isInstr()      {}
func (*Store) isInstr()     {}
func (*LoadSlot) isInstr()  {}
func (*StoreSlot) isInstr() {}
func (*Builtin) isInstr()   {}
func (*Batch) isInstr()     {}

// ---------------------------------------------------------------------------
// Terminators
// ---------------------------------------------------------------------------

type Cond int

const (
	Eq Cond = iota
	Ne
	Lt
	Le
	Gt
	Ge
	NonZero // a != 0
	Zero    // a == 0
)

func (c Cond) String() string {
	switch c {
	case Eq:
		return "eq"
	case Ne:
		return "ne"
	case Lt:
		return "lt"
	case Le:
		return "le"
	case Gt:
		return "gt"
	case Ge:
		return "ge"
	case NonZero:
		return "nz"
	case Zero:
		return "z"
	}
	return "?"
}

// Invert returns the logical negation of the condition.
func (c Cond) Invert() Cond {
	switch c {
	case Eq:
		return Ne
	case Ne:
		return Eq
	case Lt:
		return Ge
	case Ge:
		return Lt
	case Le:
		return Gt
	case Gt:
		return Le
	case NonZero:
		return Zero
	case Zero:
		return NonZero
	}
	return c
}

type Term interface{ isTerm() }

type Jmp struct{ Target *Block }

type Br struct {
	Cond       Cond
	A, B       Value // B is nil for unary conditions
	Then, Else *Block
}

type Ret struct{ Value Value }

func (*Jmp) isTerm() {}
func (*Br) isTerm()  {}
func (*Ret) isTerm() {}

// ---------------------------------------------------------------------------
// Blocks and functions
// ---------------------------------------------------------------------------

type Block struct {
	ID     int
	Instrs []Instr
	Term   Term
	Preds  []*Block
	Succs  []*Block
}

type Function struct {
	Name    string
	Blocks  []*Block
	Entry   *Block
	NumRegs int
}

// BuildCFG recomputes predecessor and successor lists from terminators.
func (f *Function) BuildCFG() {
	for _, b := range f.Blocks {
		b.Preds = nil
		b.Succs = nil
	}
	for _, b := range f.Blocks {
		switch t := b.Term.(type) {
		case *Jmp:
			b.Succs = append(b.Succs, t.Target)
		case *Br:
			b.Succs = append(b.Succs, t.Then, t.Else)
		}
	}
	for _, b := range f.Blocks {
		for _, s := range b.Succs {
			s.Preds = append(s.Preds, b)
		}
	}
}

// ---------------------------------------------------------------------------
// Builder
// ---------------------------------------------------------------------------

type Builder struct {
	fn  *Function
	cur *Block
}

func NewBuilder(name string) *Builder {
	b := &Builder{fn: &Function{Name: name}}
	b.cur = b.fn.NewBlock()
	return b
}

func (b *Builder) Fn() *Function { return b.fn }

func (b *Builder) Cur() *Block { return b.cur }

func (b *Builder) SetBlock(blk *Block) { b.cur = blk }

func (b *Builder) NewBlock() *Block {
	return b.fn.NewBlock()
}

func (b *Builder) NewReg(name string) *Reg {
	return b.fn.NewReg(name)
}

// NewReg allocates a fresh virtual register in the function.
func (f *Function) NewReg(name string) *Reg {
	r := &Reg{ID: f.NumRegs, Name: name}
	f.NumRegs++
	return r
}

func (b *Builder) Const(v float64) *Const { return &Const{V: v} }

func (b *Builder) Emit(i Instr) {
	b.cur.Instrs = append(b.cur.Instrs, i)
}

func (b *Builder) SetTerm(t Term) {
	if b.cur.Term != nil {
		return
	}
	b.cur.Term = t
}

// NewFunctionBlock appends a block to the function.
func (f *Function) NewBlock() *Block {
	b := &Block{ID: len(f.Blocks)}
	f.Blocks = append(f.Blocks, b)
	if f.Entry == nil {
		f.Entry = b
	}
	return b
}
