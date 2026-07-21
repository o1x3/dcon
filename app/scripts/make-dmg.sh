#!/usr/bin/env bash
# Packages dist/Dcon.app into a compressed DMG with an /Applications symlink.
#
# Usage: scripts/make-dmg.sh [version]
set -euo pipefail

APP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="$APP_DIR/dist"
APP="$DIST/Dcon.app"
if [ -n "${1:-}" ]; then
  VERSION="$1"
elif [ -n "${APP_VERSION:-}" ]; then
  VERSION="$APP_VERSION"
elif [ -f "$APP_DIR/VERSION" ]; then
  VERSION="$(tr -d '[:space:]' < "$APP_DIR/VERSION")"
else
  VERSION="0.0.0-dev"
fi

[ -d "$APP" ] || { echo "error: $APP not found — run package-app.sh first" >&2; exit 1; }

STAGE="$DIST/dmg-root"
rm -rf "$STAGE"
mkdir -p "$STAGE"
cp -R "$APP" "$STAGE/"
ln -s /Applications "$STAGE/Applications"

DMG="$DIST/Dcon_${VERSION}.dmg"
rm -f "$DMG"
hdiutil create -volname "Dcon" -srcfolder "$STAGE" -ov -format UDZO "$DMG"
rm -rf "$STAGE"

echo "==> $DMG"
