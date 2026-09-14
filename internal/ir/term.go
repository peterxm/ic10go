package ir

import "strconv"

// Term is a block terminator. Successors returns the control-flow successors in
// the preferred layout order: the order codegen should visit them so that the
// intended block ends up as the fall-through after reverse-post-order layout.
// Uses returns the values read by the terminator (entries may be nil and are
// ignored by callers). Redirect rewrites a block reference. RewriteUses maps
// each read value through rewrite. Key is a structural key used to detect
// equivalent terminators.
type Term interface {
	isTerm()
	Successors() []*Block
	Uses() []Value
	Redirect(from, to *Block)
	RewriteUses(rewrite func(Value) Value)
	Key() string
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func valueKey(v Value) string {
	switch x := v.(type) {
	case *Reg:
		return "r" + strconv.Itoa(x.ID)
	case *Const:
		return "c" + x.String()
	case *Device:
		return "d" + x.Name
	}
	return "?"
}

func blockKey(b *Block) string {
	if b == nil {
		return "-"
	}
	return strconv.Itoa(b.ID)
}

func appendUse(u []Value, v Value) []Value {
	if v != nil {
		u = append(u, v)
	}
	return u
}

// ---------------------------------------------------------------------------
// Jmp / Goto / Call / JmpRA / JmpDyn
// ---------------------------------------------------------------------------

func (t *Jmp) Successors() []*Block { return []*Block{t.Target} }
func (t *Jmp) Uses() []Value        { return nil }
func (t *Jmp) Redirect(from, to *Block) {
	if t.Target == from {
		t.Target = to
	}
}
func (t *Jmp) Key() string { return "jmp|" + blockKey(t.Target) }

func (t *Goto) Successors() []*Block { return []*Block{t.Target} }
func (t *Goto) Uses() []Value        { return nil }
func (t *Goto) Redirect(from, to *Block) {
	if t.Target == from {
		t.Target = to
	}
}
func (t *Goto) Key() string { return "goto|" + blockKey(t.Target) }

func (t *Call) Successors() []*Block {
	if t.Return == nil {
		return []*Block{t.Target}
	}
	// Visit the callee first so the return block is laid out right after it.
	return []*Block{t.Target, t.Return}
}
func (t *Call) Uses() []Value { return nil }
func (t *Call) Redirect(from, to *Block) {
	if t.Target == from {
		t.Target = to
	}
	if t.Return == from {
		t.Return = to
	}
}
func (t *Call) Key() string {
	return "call|" + blockKey(t.Target) + "|" + blockKey(t.Return)
}

func (t *Call) returnBlock() *Block { return t.Return }

func (t *JmpRA) Successors() []*Block { return nil }
func (t *JmpRA) Uses() []Value        { return nil }
func (t *JmpRA) Redirect(*Block, *Block) {}
func (t *JmpRA) Key() string          { return "jmpra" }

func (t *JmpDyn) Successors() []*Block { return t.Table }
func (t *JmpDyn) Uses() []Value        { return []Value{t.Target} }
func (t *JmpDyn) Redirect(from, to *Block) {
	for i, tb := range t.Table {
		if tb == from {
			t.Table[i] = to
		}
	}
}
func (t *JmpDyn) Key() string { return "jmpdyn|" + valueKey(t.Target) }

// ---------------------------------------------------------------------------
// Ret and conditional terminators
// ---------------------------------------------------------------------------

func (t *Ret) Successors() []*Block { return nil }
func (t *Ret) Uses() []Value        { return appendUse(nil, t.Value) }
func (t *Ret) Redirect(*Block, *Block) {}
func (t *Ret) Key() string          { return "ret|" + valueKey(t.Value) }

func (t *Br) Successors() []*Block {
	// Visit the false edge first so the true target becomes the fall-through.
	return []*Block{t.Else, t.Then}
}
func (t *Br) Uses() []Value { return []Value{t.A, t.B} }
func (t *Br) Redirect(from, to *Block) {
	if t.Then == from {
		t.Then = to
	}
	if t.Else == from {
		t.Else = to
	}
}
func (t *Br) Key() string {
	return "br|" + strconv.Itoa(int(t.Cond)) + "|" + valueKey(t.A) + "|" + valueKey(t.B) +
		"|" + blockKey(t.Then) + "|" + blockKey(t.Else)
}

func (t *BrValid) Successors() []*Block {
	// Lay out Valid as the fall-through: visit Invalid first.
	return []*Block{t.Invalid, t.Valid}
}
func (t *BrValid) Uses() []Value { return nil }
func (t *BrValid) Redirect(from, to *Block) {
	if t.Valid == from {
		t.Valid = to
	}
	if t.Invalid == from {
		t.Invalid = to
	}
}
func (t *BrValid) Key() string {
	return "brvalid|" + t.Dev + "|" + t.Logic + "|" + strconv.FormatBool(t.Store) +
		"|" + blockKey(t.Valid) + "|" + blockKey(t.Invalid)
}

func (t *BrApprox) Successors() []*Block { return []*Block{t.Else, t.Then} }
func (t *BrApprox) Uses() []Value        { return []Value{t.A, t.B, t.Tol} }
func (t *BrApprox) Redirect(from, to *Block) {
	if t.Then == from {
		t.Then = to
	}
	if t.Else == from {
		t.Else = to
	}
}
func (t *BrApprox) Key() string {
	return "brapprox|" + strconv.FormatBool(t.Negate) + "|" + valueKey(t.A) + "|" + valueKey(t.B) +
		"|" + valueKey(t.Tol) + "|" + blockKey(t.Then) + "|" + blockKey(t.Else)
}

func (t *BrApproxZero) Successors() []*Block { return []*Block{t.Else, t.Then} }
func (t *BrApproxZero) Uses() []Value        { return []Value{t.A, t.Tol} }
func (t *BrApproxZero) Redirect(from, to *Block) {
	if t.Then == from {
		t.Then = to
	}
	if t.Else == from {
		t.Else = to
	}
}
func (t *BrApproxZero) Key() string {
	return "brapproxz|" + strconv.FormatBool(t.Negate) + "|" + valueKey(t.A) + "|" + valueKey(t.Tol) +
		"|" + blockKey(t.Then) + "|" + blockKey(t.Else)
}

func (t *BrCall) Successors() []*Block {
	// Visit the callee first so the continuation is laid out right after.
	return []*Block{t.Target, t.Return}
}
func (t *BrCall) Uses() []Value { return []Value{t.A, t.B} }
func (t *BrCall) Redirect(from, to *Block) {
	if t.Target == from {
		t.Target = to
	}
	if t.Return == from {
		t.Return = to
	}
}
func (t *BrCall) Key() string {
	return "brcall|" + strconv.Itoa(int(t.Cond)) + "|" + valueKey(t.A) + "|" + valueKey(t.B) +
		"|" + blockKey(t.Target) + "|" + blockKey(t.Return)
}

func (t *BrCall) returnBlock() *Block { return t.Return }

// ---------------------------------------------------------------------------
// RewriteUses
// ---------------------------------------------------------------------------

func (t *Jmp) RewriteUses(func(Value) Value)     {}
func (t *Goto) RewriteUses(func(Value) Value)    {}
func (t *Call) RewriteUses(func(Value) Value)    {}
func (t *JmpRA) RewriteUses(func(Value) Value)   {}
func (t *BrValid) RewriteUses(func(Value) Value) {}

func (t *JmpDyn) RewriteUses(rewrite func(Value) Value) {
	if t.Target != nil {
		t.Target = rewrite(t.Target)
	}
}

func (t *Ret) RewriteUses(rewrite func(Value) Value) {
	if t.Value != nil {
		t.Value = rewrite(t.Value)
	}
}

func (t *Br) RewriteUses(rewrite func(Value) Value) {
	if t.A != nil {
		t.A = rewrite(t.A)
	}
	if t.B != nil {
		t.B = rewrite(t.B)
	}
}

func (t *BrApprox) RewriteUses(rewrite func(Value) Value) {
	if t.A != nil {
		t.A = rewrite(t.A)
	}
	if t.B != nil {
		t.B = rewrite(t.B)
	}
	if t.Tol != nil {
		t.Tol = rewrite(t.Tol)
	}
}

func (t *BrApproxZero) RewriteUses(rewrite func(Value) Value) {
	if t.A != nil {
		t.A = rewrite(t.A)
	}
	if t.Tol != nil {
		t.Tol = rewrite(t.Tol)
	}
}

func (t *BrCall) RewriteUses(rewrite func(Value) Value) {
	if t.A != nil {
		t.A = rewrite(t.A)
	}
	if t.B != nil {
		t.B = rewrite(t.B)
	}
}
