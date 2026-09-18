package browser

import (
	"sync"

	"github.com/HashShin/shade/requests"
)

// The browser plugs itself in behind requests.Request.Browser. requests cannot
// import this package - this one imports it - so the dependency is inverted:
// requests exposes a factory and this file installs one, which is why importing
// the module root is enough to make
//
//	rsp, err := Send(Request{URL: url, Browser: true})
//
// load the page in a browser.
func init() {
	requests.UseBrowser(func(sess *requests.Session) requests.BrowserRenderer {
		return &renderer{sess: sess}
	})
}

// renderer is the requests.BrowserRenderer for one session: a browser created
// on first use and reused, so its pages keep the session's cookies.
type renderer struct {
	sess *requests.Session

	mu sync.Mutex
	b  *Browser
}

// Send loads a request's URL and returns the settled page as a response. The
// body is the rendered document, not the bytes the server sent: that is the
// whole point of asking for the browser.
func (r *renderer) Send(req requests.Request) (*requests.Response, error) {
	b, oneOff := r.browserFor(req)
	if oneOff {
		defer b.Close()
	}

	p, err := b.Open(req.URL)
	if err != nil {
		return nil, err
	}
	// The page is dropped, not closed: Page.Close closes the browser behind
	// it, which here is the session's.
	resp := p.Response()
	if resp == nil {
		resp = requests.NewResponse()
	}
	out := *resp
	out.Content = []byte(p.HTML())
	out.Request = &req
	return &out, nil
}

// Close releases the session's browser.
func (r *renderer) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.b != nil {
		r.b.Close()
		r.b = nil
	}
}

func (r *renderer) browser() *Browser {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.b == nil {
		r.b = New(Options{Session: r.sess})
	}
	return r.b
}

// browserFor returns the session's browser, or one loaded with the request's
// overrides and a flag saying it is the caller's to close. They are carried as
// load options rather than as browser options because the browser is reused:
// this way the fingerprint a request asks for reaches every resource the page
// fetches, and the session's cookies are still the ones it uses.
func (r *renderer) browserFor(req requests.Request) (b *Browser, oneOff bool) {
	var load []requests.Option
	if req.Impersonate != "" {
		load = append(load, requests.WithImpersonate(req.Impersonate))
	}
	if req.Proxy != "" {
		load = append(load, requests.WithProxy(req.Proxy))
	}
	if req.Timeout > 0 {
		load = append(load, requests.WithTimeout(req.Timeout))
	}
	if len(load) == 0 && req.Headers == nil {
		return r.browser(), false
	}
	opts := Options{Session: r.sess, LoadOptions: load}
	if req.Headers != nil {
		opts.Headers = requests.NewHeaders(req.Headers).Items()
	}
	return New(opts), true
}
