# dcon vs docker — benchmark

_Host: Darwin arm64, Mac16,12, 2026-06-23_  
_Runs per metric: 5 (median reported)_

_docker engine on this host: OrbStack (29.4.0)_  

| metric | dcon (Apple container 1.0.0-dev) | docker (OrbStack) |
|---|---|---|
| CLI binary (single static file) | 7.7 MB | app bundle (100s of MB) |
| `run --rm alpine:latest echo` — cold (fresh microVM) | 700 ms | 206 ms |
| `run --rm alpine:latest echo` — **warm pool** (still per-container) | **92 ms** | 206 ms |
| cold `pull alpine:latest` | 12712 ms | 3266 ms |
| idle engine host RSS | 91 MB | 1004 MB |
| isolation model | per-container microVM | shared Linux VM |
| background daemon | launchd helper, on-demand | persistent VM |

_Methodology: images pre-warmed; medians of 5 runs. **Cold** dcon boots a fresh microVM per container (max isolation, ~92 MB idle). **Warm pool** keeps a pre-booted single-use microVM ready and exec's into it — same per-container isolation, but the boot cost is paid ahead of time, landing under the always-warm docker engine (which reuses one shared VM at ~1 GB idle). Each idle warm VM costs ~35 MB until claimed; enable with `dcon warm` or `DCON_WARM=auto`. Pull uses dcon's default of 8 concurrent layer downloads._
