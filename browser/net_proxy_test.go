package browser

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A proxy receives the absolute URL as the request target, so a request that
// goes through it carries the full "http://host/path" in r.RequestURI. That is
// how the test tells "went through the proxy" from "went direct".
func TestProxyRoutesRequests(t *testing.T) {
	var proxied int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&proxied, 1)
		if !strings.HasPrefix(r.RequestURI, "http://") {
			t.Errorf("proxy got RequestURI %q, want an absolute URL", r.RequestURI)
		}
		io.WriteString(w, `<html><body><p id="via">proxy</p></body></html>`)
	}))
	defer proxy.Close()

	b := New(Options{Proxy: proxy.URL})
	defer b.Close()
	off := false
	b.opts.RunScripts = &off
	p, err := b.Open("http://target.example/page")
	if err != nil {
		t.Fatalf("Open through proxy: %v", err)
	}
	if got := p.Text(); !strings.Contains(got, "proxy") {
		t.Fatalf("rendered content = %q, want it to contain %q", got, "proxy")
	}
	if atomic.LoadInt32(&proxied) == 0 {
		t.Fatal("the proxy was never hit")
	}
}

// With no Proxy set, the fast path must not touch a proxy at all.
func TestNoProxyByDefault(t *testing.T) {
	b := New(Options{})
	defer b.Close()
	if b.opts.Proxy != "" {
		t.Fatalf("Proxy default = %q, want empty", b.opts.Proxy)
	}
}
