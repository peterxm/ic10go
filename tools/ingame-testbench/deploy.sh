#!/usr/bin/env sh
# Build the ic10go testbench mod and install it into the Stationeers mods folder.
#
# It refuses to overwrite the DLL while the game is running: Mono reads managed
# assemblies lazily, so swapping the file under a live process makes later JITs
# fail with "BadImageFormatException: method has zero rva". Quit the game to the
# desktop first (a world reload is not enough — the process caches the DLL).
#
# Usage:
#   tools/ingame-testbench/deploy.sh            # build + install (must not be running)
#   tools/ingame-testbench/deploy.sh --wait     # wait for the game to exit, then install
#   tools/ingame-testbench/deploy.sh --force    # install even if the game is running
#
# Overridable env: STATIONEERS_DIR, MODS_DIR, DOTNET.
set -e

here=$(cd "$(dirname "$0")" && pwd)

force=0
wait_exit=0
for arg in "$@"; do
    case "$arg" in
        --force) force=1 ;;
        --wait) wait_exit=1 ;;
    esac
done

# --- locate the game + mods folder -------------------------------------------
stationeers=${STATIONEERS_DIR:-}
if [ -z "$stationeers" ]; then
    for d in "$HOME"/.local/share/Steam/steamapps/common/Stationeers \
             "$HOME"/.steam/steam/steamapps/common/Stationeers; do
        [ -d "$d" ] && stationeers=$d && break
    done
fi
[ -n "$stationeers" ] || { echo "deploy: set STATIONEERS_DIR to your Stationeers install" >&2; exit 1; }

mods=${MODS_DIR:-}
if [ -z "$mods" ]; then
    for d in "$HOME"/.local/share/Steam/steamapps/compatdata/*/pfx/drive_c/users/*/Documents/My\ Games/Stationeers/mods/ic10go-testbench \
             "$HOME"/Documents/My\ Games/Stationeers/mods/ic10go-testbench \
             "$HOME"/.steam/steam/steamapps/compatdata/*/pfx/drive_c/users/*/Documents/My\ Games/Stationeers/mods/ic10go-testbench; do
        [ -d "$d" ] && mods=$d && break
    done
fi
[ -n "$mods" ] || { echo "deploy: set MODS_DIR to .../My Games/Stationeers/mods/ic10go-testbench" >&2; exit 1; }

# --- safety: never swap the DLL under a running game --------------------------
if pgrep -f 'rocketstation.exe' >/dev/null 2>&1; then
    if [ "$wait_exit" -eq 1 ]; then
        echo "deploy: Stationeers is running — waiting for it to exit..."
        while pgrep -f 'rocketstation.exe' >/dev/null 2>&1; do sleep 2; done
        sleep 3   # let the process release the DLL
        echo "deploy: game exited."
    elif [ "$force" -ne 1 ]; then
        echo "deploy: Stationeers is running — refusing to swap the mod DLL."
        echo "        Quit the game to the desktop, then run this again (--wait or --force)."
        exit 1
    fi
fi

# --- build --------------------------------------------------------------------
dotnet=${DOTNET:-dotnet}
command -v "$dotnet" >/dev/null 2>&1 || dotnet="$HOME/.dotnet/dotnet"
command -v "$dotnet" >/dev/null 2>&1 || { echo "deploy: dotnet not found (set DOTNET)" >&2; exit 1; }

"$dotnet" build -c Release -p:StationeersDir="$stationeers" "$here/Ic10GoTestbench.csproj"

dll="$here/bin/Release/ic10go-testbench.dll"
[ -f "$dll" ] || { echo "deploy: build produced no $dll" >&2; exit 1; }

# --- install (with a backup) --------------------------------------------------
if [ -f "$mods/ic10go-testbench.dll" ]; then
    cp "$mods/ic10go-testbench.dll" "$mods/ic10go-testbench.dll.bak-$(date +%Y%m%d-%H%M%S)"
fi
cp "$dll" "$mods/ic10go-testbench.dll"
[ -f "$here/About/About.xml" ] && cp "$here/About/About.xml" "$mods/About/About.xml"

ver=$(sed -n 's/.*<Version>\([^<]*\)<.*/\1/p' "$mods/About/About.xml" 2>/dev/null | head -1)
echo "deploy: installed mod ${ver:-?} to $mods"
echo "deploy: start Stationeers to load it."
