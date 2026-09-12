# gocurlffi - Go port of curl_cffi

BIN := bin/gocurlffi

.PHONY: all build preprocess gen fmt vet test test-live lint clean

all: build

build:
	go build -trimpath -o $(BIN) ./cmd/gocurlffi

# Regenerate impersonate/presets.json and presets_gen.go from the vendored
# curl-impersonate source.
preprocess: gen
gen:
	python3 scripts/gen_presets.py
	python3 scripts/gen_go.py
	gofmt -w impersonate/presets_gen.go

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./...

# Live fingerprint comparison against the installed curl_cffi. Requires
# python3 with curl_cffi installed and network access.
test-live: build
	GOCURLFFI_BIN=$(BIN) python3 scripts/compare_fingerprints.py

lint: vet
	@command -v staticcheck >/dev/null && staticcheck ./... || echo "staticcheck not installed, skipping"

clean:
	rm -rf bin
