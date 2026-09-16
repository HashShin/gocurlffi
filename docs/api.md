# API and impersonation targets

The Go surface: how a request is written, what a `Request` carries, the
impersonation targets, and the options. Every name here is reachable
unqualified from the module root; see the README for that spelling.

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

`import . "github.com/HashShin/gocurlffi"` brings the whole client into one
namespace, which is how the examples here are written. Every name it provides is
an alias of the same name in the `requests` package, so the unqualified and the
qualified spelling refer to the same values, types and functions.

Module-level helpers run in a throwaway session, mirroring
`curl_cffi.requests`:

```go
Get(url, opts...)
Post(url, opts...)
Put / Patch / Delete / Head / Options / Trace
Do(method, url, opts...)
```

For connection and cookie reuse, use a `Session` and send a `Request` value, as
in the example at the top. `Request` only requires `URL`; every other field falls
back to the session when it is left empty, so a zero `Timeout` or an empty
`Proxy` leaves the session's setting alone instead of clearing it:

| Field | Meaning |
| --- | --- |
| `Method` | HTTP method. Empty means GET. |
| `URL` | Target. A URL with no scheme defaults to https. |
| `Headers` | `Headers`, `[]string`, `[]HeaderPair`, `map[string]string`, or `*Headers`. |
| `Params` | Query parameters, appended to any already in the URL. |
| `Cookies` | Cookies for this request. |
| `Body` | Raw request body. |
| `JSON` | Marshalled as the body with `application/json`. Wins over `Body`. |
| `Impersonate` | Fingerprint target. |
| `Timeout` | Overrides the session timeout when non-zero. |
| `Proxy` | Overrides the session proxy when non-empty. |

The same request works as options:

```go
sess := NewSession(WithImpersonate(DefaultFirefox))
defer sess.Close()

rsp, err := sess.Post("https://httpbin.org/post",
	WithJSON(map[string]any{"hello": "world"}),
	WithTimeoutSeconds(15),
)
```

The fingerprint target can be named two ways:

```go
Impersonate: Chrome146   // a constant: a typo does not compile
Impersonate: "chrome146" // a plain string: a typo fails at request time
```

Prefer the constant. A misspelt one does not compile, whereas a misspelt string
fails only when the request is made, as an `*ImpersonateError`. All 40 targets
are named, for example `Chrome146`, `Safari260`, `Firefox147`, `Edge101`,
`Tor145` and `Chrome131Android`, and the family defaults such as `DefaultChrome`
follow the current version of each family rather than pinning one.

`Headers` is a slice of `"Name: Value"` lines, so it can also be built from a
`map[string]string`, a `[]HeaderPair`, or an existing `*Headers`.

### If you would rather have prefixes

Every one of those names is also in the `requests` package, so importing it
normally gives `requests.Request`, `requests.Headers`, `requests.Chrome146` and
so on, and puts nothing else in scope:

```go
import "github.com/HashShin/gocurlffi/requests"

sess := requests.NewSession()
rsp, err := sess.Send(requests.Request{
	URL:         url,
	Headers:     requests.Headers{"Accept: application/json"},
	Impersonate: requests.Chrome146,
})
```

Import that one instead if any of the unqualified names collide with a name
already in your file; `Get`, `Head`, `Options`, `Timeout` and `Cookie` are the
likely ones. A dot import is what `staticcheck` reports as `ST1001`, so to keep
it deliberately, silence it for the file with a `//lint:file-ignore ST1001
<reason>` comment, or exclude ST1001 in `staticcheck.conf`.

Options include `WithParams`, `WithData`, `WithContent`, `WithJSON`,
`WithHeaders`, `WithHeader`, `WithCookies`, `WithAuth`, `WithTimeout`,
`WithAllowRedirects`, `WithMaxRedirects`, `WithProxy`, `WithProxies`,
`WithProxyAuth`, `WithVerify`, `WithReferer`, `WithAcceptEncoding`,
`WithImpersonate`, `WithJA3`, `WithAkamai`, `WithExtraFP`,
`WithDefaultHeaders`, `WithDefaultEncoding`, `WithHTTPVersion`, `WithInterface`,
`WithCert`, `WithStream`, `WithContentCallback`, `WithDiscardCookies`,
`WithRaiseForStatus`, `WithRetry`, `WithBaseURL`, `WithTrustEnv`, `WithDebug`.
See [`requests/README.md`](../requests/README.md) for the full list. Five of them
are kept only for curl_cffi API compatibility and cannot be honoured by this
port's transport - `WithJA3`, `WithAkamai`, `WithExtraFP`, `WithQuote` and
`WithMaxRecvSpeed`. Setting one fails the request with an
`*UnsupportedOptionError` rather than silently sending a different fingerprint
than you asked for.

`Response` mirrors curl_cffi: `Content`, `Text()`, `JSON(v)`, `StatusCode`,
`Reason`, `Ok`, `Headers`, `Cookies`, `Elapsed`, `RedirectCount`, `RedirectURL`,
`HTTPVersion`, `History`, `Request`, `IterContent`, `IterLines`,
`RaiseForStatus`, `Close`.
