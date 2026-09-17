package ic10_test

import (
	"fmt"
	"os"
	"path/filepath"
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
			code, diags, err := ic10.Compile(name, []byte(icg))
			if diags.HasErrors() || err != nil {
				t.Fatalf("recompile: diags=%v err=%v\n%s", diags.Diags, err, icg)
			}

			a := vm.New()
			portSetup(a)
			if err := a.Load(string(src)); err != nil {
				t.Fatalf("original load: %v", err)
			}
			b := vm.New()
			portSetup(b)
			if err := b.Load(code); err != nil {
				t.Fatalf("round-trip load: %v", err)
			}

			const writes = 100
			wa := writeSequence(a, writes, 40000)
			wb := writeSequence(b, writes, 40000)
			if strings.Join(wa, "|") != strings.Join(wb, "|") {
				i := 0
				for i < len(wa) && i < len(wb) && wa[i] == wb[i] {
					i++
				}
				t.Errorf("device writes differ at %d (len %d vs %d)\n original[%d:]: %s\n roundtrip[%d:]: %s",
					i, len(wa), len(wb), i, preview(wa[i:]), i, preview(wb[i:]))
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
			skipKnownStructured(t, path)
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			icg, _, err := decomp.DecompileStructured(string(src))
			if err != nil {
				t.Fatalf("decompile: %v", err)
			}
			code, diags, err := ic10.Compile(name, []byte(icg))
			if diags.HasErrors() || err != nil {
				t.Skipf("structured output does not compile (falls back to flat): %v", err)
			}

			a := vm.New()
			portSetup(a)
			if err := a.Load(string(src)); err != nil {
				t.Fatalf("original load: %v", err)
			}
			b := vm.New()
			portSetup(b)
			if err := b.Load(code); err != nil {
				t.Fatalf("structured load: %v", err)
			}

			const writes = 100
			wa := writeSequence(a, writes, 40000)
			wb := writeSequence(b, writes, 40000)
			if strings.Join(wa, "|") != strings.Join(wb, "|") {
				i := 0
				for i < len(wa) && i < len(wb) && wa[i] == wb[i] {
					i++
				}
				t.Errorf("device writes differ at %d (len %d vs %d)\n original[%d:]: %s\n structured[%d:]: %s",
					i, len(wa), len(wb), i, preview(wa[i:]), i, preview(wb[i:]))
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
