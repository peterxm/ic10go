# ic10go in-game exporter

A minimal Stationeers mod that dumps the two things only the running game knows:

- **`prefabs.json`** — every prefab's `name`, `hash` and `displayName`. This is
  the authoritative prefab list, including the procedural names (`ItemWreckage*`,
  kits) the localization does not carry.
- **`devices.json`** — for every logic-capable prefab: which `LogicType`s it
  accepts for **read** and **write**, its slot count, and the `LogicSlotType`s
  slot 0 answers. This is the data that cannot be read from `Assembly-CSharp.dll`
  (it lives in Unity's asset bundles).

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
dotnet build -c Release -p:StationeersDir="C:\Program Files (x86)\Steam\steamapps\common\Stationeers"
# -> bin/Release/ic10go-exporter.dll
```

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
either mod can be consumed:

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
