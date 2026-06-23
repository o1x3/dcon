<div align="center">

# dcon cookbook — scenarios & recipes

Real-world, copy-pasteable workflows. Every command is `dcon …`; alias `docker=dcon` and they're all `docker …`.

</div>

> New here? Start with the [README](README.md). This file is the deep end:
> varied, end-to-end scenarios that show what dcon (and the Apple `container`
> backend) can actually do.

## Contents

- [1. Make dcon your `docker`](#1-make-dcon-your-docker)
- [2. Throwaway dev containers](#2-throwaway-dev-containers)
- [3. A real Compose stack (web + db + cache)](#3-a-real-compose-stack-web--db--cache)
- [4. Compose profiles: dev vs prod](#4-compose-profiles-dev-vs-prod)
- [5. Scaling a service](#5-scaling-a-service)
- [6. Build, tag, and push an image](#6-build-tag-and-push-an-image)
- [7. Private / local / insecure registries](#7-private--local--insecure-registries)
- [8. Persisting data with volumes](#8-persisting-data-with-volumes)
- [9. Service-to-service networking](#9-service-to-service-networking)
- [10. Debugging a running container](#10-debugging-a-running-container)
- [11. Scripting with `--format` and `-q`](#11-scripting-with---format-and--q)
- [12. Apple-native superpowers](#12-apple-native-superpowers)
- [13. Cleaning up](#13-cleaning-up)
- [14. Troubleshooting](#14-troubleshooting)
- [15. What dcon won't do (and why)](#15-what-dcon-wont-do-and-why)

---

## 1. Make dcon your `docker`

```sh
# one-time backend setup
dcon system start
dcon system kernel set --recommended

# become docker (pick one)
alias docker=dcon                 # add to ~/.zshrc
make link-docker                  # symlink /usr/local/bin/docker -> dcon

# verify your muscle memory still works
docker run --rm alpine echo "it's just docker"
docker ps
docker images
```

Your existing `Makefile`, `docker compose` files, and CI scripts now run on
Apple's runtime with no changes.

## 2. Throwaway dev containers

```sh
# interactive shell, auto-removed on exit
dcon run --rm -it alpine sh

# a one-off Python REPL with your code mounted
dcon run --rm -it -v "$PWD:/app" -w /app python:3.12 python

# a postgres you can poke at, with a published port
dcon run -d --name pg -e POSTGRES_PASSWORD=dev -p 5432:5432 postgres:16
dcon exec -it pg psql -U postgres
dcon rm -f pg
```

`--cpus` accepts Docker's fractional form — dcon rounds up to whole CPUs (the
backend's unit) and tells you:

```sh
dcon run --rm --cpus 1.5 alpine nproc
# dcon: warning: --cpus 1.5 rounded up to 2 (backend accepts whole CPUs only)
```

### ⚡ Warm pool — `--rm` runs in ~90 ms instead of ~700 ms

A fresh microVM cold-boots in ~700 ms. The warm pool pre-boots a **single-use**
VM and `exec`s your workload into it, landing in ~90 ms — faster than a
shared-VM engine, and each run still gets its own fresh VM (the member is used
once, then destroyed). Great for tight test/dev loops that re-run the same image.

```sh
dcon warm alpine                 # pre-boot 1 warm alpine VM (~700 ms, once)
dcon run --rm alpine echo hi     # served from the pool → ~90 ms
dcon warm -n 3 python:3.12       # keep 3 warm python VMs ready
dcon warm ls                     # CONTAINER ID / IMAGE / AGE / STATE
dcon warm prune                  # tear the pool down

# Or let dcon self-prime: the first eligible run is cold, the rest are warm.
export DCON_WARM=auto            # off by default to keep the ~92 MB idle footprint
for i in 1 2 3; do dcon run --rm alpine echo "run $i"; done
```

What's eligible for the fast path: a `--rm` run with no bind mounts, ports,
resource limits, or custom networking (those are bound at VM-boot time, so they
take the cold path automatically). Env vars, `--workdir`, `--user`, `-it`, and
the image's own `ENTRYPOINT`/`CMD` are reproduced exactly — warm output matches
cold output. Knobs: `DCON_WARM_DEPTH` (sustained depth), `DCON_WARM_TTL` (idle
reap), `DCON_WARM=off` (force always-cold). See also `dcon doctor`.

## 3. A real Compose stack (web + db + cache)

`compose.yaml`:

```yaml
name: shop
services:
  web:
    build: ./web
    ports: ["8080:80"]
    environment:
      DATABASE_URL: postgres://app:secret@db:5432/app
      REDIS_URL: redis://cache:6379
    depends_on: [db, cache]
  db:
    image: postgres:16
    environment:
      POSTGRES_USER: app
      POSTGRES_PASSWORD: secret
    volumes: ["pgdata:/var/lib/postgresql/data"]
  cache:
    image: redis:7
volumes:
  pgdata:
```

```sh
dcon compose up -d --build        # build web, start db+cache first (depends_on), then web
dcon compose ps                   # NAME  IMAGE  COMMAND  SERVICE  STATUS  PORTS
dcon compose logs -f web          # follow one service (prefixed, aggregated)
dcon compose exec db psql -U app  # hop into a service
dcon compose down -v              # stop, remove containers + network + named volumes
```

dcon writes the standard `com.docker.compose.*` labels, so `dcon ps` and
`dcon compose ls` recognise the project too.

## 4. Compose profiles: dev vs prod

```yaml
services:
  app:    { image: myapp }
  db:     { image: postgres:16 }
  adminer:                       # only runs when the "dev" profile is on
    image: adminer
    profiles: ["dev"]
```

```sh
dcon compose up -d                      # app + db
dcon compose --profile dev up -d        # app + db + adminer
COMPOSE_PROFILES=dev dcon compose up -d  # same, via env
```

## 5. Scaling a service

```sh
dcon compose up -d --scale worker=3     # worker-1, worker-2, worker-3
dcon compose ps worker
dcon compose scale worker=1             # tears down worker-2 and worker-3
```

> A service with a fixed `container_name:` can't be scaled (dcon errors clearly,
> just like Docker).

## 6. Build, tag, and push an image

```sh
# build for the host platform
dcon build -t myorg/api:1.4 .

# multi-platform (the backend builds per-arch)
dcon build --platform linux/arm64,linux/amd64 -t myorg/api:1.4 .

# build args, target stage, no cache
dcon build --build-arg VERSION=1.4 --target runtime --no-cache -t myorg/api:1.4 .

# tag + push
dcon tag myorg/api:1.4 myorg/api:latest
dcon push myorg/api:1.4
```

Docker `--output` exporter types are translated: `type=docker` / `type=image`
load into the local store (mapped to the backend's `oci`); `type=registry`
returns a clear error (push separately with `dcon push`).

## 7. Private / local / insecure registries

```sh
# log in (password via stdin, like docker)
echo "$TOKEN" | dcon login -u me --password-stdin ghcr.io

# a plain-HTTP local registry (the backend speaks http when asked)
dcon login -u dev -p dev --scheme http localhost:5000
dcon pull --scheme http localhost:5000/myimage:dev
dcon push --scheme http localhost:5000/myimage:dev

dcon registry ls                  # who am I logged into?
dcon logout ghcr.io
```

## 8. Persisting data with volumes

```sh
dcon volume create appdata --size 10G       # --size is an Apple-container extra
dcon run -d -v appdata:/data --name w alpine sh -c 'echo hi > /data/x; sleep 999'
dcon volume ls
dcon volume inspect appdata
dcon rm -f w
dcon run --rm -v appdata:/data alpine cat /data/x   # data survives the container
```

Bind mounts work with relative paths too — `-v ./src:/app` resolves against your
cwd. macOS-only `:z`/`:Z`/`:cached` flags are stripped with a warning (no SELinux
on a Linux VM).

## 9. Service-to-service networking

```sh
dcon network create backend
dcon run -d --name api  --network backend myorg/api
dcon run -d --name db   --network backend postgres:16
# api reaches the database at host "db" on the shared network
dcon exec api ping -c1 db
```

In Compose, declare networks per service and dcon wires them up (honouring
`internal:` and `labels:`):

```yaml
services:
  api: { image: myorg/api, networks: [edge, backend] }
  db:  { image: postgres:16, networks: [backend] }
networks:
  edge:
  backend:
    internal: true
```

## 10. Debugging a running container

```sh
dcon logs -f --tail 100 web         # stream the last 100 lines
dcon exec -it web sh                # shell in
dcon top web                        # processes (ps -ef inside)
dcon stats                          # live CPU/mem/net/block/pids table
dcon stats --no-stream              # one snapshot
dcon port web                       # published port mappings
dcon inspect web                    # full backend JSON
dcon inspect --format '{{.status.state}}' web   # template a field
dcon cp web:/etc/nginx/nginx.conf ./nginx.conf  # copy a file out
dcon logs --boot web                # the VM boot log (Apple-container extra)
```

## 11. Scripting with `--format` and `-q`

```sh
# stop everything that's running
dcon stop $(dcon ps -q)

# remove all stopped containers
dcon container prune

# IDs of images for a repo
dcon images --filter reference='myorg/*' -q

# custom columns (Go templates, docker-compatible fields)
dcon ps --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}'

# JSON per line for piping into jq
dcon ps --format json | jq -r '.Names'
```

## 12. Apple-native superpowers

Things Docker on macOS can't do, exposed straight through dcon:

```sh
# run an x86_64 image on Apple silicon via Rosetta
dcon run --rm --rosetta --platform linux/amd64 amd64/alpine uname -m

# forward your SSH agent into the build/container
dcon run --rm --ssh -v "$PWD:/src" -w /src alpine sh -c 'ssh -T git@github.com'

# manage the container "machine" and builder VMs
dcon machine ls
dcon builder status
dcon system dns create mydomain.test     # local DNS domains
dcon system kernel set --recommended     # swap the guest kernel
```

## 13. Cleaning up

```sh
dcon system df                  # disk usage: images / containers / volumes
dcon container prune            # stopped containers
dcon image prune -a             # unused images
dcon system prune -a --volumes  # everything unused, including volumes
```

## 14. Troubleshooting

| Symptom | Fix |
|---|---|
| `Plugins are unavailable` / commands hang | `dcon system start` |
| container won't boot / `no default kernel` | `dcon system kernel set --recommended` |
| see what dcon runs under the hood | `DCON_DEBUG=1 dcon run …` (prints the `container …` call) |
| `--restart` / `--gpus` / `-P` "ignored" warnings | expected — accepted-but-unsupported flags, so scripts don't break |
| point dcon at a different backend binary | `DCON_CONTAINER_BIN=/path/to/container dcon …` |
| build fails with "builder not running" | `dcon builder start` |

## 15. What dcon won't do (and why)

These are limits of the **backend**, not dcon — each returns a clear message
instead of failing silently:

- `pause` / `unpause`, `rename`, `commit`, `diff`, `update`, `search`, `events`
- `network connect` / `disconnect` after creation — attach at `run --network` time
- `--network host` / `container:` — no host/namespace networking inside a VM
- `wait` reports exit code `0` — the engine doesn't surface process exit codes
- `--privileged` is approximated as `--cap-add ALL` (no device passthrough)

Everything else is real Docker behaviour, translated. See the
[README parity tables](README.md#command-parity) for the full surface.
