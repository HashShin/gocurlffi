package browser

import (
	"fmt"

	"github.com/dop251/goja"
)

// Web Workers. A page starts a worker to run a script off the main thread; here
// the worker runs in its own goja runtime on its own goroutine, so the main
// runtime is never touched from another goroutine. postMessage is a channel in
// each direction: the main thread queues to the worker, and the worker's replies
// are delivered as message events while the page's task queue is drained.
// importScripts loads and evaluates more worker scripts synchronously, which is
// how classic workers pull in dependencies.

type workerMsg struct {
	value any
}

type jsWorker struct {
	e   *jsEnv
	obj *goja.Object
	url string

	toWorker   chan workerMsg
	fromWorker chan any
	done       chan struct{}
	stopped    bool

	handlers map[string][]goja.Callable
	props    map[string]goja.Value
}

// installWorker exposes the Worker global.
func (e *jsEnv) installWorker(rt *goja.Runtime) {
	_ = rt.Set("Worker", e.vm.ToValue(func(call goja.ConstructorCall) *goja.Object {
		url := resolveURL(e.page.baseURL(), argString(call.Argument(0)))
		return e.newWorker(url).obj
	}))
}

func (e *jsEnv) newWorker(url string) *jsWorker {
	w := &jsWorker{
		e:          e,
		url:        url,
		toWorker:   make(chan workerMsg, 64),
		fromWorker: make(chan any, 64),
		done:       make(chan struct{}),
		handlers:   map[string][]goja.Callable{},
		props:      map[string]goja.Value{},
	}
	o := e.vm.NewObject()
	w.obj = o
	_ = o.Set("url", url)
	for _, name := range []string{"onmessage", "onerror"} {
		name := name
		e.accessor(o, name, func(goja.FunctionCall) goja.Value {
			if v := w.props[name]; v != nil {
				return v
			}
			return goja.Null()
		}, func(call goja.FunctionCall) goja.Value {
			w.props[name] = call.Argument(0)
			return goja.Undefined()
		})
	}
	_ = o.Set("addEventListener", func(call goja.FunctionCall) goja.Value {
		typ := argString(call.Argument(0))
		if len(call.Arguments) > 1 {
			if fn, ok := goja.AssertFunction(call.Argument(1)); ok {
				w.handlers[typ] = append(w.handlers[typ], fn)
			}
		}
		return goja.Undefined()
	})
	_ = o.Set("removeEventListener", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("postMessage", func(call goja.FunctionCall) goja.Value {
		w.send(call.Argument(0).Export())
		return goja.Undefined()
	})
	_ = o.Set("terminate", func(goja.FunctionCall) goja.Value {
		w.stop()
		return goja.Undefined()
	})

	script := ""
	failed := false
	if resp, err := e.page.browser.fetch(url, nil, "script"); err != nil || resp.StatusCode >= 400 {
		if err == nil {
			err = fmt.Errorf("status %d", resp.StatusCode)
		}
		failed = true
		e.schedule(func() { w.fire("error", fmt.Sprintf("worker load failed: %v", err)) })
	} else {
		script = string(resp.Content)
	}
	if failed {
		w.stopped = true
	} else {
		go w.run(script)
	}
	e.workers = append(e.workers, w)
	return w
}

// run owns the worker's runtime on its own goroutine.
func (w *jsWorker) run(script string) {
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("js", true))
	vm.SetMaxCallStackSize(maxCallStackSize)
	vm.SetPromiseRejectionTracker(func(_ *goja.Promise, _ goja.PromiseRejectionOperation) {})

	self := vm.NewObject()
	post := func(call goja.FunctionCall) goja.Value {
		v := call.Argument(0).Export()
		select {
		case w.fromWorker <- v:
		default:
		}
		return goja.Undefined()
	}
	_ = vm.Set("postMessage", post)
	_ = vm.Set("self", self)
	_ = vm.Set("globalThis", self)
	_ = vm.Set("location", func() goja.Value {
		loc := vm.NewObject()
		_ = loc.Set("href", w.url)
		_ = loc.Set("toString", func(goja.FunctionCall) goja.Value { return vm.ToValue(w.url) })
		return loc
	}())
	_ = vm.Set("console", workerConsole(vm))
	_ = self.Set("postMessage", post)
	_ = self.Set("close", func(goja.FunctionCall) goja.Value {
		w.stop()
		return goja.Undefined()
	})

	// onmessage and addEventListener are stored on self and consulted when a
	// message arrives.
	var listeners []goja.Callable
	addListener := func(call goja.FunctionCall) goja.Value {
		if argString(call.Argument(0)) == "message" && len(call.Arguments) > 1 {
			if fn, ok := goja.AssertFunction(call.Argument(1)); ok {
				listeners = append(listeners, fn)
			}
		}
		return goja.Undefined()
	}
	_ = self.Set("addEventListener", addListener)
	_ = vm.Set("addEventListener", addListener)
	_ = vm.Set("importScripts", func(call goja.FunctionCall) goja.Value {
		for _, a := range call.Arguments {
			u := resolveURL(w.url, a.String())
			resp, err := w.e.page.browser.fetch(u, nil, "script")
			if err != nil {
				continue
			}
			_, _ = vm.RunString(string(resp.Content))
		}
		return goja.Undefined()
	})

	if _, err := vm.RunString(script); err != nil {
		msg := err.Error()
		w.e.schedule(func() { w.fire("error", msg) })
	}

	for {
		select {
		case <-w.done:
			return
		case m := <-w.toWorker:
			ev := vm.NewObject()
			_ = ev.Set("type", "message")
			_ = ev.Set("target", self)
			_ = ev.Set("data", vm.ToValue(m.value))
			callWorker(vm, self, listeners, "onmessage", ev)
		}
	}
}

// callWorker invokes the worker's onmessage property and its listeners.
func callWorker(vm *goja.Runtime, self *goja.Object, listeners []goja.Callable, prop string, ev *goja.Object) {
	if v := self.Get(prop); v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
		if fn, ok := goja.AssertFunction(v); ok {
			func() {
				defer func() { _ = recover() }()
				_, _ = fn(self, ev)
			}()
		}
	}
	for _, fn := range listeners {
		func() {
			defer func() { _ = recover() }()
			_, _ = fn(self, ev)
		}()
	}
}

func workerConsole(vm *goja.Runtime) *goja.Object {
	o := vm.NewObject()
	for _, level := range []string{"log", "info", "warn", "error", "debug"} {
		_ = o.Set(level, func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	}
	return o
}

// send queues a message for the worker.
func (w *jsWorker) send(v any) {
	select {
	case w.toWorker <- workerMsg{value: v}:
	default:
	}
}

// stop terminates the worker's goroutine.
func (w *jsWorker) stop() {
	if w.stopped {
		return
	}
	w.stopped = true
	close(w.done)
}

// fire delivers an error event to the main thread's handlers.
func (w *jsWorker) fire(typ, message string) {
	ev := w.e.vm.NewObject()
	_ = ev.Set("type", typ)
	_ = ev.Set("target", w.obj)
	if message != "" {
		_ = ev.Set("message", message)
	}
	w.deliver(typ, ev)
}

func (w *jsWorker) deliver(typ string, ev *goja.Object) {
	for _, h := range w.handlers[typ] {
		func() {
			defer func() { _ = recover() }()
			_, _ = h(w.obj, ev)
		}()
	}
	if v := w.props["on"+typ]; v != nil {
		if fn, ok := goja.AssertFunction(v); ok {
			func() {
				defer func() { _ = recover() }()
				_, _ = fn(w.obj, ev)
			}()
		}
	}
}

// drainWorkers delivers queued worker replies as message events, and reports
// whether any were delivered.
func (e *jsEnv) drainWorkers() bool {
	any := false
	for _, w := range e.workers {
		for {
			select {
			case v := <-w.fromWorker:
				any = true
				ev := e.vm.NewObject()
				_ = ev.Set("type", "message")
				_ = ev.Set("target", w.obj)
				_ = ev.Set("data", e.vm.ToValue(v))
				w.deliver("message", ev)
			default:
				goto next
			}
		}
	next:
	}
	return any
}

// hasLiveWorker reports whether a worker is still running, so the loader may
// wait briefly for its reply.
func (e *jsEnv) hasLiveWorker() bool {
	for _, w := range e.workers {
		if !w.stopped {
			return true
		}
	}
	return false
}
