package browser

import (
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dop251/goja"
	"golang.org/x/net/html"
)

// setupWeb installs the web-platform globals that page bundles expect:
// URL, URLSearchParams, TextEncoder/Decoder, AbortController, observers,
// Event constructors, performance, crypto and structuredClone.
func (e *jsEnv) setupWeb() {
	rt := e.vm
	_ = rt.Set("URL", func(call goja.ConstructorCall) *goja.Object {
		return e.newURLObject(call.Argument(0).String(), argString(call.Argument(1)))
	})
	_ = rt.Set("URLSearchParams", func(call goja.ConstructorCall) *goja.Object {
		return e.newURLSearchParams(call.Argument(0))
	})
	_ = rt.Set("TextEncoder", func(call goja.ConstructorCall) *goja.Object {
		return e.newTextEncoder()
	})
	_ = rt.Set("TextDecoder", func(call goja.ConstructorCall) *goja.Object {
		return e.newTextDecoder()
	})
	_ = rt.Set("AbortController", func(call goja.ConstructorCall) *goja.Object {
		return e.newAbortController()
	})
	_ = rt.Set("AbortSignal", func(call goja.ConstructorCall) *goja.Object {
		return e.newAbortSignal()
	})
	_ = rt.Set("Event", func(call goja.ConstructorCall) *goja.Object {
		return e.newEventCtor(argString(call.Argument(0)), call.Argument(1))
	})
	_ = rt.Set("CustomEvent", func(call goja.ConstructorCall) *goja.Object {
		return e.newEventCtor(argString(call.Argument(0)), call.Argument(1))
	})
	_ = rt.Set("MutationObserver", func(call goja.ConstructorCall) *goja.Object {
		return e.newObserver(call.Argument(0))
	})
	_ = rt.Set("IntersectionObserver", func(call goja.ConstructorCall) *goja.Object {
		return e.newIntersectionObserver(call.Argument(0))
	})
	_ = rt.Set("ResizeObserver", func(call goja.ConstructorCall) *goja.Object {
		return e.newObserver(call.Argument(0))
	})
	_ = rt.Set("performance", e.performanceObject())
	_ = rt.Set("crypto", e.cryptoObject())
	_ = rt.Set("queueMicrotask", func(call goja.FunctionCall) goja.Value {
		if fn, ok := goja.AssertFunction(call.Argument(0)); ok {
			_, _ = fn(goja.Undefined())
		}
		return goja.Undefined()
	})
	_ = rt.Set("structuredClone", func(call goja.FunctionCall) goja.Value {
		return e.structuredClone(call.Argument(0))
	})
	_ = rt.Set("DOMParser", func(call goja.ConstructorCall) *goja.Object {
		return e.newDOMParser()
	})
	_ = rt.Set("requestIdleCallback", func(call goja.FunctionCall) goja.Value {
		return e.addIdleCallback(call)
	})
	_ = rt.Set("cancelIdleCallback", func(call goja.FunctionCall) goja.Value {
		e.clearTimer(argString(call.Argument(0)))
		return goja.Undefined()
	})
	_ = rt.Set("customElements", e.customElementsObject())
	_ = rt.Set("NodeFilter", e.newNodeFilter())
	_ = rt.Set("XPathResult", e.newXPathResultCtor())
	e.installIndexedDB(rt)
	e.installWebSocket(rt)
	e.installWorker(rt)
}

// newDOMParser implements new DOMParser().parseFromString(html, type).
func (e *jsEnv) newDOMParser() *goja.Object {
	o := e.vm.NewObject()
	_ = o.Set("parseFromString", func(call goja.FunctionCall) goja.Value {
		doc, err := html.Parse(strings.NewReader(argString(call.Argument(0))))
		if err != nil {
			panic(e.vm.NewGoError(err))
		}
		for t, frag := range extractTemplateContents(doc) {
			e.page.templateContent[t] = frag
		}
		return e.wrap(doc)
	})
	return o
}

// newFragmentFromHTML parses HTML into a document fragment with the fragment
// prototype, using the body as the parsing context.
func (e *jsEnv) newFragmentFromHTML(htmlStr string) goja.Value {
	ctx := findElement(e.page.doc, "body")
	if ctx == nil {
		ctx = e.page.doc
	}
	frag := &html.Node{Type: html.ElementNode, Data: "#document-fragment"}
	nodes, err := html.ParseFragment(strings.NewReader(htmlStr), ctx)
	if err == nil {
		for _, c := range nodes {
			c.Parent = nil
			appendChild(frag, c)
		}
	}
	v := e.wrap(frag)
	if obj, ok := v.(*goja.Object); ok {
		_ = obj.SetPrototype(e.protosRef.fragment)
	}
	return v
}

// newImplementation implements document.implementation. Frameworks such as
// jQuery use createHTMLDocument to parse HTML fragments.
func (e *jsEnv) newImplementation() *goja.Object {
	o := e.vm.NewObject()
	_ = o.Set("createHTMLDocument", func(call goja.FunctionCall) goja.Value {
		return e.wrap(e.newHTMLDocument(argString(call.Argument(0))))
	})
	_ = o.Set("createDocument", func(call goja.FunctionCall) goja.Value {
		return e.wrap(e.newHTMLDocument(""))
	})
	_ = o.Set("createDocumentType", func(call goja.FunctionCall) goja.Value {
		return e.wrap(&html.Node{Type: html.DoctypeNode, Data: argString(call.Argument(0))})
	})
	_ = o.Set("hasFeature", func(goja.FunctionCall) goja.Value { return e.vm.ToValue(true) })
	return o
}

// newHTMLDocument builds a minimal html/head/body document tree.
func (e *jsEnv) newHTMLDocument(title string) *html.Node {
	doc := &html.Node{Type: html.DocumentNode}
	root := createElement("html")
	head := createElement("head")
	body := createElement("body")
	appendChild(doc, root)
	appendChild(root, head)
	appendChild(root, body)
	if title != "" {
		t := createElement("title")
		setTextContent(t, title)
		appendChild(head, t)
	}
	for t, frag := range extractTemplateContents(doc) {
		e.page.templateContent[t] = frag
	}
	return doc
}

// newRange implements the parts of DOM Range that libraries use.
func (e *jsEnv) newRange() *goja.Object {
	o := e.vm.NewObject()
	contextual := func(htmlStr string) goja.Value {
		return e.newFragmentFromHTML(htmlStr)
	}
	_ = o.Set("setStart", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("setEnd", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("setStartBefore", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("setStartAfter", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("setEndBefore", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("setEndAfter", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("selectNode", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("selectNodeContents", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("collapse", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("detach", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("deleteContents", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("insertNode", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("surroundContents", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("cloneContents", func(goja.FunctionCall) goja.Value { return e.newDocumentFragment() })
	_ = o.Set("extractContents", func(goja.FunctionCall) goja.Value { return e.newDocumentFragment() })
	_ = o.Set("createContextualFragment", func(call goja.FunctionCall) goja.Value {
		return contextual(argString(call.Argument(0)))
	})
	_ = o.Set("toString", func(goja.FunctionCall) goja.Value { return e.vm.ToValue("") })
	_ = o.Set("collapsed", true)
	_ = o.Set("commonAncestorContainer", e.wrap(findElement(e.page.doc, "body")))
	rect := func(goja.FunctionCall) goja.Value {
		r := e.vm.NewObject()
		for _, k := range []string{"x", "y", "top", "left", "right", "bottom", "width", "height"} {
			_ = r.Set(k, 0)
		}
		return r
	}
	_ = o.Set("getBoundingClientRect", rect)
	_ = o.Set("getClientRects", func(goja.FunctionCall) goja.Value { return e.vm.NewArray() })
	return o
}

// addIdleCallback schedules cb as a timer with an IdleDeadline argument.
func (e *jsEnv) addIdleCallback(call goja.FunctionCall) goja.Value {
	fn, ok := goja.AssertFunction(call.Argument(0))
	if !ok {
		return e.vm.ToValue(0)
	}
	e.timerSeq++
	id := e.timerSeq
	deadline := e.vm.NewObject()
	_ = deadline.Set("didTimeout", false)
	_ = deadline.Set("timeRemaining", func(goja.FunctionCall) goja.Value { return e.vm.ToValue(50) })
	wrapper, _ := goja.AssertFunction(e.vm.ToValue(func(goja.FunctionCall) goja.Value {
		_, _ = fn(goja.Undefined(), deadline)
		return goja.Undefined()
	}))
	e.timers = append(e.timers, &jsTimer{id: id, due: time.Now(), fn: wrapper})
	return e.vm.ToValue(id)
}

// customElementsObject is a registry stub; enough for registrations to succeed.
func (e *jsEnv) customElementsObject() *goja.Object {
	o := e.vm.NewObject()
	registry := map[string]goja.Value{}
	_ = o.Set("define", func(call goja.FunctionCall) goja.Value {
		name := argString(call.Argument(0))
		if _, exists := registry[name]; exists {
			panic(e.vm.NewTypeError("custom element already defined: " + name))
		}
		registry[name] = call.Argument(1)
		return goja.Undefined()
	})
	_ = o.Set("get", func(call goja.FunctionCall) goja.Value {
		if v, ok := registry[argString(call.Argument(0))]; ok {
			return v
		}
		return goja.Undefined()
	})
	_ = o.Set("whenDefined", func(goja.FunctionCall) goja.Value {
		return e.resolvedPromise(goja.Undefined())
	})
	_ = o.Set("upgrade", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	return o
}

// --- URL ---

func (e *jsEnv) newURLObject(raw, base string) *goja.Object {
	u, err := url.Parse(raw)
	if err != nil {
		panic(e.vm.NewTypeError("Invalid URL: " + raw))
	}
	if base != "" {
		b, berr := url.Parse(base)
		if berr != nil {
			panic(e.vm.NewTypeError("Invalid base URL: " + base))
		}
		u = b.ResolveReference(u)
	}
	o := e.vm.NewObject()
	host := u.Host
	_ = o.Set("href", u.String())
	_ = o.Set("protocol", u.Scheme+":")
	_ = o.Set("host", host)
	_ = o.Set("hostname", u.Hostname())
	_ = o.Set("port", u.Port())
	_ = o.Set("pathname", u.EscapedPath())
	search := u.RawQuery
	if search != "" {
		search = "?" + search
	}
	_ = o.Set("search", search)
	hash := ""
	if u.Fragment != "" {
		hash = "#" + u.Fragment
	}
	_ = o.Set("hash", hash)
	_ = o.Set("username", u.User.Username())
	_ = o.Set("origin", u.Scheme+"://"+host)
	_ = o.Set("searchParams", e.newURLSearchParams(e.vm.ToValue(u.RawQuery)))
	_ = o.Set("toString", func(goja.FunctionCall) goja.Value { return e.vm.ToValue(u.String()) })
	_ = o.Set("toJSON", func(goja.FunctionCall) goja.Value { return e.vm.ToValue(u.String()) })
	return o
}

func (e *jsEnv) newURLSearchParams(arg goja.Value) *goja.Object {
	values := url.Values{}
	switch v := arg.(type) {
	case nil:
	case *goja.Object:
		if len(v.Keys()) > 0 {
			for _, k := range v.Keys() {
				values.Set(k, v.Get(k).String())
			}
		} else if s := argString(arg); s != "" {
			values, _ = url.ParseQuery(strings.TrimPrefix(s, "?"))
		}
	default:
		values, _ = url.ParseQuery(strings.TrimPrefix(argString(arg), "?"))
	}
	o := e.vm.NewObject()
	_ = o.Set("get", func(call goja.FunctionCall) goja.Value {
		k := argString(call.Argument(0))
		if vs, ok := values[k]; ok && len(vs) > 0 {
			return e.vm.ToValue(vs[0])
		}
		return goja.Null()
	})
	_ = o.Set("getAll", func(call goja.FunctionCall) goja.Value {
		k := argString(call.Argument(0))
		items := make([]interface{}, 0, len(values[k]))
		for _, s := range values[k] {
			items = append(items, s)
		}
		return e.vm.NewArray(items...)
	})
	_ = o.Set("has", func(call goja.FunctionCall) goja.Value {
		_, ok := values[argString(call.Argument(0))]
		return e.vm.ToValue(ok)
	})
	_ = o.Set("set", func(call goja.FunctionCall) goja.Value {
		values.Set(argString(call.Argument(0)), argString(call.Argument(1)))
		return goja.Undefined()
	})
	_ = o.Set("append", func(call goja.FunctionCall) goja.Value {
		values.Add(argString(call.Argument(0)), argString(call.Argument(1)))
		return goja.Undefined()
	})
	_ = o.Set("delete", func(call goja.FunctionCall) goja.Value {
		values.Del(argString(call.Argument(0)))
		return goja.Undefined()
	})
	_ = o.Set("forEach", func(call goja.FunctionCall) goja.Value {
		if fn, ok := goja.AssertFunction(call.Argument(0)); ok {
			for k, vs := range values {
				for _, s := range vs {
					_, _ = fn(goja.Undefined(), e.vm.ToValue(s), e.vm.ToValue(k))
				}
			}
		}
		return goja.Undefined()
	})
	_ = o.Set("keys", func(goja.FunctionCall) goja.Value {
		out := make([]interface{}, 0, len(values))
		for k := range values {
			out = append(out, k)
		}
		return e.vm.NewArray(out...)
	})
	_ = o.Set("values", func(goja.FunctionCall) goja.Value {
		out := make([]interface{}, 0, len(values))
		for _, vs := range values {
			for _, s := range vs {
				out = append(out, s)
			}
		}
		return e.vm.NewArray(out...)
	})
	_ = o.Set("toString", func(goja.FunctionCall) goja.Value {
		return e.vm.ToValue(values.Encode())
	})
	return o
}

// --- text encoding ---

func (e *jsEnv) newTextEncoder() *goja.Object {
	o := e.vm.NewObject()
	_ = o.Set("encoding", "utf-8")
	_ = o.Set("encode", func(call goja.FunctionCall) goja.Value {
		b := []byte(argString(call.Argument(0)))
		out := e.vm.NewArray()
		for i, c := range b {
			_ = out.Set(strconv.Itoa(i), int(c))
		}
		_ = out.Set("length", len(b))
		return out
	})
	_ = o.Set("encodeInto", func(call goja.FunctionCall) goja.Value {
		r := e.vm.NewObject()
		_ = r.Set("read", utf8.RuneCountInString(argString(call.Argument(0))))
		_ = r.Set("written", len(argString(call.Argument(0))))
		return r
	})
	return o
}

func (e *jsEnv) newTextDecoder() *goja.Object {
	o := e.vm.NewObject()
	_ = o.Set("encoding", "utf-8")
	_ = o.Set("fatal", false)
	_ = o.Set("ignoreBOM", false)
	_ = o.Set("decode", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue(toBytesString(call.Argument(0)))
	})
	return o
}

// toBytesString converts a JS buffer/array/string argument to a Go string.
func toBytesString(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	if s, ok := v.Export().(string); ok {
		return s
	}
	if b, ok := v.Export().([]byte); ok {
		return string(b)
	}
	if o, ok := v.(*goja.Object); ok {
		if lv := o.Get("length"); lv != nil {
			n := int(lv.ToInteger())
			buf := make([]byte, 0, n)
			for i := 0; i < n; i++ {
				buf = append(buf, byte(o.Get(strconv.Itoa(i)).ToInteger()))
			}
			return string(buf)
		}
	}
	return v.String()
}

// --- AbortController / AbortSignal ---

func (e *jsEnv) newAbortSignal() *goja.Object {
	o := e.vm.NewObject()
	_ = o.Set("aborted", false)
	_ = o.Set("reason", goja.Undefined())
	_ = o.Set("addEventListener", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("removeEventListener", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("throwIfAborted", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("dispatchEvent", func(goja.FunctionCall) goja.Value { return e.vm.ToValue(true) })
	return o
}

func (e *jsEnv) newAbortController() *goja.Object {
	o := e.vm.NewObject()
	signal := e.newAbortSignal()
	_ = o.Set("signal", signal)
	_ = o.Set("abort", func(call goja.FunctionCall) goja.Value {
		_ = signal.Set("aborted", true)
		if r := call.Argument(0); r != nil && !goja.IsUndefined(r) {
			_ = signal.Set("reason", r)
		}
		return goja.Undefined()
	})
	return o
}

// --- Event constructors ---

func (e *jsEnv) newEventCtor(typ string, init goja.Value) *goja.Object {
	o := e.newEvent(typ)
	if io, ok := init.(*goja.Object); ok {
		if d := io.Get("detail"); d != nil && !goja.IsUndefined(d) {
			_ = o.Set("detail", d)
		}
		if b := io.Get("bubbles"); b != nil && !goja.IsUndefined(b) {
			_ = o.Set("bubbles", b.ToBoolean())
		}
		if c := io.Get("cancelable"); c != nil && !goja.IsUndefined(c) {
			_ = o.Set("cancelable", c.ToBoolean())
		}
	}
	return o
}

// --- observers ---

// jsIntersectionObserver records observed targets and reports them once as
// intersecting after load, which is what reveals lazy-loaded content in a
// headless context (there is no viewport to scroll).
type jsIntersectionObserver struct {
	cb      goja.Callable
	obj     *goja.Object
	targets []*html.Node
}

func (e *jsEnv) newIntersectionObserver(cbValue goja.Value) *goja.Object {
	o := e.vm.NewObject()
	obs := &jsIntersectionObserver{obj: o}
	if fn, ok := goja.AssertFunction(cbValue); ok {
		obs.cb = fn
	}
	e.observers = append(e.observers, obs)

	_ = o.Set("observe", func(call goja.FunctionCall) goja.Value {
		if n := e.nodeArg(call.Argument(0)); n != nil {
			obs.targets = append(obs.targets, n)
		}
		return goja.Undefined()
	})
	_ = o.Set("unobserve", func(call goja.FunctionCall) goja.Value {
		n := e.nodeArg(call.Argument(0))
		for i, t := range obs.targets {
			if t == n {
				obs.targets = append(obs.targets[:i], obs.targets[i+1:]...)
				break
			}
		}
		return goja.Undefined()
	})
	_ = o.Set("disconnect", func(goja.FunctionCall) goja.Value {
		obs.targets = nil
		return goja.Undefined()
	})
	_ = o.Set("takeRecords", func(goja.FunctionCall) goja.Value { return e.vm.NewArray() })
	_ = o.Set("root", goja.Null())
	_ = o.Set("rootMargin", "0px")
	_ = o.Set("thresholds", e.vm.NewArray(0))
	return o
}

// flushIntersection reports observed elements as intersecting, then clears
// them so a repeated flush does not re-fire.
func (e *jsEnv) flushIntersection() {
	for _, obs := range e.observers {
		if obs.cb == nil || len(obs.targets) == 0 {
			continue
		}
		targets := obs.targets
		obs.targets = nil
		entries := e.vm.NewArray()
		for i, t := range targets {
			entry := e.vm.NewObject()
			_ = entry.Set("target", e.wrap(t))
			_ = entry.Set("isIntersecting", true)
			_ = entry.Set("intersectionRatio", 1)
			_ = entry.Set("time", 0)
			rect := e.zeroRect()
			_ = entry.Set("boundingClientRect", rect)
			_ = entry.Set("intersectionRect", rect)
			_ = entry.Set("rootBounds", rect)
			_ = entries.Set(strconv.Itoa(i), entry)
		}
		func() {
			defer func() { _ = recover() }()
			_, _ = obs.cb(goja.Undefined(), entries, e.vm.ToValue(obs.obj))
		}()
	}
}

// zeroRect is a placeholder ClientRect, since there is no layout engine.
func (e *jsEnv) zeroRect() *goja.Object {
	r := e.vm.NewObject()
	for _, k := range []string{"x", "y", "top", "left", "right", "bottom", "width", "height"} {
		_ = r.Set(k, 0)
	}
	return r
}

func (e *jsEnv) newObserver(cb goja.Value) *goja.Object {
	o := e.vm.NewObject()
	_ = o.Set("observe", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("unobserve", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("disconnect", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("takeRecords", func(goja.FunctionCall) goja.Value { return e.vm.NewArray() })
	if cb != nil {
		_ = o.Set("callback", cb)
	}
	return o
}

// --- performance / crypto ---

func (e *jsEnv) performanceObject() *goja.Object {
	o := e.vm.NewObject()
	start := time.Now()
	_ = o.Set("timeOrigin", float64(start.UnixNano())/1e6)
	_ = o.Set("now", func(goja.FunctionCall) goja.Value {
		return e.vm.ToValue(float64(time.Since(start).Nanoseconds()) / 1e6)
	})
	_ = o.Set("mark", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("measure", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("clearMarks", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("getEntries", func(goja.FunctionCall) goja.Value { return e.vm.NewArray() })
	_ = o.Set("getEntriesByName", func(goja.FunctionCall) goja.Value { return e.vm.NewArray() })
	_ = o.Set("getEntriesByType", func(goja.FunctionCall) goja.Value { return e.vm.NewArray() })
	return o
}

func (e *jsEnv) cryptoObject() *goja.Object {
	o := e.vm.NewObject()
	_ = o.Set("getRandomValues", func(call goja.FunctionCall) goja.Value {
		arr, ok := call.Argument(0).(*goja.Object)
		if !ok {
			return call.Argument(0)
		}
		n := 0
		if lv := arr.Get("length"); lv != nil {
			n = int(lv.ToInteger())
		}
		buf := make([]byte, n)
		_, _ = rand.Read(buf)
		for i := 0; i < n; i++ {
			_ = arr.Set(strconv.Itoa(i), int(buf[i]))
		}
		return arr
	})
	_ = o.Set("randomUUID", func(goja.FunctionCall) goja.Value {
		var b [16]byte
		_, _ = rand.Read(b[:])
		b[6] = (b[6] & 0x0f) | 0x40
		b[8] = (b[8] & 0x3f) | 0x80
		s := hex.EncodeToString(b[:])
		return e.vm.ToValue(s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:])
	})
	return o
}

func (e *jsEnv) structuredClone(v goja.Value) goja.Value {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return v
	}
	out, err := e.vm.RunString("(function(x){return JSON.parse(JSON.stringify(x));})")
	if err != nil {
		return v
	}
	fn, ok := goja.AssertFunction(out)
	if !ok {
		return v
	}
	res, err := fn(goja.Undefined(), v)
	if err != nil {
		return v
	}
	return res
}

// --- TreeWalker / NodeFilter ---

// TreeWalker whatToShow bits, matching the DOM.
const (
	showElement  = 0x1
	showText     = 0x4
	showComment  = 0x80
	showDocument = 0x100
)

// newNodeFilter exposes the NodeFilter constants used with TreeWalker.
func (e *jsEnv) newNodeFilter() *goja.Object {
	o := e.vm.NewObject()
	// float64, because these are JavaScript numbers and SHOW_ALL does not fit
	// in a 32-bit int.
	for k, v := range map[string]float64{
		"FILTER_ACCEPT": 1,
		"FILTER_REJECT": 2,
		"FILTER_SKIP":   3,
		"SHOW_ALL":      0xFFFFFFFF,
		"SHOW_ELEMENT":  showElement,
		"SHOW_TEXT":     showText,
		"SHOW_COMMENT":  showComment,
		"SHOW_DOCUMENT": showDocument,
	} {
		_ = o.Set(k, v)
	}
	return o
}

// newTreeWalker implements the navigation methods libraries actually use,
// most importantly the `while (walker.nextNode())` text-extraction loop.
func (e *jsEnv) newTreeWalker(rootVal, whatToShow, filterVal goja.Value) *goja.Object {
	root := e.nodeArg(rootVal)
	if root == nil {
		root = e.page.doc
	}
	show := int64(0xFFFFFFFF)
	if whatToShow != nil && !goja.IsUndefined(whatToShow) {
		show = whatToShow.ToInteger()
	}
	var filterFn goja.Callable
	if fn, ok := goja.AssertFunction(filterVal); ok {
		filterFn = fn
	} else if fo, ok := filterVal.(*goja.Object); ok {
		if fn, ok := goja.AssertFunction(fo.Get("acceptNode")); ok {
			filterFn = fn
		}
	}

	accept := func(n *html.Node) bool {
		if n == nil {
			return false
		}
		switch n.Type {
		case html.ElementNode:
			if isFragment(n) {
				return false
			}
			if show&showElement == 0 {
				return false
			}
		case html.TextNode:
			if show&showText == 0 {
				return false
			}
		case html.CommentNode:
			if show&showComment == 0 {
				return false
			}
		case html.DocumentNode:
			if show&showDocument == 0 {
				return false
			}
		default:
			return false
		}
		if filterFn != nil {
			res, err := filterFn(goja.Undefined(), e.wrap(n))
			if err != nil {
				return false
			}
			return res.ToInteger() == 1
		}
		return true
	}

	o := e.vm.NewObject()
	_ = o.Set("root", e.wrap(root))
	_ = o.Set("whatToShow", show)
	current := root
	_ = o.Set("currentNode", e.wrap(current))

	// documentOrderNext returns the node after n within root's subtree.
	documentOrderNext := func(n *html.Node) *html.Node {
		if n == nil {
			return nil
		}
		if n.FirstChild != nil {
			return n.FirstChild
		}
		for cur := n; cur != nil && cur != root; cur = cur.Parent {
			if cur.NextSibling != nil {
				return cur.NextSibling
			}
		}
		return nil
	}

	const maxNodes = 5_000_000

	_ = o.Set("nextNode", func(goja.FunctionCall) goja.Value {
		n := documentOrderNext(current)
		for steps := 0; n != nil && steps < maxNodes; steps++ {
			if accept(n) {
				current = n
				_ = o.Set("currentNode", e.wrap(current))
				return e.wrap(n)
			}
			n = documentOrderNext(n)
		}
		return goja.Null()
	})
	_ = o.Set("previousNode", func(goja.FunctionCall) goja.Value {
		var prevAccepted *html.Node
		n := documentOrderNext(root)
		for steps := 0; n != nil && n != current && steps < maxNodes; steps++ {
			if accept(n) {
				prevAccepted = n
			}
			n = documentOrderNext(n)
		}
		if prevAccepted == nil {
			return goja.Null()
		}
		current = prevAccepted
		_ = o.Set("currentNode", e.wrap(current))
		return e.wrap(prevAccepted)
	})
	_ = o.Set("parentNode", func(goja.FunctionCall) goja.Value {
		for n := current.Parent; n != nil && n != root; n = n.Parent {
			if accept(n) {
				current = n
				_ = o.Set("currentNode", e.wrap(current))
				return e.wrap(n)
			}
		}
		return goja.Null()
	})
	_ = o.Set("firstChild", func(goja.FunctionCall) goja.Value {
		for c := current.FirstChild; c != nil; c = c.NextSibling {
			if accept(c) {
				current = c
				_ = o.Set("currentNode", e.wrap(current))
				return e.wrap(c)
			}
		}
		return goja.Null()
	})
	_ = o.Set("lastChild", func(goja.FunctionCall) goja.Value {
		for c := current.LastChild; c != nil; c = c.PrevSibling {
			if accept(c) {
				current = c
				_ = o.Set("currentNode", e.wrap(current))
				return e.wrap(c)
			}
		}
		return goja.Null()
	})
	_ = o.Set("nextSibling", func(goja.FunctionCall) goja.Value {
		for c := current.NextSibling; c != nil; c = c.NextSibling {
			if accept(c) {
				current = c
				_ = o.Set("currentNode", e.wrap(current))
				return e.wrap(c)
			}
		}
		return goja.Null()
	})
	_ = o.Set("previousSibling", func(goja.FunctionCall) goja.Value {
		for c := current.PrevSibling; c != nil; c = c.PrevSibling {
			if accept(c) {
				current = c
				_ = o.Set("currentNode", e.wrap(current))
				return e.wrap(c)
			}
		}
		return goja.Null()
	})
	_ = o.Set("filter", filterVal)
	return o
}
