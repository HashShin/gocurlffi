package browser

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func interceptServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/style.css":
			w.Header().Set("Content-Type", "text/css")
			io.WriteString(w, "#x{color:red}")
		case "/app.js":
			w.Header().Set("Content-Type", "application/javascript")
			io.WriteString(w, `window.ran = (window.ran||0)+1`)
		default:
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<html><head><link rel="stylesheet" href="/style.css"></head>
				<body><script src="/app.js"></script><p id="x">hi</p></body></html>`)
		}
	}))
}

func TestInterceptSeesResourceTypes(t *testing.T) {
	srv := interceptServer(t)
	defer srv.Close()

	var mu sync.Mutex
	seen := map[string]int{}
	b := New(Options{Intercept: func(r *Request) *Response {
		mu.Lock()
		seen[r.ResourceType]++
		mu.Unlock()
		return nil
	}})
	defer b.Close()
	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Stylesheets are fetched lazily, on the first style-dependent call.
	if _, err := p.Eval(`getComputedStyle(document.getElementById('x')).color`); err != nil {
		t.Fatalf("Eval: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, want := range []string{"document", "script", "stylesheet"} {
		if seen[want] == 0 {
			t.Errorf("hook never saw a %q request; saw %v", want, seen)
		}
	}
}

func TestInterceptBlocksScript(t *testing.T) {
	srv := interceptServer(t)
	defer srv.Close()

	b := New(Options{Intercept: func(r *Request) *Response {
		if r.ResourceType == "script" {
			return Block()
		}
		return nil
	}})
	defer b.Close()
	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	v, err := p.Eval(`window.ran`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v.Export() != nil {
		t.Fatalf("blocked script ran: window.ran = %v", v)
	}
}

func TestInterceptFulfillsScript(t *testing.T) {
	srv := interceptServer(t)
	defer srv.Close()

	b := New(Options{Intercept: func(r *Request) *Response {
		if strings.HasSuffix(r.URL, "/app.js") {
			return Fulfill(200, "application/javascript", []byte(`window.ran = 99`))
		}
		return nil
	}})
	defer b.Close()
	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	v, err := p.Eval(`window.ran`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.ToInteger(); got != 99 {
		t.Fatalf("window.ran = %d, want the fulfilled script's 99", got)
	}
}

func TestInterceptFastPathUntouched(t *testing.T) {
	b := New(Options{})
	defer b.Close()
	if b.opts.Intercept != nil {
		t.Fatal("Intercept should default to nil")
	}
}
