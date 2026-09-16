package browser

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// A browser is a shared context: cookies set by a response, and cookies set by
// page JavaScript, must be sent on later requests.
func TestBrowserSharesCookies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/set":
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "abc123", Path: "/"})
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body>set
<script>document.cookie = "fromjs=yes; path=/";</script></body></html>`))
		case "/echo":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<html><body id="k">%s</body></html>`, r.Header.Get("Cookie"))
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body>ok</body></html>`))
		}
	}))
	defer srv.Close()

	b := New(Options{})
	defer b.Close()

	if _, err := b.Open(srv.URL + "/set"); err != nil {
		t.Fatalf("open /set: %v", err)
	}
	p2, err := b.Open(srv.URL + "/echo")
	if err != nil {
		t.Fatalf("open /echo: %v", err)
	}
	got := textContent(p2.Query("#k"))
	if !strings.Contains(got, "sid=abc123") {
		t.Fatalf("response cookie not sent on next request: %q", got)
	}
	if !strings.Contains(got, "fromjs=yes") {
		t.Fatalf("document.cookie value not sent on next request: %q", got)
	}
}

// Pages may be loaded concurrently from one Browser; the shared session and
// cookie jar must be race-free (run with -race).
func TestConcurrentPages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := r.URL.Query().Get("i")
		http.SetCookie(w, &http.Cookie{Name: "n" + i, Value: i, Path: "/"})
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><div id="a">x</div>
<script>document.getElementById('a').setAttribute('data-js','1');</script></body></html>`))
	}))
	defer srv.Close()

	b := New(Options{})
	defer b.Close()

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, err := b.Open(fmt.Sprintf("%s/?i=%d", srv.URL, i))
			if err != nil {
				errs[i] = err
				return
			}
			if Attr(p.Query("#a"), "data-js") != "1" {
				errs[i] = fmt.Errorf("page %d did not run its script", i)
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("page %d: %v", i, err)
		}
	}
	if b.sess.Cookies().Len() != n {
		t.Fatalf("cookie jar has %d cookies, want %d", b.sess.Cookies().Len(), n)
	}
}

// A <base href> changes how relative URLs resolve for links, element
// properties and fetch.
func TestBaseHref(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/root/":
			fmt.Fprintf(w, `<html><head><base href="%s/root/"></head><body>
<div id="app">loading</div><a href="/one">rel</a>
<script>
  fetch('data').then(function (r) { return r.text(); }).then(function (t) {
    document.getElementById('app').textContent = t;
  });
</script></body></html>`, srvURL(r))
		case "/root/data":
			_, _ = w.Write([]byte("from-base"))
		default:
			_, _ = w.Write([]byte("wrong"))
		}
	}))
	defer srv.Close()

	b := New(Options{})
	defer b.Close()
	p, err := b.Open(srv.URL + "/root/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// fetch('data') must resolve against the base, not the document URL
	if got := textContent(p.Query("#app")); got != "from-base" {
		t.Fatalf("fetch did not use base href: app=%q", got)
	}
	links := p.Links()
	if len(links) != 1 || links[0].Href != srv.URL+"/one" {
		t.Fatalf("link href = %+v, want %s/one", links, srv.URL)
	}
	v, _ := p.Eval("document.querySelector('a').href")
	if v.String() != srv.URL+"/one" {
		t.Fatalf("element.href = %q, want %s/one", v.String(), srv.URL)
	}
}

func srvURL(r *http.Request) string {
	return "http://" + r.Host
}

// Non-UTF-8 documents must be decoded, whether the charset comes from the
// Content-Type header or a <meta> tag.
func TestCharsetDecoding(t *testing.T) {
	const want = "こんにちは世界"
	enc := japanese.ShiftJIS.NewEncoder()
	body, _, err := transform.Bytes(enc, []byte("<html><body><p>"+want+"</p></body></html>"))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/meta" {
			w.Header().Set("Content-Type", "text/html")
			head := []byte("<html><head><meta charset=\"shift_jis\"></head><body><p>" + want + "</p></body></html>")
			encBody, _, _ := transform.Bytes(enc, head)
			_, _ = w.Write(encBody)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=shift_jis")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	b := New(Options{})
	defer b.Close()
	for _, path := range []string{"/header", "/meta"} {
		p, err := b.Open(srv.URL + path)
		if err != nil {
			t.Fatalf("%s: open: %v", path, err)
		}
		if got := p.Text(); !strings.Contains(got, want) {
			t.Fatalf("%s: decoded text %q does not contain %q", path, got, want)
		}
	}
}

// Reloading a page must run its external scripts again.
func TestReloadRunsExternalScripts(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ext.js" {
			atomic.AddInt32(&hits, 1)
			w.Header().Set("Content-Type", "application/javascript")
			_, _ = w.Write([]byte(`document.body.setAttribute('data-ran', 'yes');`))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><script src="/ext.js"></script></body></html>`))
	}))
	defer srv.Close()

	b := New(Options{})
	defer b.Close()
	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if Attr(p.Query("body"), "data-ran") != "yes" {
		t.Fatalf("script did not run on first load")
	}
	if err := p.Load(srv.URL + "/"); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("external script fetched %d times, want 2", got)
	}
	if Attr(p.Query("body"), "data-ran") != "yes" {
		t.Fatalf("script did not run on reload")
	}
}
