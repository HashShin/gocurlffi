package browser

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/dop251/goja"
)

// installErrorStatics adds the two non-standard Error members every V8 build
// carries. Their absence is a cheap way to tell Chrome from something that is
// not Chrome, and captureStackTrace is real API that libraries call.
func (e *jsEnv) installErrorStatics() {
	ctor, ok := e.vm.Get("Error").(*goja.Object)
	if !ok || ctor == nil {
		return
	}
	_ = ctor.Set("stackTraceLimit", 10)
	_ = ctor.Set("captureStackTrace", func(call goja.FunctionCall) goja.Value {
		target, ok := call.Argument(0).(*goja.Object)
		if !ok || target == nil {
			return goja.Undefined()
		}
		_ = target.Set("stack", e.v8Stack())
		return goja.Undefined()
	})
}

// v8Stack renders the current call stack the way V8's Error.stack reads, so a
// frame is "at name (file:line:column)" rather than goja's tab-indented form.
func (e *jsEnv) v8Stack() string {
	var b strings.Builder
	b.WriteString("Error")
	for _, f := range e.vm.CaptureCallStack(10, nil) {
		pos := f.Position()
		name, src := f.FuncName(), f.SrcName()
		b.WriteString("\n    at ")
		if name != "" && name != "<anonymous>" && name != "<native>" {
			fmt.Fprintf(&b, "%s (%s:%d:%d)", name, src, pos.Line, pos.Column)
			continue
		}
		fmt.Fprintf(&b, "%s:%d:%d", src, pos.Line, pos.Column)
	}
	return b.String()
}

// A host-backed function takes its JavaScript name from the Go symbol that
// implements it, so
//
//	Function.prototype.toString.call(btoa)
//
// reads "function shade/browser.(*jsEnv).setupGlobals.func25() { [native
// code] }" rather than "function btoa() { [native code] }", and btoa.name reads
// "github.com/HashShin/shade/browser.(*jsEnv).setupGlobals.func25".
//
// No browser produces that, so a page can tell it is not talking to one with a
// single regex - and this one also names the library it is talking to instead.
// It is not a fingerprint, it is a much cruder signal, and it is on every
// function in the environment: btoa, fetch, setTimeout, querySelector,
// navigator.sendBeacon, every prototype method.

// hostSymbolName reports whether a function is wearing a Go symbol as its name.
// A JavaScript function name never contains a slash, so its presence is safe to
// read as "this is a host function that has not been renamed".
func hostSymbolName(o *goja.Object) bool {
	if o == nil {
		return false
	}
	n := o.Get("name")
	if n == nil || goja.IsUndefined(n) || goja.IsNull(n) {
		return false
	}
	return strings.Contains(n.String(), "/")
}

var functionSourceRe = regexp.MustCompile(`^(function\s*)([^\s(]*)(\s*\()`)

// nameHostFunction gives a host function the name of the property it is held
// under, when it is currently wearing a Go symbol and that name is a usable
// JavaScript identifier.
func (e *jsEnv) nameHostFunction(v goja.Value, key string) {
	o, ok := v.(*goja.Object)
	if !ok || !hostSymbolName(o) || !validJSIdentifier(key) {
		return
	}
	_ = o.DefineDataProperty("name", e.vm.ToValue(key), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
}

// validJSIdentifier reports whether a property key can stand as a function
// name. Symbol keys and computed names cannot.
func validJSIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || r == '$' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// fixHostFunctionNames renames host functions reachable from the objects a page
// touches, each after the property it is held under. The walk is depth-limited
// and skips DOM nodes, because the document tree is the whole document.
func (e *jsEnv) fixHostFunctionNames() {
	seen := map[*goja.Object]bool{}
	var walk func(o *goja.Object, depth int)
	walk = func(o *goja.Object, depth int) {
		if o == nil || depth > 3 || seen[o] {
			return
		}
		seen[o] = true
		for _, k := range o.Keys() {
			v := o.Get(k)
			vo, ok := v.(*goja.Object)
			if !ok || vo == nil {
				continue
			}
			if _, isFn := goja.AssertFunction(v); isFn {
				e.nameHostFunction(v, k)
				continue
			}
			if nv := vo.GetSymbol(e.nodeSym); nv != nil && !goja.IsUndefined(nv) && !goja.IsNull(nv) {
				continue
			}
			walk(vo, depth+1)
		}
	}
	walk(e.vm.GlobalObject(), 0)
	for _, proto := range []*goja.Object{
		e.protosRef.node, e.protosRef.element, e.protosRef.document,
		e.protosRef.text, e.protosRef.comment, e.protosRef.fragment,
	} {
		walk(proto, 0)
	}
}

// sanitizeFunctionToString keeps Function.prototype.toString from leaking a Go
// symbol for any host function the walk did not reach, and gives back the
// renamed form where the walk did reach it.
func (e *jsEnv) sanitizeFunctionToString() {
	ctor, ok := e.vm.Get("Function").(*goja.Object)
	if !ok {
		return
	}
	proto, ok := ctor.Get("prototype").(*goja.Object)
	if !ok {
		return
	}
	orig, ok := goja.AssertFunction(proto.Get("toString"))
	if !ok {
		return
	}
	_ = proto.Set("toString", func(call goja.FunctionCall) goja.Value {
		v, err := orig(call.This)
		if err != nil {
			panic(err)
		}
		s := v.String()
		m := functionSourceRe.FindStringSubmatch(s)
		if m == nil || !strings.Contains(m[2], "/") {
			return e.vm.ToValue(s)
		}
		// The function knows its own name if the walk renamed it; otherwise
		// leave it anonymous rather than publishing the Go symbol.
		name := ""
		if fo, ok := call.This.(*goja.Object); ok {
			if nv := fo.Get("name"); nv != nil && !goja.IsUndefined(nv) && !goja.IsNull(nv) {
				if n := nv.String(); n != "" && !strings.Contains(n, "/") {
					name = n
				}
			}
		}
		return e.vm.ToValue(m[1] + name + m[3] + strings.TrimPrefix(s, m[0]))
	})
}
