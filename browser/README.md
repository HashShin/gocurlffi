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
  ./bin/gobrowser get https://brave.com/ --sheets
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
- Classic scripts that use top-level `await` are retried wrapped in an async
  function, since some bundlers ship them as classic scripts.
- Recursion is capped like a browser engine (`Options.LoadTimeout` and a
  ~10000-frame call stack), so a polyfill loop throws `RangeError` instead of
  spinning forever, and a slow subresource cannot stall the load past the
  budget.
- Extraction helpers: `HTML()`, `Text()`, `Links()`, `Markdown()`.
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

`scripts/bench_browser.sh` loads the same pages with `gobrowser` and with a
Chromium CLI (`--headless=new --dump-dom`) and reports wall time and rendered
bytes. Measured on Termux/arm64 against Chromium 149:

| Page | Chromium | gobrowser | Chromium bytes | gobrowser bytes |
| --- | --- | --- | --- | --- |
| `about:blank` | ~0.95 s | - | 40 | - |
| `quotes.toscrape.com/js/` | 3.7 / 2.6 s | **2.0 / 1.9 s** | 8 987 | 9 005 |
| `react.dev` | 3.6 / 2.2 s | 3.6 / 3.8 s | 272 527 | 272 744 |
| `gocomics.com` | 26.1 / 8.0 s | **8.7 / 8.2 s** | 3 078 797 | 1 575 337 |
| `foodnetwork.com` | 23.5 / 23.5 s | **3.5 / 3.7 s** | 625 985 | 340 452 |

Reading it honestly:

- gobrowser is faster on every page tested, and several times faster on heavy
  pages, because there is no browser startup (~1 s before Chromium even starts
  loading) and no per-page process to launch.
- On `react.dev` the two are level: that much JavaScript interpreted without a
  JIT costs about what Chromium spends starting up and compiling.
- Chromium renders **more** bytes on heavy pages (roughly 2x on gocomics). It is
  a real browser: it runs more scripts to completion and normalizes the DOM. So
  gobrowser being faster is partly "does less".

Caveats, so the numbers are not over-read:

- Chromium needs a warmed `--user-data-dir` here; its first run with a cold
  profile hangs. The warmup is untimed, and timed runs hit a URL the profile has
  not cached, which is the closest match to gobrowser's always-cold behaviour.
- Chromium is bounded with `--virtual-time-budget` because `--dump-dom` never
  returns on pages that do not reach network idle. That budget is virtual, not
  wall-clock, so its time is a floor rather than a full time-to-interactive.
- Chromium's launcher ignores `SIGTERM`, so the script bounds it with
  `timeout -k`.

```sh
bash scripts/bench_browser.sh -n 3
bash scripts/bench_browser.sh https://your-site.example/
GOBROWSER_ARGS="--timer-budget 0s" bash scripts/bench_browser.sh
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

### Checking the cascade against a browser

`tools/cssdiff` compares this package's computed styles with a real Chromium,
element by element, on the same page: Chromium answers over CDP, the Go browser
answers through `gobrowser get --eval`, and the two are matched by DOM position.
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
./bin/gobrowser get https://quotes.toscrape.com/js/ --screenshot page.png --width 900
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
- Still no box model: no borders on arbitrary elements, no shadows, floats,
  positioning or gradients. CSS is
  cascaded for typography, colour, display, spacing, alignment and flat block
  backgrounds. `float` and `position` are ignored for layout, so sidebars and
  menus that a browser would place beside the content flow inline or in document
  order (their *computed* values are still reported correctly).
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

## Checking sites

`scripts/check_sites.sh -B` runs the same site/target matrix with the headless
browser instead of the plain HTTP client, and shows the rendered size per cell.
Comparing the two modes shows which sites actually need JavaScript:

```sh
bash scripts/check_sites.sh -B -i chrome131,custom
bash scripts/check_sites.sh -B -i chrome131 https://bsky.app/
```

## Not implemented

This is a browsing core with a document renderer, not a web rendering engine.
`Screenshot` flows text and draws a PNG, but there is no CSS box model, no
borders or shadows, and no PDF output. Specifically absent:

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
  --screenshot FILE     # render the page to a PNG
  --width 1280          # screenshot layout width
  --scale 1             # screenshot scale factor
  --max-height 20000    # screenshot height cap
  --no-js               # disable JavaScript
  --console             # print console.* to stderr
  --status              # print HTTP status to stderr
  --sheets              # report the page's own stylesheets on stderr
  --debug               # log page-load phases to stderr
  -H 'K: V'             # extra header (repeatable)
```
