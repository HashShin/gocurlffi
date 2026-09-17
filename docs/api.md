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
| `Browser` | Load the URL in the full browser instead of the fast client. |
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

`Send` and `Browse` accept a `Request`, a `*Request` or a URL string, and take
options, so the common case needs no literal and the path is named by the call:

```go
sess.Send("https://example.com/", WithImpersonate(DefaultChrome))   // impersonated HTTP
sess.Browse("https://example.com/", WithImpersonate(DefaultChrome)) // the same URL, rendered
```

Both take every option the request has - headers, params, cookies, body, JSON,
timeout, proxy - so a URL and options is a complete request:

```go
rsp, err := Send("https://httpbun.com/post",
	WithImpersonate(DefaultChrome),
	WithHeaders(Headers{"Accept: application/json"}),
	WithJSON(map[string]any{"hello": "world"}),
)
```

Options are applied after the request's own fields, so one wins over the field
it names: `Send(Request{...}, WithTimeout(5*time.Second))` is that request with
a shorter timeout.

`Browser` is the one-word way to ask for the browser, which runs the page's
scripts and returns the document as it settled rather than the bytes the server
sent. It is a GET of a URL and nothing else: a method or a body is an
`*InterfaceError`, and it is orders of magnitude slower than the fast client.

```go
rsp, err := Browse("https://quotes.toscrape.com/js/")
fmt.Println(rsp.Text()) // rendered, after the page's scripts have run
```

The module root links the browser, so the import above is all a program needs
for this. Importing the browser package directly is for its page API -
`browser.Get`, `browser.New`, `browser.Options` - whose `Get` and `Options`
names the facade already uses for the HTTP verbs. A program that imports
`requests` alone leaves the browser out and is told so if it asks for one.

A `Request` with `Browser` set, sent on a session, uses one browser per session,
so pages and plain requests on that session share cookies.

The option form is the same path:

```go
rsp, err := Get("https://quotes.toscrape.com/js/", WithBrowser())
```

`Browse` takes what `Send` takes, so both paths read identically and only the
name says which is which:

```go
req := Request{URL: url, Impersonate: DefaultChrome}

rsp, err := sess.Send(req)          // impersonated HTTP
rsp, err = sess.Browse(req)         // the same request, rendered
rsp, err = sess.Browse("https://example.com/") // or a URL on its own
```

`Browse` is `Send` with `Browser` set, on the same session, with the same
cookies and fingerprint. Four spellings, one path: `Send` with the field,
`Browse`, the URL-and-options helpers with `WithBrowser`, and `Get` with
`WithBrowser`. Take whichever makes the call site read best - the `Request`
field is the one that lets a single value be sent down either path.

`Browser` is a field on the request, not a mode on the session, so the two paths
sit side by side: a program renders the pages that need their scripts and
scrapes the rest with the same client, the same cookies and the same import.
Flipping the field is the whole switch.

```go
sess := NewSession()
defer sess.Close()

page := Request{URL: quotes, Impersonate: DefaultChrome}
rsp, err := sess.Send(page) // fast: the script-built list is not there

page.Browser = true
rsp, err = sess.Send(page) // the same URL, rendered

page.Browser, page.URL = false, next
rsp, err = sess.Send(page) // fast again, on the same session
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

A dot import is what `staticcheck` reports as `ST1001`. Silence it for the
file with `//lint:file-ignore ST1001 <reason>` if your setup runs staticcheck.

`Headers` is a slice of `"Name: Value"` lines, so it can also be built from a
`map[string]string`, a `[]HeaderPair`, or an existing `*Headers`.

Options include `WithParams`, `WithData`, `WithContent`, `WithJSON`,
`WithHeaders`, `WithHeader`, `WithCookies`, `WithAuth`, `WithBrowser`,
`WithTimeout`,
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
