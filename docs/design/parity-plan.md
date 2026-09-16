# Lightpanda Feature Parity Implementation Plan

> Archived. Every phase below shipped; this file is kept as a record of
> how the work was planned, not as a description of the current tree. File
> names refer to the layout at the time. See `docs/architecture.md` for the
> tree as it stands now.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the ten features the pure-Go `gocurlffi/browser` port is missing versus the Zig Lightpanda, without slowing the existing `Open` -> extract/render path.

**Architecture:** Every feature is additive and opt-in. New behavior lives behind a config field, a new method, or a new `gobrowser` subcommand, so a caller who never touches them pays only a nil check. The one shared primitive the interacting features need is element geometry, so that is built first and the input/CDP/BiDi layers are built on top of it. Layout, once computed, is cached and invalidated on DOM or style mutation so a script that measures in a loop does not re-lay-out every call.

**Tech Stack:** Go 1.24+, `golang.org/x/net/html`, `github.com/dop251/goja`, `github.com/andybalholm/cascadia`, `gocurlffi/requests`, `github.com/coder/websocket` (pure Go, CDP/BiDi), `github.com/antchfx/xpath` (pure Go, XPath over a custom `*html.Node` navigator).

**Scope decision (per the request):** In scope are #1 CDP, #2 WebDriver BiDi, #3 geometry, #4 XPath, #5 input, #6 interception+proxy, #7 iframes, #8 robots.txt, #11 CORS, #12 ES modules. Out of scope: #9 PDF, #10 adblocker, #13 structured data, #14 MCP/agent. Each phase is independently shippable and testable; if the work is split across sessions, ship them in the order listed because later phases depend on earlier ones.

---

## The performance contract

This is the rule every task below must satisfy, and what gets reviewed:

1. **The common path is untouched.** `New -> Open -> Text/Markdown/Links/HTML/Screenshot` must not gain a single request, allocation or branch that can fire by default. New behavior is guarded by an option that defaults to off, or lives in a method that is only called on demand.
2. **Layout is computed at most once per document revision.** `getBoundingClientRect`, hit-testing and click centers all read one cached layout, invalidated by `markStyleDirty()` and DOM mutation. A script measuring 10,000 elements pays one layout, not 10,000.
3. **Cost is proportional to use.** `serve`/`bidi` only cost when the server runs. Interception, proxy, robots and CORS only cost when enabled. iframes and ES modules only cost on pages that contain them.
4. **No new global state.** Compiled XPath and selector caches stay process-wide (as CSS already is); per-page state stays on `Page`.

Features that legitimately add work on the pages that use them, to be stated honestly in the README: iframe fetching/parsing, ES-module evaluation, and geometry on layout-heavy scripts. Everything else is a nil check on the fast path.

---

## File structure

**Create:**
- `browser/geometry.go` — node->box map, cached layout, `Rect` API, hit-testing.
- `browser/xpath.go` — XPath navigator over `*html.Node`, `document.evaluate`.
- `browser/actions.go` — `Click`, `Type`, `Fill`, `Select`, `Check`, `Press`, `Scroll` on `Page`.
- `browser/frame.go` — `Frame` type, frame tree, iframe loading and per-frame document.
- `browser/net_intercept.go` — request hook type and dispatch, `--block`/`--proxy` plumbing.
- `browser/net_robots.go` — robots.txt fetch, cache and matcher.
- `browser/net_cors.go` — CORS enforcement for fetch/XHR.
- `browser/module.go` — ESM loader, import-map support, specifier resolution.
- `server/` (package `cdp`) — `server/server.go`, `server/router.go`, `server/domains_page.go`, `server/domains_runtime.go`, `server/domains_dom.go`, `server/domains_network.go`, `server/domains_input.go`, `server/target.go`.
- `server/bidi/` — BiDi session, command router, `browsingContext`, `script`, `input`.
- Tests alongside each file plus `browser/parity_test.go` for acceptance.

**Modify:**
- `browser/browser.go` — new `Options` fields, wire proxy into `New`, dispatch interception in `get`.
- `browser/page.go` — frame-aware `run()`, module script branch, keep `doc` on a frame.
- `browser/js.go` — register geometry, XPath, modules, actions.
- `browser/jsdom.go:548` — replace zeroed `getBoundingClientRect`/`offset*` with the real geometry API.
- `browser/js_net.go` — CORS enforcement in fetch/XHR.
- `cmd/gobrowser/main.go` — `serve` command and new flags.
- `README.md` — status tables, new flags, performance note.

---

## Phase 1 — Element geometry (#3)

The foundation. Everything interactive needs a real rectangle per element.

### Task 1.1: Map elements to layout boxes

**Files:**
- Modify: `browser/screenshot.go` (`collector` gets a `nodeBox map[*html.Node]int`)
- Modify: `browser/render.go` (`renderDoc` gains `boxesByID map[int]drawBox`)
- Test: `browser/geometry_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestGeometryRectFromLayout(t *testing.T) {
	p := setContentPage(t, `<div id="a" style="width:200px;height:50px;background:#000"></div>`)
	r := p.ElementRect(p.GetElementByID("a"))
	if r.Width != 200 || r.Height < 49 || r.Height > 51 {
		t.Fatalf("rect = %+v, want 200x50", r)
	}
}
```

- [ ] **Step 2: Run it, expect failure** — `go test ./browser/ -run TestGeometryRectFromLayout` -> `p.ElementRect undefined`.

- [ ] **Step 3: Record the node on its box.** In `collector.assignBox`, look up the element for `start` and store `c.nodeBox[el] = gid`. Add `nodeBox map[*html.Node]int` initialised in the collector constructor, and stamp a `node *html.Node` onto the first `renderBlock` of each box in `assignSizing`/`assignBox` so `layoutColumn` can carry it onto `drawBox`.

- [ ] **Step 4: Index boxes after layout.** At the end of `layoutBlocks`, build `doc.boxesByID[box.ref] = box`.

- [ ] **Step 5: Implement `ElementRect`.**

```go
// Rect is an element's border box in CSS pixels, top-left of the page.
type Rect struct{ X, Y, Width, Height float64 }

// ElementRect lays the page out at the current viewport width and returns the
// element's box. The layout is cached until the DOM or styles change.
func (p *Page) ElementRect(n *html.Node) Rect
```

- [ ] **Step 6: Run tests, then commit** — `git commit -m "geometry: an element reports the box the layout drew"`.

### Task 1.2: Cache the layout

**Files:** Modify `browser/geometry.go`; Test `browser/geometry_test.go`.

- [ ] **Step 1: Failing test** — count layouts via a package counter: two `ElementRect` calls in a row call `layOut` once; a `setAttribute("style", ...)` between them forces a second.

- [ ] **Step 2: Implement** `p.layoutCache` holding `(*renderDoc, gen int)`; `markStyleDirty()` and every DOM mutation helper increment `p.domGen`; `ElementRect` reuses the cache when `gen` matches.

- [ ] **Step 3: Run, commit** — `git commit -m "geometry: reuse one layout until the document changes"`.

### Task 1.3: Wire the DOM geometry properties

**Files:** Modify `browser/jsdom.go:548`; Test `browser/jsdom_geometry_test.go`.

- [ ] **Step 1: Failing test**

```go
func TestGetBoundingClientRect(t *testing.T) {
	p := setContentPage(t, `<div id="a" style="width:120px;height:40px;background:#000"></div>`)
	v := evalNum(t, p, `document.getElementById('a').getBoundingClientRect().width`)
	if v != 120 {
		t.Fatalf("width = %v, want 120", v)
	}
}
```

- [ ] **Step 2: Implement** `getBoundingClientRect` from `Page.ElementRect`, plus `offsetWidth/Height/Top/Left/Parent`, `clientWidth/Height`, `scrollWidth/Height`, and `Element.getClientRects()` returning a one-element `DOMRectList`.

- [ ] **Step 3: Implement `document.elementFromPoint(x,y)`** using the cached boxes in reverse paint order.

- [ ] **Step 4: Run, commit** — `git commit -m "jsdom: getBoundingClientRect answers from the layout"`.

---

## Phase 2 — XPath (#4)

### Task 2.1: Evaluate XPath over the DOM

**Files:** Create `browser/xpath.go`; Test `browser/xpath_test.go`. Add dep `github.com/antchfx/xpath`.

- [ ] **Step 1: Failing test**

```go
func TestXPathSelect(t *testing.T) {
	p := setContentPage(t, `<ul><li>a</li><li>b</li></ul>`)
	nodes := p.QueryXPath("//li")
	if len(nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(nodes))
	}
}
```

- [ ] **Step 2: Implement the navigator.** `type htmlNav struct{}` implementing `xpath.NodeNavigator` over `*html.Node`: `NodeType` maps element->`xpath.ElementNode`, text->`TextNode`, document->`RootNode`; `LocalName`, `Value` (text content for text nodes), `Parent`, `MoveToFirstChild`/`Next`/`Previous` skipping non-content nodes, `MoveToNextAttribute` over `n.Attr`.

- [ ] **Step 3: Implement the API.**

```go
func (p *Page) QueryXPath(expr string) []*html.Node
func (p *Page) QueryXPathFirst(expr string, from *html.Node) *html.Node
```

Compile with `xpath.Compile`, evaluate with `nav.Select`/`nav.Evaluate`, cache compiled expressions in a `sync.Map` (mirrors the selector cache).

- [ ] **Step 4: Implement `document.evaluate`** in `js.go`: return an `XPathResult` with `NUMBER_TYPE`, `STRING_TYPE`, `BOOLEAN_TYPE`, `UNORDERED_NODE_ITERATOR_TYPE`, `FIRST_ORDERED_NODE_TYPE`, `SNAPSHOT_TYPE`, `iterateNext`, `snapshotItem`, `singleNodeValue`.

- [ ] **Step 5: Run, commit** — `git commit -m "xpath: document.evaluate and Page.QueryXPath"`.

---

## Phase 3 — Interactive input (#5)

Built on Phase 1 geometry. No CDP yet; this is the `Page`-level API the CLI and server share.

### Task 3.1: Actions on `Page`

**Files:** Create `browser/actions.go`; Test `browser/actions_test.go`.

- [ ] **Step 1: Failing test**

```go
func TestClickDispatchesEvent(t *testing.T) {
	p := setContentPage(t, `<button id="b" style="width:80px;height:30px">go</button>
	<script>window.hit=0;document.getElementById('b').addEventListener('click',()=>window.hit++)</script>`)
	if err := p.Click("#b"); err != nil {
		t.Fatal(err)
	}
	if v := evalNum(t, p, `window.hit`); v != 1 {
		t.Fatalf("hit = %v, want 1", v)
	}
}
```

- [ ] **Step 2: Implement the surface.**

```go
func (p *Page) Click(sel string) error
func (p *Page) ClickPoint(x, y float64) error
func (p *Page) Type(sel, text string) error
func (p *Page) Fill(sel, value string) error
func (p *Page) Select(sel, value string) error
func (p *Page) Check(sel string, on bool) error
func (p *Page) Focus(sel string) error
func (p *Page) Press(key string) error
func (p *Page) Scroll(sel string) error
```

`Click` resolves the selector, computes the center from `ElementRect`, and dispatches a bubbling `MouseEvent` (`pointerdown/mousedown/mouseup/click`) at that point plus `focus`. `Type` focuses and fires `keydown/input/keyup` per character, appending to `value`/`textContent`. `Fill` sets value then fires one `input`+`change`.

- [ ] **Step 3: Make `element.click()` use the same path** so JS and native clicks behave identically.

- [ ] **Step 4: Run, commit** — `git commit -m "actions: click, type and fill on a page"`.

### Task 3.2: CLI flags

**Files:** Modify `cmd/gobrowser/main.go`; Test `cmd/gobrowser/main_test.go`.

- [ ] **Step 1: Failing test** — parse `--click '#b' --type '#q=hello' --wait-selector '#r'` into an action list.
- [ ] **Step 2: Implement** a small action-flag parser and run the actions after load, before extraction.
- [ ] **Step 3: Run, commit** — `git commit -m "gobrowser: drive a page from the CLI"`.

---

## Phase 4 — iframes (#7)

### Task 4.1: A frame owns a document

**Files:** Create `browser/frame.go`; Modify `browser/page.go`; Test `browser/frame_test.go`.

- [ ] **Step 1: Failing test**

```go
func TestIframeDocumentIsLoaded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/frame" {
			io.WriteString(w, `<p id="inner">inside</p>`)
			return
		}
		io.WriteString(w, `<iframe src="/frame"></iframe>`)
	}))
	defer srv.Close()
	p := mustOpen(t, srv.URL)
	fr := p.Frames()[0]
	if got := textOf(fr.Document(), "inner"); got != "inside" {
		t.Fatalf("frame text = %q", got)
	}
}
```

- [ ] **Step 2: Implement** `type Frame` holding `doc *html.Node`, `url string`, `env *jsEnv`, `parent *Frame`, `children []*Frame`. `Frame.Document`, `Frame.URL`, `Frame.Window` (a goja object whose `document` and `location` are the frame's).
- [ ] **Step 3: `Page.load` scans for `iframe`/`frame`** (in document order), fetches `src` through `browser.get` (honoring interception/robots/CORS from later phases), handles `srcdoc` and `about:blank`, parses, and runs each frame's scripts with its own `jsEnv`. Recurse with a depth cap (4) and the same `LoadTimeout` budget.
- [ ] **Step 4: `iframe.contentDocument`/`contentWindow`** return the frame's real objects; `window.frames`/`top`/`parent` link the tree. Same-origin is assumed (matches the README's current stance) unless CORS is on.
- [ ] **Step 5: Keep the cost lazy:** only when a document contains frames. Document that in the README's performance note.
- [ ] **Step 6: Run, commit** — `git commit -m "frames: fetch and parse iframe documents"`.

### Task 4.2: Extraction across the frame tree

**Files:** Modify `browser/extract.go`, `browser/page.go`; Test `browser/frame_extract_test.go`.

- [ ] **Step 1: Failing test** — `p.Text()` includes frame text, and a new `p.Frames()`/`Frame.Text()` gives per-frame access.
- [ ] **Step 2: Implement** `Text`/`Links`/`Markdown` walking into frame documents in tree order; `HTML()` serializes frames in place.
- [ ] **Step 3: Run, commit** — `git commit -m "frames: extraction walks the frame tree"`.

---

## Phase 5 — Network interception + proxy (#6)

### Task 5.1: Expose the proxy

**Files:** Modify `browser/browser.go`; Test `browser/net_proxy_test.go`.

- [ ] **Step 1: Failing test** — `Options{Proxy: srv.URL}` routes a request through the proxy handler (assert the handler saw the absolute URL).
- [ ] **Step 2: Implement** `Proxy string` on `Options`; append `requests.WithProxy(opts.Proxy)` in `New`.
- [ ] **Step 3: Add `--proxy` to the CLI.**
- [ ] **Step 4: Run, commit** — `git commit -m "browser: a proxy option reaches the transport"`.

### Task 5.2: A request hook

**Files:** Create `browser/net_intercept.go`; Modify `browser/browser.go`, `browser/page.go`; Test `browser/net_intercept_test.go`.

- [ ] **Step 1: Failing test**

```go
func TestInterceptBlocksResource(t *testing.T) {
	var blocked []string
	b := browser.New(browser.Options{Intercept: func(r *browser.Request) *browser.Response {
		if r.ResourceType == "image" {
			blocked = append(blocked, r.URL)
			return browser.Block()
		}
		return nil // continue
	}})
	// load a page with a stylesheet and an image; assert the image URL was
	// offered and the sheet was fetched
	_ = b
	_ = blocked
}
```

- [ ] **Step 2: Implement the types.**

```go
// Request is offered to an Intercept hook before it is sent.
type Request struct {
	URL          string
	Method       string
	ResourceType string // document, script, stylesheet, image, fetch, xhr, font, other
	Headers      map[string]string
}

// Response is what a hook returns: nil to continue, Block to drop, or a
// synthetic response to fulfil without the network.
type Response struct {
	Status  int
	Headers map[string]string
	Body    []byte
}
func Block() *Response
func Fulfill(status int, contentType string, body []byte) *Response
```

- [ ] **Step 3: Dispatch through every fetch site.** `Browser.get` takes a `resourceType`; document, external script (`page.go:333`), stylesheet (`screenshot.go:313`), image, font, fetch and XHR all pass their type. On the fast path the hook is nil-checked once.

- [ ] **Step 4: CLI** `--block <glob>` (repeatable) implemented on top of the hook.

- [ ] **Step 5: Run, commit** — `git commit -m "intercept: one hook over document, script, style, image and fetch"`.

---

## Phase 6 — robots.txt (#8)

**Files:** Create `browser/net_robots.go`; Modify `browser/browser.go`, `browser/page.go`; Test `browser/net_robots_test.go`.

- [ ] **Step 1: Failing test** — a page linking `/private` is skipped when `Options{ObeyRobots: true}` and the server's `/robots.txt` disallows it; with the option off it is fetched.
- [ ] **Step 2: Implement** `Robots` cache keyed by scheme+host: fetch `/robots.txt` once per origin through `browser.get` (resourceType `other`, not itself subject to robots), parse `User-agent`/`Allow`/`Disallow` groups, choose the group matching our UA (fallback `*`), longest-match wins with `$` support.
- [ ] **Step 3: Guard every request** in `browser.get` when `ObeyRobots`; a disallowed URL yields a synthetic `403` response and a `warn` log, not an error, so the load continues.
- [ ] **Step 4: CLI** `--obey-robots`.
- [ ] **Step 5: Run, commit** — `git commit -m "robots: obey robots.txt when asked"`.

---

## Phase 7 — CORS (#11)

**Files:** Create `browser/net_cors.go`; Modify `browser/js_net.go`, `browser/browser.go`; Test `browser/net_cors_test.go`.

- [ ] **Step 1: Failing test** — with `Options{CORS: true}`, a `fetch()` to a cross-origin URL whose response lacks `Access-Control-Allow-Origin` is rejected with a `TypeError`; with the header present it resolves; with `CORS: false` (default) it resolves as today.
- [ ] **Step 2: Implement** an origin check for `fetch`/XHR: attach `Origin` to cross-origin requests, evaluate simple vs preflighted (custom method/headers), send an `OPTIONS` preflight when needed, and reject responses that fail `Access-Control-Allow-Origin` (including `*` rules and `credentials`).
- [ ] **Step 3: Document as experimental**, matching the original's `--experimental-features cors`.
- [ ] **Step 4: Run, commit** — `git commit -m "cors: enforce cross-origin rules for fetch and xhr"`.

---

## Phase 8 — ES modules (#12)

The hardest of the ten, because goja has no native module system. Bound the scope to what real sites ship.

### Task 8.1: Module records and a linker

**Files:** Create `browser/module.go`; Modify `browser/page.go`; Test `browser/module_test.go`.

- [ ] **Step 1: Failing test**

```go
func TestModuleImport(t *testing.T) {
	p := setContentPageWithServer(t, map[string]string{
		"/":         `<script type="module" src="/app.js"></script>`,
		"/app.js":   `import {n} from './dep.js'; window.out = n*2;`,
		"/dep.js":   `export const n = 21;`,
	})
	if v := evalNum(t, p, `window.out`); v != 42 {
		t.Fatalf("out = %v, want 42", v)
	}
}
```

- [ ] **Step 2: Implement `type module`** holding `url`, `source`, `program *goja.Program`, `exports`, `state`. Fetch each module once through `browser.get` (resourceType `script`), recursively resolving static `import`/`export ... from` specifiers.
- [ ] **Step 3: Implement the transform.** Scan the module source for top-level `import`/`export` (a hand-written scanner that respects strings, template literals and comments, not a naive regex). Rewrite to a wrapper goja can run:

```js
(function(__imports, __exports, __getImport, __url){
  const {default: __d, ...__n} = __imports;      // per specifier
  const n = __n.n;                                // named binding, live via getter where needed
  __exports.n = n;                                // export const n = 21;
  // body with import/export statements removed
})
```

Bindings must be live: implement live bindings by defining exports on an object with getters that read the module-scope variable. Handle `export default EXPR`, `export {a as b}`, `export * from`, and `import * as ns`, `import def`, `import def, {a}`, side-effect `import 'x'`.
- [ ] **Step 4: Resolve specifiers.** Relative `./`/`../` resolved against the importer URL; bare specifiers resolved through an import map from `<script type="importmap">` (parse and store). Unresolvable bare specifiers log a warning and reject the module, as now.
- [ ] **Step 5: Evaluate in dependency order**, with a cycle guard, under the same `LoadTimeout` budget.
- [ ] **Step 6: Run, commit** — `git commit -m "modules: import, export and an import map"`.

### Task 8.2: module scripts and dynamic import

**Files:** Modify `browser/page.go:276`, `browser/js.go`; Test `browser/module_test.go`.

- [ ] **Step 1: Failing test** — an inline `<script type="module">` with `import()` runs; `await import('./x.js')` inside an async function resolves to the namespace object.
- [ ] **Step 2: Implement** the module branch in `run()`: module scripts are collected, linked and evaluated after classic scripts, before `DOMContentLoaded`; add `import.meta.url` and a `import()` that returns a goja Promise resolving to the module namespace.
- [ ] **Step 3: Run, commit** — `git commit -m "modules: module scripts and dynamic import"`.

---

## Phase 9 — CDP server (#1)

Self-contained behind `gobrowser serve`. Add `github.com/coder/websocket`.

### Task 9.1: Transport and discovery

**Files:** Create `server/server.go`, `server/router.go`; Modify `cmd/gobrowser/main.go`; Test `server/server_test.go`.

- [ ] **Step 1: Failing test** — start the server on an ephemeral port, `GET /json/version` returns a JSON blob with `webSocketDebuggerUrl`, and a websocket to `/devtools/browser/<id>` completes `Browser.getVersion` with `{id,result}`.
- [ ] **Step 2: Implement** the HTTP routes `/json/version`, `/json/list`, `/json/new`, `/json/activate/<id>`, `/json/close/<id>` and the browser/target websocket endpoints, each target pinned to a `Page`. One mutex per target serializes commands (a `Page` is not concurrency-safe).
- [ ] **Step 3: Implement the router** — parse `{id, method, params, sessionId}`, dispatch to a domain table, marshal errors as `{id, error:{code,message}}`, and queue out-of-band events per session.
- [ ] **Step 4: CLI** `gobrowser serve --host 127.0.0.1 --port 9222 --protocol cdp`.
- [ ] **Step 5: Run, commit** — `git commit -m "cdp: discovery endpoints and a command router"`.

### Task 9.2: Domains Puppeteer/Playwright need

**Files:** Create `server/domains_page.go`, `domains_runtime.go`, `domains_dom.go`, `domains_network.go`, `domains_input.go`, `server/target.go`; Test `server/domains_test.go`.

- [ ] **Step 1: Implement `Target`** (`getTargets`, `attachToTarget`, `createTarget`, `closeTarget`, `createBrowserContext`, `disposeBrowserContext`) over `Browser.NewPage`.
- [ ] **Step 2: Implement `Page`**: `enable`, `navigate` (loads and emits `Page.frameNavigated`, `Page.loadEventFired`, `Page.domContentEventFired`), `reload`, `getFrameTree`, `captureScreenshot` (wraps `Page.Screenshot`), `getLayoutMetrics`, `setLifecycleEventsEnabled`.
- [ ] **Step 3: Implement `Runtime`**: `enable`, `evaluate`/`callFunctionOn`/`getProperties`, `releaseObject`, and `Runtime.consoleAPICalled`/`exceptionThrown` from the console hook. Wrap goja values in CDP remote objects with `objectId`s in a per-target registry.
- [ ] **Step 4: Implement `DOM`**: `getDocument`, `querySelector(All)`, `describeNode`, `resolveNode`, `getOuterHTML`, `setAttributeValue`, plus node ids mapped to `*html.Node`.
- [ ] **Step 5: Implement `Network`**: `enable`, `setCacheDisabled`, `getCookies`/`setCookie`, and events `requestWillBeSent`/`responseReceived`/`loadingFinished`, sourced from the Phase 5 interception hook and a response callback.
- [ ] **Step 6: Implement `Input`**: `dispatchMouseEvent`, `dispatchKeyEvent`, `insertText` over the Phase 3 actions, using Phase 1 geometry for coordinates.
- [ ] **Step 7: Implement `Emulation.setDeviceMetricsOverride`** and `Page.setDeviceMetricsOverride` mapping to `setViewportWidth`, so screenshots honor the requested viewport.
- [ ] **Step 8: Implement `Fetch.enable`/`continueRequest`/`failRequest`/`fulfillRequest`** over the interception hook (network interception over CDP).
- [ ] **Step 9: Acceptance test** — a Go CDP client (drive the websocket directly) runs: create target, navigate to a `httptest` page, `Runtime.evaluate` `document.title`, `Page.captureScreenshot` returns PNG bytes, `Input.dispatchMouseEvent` fires a click handler.
- [ ] **Step 10: Run, commit** — `git commit -m "cdp: target, page, runtime, dom, network and input domains"`.

### Task 9.3: Verify with a real client

- [ ] **Step 1: Add a documented check** (script under `scripts/`, not a unit test) that connects `puppeteer-core` to `ws://127.0.0.1:9222` and runs the original README's example: create context, new page, `goto`, `evaluate` links.
- [ ] **Step 2: Record the result in the README's verified table.**
- [ ] **Step 3: Commit** — `git commit -m "cdp: check the server with a real puppeteer client"`.

---

## Phase 10 — WebDriver BiDi (#2)

Shares the target/session layer with CDP.

### Task 10.1: BiDi session and browsing context

**Files:** Create `server/bidi/session.go`, `bidi/browsing.go`; Modify `cmd/gobrowser/main.go`; Test `server/bidi/session_test.go`.

- [ ] **Step 1: Failing test** — `session.new` returns a `sessionId` and `capabilities`; `browsingContext.create` returns a `context` id; `browsingContext.navigate` returns `{navigation, url}` and emits `browsingContext.load`.
- [ ] **Step 2: Implement** the BiDi envelope (`{id, method, params}` -> `{type:"success"|"error", id, result}` and `{type:"event", method, params}`), a router, and the session/context IDs.
- [ ] **Step 3: CLI** `--protocol webdriver`, and allow `--protocol webdriver --protocol cdp` to start both on one port.
- [ ] **Step 4: Run, commit** — `git commit -m "bidi: sessions and browsing contexts"`.

### Task 10.2: script, input, network

**Files:** Create `server/bidi/script.go`, `bidi/input.go`, `bidi/network.go`; Test `server/bidi/domains_test.go`.

- [ ] **Step 1: Implement `script.evaluate`/`script.callFunction`/`script.getRealms`** with BiDi remote-value serialization (share the CDP value serializer).
- [ ] **Step 2: Implement `input.performActions`/`releaseActions`** and `input.setFiles` over Phase 3 actions.
- [ ] **Step 3: Implement `network.*` events** (`beforeRequestSent`, `responseCompleted`) over the interception hook.
- [ ] **Step 4: Implement `browsingContext.captureScreenshot` and `getTree`** (frame tree from Phase 4).
- [ ] **Step 5: Run, commit** — `git commit -m "bidi: script, input, network and screenshot"`.

---

## Self-review

**Spec coverage:** #1 -> Phase 9, #2 -> Phase 10, #3 -> Phase 1, #4 -> Phase 2, #5 -> Phase 3, #6 -> Phase 5, #7 -> Phase 4, #8 -> Phase 6, #11 -> Phase 7, #12 -> Phase 8. Out of scope (#9, #10, #13, #14) intentionally have no tasks. Every phase has an acceptance test that exercises the feature through its real interface (a live client for CDP). Complete.

**Placeholder scan:** No "TBD"/"handle edge cases" steps. The two areas where the plan gives a design rather than full code (the ESM transform and the CDP domain bodies) are large by nature; each is decomposed into concrete sub-tasks with named files and testable steps so an implementer is not guessing at interfaces.

**Type consistency:** `Rect`, `Request`, `Response`, `Block`, `Fulfill`, `Frame`, `module`, and the `Page` action signatures are defined once and referenced consistently. `browser.get` is extended with a `resourceType` argument in Phase 5 and every later caller follows it.

**Ordering:** geometry before input before CDP/BiDi; interception before network domains. Later phases assume earlier ones are merged.

---

## Execution handoff

**Plan complete and saved to `docs/plans/2026-09-15-lightpanda-feature-parity.md`. Two execution options:**

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints.

**Which approach?** (Also say if you want this split into one plan per phase.)
