# ic10go in-game exporter

A minimal Stationeers mod that dumps the two things only the running game knows:

- **`prefabs.json`** — every prefab's `name`, `hash` and `displayName`. This is
  the authoritative prefab list, including the procedural names (`ItemWreckage*`,
  kits) the localization does not carry.
- **`devices.json`** — for every logic-capable prefab: which `LogicType`s it
  accepts for **read** and **write**, its slot count, and **per slot** what the
  slot accepts (`class`, `key`, `specificPrefabHashes`) and which
  `LogicSlotType`s `ls` / `ss` accept for that slot. Slot read/write support is
  per slot in the game, not per device: a suit storage's helmet slot understands
  `On` / `Lock` / `Open`, its suit slot adds `PressureWaste` / `PressureAir`, and
  its back slot has neither. The old device-level `slotProperties` list is kept
  for format-1 consumers (slot 0's readable set). This is the data that cannot
  be read from `Assembly-CSharp.dll` (it lives in Unity's asset bundles).

It has **no Harmony patches and no runtime behaviour**: it scans once, writes the
files and stops. Nothing in the game changes.

## Why it needs the game

`CanLogicRead` / `CanLogicWrite` are methods on the game's device classes, but the
state they read (`HasOnOffState`, `HasPowerState`, …) is serialized in the asset
bundles. The DLL alone cannot answer "can a GasSensor read `On`". Only a scan of
`Prefab.AllPrefabs` inside the game can.

## Build

Needs the .NET SDK and a Stationeers install:

```bash
cd tools/ingame-exporter
dotnet build -c Release -p:StationeersDir="/path/to/Stationeers"
# -> bin/Release/ic10go-exporter.dll
```

On Linux the game path uses forward slashes, and the project pulls in
`Microsoft.NETFramework.ReferenceAssemblies` so `net472` builds without a
Windows SDK. A user-local SDK via the official `dotnet-install.sh` is enough.

## Install

The game only loads mods through a loader. Install **BepInEx** and
**[StationeersLaunchPad](https://github.com/StationeersLaunchPad/StationeersLaunchPad)**
first, then drop the mod folder in:

```
Documents\My Games\Stationeers\mods\ic10go-exporter\
├── About\About.xml
└── ic10go-exporter.dll
```

Enable it in the mods menu. `[ic10go-exporter]` in
`%USERPROFILE%\AppData\LocalLow\Rocketwerkz\rocketstation\Player.log` confirms it
loaded; a later line reports how many prefabs and devices were written.

## Output

```
Documents\My Games\Stationeers\ic10go\
├── prefabs.json
└── devices.json
```

`devices.json` uses the same shape as IZCode's catalog, so a file produced by
either mod can be consumed. Format 2 adds `slotWriteable`, a `slots` array
(`index`, `class`, `key`, `keyHash`, `specificPrefabHashes`, `interactable`,
`swappable`, `locked`, `read`, `write`), and a `memory` object:

```json
"memory": {"readable": true, "writeable": false, "stackSize": 24,
           "instructionType": "SuitStorageInstruction",
           "instructions": [{"name": "SlotHelmet", "value": 1,
                             "format": "0-7 | OP_CODE | BYTE_8 ..."}]}
```

`memory` is `null` for devices with no logic memory. `readable` / `writeable`
mean the IC10 `get` / `put` instructions accept the prefab (`IMemoryReadable` /
`IMemoryWritable`); `stackSize` is `IMemory.GetStackSize()` (the number of
addresses). For devices with a packed instruction protocol — suit storage,
printers, sorters, silos, occupancy sensors, satellite dishes, rocket avionics
and celestial trackers — `instructionType` and `instructions` carry the enum
names, opcodes and the Stationpedia bit layout (color markup stripped).
`slotProperties` stays as slot 0's readable set for format-1 consumers.

The suit storage is the read-only example: `get` returns 24 entries, each packed
as `[opcode 0-7][slot index 8-15][prefab hash 16-47]`; opcodes 1/2/3 are
helmet/suit/backpack, and 4..9 cover the suit's air/waste/coolant tanks, the
jetpack propellant, battery cells and gas filters. The hash field is unsigned in
the Stationpedia text; IC10 `HASH()` values are signed, so subtract 2^32 before
comparing a hash above 2147483647.

`CanLogicWrite(logicSlotType, slot)` cannot be probed on a prefab: the base
`Device` implementation refuses while `IsStructureCompleted` is false, and a
prefab template never is. The exporter therefore falls back to the same
class-based rules the game applies once the device is built (`On` for
Helmet/Tool/Appliance, `Lock` for Helmet, `Open` for Helmet/GasCanister/
LiquidCanister/LiquidBottle) and to `Console`'s extra `Mode`. If the game changes
those rules, `Exporter.cs` (`SafeSlotWritable`) has to change with it.

Two details that cost a run to learn:

- **The scan waits for the prefab count to stop changing**, and rescans when it
  grows. Some mods create prefabs at runtime instead of shipping them in their
  bundle — Mirrored Devices adds an `*Mirrored` copy of many structures from its
  DLL — so a single early scan would miss them.
- **`displayName` follows the player's game language.** `tools/import-devices`
  therefore keeps the existing (English) prefab titles, uses the prefab name for
  new entries, and re-titles devices from the game's English localization; the
  checked-in tables are English whatever language the game runs in.
- **The export mirrors the mod set, the tables do not.** Every enabled workshop
  mod contributes its prefabs and devices to the scan: the same machine wrote
  2016 prefabs in one run and 1759 in another after mods were disabled. The
  prefab count in the log is therefore how you tell which mod set an export saw.
  `tools/import-devices` then drops the names in `internal/builtin/modprefabs.go`,
  so the checked-in tables stay the game's.

```json
{
  "formatVersion": 1,
  "gameVersion": "0.2.6428.27798",
  "deviceCount": 2,
  "devices": [
    {
      "prefabName": "StructureVolumePump",
      "prefabHash": -321403609,
      "displayName": "Volume Pump",
      "slotCount": 0,
      "properties": [
        {"name": "On", "logicType": 28, "read": true, "write": true},
        {"name": "Pressure", "logicType": 5, "read": true, "write": false}
      ],
      "slotProperties": []
    }
  ]
}
```

## Turn it into ic10go tables

```bash
go run ./tools/import-devices            # finds the files under My Games
# or: go run ./tools/import-devices prefabs.json devices.json
```

This merges the prefab names into `internal/builtin/prefabs.go` (so the
non-localized names are kept) and writes `internal/builtin/devicecatalog_gen.go`,
which the editor uses to offer a prefab's own properties after `all(Prefab).`.

Prefabs and devices that an enabled mod adds are dropped: `internal/builtin/modprefabs.go`
lists the 395 prefabs and 115 devices that the 2026-10-18 export carried from six
workshop mods, and the importer filters them out of both the export and the table
it merges into. Re-derive the list (and its count test) when the mod set changes.

The same export can feed the editor without touching the tables: point
`icg.modPacks` (or `ic10c decompile --mods`) at this directory. The names the
game tables already have stay the game's; only the rest are attributed to the
pack, appear in completion and hover, and get no community-wiki link.
