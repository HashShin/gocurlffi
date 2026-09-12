package browser

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
	"golang.org/x/net/html"
)

// maxCallStackSize bounds JS recursion depth, matching what a browser engine
// allows closely enough while turning runaway recursion into an error.
const maxCallStackSize = 10000

// jsEnv is the per-page JavaScript environment: a goja VM plus the DOM
// bindings that let page scripts read and mutate the x/net/html tree.
type jsEnv struct {
	vm      *goja.Runtime
	page    *Page
	nodeSym *goja.Symbol
	nodes   map[*html.Node]*goja.Object

	nodeListeners map[*html.Node]map[string][]jsListener
	winListeners  map[string][]jsListener

	timers   []*jsTimer
	timerSeq int64

	protosRef *protos

	observers []*jsIntersectionObserver
}

type jsListener struct {
	fn       goja.Callable
	handleEv *goja.Object
	orig     goja.Value
}

type jsTimer struct {
	id      int64
	due     time.Time
	fn      goja.Callable
	args    []goja.Value
	repeat  time.Duration
	cleared bool
}

// prototypes
type protos struct {
	node     *goja.Object
	element  *goja.Object
	document *goja.Object
	text     *goja.Object
	comment  *goja.Object
	fragment *goja.Object
}

func newJSEnv(page *Page) *jsEnv {
	e := &jsEnv{
		vm:            goja.New(),
		page:          page,
		nodeSym:       goja.NewSymbol("node"),
		nodes:         map[*html.Node]*goja.Object{},
		nodeListeners: map[*html.Node]map[string][]jsListener{},
		winListeners:  map[string][]jsListener{},
	}
	e.vm.SetFieldNameMapper(goja.TagFieldNameMapper("js", true))
	// goja defaults to an effectively unlimited call stack, so runaway
	// recursion (a polyfill loop, for example) spins forever. Cap it like a
	// real engine so deep recursion throws a RangeError instead of hanging.
	e.vm.SetMaxCallStackSize(maxCallStackSize)
	e.setupPrototypes()
	e.setupGlobals()
	return e
}

// --- node wrapping ---

func (e *jsEnv) wrap(n *html.Node) goja.Value {
	if n == nil {
		return goja.Null()
	}
	if o, ok := e.nodes[n]; ok {
		return o
	}
	o := e.vm.NewObject()
	p := e.protosRef
	switch n.Type {
	case html.DocumentNode:
		_ = o.SetPrototype(p.document)
	case html.ElementNode:
		_ = o.SetPrototype(p.element)
	case html.TextNode:
		_ = o.SetPrototype(p.text)
	case html.CommentNode:
		_ = o.SetPrototype(p.comment)
	default:
		_ = o.SetPrototype(p.node)
	}
	_ = o.DefineDataPropertySymbol(e.nodeSym, e.vm.ToValue(n), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	e.nodes[n] = o
	return o
}

func urlParse(raw string) (*url.URL, error) { return url.Parse(raw) }

// thisNode returns the DOM node backing a JS `this`.
func (e *jsEnv) thisNode(call goja.FunctionCall) *html.Node {
	if o, ok := call.This.(*goja.Object); ok {
		v := o.GetSymbol(e.nodeSym)
		if v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
			if n, ok := v.Export().(*html.Node); ok {
				return n
			}
		}
	}
	return nil
}

// nodeArg converts a JS argument to a DOM node.
func (e *jsEnv) nodeArg(v goja.Value) *html.Node {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	if o, ok := v.(*goja.Object); ok {
		nv := o.GetSymbol(e.nodeSym)
		if nv != nil && !goja.IsUndefined(nv) && !goja.IsNull(nv) {
			if n, ok := nv.Export().(*html.Node); ok {
				return n
			}
		}
	}
	return nil
}

// argString returns a JS argument as a Go string.
func argString(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	return v.String()
}

// --- prototypes setup ---

func (e *jsEnv) setupPrototypes() {
	p := &protos{
		node:     e.vm.NewObject(),
		element:  e.vm.NewObject(),
		document: e.vm.NewObject(),
		text:     e.vm.NewObject(),
		comment:  e.vm.NewObject(),
		fragment: e.vm.NewObject(),
	}
	e.protosRef = p
	_ = p.element.SetPrototype(p.node)
	_ = p.document.SetPrototype(p.node)
	_ = p.text.SetPrototype(p.node)
	_ = p.comment.SetPrototype(p.node)
	// DocumentFragment implements ParentNode, so give it the element
	// prototype (querySelector, children, append, ...) rather than plain Node.
	_ = p.fragment.SetPrototype(p.element)
	e.defineNodeProto(p.node)
	e.defineElementProto(p.element)
	e.defineDocumentProto(p.document)
	e.defineTextProto(p.text)
}

func (e *jsEnv) method(proto *goja.Object, name string, f func(goja.FunctionCall) goja.Value) {
	_ = proto.Set(name, e.vm.ToValue(f))
}

func (e *jsEnv) accessor(proto *goja.Object, name string, get, set func(goja.FunctionCall) goja.Value) {
	var g, s goja.Value = goja.Undefined(), goja.Undefined()
	if get != nil {
		g = e.vm.ToValue(get)
	}
	if set != nil {
		s = e.vm.ToValue(set)
	}
	_ = proto.DefineAccessorProperty(name, g, s, goja.FLAG_TRUE, goja.FLAG_FALSE)
}

// --- globals ---

func (e *jsEnv) setupGlobals() {
	rt := e.vm
	global := rt.GlobalObject()

	_ = rt.Set("window", global)
	_ = rt.Set("self", global)
	_ = rt.Set("globalThis", global)
	_ = rt.Set("top", global)
	_ = rt.Set("parent", global)
	_ = rt.Set("document", e.wrap(e.page.doc))
	_ = rt.Set("navigator", e.navigatorObject())
	_ = rt.Set("location", e.locationObject())
	_ = rt.Set("console", e.consoleObject())
	_ = rt.Set("screen", e.screenObject())
	_ = rt.Set("history", e.historyObject())
	_ = rt.Set("localStorage", e.storageObject())
	_ = rt.Set("sessionStorage", e.storageObject())

	_ = rt.Set("isSecureContext", true)
	_ = rt.Set("origin", e.page.originString())
	_ = rt.Set("name", "")
	_ = rt.Set("status", "")
	_ = rt.Set("closed", false)
	_ = rt.Set("frameElement", goja.Null())
	_ = rt.Set("innerWidth", 1280)
	_ = rt.Set("innerHeight", 720)
	_ = rt.Set("outerWidth", 1280)
	_ = rt.Set("outerHeight", 720)
	_ = rt.Set("devicePixelRatio", 1)
	_ = rt.Set("scrollX", 0)
	_ = rt.Set("scrollY", 0)
	_ = rt.Set("pageXOffset", 0)
	_ = rt.Set("pageYOffset", 0)

	_ = rt.Set("setTimeout", func(call goja.FunctionCall) goja.Value {
		return e.addTimer(call, false)
	})
	_ = rt.Set("setInterval", func(call goja.FunctionCall) goja.Value {
		return e.addTimer(call, true)
	})
	_ = rt.Set("clearTimeout", func(call goja.FunctionCall) goja.Value {
		e.clearTimer(argString(call.Argument(0)))
		return goja.Undefined()
	})
	_ = rt.Set("clearInterval", func(call goja.FunctionCall) goja.Value {
		e.clearTimer(argString(call.Argument(0)))
		return goja.Undefined()
	})
	_ = rt.Set("requestAnimationFrame", func(call goja.FunctionCall) goja.Value {
		return e.addTimer(call, false)
	})
	_ = rt.Set("cancelAnimationFrame", func(call goja.FunctionCall) goja.Value {
		e.clearTimer(argString(call.Argument(0)))
		return goja.Undefined()
	})
	_ = rt.Set("queueMicrotask", func(call goja.FunctionCall) goja.Value {
		if fn, ok := goja.AssertFunction(call.Argument(0)); ok {
			_, _ = fn(goja.Undefined())
		}
		return goja.Undefined()
	})

	_ = rt.Set("addEventListener", func(call goja.FunctionCall) goja.Value {
		e.addWindowListener(argString(call.Argument(0)), call.Argument(1))
		return goja.Undefined()
	})
	_ = rt.Set("removeEventListener", func(call goja.FunctionCall) goja.Value {
		e.removeWindowListener(argString(call.Argument(0)), call.Argument(1))
		return goja.Undefined()
	})
	_ = rt.Set("dispatchEvent", func(call goja.FunctionCall) goja.Value {
		ev := e.normalizeEvent(call.Argument(0))
		e.dispatchWindow(argString(ev.Get("type")), ev)
		return e.vm.ToValue(true)
	})

	_ = rt.Set("postMessage", func(call goja.FunctionCall) goja.Value {
		ev := e.newEvent("message")
		_ = ev.Set("data", call.Argument(0))
		_ = ev.Set("origin", argString(call.Argument(1)))
		_ = ev.Set("source", global)
		e.dispatchWindow("message", ev)
		return goja.Undefined()
	})
	_ = rt.Set("getSelection", func(goja.FunctionCall) goja.Value {
		o := e.vm.NewObject()
		_ = o.Set("rangeCount", 0)
		_ = o.Set("toString", func(goja.FunctionCall) goja.Value { return e.vm.ToValue("") })
		_ = o.Set("getRangeAt", func(goja.FunctionCall) goja.Value { return goja.Null() })
		_ = o.Set("removeAllRanges", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
		_ = o.Set("addRange", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
		_ = o.Set("collapse", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
		_ = o.Set("selectAllChildren", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
		return o
	})
	_ = rt.Set("frames", global)
	_ = rt.Set("length", 0)

	_ = rt.Set("alert", func(call goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = rt.Set("confirm", func(call goja.FunctionCall) goja.Value { return e.vm.ToValue(true) })
	_ = rt.Set("prompt", func(call goja.FunctionCall) goja.Value { return goja.Null() })
	_ = rt.Set("scrollTo", func(call goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = rt.Set("scrollBy", func(call goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = rt.Set("focus", func(call goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = rt.Set("blur", func(call goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = rt.Set("matchMedia", func(call goja.FunctionCall) goja.Value {
		m := e.vm.NewObject()
		_ = m.Set("matches", false)
		_ = m.Set("media", argString(call.Argument(0)))
		_ = m.Set("addListener", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
		_ = m.Set("removeListener", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
		_ = m.Set("addEventListener", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
		_ = m.Set("removeEventListener", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
		_ = m.Set("dispatchEvent", func(goja.FunctionCall) goja.Value { return e.vm.ToValue(true) })
		return m
	})
	_ = rt.Set("getComputedStyle", func(call goja.FunctionCall) goja.Value {
		return e.newStyleObject(nil)
	})

	_ = rt.Set("atob", func(call goja.FunctionCall) goja.Value {
		s := argString(call.Argument(0))
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			b, _ = base64.RawStdEncoding.DecodeString(s)
		}
		return e.vm.ToValue(string(b))
	})
	_ = rt.Set("btoa", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue(base64.StdEncoding.EncodeToString([]byte(argString(call.Argument(0)))))
	})

	e.setupNetwork()
	e.setupWeb()
}

func (e *jsEnv) navigatorObject() *goja.Object {
	o := e.vm.NewObject()
	ua := e.page.userAgent
	_ = o.Set("userAgent", ua)
	_ = o.Set("appName", "Netscape")
	_ = o.Set("appVersion", ua)
	_ = o.Set("platform", e.page.platform)
	_ = o.Set("vendor", "")
	_ = o.Set("language", "en-US")
	_ = o.Set("languages", e.vm.ToValue([]string{"en-US", "en"}))
	_ = o.Set("cookieEnabled", true)
	_ = o.Set("onLine", true)
	_ = o.Set("hardwareConcurrency", 4)
	_ = o.Set("maxTouchPoints", 0)
	_ = o.Set("webdriver", false)
	uad := e.vm.NewObject()
	_ = uad.Set("mobile", strings.Contains(ua, "Mobile"))
	_ = uad.Set("platform", e.page.platform)
	_ = uad.Set("brands", e.vm.NewArray())
	_ = uad.Set("getHighEntropyValues", func(goja.FunctionCall) goja.Value {
		return e.resolvedPromise(e.vm.NewObject())
	})
	_ = o.Set("userAgentData", uad)
	return o
}

func (e *jsEnv) locationObject() *goja.Object {
	o := e.vm.NewObject()
	set := func(name, val string) { _ = o.Set(name, val) }
	href := e.page.URL
	set("href", href)
	// toString/valueOf must be callable so `location + ''` and friends work.
	_ = o.Set("toString", func(goja.FunctionCall) goja.Value { return e.vm.ToValue(href) })
	_ = o.Set("valueOf", func(goja.FunctionCall) goja.Value { return e.vm.ToValue(href) })
	_ = o.Set("toJSON", func(goja.FunctionCall) goja.Value { return e.vm.ToValue(href) })
	u := parseLocation(href)
	set("protocol", u.scheme)
	set("host", u.host)
	set("hostname", u.hostname)
	set("port", u.port)
	set("pathname", u.pathname)
	set("search", u.search)
	set("hash", u.hash)
	set("origin", u.origin)
	_ = o.Set("assign", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("replace", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("reload", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	return o
}

type locationParts struct {
	scheme, host, hostname, port, pathname, search, hash, origin string
}

func parseLocation(href string) locationParts {
	// Avoid net/url import cycle complaints by using it here.
	u, err := urlParse(href)
	if err != nil {
		return locationParts{pathname: href}
	}
	port := u.Port()
	host := u.Host
	return locationParts{
		scheme:   u.Scheme + ":",
		host:     host,
		hostname: u.Hostname(),
		port:     port,
		pathname: u.EscapedPath(),
		search:   "?" + u.RawQuery,
		hash:     "#" + u.Fragment,
		origin:   u.Scheme + "://" + host,
	}
}

func (e *jsEnv) consoleObject() *goja.Object {
	o := e.vm.NewObject()
	emit := func(level string) func(goja.FunctionCall) goja.Value {
		return func(call goja.FunctionCall) goja.Value {
			parts := make([]string, 0, len(call.Arguments))
			for _, a := range call.Arguments {
				parts = append(parts, formatJSValue(a))
			}
			e.page.log(level, strings.Join(parts, " "))
			return goja.Undefined()
		}
	}
	_ = o.Set("log", emit("log"))
	_ = o.Set("info", emit("info"))
	_ = o.Set("warn", emit("warn"))
	_ = o.Set("error", emit("error"))
	_ = o.Set("debug", emit("debug"))
	_ = o.Set("trace", emit("trace"))
	_ = o.Set("dir", emit("log"))
	_ = o.Set("assert", func(call goja.FunctionCall) goja.Value {
		if !call.Argument(0).ToBoolean() {
			e.page.log("error", "assertion failed: "+formatJSValue(call.Argument(1)))
		}
		return goja.Undefined()
	})
	_ = o.Set("time", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("timeEnd", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	return o
}

func (e *jsEnv) screenObject() *goja.Object {
	o := e.vm.NewObject()
	_ = o.Set("width", 1280)
	_ = o.Set("height", 720)
	_ = o.Set("availWidth", 1280)
	_ = o.Set("availHeight", 720)
	_ = o.Set("colorDepth", 24)
	_ = o.Set("pixelDepth", 24)
	return o
}

func (e *jsEnv) historyObject() *goja.Object {
	o := e.vm.NewObject()
	_ = o.Set("length", 1)
	_ = o.Set("pushState", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("replaceState", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("back", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("forward", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("go", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	return o
}

func (e *jsEnv) storageObject() *goja.Object {
	o := e.vm.NewObject()
	data := map[string]string{}
	_ = o.Set("getItem", func(call goja.FunctionCall) goja.Value {
		if v, ok := data[argString(call.Argument(0))]; ok {
			return e.vm.ToValue(v)
		}
		return goja.Null()
	})
	_ = o.Set("setItem", func(call goja.FunctionCall) goja.Value {
		data[argString(call.Argument(0))] = argString(call.Argument(1))
		return goja.Undefined()
	})
	_ = o.Set("removeItem", func(call goja.FunctionCall) goja.Value {
		delete(data, argString(call.Argument(0)))
		return goja.Undefined()
	})
	_ = o.Set("clear", func(call goja.FunctionCall) goja.Value {
		data = map[string]string{}
		return goja.Undefined()
	})
	_ = o.Set("key", func(call goja.FunctionCall) goja.Value { return goja.Null() })
	_ = o.Set("length", 0)
	return o
}

// --- timers ---

func (e *jsEnv) addTimer(call goja.FunctionCall, repeat bool) goja.Value {
	fn, ok := goja.AssertFunction(call.Argument(0))
	if !ok {
		return e.vm.ToValue(e.timerSeq)
	}
	e.timerSeq++
	id := e.timerSeq
	delayMs := 0
	if v := call.Argument(1); v != nil && !goja.IsUndefined(v) {
		delayMs = int(v.ToInteger())
	}
	d := time.Duration(delayMs) * time.Millisecond
	if d < 0 {
		d = 0
	}
	var extra []goja.Value
	if len(call.Arguments) > 2 {
		extra = call.Arguments[2:]
	}
	t := &jsTimer{id: id, due: time.Now().Add(d), fn: fn, args: extra}
	if repeat {
		t.repeat = d
	}
	e.timers = append(e.timers, t)
	return e.vm.ToValue(id)
}

func (e *jsEnv) clearTimer(id string) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return
	}
	for _, t := range e.timers {
		if t.id == n {
			t.cleared = true
		}
	}
}

// defaultTimerBudget bounds how long runTimers waits for future timers, so a
// page that schedules a long setTimeout does not stall the load.
const defaultTimerBudget = 2 * time.Second

// runTimers executes pending timers, waiting for ones that are due within the
// budget, up to maxRounds callbacks.
func (e *jsEnv) runTimers(maxRounds int) {
	deadline := time.Now().Add(e.page.timerWait())
	for round := 0; round < maxRounds; round++ {
		var due *jsTimer
		for _, t := range e.timers {
			if t.cleared {
				continue
			}
			if due == nil || t.due.Before(due.due) {
				due = t
			}
		}
		if due == nil {
			return
		}
		if wait := time.Until(due.due); wait > 0 {
			if due.due.After(deadline) {
				return
			}
			time.Sleep(wait)
		}
		e.invokeTimer(due)
	}
}

func (e *jsEnv) invokeTimer(t *jsTimer) {
	defer func() { _ = recover() }()
	_, _ = t.fn(goja.Undefined(), t.args...)
	if t.repeat > 0 {
		t.due = time.Now().Add(t.repeat)
	} else {
		t.cleared = true
	}
}

// --- events ---

func (e *jsEnv) addWindowListener(typ string, v goja.Value) {
	if l, ok := e.makeListener(v); ok {
		e.winListeners[typ] = append(e.winListeners[typ], l)
	}
}

func (e *jsEnv) removeWindowListener(typ string, v goja.Value) {
	ls := e.winListeners[typ]
	for i, l := range ls {
		if e.sameListener(l, v) {
			e.winListeners[typ] = append(ls[:i], ls[i+1:]...)
			return
		}
	}
}

func (e *jsEnv) addNodeListener(n *html.Node, typ string, v goja.Value) {
	l, ok := e.makeListener(v)
	if !ok {
		return
	}
	m := e.nodeListeners[n]
	if m == nil {
		m = map[string][]jsListener{}
		e.nodeListeners[n] = m
	}
	m[typ] = append(m[typ], l)
}

func (e *jsEnv) removeNodeListener(n *html.Node, typ string, v goja.Value) {
	m := e.nodeListeners[n]
	if m == nil {
		return
	}
	ls := m[typ]
	for i, l := range ls {
		if e.sameListener(l, v) {
			m[typ] = append(ls[:i], ls[i+1:]...)
			return
		}
	}
}

func (e *jsEnv) makeListener(v goja.Value) (jsListener, bool) {
	if fn, ok := goja.AssertFunction(v); ok {
		return jsListener{fn: fn, orig: v}, true
	}
	if o, ok := v.(*goja.Object); ok {
		if hv := o.Get("handleEvent"); hv != nil {
			if fn, ok := goja.AssertFunction(hv); ok {
				return jsListener{handleEv: o, fn: fn, orig: v}, true
			}
		}
	}
	return jsListener{}, false
}

func (e *jsEnv) sameListener(l jsListener, v goja.Value) bool {
	if l.orig == nil || v == nil {
		return false
	}
	return l.orig.StrictEquals(v)
}

func (e *jsEnv) dispatchWindow(typ string, ev *goja.Object) {
	typ = strings.ToLower(typ)
	if ev != nil {
		_ = ev.Set("target", e.vm.GlobalObject())
		_ = ev.Set("currentTarget", e.vm.GlobalObject())
	}
	for _, l := range e.winListeners[typ] {
		e.callListener(l, e.vm.GlobalObject(), ev)
	}
}

func (e *jsEnv) dispatchNode(n *html.Node, typ string, ev *goja.Object) {
	typ = strings.ToLower(typ)
	if ev != nil {
		_ = ev.Set("target", e.wrap(n))
		_ = ev.Set("currentTarget", e.wrap(n))
	}
	// bubbling
	for cur := n; cur != nil; cur = cur.Parent {
		if m := e.nodeListeners[cur]; m != nil {
			for _, l := range m[typ] {
				e.callListener(l, e.wrap(cur), ev)
			}
		}
	}
}

func (e *jsEnv) callListener(l jsListener, this goja.Value, ev *goja.Object) {
	defer func() { _ = recover() }()
	arg := goja.Value(goja.Undefined())
	if ev != nil {
		arg = ev
	}
	if l.handleEv != nil {
		_, _ = l.fn(l.handleEv, arg)
		return
	}
	_, _ = l.fn(this, arg)
}

func (e *jsEnv) normalizeEvent(v goja.Value) *goja.Object {
	if o, ok := v.(*goja.Object); ok {
		if t := o.Get("type"); t != nil && !goja.IsUndefined(t) {
			return o
		}
	}
	o := e.vm.NewObject()
	typ := "event"
	if v != nil && !goja.IsUndefined(v) {
		typ = argString(v)
	}
	_ = o.Set("type", typ)
	return o
}

// fireDOMContentLoaded and fireLoad dispatch the standard lifecycle events.
func (e *jsEnv) fireDOMContentLoaded() {
	e.dispatchWindow("DOMContentLoaded", e.newEvent("DOMContentLoaded"))
	if doc := e.page.docNode(); doc != nil {
		e.dispatchNode(doc, "DOMContentLoaded", e.newEvent("DOMContentLoaded"))
	}
}

func (e *jsEnv) fireLoad() {
	e.dispatchWindow("load", e.newEvent("load"))
	if doc := e.page.docNode(); doc != nil {
		e.dispatchNode(doc, "load", e.newEvent("load"))
	}
}

func (e *jsEnv) newEvent(typ string) *goja.Object {
	o := e.vm.NewObject()
	_ = o.Set("type", typ)
	_ = o.Set("bubbles", true)
	_ = o.Set("cancelable", true)
	_ = o.Set("defaultPrevented", false)
	_ = o.Set("preventDefault", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("stopPropagation", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	return o
}

// --- script execution ---

// runScript evaluates JS source, returning any error. The filename is used in
// compile and runtime error messages. A panic inside a native binding is
// converted into an error so a broken page script cannot crash the process.
func (e *jsEnv) runScript(src, filename string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in %s: %v", filename, r)
		}
	}()
	prog, err := goja.Compile(filename, src, false)
	e.page.debugf("compile %s: err=%v", filename, err != nil)
	if err != nil {
		// Some bundlers ship classic scripts using top-level await, which is
		// only valid inside a module (and which the parser reports as a
		// generic error). Retry wrapped in an async IIFE. Only accept the
		// wrapped form if it actually parses, so genuine syntax errors keep
		// their original message.
		wrapped := "(async function(){\n" + src + "\n}).call(globalThis).catch(function(e){" +
			"if (typeof console !== 'undefined' && console.error) console.error(String(e));" +
			"});"
		if p2, err2 := goja.Compile(filename, wrapped, false); err2 == nil {
			prog = p2
			err = nil
			e.page.debugf("using async-wrap for %s", filename)
		}
	}
	if err != nil {
		return err
	}
	e.page.debugf("run %s", filename)
	timeout := e.page.maxScriptTime()
	if timeout <= 0 {
		_, err = e.vm.RunProgram(prog)
		return err
	}
	timer := time.AfterFunc(timeout, func() {
		if e.page.browser.opts.Debug {
			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			fmt.Fprintf(os.Stderr, "[browser] script timeout; goroutine dump:\n%s\n", buf[:n])
		}
		e.vm.Interrupt("script timeout")
	})
	defer timer.Stop()
	_, err = e.vm.RunProgram(prog)
	return err
}

func formatJSValue(v goja.Value) string {
	if v == nil {
		return "undefined"
	}
	switch {
	case goja.IsUndefined(v):
		return "undefined"
	case goja.IsNull(v):
		return "null"
	}
	if s, ok := v.Export().(string); ok {
		return s
	}
	if o, ok := v.(*goja.Object); ok {
		if s := o.ClassName(); s == "Error" {
			if m := o.Get("message"); m != nil {
				return "Error: " + m.String()
			}
		}
	}
	return v.String()
}
