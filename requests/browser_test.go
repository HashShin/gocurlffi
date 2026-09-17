package requests

import (
	"errors"
	"strings"
	"testing"
)

// stubRenderer stands in for the browser package, which is what the plug-in
// point exists for: this package must be testable without it.
type stubRenderer struct {
	got   Request
	close int
}

func (s *stubRenderer) Send(req Request) (*Response, error) {
	s.got = req
	rsp := NewResponse()
	rsp.URL = req.URL
	rsp.Content = []byte("<html>rendered</html>")
	return rsp, nil
}

func (s *stubRenderer) Close() { s.close++ }

// A request with Browser set goes to the installed renderer instead of the
// transport, and the session creates exactly one of them, so a page and a plain
// request share a cookie jar.
func TestBrowserRequestUsesTheRenderer(t *testing.T) {
	stub := &stubRenderer{}
	created := 0
	UseBrowser(func(*Session) BrowserRenderer {
		created++
		return stub
	})
	defer UseBrowser(nil)

	sess := NewSession()
	sess.SetCookies(map[string]string{"a": "1"})
	rsp, err := sess.Send(Request{URL: "https://example.test/", Browser: true})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := string(rsp.Content); got != "<html>rendered</html>" {
		t.Errorf("body = %q, want the rendered document", got)
	}
	if stub.got.URL != "https://example.test/" {
		t.Errorf("renderer got URL %q", stub.got.URL)
	}
	if _, err := sess.Send(Request{URL: "https://example.test/two", Browser: true}); err != nil {
		t.Fatalf("second Send: %v", err)
	}
	if created != 1 {
		t.Errorf("renderer created %d times, want once per session", created)
	}
	sess.Close()
	if stub.close != 1 {
		t.Errorf("renderer closed %d times, want once with the session", stub.close)
	}
}

// A browser loads a URL. A method or a body is refused before the renderer is
// consulted, rather than being silently dropped.
func TestBrowserRequestRejectsWhatTheBrowserCannotSend(t *testing.T) {
	UseBrowser(func(*Session) BrowserRenderer { return &stubRenderer{} })
	defer UseBrowser(nil)

	sess := NewSession()
	defer sess.Close()
	for _, req := range []Request{
		{URL: "https://example.test/", Method: "POST", Browser: true},
		{URL: "https://example.test/", Body: []byte("x"), Browser: true},
		{URL: "https://example.test/", JSON: map[string]any{"a": 1}, Browser: true},
	} {
		_, err := sess.Send(req)
		var ie *InterfaceError
		if !errors.As(err, &ie) {
			t.Errorf("Send(%+v) error = %v, want an *InterfaceError", req, err)
			continue
		}
		if !strings.Contains(strings.ToLower(ie.Error()), "browser") {
			t.Errorf("error %q does not mention the browser", ie.Error())
		}
	}
}

// Without the browser package linked in, the error names the import that is
// missing rather than failing obscurely.
func TestBrowserRequestWithoutTheBrowserPackage(t *testing.T) {
	UseBrowser(nil)
	sess := NewSession()
	defer sess.Close()
	_, err := sess.Send(Request{URL: "https://example.test/", Browser: true})
	var ie *InterfaceError
	if !errors.As(err, &ie) {
		t.Fatalf("error = %v, want an *InterfaceError", err)
	}
	if !strings.Contains(ie.Error(), "gocurlffi/browser") {
		t.Errorf("error %q does not name the package to import", ie.Error())
	}
}

// A closed session refuses a browser request the same way it refuses a plain
// one.
func TestBrowserRequestOnAClosedSession(t *testing.T) {
	UseBrowser(func(*Session) BrowserRenderer { return &stubRenderer{} })
	defer UseBrowser(nil)

	sess := NewSession()
	sess.Close()
	_, err := sess.Send(Request{URL: "https://example.test/", Browser: true})
	var sc *SessionClosed
	if !errors.As(err, &sc) {
		t.Fatalf("error = %v, want a *SessionClosed", err)
	}
}

// Send and Browse take the same three inputs, so the common one-liner needs no
// literal and a request built elsewhere is passed as it is.
func TestRequestInputShapes(t *testing.T) {
	req := Request{URL: "https://example.test/", Impersonate: "chrome150"}
	for _, tc := range []struct {
		name string
		in   RequestTypes
	}{
		{"a URL string", "https://example.test/"},
		{"a Request", req},
		{"a *Request", &req},
	} {
		got, err := ToRequest(tc.in)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got.URL != "https://example.test/" {
			t.Errorf("%s: URL = %q", tc.name, got.URL)
		}
		if tc.name != "a URL string" && got.Impersonate != "chrome150" {
			t.Errorf("%s: Impersonate = %q, want the request's", tc.name, got.Impersonate)
		}
	}

	// A nil interface lands in the type switch's default, and a nil *Request
	// has to be reported rather than dereferenced.
	for _, bad := range []RequestTypes{42, nil, []string{"u"}} {
		if _, err := ToRequest(bad); err == nil {
			t.Errorf("ToRequest(%#v) accepted a type it cannot send", bad)
		} else {
			var ie *InterfaceError
			if !errors.As(err, &ie) {
				t.Errorf("ToRequest(%#v) error = %v, want an *InterfaceError", bad, err)
			}
		}
	}
	if _, err := ToRequest((*Request)(nil)); err == nil {
		t.Errorf("a nil *Request was accepted")
	}
}
