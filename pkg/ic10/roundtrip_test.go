package ic10_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"ic10go/internal/decomp"
	"ic10go/internal/vm"
	"ic10go/pkg/ic10"
)

// roundTripSkip lists real scripts whose decompilation cannot be recompiled
// yet, with the reason.
var roundTripSkip = map[string]string{
	"logic sorter demonstration code.ic": "decompiler emits SorterInstruction.*/SlotClass.*/SortingClass.* enum constants the compiler does not model",
}

// TestIc10CodeRoundTrip decompiles every real IC10 script in ic10code/ and
// recompiles it, then checks that it performs exactly the same sequence of
// device writes as the original. Comparing writes (rather than a fixed-step
// snapshot) makes the check independent of instruction counts per iteration.
func TestIc10CodeRoundTrip(t *testing.T) {
	var files []string
	err := filepath.Walk("../../ic10code", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".ic") || strings.HasSuffix(path, ".ic10") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no IC10 scripts found")
	}
	sort.Strings(files)

	for _, path := range files {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			if reason, ok := roundTripSkip[name]; ok {
				t.Skip(reason)
			}
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
