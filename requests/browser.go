package requests

import "sync"

// BrowserRenderer is the browser a Request with Browser set is loaded in. The
// browser package installs one from its init, which is how this package stays
// free of a dependency on it: browser imports requests, so the reverse would be
// an import cycle.
type BrowserRenderer interface {
	// Send loads the request's URL and returns the page that settles, with the
	// rendered document as the response body.
	Send(req Request) (*Response, error)
	// Close releases the renderer's browser.
	Close()
}

var (
	browserMu      sync.RWMutex
	browserFactory func(*Session) BrowserRenderer
)

// UseBrowser installs the renderer that Request.Browser delegates to, one per
// session, so a page and a plain request on the same session share cookies.
// The browser package calls it from an init, and there is no reason to call it
// yourself: importing the browser package is what installs it.
func UseBrowser(newRenderer func(sess *Session) BrowserRenderer) {
	browserMu.Lock()
	defer browserMu.Unlock()
	browserFactory = newRenderer
}

// browserRenderer returns the installed factory, or nil when no browser is
// linked into the program.
func browserRenderer() func(*Session) BrowserRenderer {
	browserMu.RLock()
	defer browserMu.RUnlock()
	return browserFactory
}
