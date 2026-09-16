#!/usr/bin/env bash
#
# bench_browser.sh - compare the pure-Go browser with a Chromium CLI on the same pages.
#
# Both tools load the page, run its JavaScript and dump the rendered DOM, so
# the numbers are comparable. Run it with `bash` (on Termux there is no
# /usr/bin, so the shebang cannot be resolved for direct ./ execution).
#
# Usage:
#   bash scripts/bench_browser.sh [options] [url ...]
#
# Options:
#   -n, --iterations N     runs per tool per URL (default 2)
#   -i, --impersonate T    impersonation target (default custom)
#   -c, --chromium BIN     Chromium binary (default chromium-browser)
#   -t, --timeout SECS     per-run timeout (default 90)
#   -v, --virtual-ms MS    Chromium --virtual-time-budget (default 15000)
#   -h, --help             show this help
#
# Environment:
#   CHROMIUM_PROFILE   reusable --user-data-dir (default: a scratch directory)
#
# Caveats, so the numbers are read correctly:
#   - Chromium needs a warmed profile on some devices; its first run with a new
#     --user-data-dir can hang. That warmup run is untimed and printed as such.
#   - Chromium's launcher ignores SIGTERM, so every call is bounded with
#     `timeout -k` and a hard SIGKILL fallback.
#   - Chromium gets --virtual-time-budget so --dump-dom terminates on pages
#     that never reach network idle. That budget is virtual, not wall-clock,
#     so its time is a floor, not a full time-to-interactive.
#   - the Go browser waits real time for timers (Options.TimerBudget, default
#     2s) so timer-rendered content appears. Pass --timer-budget 0s via
#     GOCURLFFI_ARGS to make it purely network+CPU bound.
#
# Examples:
#   bash scripts/bench_browser.sh
#   bash scripts/bench_browser.sh -n 3 https://react.dev/ https://example.com/
#   GOCURLFFI_ARGS="--timer-budget 0s" bash scripts/bench_browser.sh

set -u

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SELF_DIR/.." && pwd)"

DEFAULT_URLS=(
  "https://quotes.toscrape.com/js/"
  "https://react.dev/"
)

ITERATIONS=2
IMPERSONATE="custom"
CHROMIUM_BIN="chromium-browser"
RUN_TIMEOUT=90
VIRTUAL_MS=15000
URLS=()
GOCURLFFI_ARGS="${GOCURLFFI_ARGS:-}"

usage() {
  awk 'NR==1 { next }
       /^#/ { sub(/^# ?/, ""); print; next }
       { exit }' "${BASH_SOURCE[0]}"
}

while [ $# -gt 0 ]; do
  case "$1" in
    -n|--iterations) ITERATIONS="$2"; shift 2 ;;
    -i|--impersonate) IMPERSONATE="$2"; shift 2 ;;
    -c|--chromium) CHROMIUM_BIN="$2"; shift 2 ;;
    -t|--timeout) RUN_TIMEOUT="$2"; shift 2 ;;
    -v|--virtual-ms) VIRTUAL_MS="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    --) shift; while [ $# -gt 0 ]; do URLS+=("$1"); shift; done ;;
    -*) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
    *) URLS+=("$1"); shift ;;
  esac
done

[ "${#URLS[@]}" -eq 0 ] && URLS=("${DEFAULT_URLS[@]}")

if ! command -v "$CHROMIUM_BIN" >/dev/null 2>&1; then
  echo "error: $CHROMIUM_BIN not found in PATH" >&2
  exit 1
fi

GOCURLFFI_BIN="${GOCURLFFI_BIN:-$ROOT_DIR/bin/gocurlffi}"
if [ ! -x "$GOCURLFFI_BIN" ]; then
  echo "building gocurlffi..." >&2
  (cd "$ROOT_DIR" && go build -o bin/gocurlffi ./cmd/gocurlffi) || exit 1
fi

PROFILE="${CHROMIUM_PROFILE:-${TMPDIR:-$ROOT_DIR}/gocurlffi-bench-profile}"
mkdir -p "$PROFILE" 2>/dev/null || true

TIME_FILE="${TMPDIR:-$ROOT_DIR}/gocurlffi-bench.out"

# run <label> <timeout-secs> <command...>
run() {
  local label="$1" rt="$2"; shift 2
  local s e rc bytes
  s=$(date +%s%N)
  timeout -k 5 "$rt" "$@" >"$TIME_FILE" 2>/dev/null
  rc=$?
  e=$(date +%s%N)
  bytes=$(wc -c <"$TIME_FILE" 2>/dev/null || echo 0)
  printf '  %-22s %8d ms  %9d bytes  rc=%d\n' "$label" "$(( (e - s) / 1000000 ))" "$bytes" "$rc"
}

run_chromium() {
  local url="$1" tag="$2"
  run "chromium $tag" "$RUN_TIMEOUT" "$CHROMIUM_BIN" --headless=new --no-sandbox \
    --disable-gpu --disable-dev-shm-usage --no-first-run --no-default-browser-check \
    --virtual-time-budget="$VIRTUAL_MS" --user-data-dir="$PROFILE" --dump-dom "$url"
}

run_gocurlffi() {
  local url="$1" tag="$2"
  # shellcheck disable=SC2086
  run "gocurlffi $tag" "$RUN_TIMEOUT" "$GOCURLFFI_BIN" get "$url" --render -f html -i "$IMPERSONATE" $GOCURLFFI_ARGS
}

echo "warming the Chromium profile (untimed; a cold profile can take ~40s)..."
timeout -k 5 "$((RUN_TIMEOUT * 2))" "$CHROMIUM_BIN" --headless=new --no-sandbox --disable-gpu \
  --virtual-time-budget=5000 --user-data-dir="$PROFILE" --dump-dom about:blank \
  >/dev/null 2>&1
echo "  warm."
echo

for url in "${URLS[@]}"; do
  echo "=== $url ==="
  for i in $(seq 1 "$ITERATIONS"); do
    run_chromium "$url" "run$i"
    run_gocurlffi "$url" "run$i"
  done
  echo
done
