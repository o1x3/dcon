# Contributing to dcon

Thanks for helping make dcon better. It's a small, boring-on-purpose Go codebase
— a translation layer from the Docker CLI to Apple's `container` runtime.

## Quick start

```sh
git clone https://github.com/o1x3/dcon.git && cd dcon
make build        # builds ./dcon (version injected via ldflags)
make test         # go test ./...
make cover        # coverage summary
make vet fmt      # static checks + formatting
```

You need Apple `container` installed and started (`dcon system start`) to run the
end-to-end paths; the unit tests don't need it.

## How it's wired

```
main.go                 entrypoint
cmd/                    one file per command group (cobra)
internal/runtime/       locate + drive the `container` binary
internal/dockerfmt/     JSON models + Docker-style table/template rendering
internal/compose/       compose parser + service→container translation
```

The pattern is always the same: parse Docker flags with cobra/pflag → build a
`container …` argument list → either stream it (`runtime.Run`) or capture JSON
(`runtime.CaptureJSON`) and re-render it Docker-style.

## Adding or fixing a command

1. Find (or add) the command in `cmd/`. Keep the Docker flag surface faithful.
2. Translate flags in a **pure, testable function** (see `buildContainerArgs`,
   `buildBuildArgs`, `buildExecArgs`) rather than inline in `RunE` — then unit
   test the translation in `cmd/translate_test.go`.
3. If a Docker flag has no backend equivalent, **accept it and warn once** (so
   scripts/compose don't break) rather than erroring — unless the backend
   genuinely can't do it, in which case return a clear message.
4. Run `make test`. The `TestNoFlagShorthandCollisions` test walks the whole
   command tree and fails on any flag-shorthand clash — keep it green.

## Conventions

- **Conventional Commits**: `type(scope): summary` (feat, fix, docs, test, ci, …).
- **No emoji-free rule** — just keep messages clear.
- `gofmt` clean, `go vet` clean (CI enforces both).
- Prefer boring, explicit code over clever abstractions.
- Don't claim parity dcon doesn't have — comments and docs should be honest about
  approximations (e.g. `--privileged` ≈ `--cap-add ALL`).

## Tests

- Translation logic → table tests in `cmd/translate_test.go`.
- Rendering → `internal/dockerfmt/*_test.go`.
- Compose parsing/translation → `internal/compose/*_test.go`.
- New repeatable flags that can carry commas must use `StringArray` (not
  `StringSlice`) — there's history here; see the `--mount`/`--env` fix.

## Releasing (maintainers)

```sh
scripts/bump-version.sh patch    # or minor | major | vX.Y.Z
```

Tags `vX.Y.Z`, pushes, and the Release workflow builds + publishes binaries.
