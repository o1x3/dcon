#!/usr/bin/env bash
# Assembles Dcon.app from the SwiftPM release build, embedding the dcon CLI
# built from this repo so the app is self-contained.
#
# Usage: scripts/package-app.sh [output-dir]
# Env:   APP_VERSION (default: contents of app/VERSION)
set -euo pipefail

APP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_DIR="$(cd "$APP_DIR/.." && pwd)"
OUT_DIR="${1:-$APP_DIR/dist}"

VERSION="${APP_VERSION:-}"
if [ -z "$VERSION" ] && [ -f "$APP_DIR/VERSION" ]; then
  VERSION="$(tr -d '[:space:]' < "$APP_DIR/VERSION")"
fi
VERSION="${VERSION:-0.0.0-dev}"
BUILD="$(git -C "$REPO_DIR" rev-parse --short HEAD 2>/dev/null || echo dev)"

echo "==> building Dcon.app v$VERSION ($BUILD)"

echo "==> swift build (release)"
swift build -c release --package-path "$APP_DIR"
BIN="$(swift build -c release --package-path "$APP_DIR" --show-bin-path)/Dcon"

echo "==> go build dcon CLI"
make -C "$REPO_DIR" build

APP="$OUT_DIR/Dcon.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

cp "$BIN" "$APP/Contents/MacOS/Dcon"
cp "$REPO_DIR/dcon" "$APP/Contents/Resources/dcon"
cp "$APP_DIR/Assets/AppIcon.icns" "$APP/Contents/Resources/AppIcon.icns"
sed -e "s/APP_VERSION/$VERSION/" -e "s/APP_BUILD/$BUILD/" \
  "$APP_DIR/Support/Info.plist" > "$APP/Contents/Info.plist"
printf 'APPL????' > "$APP/Contents/PkgInfo"

# Ad-hoc sign so the bundle launches cleanly on Apple Silicon.
codesign --force --deep --sign - "$APP"

echo "==> $APP"
