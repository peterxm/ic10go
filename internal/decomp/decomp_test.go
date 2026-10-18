package decomp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ic10go/internal/builtin"
)

func TestDecompileBasic(t *testing.T) {
	src := `define X 5
alias s d0
l r0 s Temperature
add r1 r0 X
s s Setting r1
`
	code, warns, err := Decompile(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	for _, want := range []string{
		"r0 := d0.Temperature",
		"r1 := (r0 + 5)",
		"d0.Setting = r1",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("output missing %q:\n%s", want, code)
		}
	}
}

// A relative computed jump (jr rX) has no .icg form -- jump(expr) is the
// absolute `j rX`. It used to be emitted as jump(reg), which compiles and jumps
// somewhere else entirely, so a --jump-table program round-tripped to wrong
// values. It must be reported instead (review D2).
func TestDecompileRelativeComputedJumpIsReported(t *testing.T) {
	// A jump table: the classic `jr rX` idiom the compiler emits for a switch.
	src := "sltz r1 r0\nsgt r2 r0 1\nor r1 r1 r2\nbnez r1 8\nadd r0 r0 1\njr r0\nj 8\nj 5\ns d0 Setting 1\nj 0\ns d0 Setting 2\nj 0\n"
	code, warns, err := Decompile(src)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range warns {
		if strings.Contains(w.Text, "jr r0") {
			found = true
		}
	}
	if !found {
		t.Errorf("jr with a register operand must be reported: %v", warns)
	}
	if strings.Contains(code, "jump(r0)") {
		t.Errorf("a relative computed jump must not be rendered as the absolute jump():\n%s", code)
	}
	if !strings.Contains(code, "// unsupported: jr r0") {
		t.Errorf("the jr line should be kept as a comment:\n%s", code)
	}
}

func TestDecompileControlFlow(t *testing.T) {
	src := `loop:
add r0 r0 1
blt r0 5 loop
jal helper
j ra
helper:
mul r0 r0 2
j ra
`
	code, warns, err := Decompile(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	for _, want := range []string{
		"label loop:",
		"if r0 < 5 { goto loop }",
		"call helper",
		"ret",
		"label helper:",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("output missing %q:\n%s", want, code)
		}
	}
}

func TestDecompileBatchAndHash(t *testing.T) {
	src := `lbn r0 -400115994 HASH("Bank 1") Ratio 0
sbn 1220484876 HASH("Override") Open 1
`
	code, warns, err := Decompile(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if !strings.Contains(code, `batch.readName(hash("StructureBattery"), hash("Bank 1"), "Ratio", "Average")`) {
		t.Errorf("batch read not translated:\n%s", code)
	}
	if !strings.Contains(code, `batch.writeName(hash("StructureLogicSwitch"), hash("Override"), "Open", 1)`) {
		t.Errorf("batch write not translated:\n%s", code)
	}
}

func TestDecompileDynamicDevice(t *testing.T) {
	src := `s dr13 Open 0
l r3 dr13 Open
ls r0 dr13 0 Occupied
ss dr12 1 On r4
`
	code, warns, err := Decompile(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	for _, want := range []string{
		"writeDev(r13, LogicType.Open, 0)",
		"r3 := readDev(r13, LogicType.Open)",
		"readDevSlot(r13, 0, Occupied)",
		"writeDevSlot(r12, 1, On, r4)",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("output missing %q:\n%s", want, code)
		}
	}
}

func TestDecompileIndirect(t *testing.T) {
	// r5 is loaded from a device, so its value (and therefore the register the
	// pointer names) is unknown: the translator reports that it cannot bound it.
	src := `l r5 db Setting
trunc rr5 r0
mod r0 r0 rr5
`
	code, warns, err := Decompile(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 {
		t.Fatalf("expected an unbounded-pointer warning, got %v", warns)
	}
	if !strings.Contains(code, "setIreg(r5,") {
		t.Errorf("indirect store not translated:\n%s", code)
	}
	if !strings.Contains(code, "ireg(r5)") {
		t.Errorf("indirect read not translated:\n%s", code)
	}
}

func TestDecompileIndirectZeroInit(t *testing.T) {
	// r5 is never written, so it is 0 (registers start at 0) and the pointer
	// names r0. The range is bounded and the accesses are pinned: the initial
	// index of a hash table in registers often comes from this.
	code, warns, err := Decompile("trunc rr5 r0\nmod r0 r0 rr5\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	for _, want := range []string{"reserveRegs(0, 0)", "setIreg(0,", "setIreg(r5,", "ireg(0)", "ireg(r5)"} {
		if !strings.Contains(code, want) {
			t.Errorf("missing %q:\n%s", want, code)
		}
	}
}

func TestDecompileReadFirstDeclaration(t *testing.T) {
	// r1 is read as the select condition before it is written.
	code, warns, err := Decompile("select r1 r1 45000 40000\ns d0 Setting r1\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if !strings.Contains(code, "var r1 = 0") {
		t.Errorf("read-before-write register should be declared:\n%s", code)
	}
	if strings.Contains(code, "r1 :=") {
		t.Errorf("r1 should be assigned with =, not :=\n%s", code)
	}
}

func TestDecompileStructuredSplitsAtTarget(t *testing.T) {
	// brnez r0 2 jumps into the middle of what would otherwise be one block.
	src := "move r0 0\nbrnez r0 2\nmove r1 1\nmove r2 2\ns d0 Setting r2\n"
	code, warns, err := DecompileStructured(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if !strings.Contains(code, "if ") {
		t.Errorf("branch not structured into an if:\n%s", code)
	}
	// Structuring consumed the branch, so the target label is not referenced
	// and must not be left behind.
	if strings.Contains(code, "label L3:") {
		t.Errorf("unused branch-target label should be dropped:\n%s", code)
	}
}

func TestDecompileStructuredDropsLoopHeaderLabel(t *testing.T) {
	// A backward goto becomes a `for`, so the loop header's label is unused
	// (this is the shape of a real in-game chip).
	src := "main:\nl r1 d0 Pressure\nslt r2 r1 7000\ns d1 On r2\nyield\nj main\n"
	code, _, err := DecompileStructured(src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(code, "for {") {
		t.Errorf("back-edge not structured into a loop:\n%s", code)
	}
	if strings.Contains(code, "label main:") {
		t.Errorf("unused loop-header label should be dropped:\n%s", code)
	}
}

func TestDecompileDeviceValidity(t *testing.T) {
	code, warns, err := Decompile("bdnvl d0 Temperature L1\ns d0 On 1\nL1:\ns d0 On 0\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if !strings.Contains(code, `!isLoadValid(d0, "Temperature")`) {
		t.Errorf("bdnvl not translated:\n%s", code)
	}
}

func TestDecompileDynamicLogic(t *testing.T) {
	code, warns, err := Decompile("l r0 d0 r1\ns d0 r1 r0\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if !strings.Contains(code, "read(d0, r1)") {
		t.Errorf("dynamic read not translated:\n%s", code)
	}
	if !strings.Contains(code, "write(d0, r1, r0)") {
		t.Errorf("dynamic write not translated:\n%s", code)
	}
}

func TestDecompileLogicTypeQualified(t *testing.T) {
	// `ld`/`sd` address a device by ReferenceId; readById/writeById expect the
	// logic type as a LogicType.<name> enum, while a register stays a dynamic
	// logic value.
	code, warns, err := Decompile("ld r0 r0 Activate\nsd r0 On r1\nld r2 r1 LogicType.Open\nld r3 r1 r2\nsd r4 r5 r6\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	for _, want := range []string{
		"readById(r0, LogicType.Activate)", // bare name -> prefixed
		"writeById(r0, LogicType.On, r1)",  // bare name -> prefixed
		"readById(r1, LogicType.Open)",     // already prefixed -> kept
		"readById(r1, r2)",                 // register -> dynamic
		"writeById(r4, r5, r6)",            // register -> dynamic
	} {
		if !strings.Contains(code, want) {
			t.Errorf("output missing %q:\n%s", want, code)
		}
	}
}

func TestDecompileComputedJump(t *testing.T) {
	code, warns, err := Decompile("brnez r0 r0\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if !strings.Contains(code, "if r0 != 0 { jump(r0) }") {
		t.Errorf("computed jump not translated:\n%s", code)
	}
}

func TestDecompileNewBuiltins(t *testing.T) {
	src := `lr r0 d0 0 5
clrd 99
nor r1 r2 r3
`
	code, warns, err := Decompile(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	for _, want := range []string{
		"readReagent(d0, ReagentMode.Contents, 5)",
		"clrById(99)",
		"logicalNor(r2, r3)",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("output missing %q:\n%s", want, code)
		}
	}
}

func TestDecompileUnifiedGetPut(t *testing.T) {
	// The unified get/put take a port or an id; only the port form maps to the
	// .icg get/put builtins, the id form maps to getd/putd.
	src := "get r0 d0 5\nput d0 5 r1\nget r2 r1 5\nput r1 5 r2\n"
	code, warns, err := Decompile(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	for _, want := range []string{"get(d0, 5)", "put(d0, 5, r1)", "getd(r1, 5)", "putd(r1, 5, r2)"} {
		if !strings.Contains(code, want) {
			t.Errorf("output missing %q:\n%s", want, code)
		}
	}
}

func TestDecompileSanitizesLabelsAndNumbers(t *testing.T) {
	// IC10 labels may contain '-'/'+'; IC10 floats may omit the leading digit.
	src := `move r0 .85
mul r1 r0 .9
mul r1 r1 -.5
blt r0 5 Y-
j Y+
Y-:
move r0 1
Y+:
move r1 2
`
	code, warns, err := Decompile(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	for _, want := range []string{"0.85", "0.9", "-0.5", "label Y_:", "label Y__2:", "goto Y_", "goto Y__2"} {
		if !strings.Contains(code, want) {
			t.Errorf("output missing %q:\n%s", want, code)
		}
	}
	for _, bad := range []string{"Y-", "Y+", " .85", " .9", " -.5"} {
		if strings.Contains(code, bad) {
			t.Errorf("output contains invalid token %q:\n%s", bad, code)
		}
	}
}

func TestDecompileReservedLabel(t *testing.T) {
	// An IC10 label that collides with a .icg keyword must be renamed so the
	// decompiled source still parses.
	src := "return:\nmove r0 1\nbeqz r0 return\n"
	code, _, err := Decompile(src)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(code, "label return:") {
		t.Errorf("emitted reserved label \"return\":\n%s", code)
	}
	if !strings.Contains(code, "label return_:") {
		t.Errorf("output missing sanitized label \"return_\":\n%s", code)
	}
}

func TestDecompileDeviceIDRegister(t *testing.T) {
	// A register device operand (`r?` holding a ReferenceId) maps to the
	// by-id builtins, not to member access on a variable.
	src := `l r0 r1 Temperature
s r2 Open 1
sdns r0 r3
clr r4
rmap r5 r3 7
lr r6 r7 0 5
`
	code, warns, err := Decompile(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	for _, want := range []string{
		"readById(r1, LogicType.Temperature)",
		"writeById(r2, LogicType.Open, 1)",
		"isUnset(r3)",
		"clr(r4)",
		"rmap(r3, 7)",
		"readReagentById(r7, ReagentMode.Contents, 5)",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("output missing %q:\n%s", want, code)
		}
	}
}

func TestDecompileInvalidOperand(t *testing.T) {
	// Operands the compiler cannot express at all (undefined device names,
	// out-of-range registers, illegal tokens) become warnings, not invalid .icg.
	cases := []string{
		"s areaLight On r4\n",       // undefined device name
		"move r17 1\n",              // register out of range
		"sb <CTRLICH> Setting r0\n", // illegal token
	}
	for _, src := range cases {
		code, warns, err := Decompile(src)
		if err != nil {
			t.Fatalf("decompile %q: %v", src, err)
		}
		if len(warns) == 0 {
			t.Errorf("expected a warning for %q:\n%s", src, code)
		}
		if !strings.Contains(code, "// unsupported:") {
			t.Errorf("expected the instruction to be commented out for %q:\n%s", src, code)
		}
	}
}

func TestDecompileDynamicLogicOnIDRegister(t *testing.T) {
	// `l r1 rN rM` (device id in rN, logic type in rM) and its store form must
	// become readById/writeById: `read(id, ...)` needs a compile-time device.
	code, warns, err := Decompile("l r1 r12 r14\ns r2 r13 r15\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	for _, want := range []string{"readById(r12, r14)", "writeById(r2, r13, r15)"} {
		if !strings.Contains(code, want) {
			t.Errorf("missing %q:\n%s", want, code)
		}
	}
}

func TestDecompileUnknownValuePreserved(t *testing.T) {
	// A bare value operand the compiler does not know (a game special register
	// such as `rgas`, or a reagent name) is emitted verbatim through raw(), with
	// a warning, instead of dropping the whole instruction.
	for _, tc := range []struct{ src, want string }{
		{"mul r0 r0 rgas\n", `raw("rgas")`},
		{"lr r0 dr9 Contents Iron\n", `raw("Iron")`},
		{"move r0 start\n", `raw("start")`},
	} {
		code, warns, err := Decompile(tc.src)
		if err != nil {
			t.Fatalf("decompile %q: %v", tc.src, err)
		}
		if len(warns) == 0 {
			t.Errorf("expected a warning for %q", tc.src)
		}
		if !strings.Contains(code, tc.want) {
			t.Errorf("expected %q in output for %q:\n%s", tc.want, tc.src, code)
		}
		if strings.Contains(code, "// unsupported:") {
			t.Errorf("instruction should be preserved, not dropped, for %q:\n%s", tc.src, code)
		}
	}
}

func TestDecompileRadixAndBadTargets(t *testing.T) {
	// IC10 $hex / %bin become .icg 0x / 0b.
	code, warns, err := Decompile("move r0 $1F\nmove r1 %1010\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	for _, want := range []string{"0x1F", "0b1010"} {
		if !strings.Contains(code, want) {
			t.Errorf("output missing %q:\n%s", want, code)
		}
	}

	// An undefined label target becomes a warning, not an invalid goto.
	code, warns, err = Decompile("j nowhere\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) == 0 || !strings.Contains(code, "// unsupported:") {
		t.Errorf("undefined target should be unsupported:\n%s", code)
	}

	// An out-of-range numeric target halts (end label + jump(9999)).
	code, warns, err = Decompile("beqz r0 99\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if !strings.Contains(code, "label L99:") || !strings.Contains(code, "jump(9999)") {
		t.Errorf("out-of-range target should halt:\n%s", code)
	}
}

func TestDecompileMalformedDoesNotPanic(t *testing.T) {
	// Instructions with too few operands must become warnings, not panics.
	cases := []string{
		"l r0 d0\n",        // missing logic type
		"j\n",              // missing target
		"bdns\n",           // missing target
		"lb r0 7 Charge\n", // missing batch mode
		"select r0 r1\n",   // missing operands
		"sdse\n",           // missing device
		"s d0\n",           // missing logic/value
		"lr r0 d0 0\n",     // missing reagent key
		"ls r0 d0\n",       // missing index/logic
	}
	for _, src := range cases {
		for _, structured := range []bool{false, true} {
			var (
				code  string
				warns []Warning
				err   error
			)
			if structured {
				code, warns, err = DecompileStructured(src)
			} else {
				code, warns, err = Decompile(src)
			}
			if err != nil {
				t.Fatalf("decompile %q (structured=%v): %v", src, structured, err)
			}
			if len(warns) == 0 {
				t.Errorf("expected a warning for %q (structured=%v):\n%s", src, structured, code)
			}
		}
	}
}

func TestDecompileComputedCallUnsupported(t *testing.T) {
	// A computed call (jal rX / b<cond>al ... rX) cannot be expressed without
	// dropping the link, so it becomes a warning.
	for _, src := range []string{"jal r0\n", "bgeal r0 r1 r2\n"} {
		code, warns, err := Decompile(src)
		if err != nil {
			t.Fatalf("decompile %q: %v", src, err)
		}
		if len(warns) == 0 || !strings.Contains(code, "// unsupported:") {
			t.Errorf("computed call should be unsupported for %q:\n%s", src, code)
		}
	}
}

// TestDecompileNumericDeviceID checks that a bare number as a device operand is
// read back as a ReferenceId: IC10 ports are always written d0..d5 / db, so a
// number means readById/writeById, not the invalid `704.Setting`.
func TestDecompileNumericDeviceID(t *testing.T) {
	code, warns, err := Decompile("l r0 704 Setting\ns 704 On 1\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	for _, want := range []string{"readById(704, LogicType.Setting)", "writeById(704, LogicType.On, 1)"} {
		if !strings.Contains(code, want) {
			t.Errorf("output missing %q:\n%s", want, code)
		}
	}
	if strings.Contains(code, "704.") {
		t.Errorf("a bare ReferenceId must not be rendered as a port:\n%s", code)
	}
}

func TestDecompileWithModCatalogNamesModHashes(t *testing.T) {
	// 1448182275 = Hash("Text_K"), a LettersMod prefab the game tables do not have.
	src := "s d0 Setting 1448182275\n"
	code, _, err := Decompile(src)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(code, "Text_K") {
		t.Fatalf("the game catalog should not know Text_K:\n%s", code)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "prefabs.json"),
		[]byte(`{"prefabs":[{"name":"Text_K","displayName":"Letter K"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pack, err := builtin.LoadPack("LettersMod", dir)
	if err != nil {
		t.Fatal(err)
	}
	code, _, err = DecompileWith(src, Options{Prefabs: builtin.Base().WithPacks(pack)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(code, `hash("Text_K")`) {
		t.Errorf("a mod prefab hash should resolve once the pack is loaded:\n%s", code)
	}
}

func TestDecompileAliasBindsAtPointOfUse(t *testing.T) {
	// Aliases are resolved in source order; a later alias must not rewrite an
	// earlier instruction. Real programs re-alias a name to switch devices.
	src := `alias dev d0
s dev On 1
alias dev d1
s dev On 0
`
	code, _, err := Decompile(src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(code, "d0.On = 1") {
		t.Errorf("the first write must still target d0:\n%s", code)
	}
	if !strings.Contains(code, "d1.On = 0") {
		t.Errorf("the second write must target d1:\n%s", code)
	}
}

func TestSharedStackDirectiveOnlyWhenHousingStackUsed(t *testing.T) {
	// A program that never touches the housing stack does not need the pragma,
	// and without it the compiler stays free to keep the stack private.
	code, _, err := Decompile("s d0 On 1\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(code, "shared-stack") {
		t.Errorf("no stack access, but the output pins the shared stack:\n%s", code)
	}

	// `put db` publishes a slot for other chips; the write must survive.
	code, _, err = Decompile("put db 1 2\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(code, "shared-stack") {
		t.Errorf("a housing-stack write needs the pragma:\n%s", code)
	}
}

func TestDecompileLabelsAreDeterministic(t *testing.T) {
	// Two jumps land on the same instruction: `brgt` reaches the alias line and
	// `jr` reaches the line after it. Both mean the next instruction. The shared
	// label's name must not depend on map order -- a run used to be able to emit
	// `goto L16` while the label it printed was `label L15`.
	src := "alias d d0\nbrgt r1 2 3\n  alias d d0\n  jr 2\n  alias d d1\nl r0 d2 Open\n"
	first, warns, err := Decompile(src)
	if err != nil || len(warns) > 0 {
		t.Fatalf("decompile: %v %v", err, warns)
	}
	for i := 0; i < 32; i++ {
		got, _, err := Decompile(src)
		if err != nil {
			t.Fatal(err)
		}
		if got != first {
			t.Fatalf("run %d differs:\n%s\n--- vs ---\n%s", i, first, got)
		}
	}
	labels := map[string]bool{}
	for _, line := range strings.Split(first, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "label ") {
			labels[strings.TrimSuffix(strings.TrimPrefix(line, "label "), ":")] = true
		}
	}
	for _, line := range strings.Split(first, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "goto ") {
			name := strings.TrimPrefix(line, "goto ")
			if !labels[name] {
				t.Errorf("goto %s has no label:\n%s", name, first)
			}
		}
	}
}

func TestDecompileResolvesForwardDefine(t *testing.T) {
	// Defines are position independent (measured in game 2026-10-18): a use
	// above the definition still substitutes.
	code, _, err := Decompile("move r0 LIMIT\ns d0 Setting r0\ndefine LIMIT 42\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(code, "r0 := 42") {
		t.Errorf("the forward define was not substituted:\n%s", code)
	}
}

func TestDecompileHexDeviceID(t *testing.T) {
	// A device operand can be a ReferenceId written as a hex literal; .icg has
	// no `$` spelling, so it must become 0x and go through read/writeById.
	// (One real program writes `s $1CB1EA Horizontal H`.)
	code, warns, err := Decompile("alias H r11\nmove r11 5\ns $1CB1EA Horizontal H\nl r0 $1CB1EA Setting\n")
	if err != nil || len(warns) > 0 {
		t.Fatalf("decompile: %v %v", err, warns)
	}
	if !strings.Contains(code, "writeById(0x1CB1EA") {
		t.Errorf("hex device id was not lowered to writeById:\n%s", code)
	}
	if !strings.Contains(code, "readById(0x1CB1EA") {
		t.Errorf("hex device id was not lowered to readById:\n%s", code)
	}
}
