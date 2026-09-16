package server

import (
	"encoding/base64"
	"encoding/json"
	"net/url"

	"github.com/dop251/goja"

	"github.com/HashShin/gocurlffi/browser"
)

// dispatchDomain handles the page-scoped domains: Page, Runtime, DOM, Input,
// Emulation and Network. The target's mutex is already held, so a page is only
// ever touched by one command at a time.
func (c *conn) dispatchDomain(t *target, sid, method string, params json.RawMessage) (any, *cdpError) {
	// Domains this browser does not implement, but that a client sends while
	// setting up. Answering them with an empty result keeps the client moving;
	// a genuinely unknown method still gets a protocol error.
	if noopMethods[method] {
		return map[string]any{}, nil
	}
	switch method {
	// --- Page ---
	case "Page.enable", "Page.setLifecycleEventsEnabled", "Page.setDownloadBehavior":
		return map[string]any{}, nil
	case "Page.addScriptToEvaluateOnNewDocument":
		var p struct {
			Source string `json:"source"`
		}
		_ = json.Unmarshal(params, &p)
		id := randomID()
		t.initScripts = append(t.initScripts, initScript{id: id, source: p.Source})
		t.page.SetInitScripts(t.initSources())
		return map[string]any{"identifier": id}, nil
	case "Page.removeScriptToEvaluateOnNewDocument":
		var p struct {
			Identifier string `json:"identifier"`
		}
		_ = json.Unmarshal(params, &p)
		kept := t.initScripts[:0]
		for _, s := range t.initScripts {
			if s.id != p.Identifier {
				kept = append(kept, s)
			}
		}
		t.initScripts = kept
		t.page.SetInitScripts(t.initSources())
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
		c.destroyTarget(t)
		return map[string]any{}, nil
	case "Page.getResourceTree":
		return map[string]any{"frameTree": t.frameTree()}, nil
	case "Page.createIsolatedWorld":
		// One JavaScript environment backs every world, but the client tracks
		// execution contexts by id and name, so announce one for the world it
		// asked for. Puppeteer runs page.title() and friends in this world.
		var p struct {
			FrameID   string `json:"frameId"`
			WorldName string `json:"worldName"`
		}
		_ = json.Unmarshal(params, &p)
		t.contextSeq++
		id := t.contextSeq
		if id < 2 {
			id = 2
		}
		c.sendEvent(sid, "Runtime.executionContextCreated", map[string]any{
			"context": map[string]any{
				"id": id, "origin": originOf(t.page.URL), "name": p.WorldName, "uniqueId": "ctx-" + itoa(id),
				"auxData": map[string]any{"isDefault": false, "type": "isolated", "frameId": t.id},
			},
		})
		return map[string]any{"executionContextId": id}, nil

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
	case "Performance.getMetrics":
		return map[string]any{"metrics": []any{}}, nil
	case "DOM.describeNode":
		var p struct {
			NodeID   int    `json:"nodeId"`
			ObjectID string `json:"objectId"`
		}
		_ = json.Unmarshal(params, &p)
		n := t.nodeByID(p.NodeID)
		if n == nil {
			return map[string]any{"node": nil}, nil
		}
		return map[string]any{"node": t.describeNode(n, 0)}, nil

	// --- Network ---
	case "Network.enable", "Network.setCacheDisabled", "Network.setBypassServiceWorker":
		return map[string]any{}, nil
	case "Network.setUserAgentOverride":
		var p struct {
			UserAgent string `json:"userAgent"`
		}
		_ = json.Unmarshal(params, &p)
		t.uaOverride = p.UserAgent
		t.page.SetUserAgent(p.UserAgent)
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

// frameTree is called with the target lock already held, so it does not take
// it again.
func (t *target) frameTree() map[string]any {
	return map[string]any{
		"frame": map[string]any{
			"id":                t.id,
			"loaderId":          t.loaderID,
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
		"uniqueId": "ctx-1", "auxData": map[string]any{"isDefault": true, "type": "default", "frameId": t.id},
	}
}

func (c *conn) pageNavigate(t *target, sid string, params json.RawMessage) (any, *cdpError) {
	var p struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(params, &p)
	err := t.page.Load(p.URL)
	if err == nil {
		t.refresh()
	}
	// A new document gets a new loader id, which is how a client recognizes a
	// navigation commit.
	t.loaderID = randomID()
	// The lifecycle events are queued to run after the reply, so a client that
	// registers its load waiter after sending Page.navigate still sees them.
	c.pending = append(c.pending, func() {
		t.mu.Lock()
		c.emitLoad(t, sid)
		t.mu.Unlock()
	})
	return map[string]any{"frameId": t.id, "loaderId": t.loaderID}, nil
}

// emitLoad emits the navigation lifecycle events a client waits for. The
// "init" event is what carries the new loader id (a client learns the document
// changed from it), so it precedes "load".
func (c *conn) emitLoad(t *target, sid string) {
	frameID := t.id
	c.sendEvent(sid, "Page.frameNavigated", map[string]any{"frame": t.frameTree()["frame"]})
	for _, name := range []string{"init", "DOMContentLoaded", "load"} {
		c.sendEvent(sid, "Page.lifecycleEvent", map[string]any{"frameId": frameID, "loaderId": t.loaderID, "name": name, "timestamp": 0})
	}
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

// noopMethods are commands a client sends while setting up that this browser
// does not model. Answering with an empty result keeps Puppeteer/Playwright
// moving instead of failing on the first unimplemented domain.
var noopMethods = map[string]bool{
	"Fetch.enable": true, "Fetch.disable": true, "Fetch.continueRequest": true,
	"Fetch.failRequest": true, "Fetch.fulfillRequest": true, "Fetch.continueWithAuth": true,
	"Fetch.getResponseBody": true, "Fetch.takeResponseBodyAsStream": true,
	"Performance.enable": true, "Performance.disable": true,
	"Log.enable": true, "Log.disable": true, "Log.clear": true,
	"Runtime.addBinding": true, "Runtime.removeBinding": true, "Runtime.compileScript": true,
	"Runtime.setAsyncCallStackDepth": true, "Runtime.setCustomObjectFormatterEnabled": true,
	"Page.setBypassCSP": true, "Page.setInterceptFileChooserDialog": true,
	"Page.setDocumentContent": true, "Page.bringToFront": true,
	"Page.startScreencast": true, "Page.stopScreencast": true,
	"Emulation.setFocusEmulationEnabled": true, "Emulation.setTouchEmulationEnabled": true,
	"Emulation.setDefaultBackgroundColorOverride": true, "Emulation.setEmulatedMedia": true,
	"Emulation.setUserAgentOverride": true, "Emulation.setScriptExecutionDisabled": true,
	"Emulation.setLocaleOverride": true, "Emulation.setTimezoneOverride": true,
	"Network.setExtraHTTPHeaders":    true,
	"Network.setRequestInterception": true, "Network.emulateNetworkConditions": true,
	"Network.setBlockedURLs": true, "Network.disable": true,
	"Security.enable": true, "Security.disable": true, "Security.setIgnoreCertificateErrors": true,
	"Browser.getWindowForTarget": true, "Browser.setWindowBounds": true,
	"Browser.getWindowBounds": true, "Browser.setPermission": true,
	"Browser.grantPermissions": true, "Browser.resetPermissions": true,
	"Overlay.enable": true, "Overlay.disable": true,
	"Debugger.enable": true, "Debugger.disable": true, "Debugger.setBreakpointsActive": true,
	"Accessibility.enable": true, "Accessibility.disable": true,
	"Animation.enable": true, "Animation.disable": true,
	"CSS.enable": true, "CSS.disable": true,
	"Profiler.enable": true, "Profiler.disable": true,
	"ServiceWorker.enable": true, "ServiceWorker.disable": true,
	"Target.setDiscoverTargets": true, "Target.getBrowserContexts": true,
	"DOM.getFrameOwner": true, "DOM.getAttributes": true,
	"DOM.getBoxModel": true, "DOM.requestChildNodes": true, "DOM.focus": true,
	"IndexedDB.enable": true, "IndexedDB.disable": true,
	"Storage.getUsageAndQuota": true, "CacheStorage.requestCacheNames": true,
	"Page.setWebLifecycleState": true, "Input.setIgnoreInputEvents": true,
	"Audits.enable": true, "Audits.disable": true,
}
