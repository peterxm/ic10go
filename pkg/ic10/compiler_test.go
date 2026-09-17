package ic10_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ic10go/internal/vm"
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

func TestSplitSetupLoader(t *testing.T) {
	var b strings.Builder
	b.WriteString("func main() {\n")
	// One-time constant device writes: hoistable setup.
	for i := 0; i < 2; i++ {
		for _, logic := range []string{"On", "Mode", "Setting"} {
			fmt.Fprintf(&b, "    d%d.%s = 1\n", i, logic)
		}
	}
	b.WriteString("    for {\n")
	// Enough loop lines to push the runtime over the 128-line limit.
	for i := 0; i < 124; i++ {
		b.WriteString("        yield()\n")
	}
	b.WriteString("    }\n}\n")

	res, diags, err := ic10.CompileResult("split.icg", []byte(b.String()), ic10.Options{})
	if diags.HasErrors() {
		t.Fatalf("compile errors: %v", diags.Diags)
	}
	if err != nil {
		t.Fatal(err)
	}
	if res.Loader == "" {
		t.Fatalf("expected a setup loader (runtime %d lines)", strings.Count(res.Code, "\n"))
	}
	if n := strings.Count(res.Code, "\n"); n > ic10.LimitsOf().Lines {
		t.Errorf("runtime still over the limit: %d lines", n)
	}
	for _, want := range []string{"s d0 Mode 1", "s d0 On 1", "s d1 Setting 1"} {
		if !strings.Contains(res.Loader, want) {
			t.Errorf("loader missing %q:\n%s", want, res.Loader)
		}
		if strings.Contains(res.Code, want) {
			t.Errorf("setup write %q was not removed from the runtime", want)
		}
	}
}

func TestSplitSetupWithDataSegment(t *testing.T) {
	// A program that already needs a loader (data segment) hoists its setup
	// writes into that loader even though the runtime fits.
	src := "data T = [1, 2]\nfunc main() { d0.Mode = 1; d0.On = 1; for { yield(); d1.On = T[0] } }\n"
	res, diags, err := ic10.CompileResult("ds.icg", []byte(src), ic10.Options{})
	if diags.HasErrors() {
		t.Fatalf("compile errors: %v", diags.Diags)
	}
	if err != nil {
		t.Fatal(err)
	}
	if res.Loader == "" {
		t.Fatal("expected the setup to be hoisted into the loader")
	}
	for _, want := range []string{"s d0 Mode 1", "s d0 On 1"} {
		if !strings.Contains(res.Loader, want) {
			t.Errorf("loader missing %q:\n%s", want, res.Loader)
		}
		if strings.Contains(res.Code, want) {
			t.Errorf("setup write %q was not removed from the runtime:\n%s", want, res.Code)
		}
	}
}

func TestNoLoaderWhenUnderLimit(t *testing.T) {
	res, diags, err := ic10.CompileResult("small.icg",
		[]byte("func main() { d0.Mode = 1; d0.On = 1; for { yield(); d1.Setting = d2.Setting } }\n"), ic10.Options{})
	if diags.HasErrors() {
		t.Fatalf("compile errors: %v", diags.Diags)
	}
	if err != nil {
		t.Fatal(err)
	}
	if res.Loader != "" {
		t.Errorf("unexpected loader for a small program:\n%s", res.Loader)
	}
	if !strings.Contains(res.Code, "s d0 Mode 1") {
		t.Errorf("setup write should stay in the runtime when it fits:\n%s", res.Code)
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
	if !strings.Contains(code, "s d0 Mode 2") || !strings.Contains(code, "put d0 0 2") {
		t.Errorf("enum constants not resolved:\n%s", code)
	}
}

func TestModeEnumConstants(t *testing.T) {
	src := []byte("func main() {\n    d0.Mode = DisplayMode.Percent\n    d1.Mode = PowerMode.Charging\n    d2.SoundAlert = Sound.Alarm1\n    d3.Color = Color.Red\n}\n")
	code, diags, err := ic10.Compile("test.icg", src)
	if diags.HasErrors() || err != nil {
		t.Fatalf("compile: diags=%v err=%v", diags.Diags, err)
	}
	for _, want := range []string{"s d0 Mode 1", "s d1 Mode 3", "s d2 SoundAlert 45", "s d3 Color 4"} {
		if !strings.Contains(code, want) {
			t.Errorf("missing %q in:\n%s", want, code)
		}
	}
}

func TestDeviceAndTableParams(t *testing.T) {
	src := []byte("const sensor = d0\n" +
		"data T = [ 10, 20, 30 ]\n\n" +
		"func sumSlots(dev, first, last) num {\n" +
		"    n := 0\n" +
		"    for i := first; i < last; i++ {\n" +
		"        n += dev.slot[i].Occupied\n" +
		"    }\n" +
		"    return n\n" +
		"}\n\n" +
		"func load(tbl) {\n" +
		"    for i := 0; i < 3; i++ {\n" +
		"        put(db, i, tbl[i])\n" +
		"    }\n" +
		"}\n\n" +
		"func main() {\n" +
		"    if sumSlots(sensor, 2, 4) > 0 {\n" +
		"        load(T)\n" +
		"    }\n" +
		"}\n")
	code, diags, err := ic10.Compile("test.icg", src)
	if diags.HasErrors() || err != nil {
		t.Fatalf("compile: diags=%v err=%v", diags.Diags, err)
	}
	for _, want := range []string{"ls r0 d0 r1 Occupied", "put db 0 r0", "get r0 db 509"} {
		if !strings.Contains(code, want) {
			t.Errorf("missing %q in:\n%s", want, code)
		}
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

func TestLoopUnrollTable(t *testing.T) {
	src := []byte("data T = [10, 20, 30]\nfunc main() {\n    for i := 0; i < 3; i++ {\n        d0.Setting = T[i]\n    }\n}")
	code, diags, err := ic10.Compile("t.icg", src)
	if diags.HasErrors() || err != nil {
		t.Fatal(err, diags.Diags)
	}
	// The loop is unrolled and each read uses a constant stack address.
	if strings.Contains(code, "add r0 ") && strings.Contains(code, "get r0 db r0") {
		t.Errorf("loop not unrolled / index not folded:\n%s", code)
	}
	loader, err := ic10.DataLoader("t.icg", src)
	if err != nil || loader == "" {
		t.Fatalf("loader: %v", err)
	}
	m := vm.New()
	if err := m.Load(loader); err != nil {
		t.Fatal(err)
	}
	if err := m.Run(1000); err != nil {
		t.Fatal(err)
	}
	if err := m.Load(code); err != nil {
		t.Fatal(err)
	}
	if err := m.Run(1000); err != nil && err != vm.ErrStepLimit {
		t.Fatal(err)
	}
	if got := m.Get("d0", "Setting"); got != 30 {
		t.Errorf("d0.Setting = %v, want 30 (T[2])", got)
	}
}

func TestJumpTableEquivalence(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("func main() {\n    switch d0.Setting {\n")
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&sb, "    case %d: d1.On = %d\n", i, i+1)
	}
	sb.WriteString("    }\n}\n")
	src := []byte(sb.String())

	plain, _, err := ic10.Compile("t.icg", src)
	if err != nil {
		t.Fatal(err)
	}
	jt, _, err := ic10.CompileWithOptions("t.icg", src, ic10.Options{JumpTable: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jt, "jr ") {
		t.Fatalf("jump table not emitted:\n%s", jt)
	}
	for tag := -1; tag <= 10; tag++ {
		init := map[[2]string]float64{{"d0", "Setting"}: float64(tag)}
		wa, _ := runWrites(plain, init)
		wb, _ := runWrites(jt, init)
		if strings.Join(wa, "|") != strings.Join(wb, "|") {
			t.Errorf("tag %d: plain %v, jump-table %v", tag, wa, wb)
		}
	}
}

func TestMultiChipCompile(t *testing.T) {
	src := "const Shared = 10\n" +
		"func helper(x) num { return x + Shared }\n" +
		"chip control {\n    func main() { for { yield(); d0.Setting = helper(1) } }\n}\n" +
		"chip display {\n    func main() { for { yield(); d1.On = Shared } }\n}\n"
	res, diags, err := ic10.CompileResult("multi.icg", []byte(src), ic10.Options{})
	if diags.HasErrors() {
		t.Fatalf("compile errors: %v", diags.Diags)
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Chips) != 2 {
		t.Fatalf("chips = %d, want 2", len(res.Chips))
	}
	if res.Chips[0].Name != "control" || res.Chips[1].Name != "display" {
		t.Fatalf("chip names = %q, %q", res.Chips[0].Name, res.Chips[1].Name)
	}
	if !strings.Contains(res.Chips[0].Code, "s d0 Setting 11") {
		t.Errorf("control chip should fold helper(1) to 11:\n%s", res.Chips[0].Code)
	}
	if !strings.Contains(res.Chips[1].Code, "s d1 On 10") {
		t.Errorf("display chip should use the shared const:\n%s", res.Chips[1].Code)
	}
	if res.Code != res.Chips[0].Code {
		t.Errorf("Result.Code should mirror the first chip")
	}
}

func TestMultiChipShadowing(t *testing.T) {
	src := "const X = 1\n" +
		"chip a {\n    const X = 2\n    func main() { d0.Setting = X }\n}\n" +
		"chip b {\n    func main() { d0.Setting = X }\n}\n"
	res, diags, err := ic10.CompileResult("shadow.icg", []byte(src), ic10.Options{})
	if diags.HasErrors() {
		t.Fatalf("compile errors: %v", diags.Diags)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Chips[0].Code, "s d0 Setting 2") {
		t.Errorf("chip-local const should shadow the top-level one:\n%s", res.Chips[0].Code)
	}
	if !strings.Contains(res.Chips[1].Code, "s d0 Setting 1") {
		t.Errorf("chip without a local const should see the top-level one:\n%s", res.Chips[1].Code)
	}
}

func TestMultiChipNoMain(t *testing.T) {
	_, diags, _ := ic10.CompileResult("nomain.icg", []byte("chip a { func helper() {} }\n"), ic10.Options{})
	if !diags.HasErrors() {
		t.Fatal("expected an error for a chip without main")
	}
	found := false
	for _, d := range diags.Diags {
		if strings.Contains(d.Msg, `chip "a" has no main`) {
			found = true
		}
	}
	if !found {
		t.Errorf("missing chip-no-main diagnostic: %v", diags.Diags)
	}
}

func TestMultiChipTopMainRejected(t *testing.T) {
	_, diags, _ := ic10.CompileResult("mixed.icg",
		[]byte("func main() {}\nchip a { func main() {} }\n"), ic10.Options{})
	if !diags.HasErrors() {
		t.Fatal("expected an error when a top-level main is mixed with chip blocks")
	}
}

func TestBusChannelMapping(t *testing.T) {
	src := "bus B {\n    a num\n    b num\n}\n" +
		"chip c {\n    use B on db:0\n    func main() { B.a = d1.Pressure; B.b = 1 }\n}\n" +
		"chip d {\n    use B on d2:1\n    func main() { d0.Setting = B.a; d1.On = B.b }\n}\n"
	res, diags, err := ic10.CompileResult("bus.icg", []byte(src), ic10.Options{})
	if diags.HasErrors() {
		t.Fatalf("compile errors: %v", diags.Diags)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Chips[0].Code, "s db:0 Channel0") || !strings.Contains(res.Chips[0].Code, "s db:0 Channel1") {
		t.Errorf("producer should write its own access point:\n%s", res.Chips[0].Code)
	}
	if !strings.Contains(res.Chips[1].Code, "l r0 d2:1 Channel0") || !strings.Contains(res.Chips[1].Code, "l r0 d2:1 Channel1") {
		t.Errorf("consumer should read its own access point:\n%s", res.Chips[1].Code)
	}
	if got := res.Chips[0].BusAccess["B.a"]; len(got) != 1 || got[0] != "db:0" {
		t.Errorf("chip c B.a access = %v, want [db:0]", got)
	}
	if got := res.Chips[1].BusAccess["B.a"]; len(got) != 1 || got[0] != "d2:1" {
		t.Errorf("chip d B.a access = %v, want [d2:1]", got)
	}
}

func TestBusInlineAccess(t *testing.T) {
	// Inline [dev][conn] overrides the default; the channel is the slot index.
	src := "bus B {\n    a num\n    b num\n}\n" +
		"chip c {\n    use B on db:0\n    func main() { B.a[d5][1] = d1.Pressure; B.b[d4][2] = 1 }\n}\n" +
		"chip d {\n    func main() { d0.Setting = B.a[d3][0]; d1.On = B.b[d2][3] }\n}\n"
	res, diags, err := ic10.CompileResult("bus.icg", []byte(src), ic10.Options{})
	if diags.HasErrors() {
		t.Fatalf("compile errors: %v", diags.Diags)
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"s d5:1 Channel0", "s d4:2 Channel1"} {
		if !strings.Contains(res.Chips[0].Code, want) {
			t.Errorf("producer missing %q:\n%s", want, res.Chips[0].Code)
		}
	}
	for _, want := range []string{"l r0 d3:0 Channel0", "l r0 d2:3 Channel1"} {
		if !strings.Contains(res.Chips[1].Code, want) {
			t.Errorf("consumer missing %q:\n%s", want, res.Chips[1].Code)
		}
	}
	if got := res.Chips[1].BusAccess["B.a"]; len(got) != 1 || got[0] != "d3:0" {
		t.Errorf("chip d B.a access = %v, want [d3:0]", got)
	}
}

func TestBusTooManySlots(t *testing.T) {
	src := "bus B {\n    s0 num\n    s1 num\n    s2 num\n    s3 num\n" +
		"    s4 num\n    s5 num\n    s6 num\n    s7 num\n    s8 num\n}\n" +
		"chip a {\n    use B on db:0\n    func main() { B.s0 = 1 }\n}\n"
	_, diags, _ := ic10.CompileResult("bus.icg", []byte(src), ic10.Options{})
	if !diags.HasErrors() {
		t.Fatal("expected an error for 9 slots bound to a single connection")
	}
}

func TestBusNoWriter(t *testing.T) {
	src := "bus B {\n    x num\n}\n" +
		"chip a {\n    use B on db:0\n    func main() { d0.On = B.x }\n}\n"
	_, diags, _ := ic10.CompileResult("bus.icg", []byte(src), ic10.Options{})
	if !diags.HasErrors() {
		t.Fatal("expected an error for a bus slot with no writer")
	}
}

func TestBusMultiWriter(t *testing.T) {
	src := "bus B {\n    x num\n}\n" +
		"chip a {\n    use B on db:0\n    func main() { B.x = 1 }\n}\n" +
		"chip b {\n    use B on db:0\n    func main() { B.x = 2 }\n}\n"
	_, diags, _ := ic10.CompileResult("bus.icg", []byte(src), ic10.Options{})
	if !diags.HasErrors() {
		t.Fatal("expected an error for a bus slot written by two chips")
	}
}

func TestBusNotBound(t *testing.T) {
	src := "bus B {\n    x num\n}\n" +
		"chip a {\n    func main() { B.x = 1 }\n}\n"
	_, diags, _ := ic10.CompileResult("bus.icg", []byte(src), ic10.Options{})
	if !diags.HasErrors() {
		t.Fatal("expected an error for using a bus without `use`")
	}
}

func TestBusDeviceAlias(t *testing.T) {
	src := "const Mem = d3\n" +
		"bus B {\n    x num\n}\n" +
		"chip a {\n    use B on Mem:1\n    func main() { B.x = 1 }\n}\n"
	res, diags, err := ic10.CompileResult("bus.icg", []byte(src), ic10.Options{})
	if diags.HasErrors() {
		t.Fatalf("compile errors: %v", diags.Diags)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Chips[0].Code, "s d3:1 Channel0") {
		t.Errorf("device alias should resolve to d3:\n%s", res.Chips[0].Code)
	}
}

func TestBusInlineDeviceAlias(t *testing.T) {
	src := "const Mem = d3\n" +
		"bus B {\n    x num\n}\n" +
		"chip a {\n    func main() { B.x[Mem][1] = 1 }\n}\n"
	res, diags, err := ic10.CompileResult("bus.icg", []byte(src), ic10.Options{})
	if diags.HasErrors() {
		t.Fatalf("compile errors: %v", diags.Diags)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Chips[0].Code, "s d3:1 Channel0") {
		t.Errorf("inline device alias should resolve to d3:\n%s", res.Chips[0].Code)
	}
}

// TestLabelAddressValue checks that a label used as a value (the IC10 idiom of
// comparing a stored return address against a code label) compiles to that
// label's absolute line number and behaves correctly in the VM.
func TestLabelAddressValue(t *testing.T) {
	src := `func main() {
    r15 := 0
    goto work
    label home:
    d0.Setting = 1
    jump(9999)
    label work:
    call travel
    d0.Setting = 2
    jump(9999)
    label travel:
    r15 = ra
    if r15 < work { goto home }
    jump(r15)
}`
	code := mustCompile(t, src)
	if strings.Contains(code, "\x01") {
		t.Fatalf("label placeholder leaked into output:\n%s", code)
	}
	m := vm.New()
	if err := m.Load(code); err != nil {
		t.Fatal(err)
	}
	if err := m.Run(200); err != nil && err != vm.ErrStepLimit {
		t.Fatalf("run: %v", err)
	}
	if got := m.Get("d0", "Setting"); got != 2 {
		t.Errorf("d0.Setting = %v, want 2\n%s", got, code)
	}
}

// TestSpillModes checks that the default db spill mode is shorter than the
// stack fallback and leaves the devices in the same state.
func TestSpillModes(t *testing.T) {
	// 17 distinct live values force spilling.
	src := "func main() {"
	for i := 0; i < 17; i++ {
		src += fmt.Sprintf(" x%d := d0.Temperature + %d", i, i)
	}
	src += " d0.Setting ="
	for i := 0; i < 17; i++ {
		if i > 0 {
			src += " +"
		}
		src += fmt.Sprintf(" x%d", i)
	}
	src += " }\n"

	dbCode, diags, err := ic10.CompileWithOptions("t.icg", []byte(src), ic10.Options{})
	if diags.HasErrors() || err != nil {
		t.Fatalf("db compile: %v %v", diags.Diags, err)
	}
	stackCode, diags, err := ic10.CompileWithOptions("t.icg", []byte(src), ic10.Options{SpillStack: true})
	if diags.HasErrors() || err != nil {
		t.Fatalf("stack compile: %v %v", diags.Diags, err)
	}

	if n, m := countLines(dbCode), countLines(stackCode); n >= m {
		t.Errorf("db spills should be shorter: db=%d stack=%d", n, m)
	}
	if !strings.Contains(dbCode, " db ") {
		t.Errorf("db spill mode did not emit get/put db:\n%s", dbCode)
	}
	if strings.Contains(dbCode, "peek") || strings.Contains(dbCode, "poke") {
		t.Errorf("db spill mode still uses peek/poke:\n%s", dbCode)
	}

	runBoth := func(code string) *vm.Machine {
		m := vm.New()
		for i := 0; i < 6; i++ {
			m.Set(fmt.Sprintf("d%d", i), "Temperature", float64(i+1))
		}
		if err := m.Load(code); err != nil {
			t.Fatal(err)
		}
		if err := m.Run(500); err != nil && err != vm.ErrStepLimit {
			t.Fatalf("run: %v", err)
		}
		return m
	}
	a, b := runBoth(dbCode), runBoth(stackCode)
	if a.Get("d0", "Setting") != b.Get("d0", "Setting") {
		t.Errorf("db=%v stack=%v", a.Get("d0", "Setting"), b.Get("d0", "Setting"))
	}
	// x_i = 1 + i, sum(1..17) = 153.
	if got := a.Get("d0", "Setting"); got != 153 {
		t.Errorf("spilled sum = %v, want 153", got)
	}
}

// TestReservedIndirectRegs checks that reserveRegs keeps the allocator away
// from the reserved physical registers and that constant ireg/setIreg address
// them directly.
func TestReservedIndirectRegs(t *testing.T) {
	src := `func main() {
    reserveRegs(2, 4)
    ptr := 2
    setIreg(ptr, 10)
    ptr = ptr + 1
    setIreg(ptr, 20)
    ptr = ptr + 1
    setIreg(ptr, 30)
    d0.Setting = ireg(2) + ireg(3) + ireg(4)
}`
	code := mustCompile(t, src)
	if !strings.Contains(code, "move r2 10") || !strings.Contains(code, "move r3 20") ||
		!strings.Contains(code, "move r4 30") {
		t.Errorf("reserved registers not addressed directly:\n%s", code)
	}
	m := vm.New()
	if err := m.Load(code); err != nil {
		t.Fatal(err)
	}
	if err := m.Run(50); err != nil && err != vm.ErrStepLimit {
		t.Fatal(err)
	}
	if got := m.Get("d0", "Setting"); got != 60 {
		t.Errorf("d0.Setting = %v, want 60\n%s", got, code)
	}
}

// TestIndirectRegsRuntimePointer checks that a runtime pointer emits rrN and
// that the pointed-to register is only touched indirectly.
func TestIndirectRegsRuntimePointer(t *testing.T) {
	src := `func main() {
    reserveRegs(2, 3)
    ptr := d0.Setting
    ptr = ptr + 2
    setIreg(ptr, 42)
    d0.Setting = ireg(ptr)
}`
	code := mustCompile(t, src)
	if !strings.Contains(code, "rr") {
		t.Errorf("runtime pointer should emit rrN:\n%s", code)
	}
	m := vm.New()
	m.Set("d0", "Setting", 0) // ptr = 2
	if err := m.Load(code); err != nil {
		t.Fatal(err)
	}
	if err := m.Run(50); err != nil && err != vm.ErrStepLimit {
		t.Fatal(err)
	}
	if got := m.Get("d0", "Setting"); got != 42 {
		t.Errorf("d0.Setting = %v, want 42\n%s", got, code)
	}
}
