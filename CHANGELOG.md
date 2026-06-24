# Changelog

All notable changes to dcon are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
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
- Root TLS flags (`--tls`/`--tlsverify`/`--tlscacert`/`--tlscert`/`--tlskey`)
  accepted for compatibility.
- Hand-crafted SVG benchmark graphics and an authentic terminal demo GIF.
- `SECONDARY.md` cookbook with 15 end-to-end scenarios.
- Homebrew tap + `dcon completion` documentation.
- Contributor docs, issue/PR templates, dependabot.

### Fixed
- Compose `build --no-cache`/`--pull` and `down --rmi` were registered but never
  applied; they now take effect.
- `network create --subnet <ipv6-cidr>` (with `--ipv6`) now routes to the
  backend's `--subnet-v6`; `network`/`volume inspect` honor `-f/--format`.
- Compose `pull` now fails by default on a pull error (use
  `--ignore-pull-failures` to keep the previous best-effort behavior).

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
