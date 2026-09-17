package browser

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/HashShin/gocurlffi/requests"
)

// Send with Browser set loads the page in the browser: the body is the document
// after its scripts have run, not the bytes the server sent. The script writes
// into the body, so a response that still contains the placeholder came from
// the fast path instead.
func TestSendRendersThroughTheBrowser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Rendered</title></head>
			<body><p id="out">before</p>
			<script>document.getElementById('out').textContent = 'after';</script>
			</body></html>`))
	}))
	defer srv.Close()

	sess := requests.NewSession()
	defer sess.Close()
	rsp, err := sess.Send(requests.Request{URL: srv.URL, Browser: true})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	body := rsp.Text()
	if !strings.Contains(body, "after") {
		t.Errorf("body = %q, want the script's output", body)
	}
	if strings.Contains(body, "before") {
		t.Errorf("body still holds the pre-script placeholder:\n%s", body)
	}
	if rsp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", rsp.StatusCode)
	}
	if rsp.URL != srv.URL {
		t.Errorf("URL = %q, want %q", rsp.URL, srv.URL)
	}
}

// A browser request on a session shares that session's cookies, so a login made
// through the fast client is visible to the page and vice versa.
func TestBrowserSharesTheSessionCookies(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Cookie"))
		http.SetCookie(w, &http.Cookie{Name: "frompage", Value: "yes", Path: "/"})
		_, _ = w.Write([]byte("<!doctype html><html><body>hi</body></html>"))
	}))
	defer srv.Close()

	sess := requests.NewSession()
	defer sess.Close()
	sess.SetCookies(map[string]string{"sess": "abc"})
	if _, err := sess.Send(requests.Request{URL: srv.URL, Browser: true}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(seen) == 0 || !strings.Contains(seen[0], "sess=abc") {
		t.Errorf("the page did not receive the session cookie: %v", seen)
	}
	if got, _ := sess.Cookies().Get("frompage"); got != "yes" {
		t.Errorf("cookie set by the page = %q, want it in the session", got)
	}
}

// The runner prints the same page either way: --render changes whether scripts
// run, not the transport. A browser request through a session with no
// impersonation must still work, and the response must carry the page's own
// status rather than a synthetic one.
func TestBrowserKeepsThePageStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("<!doctype html><html><body>tea</body></html>"))
	}))
	defer srv.Close()

	rsp, err := requests.Send(requests.Request{URL: srv.URL, Browser: true})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if rsp.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d, want %d", rsp.StatusCode, http.StatusTeapot)
	}
}

// A fingerprint named on a browser request reaches the page, exactly as it does
// on the fast path: the two share one transport, so asking for chrome150 must
// not silently fall back to the session's default. The user agent is the
// cheapest proof of which preset was used, and comparing the two paths keeps
// the test from pinning a version.
func TestBrowserRequestHonoursTheImpersonation(t *testing.T) {
	uas := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uas <- r.UserAgent()
		_, _ = w.Write([]byte("<!doctype html><html><body>hi</body></html>"))
	}))
	defer srv.Close()

	fast, err := requests.Send(requests.Request{URL: srv.URL, Impersonate: "chrome150"})
	if err != nil {
		t.Fatalf("fast Send: %v", err)
	}
	_ = fast
	rendered, err := requests.Send(requests.Request{URL: srv.URL, Impersonate: "chrome150", Browser: true})
	if err != nil {
		t.Fatalf("browser Send: %v", err)
	}
	_ = rendered

	fastUA, browserUA := <-uas, <-uas
	if fastUA == "" || browserUA == "" {
		t.Fatalf("user agents: fast %q, browser %q", fastUA, browserUA)
	}
	if fastUA != browserUA {
		t.Errorf("fast path sent %q and the browser sent %q, want the same preset", fastUA, browserUA)
	}
}

// Params and headers on a browser request reach the load, not just the fast
// path: the request is the same value either way.
func TestBrowserRequestCarriesParamsAndHeaders(t *testing.T) {
	var gotURL, gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		gotHeader = r.Header.Get("X-Test")
		_, _ = w.Write([]byte("<!doctype html><html><body>hi</body></html>"))
	}))
	defer srv.Close()

	_, err := requests.Send(requests.Request{
		URL:     srv.URL,
		Params:  map[string]string{"q": "go"},
		Headers: []string{"X-Test: yes"},
		Browser: true,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotURL != "/?q=go" {
		t.Errorf("the page was fetched at %q, want the query appended", gotURL)
	}
	if gotHeader != "yes" {
		t.Errorf("X-Test = %q, want the request's header", gotHeader)
	}
}

// The option form reaches the same path as the Request field, so
// Get(url, WithBrowser()) renders too.
func TestWithBrowserOptionRenders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><html><body><p id="out">before</p>
			<script>document.getElementById('out').textContent = 'after';</script>
			</body></html>`))
	}))
	defer srv.Close()

	sess := requests.NewSession()
	defer sess.Close()
	rsp, err := sess.Get(srv.URL, requests.WithBrowser())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(rsp.Text(), "after") {
		t.Errorf("body = %q, want the script's output", rsp.Text())
	}
}

// A browser request and a fast one sit side by side on one session: which path
// a request takes is a field on the request, so a program renders one page and
// scrapes the next without a second client, a second import, or a re-login.
// The same URL is fetched both ways here, so the raw body proves the field
// decided and not the session's state.
func TestBrowserAndFastPathsInterleave(t *testing.T) {
	const page = `<!doctype html><html><body><p id="out">raw</p>
		<script>document.getElementById('out').textContent = 'rendered';</script>
		</body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	sess := requests.NewSession()
	defer sess.Close()

	// A base request: the whole switch is one field.
	base := requests.Request{URL: srv.URL, Impersonate: "chrome150"}

	fast, err := sess.Send(base)
	if err != nil {
		t.Fatalf("fast Send: %v", err)
	}
	if !strings.Contains(fast.Text(), `id="out">raw`) {
		t.Errorf("the plain request is not the bytes the server sent:\n%s", fast.Text())
	}

	base.Browser = true
	rendered, err := sess.Send(base)
	if err != nil {
		t.Fatalf("browser Send: %v", err)
	}
	if !strings.Contains(rendered.Text(), `id="out">rendered`) {
		t.Errorf("the browser request did not run scripts:\n%s", rendered.Text())
	}

	// Back to the fast path on the same session, and it must still be fast: a
	// session that has loaded a browser page does not become one.
	base.Browser = false
	again, err := sess.Send(base)
	if err != nil {
		t.Fatalf("second fast Send: %v", err)
	}
	if !strings.Contains(again.Text(), `id="out">raw`) {
		t.Errorf("the fast path rendered after a browser load:\n%s", again.Text())
	}
	if again.URL != srv.URL || again.StatusCode != 200 {
		t.Errorf("fast response = %s %d, want the page", again.URL, again.StatusCode)
	}
}

// The two paths agree on what they send: the same request, with only the
// Browser field flipped, must carry the same user agent and headers. Otherwise
// switching a request between the paths would change its fingerprint.
func TestSwitchingPathsKeepsTheRequestIdentical(t *testing.T) {
	type seen struct{ ua, accept string }
	got := make(chan seen, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- seen{ua: r.UserAgent(), accept: r.Header.Get("Accept")}
		_, _ = w.Write([]byte("<!doctype html><html><body>hi</body></html>"))
	}))
	defer srv.Close()

	base := requests.Request{URL: srv.URL, Impersonate: "chrome150", Headers: []string{"Accept: text/plain"}}
	if _, err := requests.Send(base); err != nil {
		t.Fatalf("fast Send: %v", err)
	}
	base.Browser = true
	if _, err := requests.Send(base); err != nil {
		t.Fatalf("browser Send: %v", err)
	}
	fast, rendered := <-got, <-got
	if fast.ua != rendered.ua {
		t.Errorf("user agent: fast %q, browser %q", fast.ua, rendered.ua)
	}
	if fast.accept != rendered.accept {
		t.Errorf("Accept: fast %q, browser %q", fast.accept, rendered.accept)
	}
}

// Side by side also means at the same time: a browser load and a fast request
// can run on one session concurrently, which is the shape of a crawler that
// renders some pages and scrapes the rest.
func TestPathsRunConcurrently(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<!doctype html><html><body>ok</body></html>"))
	}))
	defer srv.Close()

	sess := requests.NewSession()
	defer sess.Close()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			rsp, err := sess.Send(requests.Request{URL: srv.URL})
			if err != nil || rsp.StatusCode != 200 {
				t.Errorf("fast: %v %v", rsp, err)
			}
		}()
		go func() {
			defer wg.Done()
			rsp, err := sess.Send(requests.Request{URL: srv.URL, Browser: true})
			if err != nil || rsp.StatusCode != 200 {
				t.Errorf("browser: %v %v", rsp, err)
			}
		}()
	}
	wg.Wait()
}

// Browse is the same path as Send with Browser set, spelled so a call site that
// mixes the two says which is which.
func TestBrowseIsTheNamedBrowserPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><html><body><p id="out">raw</p>
			<script>document.getElementById('out').textContent = 'rendered';</script>
			</body></html>`))
	}))
	defer srv.Close()

	sess := requests.NewSession()
	defer sess.Close()
	req := requests.Request{URL: srv.URL, Impersonate: "chrome150"}

	fast, err := sess.Send(req)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !strings.Contains(fast.Text(), `id="out">raw`) {
		t.Errorf("Send ran scripts:\n%s", fast.Text())
	}

	// The caller's value is untouched: Browse takes a copy.
	rendered, err := sess.Browse(req)
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if !strings.Contains(rendered.Text(), `id="out">rendered`) {
		t.Errorf("Browse did not run scripts:\n%s", rendered.Text())
	}
	if req.Browser {
		t.Errorf("Browse set Browser on the caller's request")
	}

	// The same thing without a session.
	if _, err := requests.Browse(requests.Request{URL: srv.URL}); err != nil {
		t.Fatalf("package-level Browse: %v", err)
	}
}
