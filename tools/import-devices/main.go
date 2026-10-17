// Command import-devices turns the in-game exporter's JSON into ic10go tables:
//
//   - prefabs.json -> merge into internal/builtin/prefabs.go
//   - devices.json -> internal/builtin/devicecatalog.json (embedded) + loader
//
// The exporter runs inside Stationeers (tools/ingame-exporter) and writes both
// files under Documents/My Games/Stationeers/ic10go/. Run:
//
//	go run ./tools/import-devices [prefabs.json] [devices.json] [outdir]
package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"ic10go/internal/builtin"
)

const defaultOutDir = "internal/builtin"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "import-devices:", err)
		os.Exit(1)
	}
}

func run() error {
	prefabsPath, devicesPath, outDir := "", "", defaultOutDir
	args := os.Args[1:]
	if len(args) > 0 {
		prefabsPath = args[0]
	}
	if len(args) > 1 {
		devicesPath = args[1]
	}
	if len(args) > 2 {
		outDir = args[2]
	}

	if prefabsPath == "" || devicesPath == "" {
		found, err := findExports()
		if err != nil && prefabsPath == "" && devicesPath == "" {
			return err
		}
		if prefabsPath == "" {
			prefabsPath = found["prefabs.json"]
		}
		if devicesPath == "" {
			devicesPath = found["devices.json"]
		}
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if prefabsPath != "" {
		if err := importPrefabs(prefabsPath, filepath.Join(outDir, "prefabs.go")); err != nil {
			return err
		}
	}
	if devicesPath != "" {
		if err := importDevices(devicesPath, outDir); err != nil {
			return err
		}
	}
	return nil
}

// findExports looks for the exporter's files in the common user folders.
func findExports() (map[string]string, error) {
	home, _ := os.UserHomeDir()
	dirs := []string{
		filepath.Join(home, "Documents", "My Games", "Stationeers", "ic10go"),
		filepath.Join(home, "My Games", "Stationeers", "ic10go"),
		filepath.Join(home, ".local", "share", "Steam", "steamapps", "compatdata", "544550",
			"pfx", "drive_c", "users", "steamuser", "Documents", "My Games", "Stationeers", "ic10go"),
	}
	found := map[string]string{}
	for _, dir := range dirs {
		for _, name := range []string{"prefabs.json", "devices.json"} {
			if found[name] != "" {
				continue
			}
			if p := filepath.Join(dir, name); fileExists(p) {
				found[name] = p
			}
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no prefabs.json/devices.json found; pass their paths as arguments")
	}
	return found, nil
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// -- prefabs ----------------------------------------------------------------

type prefabsFile struct {
	GameVersion string `json:"gameVersion"`
	Prefabs     []struct {
		Name        string `json:"name"`
		Hash        int64  `json:"hash"`
		DisplayName string `json:"displayName"`
	} `json:"prefabs"`
}

func importPrefabs(jsonPath, outPath string) error {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return err
	}
	var doc prefabsFile
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("%s: %w", jsonPath, err)
	}
	if len(doc.Prefabs) == 0 {
		return fmt.Errorf("%s has no prefabs", jsonPath)
	}

	merged, err := readExistingPrefabs(outPath)
	if err != nil {
		return err
	}
	had := len(merged)
	skipped := 0
	// The tables are the game's, not the enabled mods' -- an export with mods on
	// would otherwise smuggle their prefabs in (measured: 395 of 2523).
	for name := range merged {
		if builtin.IsModPrefab(name) {
			delete(merged, name)
		}
	}
	before := len(merged)
	dropped := had - before
	for _, p := range doc.Prefabs {
		if p.Name == "" {
			continue
		}
		if builtin.IsModPrefab(p.Name) {
			skipped++
			continue
		}
		// Keep the English title from the localization merge; the runtime scan's
		// DisplayName follows the player's game language. Only add names the
		// localization does not carry (procedural wreckage, kits).
		if _, exists := merged[p.Name]; exists {
			continue
		}
		// The scan's DisplayName follows the player's game language, so using it
		// here would write e.g. Chinese titles for whatever the localization does
		// not carry (measured: 239 new names, all with CJK titles). The table is
		// English, and gengamedata can supply a real English title for any name the
		// localization has; for the rest the prefab name is the truthful title.
		merged[p.Name] = p.Name
	}
	// Titles an older import took from a localized scan carry the game's
	// missing-localisation marker ("<N:CN:Key>", "<N:EN:Key>"); that is not a
	// title. Normalise after the merge so entries that are not in this scan at all
	// are repaired too.
	repaired := 0
	for name, title := range merged {
		if strings.HasPrefix(title, "<N:") {
			merged[name] = name
			repaired++
		}
	}
	if err := writePrefabs(outPath, merged); err != nil {
		return err
	}
	if repaired > 0 {
		fmt.Printf("repaired %d missing-localisation titles\n", repaired)
	}
	if skipped > 0 {
		fmt.Printf("skipped %d third-party (mod) prefabs\n", skipped)
	}
	fmt.Printf("wrote %s: %d prefabs (was %d, dropped %d, added %d)\n",
		outPath, len(merged), had, dropped, len(merged)-before)
	return nil
}

var prefabLine = regexp.MustCompile(`"([^"]+)"\s*:\s*"([^"]*)"`)

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
		return nil, fmt.Errorf("%s does not look like the prefab table", path)
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
	b.WriteString("// Code generated by tools/gengamedata / tools/import-devices from the game; DO NOT EDIT.\n")
	b.WriteString("//\n")
	b.WriteString("// Prefabs maps a prefab name to its display title. The IC10 hash of a prefab is\n")
	b.WriteString("// Hash(name) (CRC-32).\n")
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

// -- devices ----------------------------------------------------------------

// defaultXMLs are where the game keeps its English localization.
var defaultXMLs = []string{
	"/home/*/.local/share/Steam/steamapps/common/Stationeers/rocketstation_Data/StreamingAssets/Language/english.xml",
	`C:\Program Files (x86)\Steam\steamapps\common\Stationeers\rocketstation_Data\StreamingAssets\Language\english.xml`,
}

// englishTitles reads the prefab titles the game ships in English. The in-game
// scan's displayName follows the player's UI language, so an export made in a
// Chinese client writes Chinese titles into the catalog (measured: 431 of 567).
// Prefabs is English for the same reason; the catalog has to match it.
func englishTitles() map[string]string {
	out := map[string]string{}
	for _, pattern := range defaultXMLs {
		matches, _ := filepath.Glob(pattern)
		if len(matches) == 0 {
			continue
		}
		raw, err := os.ReadFile(matches[0])
		if err != nil {
			continue
		}
		dec := xml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))))
		kind, field, key, value := "", "", "", ""
		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			switch t := tok.(type) {
			case xml.StartElement:
				switch t.Name.Local {
				case "RecordThing", "RecordReagent", "Record":
					kind, key, value = t.Name.Local, "", ""
				case "Key", "Value":
					field = t.Name.Local
				}
			case xml.CharData:
				switch field {
				case "Key":
					key += string(t)
				case "Value":
					value += string(t)
				}
			case xml.EndElement:
				if t.Name.Local == "Key" || t.Name.Local == "Value" {
					field = ""
				}
				if kind != "" && t.Name.Local == kind {
					if k := strings.TrimSpace(key); k != "" && strings.TrimSpace(value) != "" {
						out[k] = strings.TrimSpace(value)
					}
					kind = ""
				}
			}
		}
		break
	}
	return out
}

type deviceJSON struct {
	PrefabName  string `json:"prefabName"`
	DisplayName string `json:"displayName"`
}

// splitObjects returns the top-level objects of a JSON array body, byte for
// byte. Re-encoding would expand the exporter's compact catalog (933 KB) into a
// much larger file, so only the display names are patched in place.
func splitObjects(body string) ([]string, error) {
	var out []string
	depth, start := 0, -1
	inStr, esc := false, false
	for i := 0; i < len(body); i++ {
		c := body[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("unbalanced objects")
			}
			if depth == 0 {
				if start < 0 {
					return nil, fmt.Errorf("unbalanced objects")
				}
				out = append(out, body[start:i+1])
				start = -1
			}
		}
	}
	if depth != 0 || start >= 0 {
		return nil, fmt.Errorf("unbalanced objects")
	}
	return out, nil
}

// replaceDisplayName swaps the value of "displayName" inside one object.
func replaceDisplayName(obj, title string) string {
	const key = `"displayName"`
	i := strings.Index(obj, key)
	if i < 0 {
		return obj
	}
	j := strings.IndexByte(obj[i+len(key):], '"')
	if j < 0 {
		return obj
	}
	q := i + len(key) + j
	k := q + 1
	for k < len(obj) {
		if obj[k] == '\\' {
			k += 2
			continue
		}
		if obj[k] == '"' {
			break
		}
		k++
	}
	if k >= len(obj) {
		return obj
	}
	enc, err := json.Marshal(title)
	if err != nil {
		return obj
	}
	return obj[:q] + string(enc) + obj[k+1:]
}

func importDevices(jsonPath, outDir string) error {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return err
	}
	var top struct {
		FormatVersion int    `json:"formatVersion"`
		GameVersion   string `json:"gameVersion"`
	}
	if err := json.Unmarshal(data, &top); err != nil {
		return fmt.Errorf("%s: %w", jsonPath, err)
	}
	text := string(data)
	ds := strings.Index(text, `"devices"`)
	if ds < 0 {
		return fmt.Errorf("%s has no devices", jsonPath)
	}
	open := strings.IndexByte(text[ds:], '[')
	if open < 0 {
		return fmt.Errorf("%s: cannot find the devices array", jsonPath)
	}
	open += ds
	end := strings.LastIndexByte(text, ']')
	if end < open {
		return fmt.Errorf("%s: cannot find the end of the devices array", jsonPath)
	}
	objs, err := splitObjects(text[open+1 : end])
	if err != nil {
		return fmt.Errorf("%s: %w", jsonPath, err)
	}

	titles := englishTitles()
	kept := make([]string, 0, len(objs))
	dropped, retitled := 0, 0
	for _, obj := range objs {
		var d deviceJSON
		if err := json.Unmarshal([]byte(obj), &d); err != nil {
			return fmt.Errorf("%s: %w", jsonPath, err)
		}
		if builtin.IsModPrefab(d.PrefabName) {
			dropped++
			continue
		}
		title := strings.TrimSpace(titles[d.PrefabName])
		if title == "" {
			title = d.PrefabName
		}
		if title != d.DisplayName {
			obj = replaceDisplayName(obj, title)
			retitled++
		}
		kept = append(kept, obj)
	}

	var b strings.Builder
	b.WriteString("{\n")
	fmt.Fprintf(&b, "  \"formatVersion\": %d,\n", top.FormatVersion)
	fmt.Fprintf(&b, "  \"gameVersion\": %q,\n", top.GameVersion)
	fmt.Fprintf(&b, "  \"deviceCount\": %d,\n", len(kept))
	b.WriteString("  \"devices\": [\n")
	for i, obj := range kept {
		if i > 0 {
			b.WriteString(",\n")
		}
		b.WriteString("    ")
		b.WriteString(obj)
	}
	b.WriteString("\n  ]\n}\n")
	out := []byte(b.String())
	var check map[string]any
	if err := json.Unmarshal(out, &check); err != nil {
		return fmt.Errorf("rewritten catalog is not valid JSON: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "devicecatalog.json"), out, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "devicecatalog_gen.go"), []byte(devicecatalogGen), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s: %d devices (%d third-party dropped, %d titles set from the English localization)\n",
		filepath.Join(outDir, "devicecatalog.json"), len(kept), dropped, retitled)
	return nil
}

// devicecatalogGen is the fixed loader; the data lives in devicecatalog.json
// next to it and is embedded, so the generated Go file stays small.
const devicecatalogGen = `// Code generated by tools/import-devices; DO NOT EDIT.
package builtin

import (
	_ "embed"
	"encoding/json"
)

//go:embed devicecatalog.json
var deviceCatalogJSON []byte

func init() {
	var doc DeviceCatalogFile
	if err := json.Unmarshal(deviceCatalogJSON, &doc); err != nil {
		return
	}
	for _, d := range doc.Devices {
		if d.Name != "" {
			DeviceCatalog[d.Name] = d
		}
	}
}
`
