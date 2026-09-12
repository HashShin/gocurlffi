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
- `cmd/gocurlffi` - the command line tool.

## Repository layout

```
cmd/gocurlffi/          CLI
requests/               requests-like API, transport, tests
  testdata/             recorded fingerprint baseline (curl_cffi reference)
impersonate/            browser presets, aliases, TLS profile mapping
  upstream/             vendored curl-impersonate impersonate.c (MIT) +
                        LICENSE + captured_clienthellos.txt (reference data)
internal/genpresets/    parses impersonate.c -> presets_gen.go
internal/capturehello/  captures/parses a ClientHello from any command
scripts/check_sites.sh  site/target status matrix
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

```sh
gocurlffi list
gocurlffi get https://tls.browserleaks.com/json -i chrome150
gocurlffi get https://example.com -H 'Accept-Language: en-GB' -v
gocurlffi post https://httpbin.org/post -j '{"a":1}'
gocurlffi get https://example.com -o page.html
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

## License

MIT. The vendored curl-impersonate source is MIT licensed; see
`impersonate/upstream/LICENSE.curl-impersonate`.
