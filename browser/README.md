# browser - a pure-Go headless browser

`gocurlffi/browser` is a headless browser for scraping and automation, written
entirely in Go. It is the Go answer to the Zig [Lightpanda
browser](https://github.com/lightpanda-io/browser) checked out in
`browser/browser/` as a reference.

The point of the port is portability. Lightpanda embeds V8, BoringSSL, curl,
brotli, nghttp2, sqlite and PCRE2, so it needs a Zig toolchain and per-platform
native builds. This package uses only pure-Go dependencies, so it cross-compiles
anywhere Go does, including Android/Termux on `arm64`.

## How it is built

| Concern | Choice | Why |
| --- | --- | --- |
| HTML5 parse + serialize | `golang.org/x/net/html` | conformant, pure Go |
| CSS selectors | `github.com/andybalholm/cascadia` | querySelector/querySelectorAll |
| JavaScript | `github.com/dop251/goja` | ECMAScript engine, pure Go |
| Network | `gocurlffi/requests` | reuses the impersonating TLS/HTTP transport |
| DOM | the `x/net/html` node tree itself | small, and the parsed nodes are the ones scripts mutate |

The DOM *is* the `*html.Node` tree. There is no separate wrapper model, so
`html.Parse`, script mutation, CSS matching and serialization all operate on the
same nodes.

Network requests go through `gocurlffi/requests`, so the document, external
scripts, `fetch()` and `XMLHttpRequest` all carry the selected browser's
TLS/JA3, HTTP/2 fingerprint and default headers.

## Usage

```go
b := browser.New(browser.Options{Impersonate: "chrome131"})
defer b.Close()

p, err := b.Open("https://quotes.toscrape.com/js/") // scripts run during load
if err != nil {
    panic(err)
}

fmt.Println(p.Text())                    // rendered text
fmt.Println(p.Query("h1").FirstChild.Data)
for _, l := range p.Links() { fmt.Println(l.Href, l.Text) }
```

Lower-level control:

```go
b := browser.New(browser.Options{RunScripts: &off})
p := b.NewPage("https://example.com/")
_ = p.SetContent(html, "https://example.com/") // no network
_ = p.Load("https://example.com/other")
v, _ := p.Eval("document.querySelectorAll('.quote').length")
p.WaitForSelector(".loaded", 5*time.Second)
```

CLI:

```sh
make gobrowser
./bin/gobrowser get https://react.dev/ -f markdown -i chrome131
./bin/gobrowser get https://quotes.toscrape.com/js/ --eval 'document.title'
./bin/gobrowser get https://example.com/ -f text --status
```

## What works

- HTML5 parsing and a mutable DOM: `createElement`, `appendChild`,
  `insertBefore`, `innerHTML`/`outerHTML`, `textContent`, attributes,
  `classList`, `dataset`, `style`, `cloneNode`.
- CSS queries: `querySelector(All)`, `getElementById`, `getElementsBy*`,
  `matches`, `closest`.
- Events: `addEventListener`/`removeEventListener`/`dispatchEvent` with
  bubbling, `DOMContentLoaded` and `load` lifecycle, `click()`.
- Timers: `setTimeout`, `setInterval`, `requestAnimationFrame`, drained
  after load (bounded by a 2s budget).
- `fetch()` and `XMLHttpRequest`, both through the impersonating transport,
  returning `Promise`s that resolve after a synchronous request.
- `window`, `document`, `navigator`, `location`, `console`, `history`,
  `localStorage`, `matchMedia`, `atob`/`btoa`.
- Extraction helpers: `HTML()`, `Text()`, `Links()`, `Markdown()`.

Verified live: `quotes.toscrape.com/js/` (JS-rendered quotes fully materialise)
and `react.dev` (the React bundle hydrates and renders; `fetch`, `matchMedia`
and external scripts all load).

## Not implemented

This is a browsing *core*, not a rendering engine. There is no layout, paint,
screenshot or PDF output, and no image decoding. Specifically absent:

- ES modules (`<script type="module">`, `import`/`export`) are skipped.
- CSS is not cascaded: no `getComputedStyle` computation, no `offsetWidth`.
- No service workers, Workers, WebSocket, `indexedDB`, WebAssembly, Canvas.

Sites whose content is gated by a heavy framework and many chained dynamic
imports may not fully render, and absolute JS-engine parity with V8 is out of
scope. For most server-sent-then-rendered pages, and for probing JS challenges,
it is enough.

## Notes

- `browser/browser/` is the reference Lightpanda checkout and is gitignored. It
  is the source material for behaviour, not a build input.
- Scripts run in the same goroutine as `Open`, so a runaway script is bounded
  by `Options.JavaScriptTimeout` (default 10s).
