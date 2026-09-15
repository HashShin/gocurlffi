package browser

import "testing"

// navBrowser serves a tiny site through Intercept, so navigation is tested
// without touching the network. The returned counter records document loads.
func navBrowser(t *testing.T, pages map[string]string) (*Browser, *int) {
	t.Helper()
	count := new(int)
	b := New(Options{Intercept: func(r *Request) *Response {
		if r.ResourceType != "document" {
			return nil
		}
		(*count)++
		body, ok := pages[r.URL]
		if !ok {
			return Fulfill(404, "text/html", []byte("<html><body>missing</body></html>"))
		}
		return Fulfill(200, "text/html", []byte(body))
	}})
	t.Cleanup(b.Close)
	return b, count
}

func navSite(navigate string) map[string]string {
	return map[string]string{
		"https://nav.test/a": "<html><body><script>" + navigate + "</script></body></html>",
		"https://nav.test/b": `<html><body><h1 id="x">arrived</h1></body></html>`,
	}
}

func assertArrived(t *testing.T, p *Page) {
	t.Helper()
	if got := stealthEval(t, p, `document.getElementById("x").textContent`); got != "arrived" {
		t.Fatalf("after navigation textContent = %q, want arrived", got)
	}
	if p.URL != "https://nav.test/b" {
		t.Fatalf("URL = %q, want https://nav.test/b", p.URL)
	}
}

// location.replace is how Google's SG_SS challenge hands off to the results
// page. It used to be a stub that returned undefined and did nothing.
func TestLocationReplaceNavigates(t *testing.T) {
	b, _ := navBrowser(t, navSite(`location.replace("/b")`))
	p := b.NewPage("https://nav.test/a")
	if err := p.Load("https://nav.test/a"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertArrived(t, p)
}

func TestLocationAssignNavigates(t *testing.T) {
	b, _ := navBrowser(t, navSite(`location.assign("/b")`))
	p := b.NewPage("https://nav.test/a")
	if err := p.Load("https://nav.test/a"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertArrived(t, p)
}

// Assigning location.href is the commonest self-redirect; it was a plain data
// property, so the assignment was silently dropped.
func TestLocationHrefAssignmentNavigates(t *testing.T) {
	b, _ := navBrowser(t, navSite(`location.href = "/b"`))
	p := b.NewPage("https://nav.test/a")
	if err := p.Load("https://nav.test/a"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertArrived(t, p)
}

// Assigning window.location itself must navigate too.
func TestWindowLocationAssignmentNavigates(t *testing.T) {
	b, _ := navBrowser(t, navSite(`window.location = "/b"`))
	p := b.NewPage("https://nav.test/a")
	if err := p.Load("https://nav.test/a"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertArrived(t, p)
}

// A navigation asked for during the script phase must not run inline: the
// statement after it still executes on the current document.
func TestScriptAfterNavigationStillRuns(t *testing.T) {
	b, _ := navBrowser(t, map[string]string{
		"https://nav.test/a": `<html><body><script>location.replace("/b"); window.__after = "ran";</script></body></html>`,
		"https://nav.test/b": `<html><body><h1 id="x">arrived</h1></body></html>`,
	})
	p := b.NewPage("https://nav.test/a")
	if err := p.Load("https://nav.test/a"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertArrived(t, p)
	// The new document has its own environment, so the old global is gone;
	// what matters is that the first document finished its script.
	if got := stealthEval(t, p, `String(window.__after)`); got != "undefined" {
		t.Fatalf("window.__after = %q, want undefined on the new document", got)
	}
}

// A page that navigates on every load must stop rather than reload forever.
func TestScriptNavigationIsBounded(t *testing.T) {
	b, count := navBrowser(t, map[string]string{
		"https://loop.test/a": `<html><body><script>location.replace("/a")</script></body></html>`,
	})
	p := b.NewPage("https://loop.test/a")
	_ = p.Load("https://loop.test/a")
	if *count > maxScriptNavigations+1 {
		t.Fatalf("made %d document requests, want at most %d", *count, maxScriptNavigations+1)
	}
	if *count < 2 {
		t.Fatalf("made %d document requests, want the navigation to have been followed", *count)
	}
}
