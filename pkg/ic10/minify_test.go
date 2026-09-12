package ic10_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ic10go/internal/minify"
	"ic10go/internal/vm"
)

// TestMinifyIc10Code minifies every real IC10 script and checks that it leaves
// the devices in the same state as the original.
func TestMinifyIc10Code(t *testing.T) {
	requireIc10Code(t)
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
		t.Skip("no IC10 scripts found")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			out, err := minify.Minify(string(src), minify.Options{DeadCode: true})
			if err != nil {
				t.Fatalf("minify: %v", err)
			}
			if n, m := countLines(string(src)), countLines(out); m > n {
				t.Errorf("line count grew: %d -> %d", n, m)
			}

			a := vm.New()
			portSetup(a)
			if err := a.Load(string(src)); err != nil {
				t.Fatalf("original load: %v", err)
			}
			if err := a.Run(4000); err != nil && err != vm.ErrStepLimit {
				t.Fatalf("original run: %v", err)
			}

			b := vm.New()
			portSetup(b)
			if err := b.Load(out); err != nil {
				t.Fatalf("minified load: %v\n%s", err, out)
			}
			if err := b.Run(4000); err != nil && err != vm.ErrStepLimit {
				t.Fatalf("minified run: %v", err)
			}

			compareDevices(t, a, b)
		})
	}
}

func countLines(s string) int {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
