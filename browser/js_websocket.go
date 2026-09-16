package browser

import (
	"context"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/dop251/goja"
)

// WebSocket. A page opens a socket to stream data; here the connection is real
// (through the same pure-Go WebSocket client the CDP server uses), dialled
// synchronously in the constructor to match the engine's synchronous model. A
// reader goroutine queues incoming messages, and they are delivered as message
// events while the page's timer queue is drained, so a script that waits for a
// message in a timer callback receives it.

type pageWebSocket struct {
	e    *jsEnv
	obj  *goja.Object
	url  string
	conn *websocket.Conn

	mu     sync.Mutex
	queue  []wsIncoming
	closed bool
	// closePending records that the peer closed the connection and the close
	// event has not been delivered yet. The reader goroutine only sets it;
	// drain, on the page's own goroutine, delivers it.
	closePending bool

	state    int // 0 connecting, 1 open, 2 closing, 3 closed
	handlers map[string][]goja.Callable
	props    map[string]goja.Value
	binary   bool
}

type wsIncoming struct {
	data     []byte
	isBinary bool
}

// installWebSocket exposes the WebSocket global.
func (e *jsEnv) installWebSocket(rt *goja.Runtime) {
	ctor := e.vm.ToValue(func(call goja.ConstructorCall) *goja.Object {
		url := argString(call.Argument(0))
		ws := e.newWebSocket(url)
		return ws.obj
	})
	_ = rt.Set("WebSocket", ctor)
	// Ready-state constants live on the constructor and the prototype.
	if obj, ok := ctor.(*goja.Object); ok {
		_ = obj.Set("CONNECTING", 0)
		_ = obj.Set("OPEN", 1)
		_ = obj.Set("CLOSING", 2)
		_ = obj.Set("CLOSED", 3)
	}
}

func (e *jsEnv) newWebSocket(url string) *pageWebSocket {
	ws := &pageWebSocket{
		e: e, url: url,
		handlers: map[string][]goja.Callable{},
		props:    map[string]goja.Value{},
	}
	o := e.vm.NewObject()
	ws.obj = o
	_ = o.Set("url", url)
	_ = o.Set("CONNECTING", 0)
	_ = o.Set("OPEN", 1)
	_ = o.Set("CLOSING", 2)
	_ = o.Set("CLOSED", 3)
	e.accessor(o, "readyState", func(goja.FunctionCall) goja.Value {
		ws.mu.Lock()
		defer ws.mu.Unlock()
		return e.vm.ToValue(ws.state)
	}, nil)
	e.accessor(o, "binaryType", func(goja.FunctionCall) goja.Value {
		if ws.binary {
			return e.vm.ToValue("arraybuffer")
		}
		return e.vm.ToValue("blob")
	}, func(call goja.FunctionCall) goja.Value {
		ws.binary = argString(call.Argument(0)) == "arraybuffer"
		return goja.Undefined()
	})
	for _, name := range []string{"onopen", "onmessage", "onerror", "onclose"} {
		name := name
		e.accessor(o, name, func(goja.FunctionCall) goja.Value {
			if v := ws.props[name]; v != nil {
				return v
			}
			return goja.Null()
		}, func(call goja.FunctionCall) goja.Value {
			ws.props[name] = call.Argument(0)
			return goja.Undefined()
		})
	}
	_ = o.Set("addEventListener", func(call goja.FunctionCall) goja.Value {
		typ := argString(call.Argument(0))
		if len(call.Arguments) > 1 {
			if fn, ok := goja.AssertFunction(call.Argument(1)); ok {
				ws.handlers[typ] = append(ws.handlers[typ], fn)
			}
		}
		return goja.Undefined()
	})
	_ = o.Set("removeEventListener", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("send", func(call goja.FunctionCall) goja.Value {
		ws.send(argString(call.Argument(0)))
		return goja.Undefined()
	})
	_ = o.Set("close", func(call goja.FunctionCall) goja.Value {
		ws.close()
		return goja.Undefined()
	})
	ws.connect()
	e.webSockets = append(e.webSockets, ws)
	return ws
}

func (ws *pageWebSocket) connect() {
	ctx, cancel := context.WithTimeout(context.Background(), ws.e.page.netTimeout())
	defer cancel()
	conn, _, err := websocket.Dial(ctx, ws.url, nil)
	if err != nil {
		ws.mu.Lock()
		ws.state = 3
		ws.closed = true
		ws.mu.Unlock()
		ws.e.schedule(func() { ws.fire("error", nil) })
		ws.e.schedule(func() { ws.fire("close", nil) })
		return
	}
	ws.conn = conn
	ws.mu.Lock()
	ws.state = 1
	ws.mu.Unlock()
	ws.e.schedule(func() { ws.fire("open", nil) })
	go ws.readLoop()
}

func (ws *pageWebSocket) readLoop() {
	ctx := context.Background()
	for {
		typ, data, err := ws.conn.Read(ctx)
		if err != nil {
			ws.mu.Lock()
			wasClosed := ws.closed
			ws.closed = true
			ws.state = 3
			if !wasClosed {
				// Only record it. Firing or scheduling from this goroutine
				// would touch the page's timer queue and event handlers
				// concurrently with the goroutine that owns them.
				ws.closePending = true
			}
			ws.mu.Unlock()
			return
		}
		ws.mu.Lock()
		ws.queue = append(ws.queue, wsIncoming{data: data, isBinary: typ == websocket.MessageBinary})
		ws.mu.Unlock()
	}
}

func (ws *pageWebSocket) send(data string) {
	if ws.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), ws.e.page.netTimeout())
	defer cancel()
	_ = ws.conn.Write(ctx, websocket.MessageText, []byte(data))
}

func (ws *pageWebSocket) close() {
	if ws.conn == nil {
		return
	}
	ws.mu.Lock()
	ws.state = 2
	ws.mu.Unlock()
	_ = ws.conn.Close(websocket.StatusNormalClosure, "")
	ws.mu.Lock()
	ws.state = 3
	ws.closed = true
	// An explicit close reports the event itself, so the reader's pending one
	// is dropped rather than delivered twice.
	ws.closePending = false
	ws.mu.Unlock()
	ws.e.schedule(func() { ws.fire("close", nil) })
}

// drain dispatches queued messages as message events. It runs while the page's
// timer queue is drained, so a message handler that arrived a moment ago runs.
// It is also where a close the peer initiated is reported, because this is the
// page's own goroutine.
func (ws *pageWebSocket) drain() bool {
	any := false
	for {
		ws.mu.Lock()
		if len(ws.queue) == 0 {
			ws.mu.Unlock()
			break
		}
		any = true
		msg := ws.queue[0]
		ws.queue = ws.queue[1:]
		ws.mu.Unlock()

		ev := ws.e.vm.NewObject()
		_ = ev.Set("type", "message")
		_ = ev.Set("target", ws.obj)
		_ = ev.Set("data", ws.messageData(msg))
		ws.deliver("message", ev)
	}

	ws.mu.Lock()
	closePending := ws.closePending
	ws.closePending = false
	ws.mu.Unlock()
	if closePending {
		ws.fire("close", nil)
		any = true
	}
	return any
}

// messageData renders a frame the way a browser does: a text frame arrives as a
// string, and a binary frame as an ArrayBuffer or a Blob according to
// binaryType. Delivering a string for a binary frame would leave a page that
// asked for one of those with the wrong type.
func (ws *pageWebSocket) messageData(msg wsIncoming) goja.Value {
	if !msg.isBinary {
		return ws.e.vm.ToValue(string(msg.data))
	}
	if ws.binary {
		return ws.e.vm.ToValue(ws.e.vm.NewArrayBuffer(append([]byte(nil), msg.data...)))
	}
	return ws.e.newBlobObject(&jsBlobData{data: msg.data})
}

func (ws *pageWebSocket) fire(typ string, ev *goja.Object) {
	if ev == nil {
		ev = ws.e.vm.NewObject()
		_ = ev.Set("type", typ)
		_ = ev.Set("target", ws.obj)
	}
	ws.deliver(typ, ev)
}

func (ws *pageWebSocket) deliver(typ string, ev *goja.Object) {
	for _, h := range ws.handlers[typ] {
		func() {
			defer func() { _ = recover() }()
			_, _ = h(ws.obj, ev)
		}()
	}
	if v := ws.props["on"+typ]; v != nil {
		if fn, ok := goja.AssertFunction(v); ok {
			func() {
				defer func() { _ = recover() }()
				_, _ = fn(ws.obj, ev)
			}()
		}
	}
}

// drainWebSockets delivers queued socket messages and reports whether any were
// delivered. It is called while the page drains timers, so socket callbacks and
// timers interleave the way a browser runs its task sources.
func (e *jsEnv) drainWebSockets() bool {
	any := false
	for _, ws := range e.webSockets {
		if ws.drain() {
			any = true
		}
	}
	return any
}

// hasOpenWebSocket reports whether a socket is connecting or open, so the loader
// may wait a little longer for a message.
func (e *jsEnv) hasOpenWebSocket() bool {
	for _, ws := range e.webSockets {
		ws.mu.Lock()
		open := ws.state == 0 || ws.state == 1
		ws.mu.Unlock()
		if open {
			return true
		}
	}
	return false
}

// netTimeout bounds a socket dial or write.
func (p *Page) netTimeout() time.Duration {
	if p.browser.opts.Timeout > 0 {
		return p.browser.opts.Timeout
	}
	return 30 * time.Second
}
