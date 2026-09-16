package browser

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func corsResource(t *testing.T, allowOrigin bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if allowOrigin {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		io.WriteString(w, "payload")
	}))
}

func corsPage(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<p>page</p>`)
	}))
}

func fetchResult(t *testing.T, p *Page, url string) string {
	t.Helper()
	src := `(function(){
		window.__cors = 'pending';
		fetch('` + url + `').then(function(r){ return r.text() })
			.then(function(t){ window.__cors = 'ok:' + t })
			.catch(function(e){ window.__cors = 'err' });
		return window.__cors;
	})()`
	if _, err := p.Eval(src); err != nil {
		t.Fatalf("Eval: %v", err)
	}
	v, err := p.Eval(`window.__cors`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	return v.String()
}

func TestCORSBlocksResponseWithoutHeader(t *testing.T) {
	res := corsResource(t, false)
	defer res.Close()
	page := corsPage(t)
	defer page.Close()

	b := New(Options{CORS: true})
	defer b.Close()
	p, err := b.Open(page.URL)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := fetchResult(t, p, res.URL+"/x"); got != "err" {
		t.Fatalf("fetch result = %q, want err (blocked by CORS)", got)
	}
}

func TestCORSAllowsResponseWithHeader(t *testing.T) {
	res := corsResource(t, true)
	defer res.Close()
	page := corsPage(t)
	defer page.Close()

	b := New(Options{CORS: true})
	defer b.Close()
	p, err := b.Open(page.URL)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := fetchResult(t, p, res.URL+"/x"); got != "ok:payload" {
		t.Fatalf("fetch result = %q, want ok:payload", got)
	}
}

func TestCORSOffByDefault(t *testing.T) {
	res := corsResource(t, false)
	defer res.Close()
	page := corsPage(t)
	defer page.Close()

	b := New(Options{})
	defer b.Close()
	p, err := b.Open(page.URL)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := fetchResult(t, p, res.URL+"/x"); got != "ok:payload" {
		t.Fatalf("with CORS off fetch should still work, got %q", got)
	}
}
