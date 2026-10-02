# ic10go in-game testbench

A Stationeers mod that turns a fixed test-bench save into a scriptable target for
the ic10go toolchain. It listens on `127.0.0.1:7800` and speaks newline-delimited
JSON (NDJSON) over TCP; `ic10c testbench` and the VSCode extension drive it.

It lets you:

- **upload** a compiled program to a chip (running any one-time loader first),
- **download** a chip's source (`program`, so it can be decompiled back to `.icg`),
- **set / read** device inputs and outputs by port (`d0`…`d5`),
- **step** the chip deterministically while the world is paused,
- **read back** registers, stack, program counter and device values,
- **locate a chip** — `chip.list` reports each host's world position (`pos`), a
  restart-stable program fingerprint (`fp`) and its `powered` state; `ic10c
  testbench locate` finds by source (`--program`) or by coordinates (`--at X Y Z
  [--range N]`), and `hud --chip N` / the VSCode **Track in Game** action draws
  an on-screen compass arrow (your X/Y/Z + heading, distance/direction to the
  target; `F8` toggles the overlay, `F9` clears the target).
- **track a player** — `players` lists everyone (`Brain.PlayerBrains`, so **offline**
  players show too, with an online/offline flag and distance); `hud --player NAME`
  points the compass at any player who still has something in the world, reading its
  position live — an online player's body, an **offline** player's body, or a dead
  player's **body bag**.

## Disclaimer

**Single-player / local use only.** This mod can read and write device logic,
pause the world and load saves, and is meant for debugging IC10 programs in a
local, single-player test save. Using it on a **multiplayer server may be
considered cheating**, degrades other players' experience, and may violate the
game's terms of service — do not enable or use it there. It does not modify the
game or send anything beyond `127.0.0.1`. Unofficial; not affiliated with
RocketWerkz / Stationeers. Use at your own risk.

It uses **one Harmony patch** (`WriteTrace`): a prefix on `ILogicable.SetLogicValue`
that records device writes so a script's write-only logic can be compared. Everything
else goes through the game's public API, with reflection only for the chip's private
register/stack arrays. It does nothing until a client connects.

## Test-bench save

v1 targets a hand-built save with a standard IC housing:

| port | device | role |
|------|--------|------|
| `d0` | LED Display | output |
| `d1` | Logic Dial | input |
| `d2` | spare (e.g. Logic Sorter) | optional |

Load that save, then run commands from the host. The mod finds the chip through
`CircuitHolders.AllCircuitHolders`.

## Build

Needs the .NET SDK and a Stationeers install:

```bash
cd tools/ingame-testbench
dotnet build -c Release -p:StationeersDir="/path/to/Stationeers"
# -> bin/Release/ic10go-testbench.dll
```

On Linux (including a Proton install) the path uses forward slashes; the project
pulls in `Microsoft.NETFramework.ReferenceAssemblies` so `net472` builds without a
Windows SDK.

## Install

The game loads mods through BepInEx + [StationeersLaunchPad](https://github.com/StationeersLaunchPad/StationeersLaunchPad).
Drop the mod folder in:

```
Documents\My Games\Stationeers\mods\ic10go-testbench\
├── About\About.xml
└── ic10go-testbench.dll
```

Under Proton that is `.../compatdata/544550/pfx/drive_c/users/steamuser/Documents/My Games/Stationeers/mods/`.
Enable it in the mods menu. `[ic10go-testbench]` in `Player.log` confirms it
loaded and reports the listening address.

On Linux, `tools/ingame-testbench/deploy.sh` builds **and** copies it in one step
(`--force` to override). It **refuses to overwrite a running game**: Mono JITs
methods lazily, so swapping the DLL under a live process makes later calls fail
with `BadImageFormatException: method has zero rva`. Quit to the desktop first —
a world reload is not enough.

## Configure

Create `Documents/My Games/Stationeers/ic10go/testbench.json` to change the bind
address and optionally auto-load a save at startup:

```json
{ "host": "127.0.0.1", "port": 7800, "autoload": "testbench", "autoloadDelay": 90 }
```

`autoload` names a folder under `saves/`; `autoloadDelay` is how many seconds
after launch to load it (the game crashes if a save is loaded during
`GameManager.Start`, so keep it comfortably past boot; 90 is safe). The mod then
loads the save the same way the main menu's "load latest" does
(`LoadHelper.LoadGame`), so you do not have to enter the test save by hand each
time. Leave `autoload` empty to disable.

Proton shares the host network stack, so the host's `ic10c` / VSCode reach
`127.0.0.1:7800` directly.

## Protocol

One JSON object per line. See [`docs/ingame-testbench.md`](../../docs/ingame-testbench.md)
for the full command set. A quick check with `nc`:

```bash
printf '{"id":1,"cmd":"ping","args":{}}\n' | nc 127.0.0.1 7800
```

## Re-checking the game API after an update

The game API the mod uses is listed at the top of `GameApi.cs`. After a
Stationeers update, confirm it still matches:

```bash
go run ./tools/dumpgameapi
```
