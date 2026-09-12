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
#       --all-targets    use every target from `gocurlffi list`
#   -t, --timeout SECS   per-request timeout (default 30)
#   -m, --method METHOD  HTTP method (default GET)
#   -b, --best           after the matrix, print the first working target per site
#   -o, --only-ok        only show targets that returned a 2xx/3xx status
#   -H, --header "K: V"  extra header to send (repeatable)
#   -q, --quiet          hide the per-request progress line
#   -h, --help           show this help
#
# Environment:
#   GOCURLFFI_BIN   path to the gocurlffi binary (default: <repo>/bin/gocurlffi,
#                   built automatically if missing)
#
# Examples:
#   scripts/check_sites.sh
#   scripts/check_sites.sh https://www.marriott.com/ https://bsky.app/
#   scripts/check_sites.sh -i chrome131,custom --all-targets example.com
#   scripts/check_sites.sh -b -t 15

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
HEADERS=()
SITES=()

usage() { sed -n '2,40p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; }

while [ $# -gt 0 ]; do
  case "$1" in
    -i|--targets) TARGET_LIST="$2"; shift 2 ;;
    --all-targets) USE_ALL_TARGETS=1; shift ;;
    -t|--timeout) TIMEOUT="$2"; shift 2 ;;
    -m|--method) METHOD="$2"; shift 2 ;;
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
BIN="${GOCURLFFI_BIN:-$ROOT_DIR/bin/gocurlffi}"
if [ ! -x "$BIN" ]; then
  echo "building gocurlffi..." >&2
  (cd "$ROOT_DIR" && go build -o bin/gocurlffi ./cmd/gocurlffi) || exit 1
  BIN="$ROOT_DIR/bin/gocurlffi"
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

printf "%-${site_col}s" "site"
for t in "${TARGETS[@]}"; do printf "%-16s" "$t"; done
printf "\n"

total=0
for s in "${SITES[@]}"; do
  printf "%-${site_col}s" "${s#https://}"
  for t in "${TARGETS[@]}"; do
    total=$((total + 1))
    args=("$METHOD" "$s" -i "$t" --headers -t "$TIMEOUT")
    if [ "${#HEADERS[@]}" -gt 0 ]; then
      for h in "${HEADERS[@]}"; do args+=(-H "$h"); done
    fi
    if [ "$QUIET" -eq 0 ] && [ -t 2 ]; then printf "." >&2; fi
    out="$("$BIN" "${args[@]}" 2>/dev/null | head -1)"
    code="$(echo "$out" | awk '{print $2}')"
    [ -z "$code" ] && code="ERR"
    CODES["$s|$t"]="$code"
    if [ "$ONLY_OK" -eq 1 ]; then
      cell=""
      case "$code" in
        2*|3*) cell="$code" ;;
      esac
    else
      cell="$code"
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
