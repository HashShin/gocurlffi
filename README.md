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

From the shell:

```sh
gocurlffi get tls.browserleaks.com/json -i chrome150
gocurlffi post httpbin.org/post -j '{"a":1}'
```

## Browser

`browser` fetches through the same impersonating transport and then runs the
page's scripts, so client-rendered pages can be read too.

```go
package main

import (
	"fmt"

	"github.com/HashShin/gocurlffi/browser"
)

func main() {
	p, err := browser.Get("https://quotes.toscrape.com/js/", browser.Chrome131)
	if err != nil {
		panic(err)
	}
	defer p.Close()

	fmt.Println(p.Title())
	fmt.Println(p.Text())     // rendered, after the page's scripts have run
	fmt.Println(p.Markdown()) // the same document as Markdown
	for _, l := range p.Links() {
		fmt.Println(l.Href, l.Text)
	}
}
```

A page renders and drives as well as reads:

```go
png, err := p.Screenshot(browser.ScreenshotOptions{Width: 1280})
if err != nil {
	panic(err)
}
os.WriteFile("page.png", png, 0o644)

if err := p.Fill("#user", "me"); err != nil {
	panic(err)
}
if err := p.Click("button[type=submit]"); err != nil {
	panic(err)
}
p.WaitForSelector("#account", 5*time.Second)
```

From the shell, `--render` selects the same path:

```sh
gocurlffi get https://quotes.toscrape.com/js/ --render --format text
gocurlffi open example.com --screenshot page.png
gocurlffi open example.com/login --fill '#user=me' --click 'button[type=submit]'
gocurlffi open example.com/login --click '#submit' --pdf login.pdf
```

It can also be driven by Puppeteer or Playwright over CDP, or by an agent over
MCP:

```sh
gocurlffi serve            # ws://127.0.0.1:9222
gocurlffi mcp --port 9223  # http://127.0.0.1:9223/mcp
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
