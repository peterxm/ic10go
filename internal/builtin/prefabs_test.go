package builtin

import (
	"strings"
	"testing"
)

// TestPrefabsKeepExporterOnlyNames: prefabs.go has two producers — the game's
// localization (tools/gengamedata, titles) and the in-game exporter
// (tools/import-devices, the names the localization does not carry). Each keeps
// what the other contributed, and that merge reads the file it is about to
// overwrite, so running either tool with a fresh output path silently drops the
// other's contribution: regenerating the titles into an empty directory loses 278
// exporter-only names (measured). These anchors are the guard for that.
func TestPrefabsKeepExporterOnlyNames(t *testing.T) {
	for _, name := range []string{
		"H2CombustorMirrored", "ItemAuthoringToolRocketNetwork", "ItemKitHeatExchanger",
		"ItemKitModularConsole", "ItemKitModularConsoleInputs",
	} {
		if _, ok := Prefabs[name]; !ok {
			t.Errorf("Prefabs lost %q — it only exists in the in-game exporter's list; re-run tools/import-devices with the default output path", name)
		}
	}
}

// TestPrefabTitlesAreASCII: the table is English by design. The in-game scan's
// DisplayName follows the player's game language (one run in Chinese would have
// titled 239 new names in CJK), and the game's missing-localisation marker
// ("<N:CN:...>") must never reach the table either. Both are properties of the
// data, so they are checked here rather than trusted from the importer.
func TestPrefabTitlesAreASCII(t *testing.T) {
	for name, title := range Prefabs {
		if strings.HasPrefix(title, "<N:") {
			t.Errorf("Prefabs[%q] carries the missing-localisation marker: %q", name, title)
			continue
		}
		for _, r := range title {
			if r > 127 {
				t.Errorf("Prefabs[%q] = %q is not ASCII (the table is English)", name, title)
				break
			}
		}
	}
}
