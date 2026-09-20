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

	"ic10go/internal/source"
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

// labelRefMark delimits a label-address placeholder. A label used as a value
// (IC10 lets a branch operand name a label, which the assembler turns into its
// line number) is lowered to a Const whose Raw text is such a placeholder;
// codegen replaces it with the label block's absolute line once the layout is
// known.
const labelRefMark = "\x01"

// LabelRef returns the placeholder for the absolute address of a label block.
func LabelRef(blockID int) string {
	return labelRefMark + "L" + strconv.Itoa(blockID) + labelRefMark
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

// LoadSpecial reads a special register (ra or sp).
type LoadSpecial struct {
	Dst  *Reg
	Name string
}

// StoreSpecial writes a special register (ra or sp).
type StoreSpecial struct {
	Name string
	Src  Value
}

// LoadIndirect reads the register pointed to by Ptr (IC10 rrN).
type LoadIndirect struct {
	Dst *Reg
	Ptr Value
}

// StoreIndirect writes the register pointed to by Ptr (IC10 rrN).
type StoreIndirect struct {
	Ptr Value
	Src Value
}

// LoadSpill loads a spilled value from a fixed stack slot.
type LoadSpill struct {
	Dst  *Reg
	Slot int
}

// StoreSpill stores a value to a fixed stack slot.
type StoreSpill struct {
	Slot int
	Src  Value
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

// LoadSlot reads a device slot property. When DevPtr is set the device port is
// chosen at runtime from the register it names (IC10 "ls r? drN index slt").
type LoadSlot struct {
	Dst    *Reg
	Dev    string
	DevPtr Value
	Index  Value
	Logic  string
}

// StoreSlot writes a device slot property. When DevPtr is set the device port
// is chosen at runtime (IC10 "ss drN index slt r?").
type StoreSlot struct {
	Dev    string
	DevPtr Value
	Index  Value
	Logic  string
	Src    Value
}

// LoadDyn reads a device logic value whose logic type is chosen at runtime
// (IC10 "l r? d? rN"). When DevPtr is set the device port is also chosen at
// runtime from the register it names (IC10 "l r? drN rM"). When DevID is set
// the device is addressed by its ReferenceId (IC10 "ld r? id rN").
//
// When Reagent is set the node is instead an indirect reagent read
// (IC10 "lr r? drN mode key"): Logic holds the reagent mode and Reagent the
// reagent hash.
type LoadDyn struct {
	Dst     *Reg
	Dev     string
	DevPtr  Value
	DevID   Value
	Logic   Value
	Reagent Value
}

// StoreDyn writes a device logic value whose logic type is chosen at runtime.
// DevID selects a device by ReferenceId (IC10 "sd id rN r?").
type StoreDyn struct {
	Dev    string
	DevPtr Value
	DevID  Value
	Logic  Value
	Src    Value
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

func (*Assign) isInstr()        {}
func (*Bin) isInstr()           {}
func (*Un) isInstr()            {}
func (*Cmp) isInstr()           {}
func (*Select) isInstr()        {}
func (*Load) isInstr()          {}
func (*Store) isInstr()         {}
func (*LoadSlot) isInstr()      {}
func (*StoreSlot) isInstr()     {}
func (*LoadDyn) isInstr()       {}
func (*StoreDyn) isInstr()      {}
func (*Builtin) isInstr()       {}
func (*Batch) isInstr()         {}
func (*LoadSpecial) isInstr()   {}
func (*StoreSpecial) isInstr()  {}
func (*LoadIndirect) isInstr()  {}
func (*StoreIndirect) isInstr() {}
func (*LoadSpill) isInstr()     {}
func (*StoreSpill) isInstr()    {}

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

// Term is defined in term.go (with Successors/Uses/Redirect/Key).

type Jmp struct{ Target *Block }

type Br struct {
	Cond       Cond
	A, B       Value // B is nil for unary conditions
	Then, Else *Block
}

type Ret struct{ Value Value }

// Goto is an unconditional low-level jump to a label block.
type Goto struct{ Target *Block }

// Call sets the return address register and jumps to a label block. Return is
// the block execution continues at after the callee returns; it is laid out
// immediately after the call so that the IC10 return address (pc+1) is correct.
type Call struct {
	Target *Block
	Return *Block
}

// JmpRA jumps to the return address register (IC10 "j ra").
type JmpRA struct{}

// JmpDyn jumps to a computed line number (IC10 "j r0"). When Table is set it
// is a jump table: the codegen emits one `j <target>` per entry right after the
// dispatch and jumps into it with `Target` holding the case index.
type JmpDyn struct {
	Target Value
	Table  []*Block
}

// BrValid branches when a device load/store is invalid (IC10 bdnvl/bdnvs).
type BrValid struct {
	Dev     string
	Logic   string
	Store   bool // true = store validity (bdnvs), false = load (bdnvl)
	Valid   *Block
	Invalid *Block
}

// BrApprox branches when a ≈ b within tol (IC10 bap) or, when Negate is set,
// when a is not ≈ b (IC10 bna). It takes three operands, so it cannot use Br.
type BrApprox struct {
	A, B, Tol  Value
	Negate     bool
	Then, Else *Block
}

// BrApproxZero branches when a ≈ 0 within tol (IC10 bapz) or, when Negate is
// set, when a is not ≈ 0 (IC10 bnaz).
type BrApproxZero struct {
	A, Tol     Value
	Negate     bool
	Then, Else *Block
}

// BrCall conditionally calls a label: when Cond holds it jumps to Target with
// the return address set (IC10 b<cond>al), otherwise it falls through to
// Return. Return must be laid out immediately after the branch so that the
// callee's `j ra` resumes at the continuation.
type BrCall struct {
	Cond   Cond
	A, B   Value
	Target *Block // callee entry
	Return *Block // continuation / fall-through
}

func (*Jmp) isTerm()          {}
func (*Br) isTerm()           {}
func (*Ret) isTerm()          {}
func (*Goto) isTerm()         {}
func (*Call) isTerm()         {}
func (*JmpRA) isTerm()        {}
func (*JmpDyn) isTerm()       {}
func (*BrValid) isTerm()      {}
func (*BrApprox) isTerm()     {}
func (*BrApproxZero) isTerm() {}
func (*BrCall) isTerm()       {}

// ---------------------------------------------------------------------------
// Blocks and functions
// ---------------------------------------------------------------------------

type Block struct {
	ID     int
	Func   string // source function this block was emitted for ("" = main)
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
	// ReservedRegs marks physical registers the function accesses indirectly
	// (IC10 rrN via ireg/setIreg, declared with reserveRegs). The allocator
	// never colours a virtual register there, so indirect access is safe.
	ReservedRegs [16]bool
	// UserStackManual is the highest user stack slot touched by an explicit
	// absolute access (db.stack[addr], poke, get/put db), stored as slot+1.
	// UserStackDynamic is set when such an address is not a compile-time
	// constant. push/pop depth is computed separately from the CFG.
	UserStackManual  int
	UserStackDynamic bool
	// UserStackUses records every explicit user access to the persistent stack
	// so a later pass can check it against the compiler's region.
	UserStackUses []UserStackUse
}

// UserStackUse is one explicit user access to the persistent stack
// (db.stack[addr], poke, get/put db). Slot is the constant address; Dynamic is
// set when the address is not a compile-time constant.
type UserStackUse struct {
	Slot    int
	Dynamic bool
	Pos     source.Pos
}

// BuildCFG recomputes predecessor and successor lists from terminators.
func (f *Function) BuildCFG() {
	for _, b := range f.Blocks {
		b.Preds = nil
		b.Succs = nil
	}
	// A ret (JmpRA) can return to any call site, so it may transfer control to
	// any call's return block. Modelling this keeps values written by a callee
	// live after the call.
	var returns []*Block
	for _, b := range f.Blocks {
		if b.Term != nil {
			b.Succs = append(b.Succs, b.Term.Successors()...)
		}
		if hr, ok := b.Term.(interface{ returnBlock() *Block }); ok {
			if r := hr.returnBlock(); r != nil {
				returns = append(returns, r)
			}
		}
	}
	if len(returns) > 0 {
		for _, b := range f.Blocks {
			if _, ok := b.Term.(*JmpRA); ok {
				b.Succs = append(b.Succs, returns...)
			}
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

// RemoveBlock removes b from the function's block list. It does not update any
// terminator that references b; callers must redirect those first.
func (f *Function) RemoveBlock(b *Block) {
	for i, x := range f.Blocks {
		if x == b {
			f.Blocks = append(f.Blocks[:i], f.Blocks[i+1:]...)
			return
		}
	}
}
