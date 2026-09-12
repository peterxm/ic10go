package ic10_test

import (
	"strings"
	"testing"

	"ic10go/internal/vm"
	"ic10go/pkg/ic10"
)

const dataTableSrc = `data T = [10, 20, 30]

func main() {
    for {
        yield()
        d0.Setting = T[1]
    }
}
`

func TestDataTableCompile(t *testing.T) {
	code, diags, err := ic10.Compile("t.icg", []byte(dataTableSrc))
	if err != nil || diags.HasErrors() {
		t.Fatalf("compile: %v %v", diags.Diags, err)
	}
	if !strings.Contains(code, "get ") {
		t.Errorf("runtime does not read the data segment:\n%s", code)
	}
	loader, err := ic10.DataLoader("t.icg", []byte(dataTableSrc))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loader, "put db") {
		t.Errorf("loader missing writes:\n%s", loader)
	}
	if !ic10.HasData("t.icg", []byte(dataTableSrc)) {
		t.Error("HasData = false, want true")
	}
}

// runDataProgram runs the loader (if any), then the compiled runtime on a
// machine whose stack is preloaded from the loader.
func runDataProgram(t *testing.T, opts ic10.Options, withLoader bool, steps int) *vm.Machine {
	t.Helper()
	src := []byte(dataTableSrc)
	code, diags, err := ic10.CompileWithOptions("t.icg", src, opts)
	if err != nil || diags.HasErrors() {
		t.Fatalf("compile: %v %v", diags.Diags, err)
	}
	m := vm.New()
	m.Set("d0", "Setting", 7) // distinguishable from the value the program writes
	if withLoader {
		loader, err := ic10.DataLoaderWithOptions("t.icg", src, opts)
		if err != nil {
			t.Fatal(err)
		}
		lm := vm.New()
		if err := lm.Load(loader); err != nil {
			t.Fatal(err)
		}
		if err := lm.Run(100); err != nil && err != vm.ErrStepLimit {
			t.Fatal(err)
		}
		copy(m.Stack, lm.Stack)
	}
	if err := m.Load(code); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < steps; i++ {
		if err := m.Run(1); err != nil && err != vm.ErrStepLimit {
			t.Fatal(err)
		}
	}
	return m
}

func TestDataTableEndToEnd(t *testing.T) {
	m := runDataProgram(t, ic10.Options{}, true, 300)
	if got := m.Get("d0", "Setting"); got != 20 {
		t.Errorf("d0.Setting = %v, want 20 (T[1])", got)
	}
}

func TestDataTableMissingHalts(t *testing.T) {
	m := runDataProgram(t, ic10.Options{}, false, 300)
	if got := m.Get("d0", "Setting"); got != 7 {
		t.Errorf("d0.Setting = %v, want 7 (chip should halt before writing)", got)
	}
}

func TestDataTableNoCheck(t *testing.T) {
	m := runDataProgram(t, ic10.Options{NoDataCheck: true}, false, 300)
	if got := m.Get("d0", "Setting"); got != 0 {
		t.Errorf("d0.Setting = %v, want 0 (T[1] reads an empty slot)", got)
	}
}

const tableSwitchSrc = `func main() {
    var i = 0
    for {
        yield()
        switch i table {
        case 0: db.Setting = 100; d0.Setting = 10
        case 1: db.Setting = 200; d0.Setting = 20
        case 2: db.Setting = 300; d0.Setting = 30
        }
        i = (i + 1) % 3
    }
}
`

func TestTableSwitch(t *testing.T) {
	src := []byte(tableSwitchSrc)
	code, diags, err := ic10.Compile("sw.icg", src)
	if err != nil || diags.HasErrors() {
		t.Fatalf("compile: %v %v", diags.Diags, err)
	}
	loader, err := ic10.DataLoader("sw.icg", src)
	if err != nil {
		t.Fatal(err)
	}
	if loader == "" {
		t.Fatal("table switch produced no loader")
	}
	lm := vm.New()
	if err := lm.Load(loader); err != nil {
		t.Fatal(err)
	}
	if err := lm.Run(100); err != nil && err != vm.ErrStepLimit {
		t.Fatal(err)
	}
	m := vm.New()
	m.Device("db").Stack = lm.Device("db").Stack
	if err := m.Load(code); err != nil {
		t.Fatal(err)
	}
	seen := map[float64]bool{}
	for i := 0; i < 400; i++ {
		if err := m.Run(1); err != nil && err != vm.ErrStepLimit {
			t.Fatal(err)
		}
		seen[m.Get("d0", "Setting")] = true
	}
	for _, want := range []float64{10, 20, 30} {
		if !seen[want] {
			t.Errorf("table switch value %v never produced", want)
		}
	}
}

func TestTableSwitchRejectsNonDense(t *testing.T) {
	src := []byte(`func main() {
    switch d0.Setting table {
    case 1: d1.Setting = 10
    case 3: d1.Setting = 30
    }
}
`)
	_, diags, _ := ic10.Compile("bad.icg", src)
	if !diags.HasErrors() {
		t.Error("expected an error for non-dense table switch cases")
	}
}

func TestDataTableStackAccess(t *testing.T) {
	m := runDataProgram(t, ic10.Options{DataAccessStack: true}, true, 300)
	if got := m.Get("d0", "Setting"); got != 20 {
		t.Errorf("stack access: d0.Setting = %v, want 20", got)
	}
}

func TestDataTableStackAccessMissingHalts(t *testing.T) {
	m := runDataProgram(t, ic10.Options{DataAccessStack: true}, false, 300)
	if got := m.Get("d0", "Setting"); got != 7 {
		t.Errorf("stack access without data: d0.Setting = %v, want 7", got)
	}
}
