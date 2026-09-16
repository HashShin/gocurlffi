# gsearch

`gsearch` is a Google search-result scraper that drives a real Chromium process
over the Chrome DevTools Protocol (CDP), exposed as both a command-line tool and
a local web UI.

## A separate module

gsearch is not part of the main `gocurlffi` binary and is not built by the
repository's root `make build`, which builds only `./cmd/gocurlffi`. It lives in
`gsearch/` as its own Go module named `scraper`:

```text
module scraper

go 1.26

require github.com/gobwas/ws v1.4.0
```

The separation is deliberate. `gocurlffi` is pure Go, uses no cgo, and its fast
path never constructs a JavaScript engine. gsearch instead needs a real
Chromium process to render the page and negotiate Google's JavaScript gate, so
it is kept out of the main binary. Its only direct dependency is
`github.com/gobwas/ws`; CDP is spoken by hand over a WebSocket, with no
`chromedp`.

## Requirements

- Go matching the version declared in `go.mod` (currently `1.26`).
- A Chromium or Chrome binary, either auto-detected or given with `-browser`.
- An X display. Chromium is run against a virtual X server rather than in
  headless mode, because the source notes that headless gets CAPTCHA'd more
  often. gsearch uses `-display` (default `:99`), reuses an X server already
  listening there, and otherwise tries to start `Xvfb`, so `Xvfb` must be on
  `PATH` when no display is running. If you manage Xvfb yourself, start it
  before gsearch.

The module dependency is pinned in `go.sum` and is fetched on first build; there
is no vendor directory.

## Build and run

All commands are run from inside `gsearch/`, against the `scraper` module:

```sh
cd gsearch

# Run from source. With no arguments this starts the web UI on :8080.
go run .

# Build a binary, then use it.
go build -o gsearch .
./gsearch search "golang web scraping"
```

The binary selects its mode from the first argument:

```sh
gsearch                       # no arguments: start the web UI
gsearch serve                 # explicit web UI (aliases: server, web, ui)
gsearch search "query"        # CLI scrape (aliases: scrape, cli)
gsearch "query"               # CLI scrape shorthand, kept for compatibility
```

Flags must come before the positional queries. Go's `flag` package stops
parsing at the first non-flag argument, so `gsearch search "query" -json` treats
`-json` as a second query. Use `gsearch search -json "query"` instead.

## Browser resolution

`findBrowser` resolves the Chromium binary in this order, and stops at the first
match:

1. `-browser PATH`, when set. The path must exist or the engine fails with an
   error naming it.
2. The first existing path in this list:

   ```text
   /data/data/com.termux/files/usr/bin/chromium-browser
   /usr/bin/google-chrome-stable
   /usr/bin/google-chrome
   /usr/bin/chromium
   /usr/bin/chromium-browser
   /snap/bin/chromium
   /opt/google/chrome/chrome
   /Applications/Google Chrome.app/Contents/MacOS/Google Chrome
   ```

3. The first of these names found on `PATH`:

   ```text
   google-chrome-stable, google-chrome, chromium, chromium-browser
   ```

4. Otherwise: `no chromium found; pass -browser /path/to/chrome`.

The Termux path is listed first so behaviour there is unchanged, but it is one
entry in a candidate list. It used to be hardcoded as the only location, which
made the tool unusable anywhere else; the `-browser` flag was added to override
detection.

### User data directory

Chrome 136 and later refuse to open the remote debugging port against the
default profile ("DevTools remote debugging requires a non-default data
directory"), so a separate profile is required, not an optimisation. gsearch
creates it with mode `0700` and passes it as `--user-data-dir`:

- `$GSEARCH_PROFILE` if that environment variable is set;
- otherwise `os.TempDir()/gsearch-chrome-profile` (usually
  `/tmp/gsearch-chrome-profile`).

The profile is kept between runs on purpose: consent and session cookies
accumulate, which the source describes as keeping a warmed profile out of the
interstitial.

Chromium is started with a fixed flag set that includes `--no-sandbox`,
`--disable-dev-shm-usage`, `--disable-gpu`, `--remote-allow-origins=*` and
`--disable-blink-features=AutomationControlled`, plus `--user-data-dir` and
`--remote-debugging-port` on a free loopback port. gsearch reads the WebSocket
debugger URL from `http://127.0.0.1:PORT/json/version`, retrying for up to about
twenty seconds, then opens each tab with `PUT /json/new`. Every tab injects a
small stealth prelude and a randomly chosen desktop User-Agent. Startup is
retried up to three times, killing stray browser processes between attempts.

## CLI flags

Run `gsearch search -h` to print them. The `search` mode accepts:

| Flag | Default | Meaning |
| --- | --- | --- |
| `-concurrency` | `3` | parallel tabs / max concurrent requests |
| `-num` | `20` | results per page (Google caps this) |
| `-max` | `100` | max total results per query (0 = unlimited) |
| `-interval` | `2s` | global minimum delay between requests |
| `-timeout` | `20s` | per-query page-load timeout |
| `-retries` | `3` | retries per query on CAPTCHA/timeout |
| `-file` | `""` | file of queries, one per line |
| `-display` | `:99` | Xvfb display |
| `-browser` | `""` | browser binary (default: auto-detect) |
| `-json` | `false` | output JSON |
| `-out` | `""` | write JSON to a file (implies `-json`) |

Positional arguments are queries, and `-file` queries are appended to them.
`-concurrency` is clamped to at least 1 and to at most the number of queries.
If no queries are supplied at all, three built-in sample queries are run.
Human-readable output prints one block per query with `position`, title and URL,
followed by a completed count and elapsed time. `-json` (or `-out`) emits a
single JSON array of every result from every query; `-out` also writes it with
mode `0644` and reports the path.

## Serve mode

`gsearch` with no arguments, or with `serve` / `server` / `web` / `ui`, starts a
long-lived engine (one shared browser and tab pool) and an HTTP server.

| Flag | Default | Meaning |
| --- | --- | --- |
| `-addr` | `:8080` | listen address |
| `-display` | `:99` | Xvfb display |
| `-browser` | `""` | browser binary (default: auto-detect) |
| `-tabs` | `1` | browser tabs / concurrency (lower = less memory) |
| `-interval` | `2s` | min delay between requests |
| `-timeout` | `20s` | per-page load timeout |
| `-retries` | `3` | retries per search on CAPTCHA/timeout |

The startup log prints the UI as `http://127.0.0.1` followed by `-addr`. Note
that `-addr` is the real listen address; binding it to a non-loopback interface
makes the scraper reachable from other hosts.

### Routes

| Route | Returns |
| --- | --- |
| `GET /` | The embedded `index.html` as `text/html; charset=utf-8`. Any other path is answered with 404. |
| `GET /api/search?q=&num=&max=` | JSON. 400 with `{"error":"missing q"}` when `q` is blank. Otherwise `query`, `count`, `duration_ms` and a `results` array, plus an `error` string when the search failed (still HTTP 200). `num` defaults to 20 and `max` to 100 when absent or not positive. |
| `GET /api/search/stream?q=&num=&max=` | Server-sent events (`text/event-stream`, `Cache-Control: no-cache`, `X-Accel-Buffering: no`). 400 when `q` is blank. Each event is `data: {json}` with a `kind` of `info`, `page`, `error` or `done`, plus a `message` and a running `count`; the final `done` event carries the same payload as `/api/search`. |
| `GET /api/status` | `{"status":"ok","tabs":N}`, where `N` is the size of the tab pool. |

The handlers do not check the request method. `index.html` is compiled into the
binary with `//go:embed index.html`, so it must be present at build time but not
at run time. The page is the search UI: it calls `/api/search/stream`, renders
titles, URLs, domains, snippets and thumbnails, offers a JSON view and an
activity log, sends its `num`/`max` settings as query parameters, and can
auto-run a deep link such as `/?q=...`.

## What the scraper extracts

Each organic result block is selected with `.tF2Cxc`. Within a block the fields
are read as follows:

| Field | JSON key | Source |
| --- | --- | --- |
| Query | `query` | the query string that was passed in |
| Position | `position` | 1-based rank across all collected results |
| Title | `title` | `h3` `innerText` |
| URL | `url` | the first `a` element's `href` |
| Domain | `domain` | `.VuuXrf` text, otherwise the URL hostname with a leading `www.` removed |
| Snippet | `snippet` | `.VwiC3b` `innerText` (omitted when empty) |
| Image URL | `image_url` | an `image` key from the page evaluation (omitted; see Limitations) |
| Thumbnail | `thumbnail` | `img` `src` (omitted when empty) |

A row is discarded when both title and URL are empty. Results are de-duplicated
by URL across pages, and positions are assigned in collection order. Snippet,
image URL and thumbnail are optional JSON fields.

Search pages are requested as:

```text
https://www.google.com/search?q=<escaped>&num=<num>&hl=en&start=<offset>
```

Google often ignores or caps `num` (around ten results per page), so the scraper
walks `start` instead: after each page it advances `start` by the number of
result blocks the page held, and stops when a page adds no new result or `-max`
is reached. A page whose navigation fails after earlier pages succeeded returns
the partial result set rather than a hard error.

Two DOM checks drive the wait loop instead of a fixed sleep:

- the page has results when `.tF2Cxc` matches at least one element;
- the page is an interstitial when `document.body.innerHTML` matches
  `/sorry|recaptcha|unusual traffic/`, or the URL contains `/sorry`. That
  returns a `CAPTCHA detected` error.

Searches are serialized by a mutex even when the tab pool has more than one tab,
and a global rate limiter enforces one minimum interval between requests across
all callers. On a failed attempt the engine retries up to `-retries` times,
sleeping `attempt * 5` seconds between attempts; a connection error recreates
the tab, and a browser that appears dead is restarted with a fresh tab pool.

Progress events have `kind` values `info`, `page` and `error` from the scraper
(for example `Starting search for ...`, `Fetching results page (offset N)…`,
`Reached result cap`, `No more results`, `Retry n/N…`), and the serve layer adds
the final `done` event.

## Limitations

- Google frequently answers automated queries with a CAPTCHA or an "unusual
  traffic" interstitial regardless of the fingerprint presented, and this was
  observed even when the requests came from a real Chromium driven over CDP on
  some networks. gsearch can detect the interstitial and retry, but detection
  only turns a block into an error or another attempt; it does not remove the
  block.
- The tool is therefore not reliable for heavy use. `-interval`, a small tab
  pool and `-retries` reduce how often blocks appear, but they cannot guarantee
  results.
- The root README's "Limitations" section documents the same boundary for the
  pure-Go `browser`: Google's JavaScript gate hands off through a
  BotGuard/SearchGuard token whose environment verdict the server rejects, and
  the interstitial comes back over both transports. Forging or bypassing that
  token is out of scope for this repository, and gsearch does not attempt it.
- Parsing depends on Google's current CSS class names (`.tF2Cxc`, `.VwiC3b`,
  `.VuuXrf`). A markup change can quietly yield zero results; the result page is
  not a stable API.
- `image_url` is not populated: the extraction expression never produces the
  `image` key that the row builder reads, so only `thumbnail` can carry an
  image.
- `-max` is an upper bound, not a promise. Google caps results per page (the UI
  hint notes roughly ten per page and about a thousand per query), and
  pagination stops as soon as a page yields nothing new.
- A real Chromium process and an X display are required, so gsearch does not run
  in a plain serverless environment and cannot be cross-compiled alone. Searches
  are serialized, so extra tabs do not give parallel throughput.
- The CAPTCHA check is a string heuristic over the page body and URL. It can
  miss a block that uses different wording, and it can misfire on ordinary
  result text that happens to contain the same words.

## Terms and ethics

Scraping Google Search is against Google's Terms of Service. Rate limiting and
retries are engineering measures that reduce load; they are not permission.
You are responsible for how you use this tool and for complying with the law
and the terms that apply to you.
