#!/usr/bin/env sh
# Install the IC10 Go (.icg) VSCode extension for the current user.
#
# Usage:
#   sh editors/vscode/install.sh
#
# After it finishes, reload VSCode: Command Palette -> "Developer: Reload Window".
set -e

here=$(cd "$(dirname "$0")" && pwd)

# Read the version from package.json so the install path stays in sync.
version=$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$here/package.json" | head -1)
dest="$HOME/.vscode/extensions/ic10go.icg-$version"

rm -rf "$dest"
mkdir -p "$dest"
cp "$here/package.json" "$dest/"
cp "$here/extension.js" "$dest/"
cp "$here/language-configuration.json" "$dest/"
cp -R "$here/syntaxes" "$dest/"
cp -R "$here/snippets" "$dest/"

echo "Installed IC10 Go extension $version to:"
echo "  $dest"
echo
echo "Next steps:"
echo "  1. Build the compiler:  go build -o ic10c ./cmd/ic10c"
echo "  2. Reload VSCode:       Command Palette -> 'Developer: Reload Window'"
echo "  3. Open a .icg file."
