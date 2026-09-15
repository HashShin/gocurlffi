# gocurlffi

A Go port of [curl_cffi](https://github.com/lexiforest/curl_cffi): an HTTP client
that impersonates real browsers' TLS/JA3, HTTP/2 and HTTP/3 fingerprints through
a `requests`-style API.

It is pure Go (no cgo), so it cross-compiles and runs anywhere Go does,
including Android/Termux, ARM, and musl-based systems.

```go
package main

import (
	"fmt"

	"gocurlffi/requests"
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

## Fidelity

The fingerprint presets are generated from curl-impersonate's
`lib/impersonate.c`, the same source curl_cffi uses. For every preset, the TLS
ClientHello (cipher suites, supported groups, signature algorithms, extension
set and order, GREASE, ALPS, cert compression, record size limit, key shares),
the HTTP/2 settings/pseudo-header order/stream priority, the HTTP/3 settings and
the default browser headers are reproduced from that data.

This was verified against the installed curl_cffi by comparing the JA3N cipher
and extension sets, curves, Akamai HTTP/2 hash and, via `tls.peet.ws`, the full
HTTP/2 header order:

```
41/41 presets: exact match (0 mismatches)
```

`make test-live` runs `TestLiveFingerprintBaseline`, which compares this port's
live fingerprints against `requests/testdata/fingerprint_baseline.json`, a
baseline recorded once from the Python curl_cffi. No Python is needed to run
it. Presets that send GREASE ECH randomise the ECH payload length, which makes
BoringSSL add the padding extension only when the ClientHello is short; the
comparison therefore ignores padding, exactly as the behaviour varies in
curl_cffi too.

Note that heavily protected sites still need more than a matching fingerprint,
and their decisions are stateful. Against `www.adidas.co.uk/api/...` the *same*
`curl` command returned 404 and then 403 within 30 seconds, and plain Go, curl
and this client tracked each other rather than any fixed client class. The block
follows the IP/rate state, not the fingerprint. If a site does prefer a
non-browser client, use one of the two non-impersonating targets below.

## Impersonation targets

Besides the browser presets there are two non-browser targets:

- `native` (also `none`, `go`, or simply no impersonation): Go's own TLS and
  HTTP stack, plus the default `Accept-Encoding`. This matches curl_cffi's
  behaviour when no `impersonate` is set.
- `curl`: reproduces the system curl's OpenSSL 3.x ClientHello, its HTTP/2
  settings and its default request headers (`user-agent: curl/...`,
  `accept: */*`). Its JA4 and Akamai hash are identical to curl's
  (`t13d3013h2_1d37bd780c83_8537cf56674e`, `3:100;4:65536;2:0|1048510465|0|m,s,a,p`),
  which is useful for APIs that allow a generic curl-like client but challenge
  browser fingerprints. Override the UA or Accept with `-H` if needed.

```sh
gocurlffi get https://example.com/api --impersonate curl
gocurlffi get https://example.com/api -i native
```

There is a third non-browser target, `custom`, which pairs the curl/OpenSSL
TLS+HTTP/2 fingerprint with a fixed Android Chrome header set (the header list
from the curl invocation below). It sends exactly these headers, in order:

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
gocurlffi get 'https://www.adidas.co.uk/api/products/IS811/availability' -i custom --body
```

Verified: against that endpoint `-i custom` returns the same backend response
(404 with the product JSON) as the original curl command, request-for-request.

## Packages

- `impersonate` - presets, aliases and browser-name resolution.
- `requests` - `Session`, options, `Request`/`Response`, `Headers`, `Cookies`
  and the request helpers.
- `browser` - a pure-Go headless browser that runs page JavaScript, so sites
  that render client-side can be scraped too. See `browser/README.md`.
- `cmd/gocurlffi` - the command line tool.
- `cmd/gobrowser` - the headless browser CLI. `--sheets` lists the stylesheets
  the page itself declares, whether each was applied, and its rule count.

## Repository layout

```
cmd/gocurlffi/          CLI
cmd/gobrowser/          headless browser CLI
browser/                pure-Go headless browser (DOM, JS, fetch, extraction)
  browser/              reference Lightpanda (Zig) checkout, gitignored
requests/               requests-like API, transport, tests
  testdata/             recorded fingerprint baseline (curl_cffi reference)
impersonate/            browser presets, aliases, TLS profile mapping
  upstream/             vendored curl-impersonate impersonate.c (MIT) +
                        LICENSE + captured_clienthellos.txt (reference data)
internal/genpresets/    parses impersonate.c -> presets_gen.go
internal/capturehello/  captures/parses a ClientHello from any command
scripts/check_sites.sh  site/target status matrix (add -B for the browser)
tools/cssdiff/          compares the browser/ cascade with a real Chromium
                        (separate module: dev tool only, requires chromedp)
```

Only `impersonate/upstream/impersonate.c` is an external build input; it is
vendored so a checkout builds and regenerates offline. Everything else is Go,
plus the one bash script. The reference Python curl_cffi is not vendored (the
recorded baseline in `requests/testdata` is enough to validate fingerprints).

## API

Module-level helpers run in a throwaway session, mirroring
`curl_cffi.requests`:

```go
requests.Get(url, opts...)
requests.Post(url, opts...)
requests.Put / Patch / Delete / Head / Options / Trace
requests.Do(method, url, opts...)
```

For connection and cookie reuse, use a `Session`:

```go
s := requests.NewSession(
	requests.WithImpersonate("firefox"),
	requests.WithHeaders([]requests.HeaderPair{{Name: "X-Api-Key", Value: "..."}}),
)
defer s.Close()

rsp, err := s.Post("https://httpbin.org/post",
	requests.WithJSON(map[string]any{"hello": "world"}),
	requests.WithTimeoutSeconds(15),
)
```

Options include: `WithParams`, `WithData`, `WithContent`, `WithJSON`,
`WithHeaders`, `WithHeader`, `WithCookies`, `WithAuth`, `WithTimeout`,
`WithAllowRedirects`, `WithMaxRedirects`, `WithProxy`, `WithProxies`,
`WithProxyAuth`, `WithVerify`, `WithReferer`, `WithAcceptEncoding`,
`WithImpersonate`, `WithJA3`, `WithAkamai`, `WithExtraFP`,
`WithDefaultHeaders`, `WithDefaultEncoding`, `WithHTTPVersion`, `WithInterface`,
`WithCert`, `WithStream`, `WithContentCallback`, `WithDiscardCookies`,
`WithRaiseForStatus`, `WithRetry`, `WithBaseURL`, `WithTrustEnv`, `WithDebug`.

`Response` mirrors curl_cffi: `Content`, `Text()`, `JSON(v)`, `StatusCode`,
`Reason`, `Ok`, `Headers`, `Cookies`, `Elapsed`, `RedirectCount`, `RedirectURL`,
`HTTPVersion`, `History`, `Request`, `IterContent`, `IterLines`,
`RaiseForStatus`, `Close`.

## CLI

After `make build` the binary is at `bin/gocurlffi` (run it directly, not with
`go run`). To run straight from source use the package path `./cmd/gocurlffi`.
The scheme is optional and defaults to https.

```sh
make build
./bin/gocurlffi list
./bin/gocurlffi get tls.browserleaks.com/json -i chrome150
./bin/gocurlffi get example.com -H 'Accept-Language: en-GB' -v
./bin/gocurlffi post httpbin.org/post -j '{"a":1}'
./bin/gocurlffi get example.com -o page.html

# or without building:
go run ./cmd/gocurlffi get ritzcarlton.com -i custom
```

## Checking sites

`scripts/check_sites.sh` fetches a list of URLs with several impersonation
targets and prints a status matrix, so you can see which target works where.

```sh
bash scripts/check_sites.sh                       # built-in sites + default targets
bash scripts/check_sites.sh https://a.com https://b.com
bash scripts/check_sites.sh -i custom,chrome131,native -t 15
bash scripts/check_sites.sh --all-targets -b      # every target, best per site
make sites
```

Defaults to these sites: `marriott.com`, `ritzcarlton.com`, `foodnetwork.com`,
`gocomics.com`, `bsky.app`, `mstdn.social`, `ubiqueros.com`.

Pass `-B` to run the same matrix with the headless browser
(`cmd/gobrowser`) instead of the plain HTTP client; cells then show
`status/rendered-size`, which makes it obvious which sites need JavaScript.

Observed results (marriott is the discriminating site):

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

## Regenerating the presets

The vendored upstream source lives in `impersonate/upstream/impersonate.c`. To
refresh the generated Go data:

```sh
make preprocess   # internal/genpresets: parse impersonate.c -> presets_gen.go
make test         # unit tests
make test-live    # live fingerprints vs the recorded curl_cffi baseline
make capture      # capture a ClientHello with internal/capturehello
```

Everything in the build and test path is Go. The only optional non-Go pieces
are `scripts/check_sites.sh` (bash) and the one-off recording of the
fingerprint baseline, which used the Python curl_cffi as the reference.

## Limitations

- HTTP/1.1 header casing is lower-cased on the wire by the transport. HTTP/2
  (the default for impersonated traffic) is unaffected because header names are
  lower-case there.
- `Response.PrimaryIP`, `LocalIP`, ports and size counters are not populated:
  the transport does not expose per-connection metadata.
- Chrome 110+ permutes its TLS extensions on every connection. This port emits
  a fixed (valid) canonical order; JA3N/JA4 (which sort extensions) are exact,
  while the raw JA3 byte order is one valid sample rather than re-randomized.
- Async sessions, WebSockets, caching backends, DoH and `curl_options` are not
  ported.
- `requests` alone has no JavaScript engine. For client-rendered pages use the
  `browser` package, which runs scripts in pure Go (`goja`) over the same
  impersonating transport, e.g. `gobrowser get https://quotes.toscrape.com/js/`.
  Some gates are still server-side and not fingerprint- or JS-based. The
  clearest example is Google: `https://www.google.com/search?q=...` returns a
  "Turn on JavaScript to keep searching" page (~92 KB) to *every* client without
  a full browser stack. Plain `curl` receives the same page, so this is not a
  client bug. The `gocurlffi` CLI detects that interstitial and prints a
  `warning:`. Server-rendered alternatives that do return linkable HTML:
  `https://www.bing.com/search?q=...`, `https://search.brave.com/search?q=...`,
  `https://lite.duckduckgo.com/lite/?q=...`.

  How far the browser gets on that page, measured. The interstitial carries
  Google's BotGuard program (pure JavaScript - `window.knitsail`, no
  WebAssembly) and the browser runs it to completion: the challenge callback
  fires rather than the `sg_b_e` error beacon, no console error is raised, and
  `SG_SS` is minted with a 0.8-1.2 KB token. The page then hands off with
  `location.replace(...)`, which the browser follows. That handoff carries the
  token in the `SG_SS` cookie, not the query string (`S()` strips `sg_ss` and
  adds only `sei`), and the jar hands the cookie back byte for byte.

  Google answers that request with `429` and a CAPTCHA reading "our systems have
  detected unusual traffic from your computer network ... the block will expire
  shortly after those requests stop". The wording points at the network, and it
  is wrong. Three controls, each varying one thing:

  - two `/search` requests in one session with JavaScript disabled, so no token
    is ever minted, both return the ordinary interstitial - repeatedly, either
    side of the token path being blocked. The IP is not rate-limited;
  - the challenge run with JavaScript on but with `SG_SS` cleared from the jar
    before the follow-up request - same session, same cookie set, same burst of
    subresources and beacons - returns the ordinary interstitial. The burst is
    not the trigger either;
  - forcing the other handoff branch (Google's code is
    `ss_cgi || document.cookie.indexOf("SG_SS=") < 0 ? T(a) : U(S())`, where `T`
    puts the token in the query and `U` leaves it in the cookie) sends
    `sg_ss=<token>&sei=<id>` in the URL instead, and is answered the same way.

  So it is the token, it is rejected over both transports, and the request is well
  formed: the VM reports success and raises no error, and the jar hands the cookie
  back byte for byte. What Google's server disagrees with is inside an encrypted
  ~800 byte blob this repository has no reference for. Query flags (`gbv=1`,
  `udm=14`), every impersonation target, a warmed cookie jar, and corrected
  binary/base64, text-encoding and cookie layers all make no difference. Use one
  of the alternatives above when the HTML is all that is wanted.
- `browser` has no CSS box model (no borders, shadows, floats, positioning,
  gradients or images), no WebSockets and no PDF output. It cascades the page's
  CSS - selectors, specificity, `!important`, inheritance, `@import`, `@media`,
  custom properties and presentational attributes - for typography, colour,
  display and spacing, and can render a page to a PNG with `Screenshot`, but
  that is a document renderer (flowed text, headings, lists, quotes, flat
  backgrounds), not a web renderer. The cascade's computed values are checked
  against Chromium with `tools/cssdiff`: on 817 Hacker News elements and 109
  quotes.toscrape elements all seven compared properties match exactly.

## License

MIT. The vendored curl-impersonate source is MIT licensed; see
`impersonate/upstream/LICENSE.curl-impersonate`.
