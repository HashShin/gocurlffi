package server

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// debugCDP logs incoming commands and outgoing events when the environment
// variable is set, which turns a stuck client into a readable transcript.
var debugCDP = os.Getenv("GOBROWSER_CDP_DEBUG") != ""

// request is one CDP command.
type request struct {
	ID        int             `json:"id"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
	SessionID string          `json:"sessionId"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *cdpError) Error() string { return fmt.Sprintf("%d: %s", e.Code, e.Message) }

func methodNotFound(m string) *cdpError {
	return &cdpError{Code: -32601, Message: "method not found: " + m}
}

// handle parses one message and dispatches it, writing the reply.
func (c *conn) handle(data []byte) {
	var req request
	if err := json.Unmarshal(data, &req); err != nil {
		return
	}
	// Resolve the target: a sessionId names one, otherwise the connection's
	// own page (browser-level connections have none).
	t := c.sessions[req.SessionID]
	if t == nil {
		t = c.page
	}
	if debugCDP {
		fmt.Fprintf(os.Stderr, "CDP <- %s sid=%q\n", req.Method, req.SessionID)
	}
	result, rerr := c.dispatch(t, req.Method, req.Params)
	reply := map[string]any{"id": req.ID}
	if req.SessionID != "" {
		reply["sessionId"] = req.SessionID
	}
	if rerr != nil {
		reply["error"] = rerr
	} else {
		if result == nil {
			result = map[string]any{}
		}
		reply["result"] = result
	}
	c.send(reply)
	// Events that must arrive after the reply (navigation lifecycle) run a
	// moment later, so a client that registers its load waiter after sending
	// the command still sees them.
	if len(c.pending) > 0 {
		fns := c.pending
		c.pending = nil
		go func() {
			time.Sleep(navEventDelay)
			for _, f := range fns {
				f()
			}
		}()
	}
}

// navEventDelay is how long navigation events wait after the reply, long enough
// for a client to install its waiters.
const navEventDelay = 60 * time.Millisecond

// dispatch routes a method to its handler. It is the whole CDP surface the
// server supports; an unknown method returns a protocol error rather than
// hanging the client.
func (c *conn) dispatch(t *target, method string, params json.RawMessage) (any, *cdpError) {
	// Browser and Target methods work without a page target.
	switch method {
	case "Browser.getVersion":
		return map[string]any{
			"protocolVersion": "1.3",
			"product":         "gobrowser/" + version,
			"revision":        "",
			"userAgent":       userAgent(c),
			"jsVersion":       "goja",
		}, nil
	case "Browser.close":
		go c.ws.Close(1000, "closing")
		return map[string]any{}, nil
	case "Target.setDiscoverTargets":
		for _, tg := range c.server.targetList() {
			c.sendEvent("", "Target.targetCreated", map[string]any{"targetInfo": targetInfo(tg)})
		}
		return map[string]any{}, nil
	case "Target.setAutoAttach":
		var p struct {
			AutoAttach bool `json:"autoAttach"`
		}
		_ = json.Unmarshal(params, &p)
		already := c.autoAttach
		c.autoAttach = p.AutoAttach
		// Attach existing targets once, the first time auto-attach turns on.
		if p.AutoAttach && !already {
			for _, tg := range c.server.targetList() {
				if !c.isAttached(tg) {
					c.attachAuto(tg)
				}
			}
		}
		return map[string]any{}, nil
	case "Target.setAttachToFrames":
		return map[string]any{}, nil
	case "Target.getTargets":
		var infos []map[string]any
		for _, tg := range c.server.targetList() {
			infos = append(infos, targetInfo(tg))
		}
		if infos == nil {
			infos = []map[string]any{}
		}
		return map[string]any{"targetInfos": infos}, nil
	case "Target.createTarget":
		var p struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(params, &p)
		nt := c.server.NewTarget(p.URL)
		c.sendEvent("", "Target.targetCreated", map[string]any{"targetInfo": targetInfo(nt)})
		if c.autoAttach && !c.isAttached(nt) {
			c.attachAuto(nt)
		}
		return map[string]any{"targetId": nt.id}, nil
	case "Target.attachToTarget":
		return c.attachToTarget(params)
	case "Target.detachFromTarget":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(params, &p)
		delete(c.sessions, p.SessionID)
		return map[string]any{}, nil
	case "Target.closeTarget":
		var p struct {
			TargetID string `json:"targetId"`
		}
		_ = json.Unmarshal(params, &p)
		if tg := c.server.getTarget(p.TargetID); tg != nil {
			c.destroyTarget(tg)
		}
		return map[string]any{"success": true}, nil
	case "Target.getBrowserContexts":
		return map[string]any{"browserContextIds": []string{}}, nil
	case "Target.createBrowserContext":
		return map[string]any{"browserContextId": randomID()}, nil
	case "Target.disposeBrowserContext":
		return map[string]any{}, nil
	}

	if t == nil {
		return nil, &cdpError{Code: -32000, Message: "no target for method " + method}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	sid := c.sessionFor(t)
	return c.dispatchDomain(t, sid, method, params)
}

// sessionFor returns the session id a target is attached under, empty for a
// page-level connection.
func (c *conn) sessionFor(t *target) string {
	for id, tg := range c.sessions {
		if tg == t {
			return id
		}
	}
	return ""
}

// destroyTarget detaches and removes a target, emitting the events a client
// waits for so Page.close resolves instead of hanging.
func (c *conn) destroyTarget(t *target) {
	for sid, tg := range c.sessions {
		if tg == t {
			delete(c.sessions, sid)
		}
	}
	t.mu.Lock()
	sessions := make([]string, 0, len(t.sessions))
	for sid := range t.sessions {
		sessions = append(sessions, sid)
		delete(t.sessions, sid)
	}
	t.mu.Unlock()
	for _, sid := range sessions {
		c.sendEvent("", "Target.detachedFromTarget", map[string]any{"sessionId": sid, "targetId": t.id})
	}
	c.sendEvent("", "Target.targetDestroyed", map[string]any{"targetId": t.id})
	c.server.mu.Lock()
	delete(c.server.targets, t.id)
	c.server.mu.Unlock()
}

// isAttached reports whether this connection already holds a session for a
// target, so auto-attach does not attach the same target twice and loop.
func (c *conn) isAttached(t *target) bool {
	for _, tg := range c.sessions {
		if tg == t {
			return true
		}
	}
	return false
}

// attachAuto attaches a target and emits the event a client with auto-attach on
// is waiting for.
func (c *conn) attachAuto(t *target) {
	sid := randomID()
	c.sessions[sid] = t
	t.mu.Lock()
	t.sessions[sid] = &session{id: sid, target: t}
	t.mu.Unlock()
	c.sendEvent("", "Target.attachedToTarget", map[string]any{
		"sessionId":          sid,
		"targetInfo":         targetInfo(t),
		"waitingForDebugger": false,
	})
}

func (c *conn) attachToTarget(params json.RawMessage) (any, *cdpError) {
	var p struct {
		TargetID  string `json:"targetId"`
		Flatten   bool   `json:"flatten"`
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(params, &p)
	tg := c.server.getTarget(p.TargetID)
	if tg == nil {
		return nil, &cdpError{Code: -32000, Message: "no such target"}
	}
	sid := randomID()
	c.sessions[sid] = tg
	tg.mu.Lock()
	tg.sessions[sid] = &session{id: sid, target: tg}
	tg.mu.Unlock()
	c.sendEvent("", "Target.attachedToTarget", map[string]any{
		"sessionId":          sid,
		"targetInfo":         targetInfo(tg),
		"waitingForDebugger": false,
	})
	return map[string]any{"sessionId": sid}, nil
}

func targetInfo(t *target) map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	return map[string]any{
		"targetId": t.id,
		"type":     "page",
		"title":    t.title,
		"url":      t.url,
		"attached": false,
	}
}

func userAgent(c *conn) string {
	if c.page != nil {
		return c.page.page.UserAgent()
	}
	return ""
}
