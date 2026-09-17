package browser

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A click on a link loads what it points at, the way a browser follows a click
// rather than only running the page's handlers.
func TestClickLinkNavigates(t *testing.T) {
	srv := htmlServer(t, map[string]string{
		"/":      `<a id="l" href="/other">go</a>`,
		"/other": `<h1 id="t">other page</h1>`,
	})
	b := New(Options{})
	defer b.Close()

	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := p.Click("#l"); err != nil {
		t.Fatalf("Click: %v", err)
	}
	if got, want := p.URL, srv.URL+"/other"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
	if p.Query("#t") == nil {
		t.Fatal("the linked document was not installed")
	}
	if got := textContent(p.Query("#t")); got != "other page" {
		t.Fatalf("text = %q", got)
	}
}

// A handler that calls preventDefault keeps the page where it is, which is what
// a single-page app relies on.
func TestClickLinkPreventedStays(t *testing.T) {
	srv := htmlServer(t, map[string]string{
		"/": `<a id="l" href="/other">go</a>
<script>document.getElementById('l').addEventListener('click', function(e){ window.hit = 1; e.preventDefault(); })</script>`,
		"/other": `<h1 id="t">other page</h1>`,
	})
	b := New(Options{})
	defer b.Close()

	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := p.Click("#l"); err != nil {
		t.Fatalf("Click: %v", err)
	}
	if p.URL != srv.URL+"/" {
		t.Fatalf("URL = %q, want the page it started on", p.URL)
	}
	if p.Query("#t") != nil {
		t.Fatal("preventDefault did not stop the navigation")
	}
	v, err := p.Eval("window.hit")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v.ToInteger() != 1 {
		t.Fatalf("handler ran %v times, want 1", v)
	}
}

// A submit button posts its form, with the controls the page holds, and the
// response becomes the document.
func TestClickSubmitButtonPosts(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/posted" {
			_ = r.ParseForm()
			gotBody = r.Form.Encode()
			fmt.Fprintf(w, `<h1 id="done">%s</h1>`, r.FormValue("user"))
			return
		}
		_, _ = w.Write([]byte(`<form action="/posted" method="post">
			<input id="u" name="user" value="">
			<button id="s" type="submit">Send</button>
		</form>`))
	}))
	defer srv.Close()

	b := New(Options{})
	defer b.Close()

	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := p.Fill("#u", "me"); err != nil {
		t.Fatalf("Fill: %v", err)
	}
	if err := p.Click("#s"); err != nil {
		t.Fatalf("Click: %v", err)
	}
	if got := textContent(p.Query("#done")); got != "me" {
		t.Fatalf("rendered %q, want the posted value", got)
	}
	if gotBody != "user=me" {
		t.Fatalf("POST body = %q, want user=me", gotBody)
	}
}

// A GET form puts its controls in the query string.
func TestClickSubmitButtonGets(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/search" {
			gotQuery = r.URL.RawQuery
			_, _ = w.Write([]byte(`<h1 id="done">found</h1>`))
			return
		}
		_, _ = w.Write([]byte(`<form action="/search" method="get"><input id="q" name="q"><button id="s">Go</button></form>`))
	}))
	defer srv.Close()

	b := New(Options{})
	defer b.Close()

	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := p.Fill("#q", "go curl"); err != nil {
		t.Fatalf("Fill: %v", err)
	}
	if err := p.Click("#s"); err != nil {
		t.Fatalf("Click: %v", err)
	}
	if gotQuery != "q=go+curl" {
		t.Fatalf("query = %q, want q=go+curl", gotQuery)
	}
	if p.Query("#done") == nil {
		t.Fatal("the response to the form was not installed")
	}
}

// The button that was clicked is part of the submission, as a browser sends it.
func TestClickSubmitterIsIncluded(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/post" {
			_ = r.ParseForm()
			gotBody = r.Form.Encode()
			_, _ = w.Write([]byte(`<h1 id="done">ok</h1>`))
			return
		}
		_, _ = w.Write([]byte(`<form action="/post" method="post">
			<input name="q" value="1">
			<button id="s" type="submit" name="action" value="save">Save</button>
		</form>`))
	}))
	defer srv.Close()

	b := New(Options{})
	defer b.Close()

	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := p.Click("#s"); err != nil {
		t.Fatalf("Click: %v", err)
	}
	if gotBody != "action=save&q=1" {
		t.Fatalf("POST body = %q, want action=save&q=1", gotBody)
	}
}

// A button that is not a submit control keeps the page where it is; its own
// handler still runs.
func TestClickPlainButtonStays(t *testing.T) {
	srv := htmlServer(t, map[string]string{
		"/": `<button id="b">go</button>
<script>document.getElementById('b').addEventListener('click', function(){ window.hit = 1 })</script>`,
	})
	b := New(Options{})
	defer b.Close()

	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := p.Click("#b"); err != nil {
		t.Fatalf("Click: %v", err)
	}
	if p.URL != srv.URL+"/" {
		t.Fatalf("URL = %q, want the page it started on", p.URL)
	}
	if v, err := p.Eval("window.hit"); err != nil || v.ToInteger() != 1 {
		t.Fatalf("handler hit = %v (err %v), want 1", v, err)
	}
}

// A form submits on a page whose scripts are off, because the default action is
// not a script feature.
func TestClickSubmitWithoutScripts(t *testing.T) {
	var gotMethod, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/posted" {
			_ = r.ParseForm()
			gotMethod, gotBody = r.Method, r.Form.Encode()
			_, _ = w.Write([]byte(`<h1 id="done">ok</h1>`))
			return
		}
		_, _ = w.Write([]byte(`<form action="/posted" method="post"><input name="user" value="me"><button id="s">Send</button></form>`))
	}))
	defer srv.Close()

	noScripts := false
	b := New(Options{RunScripts: &noScripts})
	defer b.Close()

	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := p.Click("#s"); err != nil {
		t.Fatalf("Click: %v", err)
	}
	if gotMethod != "POST" || gotBody != "user=me" {
		t.Fatalf("submitted %s %q, want POST user=me", gotMethod, gotBody)
	}
	if p.Query("#done") == nil {
		t.Fatal("the response to the form was not installed")
	}
}

// element.click() from a script queues the navigation instead of reloading the
// document while the script is still running.
func TestScriptedClickNavigates(t *testing.T) {
	srv := htmlServer(t, map[string]string{
		"/": `<a id="l" href="/other">go</a>
<script>document.getElementById('l').click();</script>`,
		"/other": `<h1 id="t">other page</h1>`,
	})
	b := New(Options{})
	defer b.Close()

	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got, want := p.URL, srv.URL+"/other"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
	if p.Query("#t") == nil {
		t.Fatal("the queued navigation was not followed")
	}
}

// A click on a link with a target that needs a second window leaves the page
// alone rather than navigating the current one.
func TestClickTargetBlankStays(t *testing.T) {
	srv := htmlServer(t, map[string]string{
		"/":      `<a id="l" href="/other" target="_blank">go</a>`,
		"/other": `<h1 id="t">other page</h1>`,
	})
	b := New(Options{})
	defer b.Close()

	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := p.Click("#l"); err != nil {
		t.Fatalf("Click: %v", err)
	}
	if p.URL != srv.URL+"/" || p.Query("#t") != nil {
		t.Fatalf("URL = %q, want the page it started on", p.URL)
	}
}

// htmlServer serves one HTML body per path, so a navigation test only has to
// state the pages it moves between.
func htmlServer(t *testing.T, pages map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if !strings.HasPrefix(body, "<") {
			body = "<html><body>" + body + "</body></html>"
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A submit control can live outside its form and name it with the form
// attribute, which is how a toolbar button submits.
func TestClickSubmitterByFormAttribute(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/post" {
			_ = r.ParseForm()
			gotBody = r.Form.Encode()
			_, _ = w.Write([]byte(`<h1 id="done">ok</h1>`))
			return
		}
		_, _ = w.Write([]byte(`<form id="f" action="/post" method="post"><input name="q" value="1"></form>
			<button id="s" type="submit" form="f" name="action" value="go">Go</button>`))
	}))
	defer srv.Close()

	b := New(Options{})
	defer b.Close()

	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := p.Click("#s"); err != nil {
		t.Fatalf("Click: %v", err)
	}
	if gotBody != "action=go&q=1" {
		t.Fatalf("POST body = %q, want action=go&q=1", gotBody)
	}
	if p.Query("#done") == nil {
		t.Fatal("the response to the form was not installed")
	}
}
