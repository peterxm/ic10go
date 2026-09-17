package decomp

import (
	"strings"
	"testing"
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
	if !strings.Contains(code, `batch.readName(-400115994, hash("Bank 1"), "Ratio", "Average")`) {
		t.Errorf("batch read not translated:\n%s", code)
	}
	if !strings.Contains(code, `batch.writeName(1220484876, hash("Override"), "Open", 1)`) {
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
	src := `trunc rr5 r0
mod r0 r0 rr5
`
	code, warns, err := Decompile(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if !strings.Contains(code, "setIreg(r5,") {
		t.Errorf("indirect store not translated:\n%s", code)
	}
	if !strings.Contains(code, "ireg(r5)") {
		t.Errorf("indirect read not translated:\n%s", code)
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
	if !strings.Contains(code, "label L3:") {
		t.Errorf("branch target label was not emitted:\n%s", code)
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
