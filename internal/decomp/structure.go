package decomp

import (
	"fmt"
	"strings"

	"ic10go/internal/ic10asm"
)

// sBlock is a basic block of IC10 instructions.
type sBlock struct {
	index      int
	entryLine  int
	labels     []string
	labelLines []int
	insns      []icLine
	term       *icLine // nil means fall through
}

// sLoop describes a natural loop [header, end].
type sLoop struct {
	header int
	end    int
}

type structurer struct {
	d             *decompiler
	blocks        []*sBlock
	lineBlk       map[int]int
	loops         map[int]*sLoop
	out           strings.Builder
	indent        int
	emittedLabels map[string]bool
}

// structure rewrites the flat instruction list into structured .icg source.
func (d *decompiler) structure(lines []icLine) string {
	st := &structurer{d: d, lineBlk: map[int]int{}, loops: map[int]*sLoop{}, indent: 1, emittedLabels: map[string]bool{}}
	st.buildBlocks(lines)
	st.findLoops()
	st.emitRegion(0, len(st.blocks), -1)
	return st.out.String()
}

func isTransferOp(op string) bool {
	switch op {
	case "j", "jal", "jr", "ret":
		return true
	}
	_, _, _, ok := ic10asm.BranchInfo(op)
	return ok
}

func (s *structurer) buildBlocks(lines []icLine) {
	var blocks []*sBlock
	cur := &sBlock{entryLine: -1}
	flush := func() {
		if len(cur.labels) > 0 || len(cur.insns) > 0 || cur.term != nil {
			cur.index = len(blocks)
			blocks = append(blocks, cur)
			cur = &sBlock{entryLine: -1}
		}
	}
	for _, l := range lines {
		if l.op == "" { // label line
			if len(cur.insns) > 0 || cur.term != nil {
				flush()
			}
			if cur.entryLine < 0 {
				cur.entryLine = l.num
			}
			cur.labels = append(cur.labels, s.d.labelsAt[l.num]...)
			cur.labelLines = append(cur.labelLines, l.num)
			continue
		}
		if cur.term != nil {
			flush()
		}
		if cur.entryLine < 0 {
			cur.entryLine = l.num
		}
		if isTransferOp(l.op) {
			lc := l
			cur.term = &lc
			flush()
			continue
		}
		cur.insns = append(cur.insns, l)
	}
	flush()
	s.blocks = blocks
	for bi, b := range s.blocks {
		for _, l := range b.insns {
			s.lineBlk[l.num] = bi
		}
		for _, ln := range b.labelLines {
			s.lineBlk[ln] = bi
		}
	}
}

func (s *structurer) labelIndex(term icLine) (int, bool) {
	t, ok := s.d.jumpTarget(term)
	if !ok {
		return 0, false
	}
	bi, ok := s.lineBlk[t]
	if !ok || bi < 0 {
		return 0, false
	}
	return bi, true
}

func (s *structurer) findLoops() {
	// A back edge is a terminator in block j targeting block i with i <= j.
	// The loop spans [i, maxLatch].
	for j, b := range s.blocks {
		if b.term == nil {
			continue
		}
		if t, ok := s.labelIndex(*b.term); ok && t <= j {
			if lp, exists := s.loops[t]; !exists || j > lp.end {
				s.loops[t] = &sLoop{header: t, end: j}
			}
		}
	}
}

func (s *structurer) line(text string) {
	for i := 0; i < s.indent; i++ {
		s.out.WriteString("    ")
	}
	s.out.WriteString(text)
	s.out.WriteByte('\n')
}

// emitTranslated falls back to the flat translation of a terminator.
func (s *structurer) emitTranslated(term icLine) {
	for _, stmt := range s.d.translate(term) {
		s.line(stmt)
	}
}

func (s *structurer) emitLabels(b *sBlock) {
	emitted := map[string]bool{}
	emit := func(n string) {
		if n == "" || emitted[n] || s.emittedLabels[n] {
			return
		}
		emitted[n] = true
		s.emittedLabels[n] = true
		s.line("label " + n + ":")
	}
	for _, n := range b.labels {
		emit(n)
	}
	if b.entryLine >= 0 {
		if name, ok := s.d.labelAt[b.entryLine]; ok {
			emit(name)
		}
	}
}

// emitRegion structures blocks [start, end). suppress is a block index whose
// fall-through jump should be omitted, or -1.
func (s *structurer) emitRegion(start, end, suppress int) {
	i := start
	for i < end {
		if lp, ok := s.loops[i]; ok && lp.end < end {
			s.emitLoop(lp)
			i = lp.end + 1
			continue
		}
		i = s.emitBlock(i, end, suppress)
	}
}

func (s *structurer) emitBlock(i, end, suppress int) int {
	b := s.blocks[i]
	s.emitLabels(b)
	for _, ins := range b.insns {
		for _, stmt := range s.d.translate(ins) {
			s.line(stmt)
		}
	}
	if b.term == nil {
		return i + 1
	}
	term := *b.term
	switch term.op {
	case "j":
		if len(term.args) > 0 && s.d.resolve(term.args[0]) == "ra" {
			s.line("ret")
			return i + 1
		}
		t, ok := s.labelIndex(term)
		if !ok {
			s.emitTranslated(term)
			return i + 1
		}
		if t == i+1 || t == suppress {
			return i + 1
		}
		s.line("goto " + s.target(term))
		return i + 1
	case "jal":
		if _, ok := s.labelIndex(term); !ok {
			s.emitTranslated(term)
			return i + 1
		}
		s.line("call " + s.target(term))
		return i + 1
	case "jr":
		if _, ok := s.labelIndex(term); !ok {
			s.emitTranslated(term)
			return i + 1
		}
		s.line("goto " + s.target(term))
		return i + 1
	case "ret":
		s.line("ret")
		return i + 1
	}

	cond, _, withRA, ok := ic10asm.BranchInfo(term.op)
	if !ok {
		return i + 1
	}
	t, hasTarget := s.labelIndex(term)
	if !hasTarget {
		s.emitTranslated(term)
		return i + 1
	}
	if withRA {
		if expr, ok := s.d.branchExpr(cond, term); ok {
			s.line(fmt.Sprintf("if %s { call %s }", expr, s.target(term)))
		} else {
			s.line("call " + s.target(term))
		}
		return i + 1
	}
	if t > i && t <= end {
		return s.emitIf(i, t, end, term, cond)
	}
	// Backward branch that is not a recognised loop header: keep a goto.
	if expr, ok := s.d.branchExpr(cond, term); ok {
		s.line(fmt.Sprintf("if %s { goto %s }", expr, s.target(term)))
	}
	return i + 1
}

func (s *structurer) emitIf(i, t, end int, term icLine, cond string) int {
	inv, ok := invertCond(cond)
	if !ok {
		if expr, ok := s.d.branchExpr(cond, term); ok {
			s.line(fmt.Sprintf("if %s { goto %s }", expr, s.target(term)))
		}
		return i + 1
	}
	invExpr, ok := s.d.branchExpr(inv, term)
	if !ok {
		if expr, ok := s.d.branchExpr(cond, term); ok {
			s.line(fmt.Sprintf("if %s { goto %s }", expr, s.target(term)))
		}
		return i + 1
	}

	// Else detection: the last block before the target jumps past it.
	elseStart := -1
	merge := t
	if t-1 >= i+1 {
		last := s.blocks[t-1]
		if last.term != nil && last.term.op == "j" {
			if m, ok := s.labelIndex(*last.term); ok && m > t {
				elseStart = t
				merge = m
			}
		}
	}

	s.line(fmt.Sprintf("if %s {", invExpr))
	s.indent++
	thenSuppress := -1
	if elseStart >= 0 {
		thenSuppress = merge
	}
	s.emitRegion(i+1, t, thenSuppress)
	s.indent--
	if elseStart >= 0 {
		s.line("} else {")
		s.indent++
		s.emitRegion(elseStart, merge, -1)
		s.indent--
	}
	s.line("}")
	return merge
}

func (s *structurer) emitLoop(lp *sLoop) {
	// Recognise a loop whose header tests the exit condition first: the header
	// branch leaves the loop and there are no header instructions before it.
	header := s.blocks[lp.header]
	if header.term != nil && len(header.insns) == 0 {
		if cond, _, withRA, ok := ic10asm.BranchInfo(header.term.op); ok && !withRA {
			if t, ok := s.labelIndex(*header.term); ok && t > lp.end {
				// while-style loop: for cond { ... }
				if expr, ok := s.d.branchExpr(cond, *header.term); ok {
					s.line(fmt.Sprintf("for %s {", expr))
					s.indent++
					s.emitLabels(header)
					s.emitRegion(lp.header+1, lp.end+1, -1)
					s.indent--
					s.line("}")
					return
				}
			}
		}
	}
	// General loop: for { ... }
	s.line("for {")
	s.indent++
	s.emitLoopBody(lp)
	s.indent--
	s.line("}")
}

func (s *structurer) emitLoopBody(lp *sLoop) {
	for i := lp.header; i <= lp.end; i++ {
		b := s.blocks[i]
		// The latch may jump back to the header: turn it into a break.
		if b.term != nil {
			if t, ok := s.labelIndex(*b.term); ok && t == lp.header {
				s.emitLabels(b)
				for _, ins := range b.insns {
					for _, stmt := range s.d.translate(ins) {
						s.line(stmt)
					}
				}
				if cond, _, _, ok := ic10asm.BranchInfo(b.term.op); ok {
					if inv, ok := invertCond(cond); ok {
						if expr, ok := s.d.branchExpr(inv, *b.term); ok {
							s.line(fmt.Sprintf("if %s { break }", expr))
						}
					}
				}
				continue
			}
		}
		// A branch to the loop exit becomes a break.
		if b.term != nil {
			if cond, _, _, ok := ic10asm.BranchInfo(b.term.op); ok {
				if t, ok := s.labelIndex(*b.term); ok && t == lp.end+1 {
					s.emitLabels(b)
					for _, ins := range b.insns {
						for _, stmt := range s.d.translate(ins) {
							s.line(stmt)
						}
					}
					if expr, ok := s.d.branchExpr(cond, *b.term); ok {
						s.line(fmt.Sprintf("if %s { break }", expr))
					}
					continue
				}
			}
		}
		s.emitBlock(i, lp.end+1, -1)
	}
}

func (s *structurer) target(term icLine) string {
	if t, ok := s.d.jumpTarget(term); ok {
		return s.d.labelOf(t)
	}
	return "?"
}

var inverseCond = map[string]string{
	"eq": "ne", "ne": "eq", "lt": "ge", "ge": "lt", "le": "gt", "gt": "le",
	"eqz": "nez", "nez": "eqz", "ltz": "gez", "gez": "ltz",
	"lez": "gtz", "gtz": "lez", "ap": "na", "na": "ap", "apz": "naz", "naz": "apz",
	"dns": "dse", "dse": "dns",
}

func invertCond(cond string) (string, bool) {
	c, ok := inverseCond[cond]
	return c, ok
}
