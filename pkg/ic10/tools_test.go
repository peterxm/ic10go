package ic10_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ic10go/pkg/ic10"
)

func TestFormatIdempotent(t *testing.T) {
	programs, err := filepath.Glob("../../testdata/programs/*.icg")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range programs {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		once, diags, err := ic10.Format(p, src)
		if diags.HasErrors() || err != nil {
			t.Fatalf("%s: first pass: diags=%v err=%v", p, diags.Diags, err)
		}
		twice, diags2, err := ic10.Format(p, []byte(once))
		if diags2.HasErrors() || err != nil {
			t.Fatalf("%s: second pass: diags=%v err=%v", p, diags2.Diags, err)
		}
		if once != twice {
			t.Errorf("%s: formatting is not idempotent:\n--- once ---\n%s\n--- twice ---\n%s", p, once, twice)
		}
	}
}

func TestFormatPreservesComments(t *testing.T) {
	src := []byte("// header\nfunc main() {\n    d0.On = 1 // on\n    // before\n    d0.Open = 0\n}\n")
	out, diags, err := ic10.Format("t.icg", src)
	if diags.HasErrors() || err != nil {
		t.Fatalf("diags=%v err=%v", diags.Diags, err)
	}
	for _, want := range []string{"// header", "// on", "// before"} {
		if !strings.Contains(out, want) {
			t.Errorf("comment %q not preserved:\n%s", want, out)
		}
	}
}

func TestFormatPreservesUnitConstants(t *testing.T) {
	// Comments like "(kPa)" / "（%）" must not leak a unit suffix into the
	// literal, and plain values must not change.
	src := []byte("const (\n" +
		"    PresTarget = 3000.0  // 目标输出压力 (kPa)\n" +
		"    PresMin = 500.0  // 调压阀输出下限 (kPa)\n" +
		"    Deadband = 0.3  // 死区（%），抑制抖动\n" +
		"    Target = 33.3  // 目标比例\n" +
		")\n" +
		"func main() { d0.Setting = PresTarget; d1.Setting = PresMin; d2.Setting = Deadband; d3.Setting = Target }\n")
	out, diags, err := ic10.Format("t.icg", src)
	if diags.HasErrors() || err != nil {
		t.Fatalf("diags=%v err=%v", diags.Diags, err)
	}
	for _, want := range []string{"PresTarget = 3000.0", "PresMin = 500.0", "Deadband = 0.3", "Target = 33.3"} {
		if !strings.Contains(out, want) {
			t.Errorf("constant %q was changed by formatting:\n%s", want, out)
		}
	}
}

func TestStatsOf(t *testing.T) {
	s := ic10.StatsOf("move r0 1\ns d0 On r0\n")
	if s.Lines != 2 {
		t.Errorf("Lines = %d, want 2", s.Lines)
	}
	if s.RegsUsed != 1 {
		t.Errorf("RegsUsed = %d, want 1", s.RegsUsed)
	}
	if s.Bytes != len("move r0 1\ns d0 On r0\n") {
		t.Errorf("Bytes = %d", s.Bytes)
	}
}

func TestFormatDataTable(t *testing.T) {
	src := []byte("data T = [\n    1, // one\n    hash(\"Iron\"), // two\n    LogicType.Open, // three\n]\n")
	out, diags, err := ic10.Format("t.icg", src)
	if diags.HasErrors() || err != nil {
		t.Fatalf("diags=%v err=%v", diags.Diags, err)
	}
	for _, want := range []string{"// one", "// two", "// three"} {
		if !strings.Contains(out, want) {
			t.Errorf("comment %q not preserved:\n%s", want, out)
		}
	}
	twice, _, _ := ic10.Format("t.icg", []byte(out))
	if out != twice {
		t.Errorf("data table formatting is not idempotent:\n--- once ---\n%s\n--- twice ---\n%s", out, twice)
	}
}

func TestFormatGroupsAndTableSwitch(t *testing.T) {
	src := []byte("const (\n    A = 1\n    B = 2\n)\n\nfunc main() {\n    switch x table {\n    case 0: y = 1\n    case 1: y = 2\n    }\n}\n")
	out, diags, err := ic10.Format("t.icg", src)
	if diags.HasErrors() || err != nil {
		t.Fatalf("diags=%v err=%v", diags.Diags, err)
	}
	for _, want := range []string{"const (", "switch x table {"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	twice, _, _ := ic10.Format("t.icg", []byte(out))
	if out != twice {
		t.Errorf("not idempotent:\n--- once ---\n%s\n--- twice ---\n%s", out, twice)
	}
}

func TestFormatConvenienceSyntax(t *testing.T) {
	src := []byte(`func main() {
    for i := range 5 { d0.On = i }
    for i, v := range T { d0.On = i + v }
    if x := d0.Setting; x > 3 { d1.On = 1 }
    switch y := d0.Setting; y {
    case 1..5: d1.On = 1
    case 6, 7: d1.On = 0
    }
    label Outer:
    for {
        break Outer
    }
}
`)
	out, diags, err := ic10.Format("t.icg", src)
	if diags.HasErrors() || err != nil {
		t.Fatalf("diags=%v err=%v", diags.Diags, err)
	}
	for _, want := range []string{
		"for i := range 5 {",
		"for i, v := range T {",
		"if x := d0.Setting; x > 3 {",
		"switch y := d0.Setting; y {",
		"case 1..5:",
		"case 6, 7:",
		"label Outer:",
		"break Outer",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	twice, _, _ := ic10.Format("t.icg", []byte(out))
	if out != twice {
		t.Errorf("not idempotent:\n--- once ---\n%s\n--- twice ---\n%s", out, twice)
	}
}
