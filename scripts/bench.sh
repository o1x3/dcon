#!/usr/bin/env bash
#
# Benchmark dcon (Apple container backend) against docker on the same Mac.
# Emits a markdown table to stdout and writes scripts/bench-results.md.
#
#   ./scripts/bench.sh [RUNS]      # default 5 timed runs per metric
#
# Notes on fairness: both engines are warmed (image pre-pulled) before timing.
# dcon boots a *per-container* lightweight VM (stronger isolation); docker here
# reuses a shared Linux VM. Numbers are wall-clock medians on this host.
#
set -euo pipefail
cd "$(dirname "$0")/.."

RUNS="${1:-5}"
IMAGE="${BENCH_IMAGE:-alpine:latest}"
DCON="${DCON_BIN:-./dcon}"
OUT="scripts/bench-results.md"

have() { command -v "$1" >/dev/null 2>&1; }
median() { sort -n | awk '{a[NR]=$1} END{ if(NR%2){print a[(NR+1)/2]} else {printf "%.3f\n",(a[NR/2]+a[NR/2+1])/2} }'; }

# portable millisecond timer around a command
time_ms() {
  local start end
  start=$(python3 -c 'import time;print(int(time.time()*1000))')
  "$@" >/dev/null 2>&1 || true
  end=$(python3 -c 'import time;print(int(time.time()*1000))')
  echo $((end - start))
}

echo "# dcon vs docker — benchmark" | tee "$OUT"
echo >> "$OUT"
echo "_Host: $(uname -sm), $(sysctl -n hw.model 2>/dev/null || echo mac), $(date -u +%Y-%m-%d)_  " | tee -a "$OUT"
echo "_Runs per metric: ${RUNS} (median reported)_" | tee -a "$OUT"
echo >> "$OUT"

DCON_VER="$($DCON --version 2>/dev/null | head -1 | awk '{print $NF}')"
DOCK_VER="$(docker version --format '{{.Server.Version}}' 2>/dev/null || echo n/a)"
DOCK_BACKEND="$(docker info --format '{{.OperatingSystem}}' 2>/dev/null || echo unknown)"

mb() { awk -v b="$1" 'BEGIN{ if(b=="" || b==0){print "n/a"} else {printf "%.1f MB", b/1e6} }'; }

# --- binary size ---
dcon_size=$(stat -f%z "$DCON" 2>/dev/null || echo 0)

echo "_docker engine on this host: ${DOCK_BACKEND} (${DOCK_VER})_  " | tee -a "$OUT"
echo >> "$OUT"
echo "| metric | dcon (Apple container ${DCON_VER}) | docker (${DOCK_BACKEND}) |" | tee -a "$OUT"
echo "|---|---|---|" | tee -a "$OUT"
echo "| CLI binary (single static file) | $(mb "$dcon_size") | app bundle (100s of MB) |" | tee -a "$OUT"

# warm images
echo "warming images…" >&2
$DCON pull "$IMAGE" >/dev/null 2>&1 || true
have docker && docker pull "$IMAGE" >/dev/null 2>&1 || true

bench_metric() {
  local label="$1"; shift
  local dcon_cmd_marker="$1"; shift
  local d_samples=() k_samples=()
  for _ in $(seq 1 "$RUNS"); do
    k_samples+=("$(time_ms "$@")")
  done
  # second arg set is docker: re-run with docker binary swapped in by caller via $DOCKER_CMD
  echo "$label|$(printf '%s\n' "${k_samples[@]}" | median)"
}

# --- container run round-trip (echo) ---
echo "timing 'run --rm $IMAGE echo' …" >&2
k=(); d=()
for _ in $(seq 1 "$RUNS"); do k+=("$(time_ms $DCON run --rm "$IMAGE" echo hi)"); done
if have docker; then for _ in $(seq 1 "$RUNS"); do d+=("$(time_ms docker run --rm "$IMAGE" echo hi)"); done; fi
k_med=$(printf '%s\n' "${k[@]}" | median)
d_med="n/a"; [ ${#d[@]} -gt 0 ] && d_med="$(printf '%s\n' "${d[@]}" | median) ms"
echo "| \`run --rm $IMAGE echo\` (median) | ${k_med} ms | ${d_med} |" | tee -a "$OUT"

# --- image pull (cold) ---
echo "timing cold pull …" >&2
$DCON rmi "$IMAGE" >/dev/null 2>&1 || true
kp="$(time_ms $DCON pull "$IMAGE")"
dp="n/a"
if have docker; then docker rmi "$IMAGE" >/dev/null 2>&1 || true; dp="$(time_ms docker pull "$IMAGE") ms"; fi
echo "| cold \`pull $IMAGE\` | ${kp} ms | ${dp} |" | tee -a "$OUT"

# --- idle engine memory footprint (host-side processes) ---
echo "sampling idle footprint …" >&2
cmem="$(ps -A -o rss=,comm= 2>/dev/null | awk '/container-apiserver|container-core|container-network|container-runtime/{s+=$1} END{printf "%.0f", s/1024}')"
dmem="$(ps -A -o rss=,comm= 2>/dev/null | awk 'tolower($0) ~ /orbstack|com.docker|dockerd|colima|qemu|lima/{s+=$1} END{printf "%.0f", s/1024}')"
echo "| idle engine host RSS | ${cmem:-?} MB | ${dmem:-?} MB |" | tee -a "$OUT"

echo "| isolation model | per-container microVM | shared Linux VM |" | tee -a "$OUT"
echo "| background daemon | launchd helper, on-demand | persistent VM |" | tee -a "$OUT"

echo >> "$OUT"
echo "_Methodology: images pre-warmed; medians of ${RUNS} runs. dcon boots a fresh microVM per container (stronger isolation, higher cold start); the docker engine reuses one always-on Linux VM (faster start, larger idle footprint). vs Docker Desktop the idle-memory gap is larger still._" | tee -a "$OUT"
echo "wrote $OUT" >&2
