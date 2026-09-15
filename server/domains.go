package server

import (
	"encoding/base64"
	"encoding/json"
	"net/url"

	"github.com/dop251/goja"

	"gocurlffi/browser"
)

// dispatchDomain handles the page-scoped domains: Page, Runtime, DOM, Input,
// Emulation and Network. The target's mutex is already held, so a page is only
// ever touched by one command at a time.
func (c *conn) dispatchDomain(t *target, sid, method string, params json.RawMessage) (any, *cdpError) {
	switch method {
	// --- Page ---
	case "Page.enable", "Page.setLifecycleEventsEnabled", "Page.setDownloadBehavior":
		return map[string]any{}, nil
	case "Page.getFrameTree":
		return map[string]any{"frameTree": t.frameTree()}, nil
	case "Page.navigate":
		return c.pageNavigate(t, sid, params)
	case "Page.reload":
		_ = t.page.Load(t.page.URL)
		c.emitLoad(t, sid)
		return map[string]any{}, nil
	case "Page.getNavigationHistory":
		return map[string]any{
			"currentIndex": 0,
			"entries": []map[string]any{{
				"id": 1, "url": t.page.URL, "userTypedURL": t.page.URL,
				"title": t.title, "transitionType": "typed",
			}},
		}, nil
	case "Page.captureScreenshot":
		png, err := t.page.Screenshot(browser.ScreenshotOptions{Width: t.viewportWidth(), NoImages: false})
		if err != nil {
			return nil, &cdpError{Code: -32000, Message: err.Error()}
		}
		return map[string]any{"data": base64.StdEncoding.EncodeToString(png)}, nil
	case "Page.getLayoutMetrics":
		return map[string]any{
			"layoutViewport": map[string]any{"pageX": 0, "pageY": 0, "clientWidth": t.viewportWidth(), "clientHeight": 600},
			"contentSize":    map[string]any{"x": 0, "y": 0, "width": t.viewportWidth(), "height": 600},
		}, nil
	case "Page.close":
		return map[string]any{}, nil
	case "Page.getResourceTree":
		return map[string]any{"frameTree": t.frameTree()}, nil

	// --- Runtime ---
	case "Runtime.enable":
		c.sendEvent(sid, "Runtime.executionContextCreated", map[string]any{
			"context": t.executionContext(),
		})
		return map[string]any{}, nil
	case "Runtime.evaluate":
		return c.runtimeEvaluate(t, params)
	case "Runtime.callFunctionOn":
		return c.runtimeCallFunction(t, params)
	case "Runtime.getProperties":
		return c.runtimeGetProperties(t, params)
	case "Runtime.releaseObject":
		var p struct {
			ObjectID string `json:"objectId"`
		}
		_ = json.Unmarshal(params, &p)
		t.refs.release(p.ObjectID)
		return map[string]any{}, nil
	case "Runtime.releaseObjectGroup", "Runtime.discardConsoleEntries",
		"Runtime.setCustomObjectFormatterEnabled", "Runtime.runIfWaitingForDebugger":
		return map[string]any{}, nil

	// --- DOM ---
	case "DOM.enable", "DOM.disable":
		return map[string]any{}, nil
	case "DOM.getDocument":
		return map[string]any{"root": t.describeNode(t.page.Document(), 2)}, nil
	case "DOM.querySelector":
		return c.domQuery(t, params, false)
	case "DOM.querySelectorAll":
		return c.domQuery(t, params, true)
	case "DOM.getOuterHTML":
		var p struct {
			NodeID int `json:"nodeId"`
		}
		_ = json.Unmarshal(params, &p)
		n := t.nodeByID(p.NodeID)
		if n == nil {
			return map[string]any{"outerHTML": ""}, nil
		}
		return map[string]any{"outerHTML": t.page.OuterHTMLOf(n)}, nil
	case "DOM.resolveNode":
		var p struct {
			NodeID int `json:"nodeId"`
		}
		_ = json.Unmarshal(params, &p)
		n := t.nodeByID(p.NodeID)
		if n == nil {
			return nil, &cdpError{Code: -32000, Message: "no such node"}
		}
		return map[string]any{"object": t.remoteObject(t.page.NodeValue(n))}, nil

	// --- Input ---
	case "Input.dispatchMouseEvent":
		return c.inputMouse(t, params)
	case "Input.dispatchKeyEvent", "Input.insertText":
		return map[string]any{}, nil

	// --- Emulation ---
	case "Emulation.setDeviceMetricsOverride":
		var p struct {
			Width  float64 `json:"width"`
			Height float64 `json:"height"`
		}
		_ = json.Unmarshal(params, &p)
		if p.Width > 0 {
			t.viewport = int(p.Width)
			t.page.SetViewportWidth(p.Width)
		}
		return map[string]any{}, nil
	case "Emulation.clearDeviceMetricsOverride":
		t.viewport = 0
		return map[string]any{}, nil

	// --- Network ---
	case "Network.enable", "Network.setCacheDisabled", "Network.setBypassServiceWorker":
		return map[string]any{}, nil
	case "Network.getCookies":
		return map[string]any{"cookies": []any{}}, nil
	case "Network.clearBrowserCookies", "Network.clearBrowserCache":
		return map[string]any{}, nil
	}
	return nil, methodNotFound(method)
}

// --- Page helpers ---

func (t *target) viewportWidth() int {
	if t.viewport > 0 {
		return t.viewport
	}
	return 1280
}

func (t *target) frameTree() map[string]any {
	return map[string]any{
		"frame": map[string]any{
			"id":                t.id,
			"loaderId":          t.id,
			"url":               t.page.URL,
			"domainAndRegistry": hostOf(t.page.URL),
			"mimeType":          "text/html",
			"securityOrigin":    originOf(t.page.URL),
		},
		"childFrames": []any{},
	}
}

func (t *target) executionContext() map[string]any {
	return map[string]any{
		"id": 1, "origin": originOf(t.page.URL), "name": "",
		"uniqueId": "1", "auxData": map[string]any{"isDefault": true, "type": "default", "frameId": t.id},
	}
}

func (c *conn) pageNavigate(t *target, sid string, params json.RawMessage) (any, *cdpError) {
	var p struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(params, &p)
	if err := t.page.Load(p.URL); err != nil {
		c.sendEvent(sid, "Page.frameNavigated", map[string]any{"frame": t.frameTree()["frame"]})
		return map[string]any{"frameId": t.id, "loaderId": t.id}, nil
	}
	t.refresh()
	c.emitLoad(t, sid)
	return map[string]any{"frameId": t.id, "loaderId": t.id}, nil
}

// emitLoad emits the navigation lifecycle events a client waits for.
func (c *conn) emitLoad(t *target, sid string) {
	frameID := t.id
	c.sendEvent(sid, "Page.frameNavigated", map[string]any{"frame": t.frameTree()["frame"]})
	c.sendEvent(sid, "Page.lifecycleEvent", map[string]any{"frameId": frameID, "loaderId": frameID, "name": "load", "timestamp": 0})
	c.sendEvent(sid, "Page.domContentEventFired", map[string]any{"timestamp": 0})
	c.sendEvent(sid, "Page.loadEventFired", map[string]any{"timestamp": 0})
}

func (t *target) refresh() {
	t.url = t.page.URL
	t.title = t.page.Title()
}

// --- Runtime ---

func (c *conn) runtimeEvaluate(t *target, params json.RawMessage) (any, *cdpError) {
	var p struct {
		Expression string `json:"expression"`
	}
	_ = json.Unmarshal(params, &p)
	v, err := t.page.Eval(p.Expression)
	if err != nil {
		return map[string]any{
			"result":           map[string]any{"type": "undefined"},
			"exceptionDetails": t.exception(err),
		}, nil
	}
	return map[string]any{"result": t.remoteObject(v)}, nil
}

func (c *conn) runtimeCallFunction(t *target, params json.RawMessage) (any, *cdpError) {
	var p struct {
		FunctionDeclaration string `json:"functionDeclaration"`
		ObjectID            string `json:"objectId"`
		Arguments           []struct {
			Value    any    `json:"value"`
			ObjectID string `json:"objectId"`
		} `json:"arguments"`
	}
	_ = json.Unmarshal(params, &p)
	var this goja.Value
	if p.ObjectID != "" {
		this = t.refs.get(p.ObjectID)
	}
	var args []goja.Value
	for _, a := range p.Arguments {
		if a.ObjectID != "" {
			args = append(args, t.refs.get(a.ObjectID))
			continue
		}
		if rt := t.page.Runtime(); rt != nil {
			args = append(args, rt.ToValue(a.Value))
		}
	}
	v, err := t.page.CallOn(p.FunctionDeclaration, this, args)
	if err != nil {
		return map[string]any{
			"result":           map[string]any{"type": "undefined"},
			"exceptionDetails": t.exception(err),
		}, nil
	}
	return map[string]any{"result": t.remoteObject(v)}, nil
}

func (c *conn) runtimeGetProperties(t *target, params json.RawMessage) (any, *cdpError) {
	var p struct {
		ObjectID string `json:"objectId"`
	}
	_ = json.Unmarshal(params, &p)
	rt := t.page.Runtime()
	v := t.refs.get(p.ObjectID)
	if rt == nil || v == nil {
		return map[string]any{"result": []any{}}, nil
	}
	obj := v.ToObject(rt)
	if obj == nil {
		return map[string]any{"result": []any{}}, nil
	}
	var out []map[string]any
	for _, k := range obj.Keys() {
		out = append(out, map[string]any{
			"name": k, "enumerable": true, "writable": true, "configurable": true,
			"value": t.remoteObject(obj.Get(k)),
		})
	}
	if out == nil {
		out = []map[string]any{}
	}
	return map[string]any{"result": out}, nil
}

func (t *target) exception(err error) map[string]any {
	return map[string]any{
		"exceptionId": 1, "text": err.Error(), "lineNumber": 0, "columnNumber": 0,
		"exception": map[string]any{"type": "object", "className": "Error", "description": err.Error()},
	}
}

// --- DOM ---

func (c *conn) domQuery(t *target, params json.RawMessage, all bool) (any, *cdpError) {
	var p struct {
		NodeID   int    `json:"nodeId"`
		Selector string `json:"selector"`
	}
	_ = json.Unmarshal(params, &p)
	ctx := t.nodeByID(p.NodeID)
	if ctx == nil {
		ctx = t.page.Document()
	}
	if all {
		nodes := t.page.QuerySelectorAllFrom(ctx, p.Selector)
		ids := make([]int, 0, len(nodes))
		for _, n := range nodes {
			ids = append(ids, t.addNode(n))
		}
		return map[string]any{"nodeIds": ids}, nil
	}
	n := t.page.QuerySelectorFrom(ctx, p.Selector)
	if n == nil {
		return map[string]any{"nodeId": 0}, nil
	}
	return map[string]any{"nodeId": t.addNode(n)}, nil
}

// --- Input ---

func (c *conn) inputMouse(t *target, params json.RawMessage) (any, *cdpError) {
	var p struct {
		Type string  `json:"type"`
		X    float64 `json:"x"`
		Y    float64 `json:"y"`
	}
	_ = json.Unmarshal(params, &p)
	if p.Type == "mouseReleased" || p.Type == "mousePressed" {
		// A press followed by a release is one click; act on the release if the
		// client sends both, otherwise on the press.
		_ = t.page.ClickPoint(p.X, p.Y)
	}
	return map[string]any{}, nil
}

// --- URL helpers ---

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return "://"
	}
	return u.Scheme + "://" + u.Host
}
