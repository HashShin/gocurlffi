package browser

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
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
