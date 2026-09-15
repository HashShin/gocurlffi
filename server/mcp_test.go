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

func mcpCall(t *testing.T, m *MCP, id int, method string, params any) map[string]any {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	data, _ := json.Marshal(req)
	resp := m.Handle(data)
	if resp == nil {
		t.Fatalf("%s returned no response", method)
	}
	b, _ := json.Marshal(resp)
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if e, ok := out["error"]; ok {
		t.Fatalf("%s error: %v", method, e)
	}
	return out
}

func mcpToolText(t *testing.T, res map[string]any) string {
	t.Helper()
	result, _ := res["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("tool result had no content: %v", res)
	}
	item, _ := content[0].(map[string]any)
	s, _ := item["text"].(string)
	return s
}

func newMCP(t *testing.T) (*MCP, string) {
	t.Helper()
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<html><head><title>MCP</title>
			<script type="application/ld+json">{"@type":"Thing","name":"gadget"}</script>
			</head><body><h1>Hello MCP</h1></body></html>`)
	}))
	t.Cleanup(page.Close)
	b := browser.New(browser.Options{})
	t.Cleanup(b.Close)
	return NewMCP(b), page.URL
}

func TestMCPInitialize(t *testing.T) {
	m, _ := newMCP(t)
	res := mcpCall(t, m, 1, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	result, _ := res["result"].(map[string]any)
	if result["protocolVersion"] == nil {
		t.Fatalf("initialize returned %v", res)
	}
}

func TestMCPToolsList(t *testing.T) {
	m, _ := newMCP(t)
	res := mcpCall(t, m, 1, "tools/list", nil)
	result, _ := res["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) < 5 {
		t.Fatalf("tools/list returned %d tools, want several", len(tools))
	}
}

func TestMCPNavigateAndContent(t *testing.T) {
	m, url := newMCP(t)
	mcpCall(t, m, 1, "initialize", map[string]any{})
	res := mcpCall(t, m, 2, "tools/call", map[string]any{
		"name": "navigate", "arguments": map[string]any{"url": url},
	})
	if text := mcpToolText(t, res); !strings.Contains(text, "Hello MCP") {
		t.Fatalf("navigate text = %q", text)
	}

	res = mcpCall(t, m, 3, "tools/call", map[string]any{
		"name": "get_content", "arguments": map[string]any{"format": "markdown"},
	})
	if text := mcpToolText(t, res); !strings.Contains(text, "Hello MCP") {
		t.Fatalf("markdown = %q", text)
	}
}

func TestMCPEvaluateAndStructuredData(t *testing.T) {
	m, url := newMCP(t)
	mcpCall(t, m, 1, "initialize", map[string]any{})
	mcpCall(t, m, 2, "tools/call", map[string]any{
		"name": "navigate", "arguments": map[string]any{"url": url},
	})
	res := mcpCall(t, m, 3, "tools/call", map[string]any{
		"name": "evaluate", "arguments": map[string]any{"expression": "document.title"},
	})
	if text := mcpToolText(t, res); text != "MCP" {
		t.Fatalf("evaluate = %q, want MCP", text)
	}
	res = mcpCall(t, m, 4, "tools/call", map[string]any{
		"name": "structured_data", "arguments": map[string]any{},
	})
	if text := mcpToolText(t, res); !strings.Contains(text, "gadget") {
		t.Fatalf("structured_data = %q", text)
	}
}

func TestMCPUnknownTool(t *testing.T) {
	m, _ := newMCP(t)
	data, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "nope", "arguments": map[string]any{}},
	})
	resp := m.Handle(data)
	if resp == nil || resp.Error == nil {
		t.Fatalf("unknown tool should be a JSON-RPC error: %v", resp)
	}
}

func TestMCPUnknownMethod(t *testing.T) {
	m, _ := newMCP(t)
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "does/not/exist"})
	resp := m.Handle(data)
	if resp == nil || resp.Error == nil {
		t.Fatalf("unknown method should be a JSON-RPC error: %v", resp)
	}
}

func TestMCPNotificationHasNoReply(t *testing.T) {
	m, _ := newMCP(t)
	if resp := m.Handle([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); resp != nil {
		t.Fatalf("a notification should not be answered, got %v", resp)
	}
}

func TestMCPServeStdio(t *testing.T) {
	m, _ := newMCP(t)
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")
	var out strings.Builder
	if err := m.ServeStdio(in, &out); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}
	if !strings.Contains(out.String(), "protocolVersion") {
		t.Fatalf("stdio reply = %q", out.String())
	}
}
