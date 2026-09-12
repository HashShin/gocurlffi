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
		return e.newObserver(call.Argument(0))
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
