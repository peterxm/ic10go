package minify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommentsBlankLinesAndLabels(t *testing.T) {
	src := "# header\n\nalias x d0\nloop:\n  l r0 x Ratio\n  j loop\n"
	got, err := Minify(src, Options{DeadCode: true})
	if err != nil {
		t.Fatal(err)
	}
	want := "l r0 d0 Ratio\nj 0\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestKeepFlags(t *testing.T) {
	src := "alias x d0\nloop:\nl r0 x Ratio\nj loop\n"
	got, err := Minify(src, Options{KeepDefines: true, KeepLabels: true, DeadCode: true})
	if err != nil {
		t.Fatal(err)
	}
	want := "alias x d0\nloop:\nl r0 x Ratio\nj loop\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestNumericTargetRenumbered(t *testing.T) {
	src := "move r0 1\n# comment\nj 3\nmove r0 2\n"
	got, err := Minify(src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := "move r0 1\nj 2\nmove r0 2\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDeadCodeRemoved(t *testing.T) {
	src := "move r0 1\nj 3\nmove r0 2\nmove r0 3\n"
	got, err := Minify(src, Options{DeadCode: true})
	if err != nil {
		t.Fatal(err)
	}
	want := "move r0 1\nj 2\nmove r0 3\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDynamicBranchDisablesDeadCode(t *testing.T) {
	src := "move r0 1\nj ra\nmove r0 2\n"
	got, err := Minify(src, Options{DeadCode: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "move r0 2") {
		t.Errorf("unreachable code removed despite dynamic branch:\n%s", got)
	}
}

func TestLongLineRejected(t *testing.T) {
	long := strings.Repeat("9", 100)
	src := "define X " + long + "\nmove r0 X\n"
	if _, err := Minify(src, Options{}); err == nil {
		t.Fatal("expected a line-length error")
	}
}

func TestKeepsDefinesWhenInliningOverflows(t *testing.T) {
	// Inlining X twice would exceed the line limit, so Minify keeps the define.
	src := "define X HASH(\"ItemPureIceVolatiles\")\nor X X SorterInstruction.FilterPrefabHashNotEquals\n"
	got, err := Minify(src, Options{})
	if err != nil {
		t.Fatalf("Minify: %v", err)
	}
	if !strings.Contains(got, "define X") {
		t.Errorf("define was not kept to stay under the line limit:\n%s", got)
	}
	for _, ln := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if len(ln) > MaxLineLen {
			t.Errorf("line over limit (%d): %q", len(ln), ln)
		}
	}
}

// opcodes counts instruction mnemonics (first token, lowercased).
func opcodes(src string) map[string]int {
	m := map[string]int{}
	for _, ln := range strings.Split(src, "\n") {
		toks := strings.Fields(strings.TrimSpace(ln))
		if len(toks) == 0 || strings.HasSuffix(toks[0], ":") || strings.HasPrefix(toks[0], "#") {
			continue
		}
		m[strings.ToLower(toks[0])]++
	}
	return m
}

// The runtime guard Minify now applies to its own output: a pass that drops or
// invents instructions must be caught rather than shipped (review D4).
func TestSameOpcodesCatchesDamage(t *testing.T) {
	if err := sameOpcodes("add r1 r0 1\ns d0 Setting 1\n", "add r1 r0 1\n"); err == nil {
		t.Error("a dropped instruction must be caught")
	}
	if err := sameOpcodes("add r1 r0 1\n", "add r1 r0 1\ns d0 Setting 1\n"); err == nil {
		t.Error("an invented instruction must be caught")
	}
	// Comments, blank lines, label lines and alias/define lines are expected to
	// disappear, so they must not count.
	if err := sameOpcodes("l r0 d0 Setting\n# c\n\nL:\ns d0 Setting 1\nalias x r0\n",
		"l r0 d0 Setting\ns d0 Setting 1\n"); err != nil {
		t.Errorf("comments/labels/defs should not count: %v", err)
	}
}

// TestMinifyKeepsEveryInstruction is the guard against a compression pass that
// silently drops real code. With dead-code elimination off, minify may only drop
// comments, blank lines and labels — so the opcode multiset of the corpus files
// has to come back identical. (A branch target is renumbered, but the mnemonic
// stays.) The stack-id fold's TestStackFoldDropsOnlyScaffolding is the same idea
// for the other pass; an earlier revision of it ate a real program's tail.
func TestMinifyKeepsEveryInstruction(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "ic10", "*.ic"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("no corpus files")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			out, err := Minify(string(data), Options{DeadCode: false})
			if err != nil {
				t.Fatalf("minify: %v", err)
			}
			before, after := opcodes(string(data)), opcodes(out)
			for _, op := range []string{"alias", "define"} {
				delete(before, op) // dropped as lines (their symbols are inlined)
				delete(after, op)
			}
			for op, n := range before {
				if after[op] != n {
					t.Errorf("%s: input has %d, output %d", op, n, after[op])
				}
			}
			for op, n := range after {
				if before[op] == 0 {
					t.Errorf("%s: output invented %d", op, n)
				}
			}
		})
	}
}

func TestMinifyForwardDefineAndOrderedAlias(t *testing.T) {
	// Defines are position independent, aliases bind in source order (measured in
	// game 2026-10-18). Minify must substitute the former globally and the latter
	// as it walks.
	got, err := Minify("move r0 LIMIT\ns d0 Setting r0\ndefine LIMIT 42\n", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "move r0 42") {
		t.Errorf("forward define not substituted:\n%s", got)
	}

	got, err = Minify("alias v d0\ns v On 1\nalias v d1\ns v On 0\n", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "s d0 On 1") || !strings.Contains(got, "s d1 On 0") {
		t.Errorf("alias rebinding must follow source order:\n%s", got)
	}
}
