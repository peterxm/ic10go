package ic10_test

import (
	"strings"
	"testing"

	"ic10go/internal/vm"
	"ic10go/pkg/ic10"
)

// TestDeviceStackIDOperand checks that get/put accept a device id or a
// register holding one (the game's device operand is d?|r?|id).
func TestDeviceStackIDOperand(t *testing.T) {
	src := `func main() {
    id := d1.ReferenceId
    v := get(id, 0)
    put(id, 1, v)
    d0.Setting = v
}`
	code := mustCompile(t, src)
	m := vm.New()
	m.Set("d1", "ReferenceId", 77)
	m.Device("d1").Stack[0] = 123
	if err := m.Load(code); err != nil {
		t.Fatalf("vm load: %v", err)
	}
	if err := m.Run(200); err != nil && err != vm.ErrStepLimit {
		t.Fatalf("vm run: %v", err)
	}
	if got := m.Get("d0", "Setting"); got != 123 {
		t.Errorf("d0.Setting = %v, want 123", got)
	}
	if got := m.Device("d1").Stack[1]; got != 123 {
		t.Errorf("d1.stack[1] = %v, want 123", got)
	}
}

// TestDeviceStackSugar checks the dN.stack[addr] rvalue/lvalue sugar.
func TestDeviceStackSugar(t *testing.T) {
	src := `func main() {
    d0.stack[1] = 123
    d0.stack[2] = d0.stack[1] + 1
    d1.Setting = d0.stack[2]
}`
	code := mustCompile(t, src)
	m := vm.New()
	if err := m.Load(code); err != nil {
		t.Fatalf("vm load: %v", err)
	}
	if err := m.Run(200); err != nil && err != vm.ErrStepLimit {
		t.Fatalf("vm run: %v", err)
	}
	if got := m.Device("d0").Stack[1]; got != 123 {
		t.Errorf("d0.stack[1] = %v, want 123", got)
	}
	if got := m.Device("d0").Stack[2]; got != 124 {
		t.Errorf("d0.stack[2] = %v, want 124", got)
	}
	if got := m.Get("d1", "Setting"); got != 124 {
		t.Errorf("d1.Setting = %v, want 124", got)
	}
}

// TestUnknownEnumVerbatim checks the escape hatch: an unknown Enum.Member is
// emitted verbatim with an unknown-enum warning.
func TestUnknownEnumVerbatim(t *testing.T) {
	src := "func main() { d0.Setting = Foo.Bar }\n"
	code, diags, err := ic10.Compile("t.icg", []byte(src))
	if err != nil || diags.HasErrors() {
		t.Fatalf("compile: %v %v", diags.Diags, err)
	}
	if len(diags.Diags) != 1 || diags.Diags[0].Code != "unknown-enum" {
		t.Fatalf("diags = %+v, want one unknown-enum warning", diags.Diags)
	}
	if !strings.Contains(code, "s d0 Setting Foo.Bar") {
		t.Errorf("code = %q, want a verbatim Foo.Bar operand", code)
	}
}

// TestRawEscapeHatch checks raw("...") emits its argument verbatim, both as a
// value and as a const declaration.
func TestRawEscapeHatch(t *testing.T) {
	src := `const cond = raw("SomeNewConst")
func main() { d0.Setting = cond }`
	code := mustCompile(t, src)
	if !strings.Contains(code, "s d0 Setting SomeNewConst") {
		t.Errorf("code = %q, want a verbatim SomeNewConst operand", code)
	}
}

// TestSorterBuilders checks the sorter.* builders pack the documented bit
// fields into a single folded constant.
func TestSorterBuilders(t *testing.T) {
	src := `func main() {
    put(d0, 0, sorter.filterSortingClass(Equals, SortingClass.Ores))
    put(d0, 1, sorter.filterPrefabHash(hash("ItemIronOre")))
    put(d0, 2, sorter.filterPrefabHashNotEquals(hash("ItemGold")))
    put(d0, 3, sorter.filterSlotType(Greater, SlotClass.Battery))
    put(d0, 4, sorter.filterQuantity(Less, 10))
    put(d0, 5, sorter.limitNextExecutionByCount(5))
}`
	code := mustCompile(t, src)
	for _, want := range []string{
		"put d0 0 589827",        // Ores(9)<<16 | Equals(0)<<8 | 3
		"put d0 1 450157508353",  // hash(ItemIronOre)<<8 | 1
		"put d0 2 -323939536638", // hash(ItemGold)<<8 | 2
		"put d0 3 917764",        // Battery(14)<<16 | Greater(1)<<8 | 4
		"put d0 4 655877",        // 10<<16 | Less(2)<<8 | 5
		"put d0 5 1286",          // 5<<8 | 6
	} {
		if !strings.Contains(code, want) {
			t.Errorf("code missing %q:\n%s", want, code)
		}
	}
}

// TestPrinterBuilders checks the printer.* builders.
func TestPrinterBuilders(t *testing.T) {
	src := `func main() {
    put(d0, 0, printer.none())
    put(d0, 1, printer.stackPointer(7))
    put(d0, 2, printer.executeRecipe(50, hash("ItemCableCoil")))
    put(d0, 3, printer.waitUntilNextValid())
    put(d0, 4, printer.jumpIfNextInvalid(3))
    put(d0, 5, printer.jumpToAddress(10))
    put(d0, 6, printer.deviceSetLock(1))
    put(d0, 7, printer.ejectReagent(hash("Iron")))
    put(d0, 8, printer.ejectAllReagents())
    put(d0, 9, printer.missingRecipeReagent(2, hash("Iron")))
}`
	code := mustCompile(t, src)
	for _, want := range []string{
		"put d0 0 0",               // None
		"put d0 1 1793",            // 7<<8 | StackPointer(1)
		"put d0 2 -30543096565246", // 50<<8 | hash<<16 | ExecuteRecipe(2)
		"put d0 3 3",               // WaitUntilNextValid
		"put d0 4 772",             // 3<<8 | JumpIfNextInvalid(4)
		"put d0 5 2565",            // 10<<8 | JumpToAddress(5)
		"put d0 6 262",             // 1<<8 | DeviceSetLock(6)
		"put d0 7 -170686176761",   // hash("Iron")<<8 | EjectReagent(7)
		"put d0 8 8",               // EjectAllReagents
		"put d0 9 -43695661252087", // 2<<8 | hash("Iron")<<16 | MissingRecipeReagent(9)
	} {
		if !strings.Contains(code, want) {
			t.Errorf("code missing %q:\n%s", want, code)
		}
	}
}

// TestBuilderFieldValidation checks that a constant field overflowing its bit
// width is a compile error instead of silently spilling into the next field.
func TestBuilderFieldValidation(t *testing.T) {
	cases := []string{
		"func main() { put(d0, 0, printer.executeRecipe(300, 1)) }\n",        // quantity is 8 bits
		"func main() { put(d0, 0, printer.stackPointer(1 << 20)) }\n",        // index is 16 bits
		"func main() { put(d0, 0, printer.missingRecipeReagent(300, 1)) }\n", // quantity ceil is 8 bits
		"func main() { put(d0, 0, sorter.filterQuantity(Less, 100000)) }\n",  // quantity is 16 bits
		"func main() { put(d0, 0, sorter.filterPrefabHash(1 << 40)) }\n",     // hash is 32 bits
	}
	for _, src := range cases {
		_, diags, err := ic10.Compile("t.icg", []byte(src))
		if err != nil || !diags.HasErrors() {
			t.Errorf("src %q: want a compile error, got diags=%v err=%v", src, diags.Diags, err)
		}
	}
}

// TestStackSizeConstants checks the device-stack size/address conveniences.
func TestStackSizeConstants(t *testing.T) {
	src := `func main() {
    put(d0, SorterStack.Size, 0)
    put(d1, PrinterStack.StackPointer, 1)
    d2.Setting = Stack.Size + PrinterStack.MissingRecipeReagent
}`
	code := mustCompile(t, src)
	for _, want := range []string{"put d0 32 0", "put d1 63 1", "s d2 Setting 566"} {
		if !strings.Contains(code, want) {
			t.Errorf("code missing %q:\n%s", want, code)
		}
	}
}

// TestReadWriteByID checks readById/writeById (IC10 ld/sd).
func TestReadWriteByID(t *testing.T) {
	src := `func main() {
    id := d1.ReferenceId
    v := readById(id, LogicType.Temperature)
    writeById(id, LogicType.On, 1)
    d0.Setting = v
}`
	code := mustCompile(t, src)
	if !strings.Contains(code, "ld ") || !strings.Contains(code, "sd ") {
		t.Errorf("code = %q, want ld/sd", code)
	}
	m := vm.New()
	m.Set("d1", "ReferenceId", 55)
	m.Set("d1", "Temperature", 321)
	if err := m.Load(code); err != nil {
		t.Fatalf("vm load: %v", err)
	}
	if err := m.Run(200); err != nil && err != vm.ErrStepLimit {
		t.Fatalf("vm run: %v", err)
	}
	if got := m.Get("d0", "Setting"); got != 321 {
		t.Errorf("d0.Setting = %v, want 321", got)
	}
	if got := m.Get("d1", "On"); got != 1 {
		t.Errorf("d1.On = %v, want 1", got)
	}
}

// TestDeviceStackByID checks the id.stack[addr] sugar (getd/putd by id).
func TestDeviceStackByID(t *testing.T) {
	src := `func main() {
    id := d1.ReferenceId
    id.stack[0] = 123
    v := id.stack[0]
    d0.Setting = v
}`
	code := mustCompile(t, src)
	m := vm.New()
	m.Set("d1", "ReferenceId", 55)
	if err := m.Load(code); err != nil {
		t.Fatalf("vm load: %v", err)
	}
	if err := m.Run(200); err != nil && err != vm.ErrStepLimit {
		t.Fatalf("vm run: %v", err)
	}
	if got := m.Device("d1").Stack[0]; got != 123 {
		t.Errorf("d1.stack[0] = %v, want 123", got)
	}
	if got := m.Get("d0", "Setting"); got != 123 {
		t.Errorf("d0.Setting = %v, want 123", got)
	}
}

// TestDevSlot checks readDevSlot/writeDevSlot (dynamic device port slots).
func TestDevSlot(t *testing.T) {
	src := `func main() {
    ptr := d2.Setting
    writeDevSlot(ptr, 1, On, 1)
    v := readDevSlot(ptr, 1, On)
    d0.Setting = v
}`
	code := mustCompile(t, src)
	if !strings.Contains(code, "ss dr") || !strings.Contains(code, "ls ") {
		t.Errorf("code = %q, want ss drN / ls drN", code)
	}
	m := vm.New()
	m.Set("d2", "Setting", 1) // ptr selects port d1
	if err := m.Load(code); err != nil {
		t.Fatalf("vm load: %v", err)
	}
	if err := m.Run(200); err != nil && err != vm.ErrStepLimit {
		t.Fatalf("vm run: %v", err)
	}
	if got := m.GetSlot("d1", 1, "On"); got != 1 {
		t.Errorf("d1.slot[1].On = %v, want 1", got)
	}
	if got := m.Get("d0", "Setting"); got != 1 {
		t.Errorf("d0.Setting = %v, want 1", got)
	}
}

// TestIndirectReagent checks that readReagent accepts a register holding the
// device port (IC10 "lr r? drN mode key"), not just a dN/db operand.
func TestIndirectReagent(t *testing.T) {
	// Take the port from a device so it stays a runtime value: a constant port
	// is folded into a direct dN operand by the optimiser.
	src := `func main() {
    var idx = 0.0
    idx = d5.Setting
    d0.Setting = readReagent(idx, ReagentMode.Contents, 12345)
}`
	code := mustCompile(t, src)
	if !strings.Contains(code, "lr ") || !strings.Contains(code, "dr") {
		t.Fatalf("expected an indirect reagent load (lr ... drN ...):\n%s", code)
	}
	m := vm.New()
	m.Set("d5", "Setting", 1)
	m.Device("d1").Reagents[12345] = 7
	if err := m.Load(code); err != nil {
		t.Fatalf("vm load: %v", err)
	}
	if err := m.Run(50); err != nil && err != vm.ErrStepLimit {
		t.Fatalf("vm run: %v", err)
	}
	if got := m.Get("d0", "Setting"); got != 7 {
		t.Errorf("d0.Setting = %v, want 7", got)
	}
}
