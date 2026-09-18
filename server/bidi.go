package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/coder/websocket"

	"github.com/HashShin/shade/browser"
)

// WebDriver BiDi support. BiDi is a WebSocket protocol of JSON-RPC-like
// messages: a command is {id, method, params} and is answered with
// {type:"success", id, result} or {type:"error", id, error, message}. Events are
// {type:"event", method, params}. It shares the target/page layer with CDP, so
// a browser can serve both protocols on one port.

type bidiConn struct {
	server   *Server
	ws       *websocket.Conn
	mu       sync.Mutex
	sessions map[string]bool
	contexts map[string]*target
}

type bidiError struct {
	Kind    string `json:"error"`
	Message string `json:"message"`
}

func (e *bidiError) Error() string { return e.Kind + ": " + e.Message }

func bidiErr(kind, msg string) *bidiError { return &bidiError{Kind: kind, Message: msg} }

// serveBiDi upgrades a request and runs a BiDi session loop.
func (s *Server) serveBiDi(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	c := &bidiConn{
		server:   s,
		ws:       ws,
		sessions: map[string]bool{},
		contexts: map[string]*target{},
	}
	defer ws.Close(websocket.StatusNormalClosure, "")
	ctx := context.Background()
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		c.handle(data)
	}
}

func (c *bidiConn) send(v any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = c.ws.Write(context.Background(), websocket.MessageText, b)
}

func (c *bidiConn) event(method string, params any) {
	c.send(map[string]any{"type": "event", "method": method, "params": params})
}

func (c *bidiConn) handle(data []byte) {
	var req struct {
		ID     int             `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return
	}
	result, berr := c.dispatch(req.Method, req.Params)
	if berr != nil {
		c.send(map[string]any{"type": "error", "id": req.ID, "error": berr.Kind, "message": berr.Message})
		return
	}
	c.send(map[string]any{"type": "success", "id": req.ID, "result": result})
}

func (c *bidiConn) dispatch(method string, params json.RawMessage) (any, *bidiError) {
	switch method {
	case "session.status":
		return map[string]any{"ready": true, "message": "shade ready"}, nil
	case "session.new":
		id := randomID()
		c.sessions[id] = true
		return map[string]any{
			"sessionId": id,
			"capabilities": map[string]any{
				"acceptInsecureCerts": false,
				"browserName":         "shade",
				"browserVersion":      version,
				"platformName":        "any",
				"setWindowRect":       false,
			},
		}, nil
	case "session.end":
		return map[string]any{}, nil
	case "browsingContext.create":
		var p struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(params, &p)
		t := c.server.NewTarget("about:blank")
		c.contexts[t.id] = t
		return map[string]any{"context": t.id}, nil
	case "browsingContext.getTree":
		var contexts []map[string]any
		for _, t := range c.server.targetList() {
			contexts = append(contexts, map[string]any{
				"context": t.id, "url": t.page.URL, "children": []any{},
				"clientWindow": "", "originalOpener": nil,
			})
		}
		if contexts == nil {
			contexts = []map[string]any{}
		}
		return map[string]any{"contexts": contexts}, nil
	case "browsingContext.navigate":
		var p struct {
			Context string `json:"context"`
			URL     string `json:"url"`
		}
		_ = json.Unmarshal(params, &p)
		t := c.contextFor(p.Context)
		if t == nil {
			return nil, bidiErr("no such frame", p.Context)
		}
		t.mu.Lock()
		err := t.page.Load(p.URL)
		t.refresh()
		nav := randomID()
		t.mu.Unlock()
		if err != nil {
			return nil, bidiErr("unknown error", err.Error())
		}
		c.event("browsingContext.load", map[string]any{"context": t.id, "navigation": nav, "url": t.page.URL})
		return map[string]any{"navigation": nav, "url": t.page.URL}, nil
	case "browsingContext.close":
		var p struct {
			Context string `json:"context"`
		}
		_ = json.Unmarshal(params, &p)
		c.server.mu.Lock()
		delete(c.server.targets, p.Context)
		c.server.mu.Unlock()
		return map[string]any{}, nil
	case "browsingContext.captureScreenshot":
		var p struct {
			Context string `json:"context"`
		}
		_ = json.Unmarshal(params, &p)
		t := c.contextFor(p.Context)
		if t == nil {
			return nil, bidiErr("no such frame", p.Context)
		}
		t.mu.Lock()
		png, err := t.page.Screenshot(browser.ScreenshotOptions{Width: t.viewportWidth(), NoImages: false})
		t.mu.Unlock()
		if err != nil {
			return nil, bidiErr("unknown error", err.Error())
		}
		return map[string]any{"data": base64.StdEncoding.EncodeToString(png)}, nil
	case "script.evaluate":
		return c.scriptEvaluate(params)
	case "script.callFunction":
		return c.scriptCallFunction(params)
	case "script.getRealms":
		return map[string]any{"realms": []any{}}, nil
	case "input.performActions":
		return c.inputPerformActions(params)
	case "input.releaseActions":
		return map[string]any{}, nil
	case "network.addIntercept", "network.removeIntercept":
		return map[string]any{}, nil
	}
	return nil, bidiErr("unknown command", method)
}

func (c *bidiConn) contextFor(id string) *target {
	if t := c.contexts[id]; t != nil {
		return t
	}
	return c.server.getTarget(id)
}

func (c *bidiConn) scriptEvaluate(params json.RawMessage) (any, *bidiError) {
	var p struct {
		Expression string `json:"expression"`
		Target     struct {
			Context string `json:"context"`
		} `json:"target"`
	}
	_ = json.Unmarshal(params, &p)
	t := c.contextFor(p.Target.Context)
	if t == nil {
		return nil, bidiErr("no such frame", p.Target.Context)
	}
	t.mu.Lock()
	v, err := t.page.Eval(p.Expression)
	t.mu.Unlock()
	if err != nil {
		return nil, bidiErr("javascript error", err.Error())
	}
	return map[string]any{"type": "success", "result": bidiValue(v.Export()), "realm": "1"}, nil
}

func (c *bidiConn) scriptCallFunction(params json.RawMessage) (any, *bidiError) {
	var p struct {
		FunctionDeclaration string `json:"functionDeclaration"`
		Target              struct {
			Context string `json:"context"`
		} `json:"target"`
	}
	_ = json.Unmarshal(params, &p)
	t := c.contextFor(p.Target.Context)
	if t == nil {
		return nil, bidiErr("no such frame", p.Target.Context)
	}
	t.mu.Lock()
	v, err := t.page.CallOn(p.FunctionDeclaration, nil, nil)
	t.mu.Unlock()
	if err != nil {
		return nil, bidiErr("javascript error", err.Error())
	}
	return map[string]any{"type": "success", "result": bidiValue(v.Export()), "realm": "1"}, nil
}

func (c *bidiConn) inputPerformActions(params json.RawMessage) (any, *bidiError) {
	var p struct {
		Context string `json:"context"`
		Actions []struct {
			Type    string `json:"type"`
			ID      string `json:"id"`
			Actions []struct {
				Type string  `json:"type"`
				X    float64 `json:"x"`
				Y    float64 `json:"y"`
			} `json:"actions"`
		} `json:"actions"`
	}
	_ = json.Unmarshal(params, &p)
	t := c.contextFor(p.Context)
	if t == nil {
		return nil, bidiErr("no such frame", p.Context)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, seq := range p.Actions {
		for _, a := range seq.Actions {
			if a.Type == "pointerUp" {
				_ = t.page.ClickPoint(a.X, a.Y)
			}
		}
	}
	return map[string]any{}, nil
}

// bidiValue serializes a Go value the way BiDi expects a remote value.
func bidiValue(v any) map[string]any {
	switch x := v.(type) {
	case nil:
		return map[string]any{"type": "null"}
	case bool:
		return map[string]any{"type": "boolean", "value": x}
	case string:
		return map[string]any{"type": "string", "value": x}
	case int64:
		return map[string]any{"type": "number", "value": x}
	case int:
		return map[string]any{"type": "number", "value": x}
	case float64:
		return map[string]any{"type": "number", "value": x}
	default:
		b, _ := json.Marshal(x)
		return map[string]any{"type": "object", "value": json.RawMessage(b)}
	}
}
