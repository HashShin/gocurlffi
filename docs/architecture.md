# Architecture

`gocurlffi` is one Go module with two request paths that share a transport, plus
a server that exposes the slower one to other tools. This document explains how
the pieces fit and how the source tree is laid out. For the command line itself
see [`cli.md`](cli.md); for what the browser does not do yet see
[`limitations.md`](limitations.md).

## The two paths

```
                    ┌──────────────────────────────┐
  gocurlffi get ───▶│ requests.Session             │──▶ impersonating transport
   (fast path)      │ options, cookies, redirects  │    (uTLS + fhttp + HTTP/3)
                    └──────────────────────────────┘
                                    ▲
                                    │ same transport, so the same
                                    │ TLS/JA3 and HTTP/2 fingerprint
                    ┌───────────────┴──────────────┐
  gocurlffi get     │ browser.Browser              │──▶ goja + DOM + layout
   --render ───────▶│ Page: script, extract, render│
   (browser path)   └───────────────┬──────────────┘
                                    │
                    ┌───────────────┴──────────────┐
  serve / mcp ─────▶│ server: CDP, BiDi, MCP       │
                    └──────────────────────────────┘
```

The important property is that **both paths make their network requests through
the same code**. `browser` does not have its own HTTP client: it calls
`requests` with the same impersonation options, so a page fetched with
`--render` presents the identical TLS ClientHello, HTTP/2 settings and header
order to the server as the fast path would.

The other property is that the fast path constructs no JavaScript engine. Merging
the two commands into one binary costs size, not speed: linking `browser` pulls
in goja and the layout engine whether or not `--render` is used, which is why
`bin/gocurlffi` is around 37 MB. A plain `gocurlffi get` still allocates no VM
and makes no extra request.

## Packages

| Package | Role |
| --- | --- |
| `cmd/gocurlffi` | The only executable. A thin wrapper that sets the version and calls `cli.Main`. |
| `internal/cli` | Both command paths, the servers' subcommands, and all flag handling. |
| `requests` | The `requests`-style HTTP API: `Session`, options, `Request`/`Response`, `Headers`, `Cookies`. |
| `impersonate` | Fingerprint presets, name/alias resolution, and the mapping from a preset to a TLS/HTTP2/HTTP3 profile. |
| `browser` | The pure-Go headless browser: DOM, JavaScript, CSS cascade, layout, extraction and rendering. |
| `server` | CDP, WebDriver BiDi and MCP front ends over a `browser.Browser`. |
| `internal/capturehello` | Captures and parses a TLS ClientHello from any external command, for comparing fingerprints. |
| `tools/cssdiff` | Development-only comparison of this browser's CSS cascade and geometry against a real Chromium. A separate Go module. |

The project is pure Go. It contains no C and uses no cgo, so it cross-compiles
and builds with nothing but the Go toolchain. The preset table in
`impersonate/presets.go` is ordinary Go source, transcribed from
curl-impersonate's `lib/impersonate.c`; the MIT license for that data is at
`impersonate/upstream/LICENSE.curl-impersonate`.

## The `browser` package

`browser` is the largest package, so its files are named with a group prefix.
Go requires one package per directory, so the prefix is what keeps a directory
of roughly a hundred files navigable. Anything ending in `_test.go` sits next to
the code it exercises.

| Prefix | Files | Responsibility |
| --- | --- | --- |
| *(none)* | `browser.go`, `page.go` | The entry points: `Options`, `Browser`, `New`, `Open`, and the `Page` that owns a document and its VM. |
| `dom_` | `dom_tree.go`, `dom_shadow.go`, `dom_xml.go`, `dom_frames.go` | Tree mutation and traversal helpers, shadow roots and slots, XML documents, and iframes. |
| `css_` | `css_parse.go`, `css_om.go`, `css_values.go`, `css_webfont.go` | Stylesheet parsing, the cascade and specificity, length/`calc()` resolution, `@font-face`, and the CSSOM objects exposed to scripts. |
| `js_` | `js_env.go`, `js_dom.go`, `js_net.go`, `js_web.go`, `js_webapis.go`, `js_websocket.go`, `js_worker.go`, `js_module.go`, `js_observers.go`, `js_customelements.go`, `js_abortsignal.go`, `js_indexeddb.go`, `js_extras.go`, `js_iface.go`, `js_hostfunc.go` | The goja environment and every web API binding: DOM and element prototypes, `fetch`/`XMLHttpRequest`, `Blob`/`File`/`FormData`/`Headers`/`Response`, events and timers, WebSocket, workers, ES modules, observers, custom elements, `AbortSignal`, IndexedDB, canvas/audio/WebGL, and the `instanceof` plumbing that makes host objects answer as their real interface. |
| `net_` | `net_intercept.go`, `net_cors.go`, `net_robots.go`, `net_adblock.go`, `net_fingerprint.go` | Everything that decides whether and how a request leaves the browser: the intercept hook, CORS enforcement, `robots.txt`, the ad/tracker blocklist, and the derivation of `navigator` fields from the impersonated user agent. |
| `render_` | `render_text.go`, `render_geometry.go`, `render_image.go`, `render_canvas.go`, `render_svg.go`, `render_widget.go`, `render_screenshot.go`, `render_pdf.go` | Layout and output: the block/inline flow model, element rectangles, image decoding and rasterisation, `<canvas>` drawing, SVG rasterisation, form-control widgets, and the PNG and PDF writers. |
| `extract_` | `extract_text.go`, `extract_xpath.go`, `extract_structured.go` | Turning a loaded page into data: text, Markdown, links, XPath, and JSON-LD/microdata. |
| `act_` | `act_actions.go`, `act_wait.go` | Driving a page: click, type, fill, select; and waiting for time, a selector, a script predicate, or network idle. |

Two structural limits are worth knowing before reading the code:

- **The DOM is the `x/net/html` node tree.** There is no separate DOM model, so
  mutation helpers operate directly on `*html.Node` and the package carries a
  mutation-notification path (`js_observers.go`) to keep `MutationObserver` and
  the style cache honest.
- **There is no CSS box model.** Layout is a document flow model for typography,
  colour, display and spacing. `render_geometry.go` answers
  `getBoundingClientRect` from that flow, which is why `tools/cssdiff` compares
  geometry against Chromium rather than assuming it.

## Where to look first

| Question | File |
| --- | --- |
| How is a URL loaded and a page built? | `browser/page.go` |
| How does a page's script reach the DOM? | `browser/js_dom.go` |
| How is a fingerprint preset turned into a connection? | `impersonate/preset.go`, `requests/transport.go` |
| Which flags does a command accept? | `internal/cli/*.go` — the definitions are the documentation |
| What does the CDP server implement? | `server/domains.go` |
