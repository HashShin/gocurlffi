# Limitations

Everything this port does not do, with the measurement behind each claim where
there is one. The README carries the short version.

## - **Google search is refused.** The interstitial and the BotGuard token are
  handled correctly; Google escalates the token's environment verdict anyway, and
  a real Chromium from the same host is refused identically. The measurements
  are in [`docs/botguard.md`](botguard.md). Use Bing, Brave or
  `lite.duckduckgo.com` for HTML results.
- **The browser has no CSS box model.** No borders, shadows, floats,
  positioning, gradients or images. It cascades the page's CSS - selectors,
  specificity, `!important`, inheritance, `@import`, `@media`, custom properties
  and presentational attributes - for typography, colour, display and spacing,
  and can render to PNG with `--screenshot`, but that is a document renderer
  (flowed text, headings, lists, quotes, flat backgrounds), not a web renderer.
  The cascade's computed values are checked against Chromium with
  `tools/cssdiff`: on 817 Hacker News elements and 109 quotes.toscrape elements
  all seven compared properties match exactly.
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
- The browser is missing a further set of web APIs. The list is measured, not
  estimated, in [`docs/parity.md`](parity.md).
