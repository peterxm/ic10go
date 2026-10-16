package builtin

import "testing"

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
