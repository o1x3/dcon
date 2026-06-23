# dcon vs docker — benchmark

_Host: Darwin arm64, Mac16,12, 2026-06-23_  
_Runs per metric: 5 (median reported)_

_docker engine on this host: OrbStack (29.4.0)_  

| metric | dcon (Apple container 1.0.0-dev) | docker (OrbStack) |
|---|---|---|
| CLI binary (single static file) | 7.5 MB | app bundle (100s of MB) |
| `run --rm alpine:latest echo` (median) | 741 ms | 207 ms |
| cold `pull alpine:latest` | 20264 ms | 3368 ms |
| idle engine host RSS | 92 MB | 1094 MB |
| isolation model | per-container microVM | shared Linux VM |
| background daemon | launchd helper, on-demand | persistent VM |

_Methodology: images pre-warmed; medians of 5 runs. dcon boots a fresh microVM per container (stronger isolation, higher cold start); the docker engine reuses one always-on Linux VM (faster start, larger idle footprint). vs Docker Desktop the idle-memory gap is larger still._
