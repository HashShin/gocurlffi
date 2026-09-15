package browser

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func frameServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/frame":
			io.WriteString(w, `<html><body><p id="inner">inside</p><a href="/link">L</a></body></html>`)
		case "/mid":
			io.WriteString(w, `<iframe src="/frame"></iframe>`)
		case "/deep":
			io.WriteString(w, `<iframe src="/mid"></iframe>`)
		default:
			io.WriteString(w, `<html><body><p>top</p><iframe src="/frame"></iframe></body></html>`)
		}
	}))
}

func TestIframeDocumentIsLoaded(t *testing.T) {
	srv := frameServer(t)
	defer srv.Close()
	b := New(Options{})
	defer b.Close()
	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(p.Frames()) != 1 {
		t.Fatalf("got %d frames, want 1", len(p.Frames()))
	}
	frame := p.Frames()[0]
	if got := strings.TrimSpace(frame.Text()); got != "inside\nL" && got != "inside L" && !strings.Contains(got, "inside") {
		t.Fatalf("frame text = %q, want it to contain inside", got)
	}
}

func TestIframeTextIncludedInPage(t *testing.T) {
	srv := frameServer(t)
	defer srv.Close()
	b := New(Options{})
	defer b.Close()
	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	text := p.Text()
	if !strings.Contains(text, "top") || !strings.Contains(text, "inside") {
		t.Fatalf("page text = %q, want both top and inside", text)
	}
}

func TestIframeLinksIncluded(t *testing.T) {
	srv := frameServer(t)
	defer srv.Close()
	b := New(Options{})
	defer b.Close()
	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	found := false
	for _, l := range p.Links() {
		if strings.HasSuffix(l.Href, "/link") {
			found = true
		}
	}
	if !found {
		t.Fatalf("frame link missing from Links(): %v", p.Links())
	}
}

func TestNestedIframes(t *testing.T) {
	srv := frameServer(t)
	defer srv.Close()
	b := New(Options{})
	defer b.Close()
	p, err := b.Open(srv.URL + "/deep")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(p.Frames()) != 1 || len(p.Frames()[0].Frames()) != 1 {
		t.Fatalf("nested frames not loaded: %d then %d", len(p.Frames()), len(p.Frames()[0].Frames()))
	}
}

func TestContentDocumentReachesFrame(t *testing.T) {
	srv := frameServer(t)
	defer srv.Close()
	b := New(Options{})
	defer b.Close()
	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	v, err := p.Eval(`document.querySelector('iframe').contentDocument.getElementById('inner').textContent`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.String(); got != "inside" {
		t.Fatalf("contentDocument text = %q, want inside", got)
	}
}

func TestIframeScriptsRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/frame" {
			io.WriteString(w, `<html><body><div id="out">loading</div>
				<script>document.getElementById('out').textContent='ready'</script></body></html>`)
			return
		}
		io.WriteString(w, `<iframe src="/frame"></iframe>`)
	}))
	defer srv.Close()
	b := New(Options{})
	defer b.Close()
	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := p.Frames()[0].Text(); !strings.Contains(got, "ready") {
		t.Fatalf("frame script did not run: text = %q", got)
	}
}
