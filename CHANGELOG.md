# Changelog

All notable changes to dcon are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `VERSION` / `app/VERSION` files drive CLI and app releases: bumping either on
  `main` triggers the matching release workflow (builds, tags `v*` / `app-v*`,
  publishes a GitHub Release).
- `install.sh` now auto-installs **Dcon.app** from the latest `app-v*` release
  (DMG → `/Applications`) and strips quarantine xattrs on both the CLI binary
  and the app bundle so Gatekeeper does not block first launch. Skip with
  `DCON_SKIP_APP=1`; pin with `DCON_APP_VERSION=app-vX`.
- **`--mac-address` on `run`/`create`** now maps to Apple container's
  `--network <name>,mac=…` form (documented in apple/container ≥ 1.0). Alone it
  attaches to `default,mac=…`; combined with `--network`/`--net` it appends
  `,mac=…`. Conflicts with an existing `mac=` on `--network` error out.
  Compose `mac_address` uses the same translation on the primary network.
- **`dcon machine create --virtualization` / `--kernel`** forward nested
  virtualization and a custom guest kernel onto the backing `container run`
  (the same capabilities Apple's native `container machine` exposed in 1.1.0).
  Escape hatch `dcon machine native …` still reaches the backend machine group.

### Changed
- `dcon system property` help text notes that Apple container 1.0 removed
  `property get`/`set` in favour of `~/.config/container/config.toml` (`property
  list` still works). `get`/`set` now return a clear dcon error pointing at the
  TOML file instead of an opaque backend failure.

### Fixed
- `dcon system kernel set` is now idempotent. Apple's `container system kernel
  set` re-downloads and copies the kernel with no overwrite path, so re-running
  it (or running it when the kernel is already present) failed with an EEXIST
  error. dcon now treats that specific failure as a successful no-op, which also
  removes the misleading "kernel install skipped" warning from `install.sh` when
  the kernel is already installed.

## [1.1.0] — 2026-06-25

Warm-pool start latency, a setup doctor, and a sweeping Docker/Compose
command-and-flag parity pass.

### Added
- **Warm-VM pool** — pre-boots single-use microVMs so an eligible `--rm` run
  `exec`s into a ready VM in **~90 ms** instead of a ~769 ms cold boot, while
  still handing each run a fresh VM (isolation preserved). New `dcon warm
  [-n N] IMAGE`, `dcon warm ls`, `dcon warm prune`; opt-in `DCON_WARM=auto`
  self-priming after eligible runs and `DCON_WARM_TTL` idle reaping. Ineligible
  runs transparently fall back to the cold path.
- **`dcon doctor`** (also `dcon system doctor`) — setup diagnostic that probes
  CLI/backend/kernel/builder/`docker` symlink/warm-pool health and exits
  non-zero when something needs attention.
- **Full `docker run`/`create` flag surface.** The complete Docker flag set is
  now accepted; flags the backend can't honor (`--security-opt`, `--pids-limit`,
  `--volumes-from`, `--health-*`, namespace flags, `--log-driver`/`--log-opt`,
  cgroup/blkio/device/oom resource limits, `--annotation`, …) are warned and
  ignored instead of hard-failing the command with "unknown flag".
- **`docker context`** — daemonless shim exposing the built-in `default` context
  (`ls`, `show`, `use`, `inspect`) so tooling that probes `docker context` works.
- **`docker manifest`** group, **`docker import`**, and recognised stubs for the
  Swarm/orchestration family (`swarm`, `node`, `service`, `stack`, `secret`,
  `config`, `plugin`, `trust`) — clear, specific messages instead of "unknown
  command".
- **Compose subcommands:** `push`, `port`, `attach`, `pause`, `unpause`,
  `events`, `watch` — completing the Docker Compose v2 subcommand surface.
- **Compose flag parity** across `up`/`down`/`build`/`run`/`exec`/`ps`/`pull`/
  `create`/`logs`, plus Compose global flags (`--progress`, `--ansi`,
  `--parallel`, `--env-file`, `--compatibility`, `--dry-run`).
- **`docker compose -f file.yml`/`-p name`** global shorthands now work (rewritten
  to long forms before parsing, sidestepping cobra's persistent-shorthand
  collision with `logs -f`/`rm -f`/`run -p`).
- `docker info`/`version --format` now honor Go templates and `json` output
  (`docker info -f '{{.ServerVersion}}'` works); inspect templates gained the
  `json`/`prettyjson`/`join`/`split` helper funcs.
- **Pull concurrency** — `--max-concurrent-downloads` plus `DCON_PULL_CONCURRENCY`;
  default raised to 8 concurrent layer downloads (backend default is 3).
- **Parallel Compose startup** — `compose up` brings independent services up
  concurrently within each dependency level (cap via `DCON_COMPOSE_PARALLEL`,
  default 8); `depends_on` ordering is preserved across levels.
- `compose logs --tail` is now wired to the backend; `--no-log-prefix` added.
- Root TLS flags (`--tls`/`--tlsverify`/`--tlscacert`/`--tlscert`/`--tlskey`)
  accepted for compatibility; `system`/`network`/`volume prune --filter` accepted.
- `install.sh` now auto-installs the Apple `container` prerequisite (signed
  `.pkg`), elevates `sudo`, strips quarantine, starts the backend, optionally
  installs the guest kernel, and runs `dcon doctor`; knobs `DCON_SKIP_PREREQS`,
  `DCON_CONTAINER_VERSION`, `DCON_SKIP_SETUP`, `DCON_YES`.
- Homebrew tap + `dcon completion` documentation.
- Hand-crafted SVG benchmark graphics and an authentic terminal demo GIF;
  `SECONDARY.md` cookbook with 15 end-to-end scenarios; contributor docs,
  issue/PR templates, dependabot.

### Changed
- `compose pull` now **fails by default** on a pull error (matching Docker
  Compose); pass `--ignore-pull-failures` for the previous best-effort behavior.

### Fixed
- Compose `build --no-cache`/`--pull` and `down --rmi` were registered but never
  applied; they now take effect.
- `network create --subnet <ipv6-cidr>` (with `--ipv6`) now routes to the
  backend's `--subnet-v6`; `network`/`volume inspect` honor `-f/--format`.
- `compose logs --since`/`--until`/`--timestamps` now warn instead of silently
  doing nothing.
- Warm path applies the image's `ENTRYPOINT`/`CMD` so warmed runs match
  `docker run` semantics.

## [1.0.0] — 2026-06-24

First release. A drop-in Docker CLI for macOS, backed by Apple's `container`.

### Added
- **Containers:** `run`, `create`, `ps`, `exec`, `start`, `stop`, `restart`,
  `kill`, `rm`, `logs`, `inspect`, `cp`, `export`, `stats`, `top`, `port`,
  `attach`, `wait`, `container prune` — full Docker flag translation and
  Docker-style `ps`/`stats` output.
- **Images & registry:** `images`, `pull`, `push`, `rmi`, `tag`, `build`,
  `history`, `save`, `load`, `image prune`, `login`, `logout`.
- **Volumes / networks / system:** Docker-style `volume`/`network` management,
  `version`, `info`, `system df`, `system prune`.
- **Compose engine:** `up` (with `--scale`, `--build`, `--no-start`,
  `--remove-orphans`), `down`, `ps`, `logs`, `build`, `pull`, lifecycle,
  `run`, `exec` (`--index`), `create`, `config`/`convert`, `ls`, `top`,
  `images`, `scale`, `wait`, `cp`. Profiles, per-service networks,
  `deploy.resources`, `ulimits`, `env_file` long-form, `${VAR:-default}`.
- **Apple-native passthrough:** `machine`, `builder`, `system dns/kernel/property`.
- Version injection via ldflags; CI (vet/test/coverage/cross-build) and tagged
  release pipeline; one-line `install.sh`.

### Fixed
- 59 parity findings from an adversarial audit (e.g. `--cpus` float→int,
  `image inspect --format`, `ps --filter status=` vocabulary, `--mount`/`--tmpfs`
  option normalization, comma-containing flag values via `StringArray`).
- Flag-shorthand collisions that panicked `compose run`/`logs`/`rm` and `history`.

[Unreleased]: https://github.com/o1x3/dcon/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/o1x3/dcon/releases/tag/v1.0.0
