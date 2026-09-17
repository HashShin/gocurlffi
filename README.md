# gocurlffi

Pure-Go HTTP client that impersonates real browsers' TLS/JA3, HTTP/2 and HTTP/3
fingerprints, plus a headless browser that runs page JavaScript.

No cgo, no C, no code generation. Cross-compiles, including to Android/Termux.

---

## Install

```sh
go get github.com/HashShin/gocurlffi

go install github.com/HashShin/gocurlffi/cmd/gocurlffi@latest

make build                     # -> bin/gocurlffi
make install                   # -> /usr/local/bin
make install PREFIX=$HOME/.local
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

`Send` is the fast path. Every other sample is a fragment of one of the two
programs below: the same import, then the lines that differ. The rest of the
HTTP API - options, sessions, TLS - is in [`docs/api.md`](docs/api.md).

### Browser

The same request with the page's scripts run. One import does it: the facade
links the browser in.

```go
package main

import (
	"fmt"

	. "github.com/HashShin/gocurlffi"
)

func main() {
	rsp, err := Browse(Request{
		Method: "GET",
		URL:    "https://quotes.toscrape.com/js/",
		Headers: Headers{
			"Accept: text/html",
		},
		Impersonate: DefaultChrome,
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(rsp.StatusCode, rsp.Text()) // rendered, after the scripts have run
}
```

The facade links the browser, so it carries goja, the JavaScript engine the
browser runs pages with, and both programs above build to 34.6MB. A program
that imports `github.com/HashShin/gocurlffi/requests` instead is 17.2MB; import
`requests` when a program only makes requests.

The page API is the browser package's own, because `Get` and `Options` are
already the HTTP verbs here:

```go
import "github.com/HashShin/gocurlffi/browser"

p, _ := browser.Get("https://quotes.toscrape.com/js/", browser.Chrome131)
defer p.Close()

fmt.Println(p.Title())
fmt.Println(p.Markdown())
for _, l := range p.Links() {
	fmt.Println(l.Href, l.Text)
}
```

The two paths sit side by side on one session, and the name says which is which:

```go
rsp, err := sess.Send(url)  // impersonated HTTP
rsp, err = sess.Browse(url) // the same URL, rendered
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
