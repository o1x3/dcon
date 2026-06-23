# Changelog

All notable changes to dcon are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Hand-crafted SVG benchmark graphics and an authentic terminal demo GIF.
- `SECONDARY.md` cookbook with 15 end-to-end scenarios.
- Homebrew tap + `dcon completion` documentation.
- Contributor docs, issue/PR templates, dependabot.

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
