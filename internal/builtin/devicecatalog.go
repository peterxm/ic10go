package builtin

// DeviceProperty is one logic type a prefab accepts. Type is the game's
// LogicType value; Name is its member name (a key of LogicTypes).
type DeviceProperty struct {
	Name  string
	Type  int
	Read  bool
	Write bool
}

// DeviceSlotProperty is one slot property a prefab's slots answer.
type DeviceSlotProperty struct {
	Name string
	Type int
}

// Device is one prefab's logic surface, as scanned inside the game by
// tools/ingame-exporter. It is what Completion and hover use to offer a prefab's
// own properties instead of the whole vocabulary.
type Device struct {
	Name           string
	Hash           int32
	Title          string
	SlotCount      int
	Properties     []DeviceProperty
	SlotProperties []DeviceSlotProperty
}

// DeviceCatalog maps a prefab name to its logic surface. It is empty until
// tools/import-devices runs; devicecatalog_gen.go fills it in.
var DeviceCatalog = map[string]Device{}
