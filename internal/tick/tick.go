// Package tick analyses an IC10 program's per-tick instruction budget.
//
// The game runs a chip for at most 128 instructions per tick and ends the tick
// early at yield/sleep. A loop whose body (between two yields) can execute more
// than 128 instructions therefore spills across ticks, so a "run this loop once
// per tick" design only holds while the worst-case path fits.
//
// Analyze walks the control-flow graph and, for every yield/sleep-delimited
// segment, computes the worst-case number of instructions along any path to the
// next tick boundary. It bounds loops by the budget itself: a path may execute
// at most limit instructions before we stop, so a segment whose worst case
// exceeds the budget is reported as exceeding it without needing an exact trip
// count. Segments that fit are exact. It works on compiled .icg output and on
// hand-written IC10 alike (both parse through internal/vm).
package tick

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"ic10go/internal/ic10asm"
	"ic10go/internal/vm"
)

// DefaultLimit is the game's per-tick instruction budget (ProgrammableChip).
const DefaultLimit = 128

// maxTrackedLoops bounds how many loop back edges carry an iteration count in
// the bounded walk. Loops beyond it simply rely on the budget cap.
const maxTrackedLoops = 8

// maxDPStates caps the bounded walk's state space (the product of the tracked
// loop iteration counts). Additional loops fall back to the budget cap, which
// keeps the analysis linear in the program size.
const maxDPStates = 200000

// Options configures Analyze.
type Options struct {
	// Limit is the per-tick instruction budget (0 = DefaultLimit).
	Limit int
	// LineMap maps a 1-based IC10 line to the 1-based source line it came from
	// (0 = unknown). When set, segments and loops also report their source line,
	// and the worst-case path carries a parallel source-line list.
	LineMap []int
}

// Segment is one yield/sleep-delimited run of the program.
type Segment struct {
	Start    int   `json:"start"`              // 0-based line of the first instruction
	Barrier  int   `json:"barrier"`            // 0-based line of the terminating yield/sleep, or -1
	Cost     int   `json:"cost"`               // worst-case instructions; Limit+1 means "exceeds"
	Exceeds  bool  `json:"exceeds"`            // Cost > Limit (the exact count is unknown)
	Path     []int `json:"path,omitempty"`     // one worst-case path (line numbers)
	Indirect bool  `json:"indirect,omitempty"` // a computed branch target was hit

	// Source* are filled when Options.LineMap is set: the 1-based source line of
	// Start / Barrier / the dominant loop header, and the mapped worst-case path.
	Source         int   `json:"source,omitempty"`
	BarrierSource  int   `json:"barrierSource,omitempty"`
	SourcePath     []int `json:"sourcePath,omitempty"`
	Dominant       int   `json:"dominant,omitempty"`       // 0-based header of the loop that dominates this segment (-1 = none)
	DominantSource int   `json:"dominantSource,omitempty"` // source line of Dominant
	DominantCost   int   `json:"dominantCost,omitempty"`   // estimated instructions from that loop
}

// Loop is a natural loop found in the program (informational).
type Loop struct {
	Header  int  `json:"header"`           // 0-based line of the loop header (branch back target)
	Latch   int  `json:"latch"`            // 0-based line of the branch that jumps back
	Start   int  `json:"start"`            // lowest line in the loop body
	End     int  `json:"end"`              // highest line in the loop body
	Body    int  `json:"body"`             // worst-case (longest) instructions in one iteration
	BodyMin int  `json:"bodyMin"`          // best-case (shortest) instructions in one iteration
	Trips   int  `json:"trips"`            // detected constant trip count, or 0 when unknown
	Spans   bool `json:"spans,omitempty"`  // the loop contains a yield/sleep and so spans ticks
	Source  int  `json:"source,omitempty"` // 1-based source line of Header (with Options.LineMap)
	// SourceStart / SourceEnd are the 1-based source lines of Start / End (with
	// Options.LineMap), for highlighting the loop's range in an editor.
	SourceStart int `json:"sourceStart,omitempty"`
	SourceEnd   int `json:"sourceEnd,omitempty"`
}

// Report is the analysis result.
type Report struct {
	Limit    int       `json:"limit"`
	Lines    int       `json:"lines"`
	Segments []Segment `json:"segments"`
	Loops    []Loop    `json:"loops"`
	Indirect bool      `json:"indirect,omitempty"` // any unresolved computed target was hit
}

type node struct {
	line    int
	op      string
	args    []string
	succ    []int // successors within a tick (barrier/halt nodes have none)
	weight  int   // instruction cost of the node (0 = 1); a summarized call costs 1 + the callee
	barrier bool
	halt    bool
	// dsucc adds the barrier continuation (the instruction after yield/sleep) so
	// dominance and loop detection see the whole program, not a graph cut at
	// every tick boundary. The bounded walk uses succ, not dsucc.
	dsucc    []int
	indirect bool
	// raReturn marks an indirect `j ra` / `jr ra`: its targets are refined from a
	// forward analysis of the possible constant `ra` values, falling back to the
	// conservative set of every call return site.
	raReturn bool
}

// raSet is the set of constant line numbers the `ra` register can hold at a
// program point. unknown means "not a known constant", so the caller falls back
// to the conservative return-site set.
type raSet struct {
	unknown bool
	vals    map[int]bool
}

func raClone(s raSet) raSet {
	if s.unknown {
		return raSet{unknown: true}
	}
	out := raSet{vals: make(map[int]bool, len(s.vals))}
	for v := range s.vals {
		out.vals[v] = true
	}
	return out
}

func raMerge(a, b raSet) raSet {
	if a.unknown || b.unknown {
		return raSet{unknown: true}
	}
	out := raSet{vals: make(map[int]bool, len(a.vals)+len(b.vals))}
	for v := range a.vals {
		out.vals[v] = true
	}
	for v := range b.vals {
		out.vals[v] = true
	}
	return out
}

func raEqual(a, b raSet) bool {
	if a.unknown != b.unknown || len(a.vals) != len(b.vals) {
		return false
	}
	for v := range a.vals {
		if !b.vals[v] {
			return false
		}
	}
	return true
}

// Analyze parses src as IC10 and reports its per-tick budget. limit <= 0 uses
// DefaultLimit.
func Analyze(src string, limit int) (*Report, error) {
	return AnalyzeOpts(src, Options{Limit: limit})
}

// AnalyzeOpts is Analyze with explicit options.
func AnalyzeOpts(src string, opts Options) (*Report, error) {
	if opts.Limit <= 0 {
		opts.Limit = DefaultLimit
	}
	prog, err := vm.Parse(src)
	if err != nil {
		return nil, err
	}
	return analyzeProg(prog, opts)
}

func analyzeProg(prog *vm.Program, opts Options) (*Report, error) {
	limit := opts.Limit
	srcOf := func(line int) int {
		// line is 0-based; LineMap is 1-based.
		if len(opts.LineMap) == 0 || line < 0 {
			return 0
		}
		i := line + 1
		if i >= len(opts.LineMap) {
			return 0
		}
		return opts.LineMap[i]
	}
	instrs := prog.Instrs
	lines := make([]int, 0, len(instrs))
	for i, ins := range instrs {
		if ins != nil {
			lines = append(lines, i)
		}
	}
	if len(lines) == 0 {
		return &Report{Limit: limit, Lines: 0}, nil
	}

	next := func(i int) int {
		for j := i + 1; j < len(instrs); j++ {
			if instrs[j] != nil {
				return j
			}
		}
		return -1
	}

	// norm moves a resolved target onto the next real instruction: a label sits
	// on a blank/comment line of its own, but only instruction lines have nodes.
	norm := func(t int) (int, bool) {
		if t < 0 {
			return 0, false
		}
		for t < len(instrs) && instrs[t] == nil {
			t++
		}
		if t >= len(instrs) {
			return 0, false
		}
		return t, true
	}

	resolveTarget := func(cur int, s string) (int, bool) {
		if l, ok := prog.Labels[s]; ok {
			return norm(l)
		}
		if v, err := strconv.Atoi(s); err == nil {
			return norm(v)
		}
		return 0, false
	}

	// Return sites of call-like jumps (jal / -al) and hand-rolled / tail calls
	// (`move ra <line>`). An indirect `j ra` / `jr` connects to these rather than
	// every line, so outlined functions don't explode the walk.
	var calls []int
	for _, i := range lines {
		ins := instrs[i]
		if ins.Op == "jal" {
			calls = append(calls, next(i))
			continue
		}
		if _, _, withRA, ok := ic10asm.BranchInfo(ins.Op); ok && withRA {
			calls = append(calls, next(i))
			continue
		}
		if ins.Op == "move" && len(ins.Args) == 2 && ins.Args[0] == "ra" {
			if t, ok := resolveTarget(i, ins.Args[1]); ok {
				calls = append(calls, t)
			}
		}
	}

	nodes := make(map[int]*node, len(lines))
	for _, i := range lines {
		ins := instrs[i]
		n := &node{line: i, op: ins.Op, args: ins.Args}
		switch {
		case ins.Op == "yield" || ins.Op == "sleep":
			n.barrier = true // terminal: the tick ends here
		case ins.Op == "hcf":
			n.halt = true
		case ins.Op == "j":
			if t, ok := resolveTarget(i, ins.Args[0]); ok {
				n.succ = []int{t}
			} else if ins.Args[0] == "ra" {
				// `j ra` is an outlined-function return: connect to the call
				// return sites (refined below by the `ra` analysis).
				n.raReturn = true
				n.succ = append([]int(nil), calls...)
				n.indirect = true
			} else {
				n.indirect = true
			}
		case ins.Op == "jal":
			if t, ok := resolveTarget(i, ins.Args[0]); ok {
				n.succ = []int{t}
			} else {
				n.indirect = true
			}
		case ins.Op == "jr":
			if v, err := strconv.Atoi(ins.Args[0]); err == nil {
				if t, ok := norm(i + v); ok {
					n.succ = []int{t}
				} else {
					n.indirect = true
				}
			} else if ins.Args[0] == "ra" {
				n.raReturn = true
				n.succ = append([]int(nil), calls...)
				n.indirect = true
			} else if len(calls) > 0 {
				n.succ = append([]int(nil), calls...)
				n.indirect = true
			} else {
				n.indirect = true
			}
		default:
			if cond, relative, _, ok := ic10asm.BranchInfo(ins.Op); ok {
				ti := ic10asm.TargetIndex(cond)
				var target int
				var tok bool
				if relative {
					if v, err := strconv.Atoi(ins.Args[ti]); err == nil {
						target, tok = norm(i + v)
					}
				} else {
					target, tok = resolveTarget(i, ins.Args[ti])
				}
				nxt := next(i)
				if tok {
					n.succ = []int{target, nxt}
				} else {
					n.indirect = true
					n.succ = []int{nxt}
				}
			} else {
				n.succ = []int{next(i)}
			}
		}
		nodes[i] = n
	}

	// Outlined functions: `jal f` / `j ra` (and the `-al` conditional-call forms)
	// are calls and returns. The `ra` analysis below is context-insensitive, so a
	// callee reached from several sites would have its `j ra` connect to every
	// return site and invent a phantom loop (reported as an unbounded segment).
	// Summarise each callee instead: a call becomes a straight-line step that
	// costs the callee's worst case and returns to the site after the call, so
	// the callee body stays out of the caller's CFG. A callee that loops, yields,
	// or contains a nested call is left as-is (the conservative path).
	callTarget := func(n *node) (int, bool) {
		if n.op == "jal" && len(n.args) == 1 {
			return resolveTarget(n.line, n.args[0])
		}
		if cond, _, withRA, ok := ic10asm.BranchInfo(n.op); ok && withRA {
			if ti := ic10asm.TargetIndex(cond); ti < len(n.args) {
				return resolveTarget(n.line, n.args[ti])
			}
		}
		return 0, false
	}
	// A callee to summarise must be self-contained: it may branch and loop, but
	// must not call another function or jump into one (a tail call would return
	// elsewhere). Calls already rewritten count as straight-line steps, so a
	// nested call is summarised first and its caller retried next round.
	callEntries := map[int]bool{}
	for _, i := range lines {
		if t, ok := callTarget(nodes[i]); ok {
			callEntries[t] = true
		}
	}
	type fnInfo struct {
		cost int
		ok   bool
	}
	fnCache := map[int]fnInfo{}
	summarize := func(entry int) (int, bool) {
		if info, done := fnCache[entry]; done {
			return info.cost, info.ok
		}
		if _, ok := nodes[entry]; !ok {
			return 0, false
		}
		// Build the callee's own CFG: reachable from the entry, stopping at
		// `j ra` (a terminal here, so the walk returns) and bailing on anything
		// that does not return inside one tick (a barrier/halt, a nested call
		// that is not summarised yet, or a tail call into another function).
		sub := map[int]*node{}
		var subLines []int
		stack := []int{entry}
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if _, seen := sub[i]; seen {
				continue
			}
			n, ok := nodes[i]
			if !ok {
				return 0, false
			}
			c := &node{line: n.line, op: n.op, args: n.args, weight: n.weight, indirect: n.indirect}
			if n.raReturn {
				c.halt = true // `j ra`: the callee returns
				sub[i] = c
				continue
			}
			if n.barrier || n.halt || len(n.succ) == 0 {
				return 0, false
			}
			if _, isCall := callTarget(n); isCall && n.weight == 0 {
				return 0, false // nested call, not summarised yet
			}
			if n.op == "j" {
				if t, ok := resolveTarget(i, n.args[0]); ok && callEntries[t] {
					return 0, false // tail call into another function
				}
			}
			c.succ = n.succ
			sub[i] = c
			subLines = append(subLines, i)
			stack = append(stack, n.succ...)
		}
		sort.Ints(subLines)
		for _, i := range subLines {
			sub[i].dsucc = sub[i].succ
		}
		w := newBoundedWalk(sub, subLines, dominators(sub, entry))
		v := w.steps(entry, limit+1, [maxTrackedLoops]byte{})
		fnCache[entry] = fnInfo{cost: v, ok: true}
		return v, true
	}
	// Fixpoint: rewriting an inner call lets its caller be summarised next round.
	for progress := true; progress; {
		progress = false
		tried := map[int]bool{} // callees that failed this round (retried next)
		for _, i := range lines {
			n := nodes[i]
			if n.weight > 0 {
				continue // already rewritten
			}
			t, isCall := callTarget(n)
			if !isCall || tried[t] {
				continue
			}
			cost, ok := summarize(t)
			if !ok {
				tried[t] = true
				continue
			}
			if nxt := next(i); nxt >= 0 {
				n.succ = []int{nxt}
			} else {
				n.succ = nil
			}
			n.weight = 1 + cost
			progress = true
		}
	}

	// Refine `j ra` / `jr ra` returns by propagating the possible constant values
	// of `ra` forward. Connecting every such return to *all* call return sites
	// can invent a loop when a return site sits inside the return's own
	// continuation (the compiled `jal f` / `move ra r` / `j ra` chain): the walk
	// then revisits that site forever. With the actual `ra` value the return goes
	// where it really does and the phantom loop disappears.
	raTransfer := func(n *node, in raSet) raSet {
		switch {
		case n.op == "jal":
			if t := next(n.line); t >= 0 {
				return raSet{vals: map[int]bool{t: true}}
			}
			return raSet{unknown: true}
		case regDestOps[n.op] && len(n.args) > 0 && n.args[0] == "ra":
			if n.op == "move" && len(n.args) == 2 {
				if t, ok := resolveTarget(n.line, n.args[1]); ok {
					return raSet{vals: map[int]bool{t: true}}
				}
			}
			return raSet{unknown: true}
		default:
			if _, _, withRA, ok := ic10asm.BranchInfo(n.op); ok && withRA {
				out := raClone(in)
				if t := next(n.line); t >= 0 {
					if out.vals == nil {
						out.vals = map[int]bool{}
					}
					out.vals[t] = true
				}
				return out
			}
			return in
		}
	}
	raIn := make(map[int]raSet, len(lines))
	for _, i := range lines {
		raIn[i] = raSet{}
	}
	if len(lines) > 0 {
		raIn[lines[0]] = raSet{unknown: true}
	}
	// raSucc is the flow graph for the `ra` analysis: a `j ra` does not propagate
	// `ra` (the return site's `ra` is the caller's), so it has no successors here.
	raSucc := func(i int) []int {
		if nodes[i].raReturn {
			return nil
		}
		var out []int
		for _, s := range nodes[i].succ {
			if _, ok := nodes[s]; ok {
				out = append(out, s)
			}
		}
		return out
	}
	for changed := true; changed; {
		changed = false
		for _, i := range lines {
			out := raTransfer(nodes[i], raIn[i])
			for _, s := range raSucc(i) {
				m := raMerge(raIn[s], out)
				if !raEqual(m, raIn[s]) {
					raIn[s] = m
					changed = true
				}
			}
		}
	}
	for _, i := range lines {
		n := nodes[i]
		if !n.raReturn {
			continue
		}
		out := raTransfer(n, raIn[i])
		if !out.unknown && len(out.vals) > 0 {
			var ts []int
			for v := range out.vals {
				if _, ok := nodes[v]; ok {
					ts = append(ts, v)
				}
			}
			sort.Ints(ts)
			if len(ts) > 0 {
				n.succ = ts
				n.indirect = len(ts) > 1
				continue
			}
		}
		if len(calls) > 0 {
			n.succ = append([]int(nil), calls...)
			n.indirect = true
		}
	}

	// An unresolved jump could go anywhere: make it a conservative edge set so
	// the result stays a sound upper bound. (Reported via Indirect.)
	all := lines
	for _, n := range nodes {
		if n.indirect && len(n.succ) == 0 {
			for _, t := range all {
				if t != n.line {
					n.succ = append(n.succ, t)
				}
			}
		}
	}

	// An outlined body that nothing reaches (every call to it was summarised
	// away) must not contribute edges: a stray `j ra` would otherwise look like
	// a predecessor of its return site and pollute dominance, hiding the real
	// loop. Drop the edges of unreachable nodes.
	reach := map[int]bool{}
	stack := []int{lines[0]}
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if reach[u] {
			continue
		}
		reach[u] = true
		n := nodes[u]
		if n == nil {
			continue
		}
		stack = append(stack, n.succ...)
		if n.barrier {
			if c := next(u); c >= 0 {
				stack = append(stack, c)
			}
		}
	}
	for _, i := range lines {
		if n := nodes[i]; n != nil && !reach[i] {
			n.succ = nil
		}
	}

	// The dominance/loop CFG adds the barrier continuation, so a loop that spans
	// a yield (e.g. `for { yield(); ... }`) is seen whole.
	for _, n := range nodes {
		n.dsucc = n.succ
		if n.barrier {
			if c := next(n.line); c >= 0 && len(n.succ) == 0 {
				n.dsucc = []int{c}
			}
		}
	}

	rep := &Report{Limit: limit, Lines: len(lines)}

	// Segments: start at the entry and after every barrier.
	starts := []int{lines[0]}
	for _, i := range lines {
		if nodes[i].barrier {
			if s := next(i); s >= 0 {
				starts = append(starts, s)
			}
		}
	}
	sort.Ints(starts)
	starts = dedup(starts)

	// Back edges whose trip count we know let the walk stop repeating a loop
	// once it has run its course. Loops without a known trip count are still
	// bounded by the remaining budget, so an unbounded loop is reported as
	// exceeding the budget (its worst case is genuinely unbounded).
	dom := dominators(nodes, lines[0])
	walk := newBoundedWalk(nodes, lines, dom)

	rep.Loops = findLoops(nodes, lines, dom, &rep.Indirect)

	for _, st := range starts {
		if _, ok := nodes[st]; !ok {
			continue
		}
		cost := walk.steps(st, limit+1, [maxTrackedLoops]byte{})
		if cost > limit {
			// The exact count is unknown once it exceeds the budget (the walk
			// stops there); report limit+1, the documented "exceeds" marker.
			cost = limit + 1
		}
		seg := Segment{Start: st, Barrier: -1, Cost: cost}
		seg.Exceeds = cost > limit
		// Reconstruct the worst-case path and find its terminal barrier.
		cur, rem := st, limit+1
		counts := [maxTrackedLoops]byte{}
		for cur >= 0 && rem > 0 {
			seg.Path = append(seg.Path, cur)
			n, ok := nodes[cur]
			if !ok {
				break
			}
			if n.indirect {
				seg.Indirect = true
			}
			if n.barrier {
				seg.Barrier = cur
				break
			}
			if n.halt || len(n.succ) == 0 {
				break
			}
			to, ok := walk.choice[dpKey{cur, rem, counts}]
			if !ok || to < 0 {
				break
			}
			if bi, ok := walk.edgeIdx[[2]int{cur, to}]; ok {
				counts[bi]++
			}
			rem -= nodeCost(n)
			cur = to
		}
		if seg.Indirect {
			rep.Indirect = true
		}
		// Attribute the worst case: the loop that contributes the most along the
		// reconstructed path (visits to its header times its body size).
		visits := map[int]int{}
		for _, ln := range seg.Path {
			visits[ln]++
		}
		seg.Dominant = -1
		for _, lp := range rep.Loops {
			if v := visits[lp.Header]; v > 0 && v*lp.Body > seg.DominantCost {
				seg.Dominant, seg.DominantCost = lp.Header, v*lp.Body
			}
		}
		// Source mapping (for .icg callers that pass a LineMap).
		seg.Source = srcOf(seg.Start)
		if seg.Barrier >= 0 {
			seg.BarrierSource = srcOf(seg.Barrier)
		}
		if seg.Dominant >= 0 {
			seg.DominantSource = srcOf(seg.Dominant)
		}
		if len(opts.LineMap) > 0 && len(seg.Path) > 0 {
			seg.SourcePath = make([]int, len(seg.Path))
			for i, ln := range seg.Path {
				seg.SourcePath[i] = srcOf(ln)
			}
		}
		rep.Segments = append(rep.Segments, seg)
	}

	for i := range rep.Loops {
		l := &rep.Loops[i]
		l.Source = srcOf(l.Header)
		// SourceStart/End: the source-line span of the loop's IC10 range, so an
		// editor can highlight the whole loop. Mapping only Start/End is wrong
		// when the latch (a back jump) maps back to the header's source line.
		for ln := l.Start; ln <= l.End; ln++ {
			s := srcOf(ln)
			if s <= 0 {
				continue
			}
			if l.SourceStart == 0 || s < l.SourceStart {
				l.SourceStart = s
			}
			if s > l.SourceEnd {
				l.SourceEnd = s
			}
		}
	}
	return rep, nil
}

func dedup(xs []int) []int {
	if len(xs) == 0 {
		return xs
	}
	out := xs[:1]
	for _, x := range xs[1:] {
		if x != out[len(out)-1] {
			out = append(out, x)
		}
	}
	return out
}

// findLoops finds natural loops (back edges via dominance) and, for each, the
// worst-case one-iteration body size and, best effort, a constant trip count.
func findLoops(nodes map[int]*node, lines []int, dom map[int]map[int]bool, indirect *bool) []Loop {
	// Group back-edge latches by header.
	byHeader := map[int]map[int]bool{}
	var headers []int
	for _, u := range lines {
		for _, v := range nodes[u].dsucc {
			if _, ok := nodes[v]; !ok {
				continue
			}
			if dom[u][v] { // v dominates u -> back edge u->v
				if byHeader[v] == nil {
					byHeader[v] = map[int]bool{}
				}
				if !byHeader[v][u] {
					byHeader[v][u] = true
					if len(byHeader[v]) == 1 {
						headers = append(headers, v)
					}
				}
			}
		}
	}
	sort.Ints(headers)

	var loops []Loop
	for _, h := range headers {
		set := naturalLoop(nodes, h, byHeader[h])
		l := Loop{Header: h, Trips: 0, Spans: containsBarrier(nodes, set)}
		l.Start, l.End = h, h
		for x := range set {
			if x < l.Start {
				l.Start = x
			}
			if x > l.End {
				l.End = x
			}
		}
		// Latch = the back-edge source (pick the largest line for determinism).
		for u := range byHeader[h] {
			if u > l.Latch {
				l.Latch = u
			}
		}
		l.Body = loopBody(nodes, set, h, byHeader[h])
		l.BodyMin = loopBodyMin(nodes, set, h, byHeader[h])
		l.Trips = detectTrips(nodes, set, h)
		loops = append(loops, l)
	}
	return loops
}

// dpKey is the bounded-walk memo key: a node, the remaining budget and the
// per-loop iteration counts used so far.
type dpKey struct {
	i, rem int
	counts [maxTrackedLoops]byte
}

// boundedWalk is the worst-case path DP over a CFG. From a node with rem
// instructions left it returns the worst-case number of instructions to a
// terminal (barrier/halt/no successor), following each loop's back edge only as
// many times as its detected trip count allows.
type boundedWalk struct {
	nodes    map[int]*node
	edgeTrip map[[2]int]int // how many times a back edge is taken (0 = unknown)
	edgeIdx  map[[2]int]int // tracked loops: index into counts (absent = untracked)
	memo     map[dpKey]int
	choice   map[dpKey]int
}

// newBoundedWalk reads the loop back edges out of dom and prepares the walk.
func newBoundedWalk(nodes map[int]*node, lines []int, dom map[int]map[int]bool) *boundedWalk {
	w := &boundedWalk{
		nodes:    nodes,
		edgeTrip: map[[2]int]int{},
		edgeIdx:  map[[2]int]int{},
		memo:     map[dpKey]int{},
		choice:   map[dpKey]int{},
	}
	type backEdge struct {
		e    [2]int
		trip int // detected trip count (0 = unknown)
		cap  int // how many times this edge itself is taken
	}
	var backs []backEdge
	for _, u := range lines {
		for _, v := range nodes[u].dsucc {
			if _, ok := nodes[v]; !ok {
				continue
			}
			if !dom[u][v] {
				continue
			}
			set := naturalLoop(nodes, v, map[int]bool{u: true})
			t := detectTrips(nodes, set, v)
			// How many times this back edge itself is taken: a latch that is the
			// loop's exit check runs trips-1 times, a plain jump at the bottom
			// runs trips times.
			c := t
			if t > 0 && isConditional(nodes[u].op) {
				for _, s := range nodes[u].dsucc {
					if !set[s] {
						c = t - 1 // exits on this branch
						break
					}
				}
			}
			if c < 0 {
				c = 0
			}
			backs = append(backs, backEdge{e: [2]int{u, v}, trip: t, cap: c})
		}
	}
	// Carry iteration counts only while the walk's state space (the product of
	// the counts) stays small; the rest rely on the budget cap.
	sort.Slice(backs, func(i, j int) bool { return backs[i].cap < backs[j].cap })
	tracked, product := 0, 1
	for _, b := range backs {
		w.edgeTrip[b.e] = b.cap
		if b.trip <= 0 || tracked >= maxTrackedLoops || product*(b.cap+1) > maxDPStates {
			continue
		}
		w.edgeIdx[b.e] = tracked
		tracked++
		product *= b.cap + 1
	}
	return w
}

func (w *boundedWalk) steps(i, rem int, counts [maxTrackedLoops]byte) int {
	n := w.nodes[i]
	if rem <= 0 {
		return 0
	}
	c := nodeCost(n)
	if n.barrier || n.halt || len(n.succ) == 0 {
		return c // the terminal instruction itself counts
	}
	key := dpKey{i, rem, counts}
	if v, ok := w.memo[key]; ok {
		return v
	}
	best, bestTo := 0, -1
	for _, s := range n.succ {
		if _, ok := w.nodes[s]; !ok {
			continue
		}
		e := [2]int{i, s}
		nc := counts // arrays are values: this is already a copy
		if bi, ok := w.edgeIdx[e]; ok {
			if int(counts[bi]) >= w.edgeTrip[e] {
				continue // the loop has run all its iterations
			}
			nc[bi]++
		}
		if v := w.steps(s, rem-c, nc); v > best {
			best, bestTo = v, s
		}
	}
	res := c + best
	w.memo[key] = res
	w.choice[key] = bestTo
	return res
}

// nodeCost is the instruction cost of a node: 1, or the summarized callee cost
// of a rewritten call.
func nodeCost(n *node) int {
	if n == nil {
		return 1
	}
	if n.weight > 0 {
		return n.weight
	}
	return 1
}

// loopBody is the worst-case instructions from the header to a latch along
// edges inside the loop (one iteration, excluding the back edge).
func loopBody(nodes map[int]*node, set map[int]bool, header int, latches map[int]bool) int {
	memo := map[int]int{}
	onPath := map[int]bool{}
	var walk func(i int) int
	walk = func(i int) int {
		if latches[i] {
			return nodeCost(nodes[i])
		}
		if v, ok := memo[i]; ok {
			return v
		}
		if onPath[i] {
			return 0 // irreducible cycle: break it (conservative lower bound)
		}
		onPath[i] = true
		best := 0
		for _, s := range nodes[i].dsucc {
			if !set[s] || s == header {
				continue // don't follow the back edge
			}
			if v := walk(s); v > best {
				best = v
			}
		}
		onPath[i] = false
		memo[i] = nodeCost(nodes[i]) + best
		return nodeCost(nodes[i]) + best
	}
	return walk(header)
}

// loopBodyMin is the best-case (shortest) instructions from the header to a
// latch along edges inside the loop (one iteration, excluding the back edge).
func loopBodyMin(nodes map[int]*node, set map[int]bool, header int, latches map[int]bool) int {
	memo := map[int]int{}
	onPath := map[int]bool{}
	var walk func(i int) int
	walk = func(i int) int {
		if latches[i] {
			return nodeCost(nodes[i])
		}
		if v, ok := memo[i]; ok {
			return v
		}
		if onPath[i] {
			return 0
		}
		onPath[i] = true
		best, seen := 0, false
		for _, s := range nodes[i].dsucc {
			if !set[s] || s == header {
				continue
			}
			if v := walk(s); !seen || v < best {
				best, seen = v, true
			}
		}
		onPath[i] = false
		memo[i] = nodeCost(nodes[i]) + best
		return nodeCost(nodes[i]) + best
	}
	return walk(header)
}

// naturalLoop returns the natural loop of header h closed over the given back
// edges: h plus every node that reaches a latch without passing through h.
func naturalLoop(nodes map[int]*node, h int, latches map[int]bool) map[int]bool {
	set := map[int]bool{h: true}
	stack := make([]int, 0, len(latches))
	for l := range latches {
		if !set[l] {
			set[l] = true
			stack = append(stack, l)
		}
	}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, p := range preds(nodes, n) {
			if p == h || set[p] {
				continue
			}
			set[p] = true
			stack = append(stack, p)
		}
	}
	return set
}

func preds(nodes map[int]*node, v int) []int {
	var out []int
	for u, n := range nodes {
		for _, s := range n.dsucc {
			if s == v {
				out = append(out, u)
				break
			}
		}
	}
	return out
}

func dominators(nodes map[int]*node, entry int) map[int]map[int]bool {
	all := make([]int, 0, len(nodes))
	for i := range nodes {
		all = append(all, i)
	}
	sort.Ints(all)
	// Precompute predecessors once: the fixpoint below would otherwise re-scan
	// every node for predecessors on each pass.
	predsOf := make(map[int][]int, len(nodes))
	for u, n := range nodes {
		for _, s := range n.dsucc {
			if _, ok := nodes[s]; ok {
				predsOf[s] = append(predsOf[s], u)
			}
		}
	}
	dom := map[int]map[int]bool{}
	dom[entry] = map[int]bool{entry: true}
	for _, n := range all {
		if n == entry {
			continue
		}
		dom[n] = map[int]bool{}
		for _, x := range all {
			dom[n][x] = true
		}
	}
	changed := true
	for changed {
		changed = false
		for _, n := range all {
			if n == entry {
				continue
			}
			var newSet map[int]bool
			for _, p := range predsOf[n] {
				if _, ok := dom[p]; !ok {
					continue
				}
				if newSet == nil {
					newSet = copySet(dom[p])
				} else {
					newSet = intersect(newSet, dom[p])
				}
			}
			if newSet == nil {
				newSet = map[int]bool{}
			}
			newSet[n] = true
			if !sameSet(newSet, dom[n]) {
				dom[n] = newSet
				changed = true
			}
		}
	}
	return dom
}

func copySet(m map[int]bool) map[int]bool {
	out := make(map[int]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

func intersect(a, b map[int]bool) map[int]bool {
	out := map[int]bool{}
	for k := range a {
		if b[k] {
			out[k] = true
		}
	}
	return out
}

func sameSet(a, b map[int]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// regDestOps lists the IC10 ops whose first operand is a register written by the
// instruction. Used to tell a real write to the induction register from a branch
// (or a device/stack store) that merely names it.
var regDestOps = map[string]bool{
	"move": true, "select": true, "rand": true, "not": true,
	"add": true, "sub": true, "mul": true, "div": true, "mod": true, "pow": true, "atan2": true, "min": true, "max": true,
	"and": true, "or": true, "xor": true, "nor": true, "sll": true, "sra": true, "srl": true, "sla": true, "rol": true, "ror": true,
	"ext": true, "ins": true, "clamp": true, "lerp": true,
	"abs": true, "sgn": true, "sqrt": true, "exp": true, "log": true, "floor": true, "ceil": true,
	"round": true, "trunc": true, "sin": true, "cos": true, "tan": true, "asin": true, "acos": true, "atan": true,
	"seq": true, "sne": true, "slt": true, "sle": true, "sgt": true, "sge": true,
	"sap": true, "sna": true, "sapz": true, "snaz": true,
	"seqz": true, "snez": true, "sltz": true, "slez": true, "sgtz": true, "sgez": true, "snan": true, "snanz": true,
	"l": true, "ld": true, "lr": true, "ls": true, "lb": true, "lbn": true, "lbs": true, "lbns": true,
	"get": true, "getd": true, "peek": true, "pop": true, "rmap": true,
}

// detectTrips recognises a canonical counting loop and returns its trip count,
// or 0 when it cannot be determined. It handles both compiler output (exit check
// at the header, e.g. `bge rX BOUND out`) and hand-written loops (exit check at
// the latch, e.g. `blt rX BOUND header`) by finding the loop's single induction
// write `add/sub rX rX k` and the branch that leaves the loop.
func detectTrips(nodes map[int]*node, set map[int]bool, header int) int {
	// Candidate induction registers: exactly one write in the loop, of the form
	// add/sub reg reg k.
	writes := map[string][]*node{}
	for x := range set {
		n := nodes[x]
		if regDestOps[n.op] && len(n.args) > 0 {
			writes[n.args[0]] = append(writes[n.args[0]], n)
		}
	}
	type cand struct {
		reg  string
		step float64
	}
	var cands []cand
	for reg, ws := range writes {
		if !isReg(reg) || len(ws) != 1 {
			continue
		}
		w := ws[0]
		if (w.op == "add" || w.op == "sub") && len(w.args) == 3 && w.args[1] == reg {
			k, err := strconv.ParseFloat(w.args[2], 64)
			if err != nil || k == 0 {
				continue
			}
			if w.op == "sub" {
				k = -k
			}
			cands = append(cands, cand{reg, k})
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].reg < cands[j].reg })

	for _, c := range cands {
		init, ok := findInit(nodes, set, header, c.reg)
		if !ok {
			continue
		}
		for x := range set {
			n := nodes[x]
			cond, _, _, ok := ic10asm.BranchInfo(n.op)
			if !ok || len(n.dsucc) != 2 {
				continue
			}
			if set[n.dsucc[0]] == set[n.dsucc[1]] {
				continue // both inside (no exit) or both outside (no back edge)
			}
			cmp, ok := compareFn(cond, n.args, c.reg)
			if !ok {
				continue
			}
			continueOnTaken := set[n.dsucc[0]]
			cont := cmp
			if !continueOnTaken {
				cont = func(v float64) bool { return !cmp(v) }
			}
			var trips int
			if x == header {
				trips = countTrue(init, c.step, cont) // top-tested
			} else {
				trips = 1 + countTrue(init+c.step, c.step, cont) // bottom-tested
			}
			if trips < 0 {
				return 0 // unbounded
			}
			return trips
		}
	}
	return 0
}

// containsBarrier reports whether any node in set ends a tick (yield/sleep/hcf).
func containsBarrier(nodes map[int]*node, set map[int]bool) bool {
	for x := range set {
		if n := nodes[x]; n != nil && (n.barrier || n.halt) {
			return true
		}
	}
	return false
}

func isConditional(op string) bool {
	_, _, _, ok := ic10asm.BranchInfo(op)
	return ok
}

// compareFn builds the branch's condition as a predicate over the induction
// register: P(value) is true when the comparison as written holds.
func compareFn(cond string, args []string, reg string) (func(float64) bool, bool) {
	if ic10asm.IsBinaryCond(cond) {
		if len(args) < 3 {
			return nil, false
		}
		regLeft, regRight := args[0] == reg, args[1] == reg
		if !regLeft && !regRight {
			return nil, false
		}
		op := cond
		other := args[1]
		if regRight {
			op = flipCond(cond)
			other = args[0]
		}
		c, err := strconv.ParseFloat(other, 64)
		if err != nil {
			return nil, false
		}
		return func(v float64) bool { return compareOp(op, v, c) }, true
	}
	if len(args) < 1 || args[0] != reg {
		return nil, false
	}
	return func(v float64) bool { return compareOp(cond, v, 0) }, true
}

func compareOp(op string, v, c float64) bool {
	switch op {
	case "eq", "eqz":
		return v == c
	case "ne", "nez":
		return v != c
	case "lt", "ltz":
		return v < c
	case "le", "lez":
		return v <= c
	case "gt", "gtz":
		return v > c
	case "ge", "gez":
		return v >= c
	}
	return false
}

func flipCond(op string) string {
	switch op {
	case "lt":
		return "gt"
	case "le":
		return "ge"
	case "gt":
		return "lt"
	case "ge":
		return "le"
	}
	return op
}

// countTrue counts how many times cont holds as v starts at v0 and steps by
// step, stopping at the first false (with a sanity cap).
func countTrue(v0, step float64, cont func(float64) bool) int {
	v := v0
	for t := 0; t < 1<<20; t++ {
		if !cont(v) {
			return t
		}
		v += step
	}
	return -1 // effectively unbounded
}

// findInit walks backwards along the linear predecessor chain of the header to
// the nearest write of reg before the loop and returns its constant.
func findInit(nodes map[int]*node, set map[int]bool, header int, reg string) (float64, bool) {
	// Collect predecessors outside the loop.
	queue := preds(nodes, header)
	seen := map[int]bool{}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if seen[p] || set[p] {
			continue
		}
		seen[p] = true
		n := nodes[p]
		if regDestOps[n.op] && len(n.args) > 0 && n.args[0] == reg {
			switch n.op {
			case "move":
				if v, err := strconv.ParseFloat(n.args[1], 64); err == nil {
					return v, true
				}
			case "add", "sub":
				if len(n.args) == 3 {
					a, e1 := strconv.ParseFloat(n.args[1], 64)
					b, e2 := strconv.ParseFloat(n.args[2], 64)
					if e1 == nil && e2 == nil {
						if n.op == "sub" {
							return a - b, true
						}
						return a + b, true
					}
				}
			}
			return 0, false // written by something unknown
		}
		queue = append(queue, preds(nodes, p)...)
	}
	return 0, false
}

func isReg(s string) bool {
	if s == "sp" || s == "ra" {
		return true
	}
	if len(s) >= 2 && s[0] == 'r' {
		if _, err := strconv.Atoi(s[1:]); err == nil {
			return true
		}
	}
	return false
}

func (r *Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "per-tick budget: limit %d, %d instructions\n", r.Limit, r.Lines)
	if len(r.Segments) == 0 {
		b.WriteString("  (no instructions)\n")
		return b.String()
	}
	anyExceeds := false
	for i, s := range r.Segments {
		bar := " -> (program end)"
		if s.Barrier >= 0 {
			bar = " -> yield " + loc(s.Barrier, s.BarrierSource)
		} else if s.Exceeds {
			bar = fmt.Sprintf(" -> (no yield within %d)", r.Limit)
		}
		status := "fits"
		if s.Exceeds {
			status = fmt.Sprintf("EXCEEDS %d", r.Limit)
			anyExceeds = true
		}
		fmt.Fprintf(&b, "  tick segment %d: %s%s  worst-case %d instructions [%s]", i, loc(s.Start, s.Source), bar, s.Cost, status)
		if s.Exceeds && s.Dominant >= 0 {
			fmt.Fprintf(&b, "  <- dominated by loop %s (~%d instr)", loc(s.Dominant, s.DominantSource), s.DominantCost)
		}
		b.WriteByte('\n')
	}
	for _, l := range r.Loops {
		trips := "?"
		if l.Trips > 0 {
			trips = strconv.Itoa(l.Trips)
		}
		spans := ""
		if l.Spans {
			spans = " (spans ticks)"
		}
		body := fmt.Sprintf("%d", l.Body)
		if l.BodyMin != l.Body {
			body = fmt.Sprintf("%d..%d", l.BodyMin, l.Body)
		}
		fmt.Fprintf(&b, "  loop %s..%s (header %s): body %s x %s iterations%s\n",
			loc(l.Start, 0), loc(l.End, 0), loc(l.Header, l.Source), body, trips, spans)
	}
	if anyExceeds {
		b.WriteString("  worst case: a loop between yields can exceed the budget, so the chip resumes mid-loop on the next tick.\n")
	}
	return b.String()
}

// loc renders an IC10 line, adding its source line when known.
func loc(ic10, source int) string {
	if source > 0 {
		return fmt.Sprintf("L%d (src L%d)", ic10+1, source)
	}
	return fmt.Sprintf("L%d", ic10+1)
}
