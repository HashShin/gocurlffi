package requests

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gocurlffi/impersonate"
)

func newTestServer() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/get", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"args":    r.URL.Query(),
			"headers": r.Header,
			"method":  r.Method,
		})
	})
	mux.HandleFunc("/post", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"body":        string(body),
			"contentType": r.Header.Get("Content-Type"),
			"method":      r.Method,
		})
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/get?from=redirect", http.StatusFound)
	})
	mux.HandleFunc("/redirect-post", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/post", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/setcookie", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc", Path: "/"})
		fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("/echo-cookie", func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("session")
		if err != nil {
			fmt.Fprint(w, "none")
			return
		}
		fmt.Fprint(w, c.Value)
	})
	mux.HandleFunc("/gzip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "text/plain")
		gz := gzip.NewWriter(w)
		gz.Write([]byte("hello gzip"))
		gz.Close()
	})
	mux.HandleFunc("/status/404", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		fmt.Fprint(w, "late")
	})
	mux.HandleFunc("/redirect-set-cookie", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "redirected", Value: "yes", Path: "/"})
		http.Redirect(w, r, "/echo-cookie2", http.StatusFound)
	})
	mux.HandleFunc("/echo-cookie2", func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("redirected")
		if err != nil {
			fmt.Fprint(w, "none")
			return
		}
		fmt.Fprint(w, c.Value)
	})
	mux.HandleFunc("/large", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		chunk := strings.Repeat("abcdefghij", 1000) // 10 KiB
		for i := 0; i < 30; i++ {
			io.WriteString(w, chunk)
		}
	})
	return httptest.NewServer(mux)
}

func TestGetParamsAndHeaders(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	s := NewSession(WithHeaders([]HeaderPair{{"X-Session", "1"}}))
	defer s.Close()
	rsp, err := s.Get(ts.URL+"/get",
		WithParams(map[string]string{"a": "1", "b": "two"}),
		WithHeader("X-Request", "yes"),
	)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Args    map[string][]string `json:"args"`
		Headers map[string][]string `json:"headers"`
		Method  string              `json:"method"`
	}
	if err := rsp.JSON(&out); err != nil {
		t.Fatal(err)
	}
	if got := out.Args["a"]; len(got) != 1 || got[0] != "1" {
		t.Errorf("args a = %v", got)
	}
	if got := out.Args["b"]; len(got) != 1 || got[0] != "two" {
		t.Errorf("args b = %v", got)
	}
	if out.Method != "GET" {
		t.Errorf("method = %s", out.Method)
	}
	if got := out.Headers["X-Session"]; len(got) == 0 || got[0] != "1" {
		t.Errorf("session header missing: %v", got)
	}
	if got := out.Headers["X-Request"]; len(got) == 0 || got[0] != "yes" {
		t.Errorf("request header missing: %v", got)
	}
	if rsp.StatusCode != 200 || !rsp.Ok {
		t.Errorf("status = %d ok=%v", rsp.StatusCode, rsp.Ok)
	}
}

func TestJSONPost(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	s := NewSession()
	defer s.Close()
	rsp, err := s.Post(ts.URL+"/post", WithJSON(map[string]any{"x": 1, "y": "z"}))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Body        string `json:"body"`
		ContentType string `json:"contentType"`
	}
	if err := rsp.JSON(&out); err != nil {
		t.Fatal(err)
	}
	if out.ContentType != "application/json" {
		t.Errorf("content type = %q", out.ContentType)
	}
	if out.Body != `{"x":1,"y":"z"}` {
		t.Errorf("body = %q", out.Body)
	}
}

func TestFormPost(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	s := NewSession()
	defer s.Close()
	rsp, err := s.Post(ts.URL+"/post", WithData(map[string]string{"a": "1", "b": "2"}))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Body        string `json:"body"`
		ContentType string `json:"contentType"`
	}
	rsp.JSON(&out)
	if out.ContentType != "application/x-www-form-urlencoded" {
		t.Errorf("content type = %q", out.ContentType)
	}
	if out.Body != "a=1&b=2" {
		t.Errorf("body = %q", out.Body)
	}
}

func TestRedirectsAndHistory(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	s := NewSession()
	defer s.Close()
	rsp, err := s.Get(ts.URL + "/redirect")
	if err != nil {
		t.Fatal(err)
	}
	if rsp.StatusCode != 200 {
		t.Fatalf("status = %d", rsp.StatusCode)
	}
	if len(rsp.History) != 1 {
		t.Fatalf("history = %d", len(rsp.History))
	}
	if rsp.History[0].StatusCode != 302 {
		t.Errorf("history status = %d", rsp.History[0].StatusCode)
	}
	if !strings.Contains(rsp.URL, "from=redirect") {
		t.Errorf("final url = %s", rsp.URL)
	}

	rsp2, err := s.Get(ts.URL+"/redirect", WithAllowRedirects(false))
	if err != nil {
		t.Fatal(err)
	}
	if rsp2.StatusCode != 302 || len(rsp2.History) != 0 {
		t.Errorf("no-follow: status=%d history=%d", rsp2.StatusCode, len(rsp2.History))
	}
}

func TestRedirectPostPreservedOn307(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	s := NewSession()
	defer s.Close()
	rsp, err := s.Post(ts.URL+"/redirect-post", WithData("hello"))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Body   string `json:"body"`
		Method string `json:"method"`
	}
	rsp.JSON(&out)
	if out.Method != "POST" || out.Body != "hello" {
		t.Errorf("307 should preserve method/body: %+v", out)
	}
}

func TestCookies(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	s := NewSession()
	defer s.Close()
	if _, err := s.Get(ts.URL + "/setcookie"); err != nil {
		t.Fatal(err)
	}
	if v, ok := s.Cookies().Get("session"); !ok || v != "abc" {
		t.Fatalf("session cookie = %q %v", v, ok)
	}
	rsp, err := s.Get(ts.URL + "/echo-cookie")
	if err != nil {
		t.Fatal(err)
	}
	if rsp.Text() != "abc" {
		t.Errorf("cookie echo = %q", rsp.Text())
	}

	// discard_cookies must not persist
	s2 := NewSession()
	defer s2.Close()
	if _, err := s2.Get(ts.URL+"/setcookie", WithDiscardCookies(true)); err != nil {
		t.Fatal(err)
	}
	if s2.Cookies().Len() != 0 {
		t.Errorf("discarded cookies leaked: %v", s2.Cookies().Map())
	}
}

func TestGzipDecoding(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	s := NewSession()
	defer s.Close()
	rsp, err := s.Get(ts.URL + "/gzip")
	if err != nil {
		t.Fatal(err)
	}
	if rsp.Text() != "hello gzip" {
		t.Errorf("decoded = %q", rsp.Text())
	}
}

func TestRaiseForStatus(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	s := NewSession()
	defer s.Close()
	if _, err := s.Get(ts.URL+"/status/404", WithRaiseForStatus(true)); err == nil {
		t.Fatal("expected error")
	} else if _, ok := err.(*HTTPError); !ok {
		t.Fatalf("error type = %T", err)
	}
	rsp, err := s.Get(ts.URL + "/status/404")
	if err != nil || rsp.StatusCode != 404 || rsp.Ok {
		t.Fatalf("404: err=%v status=%d ok=%v", err, rsp.StatusCode, rsp.Ok)
	}
}

func TestTimeout(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	s := NewSession()
	defer s.Close()
	_, err := s.Get(ts.URL+"/slow", WithTimeout(200*time.Millisecond))
	if err == nil {
		t.Fatal("expected timeout")
	}
	if !IsTimeout(err) {
		t.Fatalf("error type = %T (%v)", err, err)
	}
}

func TestSessionClosed(t *testing.T) {
	s := NewSession()
	s.Close()
	if _, err := s.Get("http://example.invalid/"); err == nil {
		t.Fatal("expected error after close")
	} else if _, ok := err.(*SessionClosed); !ok {
		t.Fatalf("error type = %T", err)
	}
}

func TestDefaultHeadersOverride(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	s := NewSession()
	defer s.Close()
	rsp, err := s.Get(ts.URL+"/get",
		WithImpersonate("chrome"),
		WithHeader("User-Agent", "my-agent"),
	)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Headers map[string][]string `json:"headers"`
	}
	rsp.JSON(&out)
	if got := out.Headers["User-Agent"]; len(got) == 0 || got[0] != "my-agent" {
		t.Errorf("user agent = %v", got)
	}
	// Browser default headers that the user did not override are still present.
	if got := out.Headers["Sec-Ch-Ua-Platform"]; len(got) == 0 {
		t.Errorf("expected impersonated headers, got %v", out.Headers)
	}
}

func TestRedirectSetCookieForwarded(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	s := NewSession()
	defer s.Close()
	rsp, err := s.Get(ts.URL + "/redirect-set-cookie")
	if err != nil {
		t.Fatal(err)
	}
	if rsp.Text() != "yes" {
		t.Fatalf("cookie set by redirect was not sent on the next hop: got %q", rsp.Text())
	}
}

func TestLargeResponseNotCancelled(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	s := NewSession()
	defer s.Close()
	// The 5s timeout is far longer than the transfer; a buggy early context
	// cancel would truncate the body with "context canceled".
	rsp, err := s.Get(ts.URL+"/large", WithTimeout(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(rsp.Content) != 300*1000 {
		t.Fatalf("body length = %d, want %d", len(rsp.Content), 300*1000)
	}
}

func TestAcceptEncodingKeepsBrowserPosition(t *testing.T) {
	p, _ := impersonate.Get("chrome131")
	h := buildFinalHeaders(NewHeaders(nil), "", p)
	items := h.MultiItems()
	// The preset lists accept-encoding second to last, after sec-fetch-dest.
	if items[0].Name == "Accept-Encoding" {
		t.Fatalf("accept-encoding must not be reordered to the front: %v", items)
	}
	wantIdx := -1
	for i, it := range items {
		if strings.EqualFold(it.Name, "Accept-Encoding") {
			wantIdx = i
		}
	}
	if wantIdx == -1 {
		t.Fatal("accept-encoding missing")
	}
	if !strings.EqualFold(items[wantIdx-1].Name, "Sec-Fetch-Dest") {
		t.Errorf("accept-encoding position changed: %v", items)
	}
}
