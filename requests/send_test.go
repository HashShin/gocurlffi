package requests

import (
	"strings"
	"testing"
	"time"

	"github.com/HashShin/gocurlffi/impersonate"
)

// Send is the struct-shaped entry point: the same request can be built as a
// value instead of being spread across options at the call site.
func TestSendRequestStruct(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	s := NewSession()
	defer s.Close()

	// The body comes from the endpoint that echoes it.
	rsp, err := s.Send(Request{
		Method: "POST",
		URL:    srv.URL + "/post",
		JSON:   map[string]any{"hello": "world"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var posted struct {
		Body string `json:"body"`
	}
	if err := rsp.JSON(&posted); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if posted.Body != `{"hello":"world"}` {
		t.Errorf("body = %q", posted.Body)
	}

	// The headers come from the endpoint that echoes them; /post does not.
	rsp, err = s.Send(Request{
		URL: srv.URL + "/get",
		Headers: Headers{
			"Accept: application/json",
			"X-Custom: value",
		},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var echoed struct {
		Headers map[string][]string `json:"headers"`
	}
	if err := rsp.JSON(&echoed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := echoed.Headers["X-Custom"]; len(got) == 0 || got[0] != "value" {
		t.Errorf("X-Custom = %v, want value", got)
	}
	if got := echoed.Headers["Accept"]; len(got) == 0 || got[0] != "application/json" {
		t.Errorf("Accept = %v, want application/json", got)
	}
}

// An empty Method means GET, and the zero-valued fields fall back to the
// session rather than overriding it with an empty setting.
func TestSendDefaultsToGet(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	s := NewSession()
	defer s.Close()

	rsp, err := s.Send(Request{URL: srv.URL + "/get"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var out struct {
		Method string `json:"method"`
	}
	if err := rsp.JSON(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Method != "GET" {
		t.Errorf("method = %q, want GET", out.Method)
	}
}

// A Request carries the impersonation target and per-request timeout, so a
// configured request value behaves like the equivalent options.
func TestSendHonoursRequestSettings(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	s := NewSession()
	defer s.Close()

	rsp, err := s.Send(Request{
		URL:         srv.URL + "/get",
		Impersonate: impersonate.Chrome131,
		Timeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var out struct {
		Headers map[string][]string `json:"headers"`
	}
	if err := rsp.JSON(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ua := ""
	if v := out.Headers["User-Agent"]; len(v) > 0 {
		ua = v[0]
	}
	if !strings.Contains(ua, "Chrome/131") {
		t.Errorf("User-Agent = %q, want the Chrome 131 preset", ua)
	}
}

// Body is the raw-bytes path, and JSON wins when both are set.
func TestSendBodyAndJSONPrecedence(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()
	s := NewSession()
	defer s.Close()

	rsp, err := s.Send(Request{Method: "POST", URL: srv.URL + "/post", Body: []byte("raw=1")})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var raw struct {
		Body string `json:"body"`
	}
	if err := rsp.JSON(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if raw.Body != "raw=1" {
		t.Errorf("Body = %q, want raw=1", raw.Body)
	}

	rsp, err = s.Send(Request{
		Method: "POST",
		URL:    srv.URL + "/post",
		Body:   []byte("ignored"),
		JSON:   map[string]any{"a": 1},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var both struct {
		Body string `json:"body"`
	}
	if err := rsp.JSON(&both); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if both.Body != `{"a":1}` {
		t.Errorf("Body = %q, want the JSON to win", both.Body)
	}
}
