package browser

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A page's fetch sends a real POST with a body through the impersonating
// transport, and the promise chain a page writes around it - a `.then` that
// reads the status and headers, then a second that reads the body - resolves
// against the response the server actually sent.
func TestFetchPostsABodyAndThenReadsTheResponse(t *testing.T) {
	type seenRequest struct {
		method string
		path   string
		ctype  string
		body   string
	}
	var seen []seenRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, seenRequest{
			method: r.Method,
			path:   r.URL.Path,
			ctype:  r.Header.Get("Content-Type"),
			body:   string(body),
		})
		w.Header().Set("X-Echo", "yes")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "made:"+string(body))
	}))
	defer srv.Close()

	p := flexPage(t, `<html><body></body></html>`)
	if got := evalThen(t, p, fmt.Sprintf(`
		fetch(%q, {
			method: 'POST',
			headers: {'Content-Type': 'application/x-www-form-urlencoded'},
			body: 'a=1&b=two words',
		}).then(function(r){
			window.__status = r.status;
			window.__header = r.headers.get('X-Echo');
			return r.text();
		}).then(function(text){
			window.__body = text;
		});
	`, srv.URL+"/submit"), "window.__body"); got != "made:a=1&b=two words" {
		t.Errorf("body the page read = %q", got)
	}
	if got := evalStr(t, p, "window.__status"); got != "201" {
		t.Errorf("status the page read = %q, want 201", got)
	}
	if got := evalStr(t, p, "window.__header"); got != "yes" {
		t.Errorf("header the page read = %q, want yes", got)
	}

	if len(seen) != 1 {
		t.Fatalf("server saw %d requests, want 1: %+v", len(seen), seen)
	}
	if seen[0].method != "POST" {
		t.Errorf("method = %q, want POST", seen[0].method)
	}
	if seen[0].path != "/submit" {
		t.Errorf("path = %q, want /submit", seen[0].path)
	}
	if !strings.Contains(seen[0].ctype, "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type = %q", seen[0].ctype)
	}
	if seen[0].body != "a=1&b=two words" {
		t.Errorf("body = %q, want a=1&b=two words", seen[0].body)
	}
}

// Clicking a submit control is the navigation path: the form's controls go out
// as a POST body, and the document the server answers with becomes the page.
func TestFormSubmitPostsItsControlsAndLoadsTheResult(t *testing.T) {
	var method, path, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.Method == "POST" {
			b, _ := io.ReadAll(r.Body)
			method, path, body = r.Method, r.URL.Path, string(b)
			_, _ = io.WriteString(w,
				`<html><head><title>Logged in</title></head><body><a href="/logout">Logout</a></body></html>`)
			return
		}
		_, _ = io.WriteString(w, `<html><head><title>Login</title></head><body>
<form action="/login" method="post">
<input id="username" name="username">
<input id="password" name="password" type="password">
<input type="hidden" name="csrf_token" value="tok123">
<input type="submit" value="Login">
</form></body></html>`)
	}))
	defer srv.Close()

	b := newTestBrowser(t)
	p, err := b.Open(srv.URL + "/login")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := p.Fill("#username", "me"); err != nil {
		t.Fatalf("Fill username: %v", err)
	}
	if err := p.Fill("#password", "secret"); err != nil {
		t.Fatalf("Fill password: %v", err)
	}
	if err := p.Click("input[type=submit]"); err != nil {
		t.Fatalf("Click: %v", err)
	}

	if method != "POST" || path != "/login" {
		t.Errorf("submit sent %s %s, want POST /login", method, path)
	}
	if body != "username=me&password=secret&csrf_token=tok123" {
		t.Errorf("submit body = %q", body)
	}
	if p.Query("a[href='/logout']") == nil {
		t.Errorf("post-login document did not load; html=%s", p.HTML())
	}
	if got := p.Title(); got != "Logged in" {
		t.Errorf("Title() = %q, want Logged in", got)
	}
}

// XMLHttpRequest is the other way a page posts a body, and it has its own
// request and response plumbing: open/setRequestHeader/send, then onload.
func TestXHRPostsABodyAndReadsTheResponse(t *testing.T) {
	var method, ctype, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		method, ctype, body = r.Method, r.Header.Get("Content-Type"), string(b)
		_, _ = io.WriteString(w, "stored:"+string(b))
	}))
	defer srv.Close()

	p := flexPage(t, `<html><body></body></html>`)
	if got := evalThen(t, p, fmt.Sprintf(`
		var x = new XMLHttpRequest();
		x.open('POST', %q);
		x.setRequestHeader('Content-Type', 'application/json');
		x.onload = function(){
			window.__xhrStatus = x.status;
			window.__xhrBody = x.responseText;
		};
		x.send('{"a":1}');
	`, srv.URL+"/xhr"), "window.__xhrBody"); got != `stored:{"a":1}` {
		t.Errorf("body the page read = %q", got)
	}
	if got := evalStr(t, p, "window.__xhrStatus"); got != "200" {
		t.Errorf("status the page read = %q, want 200", got)
	}
	if method != "POST" {
		t.Errorf("method = %q, want POST", method)
	}
	if !strings.Contains(ctype, "application/json") {
		t.Errorf("Content-Type = %q", ctype)
	}
	if body != `{"a":1}` {
		t.Errorf("body = %q, want {\"a\":1}", body)
	}
}

// A JSON post is the shape an API call takes: the page sets its own
// Content-Type, and the bytes the server gets are exactly the string sent.
func TestFetchPostsAJSONBody(t *testing.T) {
	const payload = `{"user":"me","n":2}`
	var ctype, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ctype, body = r.Header.Get("Content-Type"), string(b)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	p := flexPage(t, `<html><body></body></html>`)
	if got := evalThen(t, p, fmt.Sprintf(`
		fetch(%q, {
			method: 'POST',
			headers: {'Content-Type': 'application/json'},
			body: JSON.stringify({user: 'me', n: 2}),
		}).then(function(r){ return r.json(); }).then(function(v){
			window.__ok = String(v.ok);
		});
	`, srv.URL+"/api"), "window.__ok"); got != "true" {
		t.Errorf("parsed ok = %q, want true", got)
	}
	if !strings.Contains(ctype, "application/json") {
		t.Errorf("Content-Type = %q", ctype)
	}
	if body != payload {
		t.Errorf("body = %q, want %q", body, payload)
	}
}
