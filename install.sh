#!/usr/bin/env bash
#
# dcon installer — a drop-in Docker CLI backed by Apple's container runtime.
#
#   curl -fsSL https://raw.githubusercontent.com/o1x3/dcon/main/install.sh | bash
#
# Options (env vars):
#   DCON_VERSION=v1.2.3   install a specific tag (default: latest release)
#   DCON_PREFIX=/usr/local install prefix (default: /usr/local, falls back to ~/.local)
#   DCON_LINK_DOCKER=1     also symlink `docker` -> `dcon`
#   DCON_FROM_SOURCE=1     build from source with Go instead of downloading a binary
#
set -euo pipefail

REPO="o1x3/dcon"
BINARY="dcon"
PREFIX="${DCON_PREFIX:-/usr/local}"
BIN_DIR="${PREFIX}/bin"

bold()  { printf '\033[1m%s\033[0m\n' "$*"; }
info()  { printf '\033[36m==>\033[0m %s\n' "$*"; }
warn()  { printf '\033[33mwarning:\033[0m %s\n' "$*" >&2; }
die()   { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }

# --- platform detection ---
OS="$(uname -s)"
ARCH="$(uname -m)"
[ "$OS" = "Darwin" ] || die "dcon only runs on macOS (got $OS). The Apple container backend requires macOS on Apple silicon."
case "$ARCH" in
  arm64|aarch64) GOARCH="arm64" ;;
  x86_64|amd64)  GOARCH="amd64"; warn "dcon targets Apple silicon; amd64 builds run under Rosetta where available." ;;
  *) die "unsupported architecture: $ARCH" ;;
esac

# --- pick a writable bin dir ---
if [ ! -d "$BIN_DIR" ]; then mkdir -p "$BIN_DIR" 2>/dev/null || true; fi
if [ ! -w "$BIN_DIR" ]; then
  if [ "$PREFIX" = "/usr/local" ]; then
    BIN_DIR="$HOME/.local/bin"; mkdir -p "$BIN_DIR"
    warn "/usr/local/bin not writable; installing to $BIN_DIR (ensure it is on your PATH)"
  else
    die "$BIN_DIR is not writable"
  fi
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

install_from_source() {
  command -v go >/dev/null 2>&1 || die "Go toolchain not found; install Go or unset DCON_FROM_SOURCE"
  info "Building $BINARY from source (go install)…"
  GOBIN="$TMP" go install "github.com/${REPO}@${DCON_VERSION:-latest}" 2>/dev/null \
    || die "go install failed (the module path may differ); try a release binary instead"
  install -m 0755 "$TMP/$BINARY" "$BIN_DIR/$BINARY"
}

install_from_release() {
  local tag asset url
  if [ -n "${DCON_VERSION:-}" ]; then
    tag="$DCON_VERSION"
  else
    info "Resolving latest release…"
    tag="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
      | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
    [ -n "$tag" ] || return 1
  fi
  asset="${BINARY}_${tag#v}_darwin_${GOARCH}.tar.gz"
  url="https://github.com/${REPO}/releases/download/${tag}/${asset}"
  info "Downloading ${asset} (${tag})…"
  curl -fsSL "$url" -o "$TMP/$asset" || return 1

  # checksum verification (best-effort: checksums.txt in the release)
  if curl -fsSL "https://github.com/${REPO}/releases/download/${tag}/checksums.txt" -o "$TMP/checksums.txt" 2>/dev/null; then
    ( cd "$TMP" && grep " ${asset}\$" checksums.txt | shasum -a 256 -c - >/dev/null 2>&1 ) \
      && info "checksum verified" || warn "checksum verification skipped/failed"
  fi

  tar -xzf "$TMP/$asset" -C "$TMP"
  install -m 0755 "$TMP/$BINARY" "$BIN_DIR/$BINARY"
}

bold "Installing dcon → ${BIN_DIR}/${BINARY}"
if [ "${DCON_FROM_SOURCE:-0}" = "1" ]; then
  install_from_source
elif ! install_from_release; then
  warn "no matching release binary found; falling back to building from source"
  install_from_source
fi

# --- optional docker drop-in symlink ---
if [ "${DCON_LINK_DOCKER:-0}" = "1" ]; then
  ln -sf "$BIN_DIR/$BINARY" "$BIN_DIR/docker"
  info "linked ${BIN_DIR}/docker -> ${BINARY}"
fi

# --- post-install guidance ---
echo
bold "dcon installed: $("$BIN_DIR/$BINARY" --version 2>/dev/null || echo "$BIN_DIR/$BINARY")"
echo
if ! command -v container >/dev/null 2>&1; then
  warn "Apple 'container' runtime not found. Install it from https://github.com/apple/container/releases"
else
  info "Backend detected: $(container --version 2>/dev/null | head -1)"
fi
cat <<EOF

Next steps:
  1) Start the backend (one time):   dcon system start
  2) Install a guest kernel:         dcon system kernel set --recommended
  3) Run your first container:       dcon run --rm alpine echo "hello from dcon"

Make it a true drop-in:
  alias docker=dcon          # add to your ~/.zshrc
  # or re-run with: DCON_LINK_DOCKER=1
EOF
