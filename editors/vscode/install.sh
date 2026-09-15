#!/usr/bin/env sh
# Install the IC10 Go (.icg) VSCode extension for the current user.
#
# Usage:
#   sh editors/vscode/install.sh
#
# It prefers the `code` CLI (which keeps VSCode's extension registry in sync).
# If `code` is unavailable it falls back to copying into
# ~/.vscode/extensions — that copy is *not* registered, so remove any older
# version via the Extensions view first (or use the .vsix path).
#
# After it finishes, reload VSCode: Command Palette -> "Developer: Reload Window".
set -e

here=$(cd "$(dirname "$0")" && pwd)

# Read the version from package.json so the install path stays in sync.
version=$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$here/package.json" | head -1)

# --- Preferred path: package a .vsix and install it with the code CLI --------
# This updates ~/.vscode/extensions/extensions.json, so no stale version is left
# pointing at a deleted directory.
if command -v code >/dev/null 2>&1; then
    vsix=""
    # Reuse a pre-built vsix when present (editors/vscode/ or repo dist/).
    for cand in "$here/icg-$version.vsix" "$here/../../dist/icg-$version.vsix"; do
        if [ -f "$cand" ]; then
            vsix=$cand
            break
        fi
    done
    if [ -z "$vsix" ] && command -v npx >/dev/null 2>&1; then
        ( cd "$here" && npx --yes @vscode/vsce package --allow-missing-repository >/dev/null 2>&1 ) || true
        [ -f "$here/icg-$version.vsix" ] && vsix="$here/icg-$version.vsix"
    fi
    if [ -n "$vsix" ] && [ -f "$vsix" ]; then
        if code --install-extension "$vsix" --force >/dev/null 2>&1; then
            # Drop a freshly packaged vsix; keep a pre-built one from dist/.
            case "$vsix" in "$here"/*) rm -f "$vsix" ;; esac
            echo "Installed IC10 Go extension $version via the code CLI."
            echo
            echo "Reload VSCode: Command Palette -> 'Developer: Reload Window'."
            exit 0
        fi
    fi
fi

# --- Fallback: copy into the extensions directory ---------------------------
# WARNING: this does not update VSCode's registry. If an older version is
# registered, uninstall it first: code --uninstall-extension ic10go.icg
dest="$HOME/.vscode/extensions/ic10go.icg-$version"

rm -rf "$dest"
mkdir -p "$dest"
cp "$here/package.json" "$dest/"
cp "$here"/package.nls*.json "$dest/"
cp "$here/extension.js" "$dest/"
cp "$here/language-configuration.json" "$dest/"
cp -R "$here/syntaxes" "$dest/"
cp -R "$here/snippets" "$dest/"

echo "Installed IC10 Go extension $version to:"
echo "  $dest"
echo
echo "Note: this copy does not register the extension with VSCode. If you had an"
echo "older version installed, remove it first (Extensions view, or"
echo "  code --uninstall-extension ic10go.icg) to avoid a stale reference."
echo
echo "Next steps:"
echo "  1. Build the compiler:  go build -o ic10c ./cmd/ic10c"
echo "  2. Reload VSCode:       Command Palette -> 'Developer: Reload Window'"
echo "  3. Open a .icg file."
