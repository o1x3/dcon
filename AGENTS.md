# AGENTS.md

## Cursor Cloud specific instructions

### What runs here (Linux) vs. what does not

- The **`dcon` Go CLI** (repo root) is the source of truth and fully builds/tests
  on this Linux VM. This is the in-scope deliverable for cloud agents.
- The **Swift desktop app** in `app/` is macOS-only (see "Mac app" below).
  `swift` is not installed here, so `make app-*` targets cannot run in Cursor
  Cloud. Building/packaging the app is handled on macOS runners by CI
  (`.github/workflows/app-ci.yml`, `app-release.yml`).
- The **Apple `container` runtime backend is macOS-only** (see "Container
  backend" below). It is *not* present here, so commands that actually
  boot/inspect containers cannot reach a real backend. This is expected — CI has
  no backend either and the test suite is pure unit tests (see `CLAUDE.md`).

### Container backend (Apple `container`)

- dcon has **no daemon of its own**: every command translates Docker-style args
  into `container` args, shells out to the `container` binary, and re-renders the
  output Docker-style. All backend access funnels through `internal/runtime`
  (`Run`/`Capture*`); never call `os/exec` on `container` directly from `cmd/`.
- The backend requires macOS + Apple silicon + Virtualization.framework, so it
  cannot run on this Linux VM. On a real Mac it is set up once with
  `dcon system start` and `dcon system kernel set --recommended` (see `README.md`
  "Setup"). Read-only commands work without a guest kernel; booting containers
  needs it.
- Backend-touching code paths (`internal/pool` boot/destroy, real `runtime`
  calls, warm-pool exec) are validated **manually on macOS**, not in CI. Unit
  tests that touch warm-pool/machine state redirect it via `HOME`/
  `XDG_CONFIG_HOME` temp dirs, so they are safe here.
- Useful env knobs: `DCON_CONTAINER_BIN` points dcon at a specific `container`
  binary (or a stub — see below); `DCON_DEBUG=1` echoes every underlying
  `container <args>` line to stderr.

### Mac app (`app/`, Swift / SwiftUI)

- **Dcon.app** is a SwiftUI menubar + window GUI (`swift-tools-version:5.10`,
  targets macOS 14+). It is a thin front end that **shells out to the embedded
  `dcon` CLI for everything** (`app/Sources/Dcon/Core/CLI.swift`): lists via
  `--format json`, streams via `logs -f`/`events`, interactive shells via
  Terminal — so GUI behavior is 1:1 with the CLI by construction.
- Build/test/run (macOS + full Xcode only — SwiftUI macros are not in the
  Command Line Tools; see `README.md`): `make app-build` (`swift build`),
  `make app-test` (`swift test`), `make app-bundle` (assemble
  `app/dist/Dcon.app`, embedding a freshly `make build`-ed `dcon`),
  `make app-run`, `make app-dmg`. Point Xcode at the full app first, e.g.
  `export DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer`.
- The bundled app finds its CLI at `Contents/Resources/dcon`; for local dev the
  bridge honors a `DCON_BIN` override to point at a specific `dcon` binary.
- App releases are driven by `app/VERSION` (CLI by root `VERSION`); bumping
  those files on `main` publishes a GitHub Release and creates an `app-v*` /
  `v*` tag. The app version is also read by `app/scripts/package-app.sh`.

### Build / lint / test (standard commands, see `Makefile`)

- `make build` → builds `./dcon` (also `go build`). `make vet` and `gofmt -l .`
  are what CI enforces (`.github/workflows/ci.yml`); keep both clean.
- `make test` / `make test-race` → unit tests. CI runs
  `go test -race -coverprofile=coverage.out -covermode=atomic ./...`.
- Go toolchain: `go.mod` pins `go 1.26.x`; the system `go` auto-fetches the
  matching toolchain on first use.

### Exercising the translation layer without a backend

To run `dcon` end-to-end without the macOS `container` binary, point it at a stub
via `DCON_CONTAINER_BIN` and it will print the translated `container` args:

```sh
printf '#!/usr/bin/env bash\necho "container $*"\n' > /tmp/fake-container
chmod +x /tmp/fake-container
DCON_CONTAINER_BIN=/tmp/fake-container DCON_DEBUG=1 ./dcon run --rm alpine echo hi
```

`DCON_DEBUG=1` also echoes every underlying `container <args>` line to stderr.
