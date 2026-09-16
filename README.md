# gocurlffi

A Go port of [curl_cffi](https://github.com/lexiforest/curl_cffi): an HTTP client
that impersonates real browsers' TLS/JA3, HTTP/2 and HTTP/3 fingerprints through
a `requests`-style API, plus a pure-Go headless browser for pages that need
JavaScript.

It is pure Go, with no cgo, so it cross-compiles and runs anywhere Go does,
including Android/Termux, ARM and musl-based systems.

```go
package main

import (
	"fmt"

	. "github.com/HashShin/gocurlffi"
)

func main() {
	sess := NewSession()
	defer sess.Close()

	rsp, err := sess.Send(Request{
		Method: "GET",
		URL:    "https://tls.browserleaks.com/json",
		Headers: Headers{
			"Accept: application/json",
		},
		Impersonate: DefaultChrome,
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(rsp.Text())
}
```

A one-off request needs no session at all. The module-level `Send` takes a
`Request` value, so the shortest form is one word and the literal:

```go
rsp, err := Send(Request{
	URL:         "https://tls.browserleaks.com/json",
	Impersonate: DefaultChrome,
})
```

and, for a request without a body of options to set, `Get` and its siblings go
straight to the URL:

```go
rsp, err := Get("https://tls.browserleaks.com/json",
	WithImpersonate(DefaultChrome),
)
```

`sess.Send(req)` is the same call when the session must be reused. Go's method
values shorten it further, if you want the word on its own: `send := sess.Send`
then `send(req)`.

```sh
gocurlffi get https://tls.browserleaks.com/json --impersonate chrome
```

## Documentation

The README stays short on purpose. Everything else is one hop away.

| Document | Contents |
| --- | --- |
| [`docs/api.md`](docs/api.md) | The Go API: `Request`, the options, the targets, the unqualified form. |
| [`docs/cli.md`](docs/cli.md) | Every command and flag, and the two request paths. |
| [`docs/browser.md`](docs/browser.md) | The headless browser: Go and CLI samples. |
| [`browser/README.md`](browser/README.md) | The browser in depth: what works, how fast, how it renders. |
| [`server/README.md`](server/README.md) | CDP, WebDriver BiDi and MCP. |
| [`docs/fidelity.md`](docs/fidelity.md) | What the fingerprints reproduce, and the scripts that check it. |
| [`docs/architecture.md`](docs/architecture.md) | How the packages fit together, and how to read the tree. |
| [`docs/limitations.md`](docs/limitations.md) | Everything it does not do, with the measurements. |
| [`docs/parity.md`](docs/parity.md) | What it is still missing against Lightpanda. |
| [`docs/botguard.md`](docs/botguard.md) | Why Google search is refused, measured. |
| [`docs/design/parity-plan.md`](docs/design/parity-plan.md) | Archived: how the parity work was planned. |
| [`gsearch/README.md`](gsearch/README.md) | The separate Chromium-driving search scraper. |
| [`AGENTS.md`](AGENTS.md) | Working on this repository, for a human or an agent. |

## Install
As a command:

```sh
go install github.com/HashShin/gocurlffi/cmd/gocurlffi@latest

# or from a checkout
make install                 # -> /usr/local/bin/gocurlffi
make install PREFIX=$HOME/.local
make install-go              # -> $(go env GOPATH)/bin
```

The repository is private, so a remote install needs credentials and Go told not
to use the public checksum database:

```sh
export GOPRIVATE=github.com/HashShin/*
go install github.com/HashShin/gocurlffi/cmd/gocurlffi@latest
```

Building from a checkout needs neither. `make build` writes `bin/gocurlffi`,
which is the same binary. Run it directly rather than through `go run`: the
version is embedded at build time and `go run` will not have it.

As a library, it is a normal Go module:

```sh
go get github.com/HashShin/gocurlffi
```

```sh
make build          # -> bin/gocurlffi
make test           # unit tests, no network
make test-live      # fingerprints against a recorded curl_cffi baseline
```


## Limitations

- **The browser has no CSS box model**, and **no WebAssembly** (goja has no WASM
  engine). It is a document renderer, not a web renderer.
- **Google search is refused.** The BotGuard token is handled correctly and
  Google escalates it anyway; a real Chromium from the same host is refused
  identically.
- **HTTPS/2 header casing** is lower-cased on the wire for HTTP/1.1, a fixed
  (valid) canonical Chrome extension order is emitted rather than re-randomised,
  and IP/port/size counters are unpopulated.
- The browser is missing a further set of web APIs, measured in
  [`docs/parity.md`](docs/parity.md).

[`docs/limitations.md`](docs/limitations.md) has the full list with the
measurements behind each one.

## License

MIT. The vendored curl-impersonate source is MIT licensed; see
`impersonate/upstream/LICENSE.curl-impersonate`.
