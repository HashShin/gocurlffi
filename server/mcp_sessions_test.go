package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gocurlffi/browser"
)

func stringReader(s string) *strings.Reader { return strings.NewReader(s) }

func containsStr(haystack, needle string) bool { return strings.Contains(haystack, needle) }

// sessionPage serves a page whose body says which path it came from, so two
// sessions can be told apart.
func sessionPage(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<html><body><h1>page `+r.URL.Path+`</h1></body></html>`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMCPSessionsAreIsolated(t *testing.T) {
	page := sessionPage(t)
	b := browser.New(browser.Options{})
	t.Cleanup(b.Close)
	m := NewMCP(b)

	a := m.Session("a")
	c := m.Session("b")

	call := func(sess *mcpSession, id int, name string, args map[string]any) string {
		t.Helper()
		data, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]any{"name": name, "arguments": args},
		})
		resp := m.HandleSession(sess, data)
		if resp == nil || resp.Error != nil {
			t.Fatalf("%s: %v", name, resp)
		}
		b, _ := json.Marshal(resp)
		var out map[string]any
		_ = json.Unmarshal(b, &out)
		return mcpToolText(t, out)
	}

	call(a, 1, "navigate", map[string]any{"url": page.URL + "/one"})
	call(c, 2, "navigate", map[string]any{"url": page.URL + "/two"})

	if got := call(a, 3, "get_content", map[string]any{}); !containsStr(got, "/one") {
		t.Fatalf("session a sees %q, want /one", got)
	}
	if got := call(c, 4, "get_content", map[string]any{}); !containsStr(got, "/two") {
		t.Fatalf("session b sees %q, want /two", got)
	}
}

// The same id must land on the same page, which is how two agents deliberately
// share one browsing context.
func TestMCPSameSessionSharesPage(t *testing.T) {
	page := sessionPage(t)
	b := browser.New(browser.Options{})
	t.Cleanup(b.Close)
	m := NewMCP(b)

	data := func(name string, args map[string]any) []byte {
		b, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": name, "arguments": args},
		})
		return b
	}
	sess := m.Session("shared")
	_ = m.HandleSession(sess, data("navigate", map[string]any{"url": page.URL + "/shared"}))

	// A second lookup for the same id is the same session and the same page.
	again := m.Session("shared")
	if again != sess {
		t.Fatal("Session(id) returned a different session for the same id")
	}
	resp := m.HandleSession(again, data("get_content", map[string]any{}))
	raw, _ := json.Marshal(resp)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if got := mcpToolText(t, out); !containsStr(got, "/shared") {
		t.Fatalf("shared session sees %q", got)
	}
}

// Each session forks the browser, so its cookies are its own.
func TestMCPSessionsHaveSeparateBrowsers(t *testing.T) {
	b := browser.New(browser.Options{})
	t.Cleanup(b.Close)
	m := NewMCP(b)
	a, c := m.Session("a"), m.Session("b")
	if a.browser == c.browser {
		t.Fatal("two sessions share one Browser, so they share cookies")
	}
	if a.browser == b {
		t.Fatal("a session uses the template browser rather than a fork")
	}
}

func TestMCPSessionTools(t *testing.T) {
	b := browser.New(browser.Options{})
	t.Cleanup(b.Close)
	m := NewMCP(b)

	call := func(sess *mcpSession, name string, args map[string]any) string {
		t.Helper()
		data, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": name, "arguments": args},
		})
		resp := m.HandleSession(sess, data)
		if resp == nil || resp.Error != nil {
			t.Fatalf("%s: %v", name, resp)
		}
		raw, _ := json.Marshal(resp)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return mcpToolText(t, out)
	}

	def := m.Session(defaultSessionID)
	id := call(def, "session_new", map[string]any{})
	if id == "" {
		t.Fatal("session_new returned no id")
	}
	list := call(def, "session_list", map[string]any{})
	if !containsStr(list, id) {
		t.Fatalf("session_list = %q, want it to contain %q", list, id)
	}
	if got := call(def, "session_close", map[string]any{"session": id}); !containsStr(got, "closed") {
		t.Fatalf("session_close = %q", got)
	}
	if !m.CloseSession("default") {
		t.Fatal("closing the default session failed")
	}
}

func TestMCPCloseSessionUnknown(t *testing.T) {
	b := browser.New(browser.Options{})
	t.Cleanup(b.Close)
	m := NewMCP(b)
	if m.CloseSession("nope") {
		t.Fatal("CloseSession reported success for an unknown id")
	}
}

// --- HTTP transport ---------------------------------------------------------

func TestMCPHTTPSessionHeader(t *testing.T) {
	page := sessionPage(t)
	b := browser.New(browser.Options{})
	t.Cleanup(b.Close)
	m := NewMCP(b)
	srv := httptest.NewServer(http.HandlerFunc(m.ServeHTTP))
	t.Cleanup(srv.Close)

	post := func(sessionID, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", stringReader(body))
		req.Header.Set("Content-Type", "application/json")
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	resp := post("", init)
	assigned := resp.Header.Get("Mcp-Session-Id")
	_ = resp.Body.Close()
	if assigned == "" {
		t.Fatal("a request without a session id was not assigned one")
	}
	if assigned == defaultSessionID {
		t.Fatal("session id is still the constant placeholder")
	}

	// Sending the assigned id back must reach the same session.
	nav := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"navigate","arguments":{"url":"` + page.URL + `/http"}}}`
	resp2 := post(assigned, nav)
	_ = resp2.Body.Close()
	sess := m.Session(assigned)
	if p := sess.currentPage(); p == nil {
		t.Fatal("the navigated page did not land on the session named by the header")
	}

	// A second header-less request gets a different session.
	resp3 := post("", init)
	other := resp3.Header.Get("Mcp-Session-Id")
	_ = resp3.Body.Close()
	if other == assigned {
		t.Fatalf("two header-less requests were given the same session %q", other)
	}
}

func TestMCPHTTPDeleteClosesSession(t *testing.T) {
	b := browser.New(browser.Options{})
	t.Cleanup(b.Close)
	m := NewMCP(b)
	m.Session("doomed")

	req, _ := http.NewRequest(http.MethodDelete, "/mcp", nil)
	req.Header.Set("Mcp-Session-Id", "doomed")
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE returned %d, want 204", rec.Code)
	}
	if m.CloseSession("doomed") {
		t.Fatal("the session survived DELETE")
	}
}

func TestMCPHTTPDeleteUnknown(t *testing.T) {
	b := browser.New(browser.Options{})
	t.Cleanup(b.Close)
	m := NewMCP(b)
	req, _ := http.NewRequest(http.MethodDelete, "/mcp", nil)
	req.Header.Set("Mcp-Session-Id", "ghost")
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("DELETE of an unknown session returned %d, want 404", rec.Code)
	}
}
