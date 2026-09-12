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

	// Console receives console.* output. nil discards it.
	Console func(level, message string)

	// Insecure skips TLS verification when true.
	Insecure bool
}

func (o Options) scriptsEnabled() bool { return o.RunScripts == nil || *o.RunScripts }

// Browser is a reusable browsing context: it owns cookies and an
// impersonating HTTP session so multiple pages share state.
type Browser struct {
	opts Options
	sess *requests.Session
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
	return &Browser{opts: opts, sess: requests.NewSession(sopts...)}
}

// Close releases the underlying HTTP session.
func (b *Browser) Close() { b.sess.Close() }

// Options returns a copy of the browser options.
func (b *Browser) Options() Options { return b.opts }

// request performs a GET through the impersonating transport.
func (b *Browser) get(rawURL string, headers map[string]string) (*requests.Response, error) {
	opts := []requests.Option{}
	hs := map[string]string{}
	for k, v := range b.opts.Headers {
		hs[k] = v
	}
	for k, v := range headers {
		hs[k] = v
	}
	if len(hs) > 0 {
		opts = append(opts, requests.WithHeaders(hs))
	}
	return b.sess.Get(rawURL, opts...)
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
