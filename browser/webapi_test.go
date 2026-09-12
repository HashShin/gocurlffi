package browser

import "testing"

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
