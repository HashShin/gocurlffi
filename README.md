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
make build                     # -> bin/gocurlffi
make install                   # -> /usr/local/bin
make install PREFIX=$HOME/.local
```

If the Go toolchain is not on `PATH`, set `GOPATH` and let Go use its downloaded
one:

```sh
export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOCACHE=/tmp/gocache
```

---

## Commands

One binary, `bin/gocurlffi`, carries both request paths and both servers.
`gocurlffi help <command>` prints a command's flags; the list is generated from
the definitions, so it cannot drift from what the command accepts.

| Command | What it does |
| --- | --- |
| `get <url>` | Fast HTTP client with a browser's TLS/JA3, HTTP/2 and HTTP/3. |
| `get <url> --render` | The same request, loaded in the pure-Go browser. |
| `open <url>` | Shorthand for `get --render`. |
| `serve` | CDP and WebDriver BiDi server on `ws://127.0.0.1:9222`. |
| `mcp` | MCP tools over stdio, or over HTTP with `--port`. |
| `targets` | List every impersonation target. |
| `version`, `help` | Print the version, or a command's flags. |

A bare method name is a command, so `gocurlffi post ...` is `gocurlffi get -X
POST ...`. `fetch`, `browse`, `render` and `list` are kept as aliases.

```sh
gocurlffi get tls.browserleaks.com/json -i chrome150
gocurlffi post httpbin.org/post -j '{"a":1}'

gocurlffi get https://quotes.toscrape.com/js/ --render --format text
gocurlffi open example.com --screenshot page.png
gocurlffi open example.com/login --fill '#user=me' --click 'button[type=submit]'

gocurlffi serve --port 9222
gocurlffi mcp --port 9223
```

`--render` is refused for `post`, `put`, `patch`, `delete`, `head`, `options`
and `trace`, because a request with a body has no browser path. Exit status is
`0` on success, `1` when the command ran and failed, `2` when the arguments were
wrong, so a shell can tell a bad URL from a bad flag.

[`docs/cli.md`](docs/cli.md) has every flag of every command.

### Working on the repository

```sh
make test                      # unit tests, no network
make test-race                 # the same under the race detector
make test-live                 # live fingerprints vs the curl_cffi baseline (network)
make vet                       # go vet ./...
make lint                      # vet, plus staticcheck when it is installed
make fmt                       # gofmt -w .
make clean                     # rm -rf bin
```

---

## Usage

```go
package main

import (
	"fmt"

	. "github.com/HashShin/gocurlffi"
)

func main() {
	rsp, err := Send(Request{
		Method: "GET",
		URL:    "https://httpbun.com/get",
		Headers: Headers{
			"Accept: application/json",
			"X-Custom: value",
		},
		Impersonate: DefaultChrome,
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(rsp.StatusCode, rsp.Text())
}
```

`Send` is the fast path. Every other sample is a fragment of that program: the
same import, then the lines that differ.

### HTTP

A URL on its own is a request, and the one-shot helpers take the same options:

```go
rsp, err := Get(url, WithImpersonate(DefaultChrome))
rsp, err = Post(url, WithJSON(map[string]any{"hello": "world"}))
```

`Put`, `Patch`, `Delete`, `Head`, `Options`, `Trace` and `Do` are the rest. The
options form sets the same fields as the literal:

```go
rsp, err = Send(url,
	WithImpersonate(DefaultChrome),
	WithHeaders(Headers{
		"Accept: application/json",
		"X-Custom: value",
	}),
	WithParams(map[string]string{"q": "go"}),
	WithTimeoutSeconds(15),
	WithProxy("socks5://127.0.0.1:1080"),
)
```

`WithHeader("Accept", "application/json")` sets one and repeats; `WithHeaders`
also takes a `map[string]string`, a `[]string` or a `[]HeaderPair`. Options are
applied after the request's own fields, so a later option wins over the field it
names.

A session keeps cookies and connections, and a request is a value, so it can be
built in one place, logged, queued, and sent either way:

```go
sess := NewSession(WithImpersonate(DefaultChrome))
defer sess.Close()

req := Request{URL: url, Impersonate: DefaultChrome}

rsp, err := Send(req)                                 // as a value
rsp, err = sess.Send(req, WithTimeout(5*time.Second)) // with an option on top
rsp, err = sess.Browse(req)                           // or rendered, same request

sess.Send(Request{Method: "POST", URL: login, JSON: credentials}) // cookies are kept
```

TLS is an option too, for a self-signed server or a client certificate:

```go
sess = NewSession(WithImpersonate(DefaultChrome), WithVerify(false))
sess = NewSession(WithImpersonate(DefaultChrome), WithCert("cert.pem", "key.pem"))
```

### Browser

```go
import _ "github.com/HashShin/gocurlffi/browser" // links the browser in

rsp, err := Browse("https://quotes.toscrape.com/js/", WithImpersonate(DefaultChrome))
fmt.Println(rsp.Text()) // rendered, after the page's scripts have run
```

The browser is a separate import on purpose: a program that only makes requests
does not pull a JavaScript engine into its binary, which would double its size -
17.2MB to 34.6MB for the same build. A `Browse` without that import reports which
one is missing.

The two paths sit side by side on one session, and the name says which is which:

```go
rsp, err := sess.Send(url)  // impersonated HTTP
rsp, err = sess.Browse(url) // the same URL, rendered
```

For the page itself, rather than its bytes:

```go
p, _ := browser.Get("https://quotes.toscrape.com/js/", browser.Chrome131)
defer p.Close()

fmt.Println(p.Title())
fmt.Println(p.Markdown())
for _, l := range p.Links() {
	fmt.Println(l.Href, l.Text)
}
```

```go
png, _ := p.Screenshot(browser.ScreenshotOptions{Width: 1280})
os.WriteFile("page.png", png, 0o644)

p.Fill("#user", "me")
p.Click("button[type=submit]")
p.WaitForSelector("#account", 5*time.Second)
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
