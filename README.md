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

`scripts/compare_fingerprints.py` runs the comparison, and
`scripts/parity_check.py` compares HTTP status codes against curl_cffi across a
set of real sites. Presets that send GREASE ECH randomise the ECH payload
length, which makes BoringSSL add the padding extension only when the
ClientHello is short; the comparison therefore ignores padding, exactly as the
behaviour varies in curl_cffi too.

Note that heavily protected sites still need more than a matching fingerprint,
and their decisions are stateful. `www.adidas.co.uk/api/...` returned 404 to
`curl` for a while and then started returning 403 to `curl`, plain Go, curl_cffi
and this client alike; the block moved with the IP/rate state, not the client.
When such a site prefers a non-browser client, use one of the two
non-impersonating targets below.

## Impersonation targets

Besides the browser presets there are two non-browser targets:

- `native` (also `none`, `go`, or simply no impersonation): Go's own TLS and
  HTTP stack, plus the default `Accept-Encoding`. This matches curl_cffi's
  behaviour when no `impersonate` is set.
- `curl`: reproduces the system curl's OpenSSL 3.x ClientHello and its HTTP/2
  settings exactly. Its JA4 and Akamai hash are identical to curl's
  (`t13d3013h2_1d37bd780c83_8537cf56674e`, `3:100;4:65536;2:0|1048510465|0|m,s,a,p`),
  which is useful for APIs that allow a generic curl-like client but challenge
  browser fingerprints.

```sh
gocurlffi get https://example.com/api --impersonate curl
gocurlffi get https://example.com/api -i native
```

## Packages

- `impersonate` - presets, aliases and browser-name resolution.
- `requests` - `Session`, options, `Request`/`Response`, `Headers`, `Cookies`
  and the request helpers.
- `cmd/gocurlffi` - the command line tool.

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

## Regenerating the presets

The vendored upstream source lives in `impersonate/upstream/impersonate.c`. To
refresh the generated Go data:

```sh
make preprocess   # parse impersonate.c -> presets.json -> presets_gen.go
make test         # unit tests
make test-live    # + live fingerprint comparison against curl_cffi
```

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
