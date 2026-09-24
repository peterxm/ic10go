package ic10_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ic10go/internal/diag"
	"ic10go/pkg/ic10"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasDiagMsg(bag *diag.Bag, substr string) bool {
	for _, d := range bag.Diags {
		if strings.Contains(d.Msg, substr) {
			return true
		}
	}
	return false
}

func TestImportMergesAndFolds(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib.icg"),
		"func triple(x num) num { return x * 3 }\nconst BASE = 100\n")
	main := filepath.Join(dir, "main.icg")
	src := "import \"lib.icg\"\n\nfunc main() { d0.Setting = BASE + triple(4) }\n"

	code, diags, err := ic10.CompileWithOptions(main, []byte(src), ic10.Options{Imports: true})
	if err != nil || diags.HasErrors() {
		t.Fatalf("compile: %v %v", diags.Diags, err)
	}
	if !strings.Contains(code, "s d0 Setting 112") {
		t.Errorf("imported const/function not folded into the output:\n%s", code)
	}
}

func TestImportMissingReportsError(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "main.icg")
	src := "import \"nope.icg\"\nfunc main() { d0.On = 1 }\n"
	_, diags, _ := ic10.CompileWithOptions(main, []byte(src), ic10.Options{Imports: true})
	if !hasDiagMsg(diags, "cannot find import") {
		t.Errorf("expected a cannot-find-import error, got %+v", diags.Diags)
	}
}

// TestImportCycleIsMergedOnce checks a cyclic import merges each file once
// instead of looping.
func TestImportCycleIsMergedOnce(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "c.icg"), "import \"d.icg\"\nconst C = 1\n")
	writeFile(t, filepath.Join(dir, "d.icg"), "import \"c.icg\"\nconst D = 2\n")
	main := filepath.Join(dir, "main.icg")
	src := "import \"c.icg\"\nfunc main() { d0.On = C + D }\n"
	code, diags, err := ic10.CompileWithOptions(main, []byte(src), ic10.Options{Imports: true})
	if err != nil || diags.HasErrors() {
		t.Fatalf("compile: %v %v", diags.Diags, err)
	}
	if !strings.Contains(code, "s d0 On 3") {
		t.Errorf("cyclic import did not merge cleanly:\n%s", code)
	}
}

func TestImportedFileMustNotDeclareMain(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib.icg"), "func main() { d0.On = 1 }\n")
	main := filepath.Join(dir, "main.icg")
	_, diags, _ := ic10.CompileWithOptions(main, []byte("import \"lib.icg\"\nfunc main() { d0.On = 0 }\n"), ic10.Options{Imports: true})
	if !hasDiagMsg(diags, "must not declare main") {
		t.Errorf("expected an error about main in an imported file, got %+v", diags.Diags)
	}
}
