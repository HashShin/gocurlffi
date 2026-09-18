# server

`server` exposes one pure-Go browser through three protocols on one
server: Chrome DevTools Protocol (CDP), WebDriver BiDi, and a Model Context
Protocol (MCP) tool server.

The package is a thin adapter. Every protocol call lands on the same
`*browser.Browser` and `*browser.Page` API described in
[`browser/README.md`](../browser/README.md), so automation and extraction
share one engine and one load path.

## How it is driven

The CLI wires the package up in `internal/cli/browser.go`. Two subcommands
exist; neither is required to embed the package directly.

### serve

`shade serve` starts CDP and WebDriver BiDi on one port. Both protocols
are always served on that port.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--host` | `127.0.0.1` | Bind host for CDP and BiDi |
| `--port` | `9222` | Bind port |
| `-i`, `--impersonate` | empty | TLS/HTTP fingerprint target |
| `--no-js` | `false` | Disable JavaScript execution |
| `--proxy` | empty | Route requests through a proxy URL |
| `--obey-robots` | `false` | Honour `robots.txt` |
| `--adblock` | `false` | Block ads and trackers |
| `--debug` | `false` | Log page-load phases to stderr |

There is no protocol selector: CDP and BiDi are always served together on the
one port.

It prints `CDP server listening on ws://<host>:<port>` to stderr and blocks
until interrupted. Before serving, it calls `server.SetVersion(cli.Version)`,
so the build commit is reported by `Browser.getVersion` and by the MCP
`initialize` reply.

### mcp

`shade mcp` serves the MCP tool server. With no `--port` it speaks
JSON-RPC on stdin/stdout (`--port 0` is the default and selects stdio); with a
non-zero `--port` it serves HTTP at `/mcp` and `/mcp/`.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--host` | `127.0.0.1` | Bind host for the HTTP transport |
| `--port` | `0` | HTTP port; `0` selects stdio |
| `-i`, `--impersonate` | empty | TLS/HTTP fingerprint target |
| `--no-js` | `false` | Disable JavaScript execution |
| `--proxy` | empty | Route requests through a proxy URL |
| `--obey-robots` | `false` | Honour `robots.txt` |
| `--adblock` | `false` | Block ads and trackers |
| `--debug` | `false` | Log page-load phases to stderr |

In HTTP mode it prints `MCP server listening on http://<host>:<port>/mcp` to
stderr. The HTTP transport is request/response per `POST`; there is no
server-initiated event stream.

## Go API

Import `github.com/HashShin/shade/server`. The exported surface is small. The CDP target, MCP
session, and JSON-RPC response types are unexported, so external code holds
values returned by the package and passes them back rather than naming their
types.

### CDP server

```go
type Config struct {
    Browser *browser.Browser
    Host    string
    Port    int
}

func New(cfg Config) *Server
func (s *Server) ListenAndServe(ctx context.Context, host string, port int) error
func (s *Server) Handler() http.Handler
func (s *Server) NewTarget(url string) *target
func SetVersion(v string)
```

`New` reads only `Config.Browser`; binding is done by the `host` and `port`
arguments of `ListenAndServe`, not by the `Config.Host` and `Config.Port`
fields. `ListenAndServe` listens with `net.Listen`, serves `Handler()`, and
closes the server when `ctx` is cancelled. `Handler()` returns the same mux
without the listener, which is what the tests use. `NewTarget` creates a page
target and returns an unexported `*target`. `SetVersion` sets the package-level
build string.

Embedding the CDP server:

```go
b := browser.New(browser.Options{})
defer b.Close()

srv := server.New(server.Config{Browser: b})
ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
defer cancel()
if err := srv.ListenAndServe(ctx, "127.0.0.1", 9222); err != nil {
    log.Fatal(err)
}
```

### MCP server

```go
func NewMCP(b *browser.Browser) *MCP
func (s *MCP) ServeStdio(r io.Reader, w io.Writer) error
func (s *MCP) ServeHTTP(w http.ResponseWriter, r *http.Request)
func (s *MCP) Handle(data []byte) *rpcResponse
func (s *MCP) HandleSession(sess *mcpSession, data []byte) *rpcResponse
func (s *MCP) Session(id string) *mcpSession
func (s *MCP) CloseSession(id string) bool
func (s *MCP) SessionIDs() []string
```

`ServeStdio` reads one JSON-RPC message per line, skips blank lines, and
answers on the default session. `ServeHTTP` is the streamable-HTTP transport,
described under "MCP session model". `Handle` processes one message on the
default session; `HandleSession` does the same on a session returned by
`Session`. Both return `nil` for a notification (a message without an `id`).
`Session` returns an existing session or creates one; `CloseSession` closes it
and reports whether it existed; `SessionIDs` lists live ids, oldest first.

Embedding the MCP server over stdio:

```go
m := server.NewMCP(b)
if err := m.ServeStdio(os.Stdin, os.Stdout); err != nil {
    log.Fatal(err)
}
```

## HTTP and WebSocket routes

`Server.Handler()` registers the following routes. Unknown paths return 404;
`/devtools/browser/` ignores the identifier after the prefix (the server
always reports `shade`).

| Path | Protocol | Purpose |
| --- | --- | --- |
| `GET /json/version` | CDP discovery | Browser name, protocol version, user agent, and the browser WebSocket URL |
| `GET /json/list` | CDP discovery | One page entry per target, each with its own WebSocket URL |
| `GET /json/new?url=` | CDP discovery | Create a page target; `about:blank` when `url` is omitted |
| `WS /` | CDP | Browser-level WebSocket; the endpoint a bare `ws://host:port` reaches |
| `WS /devtools/browser/{id}` | CDP | Browser-level WebSocket with `sessionId`-tagged commands |
| `WS /devtools/page/{id}` | CDP | Page-level WebSocket bound to one target |
| `WS /session` and `/session/{id}` | WebDriver BiDi | BiDi connection for `session.*`, `browsingContext.*`, `script.*`, `input.*` |

## MCP tools

`tools/list` returns these tools; the JSON-RPC methods are `initialize`,
`ping`, `tools/list`, and `tools/call`. Unknown methods and unknown tools are
JSON-RPC errors.

| Tool | Purpose | Key arguments |
| --- | --- | --- |
| `navigate` | Load a URL and return its rendered text | `url` (string, required) |
| `get_content` | Return the current page as text, markdown, html, or links | `format` (enum: `text`, `markdown`, `html`, `links`) |
| `evaluate` | Evaluate a JavaScript expression and return its value | `expression` (string, required) |
| `screenshot` | Render the current page to a PNG image | `width` (integer, default 1280) |
| `click` | Click the first element matching a CSS selector | `selector` (string, required) |
| `type` | Type text into the element matching a CSS selector | `selector` and `text` (strings, both required) |
| `structured_data` | Return the page's JSON-LD structured data | none |
| `session_new` | Start a session with its own page, cookies, and memory; return its id | none |
| `session_list` | List live sessions, oldest first | none |
| `session_close` | Close a session by id | `session` (string, required) |

Tools that need a page and have none return a text result beginning with
`error: no page loaded; call navigate first` rather than failing. A page
operation that itself fails (a load, evaluation, screenshot, click, or type
error) carries `isError: true`; a missing required argument returns a text
result beginning with `error:`.

## MCP session model

`NewMCP` keeps a template `*browser.Browser`. Each session forks it, so a
session has its own cookies, storage, and memory, plus its own current page.
`Session("")` maps to the fixed id `default`. HTTP sessions minted by the
server are named `s1`, `s2`, and so on.

Over stdio there is one implicit session: the client owns the process and
never names a session, so `ServeStdio` and `Handle` use `default`.

Over HTTP, `Mcp-Session-Id` selects the session on both the request and the
response:

- A `POST` without the header is assigned a fresh session id, which comes
  back in the `Mcp-Session-Id` response header. It also creates the `default`
  session as a side effect, so `default` appears in later `session_list`
  results.
- A `POST` with the header reaches that named session, creating it if it does
  not exist yet. Two clients that send the same id deliberately share one
  page.
- A notification is answered with `202 Accepted` and no body; every other
  request gets a JSON response.
- `DELETE` with the header closes the session and returns `204 No Content`.
  It returns `400` when the header is missing and `404` for an unknown id.

The session tools manage the same state explicitly. `session_new` creates a
session and returns its id, `session_list` returns a JSON array of live ids,
and `session_close` takes a `session` id and closes it. Closing a session
closes its forked browser.

## Connecting a real client

Puppeteer connects to the browser-level WebSocket. The root path and the
`/devtools/browser/{id}` path both serve it, so either of these works:

```js
const browser = await puppeteer.connect({
  browserWSEndpoint: "ws://127.0.0.1:9222",
});
const page = await browser.newPage();
await page.goto("https://example.com/");
```

The discovery endpoint reports the full URL for the `/devtools/browser/` form:

```sh
curl -s http://127.0.0.1:9222/json/version
```

WebDriver BiDi clients connect to the same port:

```text
ws://127.0.0.1:9222/session
```

An MCP client that spawns a process uses the stdio transport:

```json
{
  "mcpServers": {
    "shade": {
      "command": "/path/to/shade",
      "args": ["mcp"]
    }
  }
}
```

An MCP client that speaks streamable HTTP is pointed at `/mcp`, after
starting the server with a non-zero port:

```sh
./bin/shade mcp --port 9223
```

```json
{
  "mcpServers": {
    "shade": {
      "url": "http://127.0.0.1:9223/mcp"
    }
  }
}
```

A minimal HTTP round trip shows the session header:

```sh
curl -i -X POST http://127.0.0.1:9223/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
# response header: Mcp-Session-Id: s1

curl -i -X DELETE http://127.0.0.1:9223/mcp \
  -H 'Mcp-Session-Id: s1'
```

## Limitations

The CDP surface is a working subset, not the whole protocol. A method that is
not handled returns a JSON-RPC error with code `-32601` ("method not found"),
and a method that needs a page target on a browser-level connection returns
code `-32000`. CDP domains implemented in `domains.go`:

| Domain | Implemented methods |
| --- | --- |
| `Browser` | `getVersion`, `close` |
| `Target` | `setDiscoverTargets`, `setAutoAttach`, `setAttachToFrames`, `getTargets`, `createTarget`, `attachToTarget`, `detachFromTarget`, `closeTarget`, `getBrowserContexts`, `createBrowserContext`, `disposeBrowserContext` |
| `Page` | `enable`, `setLifecycleEventsEnabled`, `setDownloadBehavior`, `addScriptToEvaluateOnNewDocument`, `removeScriptToEvaluateOnNewDocument`, `getFrameTree`, `navigate`, `reload`, `getNavigationHistory`, `captureScreenshot`, `getLayoutMetrics`, `close`, `getResourceTree`, `createIsolatedWorld` |
| `Runtime` | `enable`, `evaluate`, `callFunctionOn`, `getProperties`, `releaseObject`, `releaseObjectGroup`, `discardConsoleEntries`, `setCustomObjectFormatterEnabled`, `runIfWaitingForDebugger` |
| `DOM` | `enable`, `disable`, `getDocument`, `querySelector`, `querySelectorAll`, `getOuterHTML`, `resolveNode`, `describeNode` |
| `Input` | `dispatchMouseEvent` (acts on `mousePressed` and `mouseReleased`) |
| `Emulation` | `setDeviceMetricsOverride` (width only), `clearDeviceMetricsOverride` |
| `Network` | `enable`, `setCacheDisabled`, `setBypassServiceWorker`, `setUserAgentOverride`, `getCookies` (always empty), `clearBrowserCookies`, `clearBrowserCache` |
| `Performance` | `getMetrics` (always empty) |

Many other methods that a client sends during setup are accepted and ignored
rather than rejected, to keep a client moving. They return an empty result
and do nothing: the `Fetch`, `Log`, `Debugger`, `Profiler`, `Security`,
`Overlay`, `Accessibility`, `Animation`, `CSS`, `Audits`, `ServiceWorker`,
and `IndexedDB` domains, the remaining `Emulation` and `Network` methods
(`setExtraHTTPHeaders`, `setRequestInterception`, `emulateNetworkConditions`,
`setBlockedURLs`, `setEmulatedMedia`, and the rest), the `Browser` window and
permission methods, `Runtime.addBinding`/`removeBinding`/`compileScript`,
`Page` screencast, CSP, and lifecycle methods, `DOM.getBoxModel`,
`DOM.getAttributes`, `DOM.focus`, `DOM.requestChildNodes`,
`DOM.getFrameOwner`, `Storage.getUsageAndQuota`, and
`CacheStorage.requestCacheNames`. `Input.dispatchKeyEvent` and
`Input.insertText` are also accepted and ignored, so keyboard input sent
through CDP does not reach the page.

Specific approximations:

- `Page.getLayoutMetrics` returns a fixed 1280x600 viewport, and
  `Emulation.setDeviceMetricsOverride` records only the width.
- `Network.getCookies` always reports no cookies; the cookie jar lives in the
  browser session and is not exposed through CDP.
- `Page.createIsolatedWorld` announces a new execution context id, but one
  JavaScript environment backs the whole target.
- Navigation lifecycle events are emitted about 60 ms after the reply to
  `Page.navigate`, so a client that installs its waiter after the command
  still sees them.
- `Page.captureScreenshot` and BiDi
  `browsingContext.captureScreenshot` use the browser's text-layout renderer,
  not a full paint pipeline.

The browser behind the server has no CSS box model. `DOM.getBoxModel` is a
no-op and `Page.getLayoutMetrics` returns fixed values. The browser does
answer geometric queries such as `getBoundingClientRect` from its layout, but
that layout is a text-flow model with no float, no grid layout, and no full
containing-block chain (see [`browser/README.md`](../browser/README.md)).

There is no WebAssembly engine, so `WebAssembly` is unavailable to page
scripts. BiDi implements `session.*`, `browsingContext.*`, `script.*`, and
`input.*` over the same target layer; other BiDi modules are not served.
