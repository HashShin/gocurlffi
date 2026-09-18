package browser

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HashShin/shade/impersonate"
)

// Get is the one-call form for a single page: a Browser and an Open collapsed
// into one line, which is the shortest way to use this package.
func TestGetOpensAPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>hello</title></head>
			<body><p id="t">from the server</p></body></html>`))
	}))
	defer srv.Close()

	p, err := Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer p.Close()

	if got := p.Title(); got != "hello" {
		t.Errorf("Title = %q, want hello", got)
	}
	if got := p.Text(); !strings.Contains(got, "from the server") {
		t.Errorf("Text = %q, want it to contain the body", got)
	}
}

// The target is optional and is applied when given.
func TestGetHonoursTheTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>x</body></html>"))
	}))
	defer srv.Close()

	defaultPage, err := Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer defaultPage.Close()

	targeted, err := Get(srv.URL, impersonate.Chrome131)
	if err != nil {
		t.Fatalf("Get with target: %v", err)
	}
	defer targeted.Close()

	if !strings.Contains(targeted.UserAgent(), "Chrome/131") {
		t.Errorf("User-Agent = %q, want the Chrome 131 preset", targeted.UserAgent())
	}
	if defaultPage.UserAgent() == targeted.UserAgent() {
		t.Error("the default and the targeted page report the same user agent")
	}
}

// A page from Get owns its Browser, so Close is the whole cleanup. It must be
// safe to call, and safe to call twice, since it is deferred.
func TestPageCloseIsSafe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>x</body></html>"))
	}))
	defer srv.Close()

	p, err := Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	p.Close()
	p.Close()
}

// A failed load must not leave the dedicated Browser's session open.
func TestGetClosesItsBrowserOnFailure(t *testing.T) {
	// A port nothing is listening on, so the load fails without a network.
	if _, err := Get("http://127.0.0.1:1/"); err == nil {
		t.Fatal("expected the load to fail")
	}
}

// Get composes with the Page surface: everything a page from New can do.
func TestGetPageIsAFullPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><a href="/next">next</a></body></html>`))
	}))
	defer srv.Close()

	p, err := Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer p.Close()

	links := p.Links()
	if len(links) != 1 || !strings.HasSuffix(links[0].Href, "/next") {
		t.Errorf("Links = %v, want one link to /next", links)
	}
	if p.Query("a") == nil {
		t.Error("Query(a) found nothing")
	}
}
