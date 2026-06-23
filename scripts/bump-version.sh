#!/usr/bin/env bash
#
# Bump the dcon version by creating and pushing a new semver tag.
# The CI release workflow triggers on the pushed tag and publishes binaries.
#
# Usage:
#   scripts/bump-version.sh patch        # v1.2.3 -> v1.2.4
#   scripts/bump-version.sh minor        # v1.2.3 -> v1.3.0
#   scripts/bump-version.sh major        # v1.2.3 -> v2.0.0
#   scripts/bump-version.sh v1.5.0       # explicit
#   DRY_RUN=1 scripts/bump-version.sh patch
#
set -euo pipefail
cd "$(dirname "$0")/.."

die() { printf 'error: %s\n' "$*" >&2; exit 1; }

[ -z "$(git status --porcelain)" ] || die "working tree is dirty; commit or stash first"

arg="${1:-patch}"
latest="$(git tag --list 'v*' --sort=-v:refname | head -1)"
latest="${latest:-v0.0.0}"
base="${latest#v}"
IFS='.' read -r MA MI PA <<<"$base"
MA="${MA:-0}"; MI="${MI:-0}"; PA="${PA:-0}"

case "$arg" in
  major) next="v$((MA+1)).0.0" ;;
  minor) next="v${MA}.$((MI+1)).0" ;;
  patch) next="v${MA}.${MI}.$((PA+1))" ;;
  v[0-9]*) next="$arg" ;;
  *) die "unknown bump '$arg' (use major|minor|patch|vX.Y.Z)" ;;
esac

echo "current: $latest"
echo "next:    $next"

if [ "${DRY_RUN:-0}" = "1" ]; then
  echo "(dry run) would: git tag -a $next && git push origin $next"
  exit 0
fi

git tag -a "$next" -m "release $next"
git push origin "$next"
echo "tagged and pushed $next — the release workflow will build and publish binaries."
