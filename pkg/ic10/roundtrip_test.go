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
			// Legacy scripts use the whole stack, so compile with the dynamic
			// boundary (the fixed 30-slot default would reject them).
			code, diags, err := ic10.CompileWithOptions(name, []byte(icg), ic10.Options{DynamicStack: true})
			if diags.HasErrors() || err != nil {
				t.Fatalf("recompile: diags=%v err=%v\n%s", diags.Diags, err, icg)
			}

			// The fixed setup, plus several random device-value seeds so
			// value-dependent branches are covered too.
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
				if err := a.Load(string(src)); err != nil {
					t.Fatalf("original load: %v", err)
				}
				if err := b.Load(code); err != nil {
					t.Fatalf("round-trip load: %v", err)
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
					t.Errorf("seed %d: device writes differ at %d (len %d vs %d)\n original[%d:]: %s\n roundtrip[%d:]: %s",
						seed, i, len(wa), len(wb), i, preview(wa[i:]), i, preview(wb[i:]))
				}
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

			// The fixed setup, plus several random device-value seeds so
			// value-dependent branches are covered too.
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
				if err := a.Load(string(src)); err != nil {
					t.Fatalf("original load: %v", err)
				}
				if err := b.Load(code); err != nil {
					t.Fatalf("structured load: %v", err)
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
					t.Errorf("seed %d: device writes differ at %d (len %d vs %d)\n original[%d:]: %s\n structured[%d:]: %s",
						seed, i, len(wa), len(wb), i, preview(wa[i:]), i, preview(wb[i:]))
				}
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
