package ic10_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// corpusDirs returns every directory that holds IC10 scripts for the
// regression tests. The bundled ic10code/ corpus (which includes the vendored
// Stationeers-Workspace scripts under ic10code/stationeers-workspace/) is used
// when present; IC10CODE_EXTRA (a colon-separated list, like PATH) adds
// external checkouts.
func corpusDirs() []string {
	var dirs []string
	add := func(d string) {
		if d == "" {
			return
		}
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			return
		}
		for _, e := range dirs {
			if e == d {
				return
			}
		}
		dirs = append(dirs, d)
	}
	add("../../ic10code")
	for _, d := range filepath.SplitList(os.Getenv("IC10CODE_EXTRA")) {
		add(d)
	}
	return dirs
}

// requireIc10Code skips tests that need a local IC10 corpus. The bundled
// ic10code/ corpus is third-party (community scripts) and is not distributed
// with the repository, so a fresh checkout simply skips these tests.
func requireIc10Code(t *testing.T) {
	t.Helper()
	if len(corpusDirs()) == 0 {
		t.Skip("ic10code/ corpus not present (third-party, not in this repo)")
	}
}

// knownUnsupported documents corpus files that the round-trip and minify tests
// intentionally skip. They are either invalid IC10 (the original would not
// assemble in-game) or exceed the chip budget when recompiled from the
// decompiler's output. Keys are file base names with or without extension.
var knownUnsupported = map[string]string{
	"oreSorter":       "source uses an undefined `hash` register (invalid IC10)",
	"sorterSample":    "source uses an undefined `hash` register (invalid IC10)",
	"traderSolver":    "decompile->recompile exceeds the 128-line chip budget",
	"traderSolverRAW": "decompile->recompile exceeds the 128-line chip budget",
}

// knownUnsupportedPorts lists hand-written ports that compile over the chip
// budget, so the port-equivalence test cannot run them. Their original .ic10
// still takes part in the round-trip/minify tests.
var knownUnsupportedPorts = map[string]string{
	"solverLarge":    "hand port compiles to 145 lines (over the 128-line budget)",
	"solverLargeRAW": "hand port compiles to 145 lines (over the 128-line budget)",
}

// skipKnownUnsupported skips a corpus file listed in knownUnsupported.
func skipKnownUnsupported(t *testing.T, path string) {
	t.Helper()
	base := filepath.Base(path)
	if reason, ok := knownUnsupported[base]; ok {
		t.Skipf("known unsupported: %s", reason)
	}
	if ext := filepath.Ext(base); ext != "" {
		if reason, ok := knownUnsupported[strings.TrimSuffix(base, ext)]; ok {
			t.Skipf("known unsupported: %s", reason)
		}
	}
}

// skipKnownUnsupportedPort skips a hand port listed in knownUnsupportedPorts.
func skipKnownUnsupportedPort(t *testing.T, path string) {
	t.Helper()
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if reason, ok := knownUnsupportedPorts[base]; ok {
		t.Skipf("known unsupported port: %s", reason)
	}
}

// corpusFiles returns every file under the corpus directories whose name ends
// in one of the given suffixes, skipping dependency/VCS directories.
func corpusFiles(t *testing.T, suffixes ...string) []string {
	t.Helper()
	var files []string
	for _, root := range corpusDirs() {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				switch info.Name() {
				case "node_modules", ".git":
					return filepath.SkipDir
				}
				return nil
			}
			for _, s := range suffixes {
				if strings.HasSuffix(path, s) {
					files = append(files, path)
					break
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(files)
	return files
}
