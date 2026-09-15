package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type bidiClient struct {
	t    *testing.T
	conn *websocket.Conn
	id   int
}

func dialBidi(t *testing.T, url string) *bidiClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return &bidiClient{t: t, conn: conn}
}

func (c *bidiClient) call(method string, params any) map[string]any {
	c.t.Helper()
	c.id++
	msg := map[string]any{"id": c.id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	data, _ := json.Marshal(msg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
		c.t.Fatalf("write %s: %v", method, err)
	}
	for {
		_, resp, err := c.conn.Read(ctx)
		if err != nil {
			c.t.Fatalf("read reply to %s: %v", method, err)
		}
		var m map[string]any
		if err := json.Unmarshal(resp, &m); err != nil {
			continue
		}
		if id, _ := m["id"].(float64); int(id) == c.id {
			if m["type"] == "error" {
				c.t.Fatalf("%s error: %v", method, m["message"])
			}
			if r, ok := m["result"].(map[string]any); ok {
				return r
			}
			return map[string]any{}
		}
	}
}

func TestBiDiSessionAndNavigate(t *testing.T) {
	httpSrv, page := newTestServer(t)
	c := dialBidi(t, wsURL(httpSrv.URL, "/session"))

	newRes := c.call("session.new", map[string]any{"capabilities": map[string]any{}})
	if newRes["sessionId"] == nil {
		t.Fatalf("session.new returned %v", newRes)
	}
	created := c.call("browsingContext.create", map[string]any{"type": "tab"})
	contextID, _ := created["context"].(string)
	if contextID == "" {
		t.Fatalf("browsingContext.create returned %v", created)
	}
	nav := c.call("browsingContext.navigate", map[string]any{"context": contextID, "url": page.URL})
	if nav["url"] != page.URL {
		t.Fatalf("navigate url = %v, want %v", nav["url"], page.URL)
	}
	eval := c.call("script.evaluate", map[string]any{
		"expression": "document.title",
		"target":     map[string]any{"context": contextID},
	})
	result, _ := eval["result"].(map[string]any)
	if result["value"] != "CDP Test" {
		t.Fatalf("script.evaluate result = %v", eval)
	}
}

func TestBiDiGetTree(t *testing.T) {
	httpSrv, _ := newTestServer(t)
	c := dialBidi(t, wsURL(httpSrv.URL, "/session"))
	c.call("session.new", map[string]any{"capabilities": map[string]any{}})
	c.call("browsingContext.create", map[string]any{"type": "tab"})
	tree := c.call("browsingContext.getTree", nil)
	contexts, _ := tree["contexts"].([]any)
	if len(contexts) != 1 {
		t.Fatalf("getTree returned %d contexts, want 1", len(contexts))
	}
}

func TestBiDiStatus(t *testing.T) {
	httpSrv, _ := newTestServer(t)
	c := dialBidi(t, wsURL(httpSrv.URL, "/session"))
	res := c.call("session.status", nil)
	if res["ready"] != true {
		t.Fatalf("session.status = %v", res)
	}
}
