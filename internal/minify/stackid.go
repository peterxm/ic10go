package minify

import (
	"strconv"
	"strings"

	"ic10go/internal/ic10asm"
)

// StackFold rewrites the "device ReferenceId kept in the host stack" idiom into
// literal ids, so a hand-written program that loads its devices with
//
//	move sp 6            ; the id of device N sits at stack[N-1] (pop reads sp-1)
//	pop  r9              ; r9 = stack[5]; sp = 5
//	l    r0 r9 TotalMoles
//	add  r10 r10 r0
//	brgt sp 4 -3         ; repeat while sp > 4 (br* targets are relative)
//
// becomes one straight-line copy per slot, with r9 replaced by the id stored
// there, followed by the sp the loop would have left behind. `ids` maps a
// 0-based stack slot to the ReferenceId a one-time loader wrote to it.
//
// It is deliberately conservative: if anything about the shape is not exactly as
// expected — a dynamic jump, the device register redefined inside the loop, a
// branch into the region, a missing id, sp that cannot be tracked — it returns
// the input unchanged rather than risk changing behaviour. minify's promise is
// "fewer lines, same meaning".
//
// Branch targets in the output are absolute (br*/jr are relative and the fold
// changes the line count), so the result is fed to minify/decompile as ordinary
// IC10.
func StackFold(src string, ids map[int]int) (string, error) {
	if len(ids) == 0 {
		return src, nil
	}
	raw := strings.Split(src, "\n")
	items, labels, defs := parse(raw)
	for i := range items {
		if items[i].kind == kInstr {
			items[i].toks = expand(items[i].toks, defs, labels)
			items[i].tgt = targetPos(items[i].toks)
			if items[i].tgt < 0 {
				// targetPos ignores br*/jr because they are relative; the fold
				// still needs their target, which is the last operand.
				if idx, ok := relativeTargetPos(items[i].toks); ok {
					items[i].tgt = idx
				}
			}
		}
	}
	idxOf := make(map[int]int, len(items)) // source line -> item index
	for i := range items {
		idxOf[items[i].orig] = i
	}

	// Resolve every branch to its target item. Anything dynamic bails out.
	tgtItem := make(map[int]int, len(items))
	relative := make(map[int]bool, len(items))
	for i := range items {
		it := &items[i]
		if it.kind != kInstr || it.tgt < 0 {
			continue
		}
		// branchTarget treats a relative operand as an absolute line, so decide
		// the form from the opcode first.
		var line int
		if isRelativeBranch(strings.ToLower(it.toks[0])) {
			n, err := strconv.Atoi(it.toks[it.tgt])
			if err != nil {
				return src, nil // dynamic relative target
			}
			line = it.orig + n
			relative[i] = true
		} else {
			tok := it.toks[it.tgt]
			if l, ok := labels[tok]; ok {
				line = l
			} else if n, err := strconv.Atoi(tok); err == nil {
				line = n
			} else {
				return src, nil // dynamic absolute target ("j ra", "j r0")
			}
		}
		j, ok := idxOf[line]
		if !ok {
			return src, nil // target is not an instruction line
		}
		for j < len(items) && items[j].kind != kInstr {
			j++ // a label (or define) is transparent: aim at its first instruction
		}
		if j >= len(items) {
			return src, nil
		}
		tgtItem[i] = j
	}
	if len(tgtItem) == 0 {
		return src, nil
	}

	type branchFix struct {
		outIdx int // index into lines
		target int // item index
	}

	// Phase 1: plan the output, folding loops as they are found.
	var lines []string
	var fixes []branchFix
	lineOf := make(map[int]int, len(items))
	consumed := make(map[int]bool)
	sp, spKnown := 0, true
	folded := false

	for i := 0; i < len(items); i++ {
		it := &items[i]
		if it.kind != kInstr {
			continue
		}
		updateSP(it.toks, &sp, &spKnown)

		if isSpLoopLatch(it.toks) {
			if j, ok := tgtItem[i]; ok && j < i && !consumed[j] {
				if body, newSP, ok := foldLoop(items, i, j, sp, spKnown, ids, tgtItem, relative, consumed); ok {
					// The walk already emitted the pop and the body: roll back to
					// the pop's output line before emitting the unrolled copies.
					if start, ok := lineOf[j]; ok {
						lines = lines[:start]
						for len(fixes) > 0 && fixes[len(fixes)-1].outIdx >= start {
							fixes = fixes[:len(fixes)-1]
						}
					}
					lines = append(lines, body...)
					for k := j; k <= i; k++ {
						consumed[k] = true
						lineOf[k] = len(lines) - len(body)
					}
					sp, spKnown, folded = newSP, true, true
					continue
				}
			}
		}

		lineOf[i] = len(lines)
		lines = append(lines, strings.Join(it.toks, " "))
		if t, ok := tgtItem[i]; ok {
			fixes = append(fixes, branchFix{outIdx: len(lines) - 1, target: t})
			if relative[i] {
				lines[len(lines)-1] = absoluteBranch(it.toks)
			}
		}
	}

	// Phase 2: resolve targets now that every output line is known.
	for _, f := range fixes {
		ln, ok := lineOf[f.target]
		if !ok {
			return src, nil // target line no longer exists
		}
		toks := strings.Fields(lines[f.outIdx])
		if len(toks) == 0 {
			return src, nil
		}
		toks[len(toks)-1] = strconv.Itoa(ln)
		lines[f.outIdx] = strings.Join(toks, " ")
	}

	if !folded {
		// Nothing matched: leave the program byte-for-byte alone (making br*
		// absolute would be safe but is still a needless change).
		return src, nil
	}
	lines = dropDeadSpMoves(lines)
	if len(lines) == 0 {
		return "", nil
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// dropDeadSpMoves removes a `move sp X` that is overwritten by a later `move sp`
// before anything reads sp — the fold leaves one behind for every group.
func dropDeadSpMoves(lines []string) []string {
	dead := make(map[int]bool)
	for i, ln := range lines {
		toks := strings.Fields(ln)
		if len(toks) != 3 || !strings.EqualFold(toks[0], "move") || !strings.EqualFold(toks[1], "sp") {
			continue
		}
		for k := i + 1; k < len(lines); k++ {
			t := strings.Fields(lines[k])
			if len(t) == 0 || !mentionsSP(t) {
				continue
			}
			if len(t) == 3 && strings.EqualFold(t[0], "move") && strings.EqualFold(t[1], "sp") {
				dead[i] = true // overwritten before any read
			}
			break
		}
	}
	if len(dead) == 0 {
		return lines
	}
	out := lines[:0:0]
	for i, ln := range lines {
		if !dead[i] {
			out = append(out, ln)
		}
	}
	return out
}

// mentionsSP reports whether an instruction's operands include sp.
func mentionsSP(toks []string) bool {
	for _, t := range toks[1:] {
		if strings.EqualFold(t, "sp") {
			return true
		}
	}
	return false
}

// foldLoop tries to unroll a `pop rM ... brgt sp J <pop>` loop. It returns the
// replacement lines and the sp the loop leaves behind.
func foldLoop(items []item, br, pop, sp int, spKnown bool, ids map[int]int,
	tgtItem map[int]int, relative map[int]bool, consumed map[int]bool) ([]string, int, bool) {
	if !spKnown || pop < 0 || pop >= br {
		return nil, 0, false
	}
	popIt := items[pop]
	if popIt.kind != kInstr || strings.ToLower(popIt.toks[0]) != "pop" || len(popIt.toks) != 2 {
		return nil, 0, false
	}
	dev := popIt.toks[1]
	if !isReg(dev) {
		return nil, 0, false
	}
	// The latch is `br<cond> sp <bound> <target>`, or the unary `brgtz sp
	// <target>` (bound 0) the last group of a chain uses.
	btoks := items[br].toks
	var j int
	switch {
	case len(btoks) == 4:
		n, err := strconv.Atoi(btoks[2])
		if err != nil {
			return nil, 0, false
		}
		j = n
	case len(btoks) == 3 && strings.EqualFold(btoks[0], "brgtz"):
		j = 0
	default:
		return nil, 0, false
	}
	if sp < j {
		return nil, 0, false
	}
	// The latch's sp is already one below the last pop, so the loop reads slots
	// j, j+1, … sp (pop reads stack[sp-1]).
	for s := j; s <= sp; s++ {
		if _, ok := ids[s]; !ok {
			return nil, 0, false
		}
	}
	for k := pop; k <= br; k++ {
		if consumed[k] {
			return nil, 0, false
		}
	}
	// Branch shapes the unroll can keep correct:
	//   · nothing outside may branch into the region (its pops are gone);
	//   · an inner branch must be relative — the body is copied verbatim, so its
	//     offset still means the same thing inside each copy — and must stay
	//     inside the region and off the pop (which disappears; a branch to the
	//     latch, e.g. a nested if skipping to the loop check, lands just past
	//     the copy, which is where the unrolled code continues).
	for k, t := range tgtItem {
		if k == br {
			continue // the latch itself
		}
		kIn := k >= pop && k <= br
		tIn := t >= pop && t <= br
		if !kIn && tIn {
			return nil, 0, false
		}
		if kIn && (!relative[k] || t == pop || !tIn) {
			return nil, 0, false
		}
	}
	// The body may neither redefine the device register nor move sp.
	for k := pop + 1; k <= br; k++ {
		it := items[k]
		if it.kind != kInstr {
			continue
		}
		op := strings.ToLower(it.toks[0])
		if op == "pop" || op == "push" {
			return nil, 0, false
		}
		if writesDest(it.toks, dev) {
			return nil, 0, false
		}
	}
	var out []string
	for s := sp; s >= j; s-- {
		id := strconv.Itoa(ids[s])
		for k := pop + 1; k < br; k++ {
			it := items[k]
			if it.kind != kInstr {
				continue
			}
			out = append(out, substituteDev(it.toks, dev, id))
		}
	}
	// Keep the sp the loop would have left: later code (another group's pop, or
	// a brgtz sp) still reads it.
	out = append(out, "move sp "+strconv.Itoa(j))
	return out, j, true
}

// updateSP advances the constant sp tracker through one instruction.
func updateSP(toks []string, sp *int, known *bool) {
	if len(toks) == 0 {
		return
	}
	switch strings.ToLower(toks[0]) {
	case "move":
		if len(toks) == 3 && strings.EqualFold(toks[1], "sp") {
			if n, err := strconv.Atoi(toks[2]); err == nil {
				*sp, *known = n, true
			} else {
				*known = false
			}
		}
	case "pop":
		if *known {
			*sp--
		}
	case "push":
		if *known {
			*sp++
		}
	case "j":
		// An unconditional jump makes the fall-through's sp path-dependent.
		if len(toks) == 2 {
			if _, err := strconv.Atoi(toks[1]); err == nil {
				*known = false
			}
		}
	}
}

// isRelativeBranch reports whether the mnemonic is a relative branch (br*/jr).
func isRelativeBranch(op string) bool {
	if op == "jr" {
		return true
	}
	_, relative, _, ok := ic10asm.BranchInfo(op)
	return ok && relative
}

// relativeTargetPos returns the operand index of a relative branch's target
// (br*/jr carry the offset as their last operand).
func relativeTargetPos(toks []string) (int, bool) {
	if len(toks) < 2 {
		return 0, false
	}
	op := strings.ToLower(toks[0])
	if op == "jr" {
		return len(toks) - 1, true
	}
	if _, relative, _, ok := ic10asm.BranchInfo(op); ok && relative {
		return len(toks) - 1, true
	}
	return 0, false
}

// isSpLoopLatch reports whether the instruction is a conditional branch on sp
// (brgt/brlt/... with sp as the first operand) — the latch this fold targets.
func isSpLoopLatch(toks []string) bool {
	if len(toks) < 3 {
		return false
	}
	op := strings.ToLower(toks[0])
	if len(op) < 3 || op[:2] != "br" {
		return false
	}
	return strings.EqualFold(toks[1], "sp")
}

// lastInt returns the last token of a branch as an integer, if it is one.
func lastInt(toks []string) (int, bool) {
	if len(toks) == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(toks[len(toks)-1])
	return n, err == nil
}

// writesDest reports whether the instruction defines reg as its destination.
// IC10 puts the destination register in the first operand of the load/alu
// forms; the store and branch forms do not write it.
func writesDest(toks []string, reg string) bool {
	if len(toks) < 2 {
		return false
	}
	op := strings.ToLower(toks[0])
	if _, relative, _, ok := ic10asm.BranchInfo(op); ok {
		_ = relative
		return false
	}
	switch op {
	case "s", "sb", "sbn", "sbs", "sd", "ss", "put", "clr", "push":
		return false
	}
	return strings.EqualFold(toks[1], reg)
}

// substituteDev replaces the device-operand occurrences of dev with lit.
func substituteDev(toks []string, dev, lit string) string {
	out := append([]string(nil), toks...)
	for i := 1; i < len(out); i++ {
		if strings.EqualFold(out[i], dev) {
			out[i] = lit
		}
	}
	return strings.Join(out, " ")
}

// absoluteBranch rewrites a relative branch (brgt / jr) into its absolute form
// (bgt / j) so the target can be a line number after the fold changed the count.
func absoluteBranch(toks []string) string {
	out := append([]string(nil), toks...)
	op := strings.ToLower(out[0])
	switch {
	case strings.HasPrefix(op, "br"):
		out[0] = "b" + out[0][2:]
	case op == "jr":
		out[0] = "j"
	}
	return strings.Join(out, " ")
}

// isReg reports whether a token names one of the 18 IC10 registers.
func isReg(s string) bool {
	if len(s) < 2 || (s[0] != 'r' && s[0] != 'R') {
		return strings.EqualFold(s, "sp") || strings.EqualFold(s, "ra")
	}
	for _, c := range s[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
