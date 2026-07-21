#!/usr/bin/env bash
#
# dcon installer — a drop-in Docker CLI backed by Apple's container runtime.
#
#   curl -fsSL https://raw.githubusercontent.com/o1x3/dcon/main/install.sh | bash
#
# It installs the prerequisites too: if Apple's `container` runtime is missing,
# the latest signed release is downloaded and installed (this needs admin
# rights, so you'll be prompted for your password), then dcon itself is placed
# on your PATH, Dcon.app is installed to /Applications (quarantine cleared so
# Gatekeeper won't block it), and the backend is brought up ready to run containers.
#
# Options (env vars):
#   DCON_VERSION=v1.2.3       install a specific dcon tag (default: latest release)
#   DCON_PREFIX=/usr/local    install prefix (default: /usr/local)
#   DCON_LINK_DOCKER=1        also symlink `docker` -> `dcon`
#   DCON_FROM_SOURCE=1        build dcon from source with Go instead of a binary
#   DCON_SKIP_PREREQS=1       do not install Apple `container` even if missing
#   DCON_CONTAINER_VERSION=X  install a specific Apple container version
#   DCON_SKIP_SETUP=1         do not start the backend / install a guest kernel
#   DCON_SKIP_APP=1           do not install Dcon.app
#   DCON_APP_VERSION=app-vX   install a specific app tag (default: latest app-v* release)
#   DCON_APP_DIR=/Applications  install Dcon.app here (default: /Applications)
#   DCON_YES=1                assume "yes" to prompts (non-interactive)
#   NO_COLOR=1                disable colored output
#
set -euo pipefail

REPO="o1x3/dcon"
CONTAINER_REPO="apple/container"
BINARY="dcon"
APP_NAME="Dcon.app"
PREFIX="${DCON_PREFIX:-/usr/local}"
BIN_DIR="${PREFIX}/bin"
APP_DIR="${DCON_APP_DIR:-/Applications}"

# --- styling ----------------------------------------------------------------
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  B=$'\033[1m'; D=$'\033[2m'; R=$'\033[31m'; G=$'\033[32m'; Y=$'\033[33m'; C=$'\033[36m'; M=$'\033[35m'; X=$'\033[0m'
else
  B=''; D=''; R=''; G=''; Y=''; C=''; M=''; X=''
fi
step() { printf '\n%s%s%s\n' "$B$C" "$1" "$X"; }
ok()   { printf '  %s✓%s %s\n' "$G" "$X" "$1"; }
info() { printf '  %s›%s %s\n' "$C" "$X" "$1"; }
warn() { printf '  %s!%s %s\n' "$Y" "$X" "$1" >&2; }
die()  { printf '\n%serror%s %s\n' "$R$B" "$X" "$1" >&2; exit 1; }

header() {
  printf '\n'
  printf '  %sdcon%s  %sa drop-in docker CLI for macOS, on Apple%s %scontainer%s\n' "$B$M" "$X" "$D" "$X" "$C" "$X"
  printf '  %s────────────────────────────────────────────────────%s\n' "$D" "$X"
}

confirm() { # confirm "question" -> 0 yes / 1 no ; auto-yes with DCON_YES
  [ "${DCON_YES:-0}" = "1" ] && return 0
  local a; printf '  %s?%s %s [Y/n] ' "$Y" "$X" "$1" >&2
  if [ -r /dev/tty ]; then read -r a </dev/tty || a=""; else a=""; fi
  case "$a" in n|N|no|NO) return 1 ;; *) return 0 ;; esac
}

# --- sudo handling ----------------------------------------------------------
# Privileged steps (installing the .pkg, writing to /usr/local/bin) need root.
# When run as a normal user we request sudo up front — its prompt goes to the
# controlling terminal, so this works even under `curl … | bash`.
SUDO=""
ensure_root() {
  [ "$(id -u)" = "0" ] && { SUDO=""; return 0; }
  command -v sudo >/dev/null 2>&1 || die "need root for this step but 'sudo' is not available; re-run as root."
  if [ -z "$SUDO" ]; then
    info "administrator access is required (you'll be prompted once)…"
    if ! sudo -v; then die "could not obtain administrator access (sudo)."; fi
    # keep the sudo timestamp warm during long downloads
    ( while kill -0 "$$" 2>/dev/null; do sudo -n true 2>/dev/null; sleep 50; done ) >/dev/null 2>&1 &
    SUDO="sudo"
  fi
}
as_root() { if [ -n "$SUDO" ] || [ "$(id -u)" = "0" ]; then ${SUDO:+$SUDO} "$@"; else ensure_root; ${SUDO:+$SUDO} "$@"; fi; }

# --- platform detection -----------------------------------------------------
header
step "Checking your system"
OS="$(uname -s)"; ARCH="$(uname -m)"
[ "$OS" = "Darwin" ] || die "dcon only runs on macOS (got $OS). Apple container requires macOS on Apple silicon."
case "$ARCH" in
  arm64|aarch64) GOARCH="arm64"; ok "macOS on Apple silicon ($ARCH)" ;;
  x86_64|amd64)  GOARCH="amd64"; warn "Intel Mac detected — Apple container needs Apple silicon; dcon will install but cannot boot containers here." ;;
  *) die "unsupported architecture: $ARCH" ;;
esac
OS_MAJOR="$(sw_vers -productVersion 2>/dev/null | cut -d. -f1 || echo 0)"
if [ "${OS_MAJOR:-0}" -lt 26 ] 2>/dev/null; then
  warn "macOS $(sw_vers -productVersion 2>/dev/null) detected — Apple container works best on macOS 26+. Older versions have limited support."
else
  ok "macOS $(sw_vers -productVersion 2>/dev/null)"
fi

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

# --- prerequisite: Apple `container` ----------------------------------------
install_container() {
  step "Installing the Apple container runtime"
  local tag url pkg
  if [ -n "${DCON_CONTAINER_VERSION:-}" ]; then tag="$DCON_CONTAINER_VERSION"; else
    info "resolving the latest container release…"
    tag="$(curl -fsSL "https://api.github.com/repos/${CONTAINER_REPO}/releases/latest" \
      | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
    [ -n "$tag" ] || die "could not resolve the latest Apple container release."
  fi
  url="https://github.com/${CONTAINER_REPO}/releases/download/${tag}/container-${tag}-installer-signed.pkg"
  pkg="$TMP/container-${tag}.pkg"
  info "downloading container ${tag} (~85 MB)…"
  curl -fL --progress-bar "$url" -o "$pkg" || die "download failed: $url"
  info "installing the signed package (Apple-notarized)…"
  as_root installer -pkg "$pkg" -target / >/dev/null || die "package install failed."
  command -v container >/dev/null 2>&1 || export PATH="/usr/local/bin:$PATH"
  ok "Apple container installed: $(container --version 2>/dev/null | head -1)"
}

step "Checking the Apple container runtime"
if command -v container >/dev/null 2>&1; then
  ok "already installed: $(container --version 2>/dev/null | head -1)"
elif [ "${DCON_SKIP_PREREQS:-0}" = "1" ]; then
  warn "Apple container not found and DCON_SKIP_PREREQS=1 — dcon won't be able to boot containers until you install it."
elif [ "$GOARCH" != "arm64" ]; then
  warn "skipping container install on a non-Apple-silicon host."
elif confirm "Apple container isn't installed. Install the latest release now?"; then
  install_container
else
  warn "skipped — install it later from https://github.com/${CONTAINER_REPO}/releases"
fi

# --- quarantine / gatekeeper ------------------------------------------------
clear_xattrs() { # clear_xattrs <path> : strip quarantine/provenance xattrs
  # Release artifacts are unsigned/ad-hoc signed, so macOS may attach quarantine
  # xattrs on download — Gatekeeper then blocks the first run with a "cannot
  # verify the developer" prompt. Best effort, elevating if root-owned.
  local target="$1" flags="-c"
  [ -d "$target" ] && flags="-cr"
  xattr $flags "$target" 2>/dev/null || { [ -n "$SUDO" ] && $SUDO xattr $flags "$target" 2>/dev/null; } || true
}

# --- install dcon -----------------------------------------------------------
write_bin() { # write_bin <src> : install <src> to BIN_DIR, elevating if needed
  if [ ! -d "$BIN_DIR" ]; then as_root mkdir -p "$BIN_DIR"; fi
  if [ -w "$BIN_DIR" ]; then install -m 0755 "$1" "$BIN_DIR/$BINARY"; else as_root install -m 0755 "$1" "$BIN_DIR/$BINARY"; fi
  clear_xattrs "$BIN_DIR/$BINARY"
}

install_from_source() {
  command -v go >/dev/null 2>&1 || die "Go toolchain not found; install Go or unset DCON_FROM_SOURCE."
  info "building $BINARY from source (go install)…"
  GOBIN="$TMP" go install "github.com/${REPO}@${DCON_VERSION:-latest}" 2>/dev/null \
    || die "go install failed (module path may differ); try a release binary instead."
  write_bin "$TMP/$BINARY"
}

install_from_release() {
  local tag asset url
  if [ -n "${DCON_VERSION:-}" ]; then tag="$DCON_VERSION"; else
    info "resolving the latest dcon release…"
    tag="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
      | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
    [ -n "$tag" ] || return 1
  fi
  asset="${BINARY}_${tag#v}_darwin_${GOARCH}.tar.gz"
  url="https://github.com/${REPO}/releases/download/${tag}/${asset}"
  info "downloading ${asset} (${tag})…"
  curl -fsSL "$url" -o "$TMP/$asset" || return 1
  if curl -fsSL "https://github.com/${REPO}/releases/download/${tag}/checksums.txt" -o "$TMP/checksums.txt" 2>/dev/null; then
    ( cd "$TMP" && grep " ${asset}\$" checksums.txt | shasum -a 256 -c - >/dev/null 2>&1 ) \
      && ok "checksum verified" || warn "checksum verification skipped"
  fi
  tar -xzf "$TMP/$asset" -C "$TMP"
  write_bin "$TMP/$BINARY"
}

step "Installing dcon → ${BIN_DIR}/${BINARY}"
if [ "${DCON_FROM_SOURCE:-0}" = "1" ]; then
  install_from_source
elif ! install_from_release; then
  warn "no matching release binary; building from source instead"
  install_from_source
fi
ok "dcon installed: $("$BIN_DIR/$BINARY" --version 2>/dev/null | head -1 || echo "$BIN_DIR/$BINARY")"

if [ "${DCON_LINK_DOCKER:-0}" = "1" ]; then
  if [ -w "$BIN_DIR" ]; then ln -sf "$BIN_DIR/$BINARY" "$BIN_DIR/docker"; else as_root ln -sf "$BIN_DIR/$BINARY" "$BIN_DIR/docker"; fi
  ok "linked ${BIN_DIR}/docker -> ${BINARY}"
fi

# --- install Dcon.app --------------------------------------------------------
install_app() {
  step "Installing ${APP_NAME}"
  local tag version asset url mount dest
  if [ -n "${DCON_APP_VERSION:-}" ]; then tag="$DCON_APP_VERSION"; else
    info "resolving the latest app release…"
    tag="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases" \
      | grep '"tag_name": "app-v' | head -1 | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
    [ -n "$tag" ] || { warn "no app release found — skipping ${APP_NAME} install"; return 0; }
  fi
  version="${tag#app-v}"
  asset="Dcon_${version}.dmg"
  url="https://github.com/${REPO}/releases/download/${tag}/${asset}"
  dest="$APP_DIR/$APP_NAME"
  mount="$TMP/mnt"

  info "downloading ${asset} (${tag})…"
  curl -fL --progress-bar "$url" -o "$TMP/$asset" || die "app download failed: $url"
  if curl -fsSL "https://github.com/${REPO}/releases/download/${tag}/checksums.txt" -o "$TMP/app-checksums.txt" 2>/dev/null; then
    ( cd "$TMP" && grep " ${asset}\$" app-checksums.txt | shasum -a 256 -c - >/dev/null 2>&1 ) \
      && ok "checksum verified" || warn "checksum verification skipped"
  fi

  info "mounting disk image…"
  hdiutil attach -nobrowse -readonly -mountpoint "$mount" "$TMP/$asset" >/dev/null \
    || die "could not mount ${asset}"

  info "installing to ${dest}…"
  as_root mkdir -p "$APP_DIR"
  if [ -d "$dest" ]; then as_root rm -rf "$dest"; fi
  as_root cp -R "$mount/$APP_NAME" "$dest"
  hdiutil detach "$mount" >/dev/null 2>&1 || hdiutil detach -force "$mount" >/dev/null 2>&1 || true

  clear_xattrs "$dest"
  ok "${APP_NAME} installed: ${dest}"
}

if [ "${DCON_SKIP_APP:-0}" != "1" ]; then
  install_app
else
  info "skipping ${APP_NAME} install (DCON_SKIP_APP=1)"
fi

# --- bring the backend up ----------------------------------------------------
if [ "${DCON_SKIP_SETUP:-0}" != "1" ] && command -v container >/dev/null 2>&1 && [ "$GOARCH" = "arm64" ]; then
  step "Bringing the backend up"
  if "$BIN_DIR/$BINARY" system start >/dev/null 2>&1; then ok "backend started"; else info "run 'dcon system start' if the backend isn't up yet"; fi
  if confirm "Install the recommended guest kernel now? (needed to run containers)"; then
    info "fetching the recommended kernel…"
    if "$BIN_DIR/$BINARY" system kernel set --recommended >/dev/null 2>&1; then ok "guest kernel installed"; else warn "kernel install skipped — run 'dcon system kernel set --recommended' later"; fi
  fi
fi

# --- final report ------------------------------------------------------------
step "Done"
if command -v "$BIN_DIR/$BINARY" >/dev/null 2>&1; then "$BIN_DIR/$BINARY" doctor 2>/dev/null || true; fi
case ":$PATH:" in *":$BIN_DIR:"*) : ;; *) warn "add ${BIN_DIR} to your PATH: export PATH=\"${BIN_DIR}:\$PATH\"" ;; esac
cat <<EOF

${B}Run your first container:${X}
  ${C}dcon run --rm alpine echo "hello from dcon"${X}

${B}Make it a true drop-in:${X}
  alias docker=dcon          ${D}# add to ~/.zshrc, or re-run with DCON_LINK_DOCKER=1${X}

${B}GUI:${X}
  open -a Dcon               ${D}# menubar app installed to ${APP_DIR}/${APP_NAME}${X}

Docs: https://github.com/${REPO}  ·  Wiki: https://github.com/${REPO}/wiki
EOF
