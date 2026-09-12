package browser

import (
	"encoding/json"
	"strings"

	"github.com/dop251/goja"
	"gocurlffi/requests"
)

// setupNetwork installs fetch() and XMLHttpRequest. Both use the browser's
// impersonating HTTP session, so subresource requests carry the same browser
// fingerprint as the document request.
func (e *jsEnv) setupNetwork() {
	_ = e.vm.Set("fetch", func(call goja.FunctionCall) goja.Value {
		return e.fetch(call)
	})
	_ = e.vm.Set("XMLHttpRequest", func(call goja.ConstructorCall) *goja.Object {
		return e.newXHR()
	})
}

func (e *jsEnv) resolvedPromise(v goja.Value) goja.Value {
	p, resolve, _ := e.vm.NewPromise()
	_ = resolve(v)
	return e.vm.ToValue(p)
}

func (e *jsEnv) rejectedPromise(err error) goja.Value {
	p, _, reject := e.vm.NewPromise()
	_ = reject(e.vm.NewGoError(err))
	return e.vm.ToValue(p)
}

// fetch performs a synchronous request and returns an already-resolved
// Promise, which is enough for headless page rendering.
func (e *jsEnv) fetch(call goja.FunctionCall) goja.Value {
	input := call.Argument(0)
	method := "GET"
	rawURL := ""
	headers := map[string]string{}
	var body []byte

	switch v := input.(type) {
	case *goja.Object:
		if u := v.Get("url"); u != nil && !goja.IsUndefined(u) {
			rawURL = u.String()
		} else {
			rawURL = v.String()
		}
	default:
		rawURL = argString(input)
	}

	if init, ok := call.Argument(1).(*goja.Object); ok {
		if m := init.Get("method"); m != nil && !goja.IsUndefined(m) {
			method = strings.ToUpper(m.String())
		}
		if h := init.Get("headers"); h != nil && !goja.IsUndefined(h) {
			if ho, ok := h.(*goja.Object); ok {
				for _, k := range ho.Keys() {
					headers[k] = ho.Get(k).String()
				}
			}
		}
		if b := init.Get("body"); b != nil && !goja.IsUndefined(b) && !goja.IsNull(b) {
			body = []byte(b.String())
		}
	}

	rawURL = resolveURL(e.page.baseURL(), rawURL)
	resp, err := e.doRequest(method, rawURL, headers, body)
	if err != nil {
		return e.rejectedPromise(err)
	}
	return e.resolvedPromise(e.vm.ToValue(e.fetchResponse(resp)))
}

// doRequest issues a request through the impersonating session.
func (e *jsEnv) doRequest(method, rawURL string, headers map[string]string, body []byte) (*requests.Response, error) {
	opts := []requests.Option{}
	if len(headers) > 0 {
		opts = append(opts, requests.WithHeaders(headers))
	}
	if len(body) > 0 {
		opts = append(opts, requests.WithContent(body))
	}
	return e.page.browser.session().Request(method, rawURL, opts...)
}

func (e *jsEnv) fetchResponse(resp *requests.Response) *goja.Object {
	o := e.vm.NewObject()
	_ = o.Set("ok", resp.StatusCode >= 200 && resp.StatusCode < 300)
	_ = o.Set("status", resp.StatusCode)
	_ = o.Set("statusText", resp.Reason)
	_ = o.Set("url", resp.URL)
	_ = o.Set("redirected", resp.RedirectCount > 0)
	_ = o.Set("type", "basic")

	h := e.vm.NewObject()
	_ = h.Set("get", func(call goja.FunctionCall) goja.Value {
		name := argString(call.Argument(0))
		if resp.Headers == nil {
			return goja.Null()
		}
		if v := resp.Headers.Get(name); v != "" {
			return e.vm.ToValue(v)
		}
		return goja.Null()
	})
	_ = h.Set("has", func(call goja.FunctionCall) goja.Value {
		if resp.Headers == nil {
			return e.vm.ToValue(false)
		}
		return e.vm.ToValue(resp.Headers.Has(argString(call.Argument(0))))
	})
	_ = h.Set("forEach", func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if ok && resp.Headers != nil {
			for _, item := range resp.Headers.MultiItems() {
				_, _ = fn(goja.Undefined(), e.vm.ToValue(item.Value), e.vm.ToValue(item.Name))
			}
		}
		return goja.Undefined()
	})
	_ = o.Set("headers", h)

	content := resp.Content
	_ = o.Set("text", func(goja.FunctionCall) goja.Value {
		return e.resolvedPromise(e.vm.ToValue(string(content)))
	})
	_ = o.Set("json", func(goja.FunctionCall) goja.Value {
		var parsed interface{}
		if err := json.Unmarshal(content, &parsed); err != nil {
			return e.rejectedPromise(err)
		}
		return e.resolvedPromise(e.vm.ToValue(parsed))
	})
	_ = o.Set("arrayBuffer", func(goja.FunctionCall) goja.Value {
		return e.resolvedPromise(e.vm.ToValue(e.vm.NewArrayBuffer(content)))
	})
	_ = o.Set("clone", func(goja.FunctionCall) goja.Value {
		return e.vm.ToValue(e.fetchResponse(resp))
	})
	return o
}

// --- XMLHttpRequest ---

func (e *jsEnv) newXHR() *goja.Object {
	o := e.vm.NewObject()

	var method, url string
	headers := map[string]string{}
	sent := false

	_ = o.Set("readyState", 0)
	_ = o.Set("status", 0)
	_ = o.Set("statusText", "")
	_ = o.Set("responseText", "")
	_ = o.Set("response", "")
	_ = o.Set("responseURL", "")
	_ = o.Set("timeout", 0)
	_ = o.Set("withCredentials", false)
	_ = o.Set("responseType", "")

	fire := func(typ string) {
		ev := e.newEvent(typ)
		_ = ev.Set("target", o)
		_ = ev.Set("currentTarget", o)
		if h := o.Get("on" + strings.ToLower(typ)); h != nil {
			if fn, ok := goja.AssertFunction(h); ok {
				_, _ = fn(o, ev)
			}
		}
		// addEventListener list
		if lv := o.Get("__listeners"); lv != nil {
			if lo, ok := lv.(*goja.Object); ok {
				if arr := lo.Get(typ); arr != nil {
					if ao, ok := arr.(*goja.Object); ok {
						for i := 0; i < int(ao.Get("length").ToInteger()); i++ {
							if fn, ok := goja.AssertFunction(ao.Get(intToString(i))); ok {
								_, _ = fn(o, ev)
							}
						}
					}
				}
			}
		}
	}

	_ = o.Set("open", func(call goja.FunctionCall) goja.Value {
		method = strings.ToUpper(argString(call.Argument(0)))
		url = resolveURL(e.page.baseURL(), argString(call.Argument(1)))
		_ = o.Set("readyState", 1)
		fire("readystatechange")
		return goja.Undefined()
	})
	_ = o.Set("setRequestHeader", func(call goja.FunctionCall) goja.Value {
		headers[argString(call.Argument(0))] = argString(call.Argument(1))
		return goja.Undefined()
	})
	_ = o.Set("getResponseHeader", func(call goja.FunctionCall) goja.Value {
		if v := o.Get("__respHeaders"); v != nil {
			if ho, ok := v.(*goja.Object); ok {
				if g := ho.Get(argString(call.Argument(0))); g != nil {
					return g
				}
			}
		}
		return goja.Null()
	})
	_ = o.Set("getAllResponseHeaders", func(call goja.FunctionCall) goja.Value {
		if v := o.Get("__headerText"); v != nil {
			return v
		}
		return e.vm.ToValue("")
	})
	_ = o.Set("abort", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("addEventListener", func(call goja.FunctionCall) goja.Value {
		typ := strings.ToLower(argString(call.Argument(0)))
		ls := o.Get("__listeners")
		lo, ok := ls.(*goja.Object)
		if !ok {
			lo = e.vm.NewObject()
			_ = o.Set("__listeners", lo)
		}
		arr := lo.Get(typ)
		ao, ok := arr.(*goja.Object)
		if !ok {
			ao = e.vm.NewArray()
			_ = lo.Set(typ, ao)
		}
		n := int(ao.Get("length").ToInteger())
		_ = ao.Set(intToString(n), call.Argument(1))
		return goja.Undefined()
	})
	_ = o.Set("removeEventListener", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("send", func(call goja.FunctionCall) goja.Value {
		if sent {
			return goja.Undefined()
		}
		sent = true
		_ = o.Set("readyState", 2)
		fire("readystatechange")

		var body []byte
		if b := call.Argument(0); b != nil && !goja.IsUndefined(b) && !goja.IsNull(b) {
			body = []byte(b.String())
		}
		resp, err := e.doRequest(method, url, headers, body)
		if err != nil {
			_ = o.Set("readyState", 4)
			fire("readystatechange")
			fire("error")
			fire("loadend")
			return goja.Undefined()
		}
		_ = o.Set("status", resp.StatusCode)
		_ = o.Set("statusText", resp.Reason)
		_ = o.Set("responseText", string(resp.Content))
		_ = o.Set("response", string(resp.Content))
		_ = o.Set("responseURL", resp.URL)
		if resp.Headers != nil {
			hm := e.vm.NewObject()
			var sb strings.Builder
			for _, item := range resp.Headers.MultiItems() {
				_ = hm.Set(item.Name, item.Value)
				sb.WriteString(item.Name + ": " + item.Value + "\r\n")
			}
			_ = o.Set("__respHeaders", hm)
			_ = o.Set("__headerText", sb.String())
		}
		_ = o.Set("readyState", 4)
		fire("readystatechange")
		fire("load")
		fire("loadend")
		return goja.Undefined()
	})
	return o
}

func intToString(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
