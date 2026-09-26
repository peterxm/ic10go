#!/usr/bin/env bash
# Build script for ic10go: the ic10c compiler and the VSCode extension.
#
# Usage:
#   ./build.sh [command]
#
# Commands:
#   build      (default) build ic10c for the host into ./ic10c
#   test       run the Go test suite
#   release    cross-compile release binaries into dist/
#   install    build ic10c and install it to ~/.local/bin
#   vsix       package the VSCode extension into dist/ (needs npx)
#   clean      remove ./ic10c and dist/
#   all        test + build + release
#   help       show this help

set -euo pipefail

cd "$(dirname "$0")"

BIN=ic10c
PKG=./cmd/ic10c
DIST=dist
VERSION_PKG=ic10go/internal/version

# release targets as <GOOS>/<GOARCH>
TARGETS="linux/amd64 linux/arm64 windows/amd64 darwin/amd64 darwin/arm64"

version() {
    sed -n 's/.*Version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' internal/version/version.go | head -1
}

# meta_flags injects version/commit/build metadata via -ldflags -X.
meta_flags() {
    printf -- '-X %s.Version=%s -X %s.Commit=%s -X %s.BuildTime=%s -X %s.BuildUser=%s' \
        "$VERSION_PKG" "$(version)" \
        "$VERSION_PKG" "$(git rev-parse --short HEAD 2>/dev/null || true)" \
        "$VERSION_PKG" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        "$VERSION_PKG" "$(id -un 2>/dev/null || true)"
}

info() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }

build() {
    info "building $BIN for the host ($(go env GOOS)/$(go env GOARCH))"
    go build -trimpath -ldflags "$(meta_flags)" -o "$BIN" "$PKG"
    info "wrote ./$BIN ($(version))"
}

test() {
    info "running go test ./..."
    go test ./...
}

release() {
    v=$(version)
    mkdir -p "$DIST"
    for t in $TARGETS; do
        os=${t%/*}
        arch=${t#*/}
        out="$DIST/$BIN-$v-$os-$arch"
        [ "$os" = windows ] && out="$out.exe"
        info "building $os/$arch -> $out"
        CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
            go build -trimpath -ldflags "-s -w $(meta_flags)" -o "$out" "$PKG"
    done
    info "release binaries in $DIST/"
}

install() {
    build
    dest="${HOME}/.local/bin"
    mkdir -p "$dest"
    if [ -e "$dest/$BIN" ] && [ "$(readlink -f "$dest/$BIN")" = "$(readlink -f "$BIN")" ]; then
        info "$dest/$BIN already points to this build"
    else
        cp -f "$BIN" "$dest/"
        info "installed $dest/$BIN ($(version))"
    fi
    command -v "$BIN" >/dev/null 2>&1 || \
        info "note: $dest is not on PATH"
}

vsix() {
    mkdir -p "$DIST"
    info "packaging VSCode extension"
    ( cd editors/vscode && npx --yes @vscode/vsce package --allow-missing-repository --no-rewrite-relative-links )
    mv editors/vscode/*.vsix "$DIST/" 2>/dev/null || true
    info "vsix in $DIST/"
}

clean() {
    info "removing ./$BIN and $DIST/"
    rm -f "$BIN"
    rm -rf "$DIST"
}

usage() {
    sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'
}

case "${1:-build}" in
    build)   build ;;
    test)    test ;;
    release) release ;;
    install) install ;;
    vsix)    vsix ;;
    clean)   clean ;;
    all)     test; build; release ;;
    help|-h|--help) usage ;;
    *) echo "unknown command: $1" >&2; usage; exit 2 ;;
esac
