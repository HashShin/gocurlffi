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

	"github.com/HashShin/gocurlffi/requests"
)

func main() {
	rsp, err := requests.Get("https://tls.browserleaks.com/json",
		requests.WithImpersonate("chrome"),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println(rsp.Text())
}
```

```sh
gocurlffi get https://tls.browserleaks.com/json --impersonate chrome
```

## Documentation

| Document | Contents |
| --- | --- |
| [`docs/cli.md`](docs/cli.md) | Every command and flag, and the two request paths. |
| [`docs/architecture.md`](docs/architecture.md) | How the packages fit together and how to read the `browser` tree. |
| [`docs/parity.md`](docs/parity.md) | What the browser is still missing against Lightpanda, measured. |
| [`docs/botguard.md`](docs/botguard.md) | Why Google search is refused, with the measurements. |
| [`requests/README.md`](requests/README.md) | The HTTP API, options and response surface. |
| [`impersonate/README.md`](impersonate/README.md) | Presets, aliases and how they map to a fingerprint. |
| [`browser/README.md`](browser/README.md) | The headless browser: what works, how fast, how it renders. |
| [`server/README.md`](server/README.md) | The CDP, WebDriver BiDi and MCP front ends. |
| [`gsearch/README.md`](gsearch/README.md) | The separate Chromium-driving search scraper. |

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

## Fidelity

The fingerprint presets are transcribed from curl-impersonate's
`lib/impersonate.c`, the same source curl_cffi uses. For every preset, the TLS
ClientHello (cipher suites, supported groups, signature algorithms, extension
set and order, GREASE, ALPS, cert compression, record size limit, key shares),
the HTTP/2 settings, pseudo-header order and stream priority, the HTTP/3
settings, and the default browser headers are reproduced from that data.

This was verified against the installed curl_cffi by comparing the JA3N cipher
and extension sets, curves, Akamai HTTP/2 hash and, via `tls.peet.ws`, the full
HTTP/2 header order:

```
42/42 recorded targets: exact match (0 mismatches)
```

`make test-live` runs `TestLiveFingerprintBaseline`, which compares this port's
live fingerprints against `requests/testdata/fingerprint_baseline.json`, a
baseline recorded once from the Python curl_cffi. No Python is needed to run it.
Presets that send GREASE ECH randomise the ECH payload length, which makes
BoringSSL add the padding extension only when the ClientHello is short; the
comparison therefore ignores padding, exactly as the behaviour varies in
curl_cffi too.

A matching fingerprint is not a bypass. Heavily protected sites need more than
that, and their decisions are stateful. Against `www.adidas.co.uk/api/...` the
*same* `curl` command returned 404 and then 403 within 30 seconds, and plain Go,
curl and this client tracked each other rather than any fixed client class. The
block follows the IP and rate state, not the fingerprint.

## Impersonation targets

`gocurlffi targets` lists every one. Besides the browser presets there are three
non-browser targets:

**`native`** (also `none`, `go`, or simply no impersonation) uses Go's own TLS
and HTTP stack plus the default `Accept-Encoding`. This matches curl_cffi's
behaviour when no `impersonate` is set.

**`curl`** reproduces the system curl's OpenSSL 3.x ClientHello, its HTTP/2
settings and its default request headers (`user-agent: curl/...`,
`accept: */*`). Its JA4 and Akamai hash are identical to curl's
(`t13d3013h2_1d37bd780c83_8537cf56674e`, `3:100;4:65536;2:0|1048510465|0|m,s,a,p`),
which is useful for APIs that allow a generic curl-like client but challenge
browser fingerprints. Override the UA or Accept with `-H` if needed.

**`custom`** pairs the curl/OpenSSL TLS and HTTP/2 fingerprint with a fixed
Android Chrome header set, sending exactly these headers in this order:

```
user-agent: Mozilla/5.0 (Linux; Android 10; K) ... Chrome/150.0.0.0 Mobile Safari/537.36
accept: text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,...
accept-encoding: gzip, deflate, br, zstd
sec-ch-ua: "Not;A=Brand";v="8", "Chromium";v="150", "Google Chrome";v="150"
sec-ch-ua-mobile: ?1
sec-ch-ua-platform: "Android"
upgrade-insecure-requests: 1
sec-fetch-site: none
sec-fetch-mode: navigate
sec-fetch-user: ?1
sec-fetch-dest: document
accept-language: en-US,en;q=0.9
priority: u=0, i
```

```sh
gocurlffi get 'https://www.adidas.co.uk/api/products/IS811/availability' -i custom --headers
```

Verified: against that endpoint `-i custom` returns the same backend response
(404 with the product JSON) as the original curl command, request-for-request.

## API

Module-level helpers run in a throwaway session, mirroring
`curl_cffi.requests`:

```go
requests.Get(url, opts...)
requests.Post(url, opts...)
requests.Put / Patch / Delete / Head / Options / Trace
requests.Do(method, url, opts...)
```

For connection and cookie reuse, use a `Session`. A request can be written as a
value and sent:

```go
sess := requests.NewSession()
defer sess.Close()

rsp, err := sess.Send(requests.Request{
	Method: "GET",
	URL:    "https://httpbun.com/get",
	Headers: requests.Headers{
		"Accept: application/json",
		"X-Custom: value",
	},
	Impersonate: impersonate.Chrome146,
})
if err != nil {
	panic(err)
}
fmt.Println(rsp.StatusCode, rsp.Text())
```

`Request` only requires `URL`; every other field falls back to the session when
it is left empty. The same request works as options, which is the shorter form
for a one-off:

```go
sess := requests.NewSession(requests.WithImpersonate("firefox"))
defer sess.Close()

rsp, err := sess.Post("https://httpbin.org/post",
	requests.WithJSON(map[string]any{"hello": "world"}),
	requests.WithTimeoutSeconds(15),
)
```

Targets are named constants, so a typo is a compile error rather than a request
that quietly uses the wrong browser:

```go
impersonate.Chrome146   impersonate.Safari260   impersonate.Firefox147
impersonate.Edge101     impersonate.Tor145      impersonate.Chrome131Android
```

`Headers` is a slice of `"Name: Value"` lines, so it can also be built from a
`map[string]string`, a `[]requests.HeaderPair`, or an existing `*Headers`.

Options include `WithParams`, `WithData`, `WithContent`, `WithJSON`,
`WithHeaders`, `WithHeader`, `WithCookies`, `WithAuth`, `WithTimeout`,
`WithAllowRedirects`, `WithMaxRedirects`, `WithProxy`, `WithProxies`,
`WithProxyAuth`, `WithVerify`, `WithReferer`, `WithAcceptEncoding`,
`WithImpersonate`, `WithJA3`, `WithAkamai`, `WithExtraFP`,
`WithDefaultHeaders`, `WithDefaultEncoding`, `WithHTTPVersion`, `WithInterface`,
`WithCert`, `WithStream`, `WithContentCallback`, `WithDiscardCookies`,
`WithRaiseForStatus`, `WithRetry`, `WithBaseURL`, `WithTrustEnv`, `WithDebug`.
See [`requests/README.md`](requests/README.md) for the full list. Five of them
are kept only for curl_cffi API compatibility and cannot be honoured by this
port's transport - `WithJA3`, `WithAkamai`, `WithExtraFP`, `WithQuote` and
`WithMaxRecvSpeed`. Setting one fails the request with an
`*UnsupportedOptionError` rather than silently sending a different fingerprint
than you asked for.

`Response` mirrors curl_cffi: `Content`, `Text()`, `JSON(v)`, `StatusCode`,
`Reason`, `Ok`, `Headers`, `Cookies`, `Elapsed`, `RedirectCount`, `RedirectURL`,
`HTTPVersion`, `History`, `Request`, `IterContent`, `IterLines`,
`RaiseForStatus`, `Close`.

## The browser

`browser` is a pure-Go headless browser that runs page JavaScript over the same
impersonating transport, so sites that render client-side can be scraped too.

```sh
gocurlffi get https://quotes.toscrape.com/js/ --render --format text
gocurlffi open example.com --screenshot page.png
gocurlffi open example.com --wait '#results' --wait-until domcontentloaded
```

It drives the page as well as reading it:

```sh
gocurlffi open example.com/login \
  --fill '#user=me' --fill '#pass=secret' --click 'button[type=submit]' \
  --wait '#account'
```

It can also be driven by real browser tooling over CDP and WebDriver BiDi, or
exposed to an agent over MCP:

```sh
gocurlffi serve            # ws://127.0.0.1:9222
gocurlffi mcp --port 9223  # http://127.0.0.1:9223/mcp
```

See [`browser/README.md`](browser/README.md) and
[`server/README.md`](server/README.md).

## Checking sites

`scripts/check_sites.sh` fetches a list of URLs with several impersonation
targets and prints a status matrix, so you can see which target works where.

```sh
bash scripts/check_sites.sh                       # built-in sites + default targets
bash scripts/check_sites.sh https://a.com https://b.com
bash scripts/check_sites.sh -i custom,chrome131,native -t 15
bash scripts/check_sites.sh --all-targets -b      # every target, best per site
bash scripts/check_sites.sh -B quote.toscrape.com # the browser path instead
make sites
```

Defaults to `marriott.com`, `ritzcarlton.com`, `foodnetwork.com`,
`gocomics.com`, `bsky.app`, `mstdn.social` and `ubiqueros.com`.

Pass `-B` to run the same matrix with the headless browser (`--render`) instead
of the plain HTTP client; cells then show `status/rendered-size`, which makes it
obvious which sites need JavaScript.

Observed results, where marriott is the discriminating site:

| target | sites passed | marriott |
| --- | --- | --- |
| `custom` | 7/7 | 200 (3/3 runs) |
| `chrome131_android`, `chrome99_android` | 6-7/7 | 403 (3/3 runs) |
| `chrome150`, `safari260_ios` | 6-7/7 | 200/403 (flaps) |
| `chrome131`, `safari2601`, `firefox147`, `edge101`, `tor145` | 6/7 | 403 |
| `native` | 5/7 | 403 |
| `curl` | 4/7 | 403 |

`custom` is the most reliable target for Akamai-style sites: it is the only one
that stayed 200 on marriott across repeated runs, while a plain browser
fingerprint is challenged there.

## Checking the presets

The preset table lives in `impersonate/presets.go`, transcribed from
curl-impersonate's `lib/impersonate.c`. It is ordinary Go source, not a build
product: there is no code generation step and nothing outside Go is needed to
build or test the project.

```sh
make test         # unit tests, including the preset invariants
make test-live    # live fingerprints vs the recorded curl_cffi baseline
make capture      # capture a ClientHello with internal/capturehello
```

`make test-live` is the strong check: it compares live handshakes against
`requests/testdata/fingerprint_baseline.json`, a baseline recorded from the
Python curl_cffi. To re-check a preset against its origin, clone
[curl-impersonate](https://github.com/lexiforest/curl-impersonate) and compare
`lib/impersonate.c`.

The only optional non-Go pieces are `scripts/check_sites.sh` (bash) and the
one-off recording of the fingerprint baseline, which used the Python curl_cffi
as the reference.

## Limitations

- **Google search is refused.** The interstitial and the BotGuard token are
  handled correctly; Google escalates the token's environment verdict anyway, and
  a real Chromium from the same host is refused identically. The measurements
  are in [`docs/botguard.md`](docs/botguard.md). Use Bing, Brave or
  `lite.duckduckgo.com` for HTML results.
- **The browser has no CSS box model.** No borders, shadows, floats,
  positioning, gradients or images. It cascades the page's CSS - selectors,
  specificity, `!important`, inheritance, `@import`, `@media`, custom properties
  and presentational attributes - for typography, colour, display and spacing,
  and can render to PNG with `--screenshot`, but that is a document renderer
  (flowed text, headings, lists, quotes, flat backgrounds), not a web renderer.
  The cascade's computed values are checked against Chromium with
  `tools/cssdiff`: on 817 Hacker News elements and 109 quotes.toscrape elements
  all seven compared properties match exactly.
- **There is no WebAssembly.** goja has no WASM engine. This is structural, and
  it is the largest single gap against Lightpanda, which embeds V8.
- **HTTP/1.1 header casing** is lower-cased on the wire by the transport. HTTP/2,
  the default for impersonated traffic, is unaffected because header names are
  lower-case there.
- `Response.PrimaryIP`, `LocalIP`, ports and size counters are not populated:
  the transport does not expose per-connection metadata.
- **Chrome 110+ permutes its TLS extensions on every connection.** This port
  emits a fixed (valid) canonical order; JA3N/JA4, which sort extensions, are
  exact, while the raw JA3 byte order is one valid sample rather than
  re-randomised.
- Async sessions, caching backends, DoH and `curl_options` are not ported.
- **`requests` alone has no JavaScript engine.** For client-rendered pages use
  the `browser` package or `--render`.
- The browser is missing a further set of web APIs. The list is measured, not
  estimated, in [`docs/parity.md`](docs/parity.md).

## License

MIT. The vendored curl-impersonate source is MIT licensed; see
`impersonate/upstream/LICENSE.curl-impersonate`.
