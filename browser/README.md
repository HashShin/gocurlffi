# browser - a pure-Go headless browser

`gocurlffi/browser` is a headless browser for scraping and automation, written
entirely in Go. It is the Go answer to the Zig
[Lightpanda browser](https://github.com/lightpanda-io/browser); how much of
Lightpanda's web API surface it covers is measured in
[`../docs/parity.md`](../docs/parity.md).

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
make build
./bin/gocurlffi get https://react.dev/ --render -f markdown -i chrome131
./bin/gocurlffi get https://quotes.toscrape.com/js/ --render --eval 'document.title'
./bin/gocurlffi open https://example.com/ -f text --status
```

`open` is shorthand for `get --render`. Every flag is listed in
[`../docs/cli.md`](../docs/cli.md), or run `gocurlffi help open`.

## What works

- CSS: `<style>` blocks and `<link rel=stylesheet>` sheets are fetched and
  cascaded (specificity, `!important`, source order, inheritance, `@import`,
  `@media`, custom properties, a user-agent default sheet), and drive both
  `getComputedStyle` and the screenshot renderer. Loading is lazy: loading a
  page or extracting text never touches stylesheets; the first style-dependent
  operation fetches them once and caches them.
- The page's own stylesheets, and nothing else: every `<style>` element and
  every `<link rel=stylesheet>` the document contains, in document order, with
  relative and root-relative (`/assets/main.min.<hash>.css`) hrefs resolved
  against the page, whatever `integrity` or `crossorigin` attributes they carry.
  A sheet added by a script is picked up too, because the style caches are
  invalidated when the DOM changes. `Page.StyleSheets` and `--sheets` report
  each one with its size and rule count, or the reason it was not applied:

  ```sh
  ./bin/gocurlffi get --render https://brave.com/ --sheets
  applied      https://brave.com/static-assets/css/main.min.25ad059cc...css  2332 rules, 415249 bytes
  applied      https://brave.com/static-assets/css/fonts-latin.min.e...css     2 rules, 16677 bytes
  applied      inline <style>                                                  1 rules, 69 bytes
  ```

  The report goes to stderr and does not stop the command, so it composes:
  `get brave.com --screenshot page.png --width 900 --sheets` writes the PNG and
  lists the sheets it was rendered with.
- `document.styleSheets` (with `href`, `media`, `ownerNode` and `cssRules`,
  including each rule's `selectorText` and `style.getPropertyValue`), so a page
  can inspect its own CSS. Reading a sheet's URL never fetches it; reading its
  rules does.
- CSS custom properties and `var(--name, fallback)`, including inheritance and
  nested references. Modern sites theme everything this way: Wikipedia's body
  colour is `color: var(--color-base, #202122)` and Bootstrap 5 styles every
  component with `--bs-*`, so without `var()` such a page falls back to the
  user-agent defaults.
- `@media` is evaluated against the width being rendered: media types,
  `not`/`only`, `and`/`or` chains, `min-width`/`max-width` (and the `height`
  family, `orientation`, the `device-*` aliases, and the `(width >= 600px)`
  range form). A query the parser cannot understand does not match, because
  treating it as matching applies every mobile rule to a desktop page.
- Selectors written for real build tools: CSS escapes outside strings
  (`".font-\[\'Poppins\'\2c sans\]"`, `".\!text-\[14px\]"`,
  `".lg\:font-semibold"`), including class names with escapes cascadia compiles
  but never matches, which are matched by name instead. Without this a single
  escaped quote in brave.com's stylesheet made the parser swallow the remaining
  117KB: 1110 rules instead of 2332.
- The `font` shorthand in its real grammar, where the weight, style and variant
  precede the size and the size must carry a unit: `font: 600 14px/22px
  system-ui` sets weight 600, not a 600px font. This is how Leo (brave.com's
  design system) sets its buttons, through a custom property.
- `font-weight` keeps its number, so `font-semibold` reports 600 and
  `font-medium` 500, and `bolder`/`lighter` step through the CSS weight table.
- Blockification for flex and grid items as well as for floats and positioned
  boxes, so a `<span>` inside a flex container reports `display: block`.
- User-agent form controls by type: a text field and textarea are white on
  black, a button and select keep the platform face (`#efefef`) and centre their
  label, a checkbox or radio is transparent.
- HTML presentational attributes (`bgcolor`, `color`, `align`) act as
  author-origin hints below the cascade, which is how old table layouts paint
  themselves, and a page without a doctype is styled in quirks mode (tables
  take the medium font size instead of inheriting).
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
- Script-driven navigation: `location.assign`, `location.replace`,
  `location.reload`, assigning `location.href`, and assigning `window.location`
  all load the new document and swap in a fresh JavaScript environment. A
  relative URL is resolved against the current document. The load is deferred
  until the running script phase has finished, so the statement after the call
  still runs against the old document, and the chain is bounded so a page that
  redirects on every load cannot spin. History entries are not modelled, so
  `assign` and `replace` behave alike. This matters for handoff pages that
  finish a JavaScript challenge and then send the browser on with
  `location.replace(...)`.
- Classic scripts that use top-level `await` are retried wrapped in an async
  function, since some bundlers ship them as classic scripts.
- Recursion is capped like a browser engine (`Options.LoadTimeout` and a
  ~10000-frame call stack), so a polyfill loop throws `RangeError` instead of
  spinning forever, and a slow subresource cannot stall the load past the
  budget.
- Extraction helpers: `HTML()`, `Text()`, `Links()`, `Markdown()`.
- Element geometry: `getBoundingClientRect`, `getClientRects`, `offsetWidth`/
  `offsetHeight`/`offsetTop`/`offsetLeft`, `clientWidth`/`clientHeight` and
  `document.elementFromPoint` answer from the layout, not zeroed stubs. The
  layout is computed at most once per document revision and reused, so a script
  that measures many elements pays for one layout.
- XPath: `Page.QueryXPath`/`QueryXPathFirst`/`XPath` and `document.evaluate`
  with `XPathResult` (node iterators, snapshots, number, string and boolean
  types). Compiled expressions are cached process-wide.
- Page actions: `Click`, `ClickPoint`, `Type`, `Fill`, `Select`, `Check`,
  `Focus`, `Press` and `Scroll`, dispatching the events a browser would. They
  are the shared layer the CLI, the CDP Input domain and the BiDi input module
  call, and clicks use the geometry centre.
- iframes: each `<iframe>` is loaded as a child page with its own document and
  JavaScript environment, sharing the browser's cookies. `Page.Frames()`,
  `iframe.contentDocument`/`contentWindow` and extraction across the frame tree
  (`Text`, `Links`, `Markdown`) all work. Nesting is bounded.
- Network interception and proxy: `Options.Intercept` is offered every request
  (document, script, stylesheet, image, font, fetch, XHR) and may continue,
  block or fulfil it. `Options.Proxy` routes every request through a proxy.
- `robots.txt`: `Options.ObeyRobots` fetches and caches each origin's
  robots.txt and skips a disallowed URL with a synthetic 403.
- CORS (`Options.CORS`, off by default): cross-origin fetch/XHR carries an
  `Origin` header, runs a preflight when it is not simple, and rejects a
  response without a matching `Access-Control-Allow-Origin`.
- ES modules: `<script type="module">`, static `import`/`export` (default,
  named and namespace forms), re-exports, side-effect imports, an import map
  for bare specifiers, and dynamic `import()`, all loaded and evaluated in
  dependency order.
- A Chrome DevTools Protocol server (`gocurlffi serve`) and WebDriver BiDi on
  the same port, so Puppeteer, Playwright and chromedp can drive it.
- An MCP (Model Context Protocol) tool server (`gocurlffi mcp`) over stdio or
  HTTP, exposing navigate, get_content, evaluate, screenshot, click, type and
  structured_data to an AI agent.
- Structured data: `Page.StructuredData()` returns every JSON-LD object the
  document declares, and `Page.Microdata()` the itemscope/itemprop graph.
- An ad and tracker blocker (`Options.Adblock`): well-known advertising and
  analytics hosts and paths are dropped, with the caller's Intercept hook still
  able to override it.
- PDF output: `Page.PDF` renders the page and embeds the image in a one-page
  PDF, the same class of output the screenshot produces.
- Canvas: `getContext('2d')` implements the drawing calls pages use (fills,
  strokes, text, paths, `drawImage`, `clearRect`) over an `image.RGBA`, and
  `toDataURL` returns a PNG. Charts and generated images come out drawable.
- `WebSocket`: a real client over the same pure-Go library the CDP server uses.
  Incoming messages are delivered as `message` events while the page's task
  queue is drained, so a handler sees a reply sent in `onopen`.
- Web Workers: `new Worker(url)` runs the script in its own goja runtime on its
  own goroutine, with `postMessage` both ways and `importScripts`.
- IndexedDB: an in-memory `indexedDB` with `open` and an upgrade, object stores
  (keyPath, autoIncrement), `put`/`add`/`get`/`getAll`/`delete`/`clear`/`count`,
  simple indexes, `openCursor`, and transactions whose requests resolve on a
  later turn.
- Bodies and streams: `Blob`, `File`, `FileReader`, `FormData`, `Headers`,
  `Request`, `Response`, `ReadableStream`, `WritableStream` and
  `TransformStream` are real implementations over byte slices, not just names.
  `new Blob(['hi']).size` is 2, `Array.from(new FormData(...))` iterates,
  `new FormData(form)` collects a form's successful controls, a `FormData`
  holding a file is sent as multipart and one without is urlencoded, and
  `fetch(...).body.getReader()` drains the response.
- `MutationObserver` actually fires: childList, attributes (with
  `attributeOldValue`), characterData and `subtree` are recorded at every DOM
  mutation point, batched, and delivered at the microtask checkpoint -- after a
  script, a timer callback, an event or an eval. `disconnect` and `takeRecords`
  behave, so a framework that mounts into a mutation callback works.
- `IntersectionObserver` and `ResizeObserver` report each observed target once
  at the next checkpoint, with rectangles read from the real layout. Because
  there is no viewport to scroll, an observed target is reported as
  intersecting; that is what reveals lazy-loaded content.
- Shadow DOM: `attachShadow` returns a real `ShadowRoot` (with `innerHTML`,
  `querySelector`, `appendChild`, `getElementById`), `element.shadowRoot`
  returns it for `open` roots and `null` for `closed`, and `getRootNode()`
  answers the shadow root a node lives in. Declarative shadow DOM
  (`<template shadowrootmode="open">`) is attached at parse time, so a
  server-rendered component is visible even with `--no-js`. Extraction reads
  through the shadow boundary -- `Text`, `Markdown`, `Links`, `HTML` and the Go
  query helpers include shadow content, substituting a `<slot>` with the host's
  light children so nothing is duplicated. The DOM's own
  `document.querySelector` still stops at the boundary, as the spec requires.
- `history.pushState`/`replaceState` rewrite the document URL in place, so
  `location.pathname` and relative URL resolution follow an SPA route, and
  `back`/`forward`/`go` walk the entries those calls created and fire
  `popstate`. `location` reads through to the live URL rather than a copy taken
  at load.
- Custom elements work. `customElements.define` upgrades the elements already in
  the document, every later `createElement`/parse/insert upgrades too, and
  `constructor`, `connectedCallback`, `disconnectedCallback` and
  `attributeChangedCallback` (driven by the static `observedAttributes`) all
  fire. `get`, `upgrade` and `whenDefined` behave. The element object the page
  receives is the constructed instance, so a component's own methods are
  callable on it, and an element the page already held keeps its identity across
  the upgrade. A constructor may call `this.attachShadow(...)`:
  `document.querySelector` still stops at the boundary, but extraction reads
  through it, so a component's content reaches `Text`, `Markdown` and `HTML`.
  Two limits: callback timing is not modelled, so lifecycle and attribute
  callbacks run synchronously instead of being queued on a microtask, and only
  autonomous custom elements are supported (`define(name, ctor, {extends:...})`
  for a customized built-in is ignored), with no `:defined` CSS.
- `AbortSignal` is real. `abort()` updates `aborted`/`reason`, fires the `abort`
  event to `addEventListener` and `onabort` listeners, and propagates to signals
  derived with `AbortSignal.any`; `AbortSignal.timeout` schedules an abort on the
  page's own timer queue. A `Request` carries the controller's own signal, so
  `request.signal.aborted` follows `controller.abort()`, and `fetch` rejects an
  already-aborted signal before sending. A request in flight cannot be
  interrupted, because the loader is synchronous.
- `DOMParser` honours its type argument. `text/xml`, `application/xml`,
  `application/xhtml+xml` and `image/svg+xml` parse as XML through
  `encoding/xml` and yield an `XMLDocument` whose `documentElement` is the XML
  root, with case preserved (`tagName`, `localName`) as XML requires; anything
  else parses as HTML. Malformed XML returns a `<parsererror>` document rather
  than throwing, as a browser does.
- `structuredClone` is a real structured clone rather than a JSON round-trip:
  `Map`, `Set`, `Date`, `RegExp`, `ArrayBuffer`, typed arrays and cycles all
  survive, and a nested `Map` no longer comes back as a plain object.
- The Service Worker surface is stubbed for feature detection
  (`register` resolves, `controller` is null); there is no persistent worker.

### MCP server

`gocurlffi mcp` speaks MCP JSON-RPC 2.0 over stdio, and over HTTP with
`--port`. Each HTTP session gets its own browsing context -- its own page,
cookies and memory -- so several agents can share one server without clobbering
each other. A client that `initialize`s without an `Mcp-Session-Id` header is
assigned one, and the id comes back in the response; send it on later requests
to stay on that session, or send the same id from two clients to deliberately
share one page. `DELETE /mcp` with the header closes a session, and the
`session_new`, `session_list` and `session_close` tools manage them explicitly.
Over stdio there is one implicit session, because the client owns the process.

Point an MCP client at it:

```json
{
  "mcpServers": {
    "gocurlffi": { "command": "/path/to/gocurlffi", "args": ["mcp"] }
  }
}
```

The tools are `navigate`, `get_content` (text/markdown/html/links), `evaluate`,
`screenshot`, `click`, `type` and `structured_data`. Each session keeps one
current page; a tool that needs a page and has none says so rather than
failing.
- `Screenshot`, a text-layout PNG renderer (see below).

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

## Speed against a real Chromium

`scripts/bench_browser.sh` loads the same pages with `gocurlffi` and with a
Chromium CLI (`--headless=new --dump-dom`) and reports wall time and rendered
bytes. Measured on Termux/arm64 against Chromium 149:

| Page | Chromium | gocurlffi | Chromium bytes | gocurlffi bytes |
| --- | --- | --- | --- | --- |
| `about:blank` | ~0.95 s | - | 40 | - |
| `quotes.toscrape.com/js/` | 3.7 / 2.6 s | **2.0 / 1.9 s** | 8 987 | 9 005 |
| `react.dev` | 3.6 / 2.2 s | 3.6 / 3.8 s | 272 527 | 272 744 |
| `gocomics.com` | 26.1 / 8.0 s | **8.7 / 8.2 s** | 3 078 797 | 1 575 337 |
| `foodnetwork.com` | 23.5 / 23.5 s | **3.5 / 3.7 s** | 625 985 | 340 452 |

Reading it honestly:

- gocurlffi is faster on every page tested, and several times faster on heavy
  pages, because there is no browser startup (~1 s before Chromium even starts
  loading) and no per-page process to launch.
- On `react.dev` the two are level: that much JavaScript interpreted without a
  JIT costs about what Chromium spends starting up and compiling.
- Chromium renders **more** bytes on heavy pages (roughly 2x on gocomics). It is
  a real browser: it runs more scripts to completion and normalizes the DOM. So
  gocurlffi being faster is partly "does less".

Caveats, so the numbers are not over-read:

- Chromium needs a warmed `--user-data-dir` here; its first run with a cold
  profile hangs. The warmup is untimed, and timed runs hit a URL the profile has
  not cached, which is the closest match to gocurlffi's always-cold behaviour.
- Chromium is bounded with `--virtual-time-budget` because `--dump-dom` never
  returns on pages that do not reach network idle. That budget is virtual, not
  wall-clock, so its time is a floor rather than a full time-to-interactive.
- Chromium's launcher ignores `SIGTERM`, so the script bounds it with
  `timeout -k`.

```sh
bash scripts/bench_browser.sh -n 3
bash scripts/bench_browser.sh https://your-site.example/
GOCURLFFI_ARGS="--timer-budget 0s" bash scripts/bench_browser.sh
```

## Screenshots

`Page.Screenshot` renders the page to a PNG, mirroring what Lightpanda's
screenshot does: it applies the page's CSS first, then flows the document at a
given width and draws headings, paragraphs, lists with markers, preformatted
blocks, blockquotes, rules, and styled runs (bold, italic, monospace, links,
underlines, inline colors), with flat block background colours and
`text-align`. The page's own web fonts are used too: a run whose computed
`font-family` matches an `@font-face` is drawn with the page's file, at the
closest weight and slope, instead of an embedded one.

Verified against a live page rather than by eye: on
`quotes.toscrape.com/js/` the cascade reproduces that site's own CSS exactly -
`.quote span.text` at 19.2px italic (`font-size: large`), `.quote small.author`
at weight 700 in `#3677E8`, `.quote` with 30px bottom margin and 10px padding,
and `body` in `sans-serif`. Applying it grows the render from 1100px to 1496px,
which is that CSS's margin and padding taking effect.

### Rendering model

A browser parses HTML into a DOM and CSS into a rule set, cascades the rules to
a computed style per element, builds a box tree, lays it out (block and inline
formatting contexts, replaced elements, margin collapsing) and paints. This
renderer follows that order for the parts it implements: the cascade produces a
computed style, the collector turns the DOM into an ordered list of blocks
(element boxes, text runs and replaced boxes), the layout flows them at a width,
and the painter rasterizes the draw list. `getComputedStyle` answers from the
cascade, so what it reports is what a render uses.

The user-agent defaults follow the HTML rendering spec: em-relative heading
sizes and margins, 1em paragraph/list/blockquote margins, 40px list indentation,
monospace for `pre`/`code`, `mark`, `s`/`del`, `sub`/`sup`, form-control faces
and so on, all overridable by author CSS through the cascade. Vertical margins
do not stack: adjacent margins collapse to the larger of the two, and padding
never collapses, as in a browser.

## Checking the cascade against a browser

`tools/cssdiff` compares this package's computed styles with a real Chromium,
element by element, on the same page: Chromium answers over CDP, the Go browser
answers through `gocurlffi get --render --eval`, and the two are matched by DOM position.
It is a separate module, so its one dependency (chromedp) never reaches the
browser package.

```sh
cd tools/cssdiff && go mod download && go run . https://news.ycombinator.com/
```

Every cascade fix here was found with it. Current agreement, 7 properties
(font-size, colour, background, weight, style, display, alignment) over every
element both engines produce:

| page | elements compared | agree |
| --- | --- | --- |
| `news.ycombinator.com` | 817 | 7/7 properties, 0 differences |
| `quotes.toscrape.com/js/` | 109 | 7/7 properties, 0 differences |
| `brave.com` | 959 | font-size 1.1%, colour 1.6%, weight 0.0%, style 0.0%, display 1.1%, alignment 0.0%, background 5.0% |
| `en.wikipedia.org` (Go article) | 6703 | font-size 0.3%, colour 0.0%, background 0.5%, weight 0.5%, style 0.0%, display 2.5%, alignment 0.3% |

The remaining Wikipedia differences are mostly `display: flex`/`flow-root`
style rules behind `:is()`/`:has()` selectors that cascadia cannot compile, and
grid layout the renderer does not use.

```go
png, err := p.Screenshot(browser.ScreenshotOptions{Width: 1280, Scale: 2})
```

```sh
./bin/gocurlffi get --render https://quotes.toscrape.com/js/ --screenshot page.png --width 900
```

`--debug` reports what the styling actually did, which is the first thing to
check when a page looks unstyled: every stylesheet it fetched and its size, any
sheet it could not fetch, the rules each one produced, and the unsupported
selector samples.

```
[browser] stylesheet: 125934 bytes from https://quotes.toscrape.com/static/bootstrap.min.css
[browser] stylesheet: 1368 bytes from https://quotes.toscrape.com/static/main.css
[browser] page stylesheets: 2 declared, 2 applied
[browser] stylesheet 0: 1883 rules, 345/2228 selectors unsupported
[browser] style engine: 5356 rules at width 900
[browser] page fonts: 8 @font-face rules
```

Only what the page itself declares is used: every `<style>` element and every
`<link rel=stylesheet>`, in document order, fetched from the URL the page gives.
Nothing is injected, and nothing is substituted - when a line above names
`bootstrap.min.css` that is a sheet the site itself links. Every declaration is
accounted for: applied with its size, or skipped with the reason (disabled,
`media` that does not match, a `<link>` without `href`) or reported as a failed
fetch.

| Option | Default | Meaning |
| --- | --- | --- |
| `Width` | 1280 | layout width in CSS px, clamped to 320..4096 |
| `Scale` | 1 | output multiplier (`2` gives a 2x image) |
| `MaxHeight` | 20000 | height cap; longer content is cropped |

It is deliberately the same class of renderer as the Zig reference, with the
same honest limits:

- The page's pictures are drawn: `<img>` and `srcset` (the largest candidate),
  PNG, JPEG, GIF and WebP, scaled to their box with a high-quality filter.
  Bounded and optional: at most 48 images and 16MB per render, fetched six at a
  time, and `NoImages` / `--no-images` turns it off. An image with a CSS width
  or height is drawn at that size; otherwise its natural size, scaled down to
  the column. `background-image` and gradients are not painted, so a page that
  is mostly CSS art still renders as text. On `brave.com` a 900px render goes
  from 3s and 2,072 distinct colours without images to 9s and 97,802 with them,
  which is what real photographs look like.
- Inline SVG icons are rasterized with a pure-Go renderer (oksvg + rasterx) and
  drawn at the element's CSS size, with `currentColor` following the computed
  text colour. Icons and images flow as inline replaced boxes, so one can sit
  next to text on a line rather than forcing a line break.
- Native form controls are drawn, not left blank: checkbox and radio (with
  their checked state), range, and color swatch show a graphic, while text
  inputs, textareas, buttons, file and select show their value, placeholder or
  selected option inside a box. A control's uniform border and `min-height` are
  drawn, so an empty textarea is still the tall box the page asked for. Text is
  centered inside a button, and a column flex container's `align-items: center`
  centers its children.
- Positioning: `position: relative`/`sticky` keeps the element in flow (its
  inset offsets shift it), and `absolute`/`fixed` take it out of flow. An
  absolute/fixed box is placed from its nearest positioned ancestor using
  `left`/`top`/`right`/`bottom` (and the `inset` shorthand), sized by its
  `width` or shrink-to-fit, and painted above the flow. `z-index` values are
  recorded but stacking is approximated by document order.
- Still no float and no full containing-block chain: an absolute element with no
  positioned ancestor uses the page origin, and `fixed` is placed like
  `absolute` (a full-page screenshot has no scroll). CSS is otherwise
  cascaded for typography, colour, display, spacing, alignment, backgrounds and
  borders.
- Element decoration: a block with a `background-color`, `border` or
  `border-radius` is drawn once as a rectangle (rounded when it has a radius,
  with anti-aliased corners) rather than per line, and nested boxes paint
  parent-first. `opacity` scales the element's own colours, and `box-shadow`
  (the first shadow) draws as a soft rectangle behind the box. `transform` and
  `filter` are not drawn.
- Block sizing: `width`, `max-width`, `max-height`, `height` (as a minimum) and
  `box-sizing: border-box` are applied, plus right margins and padding, and
  `margin: 0 auto` centers a sized block. `max-height` clips the overflowing
  lines, like `overflow` on a scroll box. A sizing context is inherited by the
  subtree, so `max-width:1100px;margin:0 auto` centers a whole page column, not
  just the element's own text.
- `display: flex` rows are laid out: children sit side by side, sized from
  `flex-grow`, `flex-basis` (including `calc(50% - 7px)`) or their content, with
  `gap`, `flex-wrap`, `justify-content`, `align-items` and nested rows. A column
  flex container stacks its children (and honors `align-items` horizontally);
  `display: grid` still stacks, and a row is not a full flexbox: baseline
  alignment, margins on items and ordered/reverse directions are not modelled.
- Lengths include `clamp()`, `min()` and `max()`, and the `vw` unit, resolved
  against the layout width. Vertical and left margins and padding are applied as
  flow space; `padding-top`/`padding-bottom` push the content down and add space
  after it, even inside a block whose first child is another block.
- `text-transform` (uppercase, lowercase, capitalize) and `letter-spacing` are
  applied to the drawn text and its measured width. `rgba()` backgrounds and
  text are composited over what is behind them, so a translucent surface on a
  dark page stays dark instead of coming out near-white, and the PNG is opaque.
- Selectors cascadia cannot compile are skipped, and the engine counts them:
  `--debug` prints how many rules each sheet produced and lists the first
  unsupported selectors. On a real site most of those are `::before`/`::after`
  and vendor pseudo-elements, which carry no element styling, so the number
  overstates the loss. `:is()`, `:where()` and `:has()` are not supported.
- Web fonts are applied from the page's own `@font-face` rules, including font
  providers reached through `<link>` or `@import`: a matching run is drawn with
  the page's file rather than an embedded one, at the closest weight and slope.
  Only raw sfnt (TTF and OTF) is decoded. A face offered only as WOFF or WOFF2
  is recorded and reported under `--debug`, then falls back to the embedded Go
  fonts (Go regular/bold/italic and Go Mono), and there is no synthetic bolding
  or oblique.
- Not pixel-identical to a browser: glyph metrics differ from the browser's own
  text shaping, and layout is still the text-flow model below.

**Cost, measured on Termux/arm64:** loading is unaffected, because font parsing
is behind a `sync.Once`, faces are built lazily and nothing runs unless
`Screenshot` is called. A `quotes.toscrape.com` render at 900px adds only a few
milliseconds; a tall 900x3500 page takes ~115 ms, most of it PNG encoding and
pixel work rather than layout.

## Measuring layout against a live page

Computed styles are half of it; where a box ends is the other half, and both
engines have to be asked the same question.

- `Page.RenderOutline` prints the lines of text, then the boxes the layout
  drew as `box x= y= w= h= id=`, carrying the same rectangle a
  `getBoundingClientRect` would. Box lines only cover elements that paint
  something, so give an element a background to see its rectangle.
- Chromium answers through `tools/cssdiff/probe -js EXPRESSION`, which runs
  the expression in the page and prints the result.

For a page-level check, `tools/cssdiff/accept.html` is a fixture with a grid
of named areas, a padded flex row, a float and wrapping text. Rendering it in
both engines and comparing the PNGs row by row is the acceptance test:

```sh
./bin/gocurlffi get --render http://127.0.0.1:8000/accept.html --screenshot ours.png --width 800
cd tools/cssdiff && go run ./probe -url http://127.0.0.1:8000/accept.html -width 800 -out chrome.png -js '0'
```

As of this session the two agree pixel for pixel down to the paragraph beside
the float; the only differing rows are the free text, where the embedded face
is about 10% narrower than Chromium's DejaVu for lowercase (see "What works").

Two traps, both of which cost real time here:

- A live page's total height drifts. `bootstrap.com` moves about 180 pixels
  between runs on its own, so comparing a sweep with one from an hour ago is
  not evidence. Compare the same pages in one session, or better, in one run:
  a change was once reverted on a 178-pixel "regression" that turned out to be
  the page moving, not the change.
- A fixture that copies a live page must rewrite its stylesheet `href`s to
  absolute URLs, or neither engine loads any CSS and the comparison is
  meaningless. A fixture that measures an element's *declared* size must also
  put a block before it: as a container's first block, the container's own
  height lands on the element as a minimum and its declaration cannot be seen.

## Driving it as a browser (CDP and WebDriver BiDi)

`gocurlffi serve` starts a Chrome DevTools Protocol server on `--host`/`--port`
(default `127.0.0.1:9222`), so an existing automation client drives this browser
the way it drives Chrome:

```sh
./bin/gocurlffi serve --port 9222
# then, from Puppeteer:
#   puppeteer.connect({ browserWSEndpoint: "ws://127.0.0.1:9222" })
```

It serves the discovery endpoints (`/json/version`, `/json/list`, `/json/new`)
and a browser-level WebSocket (`/devtools/browser/<id>`) plus a per-page one
(`/devtools/page/<id>`). The implemented domains are `Browser`, `Target`,
`Page`, `Runtime`, `DOM`, `Network` (enable/cookies) and `Input`, enough for
navigation, `Runtime.evaluate`, a DOM query, an interaction and
`Page.captureScreenshot`. A client that asks for a method outside that set gets a
protocol error rather than a hang.

`Page.addScriptToEvaluateOnNewDocument` and `Network.setUserAgentOverride` are
implemented, not accepted-and-ignored. An init script is evaluated in the page's
JavaScript environment once the document exists but before any of the page's own
scripts run, so an injected patch is in place before page code can read
`navigator`; `Page.removeScriptToEvaluateOnNewDocument` revokes one from the next
document on. This matters because Playwright's `addInitScript` and Puppeteer's
`evaluateOnNewDocument` are built on that method, and a client whose setup script
is silently dropped does not fail - it just runs unpatched.

WebDriver BiDi is served on the same port at `/session`, sharing the target and
page layer: `session.new`/`status`, `browsingContext.create`/`navigate`/`getTree`/
`close`/`captureScreenshot`, `script.evaluate`/`callFunction` and
`input.performActions`. Both protocols are always served together.

The server only costs anything when it runs. A plain `Open` and extract never
touches it. The routes, the implemented domains and the MCP tools are documented
in [`../server/README.md`](../server/README.md).

Verified against a real client: `scripts/check_puppeteer.sh` starts the server
and a local page, then runs `scripts/check_puppeteer.mjs`, which connects with
`puppeteer-core`, opens a page, navigates, reads `document.title`, evaluates an
expression and takes a screenshot. It prints:

```
{"title":"Puppeteer Check","h1":"hello cdp","screenshotBytes":4356}
```

## Checking sites

`scripts/check_sites.sh -B` runs the same site/target matrix with the headless
browser instead of the plain HTTP client, and shows the rendered size per cell.
Comparing the two modes shows which sites actually need JavaScript:

```sh
bash scripts/check_sites.sh -B -i chrome131,custom
bash scripts/check_sites.sh -B -i chrome131 https://bsky.app/
```

## Not implemented

This is a browsing core with a document renderer, not a full web rendering
engine. `Screenshot` applies a large subset of CSS (see the limits above) but is
not a browser, and the gaps below remain. Specifically absent:

- The JavaScript engine has no async generators or `for await (... of ...)`
  (goja reports "Async generators are not supported yet"). Bundles that rely
  on them, such as the Bluesky web app's main chunk, stop at that point even
  though the page still loads. Async functions and top-level `await` work.
- A frame's `contentWindow` is a thin window shape over the shared DOM nodes,
  because a frame runs in its own JavaScript runtime; a script cannot call a
  function the frame defined on its own window.
- There is no native agent mode (an LLM driving the browser in-process); the
  MCP server lets an external agent drive it instead.
- No WebAssembly (no engine). Service Workers are feature-detection only: there
  is no persistent, background worker.
- `<template>` contents are moved out of the element at parse time, so
  appending a node directly to a template element puts it in the element
  (visible) rather than in `content`. Use `template.content` to build content.

Known approximation: when a container holds a single block child, the engine
merges the container's padding onto that one block, so the parent and child can
report the same rectangle from `getBoundingClientRect`; the parent's own height
is still correct. Hit testing and a click's centre are unaffected in the common
case (an element with its own content, or its own background box).

Sites whose content is gated by a heavy framework and many chained dynamic
imports may not fully render, and absolute JS-engine parity with V8 is out of
scope. For most server-sent-then-rendered pages, and for probing JS challenges,
it is enough.

## Notes

- No Lightpanda checkout is kept in this repository. `../docs/parity.md` records
  what is still missing from it, measured by probing, so the reference is not
  needed in-tree to plan the remaining work. Clone it yourself if you want to
  re-run the probe; the instructions are at the top of that file.
- Scripts run in the same goroutine as `Open`. A runaway script is bounded by
  `Options.JavaScriptTimeout` (default 10s); the whole script-loading phase is
  bounded by `Options.LoadTimeout` (default 30s). After `DOMContentLoaded` and
  again after `load` the loader waits up to `Options.TimerBudget` (default 2s)
  for pending timers, so a timer-driven page can finish rendering.
- Rendering is much slower than the plain HTTP client by design: it issues one
  request per script (a large site can be 30-40), executes them in a pure-Go
  interpreter with no JIT, and re-serializes the DOM. Use `gocurlffi get` when
  the HTML is server-rendered and `gocurlffi get --render` only when scripts are
  required: they are the same binary, so the fast path is a flag away, but
  linking the browser is what makes the binary about 37 MB instead of 18 MB.
- All network traffic, including `fetch`, `XMLHttpRequest` and external
  scripts, goes through one shared session, so a browser-like flow works across
  hosts. Every request carries the impersonation target selected at `New`, not
  just the first one.
- One `Browser` may be used from several goroutines: the session and its
  cookie jar are synchronized, so pages can be crawled in parallel. A single
  `Page` is not safe for concurrent use. The race detector is unavailable on
  android/arm64, so this is covered by construction plus a concurrency test
  (`TestConcurrentPages`) run repeatedly, not by `-race`.
- The parity features are opt-in and lazy, so they do not slow the plain
  `Open` -> extract path: geometry, XPath and the actions only run when called
  (and a geometry layout is cached until the DOM changes); interception, proxy,
  robots and CORS only act when their option is set; `serve` only costs while
  it runs; iframes and ES modules only cost on pages that contain them.

## Waiting for a page

A load runs scripts, fires `DOMContentLoaded` and `load`, then waits up to
`--timer-budget` (default 2s) for pending timers. That covers most pages, and
the waits below are for the rest:

- `--wait-until load` (default) is the behaviour above.
- `--wait-until domcontentloaded` stops right after the `DOMContentLoaded`
  event, before the timer budget and before child frames. A handler that renders
  on that event still runs; a page that needs a later turn does not. On a
  timer-driven page this roughly halves the load time, which is the point.
- `--wait-until networkidle0` keeps draining after load until nothing has been
  pending for 500ms. A page that holds a socket open legitimately never goes
  idle, so the timeout ends the wait without it being an error.
- `--wait-ms 500ms` advances the page's clock, running timers as their turn
  comes; sleeping alone would not run the callback.
- `--wait-script 'window.__ready'` evaluates the expression repeatedly until it
  is truthy, up to `--wait-timeout`. A syntax error fails immediately rather
  than polling until the timeout, since it will never become true.
- `--wait SELECTOR` waits for an element, as before.

The same primitives are on `Page`: `WaitForTime`, `WaitForScript`,
`WaitForNetworkIdle` and `WaitForSelector`.

## CLI flags

Every flag is documented in [`../docs/cli.md`](../docs/cli.md). The generated
list is the source of truth:

```sh
gocurlffi help open     # the browser path
gocurlffi help get      # the fast path
gocurlffi help serve    # CDP + WebDriver BiDi
gocurlffi help mcp      # MCP
```

The flags most specific to this package, in one place:

| Flag | Effect |
| --- | --- |
| `--wait-until MODE` | Stop the load after `load` (default), `domcontentloaded` or `networkidle0`. |
| `--wait-ms D` | Keep running the page's clock for `D`, so timer callbacks fire. |
| `--wait-script JS` | Poll an expression until it is truthy. |
| `--wait SELECTOR` | Wait for an element before extracting. |
| `--timer-budget D` | How long a load waits for pending timers. Lower is faster. |
| `--screenshot FILE` | Render the page to a PNG. `--width`, `--scale`, `--max-height`, `--no-images` control it. |
| `--pdf FILE` | Render the page to a PDF. |
| `--sheets` | Report the page's own stylesheets, and whether each was applied. |
| `--block GLOB` | Block requests matching a glob. Repeatable. |
| `--click`, `--type`, `--fill`, `--select` | Drive the page. Repeatable, and applied in the order given. |
