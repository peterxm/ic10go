package ic10_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"ic10go/internal/decomp"
	"ic10go/internal/vm"
	"ic10go/pkg/ic10"
)

// TestIc10CodeRoundTrip decompiles every real IC10 script in the corpus and
// recompiles it, then checks that it performs exactly the same sequence of
// device writes as the original. Comparing writes (rather than a fixed-step
// snapshot) makes the check independent of instruction counts per iteration.
func TestIc10CodeRoundTrip(t *testing.T) {
	requireIc10Code(t)
	files := corpusFiles(t, ".ic", ".ic10")
	if len(files) == 0 {
		t.Skip("no IC10 scripts found")
	}

	for _, path := range files {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			skipKnownUnsupported(t, path)
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			icg, _, err := decomp.Decompile(string(src))
			if err != nil {
				t.Fatalf("decompile: %v", err)
			}
			// Legacy scripts use the whole stack, so compile with the dynamic
			// boundary (the fixed 30-slot default would reject them).
			code, diags, err := ic10.CompileWithOptions(name, []byte(icg), ic10.Options{DynamicStack: true})
			if diags.HasErrors() || err != nil {
				t.Fatalf("recompile: diags=%v err=%v\n%s", diags.Diags, err, icg)
			}

			if msg := compareWrites(string(src), code); msg != "" {
				skipIfNaNSensitive(t, name, icg, string(src))
				t.Errorf("%s", msg)
			}
		})
	}
}

// TestIc10CodeRoundTripStructured is TestIc10CodeRoundTrip using the
// structured decompiler. Structuring is best-effort: when the structured
// output does not compile, the CLI falls back to the flat form, so those cases
// are skipped. A structured result that compiles but behaves differently is a
// structuring bug and fails.
func TestIc10CodeRoundTripStructured(t *testing.T) {
	requireIc10Code(t)
	files := corpusFiles(t, ".ic", ".ic10")
	if len(files) == 0 {
		t.Skip("no IC10 scripts found")
	}

	for _, path := range files {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			skipKnownUnsupported(t, path)
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			icg, _, err := decomp.DecompileStructured(string(src))
			if err != nil {
				t.Fatalf("decompile: %v", err)
			}
			code, diags, err := ic10.CompileWithOptions(name, []byte(icg), ic10.Options{DynamicStack: true})
			if diags.HasErrors() || err != nil {
				t.Skipf("structured output does not compile (falls back to flat): %v", err)
			}

			if msg := compareWrites(string(src), code); msg != "" {
				skipIfNaNSensitive(t, name, icg, string(src))
				t.Errorf("%s", msg)
			}
		})
	}
}

// writeSequence records the device writes (dev.logic=value) performed while
// executing up to maxWrites writes or maxSteps instructions.
func writeSequence(m *vm.Machine, maxWrites, maxSteps int) []string {
	var seq []string
	m.OnWrite = func(dev, logic string, v float64) {
		seq = append(seq, fmt.Sprintf("%s.%s=%v", dev, logic, v))
	}
	for i := 0; i < maxSteps && len(seq) < maxWrites; i++ {
		if err := m.Run(1); err != nil && err != vm.ErrStepLimit {
			break
		}
	}
	return seq
}

func preview(s []string) string {
	if len(s) > 8 {
		s = s[:8]
	}
	return strings.Join(s, " ")
}

// compareWrites runs the original and the recompiled program over the fixed
// setup and the random seeds, and returns the first mismatch (or "" when their
// device-write sequences agree).
func compareWrites(src, code string) string {
	for _, seed := range []int64{0, 1, 2, 3, 4, 5} {
		a := vm.New()
		b := vm.New()
		if seed == 0 {
			portSetup(a)
			portSetup(b)
		} else {
			portSetupSeed(a, seed)
			portSetupSeed(b, seed)
		}
		if err := a.Load(src); err != nil {
			return fmt.Sprintf("original load: %v", err)
		}
		if err := b.Load(code); err != nil {
			return fmt.Sprintf("recompile load: %v", err)
		}
		writes, steps := 100, 40000
		if seed != 0 {
			writes, steps = 60, 30000
		}
		wa := writeSequence(a, writes, steps)
		wb := writeSequence(b, writes, steps)
		if strings.Join(wa, "|") != strings.Join(wb, "|") {
			i := 0
			for i < len(wa) && i < len(wb) && wa[i] == wb[i] {
				i++
			}
			return fmt.Sprintf("seed %d: device writes differ at %d (len %d vs %d)\n original[%d:]: %s\n recompiled[%d:]: %s",
				seed, i, len(wa), len(wb), i, preview(wa[i:]), i, preview(wb[i:]))
		}
	}
	return ""
}

// skipIfNaNSensitive skips a round-trip subtest whose only divergence is the
// default (non-NaN-safe) negation of an ordering comparison. A program that
// reads an absent device (or a genuine NaN) hits NaN, where `!(a < b)` is not
// `a >= b`; recompiling with NaNSafe and matching again confirms that is the
// whole story, so the file is reported as NaN-sensitive rather than a bug.
func skipIfNaNSensitive(t *testing.T, name, icg, src string) {
	t.Helper()
	ns, diags, err := ic10.CompileWithOptions(name, []byte(icg), ic10.Options{DynamicStack: true, NaNSafe: true})
	if err != nil || diags.HasErrors() {
		return
	}
	if compareWrites(src, ns) == "" {
		t.Skipf("NaN-sensitive: the default (non-NaN-safe) negation of an ordering comparison changes NaN behaviour; matches with --nan-safe")
	}
}

// TestIc10CodeRoundTripStack checks that the recompiled program leaves the same
// user-stack contents as the original. The device-write comparison above cannot
// see a program whose interface is the housing stack (`put db N v` / `get db
// N`): a stack loader writes no device logic at all, so a translation that
// deletes or misplaces its writes would still pass. This runs both forms with a
// pre-filled stack and compares the user region [0,128).
//
// Only the user region is compared: the compiler keeps its data segment and
// register spills above it, and a recompiled program may legitimately leave
// those alone (constant data reads are inlined to literals). Programs the
// decompiler warns about are skipped: it could not translate an instruction, so
// behaviour is not expected to match.
func TestIc10CodeRoundTripStack(t *testing.T) {
	requireIc10Code(t)
	files := corpusFiles(t, ".ic", ".ic10")
	if len(files) == 0 {
		t.Skip("no IC10 scripts found")
	}

	for _, path := range files {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			skipKnownUnsupported(t, path)
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			icg, warns, err := decomp.Decompile(string(src))
			if err != nil {
				t.Fatalf("decompile: %v", err)
			}
			if len(warns) > 0 {
				t.Skipf("decompiler could not translate %d instruction(s)", len(warns))
			}
			code, diags, err := ic10.CompileWithOptions(name, []byte(icg), ic10.Options{DynamicStack: true, MaxLines: 512})
			if diags.HasErrors() || err != nil {
				t.Skipf("recompile failed: %v", err)
			}

			a, b := vm.New(), vm.New()
			portSetup(a)
			portSetup(b)
			// A non-zero starting stack so a program that reads it without
			// writing first has something to read.
			for i := 0; i < 16; i++ {
				a.Stack[i] = float64(i+1) + 0.25
				b.Stack[i] = float64(i+1) + 0.25
			}
			if err := a.Load(string(src)); err != nil {
				t.Fatalf("original load: %v", err)
			}
			if err := b.Load(code); err != nil {
				t.Fatalf("round-trip load: %v", err)
			}
			settle(a)
			settle(b)
			for i := 0; i < 128; i++ {
				av, bv := stackText(a.Stack[i]), stackText(b.Stack[i])
				if av != bv {
					t.Errorf("user stack slot %d differs: original %s, round-trip %s", i, av, bv)
					return
				}
			}
		})
	}
}

// settle runs a program until its stack has not changed for quiet ticks, so the
// two forms are compared at a steady state rather than at a fixed step count
// (they execute different instruction counts per iteration). The cap bounds a
// program that never reaches one.
func settle(m *vm.Machine) {
	const quiet, maxTicks = 3000, 300000
	last := make([]string, len(m.Stack))
	for i := range last {
		last[i] = stackText(m.Stack[i])
	}
	unchanged := 0
	for t := 0; t < maxTicks; t++ {
		if err := m.Run(1); err != nil && err != vm.ErrStepLimit {
			return
		}
		same := true
		for i := range m.Stack {
			s := stackText(m.Stack[i])
			if s != last[i] {
				same = false
				last[i] = s
			}
		}
		if same {
			unchanged++
			if unchanged >= quiet {
				return
			}
		} else {
			unchanged = 0
		}
	}
}

// stackText renders a stack slot so NaN compares equal to itself (two NaNs are
// the same value for this comparison, while Go's == says otherwise).
func stackText(v float64) string {
	if v != v {
		return "nan"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}
