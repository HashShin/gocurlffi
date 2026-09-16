# gocurlffi - Go port of curl_cffi

BIN := bin/gocurlffi

.PHONY: all build preprocess gen fmt vet test test-browser test-live sites capture lint clean

all: build

# The commit is embedded so that --debug says which build produced a render: a
# stale binary that predates a cascade fix looks exactly like a cascade bug.
VERSION := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X main.version=$(VERSION)

# One binary for both paths: "gocurlffi get" is the fast HTTP client with
# TLS/JA3 impersonation, "gocurlffi get --render" (or "open") is the pure-Go
# headless browser, and serve/mcp/targets share the same file. See browser/README.md.
build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/gocurlffi

# Regenerate impersonate/presets_gen.go from the vendored curl-impersonate
# source. Pure Go, no Python.
preprocess: gen
gen:
	go run ./internal/genpresets

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./...

# Unit tests for the pure-Go browser and the CLI (no network).
test-browser:
	go test ./browser/ ./internal/cli/ -count=1

# Live fingerprint check against the recorded curl_cffi baseline (network).
test-live:
	go test ./requests -run TestLiveFingerprintBaseline -count=1 -v

# Capture the ClientHello sent by an external command (pure Go).
capture:
	go run ./internal/capturehello 'curl -s --http2 -k -o /dev/null %s' 

# Status matrix for a list of sites across impersonation targets.
sites: build
	bash scripts/check_sites.sh

lint: vet
	@command -v staticcheck >/dev/null && staticcheck ./... || echo "staticcheck not installed, skipping"

clean:
	rm -rf bin
