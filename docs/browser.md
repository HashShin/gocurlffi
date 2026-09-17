# The browser

A pure-Go headless browser that runs page JavaScript over the same impersonating
transport. [`../browser/README.md`](../browser/README.md) documents the whole
surface; this page is the orientation.

`browser` runs page JavaScript over the same impersonating transport, so sites
that render client-side can be scraped too. It re-exports the impersonation
targets, so one import is enough, and importing it dotted needs no prefix:

For one page, a request can ask for it directly, with no browser API at all:

```go
rsp, err := Send(Request{URL: "https://quotes.toscrape.com/js/", Browser: true})
```

`Request.Browser` is served by this package, which installs itself behind it
when imported. The body is then the rendered document, and `Impersonate`,
`Proxy`, `Timeout` and `Headers` on the same request apply to every resource the
page fetches, the document and its subresources alike. Only a GET of a URL has a
browser path. A `Session` keeps one browser, so pages and plain requests on that
session share cookies.

```go
package main

import (
	"fmt"

	. "github.com/HashShin/gocurlffi/browser"
)

func main() {
	// Get fetches through the same impersonating transport the fast path
	// uses, then runs the page's scripts.
	p, err := Get("https://quotes.toscrape.com/js/", Chrome131)
	if err != nil {
		panic(err)
	}
	defer p.Close()

	fmt.Println(p.Title())
	fmt.Println(p.Text())     // the rendered text, not the raw HTML
	fmt.Println(p.Markdown()) // the same document as Markdown
	for _, l := range p.Links() {
		fmt.Println(l.Href, l.Text)
	}
}
```

`Get` opens one page in a `Browser` of its own, so a single page needs one call.
When several pages should share cookies, or more than the target has to be set,
use the longer form:

```go
b := New(Options{Impersonate: Chrome131, Proxy: proxy})
defer b.Close()

p, err := b.Open(url)
```

A `Page` renders and drives as well as reads:

```go
png, err := p.Screenshot(ScreenshotOptions{Width: 1280})
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

The same thing from the shell, where `--render` selects this path:

```sh
gocurlffi get https://quotes.toscrape.com/js/ --render --format text
gocurlffi open example.com --screenshot page.png
gocurlffi open example.com/login \
  --fill '#user=me' --fill '#pass=secret' --click 'button[type=submit]' \
  --wait '#account'
```

It can also be driven by real browser tooling over CDP and WebDriver BiDi, or
exposed to an agent over MCP:

```sh
gocurlffi serve            # ws://127.0.0.1:9222
gocurlffi mcp --port 9223  # http://127.0.0.1:9223/mcp
```

See [`browser/README.md`](../browser/README.md) for the full surface and
[`server/README.md`](../server/README.md) for the protocols.
