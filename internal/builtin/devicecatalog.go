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

// DeviceSlot is one slot on a prefab: what it accepts (Class /
// SpecificPrefabHashes) and which LogicSlotTypes `ls` and `ss` accept for that
// slot. The game answers this per slot, so a single device-level list cannot
// describe e.g. a suit storage (helmet / suit / back slots differ).
type DeviceSlot struct {
	Index                int                  `json:"index"`
	Class                string               `json:"class"`
	Key                  string               `json:"key"`
	KeyHash              int32                `json:"keyHash"`
	SpecificPrefabHashes []int32              `json:"specificPrefabHashes"`
	Interactable         bool                 `json:"interactable"`
	Swappable            bool                 `json:"swappable"`
	Locked               bool                 `json:"locked"`
	Read                 []DeviceSlotProperty `json:"read"`
	Write                []DeviceSlotProperty `json:"write"`
}

// DeviceInstruction is one opcode of a device's packed memory protocol (the
// Stationpedia's "memory" page): its enum member name, numeric opcode, and the
// bit layout text (color markup stripped) for that opcode.
type DeviceInstruction struct {
	Name   string `json:"name"`
	Value  int    `json:"value"`
	Format string `json:"format"`
}

// DeviceMemory is the logic-memory ("stack") surface of a prefab: whether the
// IC10 `get` / `put` instructions accept it, how many addresses it has, and,
// for devices with a packed instruction protocol (suit storage, printers,
// sorters, silos, ...), the instruction enum and its opcodes.
type DeviceMemory struct {
	Readable        bool                `json:"readable"`
	Writeable       bool                `json:"writeable"`
	StackSize       int                 `json:"stackSize"`
	InstructionType string              `json:"instructionType"`
	Instructions    []DeviceInstruction `json:"instructions"`
}

// Device is one prefab's logic surface, as scanned inside the game by
// tools/ingame-exporter. It is what completion uses to offer a prefab's own
// properties instead of the whole vocabulary. The JSON tags match devices.json.
type Device struct {
	Name           string               `json:"prefabName"`
	Hash           int32                `json:"prefabHash"`
	Title          string               `json:"displayName"`
	SlotCount      int                  `json:"slotCount"`
	SlotWriteable  bool                 `json:"slotWriteable"`
	Properties     []DeviceProperty     `json:"properties"`
	SlotProperties []DeviceSlotProperty `json:"slotProperties"`
	Slots          []DeviceSlot         `json:"slots"`
	Memory         *DeviceMemory        `json:"memory"`
}

// DeviceCatalog maps a prefab name to its logic surface. It is filled at init
// from devicecatalog.json, which tools/import-devices writes; it stays empty
// until the in-game exporter has run and its JSON imported.
var DeviceCatalog = map[string]Device{}

// DeviceCatalogFile is the shape of devices.json (and devicecatalog.json). The
// header fields are the exporter's stamps: they are what a human compares against
// the running game's version when deciding whether the catalog needs a re-scan.
type DeviceCatalogFile struct {
	FormatVersion int      `json:"formatVersion"`
	GameVersion   string   `json:"gameVersion"`
	DeviceCount   int      `json:"deviceCount"`
	Devices       []Device `json:"devices"`
}
