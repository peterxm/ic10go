package builtin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Source says where a prefab or a device comes from.
type Source struct {
	// Kind is "game" for the embedded tables and "mod" for a user-local pack.
	Kind string
	// Name is the pack label for a mod entry, empty for the game.
	Name string
}

// IsGame reports whether the entry belongs to the game itself. The community
// wiki has pages for those, and only those.
func (s Source) IsGame() bool { return s.Kind == "game" }

// PrefabEntry is one prefab: its title and where it came from.
type PrefabEntry struct {
	Title  string
	Source Source
}

// Catalog is what the tools look prefabs and devices up in: the embedded game
// tables plus zero or more user-local mod packs.
//
// The embedded tables describe the game and are generated from it. A pack is one
// in-game export (tools/ingame-exporter), and an export mirrors whatever mods
// were enabled when it ran -- so a name that the embedded tables already carry is
// the game's, and only the remaining names are attributed to the pack. That
// derivation is what keeps the wiki-link rule honest without a hand-kept list.
type Catalog struct {
	prefabs  map[string]PrefabEntry
	byHash   map[uint32]string
	devices  map[string]Device
	warnings []string
	version  string
}

// Base returns the catalog built from the embedded (game) tables.
func Base() *Catalog {
	c := &Catalog{
		prefabs: make(map[string]PrefabEntry, len(Prefabs)),
		byHash:  make(map[uint32]string, len(PrefabByHash)),
		devices: make(map[string]Device, len(DeviceCatalog)),
		version: embeddedGameVersion(),
	}
	for name, title := range Prefabs {
		c.prefabs[name] = PrefabEntry{Title: title, Source: Source{Kind: "game"}}
	}
	for h, name := range PrefabByHash {
		c.byHash[h] = name
	}
	for name, d := range DeviceCatalog {
		c.devices[name] = d
	}
	return c
}

func embeddedGameVersion() string {
	var doc struct {
		GameVersion string `json:"gameVersion"`
	}
	_ = json.Unmarshal(deviceCatalogJSON, &doc)
	return doc.GameVersion
}

// GameVersion is the game version the embedded tables were exported from.
func (c *Catalog) GameVersion() string { return c.version }

// Prefab looks a prefab name up.
func (c *Catalog) Prefab(name string) (PrefabEntry, bool) {
	e, ok := c.prefabs[name]
	return e, ok
}

// PrefabName maps a prefab hash back to its name.
func (c *Catalog) PrefabName(h uint32) (string, bool) {
	n, ok := c.byHash[h]
	return n, ok
}

// Device looks a prefab's logic surface up.
func (c *Catalog) Device(name string) (Device, bool) {
	d, ok := c.devices[name]
	return d, ok
}

// EachPrefab calls fn for every known prefab, in no particular order.
func (c *Catalog) EachPrefab(fn func(name string, e PrefabEntry)) {
	for name, e := range c.prefabs {
		fn(name, e)
	}
}

// EachDevice calls fn for every device, in no particular order.
func (c *Catalog) EachDevice(fn func(name string, d Device)) {
	for name, d := range c.devices {
		fn(name, d)
	}
}

// Len reports how many prefabs the catalog knows.
func (c *Catalog) Len() int { return len(c.prefabs) }

// ModPrefabs reports how many prefabs came from packs rather than the game.
func (c *Catalog) ModPrefabs() int {
	n := 0
	for _, e := range c.prefabs {
		if !e.Source.IsGame() {
			n++
		}
	}
	return n
}

// ModDevices reports how many devices came from packs rather than the game.
func (c *Catalog) ModDevices() int {
	n := 0
	for name := range c.devices {
		if e, ok := c.prefabs[name]; ok && !e.Source.IsGame() {
			n++
		}
	}
	return n
}

// Warnings are problems found while merging packs: a pack from another game
// version, or a name whose hash is already taken. They are shown once (the
// language server logs them), not turned into diagnostics.
func (c *Catalog) Warnings() []string { return c.warnings }

func (c *Catalog) clone() *Catalog {
	out := &Catalog{
		prefabs:  make(map[string]PrefabEntry, len(c.prefabs)),
		byHash:   make(map[uint32]string, len(c.byHash)),
		devices:  make(map[string]Device, len(c.devices)),
		warnings: append([]string(nil), c.warnings...),
		version:  c.version,
	}
	for k, v := range c.prefabs {
		out.prefabs[k] = v
	}
	for k, v := range c.byHash {
		out.byHash[k] = v
	}
	for k, v := range c.devices {
		out.devices[k] = v
	}
	return out
}

// Pack is one in-game export: the prefabs and devices the game had when it ran,
// including whatever mods were enabled.
type Pack struct {
	// Label names the pack in hovers and warnings, e.g. "LettersMod" or
	// "mods 2026-10-18".
	Label string
	// GameVersion is the exporter's stamp; empty when only one of the two files
	// was read and carried no stamp.
	GameVersion string
	Prefabs     map[string]string // name -> title
	Devices     map[string]Device
}

// LoadPack reads one exporter run. path is the export directory, prefabs.json,
// or devices.json; the sibling file is picked up when it exists.
func LoadPack(label, path string) (*Pack, error) {
	var prefabsPath, devicesPath string
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		prefabsPath = filepath.Join(path, "prefabs.json")
		devicesPath = filepath.Join(path, "devices.json")
		if !fileExists(prefabsPath) {
			prefabsPath = ""
		}
		if !fileExists(devicesPath) {
			devicesPath = ""
		}
		if prefabsPath == "" && devicesPath == "" {
			return nil, fmt.Errorf("%s: no prefabs.json or devices.json", path)
		}
	} else if filepath.Base(path) == "devices.json" {
		devicesPath = path
		if sib := filepath.Join(filepath.Dir(path), "prefabs.json"); fileExists(sib) {
			prefabsPath = sib
		}
	} else {
		prefabsPath = path
		if sib := filepath.Join(filepath.Dir(path), "devices.json"); fileExists(sib) {
			devicesPath = sib
		}
	}

	p := &Pack{Label: label, Prefabs: map[string]string{}, Devices: map[string]Device{}}
	if prefabsPath != "" {
		var doc struct {
			GameVersion string `json:"gameVersion"`
			Prefabs     []struct {
				Name        string `json:"name"`
				DisplayName string `json:"displayName"`
			} `json:"prefabs"`
		}
		if err := readJSONFile(prefabsPath, &doc); err != nil {
			return nil, err
		}
		if doc.GameVersion != "" {
			p.GameVersion = doc.GameVersion
		}
		for _, e := range doc.Prefabs {
			if e.Name == "" {
				continue
			}
			title := strings.TrimSpace(e.DisplayName)
			if title == "" {
				title = e.Name
			}
			p.Prefabs[e.Name] = title
		}
	}
	if devicesPath != "" {
		var doc DeviceCatalogFile
		if err := readJSONFile(devicesPath, &doc); err != nil {
			return nil, err
		}
		if p.GameVersion == "" {
			p.GameVersion = doc.GameVersion
		}
		for _, d := range doc.Devices {
			if d.Name != "" {
				p.Devices[d.Name] = d
			}
		}
	}
	if len(p.Prefabs) == 0 && len(p.Devices) == 0 {
		return nil, fmt.Errorf("%s: the export is empty", label)
	}
	return p, nil
}

// LoadPackSpec loads "path" or "label=path". The label is what hovers and
// warnings call the pack; it defaults to "mods".
func LoadPackSpec(spec string) (*Pack, error) {
	label, path := "mods", spec
	if i := strings.Index(spec, "="); i >= 0 {
		label, path = spec[:i], spec[i+1:]
	}
	if label == "" {
		label = "mods"
	}
	if path == "" {
		return nil, fmt.Errorf("empty pack path")
	}
	return LoadPack(label, path)
}

func readJSONFile(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// WithPacks returns a catalog with the packs merged in. The receiver is not
// modified, so a base catalog can be shared.
//
// The game's entries always win: a pack that carries a name the embedded tables
// already know is a mod re-shipping a game prefab, not a new one. Only the names
// the game does not have are attributed to the pack, which is exactly the set the
// community wiki has no page for.
func (c *Catalog) WithPacks(packs ...*Pack) *Catalog {
	out := c.clone()
	for _, p := range packs {
		if p == nil {
			continue
		}
		if p.GameVersion != "" && c.version != "" && p.GameVersion != c.version {
			out.warnings = append(out.warnings, fmt.Sprintf(
				"pack %q was exported from game %s but the tables are %s; re-run the exporter if the prefabs look wrong",
				p.Label, p.GameVersion, c.version))
		}
		for _, name := range sortedKeys(p.Prefabs) {
			if _, ok := out.prefabs[name]; ok {
				continue
			}
			out.addModPrefab(name, p.Prefabs[name], p.Label)
		}
		for _, name := range sortedDeviceKeys(p.Devices) {
			if _, ok := out.devices[name]; ok {
				continue
			}
			d := p.Devices[name]
			out.devices[name] = d
			if _, ok := out.prefabs[name]; !ok {
				// A device the prefab scan missed: keep the hash and name usable
				// too, so hover and completion do not depend on scan order.
				title := strings.TrimSpace(d.Title)
				if title == "" {
					title = name
				}
				out.addModPrefab(name, title, p.Label)
			}
		}
	}
	return out
}

func (c *Catalog) addModPrefab(name, title, label string) {
	c.prefabs[name] = PrefabEntry{Title: title, Source: Source{Kind: "mod", Name: label}}
	h := Hash(name)
	if other, ok := c.byHash[h]; ok {
		if other != name {
			c.warnings = append(c.warnings, fmt.Sprintf(
				"pack %q: %q has the same hash as %q; the first name wins for numeric lookups", label, name, other))
		}
		return
	}
	c.byHash[h] = name
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedDeviceKeys(m map[string]Device) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
