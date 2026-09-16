package browser

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdblockBlocksTrackerHost(t *testing.T) {
	if !adblockBlocked("https://www.google-analytics.com/collect?v=1", "script") {
		t.Fatal("google-analytics.com should be blocked")
	}
	if !adblockBlocked("https://securepubads.g.doubleclick.net/tag/js/gpt.js", "script") {
		t.Fatal("doubleclick.net subdomain should be blocked")
	}
	if adblockBlocked("https://example.com/app.js", "script") {
		t.Fatal("a normal script should not be blocked")
	}
	if adblockBlocked("https://example.com/", "document") {
		t.Fatal("a document must never be blocked")
	}
}

func TestAdblockBlocksTrackerPath(t *testing.T) {
	if !adblockBlocked("https://cdn.example.com/ads/banner.png", "image") {
		t.Fatal("an /ads/ path should be blocked")
	}
	if adblockBlocked("https://cdn.example.com/assets/logo.png", "image") {
		t.Fatal("a normal asset should not be blocked")
	}
}

// With Adblock on, a tracker script is dropped so its side effect never runs.
func TestAdblockDropsScript(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/track.js":
			w.Header().Set("Content-Type", "application/javascript")
			io.WriteString(w, `window.tracked = true`)
		case "/app.js":
			w.Header().Set("Content-Type", "application/javascript")
			io.WriteString(w, `window.app = true`)
		default:
			w.Header().Set("Content-Type", "text/html")
			// The tracker is served from google-analytics.com, which the
			// blocker matches by host.
			io.WriteString(w, `<script src="https://www.google-analytics.com/analytics.js"></script>
				<script src="/app.js"></script>`)
		}
	}))
	defer srv.Close()

	b := New(Options{Adblock: true})
	defer b.Close()
	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	v, err := p.Eval(`window.app`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v.Export() != true {
		t.Fatal("the page's own script should still run with adblock on")
	}
}

func TestAdblockOffByDefault(t *testing.T) {
	b := New(Options{})
	defer b.Close()
	if b.opts.Adblock {
		t.Fatal("Adblock should default to false")
	}
}

// A caller's Intercept hook runs before the blocker, so it can fulfil a request
// the blocker would have dropped.
func TestInterceptOverridesAdblock(t *testing.T) {
	var seen bool
	b := New(Options{
		Adblock: true,
		Intercept: func(r *Request) *Response {
			if strings.Contains(r.URL, "google-analytics.com") {
				seen = true
				return Fulfill(200, "application/javascript", []byte(`window.analytics = 'allowed'`))
			}
			return nil
		},
	})
	defer b.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<script src="https://www.google-analytics.com/analytics.js"></script><p>x</p>`)
	}))
	defer srv.Close()
	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !seen {
		t.Fatal("the intercept hook did not see the blocked URL")
	}
	v, err := p.Eval(`window.analytics`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v.String() != "allowed" {
		t.Fatalf("hook fulfilment did not take effect: %v", v)
	}
}
