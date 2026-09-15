package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"gocurlffi/browser"
)

type cdpClient struct {
	t    *testing.T
	conn *websocket.Conn
	id   int
}

func dial(t *testing.T, url string) *cdpClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return &cdpClient{t: t, conn: conn}
}

// call sends a command and reads until its reply, skipping events.
func (c *cdpClient) call(session, method string, params any) map[string]any {
	c.t.Helper()
	c.id++
	msg := map[string]any{"id": c.id, "method": method}
	if session != "" {
		msg["sessionId"] = session
	}
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
			if e, ok := m["error"]; ok {
				c.t.Fatalf("%s returned error: %v", method, e)
			}
			if r, ok := m["result"].(map[string]any); ok {
				return r
			}
			return map[string]any{}
		}
	}
}

func newTestServer(t *testing.T) (*httptest.Server, *httptest.Server) {
	t.Helper()
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<html><head><title>CDP Test</title></head><body><h1 id="h">hello</h1></body></html>`)
	}))
	t.Cleanup(page.Close)
	b := browser.New(browser.Options{})
	t.Cleanup(b.Close)
	srv := New(Config{Browser: b})
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)
	return httpSrv, page
}

func wsURL(httpURL, path string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http") + path
}

func TestJSONVersion(t *testing.T) {
	httpSrv, _ := newTestServer(t)
	resp, err := http.Get(httpSrv.URL + "/json/version")
	if err != nil {
		t.Fatalf("GET /json/version: %v", err)
	}
	defer resp.Body.Close()
	var v map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := v["webSocketDebuggerUrl"]; !ok {
		t.Fatalf("/json/version missing webSocketDebuggerUrl: %v", v)
	}
}

func TestBrowserGetVersionOverWS(t *testing.T) {
	httpSrv, _ := newTestServer(t)
	c := dial(t, wsURL(httpSrv.URL, "/devtools/browser/gobrowser"))
	res := c.call("", "Browser.getVersion", nil)
	if res["product"] == nil {
		t.Fatalf("Browser.getVersion returned %v", res)
	}
}

func TestCreateTargetNavigateEvaluate(t *testing.T) {
	httpSrv, page := newTestServer(t)
	c := dial(t, wsURL(httpSrv.URL, "/devtools/browser/gobrowser"))

	created := c.call("", "Target.createTarget", map[string]any{"url": "about:blank"})
	targetID, _ := created["targetId"].(string)
	if targetID == "" {
		t.Fatalf("no targetId: %v", created)
	}
	attached := c.call("", "Target.attachToTarget", map[string]any{"targetId": targetID, "flatten": true})
	session, _ := attached["sessionId"].(string)
	if session == "" {
		t.Fatalf("no sessionId: %v", attached)
	}

	c.call(session, "Page.enable", nil)
	c.call(session, "Runtime.enable", nil)
	c.call(session, "Page.navigate", map[string]any{"url": page.URL})

	evaluated := c.call(session, "Runtime.evaluate", map[string]any{"expression": "document.title"})
	result, _ := evaluated["result"].(map[string]any)
	if result["value"] != "CDP Test" {
		t.Fatalf("document.title = %v, want CDP Test", result["value"])
	}
}

func TestScreenshotOverCDP(t *testing.T) {
	httpSrv, page := newTestServer(t)
	c := dial(t, wsURL(httpSrv.URL, "/devtools/browser/gobrowser"))
	created := c.call("", "Target.createTarget", map[string]any{"url": page.URL})
	targetID, _ := created["targetId"].(string)
	attached := c.call("", "Target.attachToTarget", map[string]any{"targetId": targetID, "flatten": true})
	session, _ := attached["sessionId"].(string)
	c.call(session, "Emulation.setDeviceMetricsOverride", map[string]any{"width": 800, "height": 600, "deviceScaleFactor": 1, "mobile": false})
	shot := c.call(session, "Page.captureScreenshot", map[string]any{"format": "png"})
	data, _ := shot["data"].(string)
	if len(data) < 100 {
		t.Fatalf("screenshot data too small: %d bytes", len(data))
	}
}

func TestDOMQueryAndEvaluateNode(t *testing.T) {
	httpSrv, page := newTestServer(t)
	c := dial(t, wsURL(httpSrv.URL, "/devtools/browser/gobrowser"))
	created := c.call("", "Target.createTarget", map[string]any{"url": page.URL})
	targetID, _ := created["targetId"].(string)
	attached := c.call("", "Target.attachToTarget", map[string]any{"targetId": targetID, "flatten": true})
	session, _ := attached["sessionId"].(string)

	doc := c.call(session, "DOM.getDocument", nil)
	root, _ := doc["root"].(map[string]any)
	nodeID := int(root["nodeId"].(float64))
	q := c.call(session, "DOM.querySelector", map[string]any{"nodeId": nodeID, "selector": "#h"})
	if q["nodeId"].(float64) == 0 {
		t.Fatalf("querySelector found nothing: %v", q)
	}
	html := c.call(session, "DOM.getOuterHTML", map[string]any{"nodeId": int(q["nodeId"].(float64))})
	if !strings.Contains(html["outerHTML"].(string), "hello") {
		t.Fatalf("outerHTML = %v", html["outerHTML"])
	}
}
