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
same nodes. Compiled CSS selectors are cached process-wide, because pages issue
thousands of `querySelector` calls and compilation dominates the cost.

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

- Document decoding: the Content-Type charset, a BOM or `<meta charset>` is
  honoured (via `x/net/html/charset`), so non-UTF-8 pages are not mojibake.
  A `<base href>` sets the base for relative URLs, used by script `src`,
  `href`/`src` properties, `fetch`, `XMLHttpRequest`, links and markdown.
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
- Web platform globals: `URL`/`URLSearchParams`, `TextEncoder`/`TextDecoder`,
  `AbortController`/`AbortSignal`, `Event`/`CustomEvent`,
  `IntersectionObserver` (reports observed elements as intersecting after
  load, so lazy content materialises), `MutationObserver`/`ResizeObserver`
  (inert stubs), `performance`, `crypto.getRandomValues`/`randomUUID`,
  `structuredClone`,
  `DOMParser`, `requestIdleCallback`, `customElements` (registry stub).
- `document.implementation.createHTMLDocument`/`createDocument`, `Range`
  (`createContextualFragment`), `createEvent`, `importNode`/`adoptNode`,
  `document.fonts`, `attachShadow` (returns the host). Sub-documents created
  through `DOMParser` or `implementation` keep their own tree: document
  methods operate on the receiver, not the page document.
- `<template>` content is inert, as in a browser: parsed children live in a
  detached fragment, so they never appear in queries, `Text()`, `Links()` or
  `Markdown()`, while `template.content` exposes them for cloning and
  `template.innerHTML` reads and writes that content. Serialization
  (`HTML()`, `outerHTML`) still includes template contents.
- `TreeWalker`/`NodeIterator` (`createTreeWalker`, `NodeFilter` constants) with
  working `nextNode`, `previousNode`, `parentNode` and sibling/first/last
  navigation, which is what text-extraction loops use.
- `window.postMessage` (delivers a `message` event to window listeners),
  `document.domain`, `document.open`/`close`/`hasFocus`, `getSelection`, and
  `webkitMatchesSelector`-style aliases.
- `window`, `document` (including `document.currentScript`, which bundlers use
  to resolve chunk paths), `navigator`, `location`, `console`, `history`,
  `localStorage`, `matchMedia`, `atob`/`btoa`.
- Classic scripts that use top-level `await` are retried wrapped in an async
  function, since some bundlers ship them as classic scripts.
- Recursion is capped like a browser engine (`Options.LoadTimeout` and a
  ~10000-frame call stack), so a polyfill loop throws `RangeError` instead of
  spinning forever, and a slow subresource cannot stall the load past the
  budget.
- Extraction helpers: `HTML()`, `Text()`, `Links()`, `Markdown()`.

Verified live:

| Site | Result |
| --- | --- |
| `quotes.toscrape.com/js/` | all 10 JS-rendered quotes materialise |
| `react.dev` | React hydrates and renders; no console errors |
| `www.gocomics.com` | Next.js bundle runs; full page renders |
| `www.foodnetwork.com` | 200, ~330 KB rendered |
| `bsky.app` | shell renders (~408 chars); its main chunk uses `for await` and stops |

Rendered through the default impersonation targets, via
`scripts/check_sites.sh -B`:

| Site | Target | Result |
| --- | --- | --- |
| `www.marriott.com` | `custom` | 200, ~1.1 MB rendered |
| `www.marriott.com` | `chrome131` | 403 |
| `www.ritzcarlton.com` | any | 200, ~330 KB |
| `mstdn.social` | any | 200, ~53 KB |
| `ubiqueros.com` | any | 200, ~10 KB |

Marriott behaves the same way in the browser as it does with the plain HTTP
client: `custom` is accepted where a plain Chrome fingerprint is challenged.
That is expected, because the browser renders through the same impersonating
transport, and it is a good check that the two stay consistent.

## Checking sites

`scripts/check_sites.sh -B` runs the same site/target matrix with the headless
browser instead of the plain HTTP client, and shows the rendered size per cell.
Comparing the two modes shows which sites actually need JavaScript:

```sh
bash scripts/check_sites.sh -B -i chrome131,custom
bash scripts/check_sites.sh -B -i chrome131 https://bsky.app/
```

## Not implemented

This is a browsing *core*, not a rendering engine. There is no layout, paint,
screenshot or PDF output, and no image decoding. Specifically absent:

- ES modules (`<script type="module">`, `import`/`export`) are skipped.
- The JavaScript engine has no async generators or `for await (... of ...)`
  (goja reports "Async generators are not supported yet"). Bundles that rely
  on them, such as the Bluesky web app's main chunk, stop at that point even
  though the page still loads. Async functions and top-level `await` work.
- No iframe browsing context: `iframe.contentWindow`/`contentDocument` return
  the page's own window and document, so scripts that reach into a frame stop
  throwing, but embedded frame documents are never fetched or parsed. Frame
  documents are therefore always reported as same-origin.
- CSS is not cascaded: no `getComputedStyle` computation, no `offsetWidth`.
- No service workers, Workers, WebSocket, `indexedDB`, WebAssembly, Canvas.
- `<template>` contents are moved out of the element at parse time, so
  appending a node directly to a template element puts it in the element
  (visible) rather than in `content`. Use `template.content` to build content.

Sites whose content is gated by a heavy framework and many chained dynamic
imports may not fully render, and absolute JS-engine parity with V8 is out of
scope. For most server-sent-then-rendered pages, and for probing JS challenges,
it is enough.

## Notes

- `browser/browser/` is the reference Lightpanda checkout and is gitignored. It
  is the source material for behaviour, not a build input.
- Scripts run in the same goroutine as `Open`. A runaway script is bounded by
  `Options.JavaScriptTimeout` (default 10s); the whole script-loading phase is
  bounded by `Options.LoadTimeout` (default 30s). After `DOMContentLoaded` and
  again after `load` the loader waits up to `Options.TimerBudget` (default 2s)
  for pending timers, so a timer-driven page can finish rendering.
- Rendering is much slower than the plain HTTP client by design: it issues one
  request per script (a large site can be 30-40), executes them in a pure-Go
  interpreter with no JIT, and re-serializes the DOM. Use `gocurlffi` when the
  HTML is server-rendered and `gobrowser` only when scripts are required.
- All network traffic, including `fetch`, `XMLHttpRequest` and external
  scripts, goes through one shared session, so a browser-like flow works
  across hosts (see the note on the ClientHello fix in the repository README).
- One `Browser` may be used from several goroutines: the session and its
  cookie jar are synchronized, so pages can be crawled in parallel. A single
  `Page` is not safe for concurrent use. The race detector is unavailable on
  android/arm64, so this is covered by construction plus a concurrency test
  (`TestConcurrentPages`) run repeatedly, not by `-race`.

### CLI flags

```sh
gobrowser get URL \
  -i chrome131          # impersonation target
  -f html|text|markdown|links
  -o FILE               # write output to FILE
  --eval 'JS'           # evaluate JS and print the result
  --wait SELECTOR       # wait for a selector before extracting
  --timeout 30s         # per-request timeout
  --load-timeout 30s    # script-loading budget per page
  --timer-budget 2s     # wait for pending timers after load (lower = faster)
  --no-js               # disable JavaScript
  --console             # print console.* to stderr
  --status              # print HTTP status to stderr
  --debug               # log page-load phases to stderr
  -H 'K: V'             # extra header (repeatable)
```
