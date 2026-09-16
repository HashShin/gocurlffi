# requests

A Go port of the `curl_cffi.requests` API: a requests-style HTTP client that
impersonates real browsers' TLS/JA3, HTTP/2 and HTTP/3 fingerprints.

The package is pure Go (no cgo). A `Session` owns a cookie jar and a cache of
transports; the module-level helpers run in a throwaway session. Import path:
`github.com/HashShin/gocurlffi/requests`.

The module root re-exports all of it, so `import . "github.com/HashShin/gocurlffi"`
gives the same names unqualified; see the root README.

## Quick start

```go
package main

import (
	"fmt"

	"github.com/HashShin/gocurlffi/requests"
)

func main() {
	s := requests.NewSession(
		requests.WithImpersonate(requests.DefaultChrome),
		requests.WithTimeoutSeconds(15),
	)
	defer s.Close()

	rsp, err := s.Post("https://httpbin.org/post",
		requests.WithJSON(map[string]any{"hello": "world"}),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println(rsp.StatusCode, rsp.Text())
}
```

A one-off request needs no session:

```go
rsp, err := requests.Get("https://tls.browserleaks.com/json",
	requests.WithImpersonate(requests.DefaultChrome),
)
```

`DefaultChrome` follows the current Chrome preset; `requests.Chrome150` pins
that exact version. Either is a compile-checked name, where `"chrome"` is only
checked when the request is made.

`WithImpersonate` takes a target name. A constant is preferred, because a
misspelt one does not compile: `requests.DefaultChrome` and the other family
defaults follow the current preset of each family, the exact targets are
`requests.Chrome150` and its siblings, and both are re-exported from the
`impersonate` package, which is where they are defined and where `Targets()`
lists them all. `native`, `none`, `go` or an empty name select Go's own stack,
and `curl` reproduces the system curl.

To drop the qualifier at a call site, import the module root instead. It
re-exports this package, the targets and the options, so `Request`, `Headers` and
`Chrome146` need no prefix; see the root README. A dot import is what
`staticcheck` reports as `ST1001`, so silence it deliberately if your setup runs
staticcheck.

## Module-level helpers

Each helper builds a `Session`, sends the request and closes the session, so
cookies and connections are not reused across calls. The method is upper-cased
by `Session.Request`.

| Function | Method |
| --- | --- |
| `Get(rawURL string, opts ...Option) (*Response, error)` | GET |
| `Post(rawURL string, opts ...Option) (*Response, error)` | POST |
| `Put(rawURL string, opts ...Option) (*Response, error)` | PUT |
| `Patch(rawURL string, opts ...Option) (*Response, error)` | PATCH |
| `Delete(rawURL string, opts ...Option) (*Response, error)` | DELETE |
| `Head(rawURL string, opts ...Option) (*Response, error)` | HEAD |
| `Options(rawURL string, opts ...Option) (*Response, error)` | OPTIONS |
| `Trace(rawURL string, opts ...Option) (*Response, error)` | TRACE |
| `Do(method, rawURL string, opts ...Option) (*Response, error)` | caller-supplied |

## Session

`NewSession(opts ...Option) *Session` applies its options as defaults for every
request; options passed to a request override them for that request only. The
session is safe for concurrent use.

| Method | Purpose |
| --- | --- |
| `Request(method, rawURL string, opts ...Option) (*Response, error)` | send a request |
| `Send(req Request) (*Response, error)` | send a request described by a value |
| `Get` / `Post` / `Put` / `Patch` / `Delete` / `Head` / `Options` / `Trace` | verb helpers taking `(rawURL string, opts ...Option)` |
| `Close()` | close idle connections; later requests fail with `*SessionClosed` |
| `Cookies() *Cookies` | the live session cookie jar |
| `SetCookies(c CookieTypes)` | replace the session jar |
| `Headers() *Headers` | the session default headers (mutable) |
| `UserAgent() string` | the impersonated UA, the curl default for `curl`, `""` for `native` |

Session defaults come from `defaultConfig`:

| Setting | Default |
| --- | --- |
| timeout | 30s |
| follow redirects | true |
| maximum redirects | 30 |
| verify TLS certificates | true |
| trust proxy environment variables | true |
| default response encoding | `utf-8` |

A session keeps a transport cache keyed by impersonation name, effective proxy,
TLS verification, HTTP version, interface and client certificate, so requests
that share those values reuse connections.

`Send` takes the struct form, which is useful when a request is built from
configuration or stored rather than written out at the call site. There is a
module-level `Send(Request)` too, for a one-off that needs no session:

```go
rsp, err := sess.Send(requests.Request{
	Method:      "GET",
	URL:         "https://httpbun.com/get",
	Headers:     requests.Headers{"Accept: application/json"},
	Impersonate: requests.Chrome146, // same package: no second import
	Timeout:     10 * time.Second,
})
```

`Impersonate` is a string, so `"chrome146"` also works, but the constants are
re-exported here from the impersonate package so a request needs one import
rather than two. A misspelt constant does not compile; a misspelt string fails
when the request is made, as an `*ImpersonateError`. `impersonate.Targets()`
lists every target.

Only `URL` is required. `Method` empty means GET, and a zero `Timeout` or empty
`Proxy` leaves the session's setting in place rather than overriding it with an
empty value. `JSON` wins over `Body` when both are set. The fields are `Method`,
`URL`, `Headers`, `Params`, `Cookies`, `Body`, `JSON`, `Impersonate`, `Timeout`
and `Proxy`.

## Response

```go
rsp := requests.NewResponse() // empty: 200 OK, empty headers and cookies
```

| Field | Type | Notes |
| --- | --- | --- |
| `URL` | `string` | final URL after redirects |
| `Content` | `[]byte` | decoded body in buffered mode |
| `StatusCode` | `int` | status code |
| `Reason` | `string` | status text without the numeric code |
| `Ok` | `bool` | `200 <= StatusCode < 400` |
| `Headers` | `*Headers` | response headers |
| `Cookies` | `*Cookies` | session cookies matching the final URL |
| `Elapsed` | `time.Duration` | from the first send to the final response |
| `DefaultEncoding` | `string` | encoding used when the response has no charset |
| `RedirectCount` | `int` | number of hops, i.e. `len(History)` |
| `RedirectURL` | `string` | not populated (always `""`) |
| `HTTPVersion` | `int` | HTTP major version (1, 2 or 3) |
| `PrimaryIP`, `PrimaryPort`, `LocalIP`, `LocalPort` | `string`, `int`, `string`, `int` | not populated |
| `History` | `[]*Response` | redirect hops; their `Content`/`Body` are nil |
| `Request` | `*Request` | the final request |
| `DownloadSize` | `int64` | `len(Content)` in buffered mode; 0 when streaming |
| `UploadSize`, `HeaderSize`, `RequestSize` | `int64` | not populated |
| `Body` | `io.ReadCloser` | set with `WithStream(true)`; the caller closes it |

Methods:

| Method | Purpose |
| --- | --- |
| `Text() string` | decode `Content` with `Encoding`, replacing invalid bytes; cached |
| `JSON(v any) error` | `json.Unmarshal` of `Content` |
| `Encoding() string` | explicit encoding, else the `Content-Type` charset, else `DefaultEncoding`, else `utf-8` |
| `SetEncoding(enc string) error` | override the charset; errors once `Text` has run |
| `CharsetEncoding() string` | charset parsed from `Content-Type`, or `""` |
| `ContentType() string` | `Content-Type` header value |
| `HasHeader(name string) bool` | header presence test |
| `IsRedirect() bool` | 301/302/303/307/308 with a `Location` header |
| `ReadAll() ([]byte, error)` | consume and return a streaming body; buffered mode returns `Content` |
| `IterContent() (func() ([]byte, error, bool), error)` | chunk iterator; the bool marks completion |
| `IterLines(delimiter string) ([]string, error)` | split the body; empty delimiter splits on `\n` after normalising `\r\n` |
| `Len() int` | body length in bytes |
| `RaiseForStatus() error` | `*HTTPError` unless `Ok` |
| `Close() error` | close `Body` if set |
| `String() string` | `"<Response [200]>"` |

Compression is decoded from `Content-Encoding`: gzip/x-gzip, deflate, br/brotli
and zstd/zstandard, including stacked values.

## Request

`Request` describes the final request and is reachable as `Response.Request`.

| Field | Type |
| --- | --- |
| `URL` | `string` |
| `Method` | `string` |
| `Headers` | `*Headers` |
| `Body` | `[]byte` |

## Headers

`Headers` is an ordered, case-insensitive header collection. `Set` replaces in
place (keeping the key's position), `Add` appends a second line.

It is a `[]string` of `"Name: Value"` lines, so it can be written as a literal,
which is how headers appear in HTTP and on a command line:

```go
requests.Headers{
	"Accept: application/json",
	"X-Custom: value",
}
```

`NewHeaders(h HeaderTypes) *Headers` accepts `Headers` or a plain `[]string`
(same content), `*Headers`, `[]HeaderPair`, `map[string]string` or
`map[string][]string`. A `HeaderPair` is `{Name, Value string}`. Map inputs are
applied in sorted key order, because a map has none. An unsupported type panics,
so a caller finds out immediately rather than sending a request with headers
silently missing.

A value's leading and trailing spaces are not significant and are dropped when
the line is read back, matching how an HTTP parser treats them. Only the first
colon separates the name from the value, so a value may contain one.

| Method | Purpose |
| --- | --- |
| `Set(name, value string)` | replace, or append when absent |
| `Add(name, value string)` | append another value |
| `Del(name string)` | remove all values for a key |
| `Get(name string) string` | values joined with `", "` |
| `GetList(name string) []string` | all values in order |
| `Has(name string) bool` | presence test |
| `Len() int` | number of header lines |
| `MultiItems() []HeaderPair` | every line in order, without joining |
| `Items() map[string]string` | unique keys to joined values |
| `Keys() []string` | unique names in first-seen order |
| `Update(other HeaderTypes)` | merge another set, replacing in place |
| `Clone() *Headers` | deep copy |
| `String() string` | `Headers{...}` rendering |

Header order is part of the fingerprint. The transport lower-cases names on the
wire (HTTP/2 requires it); see Limitations for the HTTP/1.1 effect.

## Cookies

`Cookies` is an ordered, mutex-protected cookie collection.
`NewCookies(c CookieTypes) *Cookies` accepts `*Cookies`, `Cookies`,
`map[string]string` or `[]*Cookie`. A `Cookie` has `Name`, `Value`, `Domain`,
`Path`, `Secure`, `HTTPOnly` and `Expires` fields.

| Method | Purpose |
| --- | --- |
| `Set(name, value string)` | set a bare cookie |
| `SetCookie(ck Cookie)` | insert or replace, matching name+domain+path |
| `Get(name string) (string, bool)` | raw value by name |
| `GetCookie(name string) (*Cookie, bool)` | full cookie by name |
| `Del(name string)` | remove by name |
| `Items() []Cookie` | snapshot in insertion order |
| `Len() int` | count |
| `Map() map[string]string` | name to value |
| `Update(other CookieTypes)` | merge another collection |
| `String() string` | render as `a=1; b=2` |

Expired, secure-on-http and domain/path-mismatched cookies are filtered before a
request; `Set-Cookie` from each hop updates the jar unless `WithDiscardCookies`
is set.

## Params

`Params` is `[]Param`, an ordered query list that preserves duplicates;
`Param` is `{Key, Value string}` and `Params.Encode()` renders a query string.
`WithParams` accepts `Params`, `[]Param`, `url.Values`, `map[string]string` or
`map[string][]string` (and panics on any other type).

## Options

`Option` configures a `Session` or a single request. All options below exist in
`options.go`.

Some are kept only for curl_cffi API compatibility and cannot be honoured by
this port's transport. Those are marked **refused**: setting one makes the
request fail with an `*UnsupportedOptionError` before anything is sent, rather
than silently dropping it and sending a fingerprint the caller did not ask for.

### Request body

| Option | Effect |
| --- | --- |
| `WithData(v any)` | body from `string`, `[]byte`, `io.Reader`, `url.Values`, `Params`, `map[string]string` or `map[string][]string`; maps are form-encoded with `application/x-www-form-urlencoded` |
| `WithContent(v any)` | raw body from `string`, `[]byte` or `io.Reader`; other types error |
| `WithJSON(v any)` | JSON body and `application/json`, without an added trailing newline |

### URL and query

| Option | Effect |
| --- | --- |
| `WithParams(p any)` | query parameters, appended to any in the URL |
| `WithBaseURL(u string)` | base URL for resolving a relative request URL |
| `WithQuote(v any)` | URL percent-encoding parity hook; **refused** |

### Headers and cookies

| Option | Effect |
| --- | --- |
| `WithHeaders(h HeaderTypes)` | merge request headers |
| `WithHeader(name, value string)` | set one request header |
| `WithCookies(c CookieTypes)` | set request cookies |
| `WithReferer(r string)` | set `Referer` |
| `WithAuth(username, password string)` | HTTP basic auth; sent only to the original host |
| `WithAcceptEncoding(v string)` | set `Accept-Encoding`; an empty value removes it and lets the transport decompress |

### Transport and connection

| Option | Effect |
| --- | --- |
| `WithTimeout(d time.Duration)` | timeout applied to each network request |
| `WithTimeoutSeconds(s float64)` | same, in seconds |
| `WithProxy(proxyURL string)` | one proxy for all schemes |
| `WithProxies(p map[string]string)` | proxy by `"all"`, `"https"` or `"http"`, first non-empty in that order |
| `WithProxyAuth(username, password string)` | proxy credentials; impersonating transport only |
| `WithVerify(v bool)` | TLS certificate verification |
| `WithCert(certFile, keyFile string)` | client certificate for mTLS; impersonating transport only |
| `WithInterface(name string)` | bind to a local IP address; interface names are not resolved |
| `WithHTTPVersion(v string)` | `"v1"`, `"v2"` or `"v3"` (also `1`, `http/2`, `h3`, `auto`, ...) |
| `WithTrustEnv(v bool)` | honour `HTTPS_PROXY`, `https_proxy`, `ALL_PROXY`, `all_proxy` when no proxy is set |
| `WithDebug(v bool)` | verbose impersonating-transport logging |
| `WithMaxRecvSpeed(n int)` | download rate limit; **refused** |
| `WithDefaultEncoding(enc string)` | charset for responses without one |

### TLS and fingerprint

| Option | Effect |
| --- | --- |
| `WithImpersonate(name string)` | select a preset, `curl`, or `native`/`none`/`go` |
| `WithJA3(s string)` | custom JA3 string; **refused** (use `WithImpersonate`) |
| `WithAkamai(s string)` | custom Akamai HTTP/2 string; **refused** |
| `WithExtraFP(fp *ExtraFingerprints)` | per-request fingerprint overrides; **refused** |
| `WithDefaultHeaders(v bool)` | keep only the caller's headers, dropping the preset's default set (`false`); the fingerprint preset itself is still used |

`ExtraFingerprints` carries `TLSMinVersion`, `TLSGrease`,
`TLSPermuteExtensions`, `TLSCertCompression`, `TLSRecordSizeLimit`,
`HTTP2StreamWeight`, `HTTP2StreamExclusive`, `HTTP2NoPriority`, `HeaderOrder`
and `SplitCookies`. Preset headers are always merged, with user headers winning.

### Streams and callbacks

| Option | Effect |
| --- | --- |
| `WithStream(v bool)` | leave the body unread in `Response.Body` for incremental reads |
| `WithContentCallback(fn func([]byte) error)` | call `fn` for each body chunk; forces buffered reading |

### Redirects

| Option | Effect |
| --- | --- |
| `WithAllowRedirects(v bool)` | follow redirects (default true) |
| `WithMaxRedirects(n int)` | limit; `-1` means unlimited; exceeding it returns `*TooManyRedirects` |

Redirect handling mirrors libcurl: 303 always becomes GET; 301/302 from a
non-GET/HEAD method becomes GET; 307/308 preserve method and body. Each hop's
`Set-Cookie` is applied before the next hop, and `Authorization` is dropped on a
cross-host redirect.

### Retries

| Option | Effect |
| --- | --- |
| `WithRetry(n int)` | retry a failed request up to `n` extra times, with no backoff |

Any transport error is retried, including a body that cannot be replayed; there
is no retry on HTTP status.

### Behaviour

| Option | Effect |
| --- | --- |
| `WithDiscardCookies(v bool)` | do not let server cookies update the session jar |
| `WithRaiseForStatus(v bool)` | return `*HTTPError` for 4xx/5xx responses |

## Errors

`RequestException` is the base error; it carries `Msg`, a curl-like numeric
`Code` and an optional `*Response`. `errors.As` distinguishes the wrappers:

| Type | Raised when |
| --- | --- |
| `*ConnectionError` | transport failure |
| `*DNSError` | host resolution fails |
| `*ProxyError` | proxy failure |
| `*SSLError`, `*CertificateVerifyError` | TLS or certificate failure |
| `*Timeout` | timeout; also matches `ErrTimeout` via `errors.Is` and `IsTimeout` |
| `*TooManyRedirects` | redirect limit exceeded |
| `*InvalidURL`, `*InvalidSchema` | malformed URL or unsupported scheme |
| `*ImpersonateError` | unknown target or unusable TLS configuration |
| `*SessionClosed` | request after `Close` |
| `*UnsupportedOptionError` | an option set for curl_cffi parity that this port cannot honour |
| `*HTTPError` | `RaiseForStatus` / `WithRaiseForStatus` |

`NewRequestException` builds the most specific wrapper for a transport error
string. `InterfaceError`, `IncompleteRead` and `UnrewindableBodyError` are
declared for API parity but are never returned.

## Tests

The unit tests use `httptest` servers for parameters, headers, JSON and form
bodies, redirects and history, cookie forwarding, gzip decoding,
`RaiseForStatus`, timeouts, session closing and header ordering
(`session_test.go`, `headers_test.go`). `curl_test.go` and `spec_test.go` check
the curl ClientHello, the curl HTTP/2 profile and the preset spec builder.
`integration_test.go`'s `TestLiveFingerprintBaseline` compares live JA3N and
Akamai hashes against `testdata/fingerprint_baseline.json`, recorded once from
the Python curl_cffi; run it with `make test-live`.

## Limitations

- Pure Go, no cgo. The impersonating transport is built on
  `github.com/bogdanfinn/fhttp`, `tls-client` and `utls`.
- Async sessions are not implemented: `Session` is synchronous.
- WebSockets are absent, as are caching backends, DoH and `curl_options`. The
  config has an unused `dohURL` field and no option sets it.
- HTTP/1.1 header names are lower-cased on the wire by the transport. HTTP/2
  (the default for impersonated traffic) is unaffected because its names are
  lower-case anyway.
- `Response.PrimaryIP`, `PrimaryPort`, `LocalIP`, `LocalPort`, `UploadSize`,
  `HeaderSize` and `RequestSize` are never populated, and `RedirectURL` is
  always empty; `DownloadSize` is only set in buffered mode.
- Chrome 110+ permutes its TLS extensions per connection. This port emits a
  fixed valid canonical order, so JA3N/JA4 (which sort extensions) are exact
  while the raw JA3 byte order is one valid sample rather than re-randomised.
- `WithJA3`, `WithAkamai`, `WithExtraFP`, `WithQuote` and `WithMaxRecvSpeed` are
  accepted for curl_cffi compatibility but cannot be honoured, so setting any of
  them makes the request fail with an `*UnsupportedOptionError`. `WithJA3` is
  the clearest case: a JA3 string lists extension ids, not their contents, and
  the transport builds a ClientHello from a full spec, so there is nothing
  faithful to construct from it. `WithAkamai` and `WithExtraFP` are plumbed to
  no profile, and no supported transport can rate-limit for
  `WithMaxRecvSpeed`.
- `WithCert` and `WithProxyAuth` only affect the impersonating transport; the
  `native` target ignores them. `WithInterface` binds only when its value parses
  as a local IP address, not for a named interface.
- `WithProxies` reads only the `"all"`, `"https"` and `"http"` keys, and the
  environment lookup uses `HTTPS_PROXY`/`https_proxy`/`ALL_PROXY`/`all_proxy`
  (not `HTTP_PROXY`).
- `requests` has no JavaScript engine. For client-rendered pages use the
  `browser` package, which runs scripts over the same impersonating transport.
  A matching fingerprint alone is not sufficient for every site; see the root
  `README.md` for measured examples.
