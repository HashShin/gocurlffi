package browser

import (
	"strings"
	"testing"
)

// Frameworks parse HTML fragments through document.implementation and
// DOMParser; both must operate on their own document, not the page's.
func TestDOMParserAndImplementation(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body><div id="page-only">page</div></body></html>`, "https://example.test/")

	cases := []struct{ expr, want string }{
		{`new DOMParser().parseFromString('<div id="x">hi</div>', 'text/html').querySelector('#x').textContent`, "hi"},
		{`document.implementation.createHTMLDocument('T').title`, "T"},
		{`document.implementation.createHTMLDocument('T').body.tagName`, "BODY"},
		// the sub-document must not see the page's element
		{`document.implementation.createHTMLDocument('T').getElementById('page-only') === null`, "true"},
		{`document.implementation.createHTMLDocument('T').querySelector('#page-only') === null`, "true"},
		{`document.getElementById('page-only').textContent`, "page"},
		{`document.createRange().createContextualFragment('<b>f</b>').firstChild.tagName`, "B"},
		{`(function(){customElements.define('x-a', function(){}); return typeof customElements.get('x-a');})()`, "function"},
		{`document.createDocumentFragment().nodeType`, "11"},
	}
	for _, c := range cases {
		v, err := p.Eval(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got := v.String(); got != c.want {
			t.Fatalf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestRequestIdleCallback(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body><div id="o"></div>
<script>
  requestIdleCallback(function (deadline) {
    document.getElementById('o').setAttribute('data-idle', String(deadline.timeRemaining()));
  });
</script></body></html>`, "https://example.test/")
	if got := Attr(p.Query("#o"), "data-idle"); got == "" {
		t.Fatalf("requestIdleCallback did not run; console=%v", p.Console())
	}
}

func TestDocumentFragmentProto(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body><div id="host"></div>
<script>
  var frag = document.createDocumentFragment();
  var span = document.createElement('span');
  span.id = 'frag-child';
  frag.appendChild(span);
  document.getElementById('host').appendChild(frag);
</script></body></html>`, "https://example.test/")
	if p.Query("#frag-child") == nil {
		t.Fatalf("fragment append failed; html=%s", p.HTML())
	}
}

// Lazy content is revealed when an IntersectionObserver reports.
func TestIntersectionObserverFires(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body><div id="lazy"></div>
<script>
  var io = new IntersectionObserver(function (entries, observer) {
    entries.forEach(function (e) {
      if (e.isIntersecting) {
        e.target.setAttribute('data-seen', 'yes');
      }
    });
  });
  io.observe(document.getElementById('lazy'));
</script></body></html>`, "https://example.test/")
	if got := Attr(p.Query("#lazy"), "data-seen"); got != "yes" {
		t.Fatalf("IntersectionObserver did not report; html=%s", p.HTML())
	}
}

func TestSelectorCache(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body><div class="a"><span>x</span><span>y</span></div>
<div class="a"><span>z</span></div></body></html>`, "https://example.test/")

	// Repeated queries (cache hits) must stay correct and independent.
	for i := 0; i < 3; i++ {
		if got := len(p.QueryAll("div.a span")); got != 3 {
			t.Fatalf("iteration %d: div.a span = %d, want 3", i, got)
		}
		if got := len(p.QueryAll(".a")); got != 2 {
			t.Fatalf("iteration %d: .a = %d, want 2", i, got)
		}
	}
	// Invalid selectors are cached as failures and return empty, not panic.
	if got := len(p.QueryAll("div >> bad#")); got != 0 {
		t.Fatalf("invalid selector returned %d nodes", got)
	}
	if got := p.Query("::not-a-selector"); got != nil {
		t.Fatalf("invalid querySelector returned a node")
	}
}

func BenchmarkQuerySelectorAll(b *testing.B) {
	br := New(Options{})
	defer br.Close()
	p := br.NewPage("https://example.test/")
	var sb strings.Builder
	sb.WriteString("<html><body>")
	for i := 0; i < 500; i++ {
		sb.WriteString(`<div class="item"><span class="label">x</span></div>`)
	}
	sb.WriteString("</body></html>")
	_ = p.SetContent(sb.String(), "https://example.test/")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if n := len(p.QueryAll("div.item span.label")); n != 500 {
			b.Fatalf("got %d", n)
		}
	}
}

func TestTreeWalker(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body><div id="a" class="x"><p id="b">one</p><span id="c">two</span></div>
<div id="d"><b id="e">three</b></div></body></html>`, "https://example.test/")

	cases := []struct{ expr, want string }{
		// all elements, in document order
		{`(function(){
			var w = document.createTreeWalker(document.body, NodeFilter.SHOW_ELEMENT);
			var ids = [];
			while (w.nextNode()) { if (w.currentNode.id) ids.push(w.currentNode.id); }
			return ids.join(',');
		})()`, "a,b,c,d,e"},
		// only text nodes
		{`(function(){
			var w = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
			var out = [];
			while (w.nextNode()) { var t = w.currentNode.data.trim(); if (t) out.push(t); }
			return out.join('|');
		})()`, "one|two|three"},
		// filter function: only elements with class x
		{`(function(){
			var w = document.createTreeWalker(document.body, NodeFilter.SHOW_ELEMENT, {
				acceptNode: function (n) { return n.className === 'x' ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_SKIP; }
			});
			var out = [];
			while (w.nextNode()) { out.push(w.currentNode.id); }
			return out.join(',');
		})()`, "a"},
		// parentNode respects whatToShow: with SHOW_ELEMENT, #b's parent is #a
		{`(function(){
			var w = document.createTreeWalker(document.body, NodeFilter.SHOW_ELEMENT);
			while (w.nextNode()) { if (w.currentNode.id === 'b') break; }
			return w.parentNode().id;
		})()`, "a"},
		// with SHOW_TEXT, an element ancestor is not "visible", so this is null
		{`(function(){
			var w = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
			w.nextNode();
			return String(w.parentNode());
		})()`, "null"},
		{`document.domain`, "example.test"},
		{`document.hasFocus()`, "true"},
		{`document.body.webkitMatchesSelector('body')`, "true"},
	}
	for _, c := range cases {
		v, err := p.Eval(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got := v.String(); got != c.want {
			t.Fatalf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestPostMessage(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body><div id="o"></div>
<script>
  window.addEventListener('message', function (e) {
    document.getElementById('o').setAttribute('data-msg', e.data.hello + '@' + e.origin);
  });
  window.postMessage({hello: 'world'}, 'https://example.test');
</script></body></html>`, "https://example.test/")
	if got := Attr(p.Query("#o"), "data-msg"); got != "world@https://example.test" {
		t.Fatalf("postMessage not delivered: %q", got)
	}
}

func TestFrameAccessors(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body><div id="plain"></div><iframe id="f"></iframe></body></html>`, "https://example.test/")

	cases := []struct{ expr, want string }{
		{`document.getElementById('f').contentDocument === document ? 'same' : 'other'`, "same"},
		{`typeof document.getElementById('f').contentWindow.document`, "object"},
		// non-frame elements do not expose the property
		{`String(document.getElementById('plain').contentDocument)`, "undefined"},
	}
	for _, c := range cases {
		v, err := p.Eval(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got := v.String(); got != c.want {
			t.Fatalf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}
