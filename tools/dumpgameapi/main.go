// Command dumpgameapi reads Stationeers' Assembly-CSharp.dll and prints (or
// checks) the types, fields and methods that the ic10go toolchain depends on.
//
// It is the version-compatibility guard for the in-game mod: run it after a
// Stationeers update to see whether the API the testbench uses still exists.
//
//	go run ./tools/dumpgameapi                 # dump the default types
//	go run ./tools/dumpgameapi ProgrammableChip ILogicable
//	go run ./tools/dumpgameapi -check          # exit 1 if a required member is gone
//
// It is a pure-Go ECMA-335 reader: no .NET SDK, no decompiler, no running game,
// only the DLL. The reader lives in internal/clr and is shared with genenums.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ic10go/internal/clr"
)

// defaultDLLs are tried when no path is given.
var defaultDLLs = []string{
	"/home/*/.local/share/Steam/steamapps/common/Stationeers/rocketstation_Data/Managed/Assembly-CSharp.dll",
	`C:\Program Files (x86)\Steam\steamapps\common\Stationeers\rocketstation_Data\Managed\Assembly-CSharp.dll`,
}

// defaultQueries are printed when no type filter is given: everything the
// testbench mod touches.
var defaultQueries = []string{"ProgrammableChip", "ICircuitHolder", "CircuitHolders", "ILogicable"}

// required is the API the in-game mod compiles against. Keep it in sync with
// tools/ingame-testbench/GameApi.cs.
var required = []struct {
	Type    string
	Members []string
}{
	{"Assets.Scripts.Objects.Electrical.ProgrammableChip", []string{
		"Execute", "ReadMemory", "WriteMemory", "ClearMemory", "GetStackSize",
		"SetSourceCode", "GetSourceCode", "Reset", "LineNumber",
		"_Registers", "_Stack", "_StackPointerIndex", "_ReturnAddressIndex", "_executeIndex",
	}},
	{"Assets.Scripts.Objects.Electrical.ICircuitHolder", []string{
		"GetLogicableFromIndex", "GetLogicableFromId", "SetSourceCode", "GetSourceCode", "Execute",
	}},
	{"Assets.Scripts.Objects.Electrical.CircuitHolders", []string{
		"AllCircuitHolders", "Execute",
	}},
	{"Assets.Scripts.Objects.Motherboards.ProgrammableChipMotherboard", []string{
		"InputFinished", "SetSourceCode", "GetSourceCode",
	}},
	{"Assets.Scripts.Objects.Pipes.ILogicable", []string{
		"SetLogicValue", "GetLogicValue", "CanLogicWrite", "CanLogicRead",
	}},
	{"Assets.Scripts.Objects.Motherboards.LogicType", []string{"Setting", "On"}},
	{"Assets.Scripts.Objects.Motherboards.LogicSlotType", nil},
	{"WorldManager", []string{"IsGamePaused"}},
	{"Assets.Scripts.Objects.Thing", []string{"PrefabName", "ReferenceId", "_customName"}},
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "dumpgameapi:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	check := false
	var rest []string
	for _, a := range args {
		if a == "-check" || a == "--check" {
			check = true
		} else {
			rest = append(rest, a)
		}
	}

	dll := ""
	if len(rest) > 0 && strings.HasSuffix(strings.ToLower(rest[0]), ".dll") {
		dll, rest = rest[0], rest[1:]
	}
	if dll == "" {
		var err error
		if dll, err = findDLL(); err != nil {
			return err
		}
	}

	f, err := clr.Open(dll)
	if err != nil {
		return err
	}
	fmt.Printf("assembly %s\n", dll)

	if check {
		return runCheck(f)
	}
	queries := rest
	if len(queries) == 0 {
		queries = defaultQueries
	}
	for i := 1; i <= f.TypeCount(); i++ {
		full, err := f.FullTypeName(i)
		if err != nil || !matches(full, queries) {
			continue
		}
		dumpType(f, i, full)
	}
	return nil
}

func runCheck(f *clr.File) error {
	missing := 0
	for _, req := range required {
		idx := findType(f, req.Type)
		if idx < 0 {
			fmt.Printf("  MISSING type %s\n", req.Type)
			missing++
			continue
		}
		fields, methods, err := f.Members(idx)
		if err != nil {
			return err
		}
		names := map[string]bool{}
		for _, fl := range fields {
			names[fl.Name] = true
		}
		for _, m := range methods {
			names[m.Name] = true
		}
		for _, want := range req.Members {
			if !hasMember(names, want) {
				fmt.Printf("  MISSING %s.%s\n", req.Type, want)
				missing++
			}
		}
	}
	if missing > 0 {
		fmt.Printf("\n%d member(s) missing — the game API changed; update tools/ingame-testbench/GameApi.cs\n", missing)
		os.Exit(1)
	}
	fmt.Printf("game API OK: %d types present\n", len(required))
	return nil
}

func hasMember(names map[string]bool, want string) bool {
	return names[want] || names["get_"+want] || names["set_"+want]
}

func findType(f *clr.File, full string) int {
	for i := 1; i <= f.TypeCount(); i++ {
		name, err := f.FullTypeName(i)
		if err == nil && name == full {
			return i
		}
	}
	return -1
}

func dumpType(f *clr.File, i int, full string) {
	fields, methods, err := f.Members(i)
	if err != nil {
		return
	}
	fmt.Printf("\n=== %s : %s ===\n", full, f.BaseName(i))
	for _, fl := range fields {
		fmt.Printf("  field  %-40s flags=%#04x sig=%s\n", fl.Name, fl.Flags, fl.Signature)
	}
	for _, m := range methods {
		fmt.Printf("  method %-40s flags=%#04x params=%v sig=%s\n", m.Name, m.Flags, m.Params, m.Signature)
	}
}

func matches(full string, queries []string) bool {
	for _, q := range queries {
		if strings.Contains(full, q) {
			return true
		}
	}
	return false
}

func findDLL() (string, error) {
	for _, p := range defaultDLLs {
		if matches, _ := filepath.Glob(p); len(matches) > 0 {
			return matches[0], nil
		}
	}
	return "", fmt.Errorf("no Assembly-CSharp.dll found; pass its path as the first argument")
}
