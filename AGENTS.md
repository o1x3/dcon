# AGENTS.md

## Cursor Cloud specific instructions

### What runs here (Linux) vs. what does not

- The **`dcon` Go CLI** (repo root) is the source of truth and fully builds/tests
  on this Linux VM. This is the in-scope deliverable for cloud agents.
- The **Swift desktop app** in `app/` is macOS-only (needs full Xcode + SwiftUI
  macros). `swift` is not installed here, so `make app-*` targets cannot run in
  Cursor Cloud. Skip them unless working on a macOS runner.
- The **Apple `container` runtime backend is macOS-only** (Virtualization.framework).
  It is *not* present here, so commands that actually boot/inspect containers
  (`dcon run`, `ps`, `images`, `system start`, warm pool boot, etc.) cannot reach
  a real backend. This is expected — CI has no backend either and the test suite
  is pure unit tests (see `CLAUDE.md`).

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
