package browser

import (
	"testing"
)

func TestQueryXPathSelects(t *testing.T) {
	p := flexPage(t, `<ul><li>a</li><li>b</li></ul>`)
	nodes := p.QueryXPath("//li")
	if len(nodes) != 2 {
		t.Fatalf("//li matched %d nodes, want 2", len(nodes))
	}
	if nodes[0].Data != "li" {
		t.Fatalf("first node = %q, want li", nodes[0].Data)
	}
}

func TestQueryXPathAttribute(t *testing.T) {
	p := flexPage(t, `<a href="/x">one</a><a href="/y">two</a>`)
	nodes := p.QueryXPath(`//a[@href="/y"]`)
	if len(nodes) != 1 {
		t.Fatalf(`//a[@href="/y"] matched %d nodes, want 1`, len(nodes))
	}
	if got := textContent(nodes[0]); got != "two" {
		t.Fatalf("node text = %q, want two", got)
	}
}

func TestQueryXPathText(t *testing.T) {
	p := flexPage(t, `<p>alpha</p><p>beta</p>`)
	nodes := p.QueryXPath(`//p[text()="beta"]`)
	if len(nodes) != 1 {
		t.Fatalf("text predicate matched %d nodes, want 1", len(nodes))
	}
}

func TestQueryXPathFromSubtree(t *testing.T) {
	p := flexPage(t, `<div id="a"><span class="k">A</span></div><div id="b"><span class="k">B</span></div>`)
	from := p.GetElementByID("b")
	nodes := p.QueryXPathFrom(`.//span[@class="k"]`, from)
	if len(nodes) != 1 || textContent(nodes[0]) != "B" {
		t.Fatalf("subtree search returned %d nodes: %v", len(nodes), nodes)
	}
}

func TestDocumentEvaluateSingleNode(t *testing.T) {
	p := flexPage(t, `<h1 id="title">Hello</h1>`)
	v, err := p.Eval(`document.evaluate('//h1', document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue.textContent`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.String(); got != "Hello" {
		t.Fatalf("singleNodeValue = %q, want Hello", got)
	}
}

func TestDocumentEvaluateSnapshot(t *testing.T) {
	p := flexPage(t, `<ul><li>a</li><li>b</li><li>c</li></ul>`)
	v, err := p.Eval(`(function(){
		var r = document.evaluate('//li', document, null, XPathResult.ORDERED_NODE_SNAPSHOT_TYPE, null);
		return r.snapshotLength;
	})()`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.ToInteger(); got != 3 {
		t.Fatalf("snapshotLength = %d, want 3", got)
	}
}

func TestDocumentEvaluateNumber(t *testing.T) {
	p := flexPage(t, `<ul><li>a</li><li>b</li></ul>`)
	v, err := p.Eval(`document.evaluate('count(//li)', document, null, XPathResult.NUMBER_TYPE, null).numberValue`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.ToFloat(); got != 2 {
		t.Fatalf("numberValue = %v, want 2", got)
	}
}

func TestXPathScalarFromPage(t *testing.T) {
	p := flexPage(t, `<ul><li>a</li><li>b</li><li>c</li></ul>`)
	r, err := p.XPath("count(//li)")
	if err != nil {
		t.Fatalf("XPath: %v", err)
	}
	if r.Kind != "number" || r.Number != 3 {
		t.Fatalf("result = %+v, want 3", r)
	}
}
