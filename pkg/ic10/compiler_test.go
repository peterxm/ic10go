package ic10_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ic10go/pkg/ic10"
)

var update = flag.Bool("update", false, "update golden files")

func TestGolden(t *testing.T) {
	programs, err := filepath.Glob("../../testdata/programs/*.icg")
	if err != nil {
		t.Fatal(err)
	}
	if len(programs) == 0 {
		t.Fatal("no test programs found")
	}
	for _, p := range programs {
		name := strings.TrimSuffix(filepath.Base(p), ".icg")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			code, diags, err := ic10.Compile(p, src)
			if diags.HasErrors() {
				t.Fatalf("compile errors: %v", diags.Diags)
			}
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("../../testdata/golden", name+".ic")
			if *update {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(code), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing golden file (run: go test ./pkg/ic10 -update): %v", err)
			}
			if code != string(want) {
				t.Errorf("output mismatch\n--- got ---\n%s\n--- want ---\n%s", code, want)
			}
		})
	}
}

func TestHashIsSignedInt32(t *testing.T) {
	code, diags, err := ic10.Compile("test.icg", []byte(`func main() { d0.Setting = hash("StructureBattery") }`))
	if diags.HasErrors() || err != nil {
		t.Fatalf("compile: diags=%v err=%v", diags.Diags, err)
	}
	if !strings.Contains(code, "-400115994") {
		t.Errorf("hash() should emit the signed int32 form, got:\n%s", code)
	}
}

func TestNoMain(t *testing.T) {
	_, diags, _ := ic10.Compile("test.icg", []byte("const A = 1\n"))
	if !diags.HasErrors() {
		t.Fatal("expected an error for a missing main function")
	}
}

func TestRecursionRejected(t *testing.T) {
	src := []byte("func f() num { return f() }\nfunc main() { d0.On = f() }\n")
	_, diags, _ := ic10.Compile("test.icg", src)
	if !diags.HasErrors() {
		t.Fatal("expected recursion to be rejected")
	}
}

func TestModSignFollowsDivisor(t *testing.T) {
	code, diags, err := ic10.Compile("test.icg", []byte("func main() { d0.Setting = -7 % 3 }\n"))
	if diags.HasErrors() || err != nil {
		t.Fatalf("diags=%v err=%v", diags.Diags, err)
	}
	if code != "s d0 Setting 2\n" {
		t.Errorf("got %q, want %q", code, "s d0 Setting 2\n")
	}
}

func TestMainWithParamsRejected(t *testing.T) {
	_, diags, _ := ic10.Compile("test.icg", []byte("func main(x num) { d0.On = x }\n"))
	if !diags.HasErrors() {
		t.Fatal("expected main with parameters to be rejected")
	}
}

func TestUndefinedLabelRejected(t *testing.T) {
	_, diags, _ := ic10.Compile("test.icg", []byte("func main() { goto nope; d0.On = 1 }\n"))
	if !diags.HasErrors() {
		t.Fatal("expected an undefined label error")
	}
}

func TestDuplicateLabelRejected(t *testing.T) {
	src := "func main() { label a:; d0.On = 1; label a:; d0.Open = 1 }\n"
	_, diags, _ := ic10.Compile("test.icg", []byte(src))
	if !diags.HasErrors() {
		t.Fatal("expected a duplicate label error")
	}
}

func TestConstHashAndStr(t *testing.T) {
	code := mustCompile(t, "const H = hash(\"StructureBattery\")\nconst M = str(\"Ready!\")\nfunc main() { d0.Setting = H; d1.Setting = M }\n")
	if !strings.Contains(code, "s d0 Setting ") {
		t.Errorf("const hash not folded:\n%s", code)
	}
	if !strings.Contains(code, `STR("Ready!")`) {
		t.Errorf("const str not emitted:\n%s", code)
	}
}

func TestJumpBuiltin(t *testing.T) {
	code := mustCompile(t, "func main() { x := 3\n jump(x)\n d0.On = 1 }\n")
	if !strings.Contains(code, "j r") {
		t.Errorf("computed jump not emitted:\n%s", code)
	}
}

func TestDynamicLogicType(t *testing.T) {
	code := mustCompile(t, "func main() { lt := 3\n d0.Setting = read(d1, lt)\n write(d1, lt, 1) }\n")
	if !strings.Contains(code, "l r") || !strings.Contains(code, " d1 r") {
		t.Errorf("dynamic read not emitted:\n%s", code)
	}
	if !strings.Contains(code, "s d1 r") {
		t.Errorf("dynamic write not emitted:\n%s", code)
	}
}

func TestStableInsOrder(t *testing.T) {
	src := []byte("func main() { x := ins(1, 8, 8)\n d0.Setting = x }\n")
	code, diags, err := ic10.Compile("t.icg", src)
	if diags.HasErrors() || err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(code, "ins r0 1 8 8") {
		t.Errorf("documented ins order not emitted:\n%s", code)
	}
	stable, diags, err := ic10.CompileWithOptions("t.icg", src, ic10.Options{StableInsOrder: true})
	if diags.HasErrors() || err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stable, "ins r0 8 8 1") {
		t.Errorf("stable ins order not emitted:\n%s", stable)
	}
}

func TestDeviceValidityBranch(t *testing.T) {
	code := mustCompile(t, "func main() { if !isLoadValid(d0, \"Temperature\") { d1.On = 0 } else { d1.On = 1 } }\n")
	if !strings.Contains(code, "bdnvl d0 Temperature") {
		t.Errorf("bdnvl not emitted:\n%s", code)
	}
	code = mustCompile(t, "func main() { if isStoreValid(d0, \"On\") { d1.On = 1 } }\n")
	if !strings.Contains(code, "bdnvs d0 On") {
		t.Errorf("bdnvs not emitted:\n%s", code)
	}
}

func TestUnknownLogicTypeWarns(t *testing.T) {
	_, diags, _ := ic10.Compile("test.icg", []byte("func main() { d0.Temperatur = 1 }\n"))
	if diags.HasErrors() {
		t.Fatal("a typo should warn, not error")
	}
	if len(diags.Diags) == 0 {
		t.Fatal("expected a warning for an unknown logic type")
	}
	if !strings.Contains(diags.Diags[0].Msg, "unknown logic type") {
		t.Errorf("unexpected diagnostic: %s", diags.Diags[0].Msg)
	}
}

func TestRedundantLoadEliminated(t *testing.T) {
	code := mustCompile(t, "func main() { x := d0.Temperature; y := d0.Temperature; d1.Setting = x + y }\n")
	if n := strings.Count(code, "l r"); n != 1 {
		t.Errorf("expected one load, got %d:\n%s", n, code)
	}
}

func TestAlgebraicSimplification(t *testing.T) {
	code := mustCompile(t, "func main() { x := d0.Temperature; d1.Setting = x * 1; d2.Setting = x + 0; d3.Setting = x / 1 }\n")
	if strings.Contains(code, "mul") || strings.Contains(code, "div") || strings.Contains(code, "add") {
		t.Errorf("identities not simplified:\n%s", code)
	}
}

func TestConstantBranchFolding(t *testing.T) {
	code := mustCompile(t, "func main() { if 1 > 0 { d0.On = 1 } else { d0.On = 0 } }\n")
	if code != "s d0 On 1\n" {
		t.Errorf("constant branch not folded:\n%s", code)
	}
}

func TestGlobalConstantPropagation(t *testing.T) {
	src := "func main() { x := 7\n if d0.On > 0 { d1.Setting = x } else { d2.Setting = x } }\n"
	code := mustCompile(t, src)
	if !strings.Contains(code, "s d1 Setting 7") || !strings.Contains(code, "s d2 Setting 7") {
		t.Errorf("constant not propagated across blocks:\n%s", code)
	}
}

func mustCompile(t *testing.T, src string) string {
	t.Helper()
	code, diags, err := ic10.Compile("test.icg", []byte(src))
	if diags.HasErrors() {
		t.Fatalf("compile errors: %v", diags.Diags)
	}
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestTernaryBecomesSelect(t *testing.T) {
	code := mustCompile(t, "func main() { d0.Setting = d1.On > 0 ? 1 : 2 }\n")
	if !strings.Contains(code, "select ") {
		t.Errorf("expected a select instruction, got:\n%s", code)
	}
}

func TestPureAndBecomesMin(t *testing.T) {
	code := mustCompile(t, "func main() { d0.On = d1.On > 0 && d2.On > 0 }\n")
	if !strings.Contains(code, "min ") {
		t.Errorf("expected min for pure &&, got:\n%s", code)
	}
}

func TestCompareBranchFusion(t *testing.T) {
	code := mustCompile(t, "func main() { for { if d0.Temperature < 100 { d1.On = 1 } } }\n")
	if !strings.Contains(code, "bge ") {
		t.Errorf("expected a fused branch, got:\n%s", code)
	}
	if strings.Contains(code, "slt ") {
		t.Errorf("comparison should have been fused, got:\n%s", code)
	}
}

func TestDeadStoreEliminated(t *testing.T) {
	code := mustCompile(t, "func main() { x := 0; if d0.On > 0 { x = 1 } else { x = 2 }; d1.Setting = x }\n")
	if strings.HasPrefix(code, "move ") {
		t.Errorf("dead store was not eliminated:\n%s", code)
	}
}

func TestBatchIO(t *testing.T) {
	code := mustCompile(t, "func main() { x := batch.read(-400115994, \"Charge\", \"Sum\"); batch.writeName(1, hash(\"N\"), \"On\", x) }\n")
	if !strings.Contains(code, "lb r") || !strings.Contains(code, "Charge 1") {
		t.Errorf("expected lb with Sum mode:\n%s", code)
	}
	if !strings.Contains(code, "sbn 1 ") {
		t.Errorf("expected sbn:\n%s", code)
	}
}

func TestChannel(t *testing.T) {
	code := mustCompile(t, "func main() { d0.channel[1][3] = 1 }\n")
	if code != "s d0:1 Channel3 1\n" {
		t.Errorf("got %q", code)
	}
}

func TestStrLiteral(t *testing.T) {
	code := mustCompile(t, "func main() { d0.Setting = str(\"Ready!\") }\n")
	if !strings.Contains(code, `STR("Ready!")`) {
		t.Errorf("got:\n%s", code)
	}
}

func TestDeviceHelpers(t *testing.T) {
	code := mustCompile(t, "func main() { d0.On = isSet(d1); d0.Mode = rmap(d1, hash(\"Iron\")) }\n")
	if !strings.Contains(code, "sdse ") {
		t.Errorf("expected sdse:\n%s", code)
	}
	if !strings.Contains(code, "rmap ") {
		t.Errorf("expected rmap:\n%s", code)
	}
}

func TestStackOps(t *testing.T) {
	code := mustCompile(t, "func main() { push(1); x := pop(); y := peek(); poke(0, y); d0.Setting = x }\n")
	for _, want := range []string{"push ", "pop ", "peek ", "poke "} {
		if !strings.Contains(code, want) {
			t.Errorf("missing %q in:\n%s", want, code)
		}
	}
}

func TestEnumConstants(t *testing.T) {
	src := []byte("func main() {\n    d0.Mode = ReagentMode.Recipe\n    put(d0, 0, PrinterInstruction.ExecuteRecipe)\n}\n")
	code, diags, err := ic10.Compile("test.icg", src)
	if diags.HasErrors() || err != nil {
		t.Fatalf("compile: diags=%v err=%v", diags.Diags, err)
	}
	if !strings.Contains(code, "s d0 Mode 2") || !strings.Contains(code, "put d0 0 1") {
		t.Errorf("enum constants not resolved:\n%s", code)
	}
}

func TestDeviceAlias(t *testing.T) {
	src := []byte("const sensor = d0\nconst pump = sensor\nconst host = db\nfunc main() {\n    d1.Setting = sensor.Temperature\n    pump.On = 1\n    d2.Setting = sensor.slot[0].Occupied\n    put(host, 0, 1)\n    d0.On = isSet(sensor)\n}\n")
	code, diags, err := ic10.Compile("test.icg", src)
	if diags.HasErrors() || err != nil {
		t.Fatalf("compile: diags=%v err=%v", diags.Diags, err)
	}
	for _, want := range []string{"l r0 d0 Temperature", "s d0 On 1", "ls r0 d0 0 Occupied", "put db 0 1", "sdse r0 d0"} {
		if !strings.Contains(code, want) {
			t.Errorf("device alias not resolved (%q missing):\n%s", want, code)
		}
	}
}

func TestDeviceAliasRedeclared(t *testing.T) {
	_, diags, _ := ic10.Compile("test.icg", []byte("const a = d0\nconst a = d1\nfunc main() {}\n"))
	if !diags.HasErrors() {
		t.Fatal("expected a redeclaration error")
	}
}

func TestSizeReport(t *testing.T) {
	src := []byte(`func helper(a num, b num) num {
    x := a + b
    y := x * 2
    z := y - a
    w := z + b
    return w
}

func main() {
    p := d0.Setting
    q := d1.Setting
    d2.Setting = helper(p, q)
    d3.Setting = helper(q, p)
    d4.Setting = helper(p, p)
}`)
	rep, err := ic10.Size("t.icg", src, ic10.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total <= 0 {
		t.Fatalf("total = %d, want > 0", rep.Total)
	}
	sum := 0
	for _, n := range rep.ByFunc {
		sum += n
	}
	if sum != rep.Total {
		t.Errorf("ByFunc sums to %d, want %d", sum, rep.Total)
	}
	// The report must agree with what Compile produces.
	code := mustCompile(t, string(src))
	if got := strings.Count(code, "\n"); got != rep.Total {
		t.Errorf("report total = %d, compiled lines = %d", rep.Total, got)
	}
}

func TestPureFuncShortCircuitUsesMax(t *testing.T) {
	src := `func open(d num) num { return batch.read(d, "Open", "Maximum") }
func main() {
    a := d0.Setting
    b := d1.Setting
    d2.On = open(a) || open(b)
}`
	code := mustCompile(t, src)
	if !strings.Contains(code, "max") {
		t.Errorf("expected `||` over pure calls to lower to max, got:\n%s", code)
	}
}

func TestFunctionSpecialization(t *testing.T) {
	src := `func helper(a num, b num) num {
    x := a + b
    y := x * 2
    z := y - a
    w := z + b
    v := w * w
    return v
}

func main() {
    d0.Setting = helper(1, 2)
    p := d1.Setting
    q := d2.Setting
    r := d3.Setting
    d4.Setting = helper(p, q)
    d5.Setting = helper(q, r)
    db.Setting = helper(r, p)
}`
	code := mustCompile(t, src)
	if !strings.Contains(code, "s d0 Setting 49") {
		t.Errorf("expected the constant call to fold, got:\n%s", code)
	}
	if !strings.Contains(code, "jal") {
		t.Errorf("expected the variable calls to be outlined, got:\n%s", code)
	}
}
