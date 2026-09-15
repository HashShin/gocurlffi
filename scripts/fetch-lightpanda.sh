#!/usr/bin/env bash
# fetch-lightpanda.sh - check out the Zig Lightpanda browser as the reference
# the pure-Go port in browser/ is measured against, and mark the checkout as its
# own Go module.
#
# Lightpanda ships a few generated Go files under src/data/. Without a nested
# go.mod, `go test ./...` in this repository would try to compile them and fail,
# so the stub below keeps the reference invisible to the Go toolchain.
set -euo pipefail
cd "$(dirname "$0")/.."

DIR="browser/browser"
REPO="${LIGHTPANDA_REPO:-https://github.com/lightpanda-io/browser}"

if [ ! -d "$DIR/.git" ]; then
  git clone --depth=1 "$REPO" "$DIR"
fi

cat > "$DIR/go.mod" <<'GOMOD'
// Not part of Lightpanda: this stub makes the reference checkout a separate Go
// module so the parent repository's `go test ./...` skips its generated Go files.
module lightpanda-reference

go 1.24
GOMOD

echo "reference ready at $DIR"
