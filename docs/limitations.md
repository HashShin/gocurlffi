# Limitations

Everything this port does not do, with the measurement behind each claim where
there is one. The README carries the short version.

- **The browser is a document renderer, not a web renderer.** It cascades the
  page's CSS - selectors, specificity, `!important`, inheritance, `@import`,
  `@media`, custom properties and presentational attributes - and draws
  typography, colour, backgrounds, borders, rounded corners, box shadows,
  spacing and images. Gradients are not implemented. The cascade's computed
  values are checked against Chromium with `tools/cssdiff`: on 817 Hacker News
  elements and 109 quotes.toscrape elements all seven compared properties match
  exactly.
- **There is no WebAssembly.** goja has no WASM engine. This is structural, and
  it is the largest single gap against Lightpanda, which embeds V8.
- **HTTP/1.1 header casing** is lower-cased on the wire by the transport. HTTP/2,
  the default for impersonated traffic, is unaffected because header names are
  lower-case there.
- `Response.PrimaryIP`, `LocalIP`, ports and size counters are not populated:
  the transport does not expose per-connection metadata.
- **Chrome 110+ permutes its TLS extensions on every connection.** This port
  emits a fixed (valid) canonical order; JA3N/JA4, which sort extensions, are
  exact, while the raw JA3 byte order is one valid sample rather than
  re-randomised.
- Async sessions, caching backends, DoH and `curl_options` are not ported.
- **`requests` alone has no JavaScript engine.** For client-rendered pages use
  the `browser` package or `--render`.
- **A click navigates, but a script's own navigation does not always reach the
  loader.** `Click` follows the enclosing `<a href>` and submits the form a
  submit control belongs to - the controls are collected, the clicked button is
  included, POST carries them as the body and GET as the query, and a handler's
  `preventDefault` stops it. Still absent: `HTMLFormElement.submit` and
  `requestSubmit`; a form submitted by an `element.click()` inside a running
  script (only a link's scripted click is queued); and a navigation a timer or
  `Page.Eval` asks for after the load, which is recorded and never followed.
- The browser is missing a further set of web APIs; the notable ones are listed
  in [`browser/README.md`](../browser/README.md).
