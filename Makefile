# gocurlffi - Go port of curl_cffi

BIN := bin/gocurlffi
BIN_BROWSER := bin/gobrowser

.PHONY: all build gobrowser preprocess gen fmt vet test test-browser test-live sites capture lint clean

all: build

build:
	go build -trimpath -o $(BIN) ./cmd/gocurlffi

# Pure-Go headless browser CLI (see browser/README.md). The commit is embedded
# so that --debug says which build produced a render: a stale binary that
# predates a cascade fix looks exactly like a cascade bug.
VERSION := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X main.version=$(VERSION)

# Pure-Go headless browser CLI (see browser/README.md).
gobrowser:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_BROWSER) ./cmd/gobrowser

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

# Unit tests for the pure-Go browser (no network).
test-browser:
	go test ./browser/ -count=1

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
