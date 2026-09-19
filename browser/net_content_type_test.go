package browser

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve returns a test server that answers every request with one media type
// and body, and the URL to load.
func serve(t *testing.T, contentType, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/"
}

// A response that is not markup is shown as text, so a <script> in a text/plain
// body stays text and never runs. Parsing it as HTML would execute it, which is
// the bug this covers.
func TestPlainTextDocumentDoesNotRunMarkup(t *testing.T) {
	url := serve(t, "text/plain", `<script>window.__ran = 1</script>BfTI0zPe9dUo7i1m`)

	b := newTestBrowser(t)
	p, err := b.Open(url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if got := evalStr(t, p, "String(window.__ran)"); got != "undefined" {
		t.Errorf("a script in a text/plain body ran: window.__ran = %s", got)
	}
	if n := p.Query("script"); n != nil {
		t.Errorf("the body was parsed as HTML: found a <script> element")
	}
	if p.Query("pre") == nil {
		t.Errorf("no text viewer; html=%s", p.HTML())
	}
	if got, want := p.Text(), `<script>window.__ran = 1</script>BfTI0zPe9dUo7i1m`; got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}
	if !strings.Contains(p.HTML(), "white-space: pre-wrap") {
		t.Errorf("text viewer does not keep whitespace: html=%s", p.HTML())
	}
}

// The awkward case: a body without a Content-Type is sniffed by net/http, and
// markup that is sniffed as HTML still runs, so the fix does not disable the
// normal document path.
func TestSniffedHTMLDocumentStillRunsScripts(t *testing.T) {
	url := serve(t, "", `<html><body><script>window.__ran = 1</script></body></html>`)

	b := newTestBrowser(t)
	p, err := b.Open(url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := evalStr(t, p, "window.__ran"); got != "1" {
		t.Errorf("window.__ran = %q, want 1", got)
	}
	if got := evalStr(t, p, "document.contentType"); got != "text/html" {
		t.Errorf("document.contentType = %q, want text/html", got)
	}
}

// document.contentType reports the served media type with its parameters
// dropped, and a JSON body is text too, not a document full of elements.
func TestNonMarkupTypesAreReportedAndNotParsed(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   string
		body   string
	}{
		{"text/plain", "text/plain", "plain body"},
		{"text/plain; charset=utf-8", "text/plain", "plain body"},
		{"application/json", "application/json", `{"a":"<b>x</b>"}`},
		{"application/octet-stream", "application/octet-stream", "\x00\x01binary"},
	} {
		t.Run(tc.header, func(t *testing.T) {
			b := newTestBrowser(t)
			p, err := b.Open(serve(t, tc.header, tc.body))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if got := evalStr(t, p, "document.contentType"); got != tc.want {
				t.Errorf("document.contentType = %q, want %q", got, tc.want)
			}
			if got := p.Text(); got != tc.body {
				t.Errorf("Text() = %q, want %q", got, tc.body)
			}
			if p.Query("b") != nil {
				t.Errorf("the body was parsed as HTML: found a <b> element")
			}
		})
	}
}

// An HTML document that sets its own type explicitly is unaffected, and the
// type it reports is its own, not the viewer's.
func TestHTMLDocumentKeepsItsContentType(t *testing.T) {
	b := newTestBrowser(t)
	p, err := b.Open(serve(t, "text/html; charset=utf-8", `<html><body><h1>hi</h1></body></html>`))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := evalStr(t, p, "document.contentType"); got != "text/html" {
		t.Errorf("document.contentType = %q, want text/html", got)
	}
	if p.Query("h1") == nil {
		t.Errorf("the document was not parsed as HTML; html=%s", p.HTML())
	}
}
