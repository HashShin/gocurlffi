package browser

import (
	"strings"
	"testing"
)

func TestDOMParserXMLType(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	for _, tc := range []struct{ expr, want string }{
		// The root is the XML root, not a synthetic <html>.
		{`new DOMParser().parseFromString('<a><b/></a>','text/xml').documentElement.tagName`, "a"},
		{`new DOMParser().parseFromString('<feed><item/></feed>','application/xml').documentElement.tagName`, "feed"},
		{`new DOMParser().parseFromString('<svg><g/></svg>','image/svg+xml').documentElement.tagName`, "svg"},
		// XML is case-sensitive, unlike HTML.
		{`new DOMParser().parseFromString('<MyTag/>','text/xml').documentElement.tagName`, "MyTag"},
		{`new DOMParser().parseFromString('<rss><CHANNEL/></rss>','text/xml').documentElement.firstChild.tagName`, "CHANNEL"},
		// Structure and text survive.
		{`new DOMParser().parseFromString('<a><b>hi</b></a>','text/xml').documentElement.firstChild.textContent`, "hi"},
		{`new DOMParser().parseFromString('<a><b/><c/></a>','text/xml').documentElement.children.length`, "2"},
		{`new DOMParser().parseFromString('<a x="1"/>','text/xml').documentElement.getAttribute('x')`, "1"},
		// Query helpers work on the result.
		{`new DOMParser().parseFromString('<a><b id="t">v</b></a>','text/xml').querySelector('#t').textContent`, "v"},
		{`new DOMParser().parseFromString('<a><b/></a>','text/xml').getElementsByTagName('b').length`, "1"},
		{`Object.prototype.toString.call(new DOMParser().parseFromString('<a/>','text/xml'))`, "[object XMLDocument]"},
		// The XML declaration is not part of the tree.
		{`new DOMParser().parseFromString('<?xml version="1.0"?><a/>','text/xml').documentElement.tagName`, "a"},
	} {
		if got := evalStr(t, p, tc.expr); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

func TestDOMParserHTMLStillHTML(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	for _, tc := range []struct{ expr, want string }{
		{`new DOMParser().parseFromString('<p>hi</p>','text/html').querySelector('p').textContent`, "hi"},
		// An unknown type falls back to HTML, as it does in a browser.
		{`new DOMParser().parseFromString('<p>hi</p>','text/plain').documentElement.tagName`, "HTML"},
		{`new DOMParser().parseFromString('<p>hi</p>','').documentElement.tagName`, "HTML"},
	} {
		if got := evalStr(t, p, tc.expr); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// Malformed XML yields a <parsererror> document rather than an exception.
func TestDOMParserXMLParseError(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := evalStr(t, p, `new DOMParser().parseFromString('<a><b></a>','text/xml').documentElement.tagName`)
	if got != "parsererror" {
		t.Fatalf("bad XML root = %q, want %q", got, "parsererror")
	}
}

// XML content must be reachable through the same helpers a page uses on any
// document: this is the sniffing pattern the bug broke.
func TestDOMParserXMLFeedSniff(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := evalStr(t, p, `
		function parse(src, type){
			var d = new DOMParser().parseFromString(src, type);
			return d.documentElement.tagName === 'parsererror' ? 'error' : d.documentElement.tagName;
		}
		parse('<rss/>','text/xml') + ',' + parse('<html><body>x</body></html>','text/html')
	`)
	if !strings.Contains(got, "rss") || !strings.Contains(got, "HTML") {
		t.Fatalf("sniff = %q", got)
	}
}
