package minify

import (
	"strings"
	"testing"
)

// TestStackFoldLoop checks the "device ReferenceId in the host stack" fold: a
// pop/latch loop is unrolled per slot with the id inlined, keeping the sp the
// loop would have left for the next group.
func TestStackFoldLoop(t *testing.T) {
	src := "move sp 6\n" +
		"pop r9\n" +
		"l r0 r9 TotalMoles\n" +
		"add r10 r10 r0\n" +
		"brgt sp 4 -3\n" + // relative: 4 + (-3) = line 1 (the pop)
		"pop r9\n" +
		"l r0 r9 Idle\n" +
		"brgt sp 3 -2\n" // 7 + (-3) = line 4 (the pop)
	ids := map[int]int{5: 555, 4: 444, 3: 333}
	out, err := StackFold(src, ids)
	if err != nil {
		t.Fatalf("fold: %v", err)
	}
	want := strings.Join([]string{
		"l r0 555 TotalMoles",
		"add r10 r10 r0",
		"l r0 444 TotalMoles",
		"add r10 r10 r0",
		"l r0 333 Idle",
		"move sp 3", // the loop's final sp (kept: nothing later rewrites it)
	}, "\n") + "\n"
	if out != want {
		t.Errorf("fold =\n%s\nwant\n%s", out, want)
	}
}

// TestStackFoldBails makes sure the pass leaves anything it cannot prove alone.
func TestStackFoldBails(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"dynamic jump", "move sp 6\npop r9\nl r0 r9 X\nbrgt sp 4 -3\nj r0\n"},
		{"missing id", "move sp 6\npop r9\nl r0 r9 X\nbrgt sp 4 -3\n"},
		{"device reg rewritten", "move sp 6\npop r9\nl r0 r9 X\nmove r9 1\nbrgt sp 4 -4\n"},
		{"no ids", "move sp 6\npop r9\nl r0 r9 X\nbrgt sp 4 -3\n"},
		{"branch into region", "move sp 6\npop r9\nl r0 r9 X\nbrgt sp 4 -3\nj 2\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ids := map[int]int{5: 555, 4: 444}
			if c.name == "no ids" {
				ids = nil
			}
			out, err := StackFold(c.src, ids)
			if err != nil {
				t.Fatalf("fold: %v", err)
			}
			if out != c.src {
				t.Errorf("expected the input unchanged, got:\n%s", out)
			}
		})
	}
}

// TestStackFoldKeepsLiveSP checks the sp the loop leaves is preserved when later
// code still reads it (a chained group or a brgtz sp terminator).
func TestStackFoldKeepsLiveSP(t *testing.T) {
	// Two chained groups: the first leaves sp = 1 for the second's pop, and the
	// last latch is the unary form (bound 0).
	src := "move sp 2\n" +
		"pop r9\n" +
		"s r9 On r0\n" +
		"brgt sp 1 -2\n" +
		"pop r9\n" +
		"s r9 On r1\n" +
		"brgtz sp -2\n"
	ids := map[int]int{1: 111, 0: 222}
	out, err := StackFold(src, ids)
	if err != nil {
		t.Fatalf("fold: %v", err)
	}
	if !strings.Contains(out, "s 111 On r0") || !strings.Contains(out, "s 222 On r1") {
		t.Errorf("both groups should be folded with their ids, got:\n%s", out)
	}
	if strings.Contains(out, "pop r9") {
		t.Errorf("the pops should have been folded away, got:\n%s", out)
	}
}

// mnemonicKey classifies a line by opcode, splitting `move` into stack-pointer
// and ordinary moves so the scaffolding the fold removes is separable.
func mnemonicKey(line string) string {
	toks := strings.Fields(line)
	if len(toks) == 0 {
		return ""
	}
	if strings.EqualFold(toks[0], "move") && len(toks) > 1 && strings.EqualFold(toks[1], "sp") {
		return "move(sp)"
	}
	return strings.ToLower(toks[0])
}

func mnemonicCounts(src string) map[string]int {
	m := map[string]int{}
	for _, ln := range strings.Split(src, "\n") {
		if k := mnemonicKey(ln); k != "" {
			m[k]++
		}
	}
	return m
}

// TestStackFoldDropsOnlyScaffolding guards against a fold that over-drops: the
// unroll may only remove the scaffolding (pop / the latch / the redundant
// move sp) and duplicate bodies — every other instruction of the input must
// still appear in the output. This is what a synthetic two-group test missed
// when an earlier version rolled the emitted lines back too far and ate real
// code (the trailing mode-dispatch block of the real program).
func TestStackFoldDropsOnlyScaffolding(t *testing.T) {
	src := strings.Join([]string{
		"move r15 %1011011110", // prologue: the fold must not touch it
		"move r14 1",
		"move sp 6",
		"pop r9",
		"l r0 r9 TotalMoles",
		"add r10 r10 r0",
		"brgt sp 4 -3", // two slots (6 -> 4)
		"pop r9",
		"s r9 Open r0",
		"brlt r2 1 2", // nested if: skip to the latch
		"s r9 Setting r0",
		"brgt sp 3 -4",   // one slot (sp is 4 after the first group; the pop sits 4 lines back)
		"ext r0 r15 0 1", // trailing code that must survive
		"ins r0 %1 9 1",
		"j 0",
	}, "\n") + "\n"
	ids := map[int]int{5: 555, 4: 444, 3: 333}
	out, err := StackFold(src, ids)
	if err != nil {
		t.Fatalf("fold: %v", err)
	}
	before := mnemonicCounts(src)
	after := mnemonicCounts(out)
	for op, n := range before {
		switch op {
		case "pop", "brgt", "brgtz", "move(sp)": // removed or rewritten scaffolding
			continue
		}
		if after[op] < n {
			t.Errorf("%s: input has %d, output only %d (the fold dropped code)\n%s", op, n, after[op], out)
		}
	}
	if strings.Contains(out, "pop r9") {
		t.Errorf("the pops should be folded away:\n%s", out)
	}
}
