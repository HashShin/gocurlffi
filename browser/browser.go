// Package browser is a pure-Go, dependency-light headless browser for
// scraping and automation.
//
// It is a Go answer to the Zig Lightpanda browser (./browser/browser), but
// deliberately built only from portable, pure-Go parts so it cross-compiles
// and runs everywhere Go does, including Android/Termux and musl systems:
//
//   - HTML5 parsing + serialization: golang.org/x/net/html
//   - CSS selectors: github.com/andybalholm/cascadia
//   - JavaScript: github.com/dop251/goja (an ECMAScript engine in pure Go)
//   - Network: gocurlffi/requests, so pages are fetched with real browser
//     TLS/JA3 and HTTP/2 fingerprints.
//
// The DOM is the x/net/html node tree itself. That keeps the port small and
// correct: the same nodes that were parsed are the ones scripts mutate.
package browser

import (
	"net/url"
	"sync"
	"time"

	"gocurlffi/requests"
)

// Options configure a Browser or a one-off Page.
type Options struct {
	// Impersonate selects the TLS/HTTP fingerprint target (see
	// gocurlffi/impersonate). Empty means the requests default.
	Impersonate string

	// UserAgent overrides navigator.userAgent and the request UA. When empty
	// the impersonation preset's UA is used.
	UserAgent string

	// Headers are added to every request.
	Headers map[string]string

	// Timeout per network request. Zero means no explicit timeout.
	Timeout time.Duration

	// MaxRedirects for subresource and document requests.
	MaxRedirects int

	// RunScripts controls JavaScript execution. Default true.
	RunScripts *bool

	// LoadImages is currently ignored (no image decoding); kept for API parity.
	LoadImages bool

	// JavaScriptTimeout bounds a single script evaluation. Default 10s.
	JavaScriptTimeout time.Duration

	// LoadTimeout bounds the whole script-loading phase (document scripts and
	// external bundles). Default 30s. A slow or hanging subresource is dropped
	// once the budget is spent, so a page always finishes loading.
	LoadTimeout time.Duration

	// TimerBudget bounds how long the loader waits for pending timers after
	// DOMContentLoaded and again after load. Default 2s each. Lower it to
	// finish sooner at the cost of missing content rendered by timers.
	TimerBudget time.Duration

	// Console receives console.* output. nil discards it.
	Console func(level, message string)

	// Insecure skips TLS verification when true.
	Insecure bool

	// Proxy routes every request (document, scripts, fetch, XHR) through the
	// given proxy URL, for example "http://127.0.0.1:8080". Empty means a
	// direct connection. It is passed straight to the transport, so socks5://
	// and authenticated http:// proxies work.
	Proxy string

	// ObeyRobots makes the browser honour robots.txt: before any request it
	// fetches and caches the origin's robots.txt and skips a disallowed URL,
	// yielding a synthetic 403 that does not stop the load. Off by default,
	// so the fast path pays nothing.
	ObeyRobots bool

	// Intercept, when set, is offered every request before it is sent (document,
	// scripts, stylesheets, images, fonts, fetch and XHR). Returning nil lets
	// the request continue; returning Block drops it; returning Fulfill answers
	// it from the hook. The hook runs synchronously on the request path.
	Intercept func(*Request) *Response

	// CORS, when set, enforces cross-origin rules on fetch and XHR: a
	// cross-origin request carries an Origin header and a preflight when it is
	// not simple, and a response without a matching Access-Control-Allow-Origin
	// is rejected. Off by default, matching the original's experimental flag,
	// and off means fetch behaves as before.
	CORS bool

	// Debug logs page-load phases to stderr.
	Debug bool
}

func (o Options) scriptsEnabled() bool { return o.RunScripts == nil || *o.RunScripts }

// Browser is a reusable browsing context: it owns cookies and an
// impersonating HTTP session so multiple pages share state.
type Browser struct {
	opts Options
	sess *requests.Session

	// robots caches one parsed robots.txt per origin, only consulted when
	// Options.ObeyRobots is set.
	robotsMu sync.Mutex
	robots   map[string]*robotsRules
}

// New creates a Browser with the given options.
func New(opts Options) *Browser {
	sopts := []requests.Option{}
	if opts.Impersonate != "" {
		sopts = append(sopts, requests.WithImpersonate(opts.Impersonate))
	}
	if opts.Timeout > 0 {
		sopts = append(sopts, requests.WithTimeout(opts.Timeout))
	}
	if opts.Insecure {
		sopts = append(sopts, requests.WithVerify(false))
	}
	if opts.MaxRedirects > 0 {
		sopts = append(sopts, requests.WithMaxRedirects(opts.MaxRedirects))
	}
	if opts.Proxy != "" {
		sopts = append(sopts, requests.WithProxy(opts.Proxy))
	}
	return &Browser{
		opts:   opts,
		sess:   requests.NewSession(sopts...),
		robots: map[string]*robotsRules{},
	}
}

// Close releases the underlying HTTP session.
func (b *Browser) Close() { b.sess.Close() }

// Options returns a copy of the browser options.
func (b *Browser) Options() Options { return b.opts }

// get performs a document GET through the impersonating transport.
func (b *Browser) get(rawURL string, headers map[string]string) (*requests.Response, error) {
	return b.fetch(rawURL, headers, "document")
}

// fetch performs a GET for a named resource type, offering it to the intercept
// hook first.
func (b *Browser) fetch(rawURL string, headers map[string]string, rtype string) (*requests.Response, error) {
	hs := map[string]string{}
	for k, v := range b.opts.Headers {
		hs[k] = v
	}
	for k, v := range headers {
		hs[k] = v
	}
	return b.request("GET", rawURL, hs, nil, rtype)
}

// request is the single path every network access goes through: page
// subresources, fetch and XHR. It offers the request to the intercept hook, and
// applies robots.txt to crawler-visible loads (not page-initiated fetch).
func (b *Browser) request(method, rawURL string, headers map[string]string, body []byte, rtype string) (*requests.Response, error) {
	if r := b.intercept(method, rawURL, headers, rtype); r != nil {
		return r.toRequests(rawURL), nil
	}
	crawlerVisible := rtype != "fetch" && rtype != "xhr"
	if b.opts.ObeyRobots && crawlerVisible && method == "GET" && !b.robotsAllowed(rawURL) {
		resp := requests.NewResponse()
		resp.URL = rawURL
		resp.StatusCode = 403
		resp.Reason = "Forbidden by robots.txt"
		resp.Ok = false
		return resp, nil
	}
	opts := []requests.Option{}
	if len(headers) > 0 {
		opts = append(opts, requests.WithHeaders(headers))
	}
	if len(body) > 0 {
		opts = append(opts, requests.WithContent(body))
	}
	return b.sess.Request(method, rawURL, opts...)
}

// resolveURL resolves a possibly-relative reference against a base URL.
func resolveURL(base, ref string) string {
	if ref == "" {
		return base
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	if u.IsAbs() {
		return ref
	}
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	return b.ResolveReference(u).String()
}
