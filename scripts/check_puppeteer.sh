#!/usr/bin/env bash
# check_puppeteer.sh - drive the Go browser's CDP server with a real Puppeteer client.
#
# Starts a local page server and `gocurlffi serve`, then runs
# scripts/check_puppeteer.mjs against them. Node and puppeteer-core must be
# available; set NODE_PATH to the directory holding node_modules if needed.
set -euo pipefail
cd "$(dirname "$0")/.."

PORT="${PORT:-9222}"
PAGE_PORT="${PAGE_PORT:-8765}"
NODE_PATH="${NODE_PATH:-}"

go build -o bin/gocurlffi-cdp ./cmd/gocurlffi

python3 scripts/serve_test_page.py "$PAGE_PORT" &
PAGE_PID=$!
bin/gocurlffi-cdp serve --port "$PORT" &
CDP_PID=$!
trap 'kill "$PAGE_PID" "$CDP_PID" 2>/dev/null || true' EXIT

sleep 2
NODE_PATH="$NODE_PATH" node scripts/check_puppeteer.mjs "ws://127.0.0.1:$PORT" "http://127.0.0.1:$PAGE_PORT/"
