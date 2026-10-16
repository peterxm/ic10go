// Command gengamedata reads the game's localization (english.xml) and
// regenerates the tables that come from game data rather than the assembly:
//
//   - internal/builtin/prefabs.go        prefab name -> display title
//   - internal/builtin/scripthelp_gen.go IC10 mnemonic -> the game's help text
//
// The prefab list is merged with the previous file: the localization titles
// what it knows, and names it does not carry (procedural wreckage, kits) are
// kept, so re-running never loses entries. The instruction help is the game's
// `ScriptCommand*` records and is applied over the existing table's signatures.
//
// Run after a Stationeers update:
//
//	go run ./tools/gengamedata [path-to-english.xml] [outdir]
package main

import (
	"encoding/xml"
	"fmt"
	"go/format"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var defaultXMLs = []string{
	"/home/*/.local/share/Steam/steamapps/common/Stationeers/rocketstation_Data/StreamingAssets/Language/english.xml",
	`C:\Program Files (x86)\Steam\steamapps\common\Stationeers\rocketstation_Data\StreamingAssets\Language\english.xml`,
}

const defaultOutDir = "internal/builtin"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gengamedata:", err)
		os.Exit(1)
	}
}

func run() error {
	xmlPath, outDir := "", defaultOutDir
	if len(os.Args) > 1 {
		xmlPath = os.Args[1]
	}
	if len(os.Args) > 2 {
		outDir = os.Args[2]
	}
	if xmlPath == "" {
		var err error
		if xmlPath, err = findXML(); err != nil {
			return err
		}
	}

	things, reagents, help, err := readLocalization(xmlPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", xmlPath, err)
	}
	if len(things) == 0 {
		return fmt.Errorf("%s has no RecordThing entries; is this the right file?", xmlPath)
	}

	prefabsPath := filepath.Join(outDir, "prefabs.go")
	prefabs, err := readExistingPrefabs(prefabsPath)
	if err != nil {
		return err
	}
	for k, v := range things {
		prefabs[k] = v
	}
	for k, v := range reagents {
		prefabs[k] = v
	}
	if err := writePrefabs(prefabsPath, prefabs); err != nil {
		return err
	}
	if err := writeScriptHelp(filepath.Join(outDir, "scripthelp_gen.go"), help); err != nil {
		return err
	}
	fmt.Printf("wrote %s: %d prefabs (%d localized)\n", prefabsPath, len(prefabs), len(things)+len(reagents))
	fmt.Printf("wrote %s: %d instructions\n", filepath.Join(outDir, "scripthelp_gen.go"), len(help))
	return nil
}

func findXML() (string, error) {
	for _, p := range defaultXMLs {
		if matches, _ := filepath.Glob(p); len(matches) > 0 {
			return matches[0], nil
		}
	}
	return "", fmt.Errorf("no english.xml found; pass its path as the first argument")
}

// readLocalization extracts the prefab (RecordThing) and reagent
// (RecordReagent) name -> title pairs and the ScriptCommand* help text.
func readLocalization(path string) (things, reagents, help map[string]string, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil, err
	}
	raw = trimBOM(raw)

	things = map[string]string{}
	reagents = map[string]string{}
	help = map[string]string{}

	dec := xml.NewDecoder(strings.NewReader(string(raw)))
	var (
		kind  = "" // current record element ("RecordThing", "RecordReagent", "Record")
		key   string
		value string
		field = ""
	)
	for {
		tok, terr := dec.Token()
		if terr != nil {
			if terr == io.EOF {
				break
			}
			return nil, nil, nil, terr
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "RecordThing", "RecordReagent", "Record":
				kind, key, value = t.Name.Local, "", ""
			case "Key":
				field = "key"
			case "Value":
				field = "value"
			}
		case xml.CharData:
			switch field {
			case "key":
				key += string(t)
			case "value":
				value += string(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "Key", "Value":
				field = ""
			}
			if t.Name.Local != kind {
				continue
			}
			key, value = strings.TrimSpace(key), strings.TrimSpace(value)
			switch {
			case kind == "RecordThing" && key != "":
				things[key] = value
			case kind == "RecordReagent" && key != "":
				reagents[key] = value
			// Two record families describe the same instructions, and they
			// disagree: ScriptCommand* is the short text the in-game editor
			// shows (ScriptCommandAnd = "Register = 1 if a and b not zero"),
			// while ScriptDescription* spells it out precisely
			// (ScriptDescriptionAnd = "Performs a bitwise logical AND ...").
			// Reading only ScriptCommand* also silently dropped the 24
			// mnemonics that have *no* command record at all — bdnvl/bdnvs,
			// clamp, ext, get/getd, put/putd, poke, sgn, rol/ror, sll/sra/srl/sla,
			// ins, lerp, rmap, clr/clrd, sbn. Prefer the description family and
			// keep ScriptCommand* as the fallback for mnemonics that lack one.
			case kind == "Record" && (strings.HasPrefix(key, "ScriptCommand") ||
				strings.HasPrefix(key, "ScriptDescription")):
				prefix := "ScriptCommand"
				if strings.HasPrefix(key, "ScriptDescription") {
					prefix = "ScriptDescription"
				}
				mnemonic := strings.ToLower(strings.TrimPrefix(key, prefix))
				if mnemonic == "" || value == "" {
					break
				}
				if prefix == "ScriptDescription" || help[mnemonic] == "" {
					help[mnemonic] = value
				}
			}
			kind = ""
		}
	}
	return things, reagents, help, nil
}

func trimBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}

var prefabLine = regexp.MustCompile(`"([^"]+)"\s*:\s*"([^"]*)"`)

// readExistingPrefabs parses the current prefabs.go so names the localization
// does not title survive a regeneration.
func readExistingPrefabs(path string) (map[string]string, error) {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	s := string(data)
	start := strings.Index(s, "var Prefabs")
	end := strings.Index(s, "// PrefabByHash")
	if start < 0 || end < 0 || end < start {
		return nil, fmt.Errorf("%s does not look like the generated prefab table", path)
	}
	for _, m := range prefabLine.FindAllStringSubmatch(s[start:end], -1) {
		out[m[1]] = m[2]
	}
	return out, nil
}

func writePrefabs(path string, prefabs map[string]string) error {
	names := make([]string, 0, len(prefabs))
	for n := range prefabs {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("// Code generated by tools/gengamedata and tools/import-devices; DO NOT EDIT.\n")
	b.WriteString("//\n")
	b.WriteString("// Prefabs maps a prefab name to its display title. The IC10 hash of a prefab is\n")
	b.WriteString("// Hash(name) (CRC-32). Two producers write this file: gengamedata supplies the\n")
	b.WriteString("// localized titles, import-devices the names the localization does not carry\n")
	b.WriteString("// (procedural wreckage, kits). Each keeps what the other contributed, so\n")
	b.WriteString("// regenerating into a path with no such file drops the other side's names.\n")
	b.WriteString("package builtin\n\n")
	b.WriteString("// Prefabs maps a prefab name to its display title.\n")
	b.WriteString("var Prefabs = map[string]string{\n")
	for _, n := range names {
		fmt.Fprintf(&b, "\t%s: %s,\n", strconv.Quote(n), strconv.Quote(prefabs[n]))
	}
	b.WriteString("}\n\n")
	b.WriteString("// PrefabByHash maps a prefab hash (CRC-32 of the name) back to its name.\n")
	b.WriteString("var PrefabByHash = map[uint32]string{}\n\n")
	b.WriteString("func init() {\n\tfor name := range Prefabs {\n\t\tPrefabByHash[Hash(name)] = name\n\t}\n}\n")

	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return fmt.Errorf("format prefabs: %w", err)
	}
	return os.WriteFile(path, src, 0o644)
}

func writeScriptHelp(path string, help map[string]string) error {
	names := make([]string, 0, len(help))
	for n := range help {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("// Code generated by tools/gengamedata from the game's english.xml; DO NOT EDIT.\n")
	b.WriteString("//\n")
	b.WriteString("// ScriptCommandHelp maps an IC10 mnemonic to the game's own help text: the\n")
	b.WriteString("// `ScriptDescription*` records, falling back to `ScriptCommand*` for the\n")
	b.WriteString("// mnemonics that have no description (see readLocalization for why).\n")
	b.WriteString("package builtin\n\n")
	b.WriteString("// ScriptCommandHelp maps an IC10 mnemonic to the game's own help text.\n")
	b.WriteString("var ScriptCommandHelp = map[string]string{\n")
	for _, n := range names {
		fmt.Fprintf(&b, "\t%s: %s,\n", strconv.Quote(n), strconv.Quote(help[n]))
	}
	b.WriteString("}\n\n")
	b.WriteString("// init refreshes the help text of the known instructions from the game, keeping\n")
	b.WriteString("// their operand signatures. Mnemonics listed in scriptHelpKeep (a hand-written\n")
	b.WriteString("// map, see help_overrides.go) keep the description from instructions.go instead,\n")
	b.WriteString("// because the game's text for them is wrong rather than merely terse.\n")
	b.WriteString("func init() {\n")
	b.WriteString("\tfor name, help := range ScriptCommandHelp {\n")
	b.WriteString("\t\tins, ok := IC10Instructions[name]\n")
	b.WriteString("\t\tif !ok || scriptHelpKeep[name] {\n")
	b.WriteString("\t\t\tcontinue\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\tins.Desc = help\n")
	b.WriteString("\t\tIC10Instructions[name] = ins\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n")

	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return fmt.Errorf("format script help: %w", err)
	}
	return os.WriteFile(path, src, 0o644)
}
