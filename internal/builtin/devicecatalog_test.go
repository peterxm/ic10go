package builtin

import (
	"encoding/json"
	"testing"
)

// TestDeviceCatalogIsSane guards the checked-in devicecatalog.json (written by
// tools/import-devices from the in-game exporter's scan).
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
		// The scan's displayName follows the player's UI language: an export made
		// in a Chinese client wrote 431 Chinese titles into the catalog before
		// tools/import-devices started re-titling from the game's English
		// localization. The catalog is English by design, like Prefabs.
		if d.Title == "" {
			t.Errorf("device %q has no title", d.Name)
		}
		for _, r := range d.Title {
			if r > 127 {
				t.Errorf("device %q title %q is not ASCII", d.Name, d.Title)
				break
			}
		}
	}
	if len(DeviceCatalog) == 0 {
		t.Error("DeviceCatalog is empty after init")
	}
}

// TestDeviceCatalogSlotShape guards the format-2 slot fields: the exporter
// writes them, tools/import-devices passes them through, and the editor reads
// them. A tag typo would silently drop the per-slot data instead of failing.
func TestDeviceCatalogSlotShape(t *testing.T) {
	const doc = `{"formatVersion":2,"gameVersion":"test","deviceCount":1,"devices":[
	{"prefabName":"StructureSuitStorage","prefabHash":255034731,"displayName":"Suit Storage",
	 "slotCount":3,"slotWriteable":true,"properties":[],"slotProperties":[],
	 "slots":[
	  {"index":0,"class":"Helmet","key":"Helmet","keyHash":1,"interactable":true,
	   "swappable":true,"locked":false,"specificPrefabHashes":[],
	   "read":[{"name":"Occupied","logicSlotType":1}],
	   "write":[{"name":"On","logicSlotType":22}]},
	  {"index":1,"class":"Suit","key":"Suit","keyHash":2,"interactable":true,
	   "swappable":true,"locked":false,"specificPrefabHashes":[123],
	   "read":[{"name":"PressureAir","logicSlotType":14}],"write":[]}
	 ]}]}`
	var f DeviceCatalogFile
	if err := json.Unmarshal([]byte(doc), &f); err != nil {
		t.Fatal(err)
	}
	d := f.Devices[0]
	if !d.SlotWriteable || d.SlotCount != 3 || len(d.Slots) != 2 {
		t.Fatalf("device fields: %+v", d)
	}
	if d.Slots[0].Class != "Helmet" || d.Slots[0].Key != "Helmet" || d.Slots[0].KeyHash != 1 {
		t.Errorf("slot 0 identity: %+v", d.Slots[0])
	}
	if got := d.Slots[0].Write; len(got) != 1 || got[0].Name != "On" || got[0].Type != 22 {
		t.Errorf("slot 0 write: %+v", got)
	}
	if got := d.Slots[1].SpecificPrefabHashes; len(got) != 1 || got[0] != 123 {
		t.Errorf("slot 1 specificPrefabHashes: %v", got)
	}
	if got := d.Slots[1].Read; len(got) != 1 || got[0].Name != "PressureAir" {
		t.Errorf("slot 1 read: %+v", got)
	}
}
