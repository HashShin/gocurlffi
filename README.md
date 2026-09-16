# gocurlffi

Pure-Go HTTP client that impersonates real browsers' TLS/JA3, HTTP/2 and HTTP/3
fingerprints, plus a headless browser that runs page JavaScript. No cgo, no C.
Cross-compiles, including to Android/Termux.

```go
package main

import (
	"fmt"

	. "github.com/HashShin/gocurlffi"
)

func main() {
	rsp, err := Send(Request{
		URL:         "https://tls.browserleaks.com/json",
		Impersonate: DefaultChrome,
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(rsp.Text())
}
```

`Send` takes a `Request`. `Get` and its siblings take a URL and options:

```go
rsp, err := Get("https://tls.browserleaks.com/json", WithImpersonate(DefaultChrome))
```

Reuse a session to keep cookies and connections:

```go
sess := NewSession(WithImpersonate(DefaultChrome))
defer sess.Close()

rsp, err := sess.Get("https://example.com/")
```

The browser is a second import. It fetches through the same transport and then
runs the page:

```go
import "github.com/HashShin/gocurlffi/browser"

p, err := browser.Get("https://quotes.toscrape.com/js/", browser.Chrome131)
defer p.Close()
fmt.Println(p.Text()) // rendered, after the page's scripts have run
```

The same from the shell:

```sh
gocurlffi get tls.browserleaks.com/json -i chrome150
gocurlffi get quotes.toscrape.com/js/ --render --format text
```

## Install

```sh
go install github.com/HashShin/gocurlffi/cmd/gocurlffi@latest

# or from a checkout
make install                    # -> /usr/local/bin/gocurlffi
make install PREFIX=$HOME/.local
```

The repository is private, so a remote install needs credentials and
`GOPRIVATE=github.com/HashShin/*`. A checkout needs neither.

As a library:

```sh
go get github.com/HashShin/gocurlffi
```

## Docs

| | |
| --- | --- |
| [`docs/api.md`](docs/api.md) | `Request`, options, impersonation targets |
| [`docs/cli.md`](docs/cli.md) | Commands and flags |
| [`docs/browser.md`](docs/browser.md) | The browser, from Go and from the shell |
| [`docs/fidelity.md`](docs/fidelity.md) | What the fingerprints reproduce, and how that is checked |
| [`docs/limitations.md`](docs/limitations.md) | What it does not do, with the measurements |
| [`docs/architecture.md`](docs/architecture.md) | Package layout |
| [`browser/README.md`](browser/README.md) | The browser in depth |
| [`server/README.md`](server/README.md) | CDP, WebDriver BiDi, MCP |
| [`docs/parity.md`](docs/parity.md) | Remaining gap against Lightpanda |
| [`docs/botguard.md`](docs/botguard.md) | Why Google search is refused |
| [`gsearch/README.md`](gsearch/README.md) | The separate Chromium search scraper |
| [`AGENTS.md`](AGENTS.md) | Working on this repository |

## Limitations

- The browser has no CSS box model and no WebAssembly. It is a document
  renderer, not a web renderer.
- Google search is refused. The challenge token is handled correctly and Google
  escalates anyway; a real Chromium from this host is refused identically.
- HTTP/1.1 header names are lower-cased on the wire, Chrome's per-connection
  extension permutation is replaced by one fixed valid order, and the IP, port
  and size counters on `Response` are unpopulated.

[`docs/limitations.md`](docs/limitations.md) has the rest.

## License

MIT. The preset data is transcribed from curl-impersonate, which is MIT
licensed; see `impersonate/upstream/LICENSE.curl-impersonate`.
