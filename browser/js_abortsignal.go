package browser

import (
	"errors"
	"time"

	"github.com/dop251/goja"
)

// AbortSignal. The previous version set a boolean on its own object and told
// nobody: abort() left every listener silent, and the signal handed to a Request
// was a different object from the controller's, so `r.signal.aborted` stayed
// false after `c.abort()`. Here a controller owns exactly one signal, and abort
// fires the event, updates the flags and propagates to derived signals.

// abortListener keeps the original value as well as the callable, because two
// goja.Callable values cannot be compared and removeEventListener has to match
// the function it was given.
type abortListener struct {
	fn   goja.Callable
	orig goja.Value
}

// jsAbortSignal is one signal.
type jsAbortSignal struct {
	e          *jsEnv
	obj        *goja.Object
	aborted    bool
	reason     goja.Value
	listeners  []abortListener
	dependents []*jsAbortSignal
}

// abortError builds the DOMException-shaped error an abort carries.
func (e *jsEnv) abortError(message string) goja.Value {
	o := e.vm.NewGoError(errors.New(message))
	_ = o.Set("name", "AbortError")
	return o
}

func (e *jsEnv) newAbortSignalState() *jsAbortSignal {
	o := e.vm.NewObject()
	e.tagObject(o, "AbortSignal")
	s := &jsAbortSignal{e: e, obj: o, reason: goja.Undefined()}
	// The state rides on the object as a non-enumerable private property, the
	// same way Blob, Headers and Response carry theirs.
	e.mark(o, abortMark, s)
	_ = o.Set("aborted", false)
	_ = o.Set("reason", goja.Undefined())
	_ = o.Set("onabort", goja.Null())

	_ = o.Set("addEventListener", func(call goja.FunctionCall) goja.Value {
		if argString(call.Argument(0)) != "abort" {
			return goja.Undefined()
		}
		fn, ok := goja.AssertFunction(call.Argument(1))
		if !ok {
			return goja.Undefined()
		}
		if s.aborted {
			// A listener added after the fact still runs, as in the spec.
			_, _ = fn(goja.Undefined(), s.event())
			return goja.Undefined()
		}
		s.listeners = append(s.listeners, abortListener{fn: fn, orig: call.Argument(1)})
		return goja.Undefined()
	})
	_ = o.Set("removeEventListener", func(call goja.FunctionCall) goja.Value {
		target := call.Argument(1)
		for i, l := range s.listeners {
			if l.orig != nil && target != nil && l.orig.SameAs(target) {
				s.listeners = append(s.listeners[:i], s.listeners[i+1:]...)
				break
			}
		}
		return goja.Undefined()
	})
	_ = o.Set("throwIfAborted", func(goja.FunctionCall) goja.Value {
		if s.aborted {
			panic(s.reason)
		}
		return goja.Undefined()
	})
	_ = o.Set("dispatchEvent", func(goja.FunctionCall) goja.Value { return e.vm.ToValue(true) })
	return s
}

// event builds the abort event handed to listeners.
func (s *jsAbortSignal) event() *goja.Object {
	ev := s.e.newEvent("abort")
	_ = ev.Set("target", s.obj)
	_ = ev.Set("currentTarget", s.obj)
	return ev
}

// abort fires the signal. It is idempotent, as the spec requires.
func (s *jsAbortSignal) abort(reason goja.Value) {
	if s.aborted {
		return
	}
	s.aborted = true
	if reason == nil || goja.IsUndefined(reason) || goja.IsNull(reason) {
		reason = s.e.abortError("signal is aborted without reason")
	}
	s.reason = reason
	_ = s.obj.Set("aborted", true)
	_ = s.obj.Set("reason", reason)

	ev := s.event()
	// onabort is read now rather than captured, because a page assigns it after
	// it gets the signal.
	if h := s.obj.Get("onabort"); h != nil && !goja.IsUndefined(h) && !goja.IsNull(h) {
		if fn, ok := goja.AssertFunction(h); ok {
			_, _ = fn(s.obj, ev)
		}
	}
	listeners := s.listeners
	s.listeners = nil
	for _, l := range listeners {
		_, _ = l.fn(goja.Undefined(), ev)
	}
	for _, d := range s.dependents {
		d.abort(reason)
	}
}

func (e *jsEnv) newAbortController() *goja.Object {
	o := e.vm.NewObject()
	e.tagObject(o, "AbortController")
	s := e.newAbortSignalState()
	_ = o.Set("signal", s.obj)
	_ = o.Set("abort", func(call goja.FunctionCall) goja.Value {
		s.abort(call.Argument(0))
		return goja.Undefined()
	})
	return o
}

// signalOf returns the jsAbortSignal behind a value, if it is one, so fetch and
// Request can read a signal without the state being visible to page script.
func signalOf(v goja.Value) *jsAbortSignal {
	o, ok := v.(*goja.Object)
	if !ok {
		return nil
	}
	s, _ := marked[*jsAbortSignal](o, abortMark)
	return s
}

// installAbortStatics adds the AbortSignal constructors that do not need a
// controller.
func (e *jsEnv) installAbortStatics() {
	ctor, ok := e.vm.Get("AbortSignal").(*goja.Object)
	if !ok || ctor == nil {
		return
	}
	_ = ctor.Set("abort", func(call goja.FunctionCall) goja.Value {
		s := e.newAbortSignalState()
		s.abort(call.Argument(0))
		return e.vm.ToValue(s.obj)
	})
	_ = ctor.Set("timeout", func(call goja.FunctionCall) goja.Value {
		s := e.newAbortSignalState()
		ms := call.Argument(0).ToInteger()
		if ms < 0 {
			ms = 0
		}
		// A real timer on the page's queue, so it fires during runTimers rather
		// than racing the interpreter from another goroutine.
		e.scheduleDelay(time.Duration(ms)*time.Millisecond, func() {
			s.abort(e.abortError("signal timed out"))
		})
		return e.vm.ToValue(s.obj)
	})
	_ = ctor.Set("any", func(call goja.FunctionCall) goja.Value {
		out := e.newAbortSignalState()
		arr, ok := call.Argument(0).(*goja.Object)
		if !ok {
			panic(e.vm.NewTypeError("AbortSignal.any: argument is not iterable"))
		}
		n := int(arr.Get("length").ToInteger())
		for i := 0; i < n; i++ {
			src := signalOf(arr.Get(intToString(i)))
			if src == nil {
				continue
			}
			if src.aborted {
				out.abort(src.reason)
				return e.vm.ToValue(out.obj)
			}
			src.dependents = append(src.dependents, out)
		}
		return e.vm.ToValue(out.obj)
	})
}
