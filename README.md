# dcon — a Docker CLI for Apple `container`

`dcon` is a **drop-in Docker CLI replacement for macOS**. It speaks the Docker
command surface you already know (`run`, `ps`, `images`, `build`, `compose`, …)
and translates every invocation to **Apple's [`container`](https://github.com/apple/container)**
runtime, which boots each Linux container inside a lightweight virtual machine.

```
┌────────────┐   docker-style args    ┌──────────┐   container args   ┌─────────────────┐
│ you / CI   │ ─────────────────────► │   dcon   │ ─────────────────► │ Apple container │ ─► Linux VM
│ docker ... │   (run, ps, compose)   │ (this)   │  (run, ls, build)  │   (apiserver)   │
└────────────┘                        └──────────┘                    └─────────────────┘
```

There is **no daemon of its own and no desktop app** — `dcon` is a single static
Go binary that shells out to the `container` engine already installed on your
Mac. It also implements a **Compose engine** so `dcon compose up` works against
the same backend.

## Why

Apple's `container` is great but its CLI is its own dialect (`container ls`,
`container image list`, different flags). If your muscle memory, scripts, CI,
Makefiles, and tools all speak `docker`, `dcon` lets them keep working unchanged
on Apple's runtime.

## Requirements

- macOS on Apple silicon
- Apple `container` installed (`/usr/local/bin/container`) and its services
  running: `container system start` (one time). `dcon` will surface the same
  hint if they are not.
- A guest kernel installed for actually booting containers
  (`dcon system kernel set --recommended`, or it is offered during
  `container system start`). Read-only commands (`ps`, `images`, …) work without
  it.

## Install

```sh
make install                 # builds and installs /usr/local/bin/dcon
make link-docker             # optional: symlink `docker` -> `dcon` (true drop-in)
```

or just build locally:

```sh
go build -o dcon .
```

### Make it a true drop-in

Any of these work:

```sh
make link-docker             # /usr/local/bin/docker -> dcon
# or
alias docker=dcon            # add to your ~/.zshrc
```

After that, `docker run …`, `docker compose up`, `docker build …` all run on
Apple's engine.

## How translation works

`dcon` parses the Docker flag set with the same libraries the real Docker CLI
uses (cobra + pflag), then maps each flag to its `container` equivalent. Where
output differs, `dcon` asks `container` for `--format json` and re-renders the
familiar Docker tables (`CONTAINER ID  IMAGE  COMMAND  CREATED  STATUS …`).

Set `DCON_DEBUG=1` (or pass `-D`) to echo every underlying `container` command:

```sh
$ DCON_DEBUG=1 dcon run -it --rm -p 8080:80 --name web nginx
+ container [run --interactive --tty --rm --name web --publish 8080:80 nginx]
```

Point `dcon` at a different backend binary with `DCON_CONTAINER_BIN`.

## Command parity

Everything below is a real `dcon` command. ✅ = full Docker behaviour,
≈ = best-effort/approximated, 🍏 = Apple-native extra also exposed,
⛔ = genuinely unsupported by the backend (clean error).

### Containers

| Docker command | Backend mapping | Status |
|---|---|---|
| `dcon run` | `container run` | ✅ (full flag translation; `--privileged`≈`--cap-add ALL`) |
| `dcon create` | `container create` | ✅ |
| `dcon ps [-a -q --format --filter -n -l]` | `container ls --format json` → Docker table | ✅ |
| `dcon exec [-it -e -u -w]` | `container exec` | ✅ |
| `dcon start / stop / restart / kill` | `container start/stop/kill` (restart = stop+start) | ✅ |
| `dcon rm [-f]` | `container delete` | ✅ |
| `dcon logs [-f --tail]` | `container logs` | ✅ |
| `dcon inspect [--format --type]` | `container inspect` / `container image inspect` (auto) | ✅ |
| `dcon cp` | `container copy` | ✅ |
| `dcon export [-o]` | `container export` | ✅ |
| `dcon stats [--no-stream --format]` | `container stats` → Docker table (CPU% from deltas) | ✅ |
| `dcon top` | `container exec … ps -ef` | ≈ |
| `dcon port` | derived from `inspect` | ✅ |
| `dcon attach` | `container logs -f` | ≈ (stdout/stderr only; no stdin) |
| `dcon wait` | polls `inspect` | ≈ (exit code reported as 0) |
| `dcon pause / unpause` | — | ⛔ |
| `dcon rename` | — | ⛔ (id is immutable) |
| `dcon commit / diff / update` | — | ⛔ |
| `dcon container prune` | `container prune` | ✅ |

### Images & registry

| Docker command | Backend mapping | Status |
|---|---|---|
| `dcon images [-q --format --digests]` | `container image list --format json` → Docker table | ✅ |
| `dcon pull [--platform -q]` | `container image pull` | ✅ |
| `dcon push [--platform]` | `container image push` | ✅ |
| `dcon rmi [-f]` | `container image delete` | ✅ |
| `dcon tag` | `container image tag` | ✅ |
| `dcon build [-t -f --build-arg --target --platform -o …]` | `container build` | ✅ |
| `dcon save / load` | `container image save / load` | ✅ |
| `dcon image prune [-a]` | `container image prune` | ✅ |
| `dcon history` | derived from OCI config in `image inspect` | ≈ (per-layer size N/A) |
| `dcon login / logout` | `container registry login / logout` | ✅ (`-p` fed via stdin) |
| `dcon search` | — | ⛔ |

### Volumes / networks / system

| Docker command | Backend mapping | Status |
|---|---|---|
| `dcon volume create/ls/rm/inspect/prune` | `container volume …` | ✅ (driver fixed to `local`; `--size` extra 🍏) |
| `dcon network create/ls/rm/inspect/prune` | `container network …` | ✅ (`nat`→`bridge`, `--internal`) |
| `dcon network connect/disconnect` | — | ⛔ (attach at `run --network`) |
| `dcon version` / `dcon info` | synthesized from `container system version/status` | ✅ |
| `dcon system df` | `container system df` (same columns) | ✅ |
| `dcon system prune [-a --volumes]` | container + image + network (+ volume) prune | ✅ |
| `dcon events` | — | ⛔ |

### Compose (`dcon compose …`)

A built-in Compose engine parses `compose.yaml` / `docker-compose.yml` and maps
services onto `container`:

| Command | Behaviour |
|---|---|
| `up [-d --build --no-start --force-recreate]` | creates project network + volumes, builds, starts services in `depends_on` order; foreground mode streams aggregated, service-prefixed logs and stops on Ctrl-C |
| `down [-v]` | stops & removes project containers + network (+ volumes) |
| `ps [-a -q --services --format]` | lists project containers (Compose `NAME IMAGE COMMAND SERVICE …` table) |
| `logs [-f] [svc…]` | aggregated, service-prefixed logs |
| `build / pull [svc…]` | build/pull service images |
| `start / stop / restart / kill / rm [svc…]` | lifecycle on project containers |
| `run [--rm -d] svc [cmd]` | one-off container from a service definition |
| `exec [-it -u -w] svc cmd` | exec into a running service container |
| `create` | `up --no-start` |
| `config [--services --volumes]` | parse/validate and render the resolved project |
| `ls` | list Compose projects across the engine |
| `top / images / version` | supported |

Supported service keys include: `image`, `build` (context/dockerfile/args/target),
`command`, `entrypoint`, `environment`, `env_file`, `ports`, `volumes` (with
relative-path resolution), `networks`, `depends_on`, `labels`, `working_dir`,
`user`, `platform`, `cpus`, `mem_limit`, `privileged`, `cap_add`/`cap_drop`,
`dns`, `tty`, `init`, `shm_size`, `tmpfs`, `read_only`, `container_name`.
`${VAR}` / `${VAR:-default}` interpolation is applied from the environment.

Containers are labelled with the standard `com.docker.compose.*` keys, so
`dcon ps`, `dcon compose ps`, and `dcon compose ls` all recognise project
membership.

### Apple-native extras (🍏 — beyond Docker)

These have no Docker analogue and are exposed as passthroughs so no backend
feature is lost:

- `dcon machine …` — manage the container machine(s)
- `dcon builder start/status/stop/rm` — manage the build VM
- `dcon system dns / kernel / property / logs / start / stop / status`
- `dcon registry …`, `dcon image …` management groups (Apple-style)
- run/build extras: `--rosetta`, `--ssh`, `--virtualization`, `--os`, `--arch`,
  `--kernel`, `--init-image`, `--publish-socket`, `--no-dns`, `--dns-domain`

## Limitations

These are limits of the **backend**, not of `dcon` — they are the only things it
won't do, and each returns a clear message rather than failing silently:

- `pause`/`unpause`, `rename`, `commit`, `diff`, `update`, `search`, `events`
- `network connect`/`disconnect` after creation (attach networks at `run` time)
- Docker run flags with no engine equivalent are **accepted and ignored with a
  warning** so scripts/compose don't break: `--restart`, `--hostname`,
  `-P/--publish-all`, `--add-host`, `--device`, `--gpus`, `--sysctl`,
  `--memory-swap`, `--cpu-shares`, … 
- `--privileged` is approximated as `--cap-add ALL` (no device passthrough)
- `wait` reports exit code `0` (the engine does not surface process exit codes)

## Development

```sh
make build      # build ./dcon
make test       # go test ./...
make vet fmt    # static checks / formatting
```

Layout:

```
main.go                     entrypoint
cmd/                        one file per command group (cobra)
internal/runtime/           locate + drive the `container` binary
internal/dockerfmt/         JSON models + Docker-style table/template rendering
internal/compose/           compose file parser + service→container translation
```

## License

Provided as-is. Apple `container` is © Apple, under its own license.
