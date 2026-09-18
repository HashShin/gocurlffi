#!/usr/bin/env bash
#
# check_sites.sh - fetch a list of URLs with several impersonation targets and
# print a status matrix, so you can see which target works where.
#
# Usage:
#   bash scripts/check_sites.sh [options] [url ...]
#
# Note: run it with `bash` (on Termux there is no /usr/bin, so the
# #!/usr/bin/env shebang cannot be resolved for direct ./ execution).
#
# Options:
#   -i, --targets LIST   comma-separated impersonation targets
#                        (default: native,curl,custom,chrome131,chrome136,
#                         safari2601,firefox147,edge101,chrome131_android)
#       --all-targets    use every target from `shade list`
#   -t, --timeout SECS   per-request timeout (default 30)
#   -m, --method METHOD  HTTP method (default GET)
#   -B, --browser        use the pure-Go headless browser ("get --render") and
#                        run page JavaScript; cells show status/rendered-size
#   -b, --best           after the matrix, print the first working target per site
#   -o, --only-ok        only show targets that returned a 2xx/3xx status
#   -H, --header "K: V"  extra header to send (repeatable)
#   -q, --quiet          hide the per-request progress line
#   -h, --help           show this help
#
# Environment:
#   SHADE_BIN   path to the shade binary (default: <repo>/bin/shade,
#                   built automatically if missing)
#
# Examples:
#   scripts/check_sites.sh
#   scripts/check_sites.sh https://www.marriott.com/ https://bsky.app/
#   scripts/check_sites.sh -i chrome131,custom --all-targets example.com
#   scripts/check_sites.sh -b -t 15
#   scripts/check_sites.sh -B -i chrome131,custom https://bsky.app/

set -u

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SELF_DIR/.." && pwd)"

DEFAULT_TARGETS=(native curl custom chrome131 chrome136 safari2601 firefox147 edge101 chrome131_android)
DEFAULT_SITES=(
  "https://www.marriott.com/"
  "https://www.ritzcarlton.com/"
  "https://www.foodnetwork.com/"
  "https://www.gocomics.com/"
  "https://bsky.app/"
  "https://mstdn.social/"
  "https://ubiqueros.com/"
)

TARGET_LIST=""
USE_ALL_TARGETS=0
TIMEOUT=30
METHOD="GET"
SHOW_BEST=0
ONLY_OK=0
QUIET=0
BROWSER=0
HEADERS=()
SITES=()

usage() {
  awk 'NR==1 { next }
       /^#/ { sub(/^# ?/, ""); print; next }
       { exit }' "${BASH_SOURCE[0]}"
}

while [ $# -gt 0 ]; do
  case "$1" in
    -i|--targets) TARGET_LIST="$2"; shift 2 ;;
    --all-targets) USE_ALL_TARGETS=1; shift ;;
    -t|--timeout) TIMEOUT="$2"; shift 2 ;;
    -m|--method) METHOD="$2"; shift 2 ;;
    -B|--browser) BROWSER=1; shift ;;
    -b|--best) SHOW_BEST=1; shift ;;
    -o|--only-ok) ONLY_OK=1; shift ;;
    -H|--header) HEADERS+=("${2:-}"); shift 2 ;;
    -q|--quiet) QUIET=1; shift ;;
    -h|--help) usage; exit 0 ;;
    --) shift; while [ $# -gt 0 ]; do SITES+=("$1"); shift; done ;;
    -*)
      echo "unknown option: $1" >&2
      usage >&2
      exit 2 ;;
    *) SITES+=("$1"); shift ;;
  esac
done

# --- locate or build the binary -------------------------------------------
# Both modes use the one binary; -B only changes the flags it is called with.
BIN="${SHADE_BIN:-$ROOT_DIR/bin/shade}"
if [ ! -x "$BIN" ]; then
  echo "building shade..." >&2
  (cd "$ROOT_DIR" && go build -o bin/shade ./cmd/shade) || exit 1
  BIN="$ROOT_DIR/bin/shade"
fi

# A hard wall-clock guard for browser mode, where a page loads many
# subresources and its scripts can outlive the per-request timeout.
RUNNER=()
if [ "$BROWSER" -eq 1 ] && command -v timeout >/dev/null 2>&1; then
  RUNNER=(timeout "$((TIMEOUT * 6))")
fi

# --- resolve the target list ----------------------------------------------
if [ "$USE_ALL_TARGETS" -eq 1 ]; then
  TARGETS=()
  while IFS= read -r t; do TARGETS+=("$t"); done < <("$BIN" list)
elif [ -n "$TARGET_LIST" ]; then
  IFS=',' read -r -a TARGETS <<< "$TARGET_LIST"
else
  TARGETS=("${DEFAULT_TARGETS[@]}")
fi

if [ "${#SITES[@]}" -eq 0 ]; then
  SITES=("${DEFAULT_SITES[@]}")
fi

# --- run the matrix --------------------------------------------------------
declare -A STATUS
declare -A CODES
site_col=24

if [ "$BROWSER" -eq 1 ]; then
  echo "mode: headless browser (cells are status/rendered-size)"
fi

printf "%-${site_col}s" "site"
for t in "${TARGETS[@]}"; do printf "%-16s" "$t"; done
printf "\n"

total=0
for s in "${SITES[@]}"; do
  printf "%-${site_col}s" "${s#https://}"
  for t in "${TARGETS[@]}"; do
    total=$((total + 1))
    if [ "$QUIET" -eq 0 ] && [ -t 2 ]; then printf "." >&2; fi
    if [ "$BROWSER" -eq 1 ]; then
      tmpout="$(mktemp)"; tmperr="$(mktemp)"
      bargs=(get "$s" --render -i "$t" --status --timeout "${TIMEOUT}s" -o "$tmpout")
      for h in "${HEADERS[@]:-}"; do
        [ -n "$h" ] && bargs+=(-H "$h")
      done
      if [ "${#RUNNER[@]}" -gt 0 ]; then
        "${RUNNER[@]}" "$BIN" "${bargs[@]}" >/dev/null 2>"$tmperr" || true
      else
        "$BIN" "${bargs[@]}" >/dev/null 2>"$tmperr" || true
      fi
      code="$(sed -n 's/^status: \([0-9][0-9]*\).*/\1/p' "$tmperr" | head -1)"
      size=0
      [ -f "$tmpout" ] && size="$(wc -c <"$tmpout" 2>/dev/null || echo 0)"
      rm -f "$tmpout" "$tmperr"
      [ -z "$code" ] && code="ERR"
      cell="$code/$((size / 1024))k"
    else
      args=("$METHOD" "$s" -i "$t" --headers -t "$TIMEOUT")
      if [ "${#HEADERS[@]}" -gt 0 ]; then
        for h in "${HEADERS[@]}"; do args+=(-H "$h"); done
      fi
      out="$("$BIN" "${args[@]}" 2>/dev/null | head -1)"
      code="$(echo "$out" | awk '{print $2}')"
      [ -z "$code" ] && code="ERR"
      cell="$code"
    fi
    CODES["$s|$t"]="$code"
    if [ "$ONLY_OK" -eq 1 ]; then
      cell=""
      case "$code" in
        2*|3*) cell="$code" ;;
      esac
    fi
    printf "%-16s" "${cell:0:15}"
  done
  printf "\n"
done
if [ "$QUIET" -eq 0 ] && [ -t 2 ]; then printf "\n" >&2; fi

# --- best target per site --------------------------------------------------
if [ "$SHOW_BEST" -eq 1 ]; then
  echo
  echo "best target per site:"
  for s in "${SITES[@]}"; do
    best=""
    for t in "${TARGETS[@]}"; do
      code="${CODES["$s|$t"]}"
      case "$code" in
        2*|3*) best="$t ($code)"; break ;;
      esac
    done
    [ -z "$best" ] && best="none"
    printf "  %-32s %s\n" "${s#https://}" "$best"
  done
fi
