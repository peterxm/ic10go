// Package codegen turns IR into IC10 assembly text.
package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"ic10go/internal/builtin"
	"ic10go/internal/ic10asm"
	"ic10go/internal/ir"
)

// IC10 editor limits the toolchain targets by default. They are used when a
// Limits field is zero; callers can override them (see Limits and
// Options.Limits, or the IC10C_MAX_* environment variables) so the toolchain
// can follow the game if its limits change again. Keep these in sync with the
// VSCode defaults (editors/vscode) and the docs.
const (
	MaxLines   = 128
	MaxBytes   = 4096
	MaxLineLen = 90
)

// Limits are the IC10 editor limits a program is validated against. A zero
// field falls back to the corresponding default (MaxLines / MaxBytes /
// MaxLineLen).
type Limits struct {
	Lines   int // maximum program lines
	Bytes   int // maximum program bytes
	LineLen int // maximum characters per line
}

// DefaultLimits are the editor limits used when a Limits field is zero.
var DefaultLimits = Limits{Lines: MaxLines, Bytes: MaxBytes, LineLen: MaxLineLen}

// Resolve fills zero fields with the defaults, so a partially-specified Limits
// is usable.
func (l Limits) Resolve() Limits {
	if l.Lines <= 0 {
		l.Lines = MaxLines
	}
	if l.Bytes <= 0 {
		l.Bytes = MaxBytes
	}
	if l.LineLen <= 0 {
		l.LineLen = MaxLineLen
	}
	return l
}

// relativeJump rewrites an absolute jump/branch line (whose text ends with a
// space before the target) into its relative form, returning the new text and
// the relative offset. It reports false for instructions without a verified
// relative form (bdnvl/bdnvs).
func relativeJump(text string, off int) (string, string, bool) {
	mnemonic, rest, found := strings.Cut(text, " ")
	if !found {
		return text, "", false
	}
	if mnemonic == "j" {
		return "jr " + rest, strconv.Itoa(off), true
	}
	if strings.HasPrefix(mnemonic, "b") {
		cond := mnemonic[1:]
		if cond == "dnvl" || cond == "dnvs" {
			return text, "", false
		}
		if _, _, withRA, ok := ic10asm.BranchInfo(mnemonic); ok && !withRA {
			return "br" + cond + " " + rest, strconv.Itoa(off), true
		}
	}
	return text, "", false
}

// spillScratch is the physical register reserved for spill loads when the
// allocator had to spill (the allocator then uses one fewer register).
const spillScratch = "r15"

type line struct {
	text   string
	target *ir.Block
	// imm, when set, is written instead of the target block's line number.
	imm string
	// halt marks a branch to a halt block. Its explicit target is resolved at
	// render time to a line just past the program (see haltTarget), so it stays
	// correct as the line limit grows.
	halt bool
	fn   string // source function this line was emitted for ("" = main)
	// table marks a jump-table entry (`j <case>` right after the dispatch).
	// The entries are addressed by index, so a no-op entry must never be
	// dropped: doing so would renumber the rest of the table.
	table bool
	// src is the 1-based .icg source line this line was lowered from, or 0 when
	// unknown. It feeds the source line map.
	src int
	// safeInvert marks a conditional branch whose negation is exact for every
	// input (an equality/zero test, or an ordering test on provably-NaN-free
	// operands). Only such branches may be rewritten to the negated form when
	// folding a following jump.
	safeInvert bool
}

// haltMarker is the IR-level sentinel a halt block jumps to. It is a marker,
// not a real line: codegen renders it as a target past the configured program
// length, so a program longer than haltMarker still halts correctly.
const haltMarker = 9999

// haltTarget returns a line number guaranteed to be past any valid program: one
// beyond the configured line limit. IC10 halts when it jumps past the end.
func haltTarget(limits Limits) string {
	return strconv.Itoa(limits.Resolve().Lines + 1)
}

// isHaltBlock reports whether a block only halts by jumping past the program
// (a bare `jump(9999)`). Such a block emits no line; branches to it target
// haltTarget directly.
func isHaltBlock(b *ir.Block) bool {
	if b == nil || len(b.Instrs) != 0 {
		return false
	}
	jd, ok := b.Term.(*ir.JmpDyn)
	if !ok || jd.Table != nil {
		return false
	}
	c, ok := jd.Target.(*ir.Const)
	return ok && c.Raw == "" && c.Special == "" && c.V == haltMarker
}

// fallsThroughTo reports whether prev's terminator can fall through to b (which
// is the block laid out immediately after prev).
func fallsThroughTo(prev, b *ir.Block) bool {
	if prev == nil || b == nil {
		return false
	}
	switch t := prev.Term.(type) {
	case *ir.Jmp:
		return t.Target == b
	case *ir.Goto:
		return t.Target == b
	case *ir.Br:
		return t.Then == b || t.Else == b
	case *ir.BrApprox:
		return t.Then == b || t.Else == b
	case *ir.BrApproxZero:
		return t.Then == b || t.Else == b
	case *ir.BrValid:
		return t.Valid == b || t.Invalid == b
	case *ir.BrSet:
		return t.Then == b || t.Else == b
	}
	return false
}

// Report breaks the generated code down by source function.
type Report struct {
	Total  int
	ByFunc map[string]int
	// LineMap maps a 1-based generated IC10 line to the 1-based .icg source
	// line it came from (0 when unknown). It is indexed by line number, so
	// len(LineMap) is Total+1.
	LineMap []int
}

// Options controls code generation.
type Options struct {
	// RelJump emits relative jumps (IC10 jr / br*) instead of absolute ones,
	// saving bytes. It requires the game's relative-jump base to match the VM
	// (relative to the jump's own line).
	RelJump bool
	// SpillDB renders register spills as get/put db (one line per load) instead
	// of the peek/poke sp save-restore sequence (five lines per load). It must
	// match the spill mode used by the register allocator.
	SpillDB bool
	// Limits overrides the IC10 editor limits the output is validated against.
	// A zero field means the default; the zero Limits means DefaultLimits.
	Limits Limits
	// LegacyByID emits the legacy `ld`/`sd` mnemonics for readById/writeById
	// instead of the current `l`/`s` with a ReferenceId device operand. Off by
	// default (the game's deprecated spelling is only for old game versions).
	LegacyByID bool
	// NaNSafe makes the codegen respect IEEE NaN semantics for ordering
	// comparisons: IC10's `!(a < b)` is not `a >= b` when an operand is NaN, so
	// an ordering branch is negated only when both operands are provably
	// numbers (or shown to be by an `isNaN` guard). Off by default: the shorter
	// negated form is emitted and the program is expected to handle NaN itself.
	NaNSafe bool
}

// Generate renders a function to IC10 code and validates the result against the
// IC10 editor limits.
func Generate(fn *ir.Function, colors map[*ir.Reg]int) (string, error) {
	return GenerateWithOptions(fn, colors, Options{})
}

// GenerateWithOptions is Generate with explicit options.
func GenerateWithOptions(fn *ir.Function, colors map[*ir.Reg]int, opts Options) (string, error) {
	code, _, err := GenerateReportWithOptions(fn, colors, opts)
	return code, err
}

// GenerateReport is Generate plus a per-function line breakdown, used by the
// size report to show which functions cost the most lines.
func GenerateReport(fn *ir.Function, colors map[*ir.Reg]int) (string, *Report, error) {
	return GenerateReportWithOptions(fn, colors, Options{})
}

// layoutLines computes the codegen block order and the emitted lines before
// branch targets are resolved to line numbers. It is shared by code generation
// and by Layout (used for the control-flow graph).
func layoutLines(fn *ir.Function, colors map[*ir.Reg]int, spillDB, legacyByID, nanSafe bool) ([]*ir.Block, []line, map[*ir.Block]int) {
	fn.BuildCFG()
	// The NaN analysis is only needed when NaN-safe codegen is requested; off by
	// default, the codegen negates ordering branches freely for the shorter form.
	var ni *nanInfo
	if nanSafe {
		ni = analyzeNaN(fn)
	}
	var blocks []*ir.Block
	if ni != nil {
		blocks = rpoOrdered(fn, ni)
	} else {
		blocks = rpo(fn)
	}
	// Blocks that a call returns to must be laid out right after the call and
	// therefore stay in place even if they only halt.
	retBlocks := map[*ir.Block]bool{}
	for _, b := range fn.Blocks {
		switch t := b.Term.(type) {
		case *ir.Call:
			if t.Return != nil {
				retBlocks[t.Return] = true
			}
		case *ir.BrCall:
			if t.Return != nil {
				retBlocks[t.Return] = true
			}
		}
	}
	// Move halt (Ret and bare-halt) blocks to the end of the layout. They emit
	// no line, so a jump/fallthrough to one must land past the program.
	{
		var rest, halt []*ir.Block
		for _, b := range blocks {
			if _, ok := b.Term.(*ir.Ret); ok || (isHaltBlock(b) && !retBlocks[b]) {
				halt = append(halt, b)
			} else {
				rest = append(rest, b)
			}
		}
		blocks = append(rest, halt...)
	}
	// A call's return block must sit immediately after the call so that the
	// IC10 return address (pc+1) points at the continuation. RPO usually does
	// this, but a callee that itself calls the caller's return block can push
	// it away.
	blocks = orderCallReturns(blocks)
	var lines []line
	start := map[*ir.Block]int{}
	curSrc := 0
	add := func(text string, target *ir.Block, f string) {
		lines = append(lines, line{text: text, target: target, fn: f, src: curSrc})
	}

	uses := regUseCounts(fn)

	// A nested call (made from inside an outlined body) overwrites ra with the
	// inner jal, so save it before the call and restore it at the continuation.
	// Real-machine verified: push ra / pop ra round-trips.
	popRA := map[*ir.Block]bool{}
	for _, b := range fn.Blocks {
		switch t := b.Term.(type) {
		case *ir.Call:
			if t.Nested && t.Return != nil {
				popRA[t.Return] = true
			}
		case *ir.BrCall:
			if t.Nested && t.Return != nil {
				popRA[t.Return] = true
			}
		}
	}

	consumed := map[*ir.Block]int{}
	for i, b := range blocks {
		curSrc = b.SrcLine
		if consumed[b] == 0 {
			if popRA[b] {
				add("pop ra", nil, b.Func)
			}
			start[b] = len(lines)
		}
		var next *ir.Block
		if i+1 < len(blocks) {
			next = blocks[i+1]
		}
		instrs := b.Instrs[consumed[b]:]
		// When b unconditionally jumps to the layout successor and that
		// successor has no other predecessor, the two blocks run as one
		// straight line (the jump is elided), so let the folds span the
		// boundary: a select whose result the next block stores to sp folds
		// into `select sp ...`.
		var spill *ir.Block
		if consumed[b] == 0 && next != nil && next != b && consumed[next] == 0 && len(next.Preds) == 1 {
			if t, ok := b.Term.(*ir.Jmp); ok && t.Target == next {
				merged := make([]ir.Instr, 0, len(b.Instrs)+len(next.Instrs))
				merged = append(merged, b.Instrs...)
				merged = append(merged, next.Instrs...)
				instrs = merged
				spill = next
			}
		}
		spillStart := -1
		termDone := false
		for idx := 0; idx < len(instrs); idx++ {
			if spill != nil && spillStart < 0 && idx >= len(b.Instrs) {
				spillStart = len(lines)
			}
			ins := instrs[idx]
			if s := fn.SrcLines[ins]; s > 0 {
				curSrc = s
			} else {
				curSrc = b.SrcLine
			}
			if text, n, ok := foldSpecialArith(instrs, idx, uses, colors); ok {
				if spill != nil && spillStart < 0 && idx+n > len(b.Instrs) {
					spillStart = len(lines)
				}
				add(text, nil, b.Func)
				idx += n - 1
				continue
			}
			if text, n, ok := foldIndirectDst(instrs, idx, uses, colors); ok {
				if spill != nil && spillStart < 0 && idx+n > len(b.Instrs) {
					spillStart = len(lines)
				}
				add(text, nil, b.Func)
				idx += n - 1
				continue
			}
			if text, n, ok := foldLoadOperand(instrs, idx, uses, colors, legacyByID); ok {
				if spill != nil && spillStart < 0 && idx+n > len(b.Instrs) {
					spillStart = len(lines)
				}
				add(text, nil, b.Func)
				idx += n - 1
				continue
			}
			// A single-use load that feeds the block's branch terminator folds
			// into it (`u = sp; bgtz u L` -> `bgtz sp L`); the lowerer's copy to
			// the variable is skipped too.
			if spill == nil && !termDone && idx >= len(instrs)-2 {
				src := b.SrcLine
				if s := fn.SrcTerms[b.Term]; s > 0 {
					src = s
				}
				if ls, ok := foldLoadTerminator(instrs, idx, b, next, uses, colors, src, ni); ok {
					lines = append(lines, ls...)
					termDone = true
					continue
				}
			}
			if ls, ok := ins.(*ir.LoadSpill); ok {
				dst := regName(ls.Dst, colors)
				if spillDB {
					add("get "+dst+" db "+strconv.Itoa(ls.Slot), nil, b.Func)
					continue
				}
				add("move "+spillScratch+" sp", nil, b.Func)
				add("move sp "+strconv.Itoa(ls.Slot), nil, b.Func)
				add("add sp sp 1", nil, b.Func)
				add("peek "+dst, nil, b.Func)
				add("move sp "+spillScratch, nil, b.Func)
				continue
			}
			if ss, ok := ins.(*ir.StoreSpill); ok {
				if spillDB {
					add("put db "+strconv.Itoa(ss.Slot)+" "+valueText(ss.Src, colors), nil, b.Func)
					continue
				}
				add("poke "+strconv.Itoa(ss.Slot)+" "+valueText(ss.Src, colors), nil, b.Func)
				continue
			}
			if text, ok := renderInstr(ins, colors, legacyByID); ok {
				add(text, nil, b.Func)
			}
		}
		if spill != nil {
			if spillStart >= 0 {
				start[spill] = spillStart
			}
			// The successor's instructions were emitted above; its own
			// terminator is still handled when the layout reaches it.
			consumed[spill] = len(spill.Instrs)
			continue
		}
		if termDone {
			continue // the terminator was folded into the last instruction
		}
		if isHaltBlock(b) {
			// A bare halt emits no line, so branches target 9999 directly. It
			// still needs a line when control can fall into it (the previous
			// block falls through, or a call returns here).
			if !retBlocks[b] && (i == 0 || !fallsThroughTo(blocks[i-1], b)) {
				continue
			}
		}
		if s := fn.SrcTerms[b.Term]; s > 0 {
			curSrc = s
		} else {
			curSrc = b.SrcLine
		}
		switch t := b.Term.(type) {
		case *ir.Jmp:
			if t.Target != next {
				add("j ", t.Target, b.Func)
			}
		case *ir.Goto:
			if t.Target != next {
				add("j ", t.Target, b.Func)
			}
		case *ir.Call:
			if t.Nested {
				add("push ra", nil, b.Func)
			}
			add("jal ", t.Target, b.Func)
		case *ir.JmpRA:
			add("j ra", nil, b.Func)
		case *ir.JmpDyn:
			if len(t.Table) > 0 {
				// Jump table: the entries are `j <case>` lines right after the
				// `jr`; `jr rX` is relative to its own line, so rX = index + 1.
				reg := valueText(t.Target, colors)
				add("add "+reg+" "+reg+" 1", nil, b.Func)
				add("jr "+reg, nil, b.Func)
				for _, tb := range t.Table {
					lines = append(lines, line{text: "j ", target: tb, fn: b.Func, table: true, src: curSrc})
				}
			} else {
				add("j "+valueText(t.Target, colors), nil, b.Func)
			}
		case *ir.BrValid:
			m := "bdnvl"
			if t.Store {
				m = "bdnvs"
			}
			add(m+" "+brDev(t.Dev, t.DevPtr, colors)+" "+t.Logic+" ", t.Invalid, b.Func)
			if t.Valid != next {
				add("j ", t.Valid, b.Func)
			}
		case *ir.BrSet:
			thenM, elseM := "bdse", "bdns"
			if !t.Set {
				thenM, elseM = "bdns", "bdse"
			}
			dev := brDev(t.Dev, t.DevPtr, colors)
			switch {
			case t.Else == next:
				add(thenM+" "+dev+" ", t.Then, b.Func)
			case t.Then == next:
				add(elseM+" "+dev+" ", t.Else, b.Func)
			default:
				add(thenM+" "+dev+" ", t.Then, b.Func)
				add("j ", t.Else, b.Func)
			}
		case *ir.Br:
			thenNext := t.Then == next
			elseNext := t.Else == next
			// `inv` reports whether the branch may be negated: equality and
			// zero tests always, a NaN test never, and an ordering test when the
			// NaNSafe analysis proved the operands (or that check is disabled).
			inv := t.Cond.Invertible()
			if !inv && t.Cond != ir.NaN {
				inv = ni == nil || ni.branchInvertible(b, t)
			}
			emitBr := func(text string, target *ir.Block) {
				add(text, target, b.Func)
				lines[len(lines)-1].safeInvert = inv
			}
			switch {
			case !inv:
				// No exact inverse (NaN, or `!(a < b)` != `a >= b` when an
				// operand may be NaN): branch on the condition directly and jump
				// to Else when it is not the next block.
				emitBr(branchText(t.Cond, t.A, t.B, colors)+" ", t.Then)
				if !elseNext {
					add("j ", t.Else, b.Func)
				}
			case elseNext:
				emitBr(branchText(t.Cond, t.A, t.B, colors)+" ", t.Then)
			case thenNext:
				emitBr(branchText(t.Cond.Invert(), t.A, t.B, colors)+" ", t.Else)
			default:
				emitBr(branchText(t.Cond, t.A, t.B, colors)+" ", t.Then)
				add("j ", t.Else, b.Func)
			}
		case *ir.BrApprox:
			m, im := "bap", "bna"
			if t.Negate {
				m, im = "bna", "bap"
			}
			switch {
			case t.Else == next:
				add(approxText(m, t.A, t.B, t.Tol, colors)+" ", t.Then, b.Func)
			case t.Then == next:
				add(approxText(im, t.A, t.B, t.Tol, colors)+" ", t.Else, b.Func)
			default:
				add(approxText(m, t.A, t.B, t.Tol, colors)+" ", t.Then, b.Func)
				add("j ", t.Else, b.Func)
			}
		case *ir.BrApproxZero:
			m, im := "bapz", "bnaz"
			if t.Negate {
				m, im = "bnaz", "bapz"
			}
			switch {
			case t.Else == next:
				add(approxZeroText(m, t.A, t.Tol, colors)+" ", t.Then, b.Func)
			case t.Then == next:
				add(approxZeroText(im, t.A, t.Tol, colors)+" ", t.Else, b.Func)
			default:
				add(approxZeroText(m, t.A, t.Tol, colors)+" ", t.Then, b.Func)
				add("j ", t.Else, b.Func)
			}
		case *ir.BrCall:
			// Conditional call: branch with the return address to the callee;
			// the continuation is laid out right after this line.
			if t.Nested {
				add("push ra", nil, b.Func)
			}
			add(branchCallText(t.Cond, t.A, t.B, colors)+" ", t.Target, b.Func)
		}
	}

	// Branches to a halt block target past the program directly; the exact line
	// is filled in at render time from the configured line limit.
	for i := range lines {
		if lines[i].target != nil && isHaltBlock(lines[i].target) {
			lines[i].halt = true
			lines[i].target = nil
		}
	}

	lines, start = simplifyBranches(lines, start)

	// A branch to a block that produced no line would resolve past the end;
	// give the program a line for it to land on. This is checked after the
	// branch cleanup, which can remove a block's only line.
	for _, ln := range lines {
		if ln.target != nil && start[ln.target] >= len(lines) {
			lines = append(lines, line{text: "move r0 r0"})
			break
		}
	}

	return blocks, lines, start
}

// regUseCounts counts how many times each register is read across the whole
// function (instructions and terminators). The special-register folding uses
// it to prove a foldable temporary has no other use.
func regUseCounts(fn *ir.Function) map[*ir.Reg]int {
	uses := map[*ir.Reg]int{}
	for _, b := range fn.Blocks {
		for _, ins := range b.Instrs {
			u, _ := ir.DefUse(ins)
			for _, r := range u {
				uses[r]++
			}
		}
		for _, r := range ir.TermUses(b.Term) {
			uses[r]++
		}
	}
	return uses
}

// foldSpecialArith folds a computation into a temporary followed by a store to
// a special register (sp or ra) into one instruction that writes the special
// register directly. It recognises two shapes:
//
//	sp = t            (t = a op b)      ->  <op> sp a b      (2 instructions)
//	t1 = sp; sp = t2  (t2 = t1 op b)    ->  <op> sp sp b     (3 instructions)
//
// where <op> is a binary op, a unary op (neg/not/seqz) or `select`. IC10 allows
// sp/ra as an ordinary destination operand, so the temporary and the `move` are
// unnecessary. The fold only fires when the temporary's sole use is the store
// (and, for the three-instruction shape, the loaded value's sole use is the
// computation), which keeps it safe. It returns the folded text and how many
// instructions it consumed.
func foldSpecialArith(instrs []ir.Instr, i int, uses map[*ir.Reg]int, colors map[*ir.Reg]int) (string, int, bool) {
	// Three-instruction shape: load the special, compute from it, store back.
	if i+2 < len(instrs) {
		if ls, ok := instrs[i].(*ir.LoadSpecial); ok && uses[ls.Dst] == 1 {
			if st, ok := instrs[i+2].(*ir.StoreSpecial); ok && st.Name == ls.Name {
				if text, ok := specialComputeText(instrs[i+1], st, ls.Dst, uses, colors); ok {
					return text, 3, true
				}
			}
		}
	}
	// Two-instruction shape: compute into a temporary, store it.
	if i+1 < len(instrs) {
		if st, ok := instrs[i+1].(*ir.StoreSpecial); ok {
			if text, ok := specialComputeText(instrs[i], st, nil, uses, colors); ok {
				return text, 2, true
			}
		}
	}
	return "", 0, false
}

// specialComputeText renders a Bin/Un whose result is written directly to a
// special register instead of a temporary. st is the store being folded, which
// must read that same temporary; loaded, when non-nil, is a register whose value
// is the special register itself (a folded load) and renders as the special's
// name.
func specialComputeText(ins ir.Instr, st *ir.StoreSpecial, loaded *ir.Reg, uses map[*ir.Reg]int, colors map[*ir.Reg]int) (string, bool) {
	src, ok := st.Src.(*ir.Reg)
	if !ok {
		return "", false
	}
	// The temporary must be defined by this instruction and read nowhere else,
	// so the store is its only reader. (The producer reading its own result,
	// e.g. `select t t a b`, is rejected: folding would leave that operand
	// pointing at a register this instruction no longer defines.)
	if uses[src] != 1 {
		return "", false
	}
	operand := func(v ir.Value) string {
		if loaded != nil {
			if r, ok := v.(*ir.Reg); ok && r == loaded {
				return st.Name
			}
		}
		return valueText(v, colors)
	}
	switch v := ins.(type) {
	case *ir.Bin:
		if v.Dst != src {
			return "", false
		}
		return v.Op.IC10() + " " + st.Name + " " + operand(v.A) + " " + operand(v.B), true
	case *ir.Un:
		if v.Dst != src {
			return "", false
		}
		switch v.Op {
		case ir.Neg:
			return "sub " + st.Name + " 0 " + operand(v.A), true
		case ir.BitNot:
			return "not " + st.Name + " " + operand(v.A), true
		case ir.Seqz:
			return "seqz " + st.Name + " " + operand(v.A), true
		}
	case *ir.Select:
		if v.Dst != src {
			return "", false
		}
		return "select " + st.Name + " " + operand(v.Cond) + " " + operand(v.Then) +
			" " + operand(v.Else), true
	}
	return "", false
}

// foldIndirectDst folds a computation into a store through a register-indexed
// destination (IC10 rrN): `t = <op> ...; rrP = t` becomes `<op> rrP ...`. The
// indirect form is an ordinary destination operand in IC10, so the temporary
// and the copy are unnecessary. It returns the folded text and how many
// instructions it consumed (always two), and only fires when the temporary's
// sole use is the store.
func foldIndirectDst(instrs []ir.Instr, i int, uses map[*ir.Reg]int, colors map[*ir.Reg]int) (string, int, bool) {
	compute := instrs[i]
	def := ir.DefOf(compute)
	if def == nil {
		return "", 0, false
	}
	// The lowerer materialises a call result into a temporary and copies it to
	// the variable, so the store may read a copy of the computed value.
	src := def
	consume := 2
	if i+2 < len(instrs) {
		if cp, ok := instrs[i+1].(*ir.Assign); ok && uses[def] == 1 {
			if r, ok := cp.Src.(*ir.Reg); ok && r == def && cp.Dst != def {
				src, consume = cp.Dst, 3
			}
		}
	}
	if i+consume-1 >= len(instrs) {
		return "", 0, false
	}
	st, ok := instrs[i+consume-1].(*ir.StoreIndirect)
	if !ok || uses[src] != 1 {
		return "", 0, false
	}
	if s, ok := st.Src.(*ir.Reg); !ok || s != src {
		return "", 0, false
	}
	dst := indirectName(st.Ptr, colors)
	operands := func(vals []ir.Value) string {
		parts := make([]string, 0, len(vals))
		for _, v := range vals {
			parts = append(parts, valueText(v, colors))
		}
		return strings.Join(parts, " ")
	}
	switch v := compute.(type) {
	case *ir.Bin:
		return v.Op.IC10() + " " + dst + " " + operands([]ir.Value{v.A, v.B}), consume, true
	case *ir.Un:
		switch v.Op {
		case ir.Neg:
			return "sub " + dst + " 0 " + valueText(v.A, colors), consume, true
		case ir.BitNot:
			return "not " + dst + " " + valueText(v.A, colors), consume, true
		case ir.Seqz:
			return "seqz " + dst + " " + valueText(v.A, colors), consume, true
		}
	case *ir.Builtin:
		text := builtin.Funcs[v.Name].Mnemonic + " " + dst
		if args := operands(v.Args); args != "" {
			text += " " + args
		}
		return text, consume, true
	}
	return "", 0, false
}

// foldLoadOperand folds a single-use load into the instruction that consumes it,
// so the load line disappears: `u = ireg(rrP); d = u op b` becomes
// `d = rrP op b` (the indexed form is an ordinary source operand in IC10). The
// special registers fold the same way: `u = sp; d = u + k` -> `d = sp + k`.
//
// Beyond arithmetic it also folds into any consumer that takes the register as
// an ordinary source operand: device stores (`s` / `ss` / `sd`), builtins
// (`poke`, `put`, ...) and `select`. The load must sit immediately before its
// consumer, the loaded value must be used exactly once, and the consumer must
// actually read it, so nothing can change the register in between and the load
// is never dropped while still needed.
func foldLoadOperand(instrs []ir.Instr, i int, uses map[*ir.Reg]int, colors map[*ir.Reg]int, legacyByID bool) (string, int, bool) {
	operand, loaded, ci, ok := loadOperand(instrs, i, uses, colors)
	if !ok || ci >= len(instrs) {
		return "", 0, false
	}
	consume := ci - i + 1
	val := func(v ir.Value) string {
		if r, ok := v.(*ir.Reg); ok && r == loaded {
			return operand
		}
		return valueText(v, colors)
	}
	mentions := func(vs ...ir.Value) bool {
		for _, v := range vs {
			if r, ok := v.(*ir.Reg); ok && r == loaded {
				return true
			}
		}
		return false
	}
	switch v := instrs[ci].(type) {
	case *ir.Bin:
		if !mentions(v.A, v.B) {
			return "", 0, false
		}
		return v.Op.IC10() + " " + regName(v.Dst, colors) + " " + val(v.A) + " " + val(v.B), consume, true
	case *ir.Un:
		if !mentions(v.A) {
			return "", 0, false
		}
		switch v.Op {
		case ir.Neg:
			return "sub " + regName(v.Dst, colors) + " 0 " + val(v.A), consume, true
		case ir.BitNot:
			return "not " + regName(v.Dst, colors) + " " + val(v.A), consume, true
		case ir.Seqz:
			return "seqz " + regName(v.Dst, colors) + " " + val(v.A), consume, true
		}
	case *ir.Select:
		if !mentions(v.Cond, v.Then, v.Else) {
			return "", 0, false
		}
		return "select " + regName(v.Dst, colors) + " " + val(v.Cond) + " " + val(v.Then) +
			" " + val(v.Else), consume, true
	case *ir.Cmp:
		if !mentions(v.A, v.B) {
			return "", 0, false
		}
		return cmpTextFolded(v.Cond, v.Dst, v.A, v.B, loaded, operand, colors), consume, true
	case *ir.Store:
		if !mentions(v.Src) {
			return "", 0, false
		}
		return "s " + v.Dev + " " + v.Logic + " " + val(v.Src), consume, true
	case *ir.StoreSlot:
		// DynDev renders DevPtr directly, so only fold when DevPtr is not the
		// loaded register.
		if mentions(v.DevPtr) || !mentions(v.Src, v.Index) {
			return "", 0, false
		}
		return "ss " + dynDev(v.Dev, v.DevPtr, colors) + " " + val(v.Index) + " " +
			v.Logic + " " + val(v.Src), consume, true
	case *ir.StoreDyn:
		if v.DevID != nil {
			if !mentions(v.DevID, v.Logic, v.Src) {
				return "", 0, false
			}
			mn := "s"
			if legacyByID {
				mn = "sd"
			}
			return mn + " " + val(v.DevID) + " " + val(v.Logic) + " " + val(v.Src), consume, true
		}
		if mentions(v.DevPtr) || !mentions(v.Logic, v.Src) {
			return "", 0, false
		}
		return "s " + dynDev(v.Dev, v.DevPtr, colors) + " " + val(v.Logic) + " " + val(v.Src), consume, true
	case *ir.Builtin:
		if !mentions(v.Args...) {
			return "", 0, false
		}
		f := builtin.Funcs[v.Name]
		parts := []string{f.Mnemonic}
		if v.Dst != nil {
			parts = append(parts, regName(v.Dst, colors))
		}
		for _, a := range v.Args {
			parts = append(parts, val(a))
		}
		return strings.Join(parts, " "), consume, true
	}
	return "", 0, false
}

// loadOperand finds a single-use load at instrs[i]. The lowerer often copies the
// loaded value to the variable (`t = load(); x = t`), so it also skips one such
// copy and returns the register the consumer actually reads plus that consumer's
// index. The index may be len(instrs), meaning the block terminator.
func loadOperand(instrs []ir.Instr, i int, uses map[*ir.Reg]int, colors map[*ir.Reg]int) (operand string, use *ir.Reg, ci int, ok bool) {
	switch ld := instrs[i].(type) {
	case *ir.LoadIndirect:
		use, operand = ld.Dst, indirectName(ld.Ptr, colors)
	case *ir.LoadSpecial:
		use, operand = ld.Dst, ld.Name
	default:
		return "", nil, 0, false
	}
	if uses[use] != 1 {
		return "", nil, 0, false
	}
	ci = i + 1
	if ci < len(instrs) {
		if cp, isCopy := instrs[ci].(*ir.Assign); isCopy {
			if r, isReg := cp.Src.(*ir.Reg); isReg && r == use && cp.Dst != use && uses[cp.Dst] == 1 {
				use, ci = cp.Dst, ci+1
			}
		}
	}
	return operand, use, ci, true
}

// cmpTextFolded renders a Cmp whose operands may be the folded-in load.
func cmpTextFolded(c ir.Cond, dst *ir.Reg, a, b ir.Value, loaded *ir.Reg, operand string, colors map[*ir.Reg]int) string {
	txt := func(v ir.Value) string {
		if r, ok := v.(*ir.Reg); ok && r == loaded {
			return operand
		}
		return valueText(v, colors)
	}
	if b == nil {
		return cmpMnemonic(c) + " " + regName(dst, colors) + " " + txt(a)
	}
	if isZeroConst(b) {
		if m, ok := zeroCmpMnemonic(c); ok {
			return m + " " + regName(dst, colors) + " " + txt(a)
		}
	}
	return cmpMnemonic(c) + " " + regName(dst, colors) + " " + txt(a) + " " + txt(b)
}

// branchTextFolded renders a branch whose operands may be the folded-in load.
func branchTextFolded(c ir.Cond, a, b ir.Value, loaded *ir.Reg, operand string, colors map[*ir.Reg]int) string {
	txt := func(v ir.Value) string {
		if r, ok := v.(*ir.Reg); ok && r == loaded {
			return operand
		}
		return valueText(v, colors)
	}
	if b == nil {
		return branchMnemonic(c) + " " + txt(a)
	}
	if isZeroConst(b) {
		if m, ok := zeroBranchMnemonic(c); ok {
			return m + " " + txt(a)
		}
	}
	return branchMnemonic(c) + " " + txt(a) + " " + txt(b)
}

// foldLoadTerminator folds a single-use load that is a block's last instruction
// into its branch terminator, so the load line disappears:
// `u = sp; bgtz u L` -> `bgtz sp L`. It mirrors foldLoadOperand for terminators
// (only plain Br; the approximate/valid forms take device operands).
func foldLoadTerminator(instrs []ir.Instr, i int, blk *ir.Block, next *ir.Block, uses map[*ir.Reg]int, colors map[*ir.Reg]int, src int, ni *nanInfo) ([]line, bool) {
	operand, loaded, ci, ok := loadOperand(instrs, i, uses, colors)
	if !ok || ci != len(instrs) {
		return nil, false
	}
	br, ok := blk.Term.(*ir.Br)
	fn := blk.Func
	if !ok {
		return nil, false
	}
	isLoaded := func(v ir.Value) bool {
		r, ok := v.(*ir.Reg)
		return ok && r == loaded
	}
	if !isLoaded(br.A) && !isLoaded(br.B) {
		return nil, false
	}
	inv := br.Cond.Invertible()
	if !inv && br.Cond != ir.NaN {
		inv = ni == nil || ni.branchInvertible(blk, br)
	}
	mk := func(c ir.Cond, target *ir.Block) line {
		return line{text: branchTextFolded(c, br.A, br.B, loaded, operand, colors) + " ", target: target, fn: fn, src: src, safeInvert: inv}
	}
	switch {
	case !inv:
		// No exact inverse: branch on the condition directly.
		if br.Else == next {
			return []line{mk(br.Cond, br.Then)}, true
		}
		return []line{mk(br.Cond, br.Then), {text: "j ", target: br.Else, fn: fn, src: src}}, true
	case br.Else == next:
		return []line{mk(br.Cond, br.Then)}, true
	case br.Then == next:
		return []line{mk(br.Cond.Invert(), br.Else)}, true
	default:
		return []line{mk(br.Cond, br.Then), {text: "j ", target: br.Else, fn: fn, src: src}}, true
	}
}

// labelLine returns the line a label used as a value stands for: the block's own
// first line when it has one, otherwise the line its unconditional jumps reach.
// A label on a line of its own (or on a block the layout dropped) therefore
// takes the following line, which is what an IC10 label means.
func labelLine(b *ir.Block, start map[*ir.Block]int, n int) (int, bool) {
	seen := map[*ir.Block]bool{}
	for b != nil && !seen[b] {
		seen[b] = true
		if line, ok := start[b]; ok && line < n {
			return line, true
		}
		switch t := b.Term.(type) {
		case *ir.Jmp:
			b = t.Target
		case *ir.Goto:
			b = t.Target
		default:
			return 0, false
		}
	}
	return 0, false
}

// Layout returns the codegen block order and each block's 0-based start line.
func Layout(fn *ir.Function, colors map[*ir.Reg]int) ([]*ir.Block, map[*ir.Block]int) {
	blocks, _, start := layoutLines(fn, colors, false, false, false)
	return blocks, start
}

// GenerateReportWithOptions is GenerateReport with explicit options.
func GenerateReportWithOptions(fn *ir.Function, colors map[*ir.Reg]int, opts Options) (string, *Report, error) {
	blocks, lines, start := layoutLines(fn, colors, opts.SpillDB, opts.LegacyByID, opts.NaNSafe)

	if err := checkCallLayout(blocks, lines, start); err != nil {
		return "", nil, err
	}

	// Resolve halt branches to a line past the configured program length.
	halt := haltTarget(opts.Limits)
	for i := range lines {
		if lines[i].halt {
			lines[i].imm = halt
		}
	}

	var sb strings.Builder
	srcMap := make([]int, len(lines)+1)
	for i, ln := range lines {
		srcMap[i+1] = ln.src
		text, target := ln.text, ""
		if ln.imm != "" {
			target = ln.imm
		} else if ln.target != nil {
			abs := strconv.Itoa(start[ln.target])
			// A relative jump is only used when it is strictly shorter than the
			// absolute form: `br*`/`jr` add an 'r' and a negative offset adds a
			// '-', so for a small program the absolute line number is often
			// shorter. Picking per jump keeps --rel-jump from growing the code.
			if opts.RelJump {
				if rt, off, ok := relativeJump(ln.text, start[ln.target]-i); ok &&
					len(rt)+len(off) < len(ln.text)+len(abs) {
					text, target = rt, off
				} else {
					target = abs
				}
			} else {
				target = abs
			}
		}
		sb.WriteString(text)
		sb.WriteString(target)
		sb.WriteByte('\n')
	}
	code := sb.String()

	// Resolve label-address placeholders (a label used as a value) to the
	// absolute line number of the label block. A label whose block emits no line
	// of its own (it is empty and only jumps onward, which happens when the
	// label sits on its own or its block was left out of the layout) takes the
	// first line it reaches, exactly as an IC10 label takes the following line.
	for _, b := range fn.Blocks {
		ref := ir.LabelRef(b.ID)
		if !strings.Contains(code, ref) {
			continue
		}
		if line, ok := labelLine(b, start, len(lines)); ok {
			code = strings.ReplaceAll(code, ref, strconv.Itoa(line))
		}
	}
	// A label reference whose block is not in the layout cannot be resolved;
	// emitting the placeholder would write control characters into the program.
	// This is a compiler bug (see docs/backlog.md), so fail loudly.
	unresolved := strings.IndexByte(code, '\x01') >= 0

	report := &Report{Total: len(lines), ByFunc: map[string]int{}, LineMap: srcMap}
	for _, ln := range lines {
		report.ByFunc[ln.fn]++
	}

	if unresolved {
		// A label reference whose block is not in the layout cannot be resolved;
		// emitting the placeholder would write control characters into the
		// program. This is a compiler bug (see docs/backlog.md), so fail loudly.
		return "", report, fmt.Errorf("internal error: unresolved label reference in generated code")
	}
	if err := ValidateWith(code, opts.Limits); err != nil {
		return "", report, err
	}
	return code, report, nil
}

// orderCallReturns moves each Call/BrCall return block to sit immediately after
// its call, which is what makes the IC10 return address (pc+1) point at the
// continuation. A block is the return of at most one call (the lowerer creates
// a fresh block per call), so a single pass over the calls in layout order is
// enough.
func orderCallReturns(blocks []*ir.Block) []*ir.Block {
	index := func(x *ir.Block) int {
		for i, b := range blocks {
			if b == x {
				return i
			}
		}
		return -1
	}
	moveAfter := func(x, y *ir.Block) {
		if x == y {
			return
		}
		xi, yi := index(x), index(y)
		if xi < 0 || yi < 0 || xi == yi+1 {
			return
		}
		blocks = append(blocks[:xi], blocks[xi+1:]...)
		yi = index(y)
		blocks = append(blocks, nil)
		copy(blocks[yi+2:], blocks[yi+1:])
		blocks[yi+1] = x
	}
	var sites [][2]*ir.Block
	for _, b := range blocks {
		switch t := b.Term.(type) {
		case *ir.Call:
			if t.Return != nil {
				sites = append(sites, [2]*ir.Block{b, t.Return})
			}
		case *ir.BrCall:
			if t.Return != nil {
				sites = append(sites, [2]*ir.Block{b, t.Return})
			}
		}
	}
	for _, s := range sites {
		moveAfter(s[1], s[0])
	}
	return blocks
}

// checkCallLayout asserts that a Call/BrCall return block is laid out
// immediately after the call, which is what makes the IC10 return address
// (pc+1) point at the continuation. A violation is a compiler bug.
func checkCallLayout(blocks []*ir.Block, lines []line, start map[*ir.Block]int) error {
	for i, b := range blocks {
		var ret *ir.Block
		switch t := b.Term.(type) {
		case *ir.Call:
			ret = t.Return
		case *ir.BrCall:
			ret = t.Return
		default:
			continue
		}
		if ret == nil {
			continue
		}
		end := len(lines)
		if i+1 < len(blocks) {
			end = start[blocks[i+1]]
		}
		if start[ret] != end {
			return fmt.Errorf("internal error: return block %d of block %d is not laid out after the call", ret.ID, b.ID)
		}
	}
	return nil
}

// simplifyBranches tidies the emitted line list: it folds redundant
// branch/jump pairs and drops branches whose target is the following line,
// repeating until neither applies (one change can enable the other).
func simplifyBranches(lines []line, start map[*ir.Block]int) ([]line, map[*ir.Block]int) {
	for {
		n := len(lines)
		lines, start = removeRedundantJumps(lines, start)
		lines, start = dropNoOpBranches(lines, start)
		if len(lines) == n {
			return lines, start
		}
	}
}

// removeRedundantJumps rewrites "b<cond> ... T" followed by "j J" into the
// inverted branch "b<!cond> ... J" when T is the instruction after the jump.
// Both paths then reach the same line, so the unconditional jump is dead.
func removeRedundantJumps(lines []line, start map[*ir.Block]int) ([]line, map[*ir.Block]int) {
	removed := make([]bool, len(lines))
	// redirect records, for a removed line, where its (removed) jump went, so a
	// branch that targeted the removed jump is remapped to the jump's target
	// rather than to whatever line now follows it.
	redirect := map[int]*ir.Block{}
	for i := 0; i+1 < len(lines); i++ {
		if removed[i] {
			continue
		}
		br, j := lines[i], lines[i+1]
		if br.target == nil || j.target == nil || j.table || !strings.HasPrefix(j.text, "j ") {
			continue
		}
		if !br.safeInvert {
			continue
		}
		inv, ok := invertBranch(br.text)
		if !ok {
			continue
		}
		if start[br.target] != i+2 {
			continue
		}
		lines[i] = line{text: inv, target: j.target, fn: br.fn, safeInvert: true}
		removed[i+1] = true
		redirect[i+1] = j.target
		i++
	}
	return removeLines(lines, start, removed, redirect)
}

// dropNoOpBranches removes a jump or conditional branch whose target is the
// line right after it: taking the branch and falling through reach the same
// line, so the instruction does nothing. Table entries and calls are left
// alone (a jump-table entry is addressed by index, and a call has to run).
func dropNoOpBranches(lines []line, start map[*ir.Block]int) ([]line, map[*ir.Block]int) {
	removed := make([]bool, len(lines))
	any := false
	for i, ln := range lines {
		if ln.target == nil || ln.table || start[ln.target] != i+1 {
			continue
		}
		if _, isBranch := invertBranch(ln.text); !isBranch && !strings.HasPrefix(ln.text, "j ") {
			continue
		}
		removed[i] = true
		any = true
	}
	if !any {
		return lines, start
	}
	return removeLines(lines, start, removed, nil)
}

// removeLines drops the marked lines and remaps the block start lines. A
// removed line with a redirect is followed to its target, so a branch that
// targeted a removed jump still lands where that jump went; a removed line
// without one simply falls through to the next kept line.
func removeLines(lines []line, start map[*ir.Block]int, removed []bool, redirect map[int]*ir.Block) ([]line, map[*ir.Block]int) {
	kept := make([]line, 0, len(lines))
	index := make([]int, len(lines))
	for i, ln := range lines {
		if removed[i] {
			index[i] = -1
			continue
		}
		index[i] = len(kept)
		kept = append(kept, ln)
	}
	preStart := make(map[*ir.Block]int, len(start))
	for b, s := range start {
		preStart[b] = s
	}
	resolve := func(s int) int {
		for n := 0; n <= len(lines); n++ {
			if s < 0 || s >= len(index) {
				return len(kept)
			}
			if index[s] >= 0 {
				return index[s]
			}
			if t, ok := redirect[s]; ok {
				s = preStart[t]
				continue
			}
			s++
		}
		return len(kept)
	}
	for b := range start {
		start[b] = resolve(preStart[b])
	}
	return kept, start
}

var invertedBranch = map[string]string{
	"beq": "bne", "bne": "beq",
	"blt": "bge", "bge": "blt",
	"ble": "bgt", "bgt": "ble",
	"bnez": "beqz", "beqz": "bnez",
}

// invertBranch returns the text with the branch mnemonic negated, or false if
// the text is not a conditional branch.
func invertBranch(text string) (string, bool) {
	sp := strings.IndexByte(text, ' ')
	mnemonic := text
	if sp >= 0 {
		mnemonic = text[:sp]
	}
	inv, ok := invertedBranch[mnemonic]
	if !ok {
		return "", false
	}
	return inv + text[len(mnemonic):], true
}

// Validate checks the code against the default IC10 editor limits.
func Validate(code string) error {
	return ValidateWith(code, DefaultLimits)
}

// ValidateWith checks the code against explicit editor limits. A zero field in
// limits falls back to the default.
func ValidateWith(code string, limits Limits) error {
	limits = limits.Resolve()
	// Trailing newlines are trimmed before upload and are not stored on the
	// chip, so they do not count against the byte limit either.
	trimmed := strings.TrimRight(code, "\r\n")
	if len(trimmed) > limits.Bytes {
		return fmt.Errorf("script is %d bytes, exceeding the %d byte limit (see `ic10c stats` for the budget)", len(trimmed), limits.Bytes)
	}
	if trimmed == "" {
		return nil
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) > limits.Lines {
		return fmt.Errorf("script has %d lines, exceeding the %d line limit (see `ic10c stats`; split the logic, use batch IO, or `ic10c minify` for existing IC10)", len(lines), limits.Lines)
	}
	for i, ln := range lines {
		if len(ln) > limits.LineLen {
			return fmt.Errorf("line %d is %d characters, exceeding the %d character limit", i, len(ln), limits.LineLen)
		}
	}
	return nil
}

func regName(r *ir.Reg, colors map[*ir.Reg]int) string {
	if r == nil {
		return "r0"
	}
	c, ok := colors[r]
	if !ok {
		return "r0"
	}
	return "r" + strconv.Itoa(c)
}

func valueText(v ir.Value, colors map[*ir.Reg]int) string {
	if r, ok := v.(*ir.Reg); ok {
		return regName(r, colors)
	}
	return v.String()
}

// brDev renders a branch's device operand: a compile-time port name (d0..d5 /
// db / const) or a runtime register/id (IC10's `r?|id` device operand).
func brDev(dev string, ptr ir.Value, colors map[*ir.Reg]int) string {
	if ptr == nil {
		return dev
	}
	return valueText(ptr, colors)
}

// successorsFor returns a terminator's successors in layout preference order,
// taking NaN-freeness into account: a branch whose condition cannot be negated
// lays its else edge out as the fall-through, so the codegen can branch on the
// condition directly with one instruction.
func successorsFor(b *ir.Block, ni *nanInfo) []*ir.Block {
	if br, ok := b.Term.(*ir.Br); ok {
		if br.Cond == ir.NaN || !ni.branchInvertible(b, br) {
			return []*ir.Block{br.Then, br.Else}
		}
	}
	return b.Term.Successors()
}

// rpoOrdered is rpo with a NaN-aware successor preference.
func rpoOrdered(fn *ir.Function, ni *nanInfo) []*ir.Block {
	var order []*ir.Block
	visited := map[*ir.Block]bool{}
	var dfs func(*ir.Block)
	dfs = func(b *ir.Block) {
		if b == nil || visited[b] {
			return
		}
		visited[b] = true
		if b.Term != nil {
			for _, s := range successorsFor(b, ni) {
				dfs(s)
			}
		}
		order = append(order, b)
	}
	dfs(fn.Entry)
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	return order
}

// rpo returns the reachable blocks in reverse post-order, which tends to place
// branch targets so that fall-through is possible.
func rpo(fn *ir.Function) []*ir.Block {
	var order []*ir.Block
	visited := map[*ir.Block]bool{}
	var dfs func(b *ir.Block)
	dfs = func(b *ir.Block) {
		if b == nil || visited[b] {
			return
		}
		visited[b] = true
		// Successors are returned in the preferred layout order: visiting them
		// in sequence (then reversing) puts the intended block last, i.e. as the
		// fall-through. Call/BrCall visit the callee first so the return block
		// lands right after the call.
		if b.Term != nil {
			for _, s := range b.Term.Successors() {
				dfs(s)
			}
		}
		order = append(order, b)
	}
	dfs(fn.Entry)
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	return order
}

// InstrText renders one IR instruction as IC10 text. It is used by the
// control-flow graph (ic10c graph) to label blocks.
func InstrText(ins ir.Instr, colors map[*ir.Reg]int) (string, bool) {
	return renderInstr(ins, colors, false)
}

func renderInstr(ins ir.Instr, colors map[*ir.Reg]int, legacyByID bool) (string, bool) {
	switch v := ins.(type) {
	case *ir.Assign:
		if r, ok := v.Src.(*ir.Reg); ok && regName(r, colors) == regName(v.Dst, colors) {
			return "", false
		}
		return "move " + regName(v.Dst, colors) + " " + valueText(v.Src, colors), true
	case *ir.Bin:
		return v.Op.IC10() + " " + regName(v.Dst, colors) + " " +
			valueText(v.A, colors) + " " + valueText(v.B, colors), true
	case *ir.Un:
		switch v.Op {
		case ir.Neg:
			return "sub " + regName(v.Dst, colors) + " 0 " + valueText(v.A, colors), true
		case ir.BitNot:
			return "not " + regName(v.Dst, colors) + " " + valueText(v.A, colors), true
		case ir.Seqz:
			return "seqz " + regName(v.Dst, colors) + " " + valueText(v.A, colors), true
		}
	case *ir.Cmp:
		if v.B == nil {
			return cmpMnemonic(v.Cond) + " " + regName(v.Dst, colors) + " " + valueText(v.A, colors), true
		}
		if isZeroConst(v.B) {
			if m, ok := zeroCmpMnemonic(v.Cond); ok {
				return m + " " + regName(v.Dst, colors) + " " + valueText(v.A, colors), true
			}
		}
		m := cmpMnemonic(v.Cond)
		return m + " " + regName(v.Dst, colors) + " " + valueText(v.A, colors) +
			" " + valueText(v.B, colors), true
	case *ir.Select:
		return "select " + regName(v.Dst, colors) + " " + valueText(v.Cond, colors) +
			" " + valueText(v.Then, colors) + " " + valueText(v.Else, colors), true
	case *ir.Load:
		return "l " + regName(v.Dst, colors) + " " + v.Dev + " " + v.Logic, true
	case *ir.Store:
		return "s " + v.Dev + " " + v.Logic + " " + valueText(v.Src, colors), true
	case *ir.LoadSlot:
		return "ls " + regName(v.Dst, colors) + " " + dynDev(v.Dev, v.DevPtr, colors) + " " +
			valueText(v.Index, colors) + " " + v.Logic, true
	case *ir.LoadDyn:
		if v.Reagent != nil {
			dev := dynDev(v.Dev, v.DevPtr, colors)
			if v.DevID != nil {
				dev = valueText(v.DevID, colors)
			}
			return "lr " + regName(v.Dst, colors) + " " + dev + " " +
				valueText(v.Logic, colors) + " " + valueText(v.Reagent, colors), true
		}
		if v.DevID != nil {
			mn := "l"
			if legacyByID {
				mn = "ld"
			}
			return mn + " " + regName(v.Dst, colors) + " " + valueText(v.DevID, colors) + " " +
				valueText(v.Logic, colors), true
		}
		return "l " + regName(v.Dst, colors) + " " + dynDev(v.Dev, v.DevPtr, colors) + " " +
			valueText(v.Logic, colors), true
	case *ir.StoreDyn:
		if v.DevID != nil {
			mn := "s"
			if legacyByID {
				mn = "sd"
			}
			return mn + " " + valueText(v.DevID, colors) + " " + valueText(v.Logic, colors) + " " +
				valueText(v.Src, colors), true
		}
		return "s " + dynDev(v.Dev, v.DevPtr, colors) + " " + valueText(v.Logic, colors) + " " +
			valueText(v.Src, colors), true
	case *ir.StoreSlot:
		return "ss " + dynDev(v.Dev, v.DevPtr, colors) + " " + valueText(v.Index, colors) + " " +
			v.Logic + " " + valueText(v.Src, colors), true
	case *ir.Builtin:
		return renderBuiltin(v, colors), true
	case *ir.Batch:
		return renderBatch(v, colors), true
	case *ir.LoadSpecial:
		return "move " + regName(v.Dst, colors) + " " + v.Name, true
	case *ir.StoreSpecial:
		return "move " + v.Name + " " + valueText(v.Src, colors), true
	case *ir.LoadIndirect:
		return "move " + regName(v.Dst, colors) + " " + indirectName(v.Ptr, colors), true
	case *ir.StoreIndirect:
		return "move " + indirectName(v.Ptr, colors) + " " + valueText(v.Src, colors), true
	}
	return "", false
}

// indirectName renders the IC10 indirect register operand rrN. A constant
// index addresses the register directly (rN).
func indirectName(ptr ir.Value, colors map[*ir.Reg]int) string {
	if r, ok := ptr.(*ir.Reg); ok {
		return "r" + regName(r, colors)
	}
	if c, ok := ptr.(*ir.Const); ok && c.Raw == "" && c.Special == "" {
		if n := int(c.V); n >= 0 && n < 16 {
			return "r" + strconv.Itoa(n)
		}
	}
	return "r0"
}

// dynDev renders a dynamic-device operand: a register-selected port (IC10 drN)
// when DevPtr is set, otherwise the fixed port name.
func dynDev(dev string, ptr ir.Value, colors map[*ir.Reg]int) string {
	if ptr == nil {
		return dev
	}
	if r, ok := ptr.(*ir.Reg); ok {
		return "d" + regName(r, colors)
	}
	// A constant port index folds to the fixed port name (d0..d5).
	if c, ok := ptr.(*ir.Const); ok && c.Raw == "" && c.Special == "" {
		return "d" + strconv.Itoa(int(c.V))
	}
	return dev
}

func renderBatch(v *ir.Batch, colors map[*ir.Reg]int) string {
	var parts []string
	switch v.Kind {
	case ir.BatchLoad:
		parts = []string{"lb", regName(v.Dst, colors), valueText(v.Device, colors), v.Logic, valueText(v.Mode, colors)}
	case ir.BatchLoadName:
		parts = []string{"lbn", regName(v.Dst, colors), valueText(v.Device, colors), valueText(v.Name, colors), v.Logic, valueText(v.Mode, colors)}
	case ir.BatchLoadSlot:
		parts = []string{"lbs", regName(v.Dst, colors), valueText(v.Device, colors), valueText(v.Slot, colors), v.Logic, valueText(v.Mode, colors)}
	case ir.BatchLoadNameSlot:
		parts = []string{"lbns", regName(v.Dst, colors), valueText(v.Device, colors), valueText(v.Name, colors), valueText(v.Slot, colors), v.Logic, valueText(v.Mode, colors)}
	case ir.BatchStore:
		parts = []string{"sb", valueText(v.Device, colors), v.Logic, valueText(v.Src, colors)}
	case ir.BatchStoreName:
		parts = []string{"sbn", valueText(v.Device, colors), valueText(v.Name, colors), v.Logic, valueText(v.Src, colors)}
	case ir.BatchStoreSlot:
		parts = []string{"sbs", valueText(v.Device, colors), valueText(v.Slot, colors), v.Logic, valueText(v.Src, colors)}
	}
	return strings.Join(parts, " ")
}

func renderBuiltin(v *ir.Builtin, colors map[*ir.Reg]int) string {
	f := builtin.Funcs[v.Name]
	parts := []string{f.Mnemonic}
	if v.Dst != nil {
		parts = append(parts, regName(v.Dst, colors))
	}
	for _, a := range v.Args {
		parts = append(parts, valueText(a, colors))
	}
	return strings.Join(parts, " ")
}

func branchText(c ir.Cond, a, b ir.Value, colors map[*ir.Reg]int) string {
	if b == nil {
		return branchMnemonic(c) + " " + valueText(a, colors)
	}
	if isZeroConst(b) {
		if m, ok := zeroBranchMnemonic(c); ok {
			return m + " " + valueText(a, colors)
		}
	}
	m := branchMnemonic(c)
	return m + " " + valueText(a, colors) + " " + valueText(b, colors)
}

// branchCallText renders a conditional-call branch (IC10 b<cond>al).
func branchCallText(c ir.Cond, a, b ir.Value, colors map[*ir.Reg]int) string {
	if b == nil {
		return branchMnemonic(c) + "al " + valueText(a, colors)
	}
	if isZeroConst(b) {
		if m, ok := zeroBranchMnemonic(c); ok {
			return m + "al " + valueText(a, colors)
		}
	}
	return branchMnemonic(c) + "al " + valueText(a, colors) + " " + valueText(b, colors)
}

// isZeroConst reports whether v is the numeric constant 0 (not nan/raw).
func isZeroConst(v ir.Value) bool {
	c, ok := v.(*ir.Const)
	return ok && c.Special == "" && c.Raw == "" && c.V == 0
}

// approxText renders a bap/bna instruction (a, b, tol) without its target.
func approxText(m string, a, b, tol ir.Value, colors map[*ir.Reg]int) string {
	return m + " " + valueText(a, colors) + " " + valueText(b, colors) + " " + valueText(tol, colors)
}

// approxZeroText renders a bapz/bnaz instruction (a, tol) without its target.
func approxZeroText(m string, a, tol ir.Value, colors map[*ir.Reg]int) string {
	return m + " " + valueText(a, colors) + " " + valueText(tol, colors)
}

// zeroCmpMnemonic maps a comparison with the constant 0 to IC10's single
// operand s*z form (seqz, snez, sltz, slez, sgtz, sgez).
func zeroCmpMnemonic(c ir.Cond) (string, bool) {
	switch c {
	case ir.Eq, ir.Zero:
		return "seqz", true
	case ir.Ne, ir.NonZero:
		return "snez", true
	case ir.Lt:
		return "sltz", true
	case ir.Le:
		return "slez", true
	case ir.Gt:
		return "sgtz", true
	case ir.Ge:
		return "sgez", true
	}
	return "", false
}

// zeroBranchMnemonic maps a comparison with the constant 0 to IC10's single
// operand b*z form (beqz, bnez, bltz, blez, bgtz, bgez).
func zeroBranchMnemonic(c ir.Cond) (string, bool) {
	switch c {
	case ir.Eq, ir.Zero:
		return "beqz", true
	case ir.Ne, ir.NonZero:
		return "bnez", true
	case ir.Lt:
		return "bltz", true
	case ir.Le:
		return "blez", true
	case ir.Gt:
		return "bgtz", true
	case ir.Ge:
		return "bgez", true
	}
	return "", false
}

func cmpMnemonic(c ir.Cond) string {
	switch c {
	case ir.Eq:
		return "seq"
	case ir.Ne:
		return "sne"
	case ir.Lt:
		return "slt"
	case ir.Le:
		return "sle"
	case ir.Gt:
		return "sgt"
	case ir.Ge:
		return "sge"
	case ir.NonZero:
		return "snez"
	case ir.Zero:
		return "seqz"
	case ir.NaN:
		return "snan"
	}
	return "seq"
}

func branchMnemonic(c ir.Cond) string {
	switch c {
	case ir.Eq:
		return "beq"
	case ir.Ne:
		return "bne"
	case ir.Lt:
		return "blt"
	case ir.Le:
		return "ble"
	case ir.Gt:
		return "bgt"
	case ir.Ge:
		return "bge"
	case ir.NonZero:
		return "bnez"
	case ir.Zero:
		return "beqz"
	case ir.NaN:
		return "bnan"
	}
	return "beq"
}
