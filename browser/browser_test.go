package browser

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestBrowser(t *testing.T) *Browser {
	t.Helper()
	b := New(Options{})
	t.Cleanup(b.Close)
	return b
}

func TestInlineScriptMutatesDOM(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	err := p.SetContent(`<!doctype html><html><head><title>t</title></head>
<body><div id="root"></div>
<script>
  var d = document.createElement('p');
  d.id = 'hello';
  d.textContent = 'Hello ' + 'world';
  d.className = 'greeting big';
  document.getElementById('root').appendChild(d);
  document.title = 'changed';
</script>
</body></html>`, "https://example.test/")
	if err != nil {
		t.Fatalf("SetContent: %v", err)
	}
	if got := p.Query("#hello"); got == nil {
		t.Fatalf("script did not create #hello; html=%s", p.HTML())
	}
	if got := strings.TrimSpace(textContent(p.Query("#hello"))); got != "Hello world" {
		t.Fatalf("textContent = %q", got)
	}
	if got := p.Query("#hello"); !hasClass(got, "greeting") || !hasClass(got, "big") {
		t.Fatalf("className not applied: %v", classList(got))
	}
	if got := p.Query("title"); textContent(got) != "changed" {
		t.Fatalf("title = %q", textContent(got))
	}
	if p.ReadyState() != "complete" {
		t.Fatalf("readyState = %q", p.ReadyState())
	}
}

func TestLifecycleEventsAndTimers(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<!doctype html><html><body>
<div id="out">before</div>
<script>
  document.addEventListener('DOMContentLoaded', function () {
    document.getElementById('out').textContent = 'dcl';
  });
  setTimeout(function () {
    var o = document.getElementById('out');
    o.setAttribute('data-later', 'yes');
  }, 5);
</script>
</body></html>`, "https://example.test/")

	if got := textContent(p.Query("#out")); got != "dcl" {
		t.Fatalf("DOMContentLoaded listener did not run, out=%q", got)
	}
	if got := Attr(p.Query("#out"), "data-later"); got != "yes" {
		t.Fatalf("timer did not run, data-later=%q", got)
	}
}

func TestSelectorAndNavigation(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<!doctype html><html><body>
<ul><li><a href="/one">One</a></li><li><a href="https://other.test/two">Two</a></li></ul>
</body></html>`, "https://example.test/base/")
	list := p.QueryAll("ul li a")
	if len(list) != 2 {
		t.Fatalf("expected 2 links, got %d", len(list))
	}
	links := p.Links()
	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %d", len(links))
	}
	if links[0].Href != "https://example.test/one" {
		t.Fatalf("relative href resolved to %q", links[0].Href)
	}
	if links[1].Href != "https://other.test/two" {
		t.Fatalf("absolute href = %q", links[1].Href)
	}
	if links[0].Text != "One" {
		t.Fatalf("link text = %q", links[0].Text)
	}
}

func TestFetchAndXHR(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/data":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"value":"from-server"}`))
		case "/xhr":
			_, _ = w.Write([]byte("xhr-body"))
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body><div id="app">loading</div></body></html>`))
		}
	}))
	defer srv.Close()

	b := newTestBrowser(t)
	p := b.NewPage(srv.URL + "/")
	_ = p.SetContent(`<!doctype html><html><body><div id="app">loading</div>
<script>
  fetch('/data').then(function (r) { return r.json(); }).then(function (j) {
    document.getElementById('app').textContent = j.value;
  });
</script></body></html>`, srv.URL+"/")

	if got := textContent(p.Query("#app")); got != "from-server" {
		t.Fatalf("fetch did not populate app: %q", got)
	}

	p2 := b.NewPage(srv.URL + "/")
	_ = p2.SetContent(`<!doctype html><html><body><div id="app2">loading</div>
<script>
  var x = new XMLHttpRequest();
  x.open('GET', '/xhr', true);
  x.onload = function () { document.getElementById('app2').textContent = x.responseText; };
  x.send();
</script></body></html>`, srv.URL+"/")
	if got := textContent(p2.Query("#app2")); got != "xhr-body" {
		t.Fatalf("XHR did not populate app2: %q", got)
	}
}

func TestScriptsDisabled(t *testing.T) {
	off := false
	b := New(Options{RunScripts: &off})
	defer b.Close()
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body><div id="a">x</div>
<script>document.getElementById('a').textContent='mutated';</script></body></html>`, "https://example.test/")
	if got := textContent(p.Query("#a")); got != "x" {
		t.Fatalf("script ran despite RunScripts=false: %q", got)
	}
}

func TestMarkdown(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body><h1>Title</h1><p>Hello <a href="/x">link</a> and <b>bold</b>.</p>
<script>document.body.setAttribute('data-ok','1');</script></body></html>`, "https://example.test/")
	md := p.Markdown()
	if !strings.Contains(md, "# Title") {
		t.Fatalf("markdown missing heading:\n%s", md)
	}
	if !strings.Contains(md, "[link](https://example.test/x)") {
		t.Fatalf("markdown missing link:\n%s", md)
	}
	if !strings.Contains(md, "**bold**") {
		t.Fatalf("markdown missing bold:\n%s", md)
	}
}

func TestWaitForSelector(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body><div id="late"></div>
<script>setTimeout(function(){var e=document.createElement('span');e.id='ready';document.body.appendChild(e);},10);</script>
</body></html>`, "https://example.test/")
	// timers already drained during load; assert it is present.
	if n := p.WaitForSelector("#ready", 200*time.Millisecond); n == nil {
		t.Fatalf("expected #ready after timers")
	}
}
