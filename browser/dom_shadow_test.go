package browser

import (
	"strings"
	"testing"
)

func TestAttachShadowCreatesRoot(t *testing.T) {
	p := flexPage(t, `<html><body><div id="host"></div></body></html>`)
	for _, tc := range []struct{ expr, want string }{
		{`document.getElementById('host').attachShadow({mode:'open'}).mode`, "open"},
		{`(function(){var h=document.getElementById('host');h.attachShadow({mode:'open'});return h.shadowRoot !== null})()`, "true"},
		{`(function(){var h=document.getElementById('host');h.attachShadow({mode:'open'});return h.shadowRoot.mode})()`, "open"},
		{`Object.prototype.toString.call(document.getElementById('host').attachShadow({mode:'open'}))`, "[object ShadowRoot]"},
	} {
		// Each case uses its own host so attachShadow is not called twice.
		pp := flexPage(t, `<html><body><div id="host"></div></body></html>`)
		got := evalStr(t, pp, tc.expr)
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
	_ = p
}

func TestClosedShadowRootIsHidden(t *testing.T) {
	p := flexPage(t, `<html><body><div id="host"></div></body></html>`)
	got := evalThen(t, p, `
		var h = document.getElementById('host');
		var sr = h.attachShadow({mode:'closed'});
		window.__open = (h.shadowRoot === null);
		sr.innerHTML = '<b>secret</b>';
	`, `window.__open + ':' + document.getElementById('host').shadowRoot`)
	if got != "true:null" {
		t.Fatalf("closed root exposure = %q, want %q", got, "true:null")
	}
}

func TestShadowRootHoldsContent(t *testing.T) {
	p := flexPage(t, `<html><body><div id="host"></div></body></html>`)
	got := evalThen(t, p, `
		var sr = document.getElementById('host').attachShadow({mode:'open'});
		sr.innerHTML = '<p id="inner">shadow text</p>';
	`, `document.getElementById('host').shadowRoot.getElementById('inner').textContent`)
	if got != "shadow text" {
		t.Fatalf("shadow content = %q, want %q", got, "shadow text")
	}
}

func TestShadowRootQuerySelector(t *testing.T) {
	p := flexPage(t, `<html><body><div id="host"></div></body></html>`)
	got := evalThen(t, p, `
		var sr = document.getElementById('host').attachShadow({mode:'open'});
		sr.innerHTML = '<span class="x">found</span>';
	`, `document.getElementById('host').shadowRoot.querySelector('.x').textContent`)
	if got != "found" {
		t.Fatalf("shadowRoot.querySelector = %q, want %q", got, "found")
	}
}

func TestGetRootNodeInsideShadow(t *testing.T) {
	p := flexPage(t, `<html><body><div id="host"></div></body></html>`)
	got := evalThen(t, p, `
		var sr = document.getElementById('host').attachShadow({mode:'open'});
		sr.innerHTML = '<span id="s">x</span>';
		var inner = sr.getElementById('s');
		window.__isRoot = (inner.getRootNode() === sr);
		window.__hostRoot = (document.getElementById('host').getRootNode() === document);
	`, `window.__isRoot + ':' + window.__hostRoot`)
	if got != "true:true" {
		t.Fatalf("getRootNode = %q, want %q", got, "true:true")
	}
}

// The whole point for a scraper: content inside a shadow root must appear in the
// extracted text, not be replaced by the <slot> template.
func TestShadowContentAppearsInText(t *testing.T) {
	p := flexPage(t, `<html><body><div id="host"></div></body></html>`)
	if _, err := p.Eval(`
		var sr = document.getElementById('host').attachShadow({mode:'open'});
		sr.innerHTML = '<p>inside the shadow</p>';
	`); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(p.Text()); !strings.Contains(got, "inside the shadow") {
		t.Fatalf("Page.Text() = %q, want it to contain the shadow text", got)
	}
	if got := strings.TrimSpace(p.HTML()); !strings.Contains(got, "inside the shadow") {
		t.Fatalf("Page.HTML() = %q, want it to contain the shadow content", got)
	}
}

func TestShadowContentAppearsInLinks(t *testing.T) {
	p := flexPage(t, `<html><body><div id="host"></div></body></html>`)
	if _, err := p.Eval(`
		var sr = document.getElementById('host').attachShadow({mode:'open'});
		sr.innerHTML = '<a href="/from-shadow">shadow link</a>';
	`); err != nil {
		t.Fatal(err)
	}
	links := p.Links()
	if len(links) != 1 || links[0].Href != "https://example.test/from-shadow" {
		t.Fatalf("Links() = %+v, want the shadow anchor", links)
	}
}

func TestShadowContentAppearsInMarkdown(t *testing.T) {
	p := flexPage(t, `<html><body><div id="host"></div></body></html>`)
	if _, err := p.Eval(`
		var sr = document.getElementById('host').attachShadow({mode:'open'});
		sr.innerHTML = '<h1>Shadow Heading</h1>';
	`); err != nil {
		t.Fatal(err)
	}
	if got := p.Markdown(); !strings.Contains(got, "Shadow Heading") {
		t.Fatalf("Markdown() = %q, want the shadow heading", got)
	}
}

// A slot renders the host's light children, so text must not be duplicated and
// light content must not be lost.
func TestSlotRendersLightChildren(t *testing.T) {
	p := flexPage(t, `<html><body><div id="host"><span>slotted text</span></div></body></html>`)
	if _, err := p.Eval(`
		var sr = document.getElementById('host').attachShadow({mode:'open'});
		sr.innerHTML = '<div class="wrapper"><slot></slot></div>';
	`); err != nil {
		t.Fatal(err)
	}
	got := p.Text()
	if !strings.Contains(got, "slotted text") {
		t.Fatalf("Text() = %q, want the slotted light content", got)
	}
	if strings.Count(got, "slotted text") != 1 {
		t.Fatalf("Text() = %q, want the slotted text exactly once", got)
	}
}

// Server-rendered components ship their shadow tree as a template, which is how
// most real pages use shadow DOM.
func TestDeclarativeShadowDOM(t *testing.T) {
	p := flexPage(t, `<html><body>
		<div id="host"><template shadowrootmode="open"><p>declarative</p></template></div>
	</body></html>`)
	if got := p.shadowCount(); got != 1 {
		t.Fatalf("attached %d shadow roots, want 1", got)
	}
	if got := strings.TrimSpace(p.Text()); !strings.Contains(got, "declarative") {
		t.Fatalf("Text() = %q, want the declarative shadow content", got)
	}
	if got := evalStr(t, p, `document.getElementById('host').shadowRoot.querySelector('p').textContent`); got != "declarative" {
		t.Fatalf("shadowRoot content = %q, want %q", got, "declarative")
	}
}

func TestDeclarativeShadowDOMWithScriptsOff(t *testing.T) {
	noJS := false
	b := New(Options{RunScripts: &noJS})
	defer b.Close()
	p := b.NewPage("https://example.test/")
	if err := p.SetContent(`<html><body>
		<div id="host"><template shadowrootmode="open"><p>no js needed</p></template></div>
	</body></html>`, "https://example.test/"); err != nil {
		t.Fatal(err)
	}
	if got := p.shadowCount(); got != 1 {
		t.Fatalf("attached %d shadow roots with scripts off, want 1", got)
	}
	if got := p.Text(); !strings.Contains(got, "no js needed") {
		t.Fatalf("Text() = %q", got)
	}
}

func TestNestedShadowRoots(t *testing.T) {
	p := flexPage(t, `<html><body>
		<div id="outer"><template shadowrootmode="open"><div id="inner"><template shadowrootmode="open"><b>deep</b></template></div></template></div>
	</body></html>`)
	if got := p.shadowCount(); got != 2 {
		t.Fatalf("attached %d shadow roots, want 2", got)
	}
	if got := p.Text(); !strings.Contains(got, "deep") {
		t.Fatalf("Text() = %q, want the nested shadow text", got)
	}
}

// A Go query reaches into shadow roots; the DOM's own document.querySelector
// still does not, which is what the spec requires.
func TestGoQueryPiercesShadowButJSDoesNot(t *testing.T) {
	p := flexPage(t, `<html><body>
		<div id="host"><template shadowrootmode="open"><span id="hidden">deep</span></template></div>
	</body></html>`)
	if n := p.Query("#hidden"); n == nil {
		t.Fatal("Page.Query did not reach into the shadow root")
	}
	if got := evalStr(t, p, `document.querySelector('#hidden') === null`); got != "true" {
		t.Fatalf("document.querySelector pierced the shadow boundary: got %q", got)
	}
}

func TestShadowRootsListing(t *testing.T) {
	p := flexPage(t, `<html><body>
		<div id="a"><template shadowrootmode="open"><p>a</p></template></div>
		<div id="b"><template shadowrootmode="closed"><p>b</p></template></div>
	</body></html>`)
	roots := p.ShadowRoots()
	if len(roots) != 2 {
		t.Fatalf("ShadowRoots() = %d, want 2", len(roots))
	}
	if roots[0].Mode() != "open" || roots[1].Mode() != "closed" {
		t.Fatalf("modes = %q, %q", roots[0].Mode(), roots[1].Mode())
	}
}
