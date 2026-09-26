// Command genenums reads the game's Assembly-CSharp.dll and regenerates
// internal/builtin/gameenums_gen.go, the canonical mirror of the game's logic
// enums that the checked-in tables are verified against.
//
// It is a pure-Go ECMA-335 reader: it needs no .NET SDK and no copy of the game
// running, only the DLL. Run it after a Stationeers update:
//
//	go run ./tools/genenums [path-to-Assembly-CSharp.dll] [output.go]
//
// A missing DLL or a renamed type is a hard error, so a game update that moves
// a namespace shows up immediately instead of silently emptying a table.
package main

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"ic10go/internal/clr"
)

// groups maps the receiver a script writes before the dot to the enum inside
// Assembly-CSharp it mirrors. The left names match the tables in builtin.go and
// the values IZCode's generator uses for the same game; where the game names a
// group by its type (ColorType, SoundAlert, Slot+Class, ...) the script spelling
// is kept.
var groups = []struct{ Name, Type string }{
	{"AirCon", "Assets.Scripts.Objects.AirConditioningMode"},
	{"AirControl", "Assets.Scripts.Objects.Motherboards.AirControlMode"},
	{"Color", "Assets.Scripts.Objects.Motherboards.ColorType"},
	{"ConditionOperation", "Assets.Scripts.Objects.Motherboards.ConditionOperation"},
	{"DaylightSensorMode", "Assets.Scripts.Objects.Electrical.DaylightSensor+DaylightSensorMode"},
	{"DisplayMode", "Assets.Scripts.Objects.Electrical.LogicDisplay+DisplayMode"},
	{"ElevatorMode", "Assets.Scripts.ElevatorMode"},
	{"EntityState", "Assets.Scripts.Objects.Entities.EntityState"},
	{"FiltrationMode", "Assets.Scripts.Objects.Pipes.FiltrationMode"},
	{"GasType", "Assets.Scripts.Atmospherics.Chemistry+GasType"},
	{"HashType", "Assets.Scripts.Objects.Motherboards.HashType"},
	{"LogicBatchMethod", "Assets.Scripts.Objects.Electrical.LogicBatchMethod"},
	{"LogicReagentMode", "Assets.Scripts.Objects.Electrical.LogicReagentMode"},
	{"LogicSlotType", "Assets.Scripts.Objects.Motherboards.LogicSlotType"},
	{"LogicType", "Assets.Scripts.Objects.Motherboards.LogicType"},
	{"NodeType", "Objects.Rockets.NodeType"},
	{"PowerMode", "Assets.Scripts.Objects.Electrical.PowerMode"},
	{"PrinterInstruction", "Assets.Scripts.Objects.Electrical.PrinterInstruction"},
	{"ReEntryProfile", "Objects.Rockets.ReEntryProfile"},
	{"RobotMode", "Assets.Scripts.Objects.RobotMode"},
	{"RocketMode", "Objects.Rockets.RocketMode"},
	{"SettingDisplayMode", "Assets.Scripts.Objects.Electrical.SettingDisplayMode"},
	{"ShuttleType", "Assets.Scripts.ShuttleType"},
	{"SlotClass", "Assets.Scripts.Objects.Slot+Class"},
	{"SorterInstruction", "Assets.Scripts.Objects.Electrical.SorterInstruction"},
	{"SortingClass", "Assets.Scripts.Objects.SortingClass"},
	{"Sound", "Assets.Scripts.Objects.Electrical.SoundAlert"},
	{"TraderInstruction", "Assets.Scripts.Objects.Electrical.TraderInstruction"},
	{"TransmitterMode", "Assets.Scripts.Objects.Electrical.LogicTransmitterMode"},
	{"Vent", "Assets.Scripts.Objects.Pipes.VentDirection"},
}

// Default locations to try when no path is given, so `go run ./tools/genenums`
// works on the common installs.
var defaultDLLs = []string{
	"/home/*/.local/share/Steam/steamapps/common/Stationeers/rocketstation_Data/Managed/Assembly-CSharp.dll",
	`C:\Program Files (x86)\Steam\steamapps\common\Stationeers\rocketstation_Data\Managed\Assembly-CSharp.dll`,
}

const defaultOut = "internal/builtin/gameenums_gen.go"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "genenums:", err)
		os.Exit(1)
	}
}

func run() error {
	dll, out := "", defaultOut
	if len(os.Args) > 1 {
		dll = os.Args[1]
	}
	if len(os.Args) > 2 {
		out = os.Args[2]
	}
	if dll == "" {
		var err error
		if dll, err = findDLL(); err != nil {
			return err
		}
	}

	tables, err := clr.Open(dll)
	if err != nil {
		return err
	}

	byType := map[string]map[string]int64{}
	for _, g := range groups {
		members, err := tables.EnumMembers(g.Type)
		if err != nil {
			return fmt.Errorf("read enum %s (%s): %w", g.Name, g.Type, err)
		}
		byType[g.Name] = members
	}

	if err := writeGo(out, byType); err != nil {
		return err
	}
	if err := updateGrammar("editors/vscode/syntaxes/icg.tmLanguage.json", byType); err != nil {
		return err
	}
	total := 0
	for _, m := range byType {
		total += len(m)
	}
	fmt.Printf("wrote %s: %d groups, %d values\n", out, len(byType), total)
	return nil
}

// grammarExtras are the non-LogicType names the icg grammar highlights in the
// same alternation: the batch modes, the slot/channel/stack qualifiers and the
// numeric constants.
const grammarExtras = "Average|Sum|Minimum|Maximum|Count|slot|channel|stack|Equals|Greater|Less|NotEquals|pi|deg2rad|rad2deg|epsilon"

// updateGrammar rewrites the `logictypes` alternation of the VSCode TextMate
// grammar from GameEnums, so the editor follows the game exactly like the
// compiler's LogicTypes does. It edits the file in place, preserving the rest.
func updateGrammar(path string, byType map[string]map[string]int64) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	const marker = `"constant.other.logictype.icg", "match": "\\b(`
	s := string(data)
	i := strings.Index(s, marker)
	if i < 0 {
		return fmt.Errorf("logictypes pattern not found in %s", path)
	}
	start := i + len(marker)
	rest := s[start:]
	j := strings.Index(rest, `)\\b"`)
	if j < 0 {
		return fmt.Errorf("logictypes pattern end not found in %s", path)
	}

	names := make([]string, 0, len(byType["LogicType"]))
	for n := range byType["LogicType"] {
		if n != "None" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	alt := strings.Join(names, "|") + "|" + grammarExtras

	if err := os.WriteFile(path, []byte(s[:start]+alt+rest[j:]), 0o644); err != nil {
		return err
	}
	fmt.Printf("updated %s: %d logic types\n", path, len(names))
	return nil
}

func findDLL() (string, error) {
	for _, p := range defaultDLLs {
		if matches, _ := filepath.Glob(p); len(matches) > 0 {
			return matches[0], nil
		}
	}
	return "", fmt.Errorf("no Assembly-CSharp.dll found; pass its path as the first argument")
}

// writeGo emits a deterministic Go source file: groups and members sorted.
func writeGo(path string, byType map[string]map[string]int64) error {
	names := make([]string, 0, len(byType))
	for n := range byType {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("// Code generated by tools/genenums/main.go from the game's Assembly-CSharp.dll; DO NOT EDIT.\n")
	b.WriteString("//\n")
	b.WriteString("// GameEnums mirrors the game's logic enums, keyed by the receiver written before\n")
	b.WriteString("// the dot. It is the baseline the checked-in tables are verified against; see\n")
	b.WriteString("// gameenums_test.go and docs/target-ic10.md.\n")
	b.WriteString("package builtin\n\n")
	b.WriteString("// GameEnums is every named constant group the game exposes, from LogicType to\n")
	b.WriteString("// Vent. Regenerate with `go run ./tools/genenums`.\n")
	b.WriteString("var GameEnums = map[string]map[string]int64{\n")
	for _, name := range names {
		members := byType[name]
		keys := make([]string, 0, len(members))
		for k := range members {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		b.WriteString("\t" + goQuote(name) + ": {\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "\t\t%s: %d,\n", goQuote(k), members[k])
		}
		b.WriteString("\t},\n")
	}
	b.WriteString("}\n")

	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return fmt.Errorf("format generated source: %w", err)
	}
	return os.WriteFile(path, src, 0o644)
}

func goQuote(s string) string {
	// The names are ASCII identifiers, so a plain quoted string is fine.
	return `"` + s + `"`
}
