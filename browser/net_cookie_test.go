package browser

import (
	"testing"
	"time"

	"gocurlffi/requests"
)

func storedCookie(t *testing.T, p *Page, name string) requests.Cookie {
	t.Helper()
	ck, ok := p.browser.sess.Cookies().GetCookie(name)
	if !ok {
		t.Fatalf("cookie %q is not in the jar", name)
	}
	return *ck
}

func cookiePage(t *testing.T, pageURL string) *Page {
	t.Helper()
	b := newTestBrowser(t)
	p := b.NewPage(pageURL)
	if err := p.SetContent(`<html><body></body></html>`, pageURL); err != nil {
		t.Fatalf("SetContent: %v", err)
	}
	return p
}

// A document.cookie write is scoped to the document's host and directory.
// Storing it with no domain made the jar offer it to every host the page went
// on to contact.
func TestDocumentCookieIsScopedToHost(t *testing.T) {
	p := cookiePage(t, "https://a.test/")
	if _, err := p.Eval(`document.cookie = "sid=abc"`); err != nil {
		t.Fatalf("set cookie: %v", err)
	}
	ck := storedCookie(t, p, "sid")
	if ck.Value != "abc" {
		t.Errorf("value = %q, want abc", ck.Value)
	}
	if ck.Domain != "a.test" {
		t.Errorf("Domain = %q, want a.test (an empty domain is sent to every host)", ck.Domain)
	}
	if ck.Path != "/" {
		t.Errorf("Path = %q, want /", ck.Path)
	}
}

// A Domain attribute widens the scope, and the leading dot is dropped the way
// the jar stores it.
func TestDocumentCookieDomainAttribute(t *testing.T) {
	p := cookiePage(t, "https://a.test/")
	if _, err := p.Eval(`document.cookie = "wide=1; Domain=.a.test"`); err != nil {
		t.Fatalf("set cookie: %v", err)
	}
	if got := storedCookie(t, p, "wide").Domain; got != "a.test" {
		t.Errorf("Domain = %q, want a.test", got)
	}
}

// Without a Path attribute the cookie is scoped to the document's directory.
func TestDocumentCookieDefaultPathIsDirectory(t *testing.T) {
	p := cookiePage(t, "https://a.test/dir/page")
	if _, err := p.Eval(`document.cookie = "deep=1"`); err != nil {
		t.Fatalf("set cookie: %v", err)
	}
	if got := storedCookie(t, p, "deep").Path; got != "/dir" {
		t.Errorf("Path = %q, want /dir", got)
	}
	// An explicit Path wins.
	explicit := cookiePage(t, "https://a.test/dir/page")
	if _, err := explicit.Eval(`document.cookie = "e=1; Path=/other"`); err != nil {
		t.Fatalf("set cookie: %v", err)
	}
	if got := storedCookie(t, explicit, "e").Path; got != "/other" {
		t.Errorf("explicit Path = %q, want /other", got)
	}
}

// An empty value, a past Expires, or Max-Age=0 clears the cookie.
func TestDocumentCookieDeletion(t *testing.T) {
	for _, tc := range []struct{ name, expr string }{
		{"empty value", `document.cookie = "gone="`},
		{"past expires", `document.cookie = "gone=; expires=Thu, 01 Jan 1970 00:00:00 GMT"`},
		{"max-age zero", `document.cookie = "gone=1; max-age=0"`},
	} {
		p := cookiePage(t, "https://a.test/")
		if _, err := p.Eval(`document.cookie = "gone=1"`); err != nil {
			t.Fatalf("%s: seed cookie: %v", tc.name, err)
		}
		if _, ok := p.browser.sess.Cookies().Get("gone"); !ok {
			t.Fatalf("%s: seed cookie missing", tc.name)
		}
		if _, err := p.Eval(tc.expr); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if _, ok := p.browser.sess.Cookies().Get("gone"); ok {
			t.Errorf("%s: cookie survived", tc.name)
		}
	}
}

// Attributes are recorded, not discarded.
func TestDocumentCookieAttributesAreStored(t *testing.T) {
	p := cookiePage(t, "https://a.test/")
	if _, err := p.Eval(`document.cookie = "keep=1; expires=Fri, 01 Jan 2100 00:00:00 GMT; Secure"`); err != nil {
		t.Fatalf("set cookie: %v", err)
	}
	ck := storedCookie(t, p, "keep")
	if !ck.Secure {
		t.Error("Secure attribute was not recorded")
	}
	if ck.Expires.IsZero() || ck.Expires.Before(time.Now()) {
		t.Errorf("Expires = %v, want a future time", ck.Expires)
	}
}

// document.cookie must still read back what was written.
func TestDocumentCookieReadBack(t *testing.T) {
	p := cookiePage(t, "https://a.test/")
	if _, err := p.Eval(`document.cookie = "one=1"`); err != nil {
		t.Fatalf("set cookie: %v", err)
	}
	if got := stealthEval(t, p, `document.cookie`); got != "one=1" {
		t.Errorf("document.cookie = %q, want one=1", got)
	}
}
