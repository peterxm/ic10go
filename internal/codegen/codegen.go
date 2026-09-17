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

// IC10 editor limits.
const (
	MaxLines   = 128
	MaxBytes   = 4096
	MaxLineLen = 90
)

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
	// imm, when set, is written instead of the target block's line number
	// (used to branch straight to 9999 to halt).
	imm string
	fn  string // source function this line was emitted for ("" = main)
}

// isHaltBlock reports whether a block only halts by jumping past the program
// (a bare `jump(9999)`). Such a block emits no line; branches to it target 9999
// directly.
func isHaltBlock(b *ir.Block) bool {
	if b == nil || len(b.Instrs) != 0 {
		return false
	}
	jd, ok := b.Term.(*ir.JmpDyn)
	if !ok || jd.Table != nil {
		return false
	}
	c, ok := jd.Target.(*ir.Const)
	return ok && c.Raw == "" && c.Special == "" && c.V == 9999
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
	}
	return false
}

// Report breaks the generated code down by source function.
type Report struct {
	Total  int
	ByFunc map[string]int
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
func layoutLines(fn *ir.Function, colors map[*ir.Reg]int, spillDB bool) ([]*ir.Block, []line, map[*ir.Block]int) {
	blocks := rpo(fn)
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
	var lines []line
	start := map[*ir.Block]int{}
	add := func(text string, target *ir.Block, f string) {
		lines = append(lines, line{text: text, target: target, fn: f})
	}

	for i, b := range blocks {
		start[b] = len(lines)
		for _, ins := range b.Instrs {
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
			if text, ok := renderInstr(ins, colors); ok {
				add(text, nil, b.Func)
			}
		}
		var next *ir.Block
		if i+1 < len(blocks) {
			next = blocks[i+1]
		}
		if isHaltBlock(b) {
			// A bare halt emits no line, so branches target 9999 directly. It
			// still needs a line when control can fall into it (the previous
			// block falls through, or a call returns here).
			if !retBlocks[b] && (i == 0 || !fallsThroughTo(blocks[i-1], b)) {
				continue
			}
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
					add("j ", tb, b.Func)
				}
			} else {
				add("j "+valueText(t.Target, colors), nil, b.Func)
			}
		case *ir.BrValid:
			m := "bdnvl"
			if t.Store {
				m = "bdnvs"
			}
			add(m+" "+t.Dev+" "+t.Logic+" ", t.Invalid, b.Func)
			if t.Valid != next {
				add("j ", t.Valid, b.Func)
			}
		case *ir.Br:
			thenNext := t.Then == next
			elseNext := t.Else == next
			switch {
			case elseNext:
				add(branchText(t.Cond, t.A, t.B, colors)+" ", t.Then, b.Func)
			case thenNext:
				add(branchText(t.Cond.Invert(), t.A, t.B, colors)+" ", t.Else, b.Func)
			default:
				add(branchText(t.Cond, t.A, t.B, colors)+" ", t.Then, b.Func)
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
			add(branchCallText(t.Cond, t.A, t.B, colors)+" ", t.Target, b.Func)
		}
	}

	// Branches to a halt block target 9999 directly.
	for i := range lines {
		if lines[i].target != nil && isHaltBlock(lines[i].target) {
			lines[i].imm = "9999"
			lines[i].target = nil
		}
	}

	// A branch to a block that produced no line would resolve past the end.
	needNop := false
	for _, ln := range lines {
		if ln.target != nil && start[ln.target] >= len(lines) {
			needNop = true
			break
		}
	}
	if needNop {
		add("move r0 r0", nil, "")
	}

	lines, start = removeRedundantJumps(lines, start)
	return blocks, lines, start
}

// Layout returns the codegen block order and each block's 0-based start line.
func Layout(fn *ir.Function, colors map[*ir.Reg]int) ([]*ir.Block, map[*ir.Block]int) {
	blocks, _, start := layoutLines(fn, colors, false)
	return blocks, start
}

// GenerateReportWithOptions is GenerateReport with explicit options.
func GenerateReportWithOptions(fn *ir.Function, colors map[*ir.Reg]int, opts Options) (string, *Report, error) {
	blocks, lines, start := layoutLines(fn, colors, opts.SpillDB)

	if err := checkCallLayout(blocks, lines, start); err != nil {
		return "", nil, err
	}

	var sb strings.Builder
	for i, ln := range lines {
		text, target := ln.text, ""
		if ln.imm != "" {
			target = ln.imm
		} else if ln.target != nil {
			if opts.RelJump {
				if rt, off, ok := relativeJump(ln.text, start[ln.target]-i); ok {
					text, target = rt, off
				} else {
					target = strconv.Itoa(start[ln.target])
				}
			} else {
				target = strconv.Itoa(start[ln.target])
			}
		}
		sb.WriteString(text)
		sb.WriteString(target)
		sb.WriteByte('\n')
	}
	code := sb.String()

	// Resolve label-address placeholders (a label used as a value) to the
	// absolute line number of the label block.
	for _, b := range blocks {
		if ref := ir.LabelRef(b.ID); strings.Contains(code, ref) {
			code = strings.ReplaceAll(code, ref, strconv.Itoa(start[b]))
		}
	}

	report := &Report{Total: len(lines), ByFunc: map[string]int{}}
	for _, ln := range lines {
		report.ByFunc[ln.fn]++
	}

	if err := Validate(code); err != nil {
		return "", report, err
	}
	return code, report, nil
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

// removeRedundantJumps rewrites "b<cond> ... T" followed by "j J" into the
// inverted branch "b<!cond> ... J" when T is the instruction after the jump.
// Both paths then reach the same line, so the unconditional jump is dead.
func removeRedundantJumps(lines []line, start map[*ir.Block]int) ([]line, map[*ir.Block]int) {
	removed := make([]bool, len(lines))
	for i := 0; i+1 < len(lines); i++ {
		if removed[i] {
			continue
		}
		br, j := lines[i], lines[i+1]
		if br.target == nil || j.target == nil || !strings.HasPrefix(j.text, "j ") {
			continue
		}
		inv, ok := invertBranch(br.text)
		if !ok {
			continue
		}
		if start[br.target] != i+2 {
			continue
		}
		lines[i] = line{text: inv, target: j.target, fn: br.fn}
		removed[i+1] = true
		i++
	}
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
	for b, s := range start {
		ns := s
		for ns < len(index) && index[ns] < 0 {
			ns++
		}
		if ns < len(index) {
			start[b] = index[ns]
		} else {
			start[b] = len(kept)
		}
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

// Validate checks the IC10 editor limits.
func Validate(code string) error {
	if len(code) > MaxBytes {
		return fmt.Errorf("script is %d bytes, exceeding the %d byte limit (see `ic10c stats` for the budget)", len(code), MaxBytes)
	}
	trimmed := strings.TrimSuffix(code, "\n")
	if trimmed == "" {
		return nil
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) > MaxLines {
		return fmt.Errorf("script has %d lines, exceeding the %d line limit (see `ic10c stats`; split the logic, use batch IO, or `ic10c minify` for existing IC10)", len(lines), MaxLines)
	}
	for i, ln := range lines {
		if len(ln) > MaxLineLen {
			return fmt.Errorf("line %d is %d characters, exceeding the %d character limit", i, len(ln), MaxLineLen)
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
	return renderInstr(ins, colors)
}

func renderInstr(ins ir.Instr, colors map[*ir.Reg]int) (string, bool) {
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
			return "ld " + regName(v.Dst, colors) + " " + valueText(v.DevID, colors) + " " +
				valueText(v.Logic, colors), true
		}
		return "l " + regName(v.Dst, colors) + " " + dynDev(v.Dev, v.DevPtr, colors) + " " +
			valueText(v.Logic, colors), true
	case *ir.StoreDyn:
		if v.DevID != nil {
			return "sd " + valueText(v.DevID, colors) + " " + valueText(v.Logic, colors) + " " +
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
	}
	return "beq"
}
