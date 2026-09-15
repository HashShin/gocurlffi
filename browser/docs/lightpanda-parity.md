# Lightpanda parity: what remains

The Zig [Lightpanda](https://github.com/lightpanda-io/browser) checkout under
`browser/browser/` is the reference this port is measured against. It is
gitignored and can be restored at any time with `make ref`
(`scripts/fetch-lightpanda.sh`).

This file records the measured difference so the checkout is not needed to plan
the remaining work. It was produced by probing every interface Lightpanda
implements (`src/browser/webapi/`) against this browser: **112 of 211 probed
interfaces were missing or threw.** Re-run the probe after `make ref` if a newer
Lightpanda has moved.

The three groups below are deliberately separated, because they are not
comparable work. Group A is bookkeeping. Group B is a genuine but bounded
ability gap. Group C is the real remainder.

## A. Capability works; only the constructor global is missing

The operation succeeds, but `typeof X` is `undefined`, so `x instanceof X`
throws and `X.name` is missing. Adding these is a few lines each.

| Interface | Capability that already works |
| :--- | :--- |
| `Range` | `document.createRange()` |
| `TreeWalker` | `document.createTreeWalker()` |
| `NodeIterator` | `document.createNodeIterator()` |
| `Selection` | `window.getSelection()` |
| `DOMRect`, `DOMRectReadOnly` | `getBoundingClientRect()` |
| `NodeList` | `querySelectorAll()` |
| `HTMLCollection` | `getElementsByTagName()` |
| `Performance` | `performance.now()` |
| `Storage` | `localStorage` / `sessionStorage` |
| `IDBFactory` | `indexedDB.open()` |
| `CustomElementRegistry` | `customElements.define()` |

## B. Synthetic event constructors that throw

`new Event(...)` and `new CustomEvent(...)` work. These do not, so a page cannot
build a synthetic event — which is a real ability, not just an `instanceof`
answer:

`MouseEvent`, `KeyboardEvent`, `PointerEvent`, `InputEvent`, `FocusEvent`,
`SubmitEvent`, `DragEvent`, `WheelEvent`, `MessageEvent`, `ProgressEvent`,
`ErrorEvent`, `CloseEvent`, `PopStateEvent`, `HashChangeEvent`, `StorageEvent`,
`UIEvent`.

Also absent as globals, though the events themselves dispatch: `CompositionEvent`,
`TouchEvent`, `ToggleEvent`, `BeforeUnloadEvent`, `PageTransitionEvent`,
`PromiseRejectionEvent`, `FormDataEvent`, `ErrorEvent`, and `EventCounts`
(`performance.eventCounts`).

## C. Genuinely absent capability

Grouped by area, roughly in order of how much a scraping browser would miss them.

**Networking and messaging**
- `EventSource` (server-sent events) — used by real-time pages.
- `MessageChannel`, `MessagePort`, `BroadcastChannel` — named today but empty;
  React's scheduler and several polyfills reach for `MessageChannel`.
- `XMLHttpRequestUpload` and `XMLHttpRequestEventTarget`.
- `CompressionStream`, `TextEncoderStream`, `TextDecoderStream`.

**Crypto**
- `crypto.subtle` (`SubtleCrypto`, `CryptoKey`): `digest` is the common use, and
  Lightpanda also ships AES, EC, HMAC, RSA and X25519. `crypto.getRandomValues`
  and `randomUUID` already work.

**Serialization and routing**
- `XMLSerializer`.
- `URLPattern`.
- `XMLDocument` as a constructor global (the XML *parser* works: `DOMParser`
  honours `text/xml`).

**Geometry and graphics**
- `DOMMatrix`, `DOMMatrixReadOnly`, `DOMPoint`, `DOMPointReadOnly`.
- `ImageData`, `OffscreenCanvas`, `Path2D`, `createImageBitmap`, `TextMetrics`,
  `CanvasGradient` (partly present via the 2D context).
- `SVGElement` and the `SVG*` interfaces.

**Storage**
- `caches` (`open`, `match`, `addAll`) — named today but empty.
- `navigator.storage` / `StorageManager` (`estimate`, `persist`).
- `CookieStore`, `QuotaExceededError`.

**Workers**
- `SharedWorker` (named today but empty), `DedicatedWorkerGlobalScope`,
  `SharedWorkerGlobalScope`, `WorkerGlobalScope`, `WorkerLocation`,
  `WorkerNavigator`. `Worker` itself is real.

**Platform**
- `Notification` (named today but empty), `geolocation` (same), `navigator.clipboard`.
- `scheduler.postTask`, `TaskController`, `TaskSignal`.
- `Animation`, `element.animate`, `getAnimations` (Web Animations).
- `VisualViewport`, the popover API (`showPopover`), `reportError`.
- `URL.createObjectURL` / `revokeObjectURL`.
- `PerformanceObserver`.
- `DataTransfer`, `DataTransferItem`, `DataTransferItemList`, `FileList`,
  `FileReaderSync` — `Blob`, `File`, `FileReader` and `FormData` are real.

**CSS object model**
- `CSSStyleSheet`, `CSSStyleDeclaration`, `CSSRule`, `CSSRuleList`,
  `StyleSheetList`, `MediaQueryList`, `FontFace` as constructor globals; the
  cascade, `styleSheets`, `getComputedStyle` and `document.fonts` work.

**Navigation API**
- `navigation`, `NavigationHistoryEntry`, `NavigationActivation`. The classic
  `history.pushState` / `popstate` path works.

**Smaller gaps**
- `DOMException` (aborts carry an error named `AbortError`, but not a real
  `DOMException`), `DocumentType`, `AbstractRange`, `StaticRange`,
  `XPathEvaluator`, `XPathExpression`, `ModelContext`.

## Not closable, or deliberately not closed

- **WebAssembly.** Lightpanda embeds V8 and has it; goja has no WASM engine.
  Structural, not a to-do. It is the one capability difference that cannot be
  closed by writing more bindings.
- **Agent mode and PandaScript** (`lightpanda agent`, `lightpanda run`). Needs an
  LLM in-process; this project's position is that the MCP server is the hook for
  an external agent instead.
- **Semantic tree** for agents. Lightpanda-specific; the MCP tools cover the same
  ground for our purposes.
- **Web Platform Tests.** Not a feature but a method: Lightpanda runs WPT and
  publishes the results, which is how it finds gaps like the ones this file
  lists. Adopting a WPT runner is the highest-value thing not yet borrowed.

## What is already at parity

Verified working, and worth not re-checking: DOM tree and traversal, the CSS
cascade, `getComputedStyle`, `styleSheets`, geometry and hit testing, XPath,
`fetch`/`XHR` with real bodies, `Blob`/`File`/`FileReader`/`FormData`/`Headers`/
`Request`/`Response`, streams, `WebSocket`, `Worker`, IndexedDB,
`<template>`, Shadow DOM (including declarative), custom elements with
lifecycle callbacks, `MutationObserver`/`IntersectionObserver`/`ResizeObserver`,
`history` and `location`, `structuredClone`, `AbortSignal`, `DOMParser` (HTML
and XML), ES modules with import maps, CORS, robots.txt, network interception,
proxy support, an adblocker, JSON-LD and microdata, canvas 2D with WebGL
contexts, PDF and screenshot output, CDP and WebDriver BiDi, MCP (with
per-session isolation), and the wait primitives (`--wait-until`, `--wait-ms`,
`--wait-script`, `--wait`).
