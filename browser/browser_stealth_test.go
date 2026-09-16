package browser

import "testing"

// stealthEval evaluates an expression and returns it as a string.
func stealthEval(t *testing.T, p *Page, expr string) string {
	t.Helper()
	v, err := p.Eval(expr)
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	return v.String()
}

// parentElement must be null when the parent is not an element. Google's
// viewport bookkeeping walks `while (e = e.parentElement)` and calls
// hasAttribute() on each hit; handing back the Document crashed the script with
// "Object has no member 'hasAttribute'".
func TestParentElementIsNullAtDocument(t *testing.T) {
	p := flexPage(t, `<html><body><div id="d"></div></body></html>`)
	for _, tc := range []struct{ expr, want string }{
		{`document.documentElement.parentElement === null`, "true"},
		{`document.body.parentElement.parentElement === null`, "true"},
		{`document.body.parentElement.tagName`, "HTML"},
		{`document.getElementById("d").parentElement.tagName`, "BODY"},
		// parentNode still reaches the Document: only parentElement is narrowed.
		{`document.documentElement.parentNode.nodeType`, "9"},
	} {
		if got := stealthEval(t, p, tc.expr); got != tc.want {
			t.Errorf("%s = %s, want %s", tc.expr, got, tc.want)
		}
	}
}

// The DOM interface constructors are feature-detected by name. A missing one is
// not a harmless gap, it is a ReferenceError that aborts the enclosing script.
func TestDOMConstructorGlobals(t *testing.T) {
	p := flexPage(t, `<html><body><img id="i" src="x.png"></body></html>`)
	for _, tc := range []struct{ expr, want string }{
		{`typeof Node`, "function"},
		{`typeof Element`, "function"},
		{`typeof HTMLElement`, "function"},
		{`typeof Document`, "function"},
		{`typeof DocumentFragment`, "function"},
		{`typeof Text`, "function"},
		{`typeof Comment`, "function"},
		{`typeof Image`, "function"},
		{`typeof HTMLImageElement`, "function"},
		{`document.body instanceof Node`, "true"},
		{`document.body instanceof Element`, "true"},
		{`document.body instanceof HTMLElement`, "true"},
		{`document instanceof Node`, "true"},
		{`document instanceof Document`, "true"},
		{`document.getElementById("i") instanceof Element`, "true"},
		{`Node.ELEMENT_NODE`, "1"},
		{`Node.TEXT_NODE`, "3"},
		{`Node.COMMENT_NODE`, "8"},
		{`Node.DOCUMENT_NODE`, "9"},
		{`Node.DOCUMENT_FRAGMENT_NODE`, "11"},
	} {
		if got := stealthEval(t, p, tc.expr); got != tc.want {
			t.Errorf("%s = %s, want %s", tc.expr, got, tc.want)
		}
	}
}

func TestImageConstructorBuildsImg(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	for _, tc := range []struct{ expr, want string }{
		{`new Image().tagName`, "IMG"},
		{`new Image(10, 20).getAttribute("width")`, "10"},
		{`new Image(10, 20).getAttribute("height")`, "20"},
		{`new Image("30").getAttribute("width")`, "30"},
		{`new Image() instanceof Element`, "true"},
	} {
		if got := stealthEval(t, p, tc.expr); got != tc.want {
			t.Errorf("%s = %s, want %s", tc.expr, got, tc.want)
		}
	}
}

// CSS.escape is used to build selector strings from data; the digit and lone
// hyphen cases are the ones a naive implementation gets wrong.
func TestCSSEscape(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	for _, tc := range []struct{ expr, want string }{
		{`CSS.escape("a b")`, `a\ b`},
		{`CSS.escape("a#b")`, `a\#b`},
		{`CSS.escape("1a")`, `\31 a`},
		{`CSS.escape("-")`, `\-`},
		{`CSS.escape("_a-b")`, `_a-b`},
		{`CSS.escape("")`, ``},
		{`typeof CSS.supports`, "function"},
	} {
		if got := stealthEval(t, p, tc.expr); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// WeakRef is ES2021 and goja has none; framework schedulers construct one per
// node, so its absence stopped those scripts outright.
func TestWeakRefPolyfill(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	for _, tc := range []struct{ expr, want string }{
		{`typeof WeakRef`, "function"},
		{`(function(){var o={x:1};return new WeakRef(o).deref()===o})()`, "true"},
		{`typeof FinalizationRegistry`, "function"},
	} {
		if got := stealthEval(t, p, tc.expr); got != tc.want {
			t.Errorf("%s = %s, want %s", tc.expr, got, tc.want)
		}
	}
}

// document.fonts.load(...).catch(...) is a common probe; a missing load() threw
// a TypeError out of the whole script.
func TestFontsLoadIsThenable(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	if got := stealthEval(t, p, `typeof document.fonts.load("10pt serif").then`); got != "function" {
		t.Errorf("fonts.load(...).then = %s, want function", got)
	}
}

// sendBeacon must exist and issue a real request; an empty URL is rejected
// rather than throwing.
func TestNavigatorSendBeacon(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	if got := stealthEval(t, p, `typeof navigator.sendBeacon`); got != "function" {
		t.Fatalf("typeof navigator.sendBeacon = %s, want function", got)
	}
	if got := stealthEval(t, p, `navigator.sendBeacon("")`); got != "false" {
		t.Errorf(`sendBeacon("") = %s, want false`, got)
	}
}

// SetInitScripts is the hook behind CDP Page.addScriptToEvaluateOnNewDocument:
// it must run before the page's own scripts see the environment.
func TestSetInitScriptsRunsBeforePageScripts(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	p.SetInitScripts([]string{`window.__injected = "yes"`})
	err := p.SetContent(`<html><body><script>window.__seen = window.__injected;</script></body></html>`,
		"https://example.test/")
	if err != nil {
		t.Fatalf("SetContent: %v", err)
	}
	if got := stealthEval(t, p, `window.__seen`); got != "yes" {
		t.Fatalf("page script saw %v, want yes", got)
	}
}

func TestSetUserAgentOverridesNavigator(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	p.SetUserAgent("TestAgent/1.0")
	if got := p.UserAgent(); got != "TestAgent/1.0" {
		t.Fatalf("UserAgent() = %q, want TestAgent/1.0", got)
	}
	if err := p.SetContent(`<html><body></body></html>`, "https://example.test/"); err != nil {
		t.Fatalf("SetContent: %v", err)
	}
	if got := stealthEval(t, p, `navigator.userAgent`); got != "TestAgent/1.0" {
		t.Errorf("navigator.userAgent = %q, want TestAgent/1.0", got)
	}
	// An empty value must not blank a working UA.
	p.SetUserAgent("")
	if got := p.UserAgent(); got != "TestAgent/1.0" {
		t.Errorf("UserAgent() after empty set = %q, want unchanged", got)
	}
}
