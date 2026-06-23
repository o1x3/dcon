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

# --- binary size ---
dcon_size=$(stat -f%z "$DCON" 2>/dev/null || echo 0)
# resolve real docker binary (the /usr/local/bin/docker may be a shim)
docker_real="$(command -v docker || true)"
docker_size=0
[ -n "$docker_real" ] && docker_size=$(stat -f%z "$(readlink "$docker_real" 2>/dev/null || echo "$docker_real")" 2>/dev/null || echo 0)

echo "| metric | dcon (Apple container ${DCON_VER}) | docker (${DOCK_VER}) |" | tee -a "$OUT"
echo "|---|---|---|" | tee -a "$OUT"
echo "| CLI binary size | $(awk "BEGIN{printf \"%.1f MB\", ${dcon_size}/1e6}") | $(awk "BEGIN{printf \"%.1f MB\", ${docker_size}/1e6}") |" | tee -a "$OUT"

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

# --- idle engine memory footprint ---
echo "sampling idle footprint …" >&2
cmem="$(ps -A -o rss=,comm= 2>/dev/null | awk '/container-apiserver|container-core/{s+=$1} END{printf "%.0f", s/1024}')"
dmem="$(ps -A -o rss=,comm= 2>/dev/null | awk '/com.docker|dockerd|vpnkit|qemu|colima|vz/{s+=$1} END{printf "%.0f", s/1024}')"
echo "| idle engine RSS (host processes) | ${cmem:-?} MB | ${dmem:-?} MB |" | tee -a "$OUT"

echo >> "$OUT"
echo "_dcon runs each container in its own VM (per-container isolation); docker shares one Linux VM._" | tee -a "$OUT"
echo "wrote $OUT" >&2
