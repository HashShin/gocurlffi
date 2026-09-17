# gocurlffi

Pure-Go HTTP client that impersonates real browsers' TLS/JA3, HTTP/2 and HTTP/3
fingerprints, plus a headless browser that runs page JavaScript.

No cgo, no C, no code generation. Cross-compiles, including to Android/Termux.

---

## Install

```sh
go get github.com/HashShin/gocurlffi

go install github.com/HashShin/gocurlffi/cmd/gocurlffi@latest
```

The repository is private, so a remote install needs credentials and
`GOPRIVATE=github.com/HashShin/*`. A checkout needs neither:

```sh
make install                    # -> /usr/local/bin/gocurlffi
make install PREFIX=$HOME/.local
```

---

## Usage

### Request style

```go
package main

import (
	"fmt"

	. "github.com/HashShin/gocurlffi"
)

func main() {
	rsp, err := Send(Request{
		URL:         "https://httpbun.com/get",
		Impersonate: DefaultChrome,
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(rsp.StatusCode, rsp.Text())
}
```

`Request` is a value, so a request can be built in one place and sent in
another, logged, or queued.

### URL and options

The same request without the literal:

```go
rsp, err := Send("https://httpbun.com/get",
	WithImpersonate(DefaultChrome),
	WithParams(map[string]string{"q": "go"}),
	WithTimeoutSeconds(15),
	WithProxy("socks5://127.0.0.1:1080"),
)
```

Options are applied after the request's own fields, so a later option wins over
the field it names: `Send(req, WithTimeout(5*time.Second))` is that request with
a shorter timeout.

### Headers

```go
WithHeader("Accept", "application/json")             // one
WithHeaders(map[string]string{"X-Custom": "value"})  // or several
```

As a value, a struct literal names the type of every field it sets, so the field
and its type are both written:

```go
req := Request{
	URL:     "https://httpbun.com/get",
	Headers: Headers{"Accept: application/json", "X-Custom: value"},
}
```

### One-shot, no session

```go
rsp, err := Get("https://httpbun.com/get", WithImpersonate(DefaultChrome))
rsp, err = Post("https://httpbun.com/post", WithJSON(map[string]any{"hello": "world"}))
```

`Put`, `Patch`, `Delete`, `Head`, `Options`, `Trace` and `Do` are the rest.

### Session: cookies and connections

```go
sess := NewSession(WithImpersonate(DefaultChrome))
defer sess.Close()

sess.Send(Request{Method: "POST", URL: login, JSON: credentials}) // cookies are kept
rsp, err := sess.Get("https://example.com/dashboard")
```

### Browser: the rendered page

```go
package main

import (
	"fmt"

	. "github.com/HashShin/gocurlffi"
	_ "github.com/HashShin/gocurlffi/browser" // links the browser in
)

func main() {
	rsp, err := Browse("https://quotes.toscrape.com/js/")
	if err != nil {
		panic(err)
	}
	fmt.Println(rsp.Text()) // rendered, after the page's scripts have run
}
```

The browser is a separate import on purpose: the client does not pull a
JavaScript engine into a program that only makes requests, which would double
its size - 17.2MB to 34.6MB for the same binary. A `Browse` without that import
reports which one is missing.

### Both paths side by side

```go
rsp, err := sess.Send("https://example.com/")  // impersonated HTTP
rsp, err = sess.Browse("https://example.com/") // the same URL, rendered
```

`Send` and `Browse` take a `Request`, a `*Request` or a URL, so which path a
request takes is one name at the call site and nothing else. `Get` with
`WithBrowser()` is the same two paths spelled the other way round.

### Browser: read the page

```go
p, err := browser.Get("https://quotes.toscrape.com/js/", browser.Chrome131)
if err != nil {
	panic(err)
}
defer p.Close()

fmt.Println(p.Title())
fmt.Println(p.Markdown())
for _, l := range p.Links() {
	fmt.Println(l.Href, l.Text)
}
```

### Browser: screenshot, fill, click

```go
png, err := p.Screenshot(browser.ScreenshotOptions{Width: 1280})
os.WriteFile("page.png", png, 0o644)

p.Fill("#user", "me")
p.Click("button[type=submit]")
p.WaitForSelector("#account", 5*time.Second)
```

### Proxy, timeout, TLS

```go
rsp, err := Send(Request{
	URL:         "https://httpbun.com/ip",
	Impersonate: DefaultChrome,
	Proxy:       "socks5://127.0.0.1:1080",
	Timeout:     10 * time.Second,
})

sess := NewSession(WithVerify(false))                          // self-signed
sess = NewSession(WithCert("cert.pem", "key.pem"))             // client certificate
```

---

## CLI

```sh
gocurlffi get tls.browserleaks.com/json -i chrome150
gocurlffi post httpbin.org/post -j '{"a":1}'

gocurlffi get https://quotes.toscrape.com/js/ --render --format text
gocurlffi open example.com --screenshot page.png
gocurlffi open example.com/login --fill '#user=me' --click 'button[type=submit]'

gocurlffi targets          # every impersonation target
gocurlffi serve            # CDP over ws://127.0.0.1:9222
gocurlffi mcp --port 9223  # MCP over http://127.0.0.1:9223/mcp
```

---

## Limitations

- The browser is a document renderer, not a web renderer, and there is no
  WebAssembly: goja has no engine for it.
- HTTP/1.1 header names are lower-cased on the wire, Chrome's per-connection
  extension permutation is replaced by one fixed valid order, and the IP, port
  and size counters on `Response` are unpopulated.
- The browser is missing a further set of web APIs.

[`docs/limitations.md`](docs/limitations.md) has the rest, with the measurements.

## License

MIT. The preset data is transcribed from curl-impersonate, which is MIT
licensed; see `impersonate/upstream/LICENSE.curl-impersonate`.
