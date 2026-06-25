# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

dcon is a **Docker-CLI-compatible front end whose backend is Apple's `container` runtime**. It is a thin translation layer: every `dcon` command parses Docker-style arguments, rewrites them into `container` arguments, shells out to the `container` binary, and re-renders the output in Docker's format. dcon has **no daemon of its own** — it is one static Go binary (cobra-based) that runs, talks to `container`, and exits.

Pipeline: `you/CI → dcon (Go) → container CLI → container-apiserver + plugins → Virtualization.framework → per-container Linux microVM`.

## Commands

```sh
make build          # build ./dcon with release flags (-s -w -trimpath, ~6.2 MB); plain `go build` is larger/unstripped
make test           # go test ./...
make test-race      # go test -race ./...   (what CI runs, with coverage)
make cover          # scripts/coverage.sh — total coverage + refreshes the README badge
make vet fmt
make bench          # scripts/bench.sh → scripts/bench-results.md (needs Apple container running)
make install link-docker   # install to /usr/local/bin and symlink `docker`->`dcon`

go test ./cmd/ -run TestWarmEligible        # a single test
go test -coverprofile=/tmp/c.out ./... && go tool cover -func=/tmp/c.out | tail -1
```

Tests are **pure unit tests and do not need the Apple `container` backend** — CI has no backend. Functions that shell out (`internal/pool` Boot/Destroy, real `runtime` calls) are validated manually, not in CI. Tests that touch warm-pool state redirect it by setting `HOME`/`XDG_CONFIG_HOME` to temp dirs.

To **run** dcon for real you need Apple `container` installed and started (`dcon system start`, `dcon system kernel set --recommended`). Point dcon at the binary with `DCON_CONTAINER_BIN=/usr/local/bin/container` if it isn't on PATH; `DCON_DEBUG=1` echoes every underlying `container <args>` line.

## Architecture

**All backend access funnels through `internal/runtime`.** `Bin()` locates the `container` binary; `Run` (stdio inherited, for interactive/streaming), `RunWith`, `Capture`, `CaptureSilent`, and `CaptureJSON` are the only ways to invoke it. Never call `os/exec` on `container` directly from `cmd/`.

**`cmd/` is the cobra command tree**, one file per command group, registered in `cmd/commands.go`'s `init()`. To add a Docker command: add `cmd/X.go` that builds a `container` arg list and calls a `runtime` helper, then register it. `cmd/run.go`'s `buildContainerArgs` is the most intricate translation (Docker run/create flags → container flags, `--cpus` rounding, volume/mount/tmpfs normalization).

**`internal/dockerfmt`** holds JSON models of `container … --format json` output plus Docker-style table/template rendering. `ps`/`images`/`inspect` parse container JSON, then re-render it as Docker would.

**`internal/compose`** is a built-in compose engine: `model.go` parses `compose.yaml` into a `Project`; `translate.go` turns each service into `container run` args. `Project.Order()` is topological; `Project.Levels()` groups services into dependency levels. `cmd/compose.go`'s `up` brings each level up **concurrently** (capped by `DCON_COMPOSE_PARALLEL`, default 8), preserving `depends_on` across levels.

**`internal/ui`** is the optional Charm/lipgloss styling layer. `ui.Enabled()` is the single gate — true only when stdout is a TTY and neither `DCON_PLAIN` nor `NO_COLOR` is set. `dockerfmt.Render` upgrades the default (non `--format`/`-q`) table to a styled `ui.Table` **only** when `ui.Enabled() && len(views) > 0`; every machine-readable path (json/template/`-q`, pipes, CI, empty lists) falls through to the byte-identical tabwriter output. `doctor`/`version`/`info`/`compose` colourise via `ui.*` helpers, which are no-ops when disabled. **Non-TTY output must never change** — `internal/dockerfmt/render_test.go` locks the byte-for-byte contract; `ui.SetEnabled(bool)` forces the gate in tests (CI has no TTY).

**`internal/machine` + `cmd/machine.go`** are OrbStack-style persistent Linux machines: a machine is a long-lived detached `container run -d --entrypoint sleep <distro-image> 2147483647` with labels `dcon.machine=1`/`.name`/`.distro`, named `dcon-machine-<name>` so it can never collide with a user container. `registry.go` maps 16 distro ids → images; `run.go`'s `BuildRunArgs` is the pure (unit-tested) arg builder; `state.go` persists only the *default* machine pointer (flock JSON, same pattern as pool) — the machine list is derived from the backend by label. Every mutating command resolves bare name → prefixed id via `resolveMachine`, which **re-verifies the label** before acting. Persistence across stop/start is the standard container lifecycle (verified manually). `rename` is unsupported (the backend container name is immutable).

**`internal/pool`** is the warm-VM pool — the one subsystem with real concurrency and persisted state. It pre-boots single-use microVMs so an eligible `--rm` run can `exec` into a ready VM (~90 ms) instead of cold-booting (~700 ms). Key invariants:
- **Daemonless state**: a flock-guarded JSON file (`~/Library/Application Support/dcon/pool.json`) lists only *available* members; `Claim` pops one atomically so concurrent runs never share a VM.
- **Single-use = isolation preserved**: each member is handed out exactly once then destroyed, so every run still gets a fresh microVM. Background boot/destroy/replenish are detached (`setsid`) processes that outlive the CLI.
- **Eligibility** (`cmd/warm.go` `warmEligible`) is a strict allow-list: only `--rm` runs whose flags `exec` can reproduce (env/workdir/user/tty/ulimit) take the fast path; anything boot-bound (`-v`, `-p`, limits, custom network, `--entrypoint`) transparently falls back to a cold run. The image's `ENTRYPOINT`/`CMD` are resolved at boot time (off the hot path) and stored on the member, then replayed on `exec` to match `docker run` semantics.
- The cold-boot latency is **Apple's floor** (the apiserver serializes VM boots; dcon adds ~zero overhead). Pre-warming is the only lever — do not try to optimize the cold path in Go.

## Conventions

- **Comma-bearing flags use `StringArray`, never `StringSlice`** (`-v`, `-e`, `--mount`, `--label`): `StringSlice` comma-splits and corrupts mount/env/label specs. See the flag definitions in `cmd/run.go`.
- **Backend gaps are warned, not errored.** Docker flags the `container` backend can't honor are accepted and ignored with a one-time `dcon: warning:` (compatibility shims) so existing scripts and compose files keep working; only genuinely unsupported *commands* return a hard error.
- Build metadata (`Version`/`Commit`/`Date`) is injected via `-ldflags` (see the `Makefile`); don't hardcode it.

## Docs

The README is intentionally minimal (one headline metric + links). Detailed docs live in the **GitHub wiki** (Home, Warm-Pool, Benchmarks-and-Comparison, Command-Parity, Architecture); end-to-end recipes are in `SECONDARY.md`.
