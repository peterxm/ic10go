package builtin

// DeviceProperty is one logic type a prefab accepts. Type is the game's
// LogicType value; Name is its member name (a key of LogicTypes).
type DeviceProperty struct {
	Name  string `json:"name"`
	Type  int    `json:"logicType"`
	Read  bool   `json:"read"`
	Write bool   `json:"write"`
}

// DeviceSlotProperty is one slot property a prefab's slots answer.
type DeviceSlotProperty struct {
	Name string `json:"name"`
	Type int    `json:"logicSlotType"`
}

// Device is one prefab's logic surface, as scanned inside the game by
// tools/ingame-exporter. It is what completion uses to offer a prefab's own
// properties instead of the whole vocabulary. The JSON tags match devices.json.
type Device struct {
	Name           string               `json:"prefabName"`
	Hash           int32                `json:"prefabHash"`
	Title          string               `json:"displayName"`
	SlotCount      int                  `json:"slotCount"`
	Properties     []DeviceProperty     `json:"properties"`
	SlotProperties []DeviceSlotProperty `json:"slotProperties"`
}

// DeviceCatalog maps a prefab name to its logic surface. It is filled at init
// from devicecatalog.json, which tools/import-devices writes; it stays empty
// until the in-game exporter has run and its JSON imported.
var DeviceCatalog = map[string]Device{}

// DeviceCatalogFile is the shape of devices.json (and devicecatalog.json).
type DeviceCatalogFile struct {
	Devices []Device `json:"devices"`
}
