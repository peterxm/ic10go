package builtin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePack writes a minimal exporter run into a temp directory.
func writePack(t *testing.T, prefabs, devices string) string {
	t.Helper()
	dir := t.TempDir()
	if prefabs != "" {
		if err := os.WriteFile(filepath.Join(dir, "prefabs.json"), []byte(prefabs), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if devices != "" {
		if err := os.WriteFile(filepath.Join(dir, "devices.json"), []byte(devices), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const testDevices = `{"formatVersion":1,"gameVersion":"0.2.6428.27798","deviceCount":1,"devices":[
{"prefabName":"Text_K","prefabHash":1448182275,"displayName":"Letter K","slotCount":0,
 "properties":[{"name":"On","logicType":28,"read":true,"write":true}],"slotProperties":[]}]}`

func TestCatalogPackAttribution(t *testing.T) {
	dir := writePack(t, `{"formatVersion":1,"gameVersion":"0.2.6428.27798","prefabs":[
{"name":"Text_K","hash":1448182275,"displayName":"Letter K"},
{"name":"ItemIronOre","hash":1,"displayName":"Iron Ore (from the pack)"}]}`, testDevices)
	pack, err := LoadPack("LettersMod", dir)
	if err != nil {
		t.Fatal(err)
	}
	cat := Base().WithPacks(pack)

	// The pack's own prefab is attributed to it, and its hash resolves.
	e, ok := cat.Prefab("Text_K")
	if !ok {
		t.Fatal("Text_K missing from the catalog")
	}
	if e.Source.IsGame() || e.Source.Name != "LettersMod" || e.Title != "Letter K" {
		t.Errorf("Text_K entry = %+v, want a LettersMod entry titled \"Letter K\"", e)
	}
	if name, ok := cat.PrefabName(Hash("Text_K")); !ok || name != "Text_K" {
		t.Errorf("PrefabName(%d) = %q, %v", Hash("Text_K"), name, ok)
	}
	if _, ok := cat.Device("Text_K"); !ok {
		t.Error("the pack's device did not come through")
	}

	// A name the game already has stays the game's, even if the pack re-titles it.
	base, _ := Base().Prefab("ItemIronOre")
	got, _ := cat.Prefab("ItemIronOre")
	if !got.Source.IsGame() {
		t.Errorf("ItemIronOre is in the game table but the pack overrode its source: %+v", got.Source)
	}
	if got.Title != base.Title {
		t.Errorf("ItemIronOre title = %q, want the game's %q", got.Title, base.Title)
	}

	if n := cat.ModPrefabs(); n != 1 {
		t.Errorf("ModPrefabs = %d, want 1", n)
	}
	if n := cat.ModDevices(); n != 1 {
		t.Errorf("ModDevices = %d, want 1", n)
	}
	if len(cat.Warnings()) != 0 {
		t.Errorf("unexpected warnings: %v", cat.Warnings())
	}
}

func TestCatalogWithoutPacksIsTheGame(t *testing.T) {
	cat := Base()
	cat.EachPrefab(func(name string, e PrefabEntry) {
		if !e.Source.IsGame() {
			t.Errorf("%q is not marked as the game's", name)
		}
	})
	if cat.ModPrefabs() != 0 || cat.ModDevices() != 0 {
		t.Errorf("base catalog has mod entries: %d prefabs, %d devices", cat.ModPrefabs(), cat.ModDevices())
	}
	if cat.Len() != len(Prefabs) {
		t.Errorf("Len = %d, want %d", cat.Len(), len(Prefabs))
	}
}

func TestCatalogWarnsOnGameVersionMismatch(t *testing.T) {
	dir := writePack(t, `{"formatVersion":1,"gameVersion":"0.0.1","prefabs":[
{"name":"Text_K","hash":1448182275,"displayName":"Letter K"}]}`, "")
	pack, err := LoadPackSpec("Old=" + dir)
	if err != nil {
		t.Fatal(err)
	}
	cat := Base().WithPacks(pack)
	warns := strings.Join(cat.Warnings(), "\n")
	if !strings.Contains(warns, "0.0.1") || !strings.Contains(warns, "Old") {
		t.Errorf("expected a game-version warning naming the pack, got %q", warns)
	}
}

func TestLoadPackSpecLabel(t *testing.T) {
	dir := writePack(t, `{"formatVersion":1,"gameVersion":"0.2.6428.27798","prefabs":[
{"name":"Text_K","hash":1448182275,"displayName":"Letter K"}]}`, "")
	p, err := LoadPackSpec("LettersMod=" + dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Label != "LettersMod" {
		t.Errorf("label = %q, want LettersMod", p.Label)
	}
	p, err = LoadPackSpec(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Label != "mods" {
		t.Errorf("default label = %q, want mods", p.Label)
	}
	if len(p.Prefabs) != 1 || len(p.Devices) != 0 {
		t.Errorf("pack = %d prefabs, %d devices; want 1 and 0", len(p.Prefabs), len(p.Devices))
	}
}

func TestCatalogIsImmutable(t *testing.T) {
	dir := writePack(t, `{"formatVersion":1,"gameVersion":"0.2.6428.27798","prefabs":[
{"name":"Text_K","hash":1448182275,"displayName":"Letter K"}]}`, "")
	p, err := LoadPack("LettersMod", dir)
	if err != nil {
		t.Fatal(err)
	}
	base := Base()
	with := base.WithPacks(p)
	if _, ok := base.Prefab("Text_K"); ok {
		t.Fatal("WithPacks modified the receiver")
	}
	if _, ok := with.Prefab("Text_K"); !ok {
		t.Fatal("WithPacks did not add the pack's prefab")
	}
}
