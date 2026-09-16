package browser

import (
	"strings"
	"testing"
)

// Template content is inert: it must not appear in document queries, text
// extraction or link collection, but template.content must expose it.
func TestTemplateContentIsInert(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body>
<template id="t"><div id="inside"><a href="/hidden">hidden</a><span>secret</span></div></template>
<div id="outside">visible</div>
</body></html>`, "https://example.test/")

	if p.Query("#inside") != nil {
		t.Fatalf("template content leaked into querySelector")
	}
	if got := len(p.QueryAll("div")); got != 1 {
		t.Fatalf("querySelectorAll(div) = %d, want 1 (only the outside div)", got)
	}
	if got := len(p.QueryAll("a")); got != 0 {
		t.Fatalf("template link leaked into querySelectorAll: %d", got)
	}
	if strings.Contains(p.Text(), "secret") {
		t.Fatalf("template text leaked into Text(): %q", p.Text())
	}
	if len(p.Links()) != 0 {
		t.Fatalf("template link leaked into Links(): %+v", p.Links())
	}
	if strings.Contains(p.Markdown(), "secret") {
		t.Fatalf("template text leaked into Markdown()")
	}
	if got := textContent(p.Query("#outside")); got != "visible" {
		t.Fatalf("normal text broken: %q", got)
	}
}

func TestTemplateContentAccess(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body>
<template id="t"><div id="inside">x</div></template>
</body></html>`, "https://example.test/")

	cases := []struct{ expr, want string }{
		{`document.getElementById('t').childNodes.length`, "0"},
		{`document.getElementById('t').content.childNodes.length`, "1"},
		{`document.getElementById('t').content.querySelector('#inside').textContent`, "x"},
		{`document.getElementById('t').content.textContent`, "x"},
		{`document.getElementById('t').innerHTML`, `<div id="inside">x</div>`},
		{`document.getElementById('t').content.nodeType`, "11"},
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

	// Setting innerHTML writes into the content, not the element.
	if _, err := p.Eval(`document.getElementById('t').innerHTML = '<p id="new">n</p>'`); err != nil {
		t.Fatalf("set innerHTML: %v", err)
	}
	if p.Query("#new") != nil {
		t.Fatalf("template innerHTML setter put nodes in the document tree")
	}
	v, _ := p.Eval(`document.getElementById('t').content.querySelector('#new').textContent`)
	if v.String() != "n" {
		t.Fatalf("content after set = %q, want n", v.String())
	}
}

// Cloning a template's content and inserting it is the standard pattern and
// must produce live document nodes.
func TestTemplateCloneIntoDocument(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body><div id="host"></div>
<template id="t"><li class="row"><span class="label">a</span></li><li class="row"><span class="label">b</span></li></template>
<script>
  var t = document.getElementById('t');
  var host = document.getElementById('host');
  for (var i = 0; i < 3; i++) { host.appendChild(t.content.cloneNode(true)); }
</script></body></html>`, "https://example.test/")

	if got := len(p.QueryAll("#host li.row")); got != 6 {
		t.Fatalf("cloned rows = %d, want 6", got)
	}
	if got := len(p.QueryAll("#host .label")); got != 6 {
		t.Fatalf("cloned labels = %d, want 6", got)
	}
}

func TestTemplateSerializationRoundTrip(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body><template id="t"><div id="inside">x</div></template></body></html>`, "https://example.test/")

	html := p.HTML()
	if !strings.Contains(html, `<template id="t"><div id="inside">x</div></template>`) {
		t.Fatalf("serialization dropped template content:\n%s", html)
	}
	// The DOM tree itself must still hold the content in the fragment, not
	// re-attached as a side effect of serializing.
	if p.Query("#inside") != nil {
		t.Fatalf("serializing re-attached template content to the document")
	}
	if v, _ := p.Eval(`document.getElementById('t').content.childNodes.length`); v.String() != "1" {
		t.Fatalf("content lost after serialization: %s", v.String())
	}
}

func TestNestedTemplates(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body><template id="outer"><div id="a"></div><template id="inner"><div id="b"></div></template></template></body></html>`, "https://example.test/")

	if p.Query("#b") != nil || p.Query("#a") != nil {
		t.Fatalf("nested template content leaked into the document tree")
	}
	// The inner template lives in the outer template's content, so it is
	// reachable through that fragment and not from the document.
	cases := []struct{ expr, want string }{
		{`document.getElementById('outer').content.querySelector('#a') ? 'yes' : 'no'`, "yes"},
		{`document.getElementById('inner') ? 'yes' : 'no'`, "no"},
		{`document.getElementById('outer').content.querySelector('#inner') ? 'yes' : 'no'`, "yes"},
		{`document.getElementById('outer').content.querySelector('#inner').content.querySelector('#b') ? 'yes' : 'no'`, "yes"},
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
