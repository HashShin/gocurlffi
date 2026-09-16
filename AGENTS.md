# Working on this repository

Guidance for anyone changing this code, human or agent. The README is for users
of the library; this file is about the repository itself.

## What it is

A pure-Go port of curl_cffi: an HTTP client that impersonates real browsers'
TLS/JA3, HTTP/2 and HTTP/3 fingerprints, plus a headless browser that runs page
JavaScript over the same impersonating transport. No cgo, no C, no code
generation. It cross-compiles, including to Android/Termux.

## Commands

```sh
make build        # -> bin/gocurlffi
make test         # unit tests, no network
make test-race    # the same under the race detector
make test-live    # live fingerprints vs the recorded curl_cffi baseline (network)
make vet          # go vet ./...
make fmt          # gofmt -w .
make lint         # vet, plus staticcheck if it is installed
make install      # -> /usr/local/bin, or PREFIX=... elsewhere
```

The module declares `go 1.26.0`. If the toolchain is not on PATH, set `GOPATH`
and let Go use its downloaded one:

```sh
export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOCACHE=/tmp/gocache
```

`staticcheck` needs a long timeout on a small machine; run it per package rather
than over `./...`:

```sh
staticcheck ./requests ./server ./internal/cli
staticcheck ./browser        # the slow one, tens of seconds
```

## Layout

| Path | What it is |
| --- | --- |
| `gocurlffi.go` | The facade at the module root: the whole client re-exported unqualified. |
| `requests/` | The HTTP API: `Session`, `Request`, `Response`, `Headers`, options, transport. |
| `impersonate/` | The preset table, target constants, alias resolution. |
| `browser/` | The headless browser. Files are grouped by prefix; see `docs/architecture.md`. |
| `server/` | CDP, WebDriver BiDi and MCP front ends. |
| `internal/cli/` | The command line. The flag definitions are the documentation. |
| `docs/` | User documentation. `docs/design/` is archived planning. |
| `tools/cssdiff/` | Separate module: compares the cascade and geometry with Chromium. |

`tools/cssdiff` is a separate module so its `chromedp` dependency stays out of
the library. Do not fold it in.

## Invariants

These are load-bearing. A change that breaks one is a bug, not a preference.

- **The two request paths share one transport.** `browser` fetches through
  `requests`, so `--render` changes whether scripts run, not the fingerprint.
  Verified with `-i chrome131` against `tls.browserleaks.com`: identical JA3N.
- **Presets are exact.** `impersonate/presets.go` reproduces a specific
  browser's ClientHello, HTTP/2 settings and header order. There is no generator
  and no C file any more: this Go table is the source of truth. Treat an edit
  there as a change to the fingerprint.
- **Everything in `gocurlffi.go` is an alias**, never a defined type, and it must
  cover `requests` completely. `facade_test.go` checks both, in both directions.
- **The README's Go examples must compile.** Extract the `package main` blocks
  and build them:
  ```sh
  # a fence with package main is a program; the rest are fragments by design
  cd /tmp && mkdir -p ex && cd ex
  cat > go.mod <<'EOF'
  module ex
  go 1.26.0
  require github.com/HashShin/gocurlffi v0.0.0
  replace github.com/HashShin/gocurlffi => /path/to/gocurlffi
  EOF
  ```
  This has caught real breakage twice.
- **Nothing outside Go is a build input.** Three bash scripts, one `.mjs` and one
  `.py` live in `scripts/` as development tools; none is needed to build or test.
- **Documentation lives in `docs/`, not the README.** The README is a landing
  page and carries no index of them; detail belongs in a `docs/` page, and the
  README links to one only where the text needs it, as it does for the
  limitations.

## Adding things

| Adding | Also update |
| --- | --- |
| A preset or target | `impersonate/presets.go`, `impersonate/targets.go`, and the re-exports in `requests/targets.go` and `browser/targets.go` (the tests there name what is missing) |
| A public function or type in `requests` | `gocurlffi.go` (the facade test names what is missing) |
| An option | `requests/options.go`, `docs/api.md` |
| A CLI flag | `internal/cli/` only; `gocurlffi help <cmd>` is generated from the definitions |
| A web API in the browser | `browser/js_*`, and the "Not implemented" list in `browser/README.md` |

## Gotchas

- **This host has one CPU.** `nproc` is 1 with a load average near 4, so timings
  are marginal here in a way they are not elsewhere. Two tests carry budgets
  that scale under `-race` for that reason: `TestRunawayRecursionIsBounded` and
  the `recursionBudget` constants beside it.
- **`-race` roughly doubles goja's runtime.** A test that passes normally can
  time out under the detector; that is what happened to the recursion test, and
  a runaway script consuming the `LoadTimeout` is by design, not a bug.
- **`pkill -f` kills the invoking shell** if the pattern matches the command
  line. Build the pattern at runtime, e.g. `P=$(printf 'gocurl%s' 'ffi serve')`.
- **A dot import is deliberate here.** The root facade exists to be dot
  imported. `staticcheck` reports it as `ST1001`; that is expected and
  documented in the README rather than worked around.
- **`tools/cssdiff` has its own `go.mod`**, so `go build ./...` from the root
  does not touch it, and `go test ./...` does not either. The `go` directive in
  both must stay in step (`1.26.0`), or the toolchain tries to download a second
  one.

## Style

- Comments explain why, not what. The existing code is dense with reasons;
  match it.
- No emoji, no marketing language, no "simply" in documentation.
- Commit messages state what changed and why, and record what was verified. They
  occasionally record a wrong assumption that the code corrected; keep doing
  that, it is more useful than a clean narrative.
