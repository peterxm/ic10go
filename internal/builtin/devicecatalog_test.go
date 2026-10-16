package builtin

import (
	"encoding/json"
	"testing"
)

// TestDeviceCatalogIsSane guards the checked-in devicecatalog.json (1 MB, written
// by tools/import-devices from the in-game exporter's scan).
//
// Nothing else validates it: the generated init() keeps DeviceCatalog empty when
// the JSON does not parse, so a corrupted file would silently drop per-device
// property completion from the editor instead of failing anywhere. The stamp is
// also how a human decides whether the catalog is older than the running game.
func TestDeviceCatalogIsSane(t *testing.T) {
	var doc DeviceCatalogFile
	if err := json.Unmarshal(deviceCatalogJSON, &doc); err != nil {
		t.Fatalf("devicecatalog.json does not parse: %v (re-run tools/import-devices)", err)
	}
	if doc.GameVersion == "" {
		t.Error("the exporter's gameVersion stamp is missing")
	}
	if doc.DeviceCount != len(doc.Devices) {
		t.Errorf("deviceCount = %d but the file lists %d devices", doc.DeviceCount, len(doc.Devices))
	}
	if len(doc.Devices) < 100 {
		t.Errorf("only %d devices: re-run the in-game exporter and tools/import-devices", len(doc.Devices))
	}
	for _, d := range doc.Devices {
		if d.Name == "" {
			t.Errorf("device with an empty prefabName: %+v", d)
			break
		}
	}
	if len(DeviceCatalog) == 0 {
		t.Error("DeviceCatalog is empty after init")
	}
}
