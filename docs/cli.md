# Command line

One binary, `bin/gocurlffi`, built with `make build`. It has two request paths
and two servers. Run `gocurlffi help <command>` for any command's flags, or
`gocurlffi <command> --help`. The flag list is generated from the definitions in
`internal/cli`, so it cannot drift from what the command accepts.

```
gocurlffi get <url> [flags]            fast HTTP client with browser TLS/JA3
gocurlffi get <url> --render [flags]   load the URL in the pure-Go browser
gocurlffi open <url> [flags]           shorthand for "get --render"
gocurlffi serve [flags]                CDP + WebDriver BiDi server
gocurlffi mcp [flags]                  MCP tool server (stdio, or --port for HTTP)
gocurlffi targets                      list impersonation targets
gocurlffi version                      print the version
gocurlffi help [command]               this overview, or a command's flags
```

Exit status is `0` for success, `1` when the command ran and failed, and `2`
when the arguments were wrong. A request that fails to connect exits `1`, so a
shell can tell a bad URL from a bad flag.

## Choosing a path

`get` is the fast path. It performs one request through the impersonating
transport and never constructs a JavaScript engine. It is what you want for a
server-rendered page or an API.

`--render` (or the `open` command) loads the same URL in the pure-Go browser,
runs the page's scripts and lets you extract from the resulting DOM. The cost is
CPU and memory, not an extra request: the browser fetches through the same
transport, so the fingerprint is identical.

```sh
gocurlffi get tls.browserleaks.com/json -i chrome150
gocurlffi get quotes.toscrape.com/js/ --render --format text
gocurlffi open example.com --screenshot page.png
```

There is no browser path for a request that carries a body, so `--render` is
rejected for `post`, `put`, `patch`, `delete`, `head`, `options` and `trace`.

## HTTP methods

A bare method is a command, so the fast path also reads as:

```sh
gocurlffi post httpbin.org/post -j '{"a":1}'
gocurlffi put example.com/thing -d 'name=value'
gocurlffi head example.com -v
```

Equivalently, name the method explicitly. This is useful when a script builds
the method dynamically:

```sh
gocurlffi get example.com -X POST -d 'name=value'
```

## Fast-path flags (`get`, and every bare method)

| Flag | Default | Meaning |
| --- | --- | --- |
| `-i`, `--impersonate` | `native` | Fingerprint target: a browser preset, or `native`, `curl`, `custom`. |
| `-H`, `--header` | | Add a request header as `Name: Value`. Repeatable. |
| `-P`, `--param` | | Add a query parameter as `key=value`. Repeatable. |
| `-d`, `--data` | | Request body: a literal string, `@file`, or `key=value` pairs. |
| `-j`, `--json` | | JSON request body. Takes precedence over `-d`. |
| `--form` | `false` | Force form encoding of `-d` when it has no `=` in it. |
| `--cookie` | | Add a cookie as `key=value`. Repeatable. |
| `--auth` | | HTTP basic auth as `user:pass`. |
| `--proxy` | | Proxy URL. |
| `-t`, `--timeout` | `30` | Request timeout in seconds. |
| `-X`, `--method` | | HTTP method, overriding the command name. |
| `--follow` | `true` | Follow redirects. Use `-follow=false` to stop. |
| `--max-redirects` | `30` | Maximum number of redirects. |
| `--verify` | `true` | Verify TLS certificates. Use `-verify=false` to skip. |
| `--http-version` | | Force `v1`, `v2` or `v3`. |
| `--stream` | `false` | Stream the body rather than buffering it. |
| `-o`, `--output` | | Write the response body to a file. |
| `--headers` | `false` | Print the response headers only. |
| `-v`, `--verbose` | `false` | Print the status line, headers and body. |

`-f` is **not** a short form of `--form`. It means `--format` on the browser
path, and one letter with two meanings across one binary is worse than a longer
spelling here.

When the response looks like a JavaScript-only interstitial rather than content,
the command prints a `warning:` to stderr. See [`botguard.md`](botguard.md).

## Browser-path flags (`get --render`, `open`)

Loading and timing:

| Flag | Default | Meaning |
| --- | --- | --- |
| `-i`, `--impersonate` | `native` | Fingerprint target. |
| `--timeout` | `30s` | Per-request timeout. |
| `--load-timeout` | `30s` | Script-loading budget per page. |
| `--timer-budget` | `2s` | Wait for pending timers after load. |
| `--wait-until` | `load` | How far the load goes: `load`, `domcontentloaded` or `networkidle0`. |
| `--wait-ms` | `0` | Keep running the page's timers for this long after load. |
| `--wait-script` | | Evaluate JavaScript repeatedly until it is truthy. |
| `--wait` | | Wait for a selector before extracting. |
| `--wait-timeout` | `10s` | Timeout for `--wait` and `--wait-script`. |
| `--no-js` | `false` | Do not run the page's JavaScript. |
| `--proxy` | | Route requests through a proxy URL. |
| `-H`, `--header` | | Extra request header as `Name: Value`. Repeatable. |
| `--block` | | Block requests matching a glob. Repeatable. |
| `--obey-robots` | `false` | Honour `robots.txt`. |
| `--adblock` | `false` | Block ads and trackers. |

Extraction:

| Flag | Default | Meaning |
| --- | --- | --- |
| `-f`, `--format` | `html` | `html`, `markdown`, `text`, `links` or `structured`. |
| `-o`, `--output` | | Write the output to a file. |
| `--eval` | | Evaluate JavaScript after load and print the result. |
| `--xpath` | | Evaluate an XPath expression and print the matched text. |

Rendering:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--screenshot` | | Render the page to a PNG at this path. |
| `--pdf` | | Render the page to a PDF at this path. |
| `--width` | `1280` | Screenshot layout width in pixels. |
| `--scale` | `1` | Screenshot scale factor. |
| `--max-height` | `20000` | Screenshot height cap in pixels. |
| `--no-images` | `false` | Do not draw the page's images. Faster, text only. |

Driving the page, applied in the order the flags appear:

| Flag | Meaning |
| --- | --- |
| `--click` | Click the matching element. Repeatable. |
| `--type` | Type text into a control, as `selector=text`. Repeatable. |
| `--fill` | Set a control's value, as `selector=value`. Repeatable. |
| `--select` | Choose an option, as `selector=value`. Repeatable. |

A click carries its default action, as it does in a browser: it follows the link
it is on, and it submits the form a submit button belongs to. The driving runs
before `--wait`, so `--fill ... --click 'button[type=submit]' --wait '#account'`
waits for the page the click produced. `--screenshot` and `--pdf` come after
both, so the picture is of the driven page.

Diagnostics:

| Flag | Meaning |
| --- | --- |
| `--console` | Print page console output to stderr. |
| `--status` | Print the HTTP status to stderr. |
| `--sheets` | Report the page's own stylesheets on stderr. |
| `--debug` | Log page-load phases to stderr, and print the layout with a screenshot. |

`--status`, `--sheets` and `--console` write to stderr and do not stop the rest
of the command, so `get URL --screenshot page.png --sheets` writes the PNG and
lists the stylesheets it was rendered with.

## `serve`

Starts the CDP and WebDriver BiDi server on one port. See
[`../server/README.md`](../server/README.md) for the routes and the domains that
are implemented.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--host` | `127.0.0.1` | Interface to bind. |
| `--port` | `9222` | Port to listen on. |
| `-i`, `--impersonate` | `native` | Fingerprint target. |
| `--no-js`, `--proxy`, `--obey-robots`, `--adblock`, `--debug` | | As above. |

## `mcp`

Exposes the browser as Model Context Protocol tools, over stdio by default or
HTTP when `--port` is non-zero.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--host` | `127.0.0.1` | Interface to bind for the HTTP transport. |
| `--port` | `0` | Serve over HTTP on this port. `0` means stdio. |
| `-i`, `--impersonate` | `native` | Fingerprint target. |
| `--no-js`, `--proxy`, `--obey-robots`, `--adblock`, `--debug` | | As above. |

## Aliases

Kept so that scripts written before the two binaries were merged keep working:

| Alias | Canonical |
| --- | --- |
| `fetch` | `get` |
| `browse`, `render` | `open` |
| `list` | `targets` |
| `--js`, `-js` | `--render` |
