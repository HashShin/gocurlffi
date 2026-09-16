package browser

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
	"golang.org/x/net/html"
)

// This file implements the body-bearing web APIs: Blob, File, FileReader,
// FormData, Headers, Request, Response and the Streams. They previously existed
// only as tags in iface.go, so `new Blob(['hi']).size` answered undefined and
// `new FormData().append` threw. The bodies are in-memory byte slices, which is
// what the rest of this browser already assumes: fetch() buffers the whole
// response before it returns a promise.

// --- markers ---------------------------------------------------------------

// Objects carry their Go state on a non-enumerable private property. Reading it
// back through Export() gives the pointer, so a JS value can be recognised
// without a parallel registry.
const (
	blobMark     = "__gocurlffiBlob"
	headersMark  = "__gocurlffiHeaders"
	formDataMark = "__gocurlffiFormData"
	requestMark  = "__gocurlffiRequest"
	responseMark = "__gocurlffiResponse"
	streamMark   = "__gocurlffiStream"
	abortMark    = "__gocurlffiAbortSignal"
)

func (e *jsEnv) mark(o *goja.Object, name string, v any) {
	_ = o.DefineDataProperty(name, e.vm.ToValue(v), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
}

func marked[T any](o *goja.Object, name string) (T, bool) {
	var zero T
	if o == nil {
		return zero, false
	}
	p := o.Get(name)
	if p == nil || goja.IsUndefined(p) || goja.IsNull(p) {
		return zero, false
	}
	v, ok := p.Export().(T)
	return v, ok
}

// --- Blob ------------------------------------------------------------------

// jsBlobData is the body shared by Blob and File.
type jsBlobData struct {
	data         []byte
	typ          string
	name         string // File
	lastModified int64  // File
}

func (e *jsEnv) newBlobObject(b *jsBlobData) *goja.Object {
	o := e.vm.NewObject()
	e.mark(o, blobMark, b)
	name := "Blob"
	if b.name != "" || b.lastModified != 0 {
		name = "File"
	}
	e.tagHost(o, name)
	_ = o.Set("size", len(b.data))
	_ = o.Set("type", b.typ)
	if b.name != "" || b.lastModified != 0 {
		_ = o.Set("name", b.name)
		_ = o.Set("lastModified", b.lastModified)
		_ = o.Set("webkitRelativePath", "")
	}
	_ = o.Set("slice", func(call goja.FunctionCall) goja.Value {
		start := clampIndex(call.Argument(0), len(b.data), 0)
		end := clampIndex(call.Argument(1), len(b.data), len(b.data))
		if end < start {
			end = start
		}
		typ := b.typ
		if t := argString(call.Argument(2)); t != "" {
			typ = strings.ToLower(t)
		}
		return e.vm.ToValue(e.newBlobObject(&jsBlobData{data: append([]byte(nil), b.data[start:end]...), typ: typ}))
	})
	_ = o.Set("text", func(goja.FunctionCall) goja.Value {
		return e.resolvedPromise(e.vm.ToValue(string(b.data)))
	})
	_ = o.Set("arrayBuffer", func(goja.FunctionCall) goja.Value {
		return e.resolvedPromise(e.vm.ToValue(e.vm.NewArrayBuffer(append([]byte(nil), b.data...))))
	})
	_ = o.Set("bytes", func(goja.FunctionCall) goja.Value {
		return e.resolvedPromise(e.newUint8Array(append([]byte(nil), b.data...)))
	})
	_ = o.Set("stream", func(goja.FunctionCall) goja.Value {
		s := e.newReadableStream()
		s.enqueue(b.data)
		s.close()
		return e.vm.ToValue(s.obj)
	})
	return o
}

// clampIndex resolves a slice index, treating a negative value as from-the-end
// and a missing one as the given default, per Blob.slice.
func clampIndex(v goja.Value, size, def int) int {
	if v == nil || goja.IsUndefined(v) {
		return def
	}
	n := int(v.ToInteger())
	if n < 0 {
		n += size
	}
	if n < 0 {
		n = 0
	}
	if n > size {
		n = size
	}
	return n
}

func (e *jsEnv) newBlob(parts goja.Value, opts *goja.Object) *goja.Object {
	var data []byte
	if parts != nil && !goja.IsUndefined(parts) && !goja.IsNull(parts) {
		if arr, ok := parts.(*goja.Object); ok {
			for i := 0; i < int(arr.Get("length").ToInteger()); i++ {
				data = append(data, e.blobPart(arr.Get(strconv.Itoa(i)))...)
			}
		} else {
			data = []byte(parts.String())
		}
	}
	b := &jsBlobData{data: data}
	if opts != nil {
		b.typ = strings.ToLower(argString(opts.Get("type")))
	}
	return e.newBlobObject(b)
}

// blobPart converts one Blob-part argument to bytes. A nested Blob contributes
// its bytes, not its "[object Blob]" string.
func (e *jsEnv) blobPart(v goja.Value) []byte {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	if o, ok := v.(*goja.Object); ok {
		if b, ok := marked[*jsBlobData](o, blobMark); ok {
			return b.data
		}
	}
	return toBytes(v)
}

func (e *jsEnv) newFile(parts goja.Value, name string, opts *goja.Object) *goja.Object {
	var data []byte
	if arr, ok := parts.(*goja.Object); ok && arr != nil {
		for i := 0; i < int(arr.Get("length").ToInteger()); i++ {
			data = append(data, e.blobPart(arr.Get(strconv.Itoa(i)))...)
		}
	} else if parts != nil && !goja.IsUndefined(parts) && !goja.IsNull(parts) {
		data = []byte(parts.String())
	}
	b := &jsBlobData{data: data, name: name, lastModified: time.Now().UnixMilli()}
	if opts != nil {
		b.typ = strings.ToLower(argString(opts.Get("type")))
		if lm := opts.Get("lastModified"); lm != nil && !goja.IsUndefined(lm) {
			b.lastModified = lm.ToInteger()
		}
	}
	return e.newBlobObject(b)
}

// blobOf returns the body behind a JS value if it is a Blob or File.
func blobOf(v goja.Value) *jsBlobData {
	o, ok := v.(*goja.Object)
	if !ok {
		return nil
	}
	b, _ := marked[*jsBlobData](o, blobMark)
	return b
}

// --- FileReader ------------------------------------------------------------

func (e *jsEnv) newFileReader() *goja.Object {
	o := e.vm.NewObject()
	e.tagHost(o, "FileReader")
	_ = o.Set("EMPTY", 0)
	_ = o.Set("LOADING", 1)
	_ = o.Set("DONE", 2)
	_ = o.Set("readyState", 0)
	_ = o.Set("result", goja.Null())
	_ = o.Set("error", goja.Null())

	dispatch := func(typ string) {
		if h := o.Get("on" + typ); h != nil {
			if fn, ok := goja.AssertFunction(h); ok {
				ev := e.newEvent(typ)
				_ = ev.Set("target", o)
				_, _ = fn(o, ev)
			}
		}
	}
	read := func(call goja.FunctionCall, conv func(b []byte) goja.Value) goja.Value {
		b := blobOf(call.Argument(0))
		if b == nil {
			_ = o.Set("readyState", 2)
			_ = o.Set("error", e.vm.NewTypeError("FileReader: argument is not a Blob"))
			dispatch("error")
			dispatch("loadend")
			return goja.Undefined()
		}
		_ = o.Set("readyState", 1)
		dispatch("loadstart")
		_ = o.Set("result", conv(b.data))
		_ = o.Set("readyState", 2)
		dispatch("load")
		dispatch("loadend")
		return goja.Undefined()
	}

	_ = o.Set("readAsText", func(call goja.FunctionCall) goja.Value {
		return read(call, func(b []byte) goja.Value { return e.vm.ToValue(string(b)) })
	})
	_ = o.Set("readAsArrayBuffer", func(call goja.FunctionCall) goja.Value {
		return read(call, func(b []byte) goja.Value { return e.vm.ToValue(e.vm.NewArrayBuffer(append([]byte(nil), b...))) })
	})
	_ = o.Set("readAsDataURL", func(call goja.FunctionCall) goja.Value {
		return read(call, func(b []byte) goja.Value {
			typ := "application/octet-stream"
			if bo := call.Argument(0).(*goja.Object); bo != nil {
				if t := argString(bo.Get("type")); t != "" {
					typ = t
				}
			}
			return e.vm.ToValue("data:" + typ + ";base64," + base64Std(b))
		})
	})
	_ = o.Set("readAsBinaryString", func(call goja.FunctionCall) goja.Value {
		return read(call, func(b []byte) goja.Value {
			var sb strings.Builder
			for _, c := range b {
				sb.WriteByte(c)
			}
			return e.vm.ToValue(sb.String())
		})
	})
	_ = o.Set("abort", func(goja.FunctionCall) goja.Value {
		_ = o.Set("readyState", 2)
		_ = o.Set("result", goja.Null())
		dispatch("abort")
		dispatch("loadend")
		return goja.Undefined()
	})
	_ = o.Set("addEventListener", func(call goja.FunctionCall) goja.Value {
		typ := argString(call.Argument(0))
		fn := call.Argument(1)
		prev := o.Get("on" + typ)
		_ = o.Set("on"+typ, func(call goja.FunctionCall) goja.Value {
			if pfn, ok := goja.AssertFunction(prev); ok {
				_, _ = pfn(o, call.Argument(0))
			}
			if nfn, ok := goja.AssertFunction(fn); ok {
				_, _ = nfn(o, call.Argument(0))
			}
			return goja.Undefined()
		})
		return goja.Undefined()
	})
	_ = o.Set("removeEventListener", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	return o
}

// --- Headers ---------------------------------------------------------------

// headersData is a case-insensitive ordered multimap, the storage the DOM
// specifies for Headers.
type headersData struct {
	names  []string // lower-cased, in insertion order
	values [][]string
}

func newHeadersData() *headersData { return &headersData{} }

func (h *headersData) index(name string) int {
	name = strings.ToLower(name)
	for i, n := range h.names {
		if n == name {
			return i
		}
	}
	return -1
}

func (h *headersData) append(name, value string) {
	i := h.index(name)
	if i < 0 {
		h.names = append(h.names, strings.ToLower(name))
		h.values = append(h.values, []string{value})
		return
	}
	h.values[i] = append(h.values[i], value)
}

func (h *headersData) set(name, value string) {
	i := h.index(name)
	if i < 0 {
		h.append(name, value)
		return
	}
	h.values[i] = []string{value}
}

func (h *headersData) get(name string) (string, bool) {
	i := h.index(name)
	if i < 0 {
		return "", false
	}
	return strings.Join(h.values[i], ", "), true
}

func (h *headersData) del(name string) {
	i := h.index(name)
	if i < 0 {
		return
	}
	h.names = append(h.names[:i], h.names[i+1:]...)
	h.values = append(h.values[:i], h.values[i+1:]...)
}

// pairs flattens the map for the transport layer as first-value-wins, which is
// what requests.Headers does with a single Set per name.
func (h *headersData) pairs() map[string]string {
	out := map[string]string{}
	for i, n := range h.names {
		out[n] = strings.Join(h.values[i], ", ")
	}
	return out
}

func (e *jsEnv) newHeadersObject(h *headersData) *goja.Object {
	o := e.vm.NewObject()
	e.mark(o, headersMark, h)
	e.tagHost(o, "Headers")
	_ = o.Set("append", func(call goja.FunctionCall) goja.Value {
		h.append(argString(call.Argument(0)), argString(call.Argument(1)))
		return goja.Undefined()
	})
	_ = o.Set("set", func(call goja.FunctionCall) goja.Value {
		h.set(argString(call.Argument(0)), argString(call.Argument(1)))
		return goja.Undefined()
	})
	_ = o.Set("get", func(call goja.FunctionCall) goja.Value {
		if v, ok := h.get(argString(call.Argument(0))); ok {
			return e.vm.ToValue(v)
		}
		return goja.Null()
	})
	_ = o.Set("getAll", func(call goja.FunctionCall) goja.Value {
		i := h.index(argString(call.Argument(0)))
		arr := e.vm.NewArray()
		if i < 0 {
			return e.vm.ToValue(arr)
		}
		for j, v := range h.values[i] {
			_ = arr.Set(strconv.Itoa(j), v)
		}
		return e.vm.ToValue(arr)
	})
	_ = o.Set("has", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue(h.index(argString(call.Argument(0))) >= 0)
	})
	_ = o.Set("delete", func(call goja.FunctionCall) goja.Value {
		h.del(argString(call.Argument(0)))
		return goja.Undefined()
	})
	_ = o.Set("forEach", func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return goja.Undefined()
		}
		for i, n := range h.names {
			_, _ = fn(goja.Undefined(), e.vm.ToValue(strings.Join(h.values[i], ", ")), e.vm.ToValue(n), o)
		}
		return goja.Undefined()
	})
	e.setEntryIterator(o, func() []goja.Value {
		out := make([]goja.Value, 0, len(h.names))
		for i, n := range h.names {
			pair := e.vm.NewArray()
			_ = pair.Set("0", n)
			_ = pair.Set("1", strings.Join(h.values[i], ", "))
			out = append(out, e.vm.ToValue(pair))
		}
		return out
	})
	return o
}

// headersFromInit accepts the four shapes the Headers constructor and a fetch
// init accept: a Headers, a plain object, a sequence of pairs, or undefined.
func (e *jsEnv) headersFromInit(v goja.Value) *headersData {
	h := newHeadersData()
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return h
	}
	o, ok := v.(*goja.Object)
	if !ok {
		return h
	}
	if existing, ok := marked[*headersData](o, headersMark); ok {
		for i, n := range existing.names {
			for _, val := range existing.values[i] {
				h.append(n, val)
			}
		}
		return h
	}
	if lv := o.Get("length"); lv != nil && !goja.IsUndefined(lv) {
		for i := 0; i < int(lv.ToInteger()); i++ {
			item, ok := o.Get(strconv.Itoa(i)).(*goja.Object)
			if !ok {
				continue
			}
			h.append(argString(item.Get("0")), argString(item.Get("1")))
		}
		return h
	}
	for _, k := range o.Keys() {
		h.set(k, argString(o.Get(k)))
	}
	return h
}

func headersOf(v goja.Value) *headersData {
	o, ok := v.(*goja.Object)
	if !ok {
		return nil
	}
	h, _ := marked[*headersData](o, headersMark)
	return h
}

// --- FormData --------------------------------------------------------------

type formEntry struct {
	name  string
	value string
	blob  *jsBlobData
}

type formData struct {
	entries []formEntry
}

func (e *jsEnv) newFormDataObject(fd *formData) *goja.Object {
	o := e.vm.NewObject()
	e.mark(o, formDataMark, fd)
	e.tagHost(o, "FormData")

	valueOf := func(v goja.Value) formEntry {
		if b := blobOf(v); b != nil {
			return formEntry{blob: b}
		}
		return formEntry{value: argString(v)}
	}
	appendFn := func(e2 formEntry, name string, replace bool) {
		if replace {
			out := fd.entries[:0]
			for _, x := range fd.entries {
				if x.name != name {
					out = append(out, x)
				}
			}
			fd.entries = out
		}
		e2.name = name
		fd.entries = append(fd.entries, e2)
	}

	_ = o.Set("append", func(call goja.FunctionCall) goja.Value {
		appendFn(valueOf(call.Argument(1)), argString(call.Argument(0)), false)
		return goja.Undefined()
	})
	_ = o.Set("set", func(call goja.FunctionCall) goja.Value {
		appendFn(valueOf(call.Argument(1)), argString(call.Argument(0)), true)
		return goja.Undefined()
	})
	_ = o.Set("get", func(call goja.FunctionCall) goja.Value {
		name := argString(call.Argument(0))
		for _, x := range fd.entries {
			if x.name != name {
				continue
			}
			if x.blob != nil {
				return e.vm.ToValue(e.newBlobObject(x.blob))
			}
			return e.vm.ToValue(x.value)
		}
		return goja.Null()
	})
	_ = o.Set("getAll", func(call goja.FunctionCall) goja.Value {
		name := argString(call.Argument(0))
		arr := e.vm.NewArray()
		n := 0
		for _, x := range fd.entries {
			if x.name != name {
				continue
			}
			if x.blob != nil {
				_ = arr.Set(strconv.Itoa(n), e.newBlobObject(x.blob))
			} else {
				_ = arr.Set(strconv.Itoa(n), x.value)
			}
			n++
		}
		return e.vm.ToValue(arr)
	})
	_ = o.Set("has", func(call goja.FunctionCall) goja.Value {
		name := argString(call.Argument(0))
		for _, x := range fd.entries {
			if x.name == name {
				return e.vm.ToValue(true)
			}
		}
		return e.vm.ToValue(false)
	})
	_ = o.Set("delete", func(call goja.FunctionCall) goja.Value {
		name := argString(call.Argument(0))
		out := fd.entries[:0]
		for _, x := range fd.entries {
			if x.name != name {
				out = append(out, x)
			}
		}
		fd.entries = out
		return goja.Undefined()
	})
	_ = o.Set("forEach", func(call goja.FunctionCall) goja.Value {
		fn, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			return goja.Undefined()
		}
		for _, x := range fd.entries {
			if x.blob != nil {
				_, _ = fn(goja.Undefined(), e.vm.ToValue(e.newBlobObject(x.blob)), e.vm.ToValue(x.name), o)
			} else {
				_, _ = fn(goja.Undefined(), e.vm.ToValue(x.value), e.vm.ToValue(x.name), o)
			}
		}
		return goja.Undefined()
	})
	e.setMethodIterator(o, "keys", func() []goja.Value {
		out := make([]goja.Value, 0, len(fd.entries))
		for _, x := range fd.entries {
			out = append(out, e.vm.ToValue(x.name))
		}
		return out
	})
	e.setMethodIterator(o, "values", func() []goja.Value {
		out := make([]goja.Value, 0, len(fd.entries))
		for _, x := range fd.entries {
			if x.blob != nil {
				out = append(out, e.vm.ToValue(e.newBlobObject(x.blob)))
			} else {
				out = append(out, e.vm.ToValue(x.value))
			}
		}
		return out
	})
	e.setMethodIterator(o, "entries", func() []goja.Value {
		out := make([]goja.Value, 0, len(fd.entries))
		for _, x := range fd.entries {
			pair := e.vm.NewArray()
			_ = pair.Set("0", x.name)
			if x.blob != nil {
				_ = pair.Set("1", e.newBlobObject(x.blob))
			} else {
				_ = pair.Set("1", x.value)
			}
			out = append(out, e.vm.ToValue(pair))
		}
		return out
	})
	e.setEntryIterator(o, func() []goja.Value {
		out := make([]goja.Value, 0, len(fd.entries))
		for _, x := range fd.entries {
			pair := e.vm.NewArray()
			_ = pair.Set("0", x.name)
			if x.blob != nil {
				_ = pair.Set("1", e.newBlobObject(x.blob))
			} else {
				_ = pair.Set("1", x.value)
			}
			out = append(out, e.vm.ToValue(pair))
		}
		return out
	})
	return o
}

// newFormDataFromForm collects a form's successful controls, which is what
// `new FormData(form)` is for.
func (e *jsEnv) newFormDataFromForm(form *goja.Object) *goja.Object {
	fd := &formData{}
	if node := e.nodeArg(form); node != nil {
		e.collectFormEntries(node, fd)
	}
	return e.newFormDataObject(fd)
}

func formDataOf(v goja.Value) *formData {
	o, ok := v.(*goja.Object)
	if !ok {
		return nil
	}
	fd, _ := marked[*formData](o, formDataMark)
	return fd
}

// encode renders the entries as a request body. A form holding a file is
// multipart; anything else is urlencoded, matching FormData's own rules.
func (fd *formData) encode() (body []byte, contentType string) {
	multipart := false
	for _, x := range fd.entries {
		if x.blob != nil {
			multipart = true
			break
		}
	}
	if !multipart {
		var sb strings.Builder
		for i, x := range fd.entries {
			if i > 0 {
				sb.WriteByte('&')
			}
			sb.WriteString(urlEncode(x.name) + "=" + urlEncode(x.value))
		}
		return []byte(sb.String()), "application/x-www-form-urlencoded;charset=UTF-8"
	}
	var b [12]byte
	_, _ = rand.Read(b[:])
	boundary := "----gocurlffi" + hex.EncodeToString(b[:])
	var sb strings.Builder
	for _, x := range fd.entries {
		sb.WriteString("--" + boundary + "\r\n")
		if x.blob != nil {
			name := x.blob.name
			if name == "" {
				name = "blob"
			}
			typ := x.blob.typ
			if typ == "" {
				typ = "application/octet-stream"
			}
			sb.WriteString(`Content-Disposition: form-data; name="` + x.name + `"; filename="` + name + "\"\r\n")
			sb.WriteString("Content-Type: " + typ + "\r\n\r\n")
			sb.Write(x.blob.data)
			sb.WriteString("\r\n")
			continue
		}
		sb.WriteString(`Content-Disposition: form-data; name="` + x.name + "\"\r\n\r\n")
		sb.WriteString(x.value + "\r\n")
	}
	sb.WriteString("--" + boundary + "--\r\n")
	return []byte(sb.String()), "multipart/form-data; boundary=" + boundary
}

func urlEncode(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '*' {
			sb.WriteByte(c)
			continue
		}
		if c == ' ' {
			sb.WriteByte('+')
			continue
		}
		const hexDigits = "0123456789ABCDEF"
		sb.WriteByte('%')
		sb.WriteByte(hexDigits[c>>4])
		sb.WriteByte(hexDigits[c&0xf])
	}
	return sb.String()
}

// --- iterators -------------------------------------------------------------

// setMethodIterator installs `name` as a function returning a fresh iterator,
// as well as the [Symbol.iterator] slot.
func (e *jsEnv) setMethodIterator(o *goja.Object, name string, items func() []goja.Value) {
	mk := func(goja.FunctionCall) goja.Value {
		return e.vm.ToValue(e.makeIterator(items()))
	}
	_ = o.Set(name, mk)
}

// setEntryIterator installs only [Symbol.iterator].
func (e *jsEnv) setEntryIterator(o *goja.Object, items func() []goja.Value) {
	_ = o.SetSymbol(goja.SymIterator, func(goja.FunctionCall) goja.Value {
		return e.vm.ToValue(e.makeIterator(items()))
	})
}

func (e *jsEnv) makeIterator(items []goja.Value) *goja.Object {
	it := e.vm.NewObject()
	i := 0
	_ = it.SetSymbol(goja.SymIterator, func(goja.FunctionCall) goja.Value {
		return e.vm.ToValue(it)
	})
	_ = it.Set("next", func(goja.FunctionCall) goja.Value {
		r := e.vm.NewObject()
		if i < len(items) {
			_ = r.Set("value", items[i])
			_ = r.Set("done", false)
			i++
		} else {
			_ = r.Set("value", goja.Undefined())
			_ = r.Set("done", true)
		}
		return e.vm.ToValue(r)
	})
	return it
}

// --- Request / Response ----------------------------------------------------

// jsResponseData is whatever a Request or Response carries as its body.
type jsResponseData struct {
	status     int
	statusText string
	url        string
	redirected bool
	typ        string
	headers    *headersData
	body       []byte
	stream     *jsReadableStream
	used       bool
}

func (e *jsEnv) newResponseObject(d *jsResponseData) *goja.Object {
	o := e.vm.NewObject()
	e.mark(o, responseMark, d)
	e.tagHost(o, "Response")
	if d.typ == "" {
		d.typ = "default"
	}
	_ = o.Set("status", d.status)
	_ = o.Set("statusText", d.statusText)
	_ = o.Set("ok", d.status >= 200 && d.status < 300)
	_ = o.Set("url", d.url)
	_ = o.Set("redirected", d.redirected)
	_ = o.Set("type", d.typ)
	_ = o.Set("headers", e.newHeadersObject(d.headers))

	stream := d.stream
	if stream == nil {
		stream = e.newReadableStream()
		if len(d.body) > 0 {
			stream.enqueue(d.body)
		}
		stream.close()
		d.stream = stream
	}
	_ = o.Set("body", e.vm.ToValue(stream.obj))
	_ = o.Set("bodyUsed", false)

	consume := func() ([]byte, goja.Value) {
		if d.used {
			return nil, e.vm.NewTypeError("body stream already read")
		}
		d.used = true
		_ = o.Set("bodyUsed", true)
		return d.body, nil
	}
	_ = o.Set("text", func(goja.FunctionCall) goja.Value {
		b, errv := consume()
		if errv != nil {
			return e.rejectedPromiseValue(errv)
		}
		return e.resolvedPromise(e.vm.ToValue(string(b)))
	})
	_ = o.Set("json", func(goja.FunctionCall) goja.Value {
		b, errv := consume()
		if errv != nil {
			return e.rejectedPromiseValue(errv)
		}
		var parsed any
		if err := json.Unmarshal(b, &parsed); err != nil {
			return e.rejectedPromise(err)
		}
		return e.resolvedPromise(e.vm.ToValue(parsed))
	})
	_ = o.Set("arrayBuffer", func(goja.FunctionCall) goja.Value {
		b, errv := consume()
		if errv != nil {
			return e.rejectedPromiseValue(errv)
		}
		return e.resolvedPromise(e.vm.ToValue(e.vm.NewArrayBuffer(append([]byte(nil), b...))))
	})
	_ = o.Set("bytes", func(goja.FunctionCall) goja.Value {
		b, errv := consume()
		if errv != nil {
			return e.rejectedPromiseValue(errv)
		}
		return e.resolvedPromise(e.newUint8Array(append([]byte(nil), b...)))
	})
	_ = o.Set("blob", func(goja.FunctionCall) goja.Value {
		b, errv := consume()
		if errv != nil {
			return e.rejectedPromiseValue(errv)
		}
		typ := ""
		if v, ok := d.headers.get("content-type"); ok {
			typ = v
		}
		return e.resolvedPromise(e.vm.ToValue(e.newBlobObject(&jsBlobData{data: b, typ: typ})))
	})
	_ = o.Set("clone", func(goja.FunctionCall) goja.Value {
		c := *d
		c.headers = newHeadersData()
		for i, n := range d.headers.names {
			for _, v := range d.headers.values[i] {
				c.headers.append(n, v)
			}
		}
		c.stream = nil
		c.used = false
		return e.vm.ToValue(e.newResponseObject(&c))
	})
	return o
}

func (e *jsEnv) rejectedPromiseValue(v goja.Value) goja.Value {
	p, _, reject := e.vm.NewPromise()
	_ = reject(v)
	return e.vm.ToValue(p)
}

// newRequestObject builds a Request. Its body is buffered, so it is also
// usable as a Response-shaped source for fetch(input) to clone.
func (e *jsEnv) newRequestObject(input string, init *goja.Object) *goja.Object {
	d := &jsResponseData{
		status:  200,
		typ:     "default",
		headers: newHeadersData(),
		url:     resolveURL(e.page.baseURL(), input),
	}
	method := "GET"
	if init != nil {
		if m := init.Get("method"); m != nil && !goja.IsUndefined(m) {
			method = strings.ToUpper(argString(m))
		}
		d.headers = e.headersFromInit(init.Get("headers"))
	}
	o := e.vm.NewObject()
	e.mark(o, requestMark, d)
	e.tagHost(o, "Request")
	_ = o.Set("url", d.url)
	_ = o.Set("method", method)
	_ = o.Set("headers", e.newHeadersObject(d.headers))
	_ = o.Set("mode", "cors")
	_ = o.Set("credentials", "same-origin")
	_ = o.Set("cache", "default")
	_ = o.Set("redirect", "follow")
	_ = o.Set("referrer", "about:client")
	_ = o.Set("referrerPolicy", "")
	_ = o.Set("integrity", "")
	_ = o.Set("keepalive", false)
	_ = o.Set("destination", "")
	// The Request shares the caller's signal, so aborting the controller is
	// visible through request.signal.
	if init != nil {
		if sig := init.Get("signal"); sig != nil && !goja.IsUndefined(sig) && !goja.IsNull(sig) {
			_ = o.Set("signal", sig)
		} else {
			_ = o.Set("signal", e.newAbortSignalState().obj)
		}
	} else {
		_ = o.Set("signal", e.newAbortSignalState().obj)
	}

	var body []byte
	if init != nil {
		body, _ = e.bodyToBytes(init.Get("body"), d.headers)
	}
	d.body = body
	stream := e.newReadableStream()
	if len(body) > 0 {
		stream.enqueue(body)
	}
	stream.close()
	d.stream = stream
	_ = o.Set("body", e.vm.ToValue(stream.obj))
	_ = o.Set("bodyUsed", false)
	consume := func() ([]byte, goja.Value) {
		if d.used {
			return nil, e.vm.NewTypeError("body stream already read")
		}
		d.used = true
		_ = o.Set("bodyUsed", true)
		return d.body, nil
	}
	_ = o.Set("text", func(goja.FunctionCall) goja.Value {
		b, errv := consume()
		if errv != nil {
			return e.rejectedPromiseValue(errv)
		}
		return e.resolvedPromise(e.vm.ToValue(string(b)))
	})
	_ = o.Set("json", func(goja.FunctionCall) goja.Value {
		b, errv := consume()
		if errv != nil {
			return e.rejectedPromiseValue(errv)
		}
		var parsed any
		if err := json.Unmarshal(b, &parsed); err != nil {
			return e.rejectedPromise(err)
		}
		return e.resolvedPromise(e.vm.ToValue(parsed))
	})
	_ = o.Set("arrayBuffer", func(goja.FunctionCall) goja.Value {
		b, errv := consume()
		if errv != nil {
			return e.rejectedPromiseValue(errv)
		}
		return e.resolvedPromise(e.vm.ToValue(e.vm.NewArrayBuffer(append([]byte(nil), b...))))
	})
	_ = o.Set("clone", func(goja.FunctionCall) goja.Value {
		c := *d
		c.used = false
		c.stream = nil
		return e.vm.ToValue(e.newResponseObject(&c))
	})
	return o
}

// bodyToBytes accepts every BodyInit shape fetch allows. A FormData or
// URLSearchParams also dictates the request's content type.
func (e *jsEnv) bodyToBytes(v goja.Value, h *headersData) ([]byte, string) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil, ""
	}
	if fd := formDataOf(v); fd != nil {
		body, ct := fd.encode()
		if h != nil && h.index("content-type") < 0 {
			h.set("content-type", ct)
		}
		return body, ct
	}
	if b := blobOf(v); b != nil {
		if h != nil && b.typ != "" && h.index("content-type") < 0 {
			h.set("content-type", b.typ)
		}
		return b.data, b.typ
	}
	if s, ok := v.Export().(string); ok {
		return []byte(s), "text/plain;charset=UTF-8"
	}
	return toBytes(v), ""
}

// jsRequestData mirrors what fetch() needs to read back out of a Request.
func requestURL(v goja.Value) string {
	o, ok := v.(*goja.Object)
	if !ok {
		return ""
	}
	if d, ok := marked[*jsResponseData](o, requestMark); ok {
		return d.url
	}
	if d, ok := marked[*jsResponseData](o, responseMark); ok {
		return d.url
	}
	if u := o.Get("url"); u != nil && !goja.IsUndefined(u) {
		return u.String()
	}
	return ""
}

func requestMethod(v goja.Value) string {
	o, ok := v.(*goja.Object)
	if !ok {
		return ""
	}
	if m := o.Get("method"); m != nil && !goja.IsUndefined(m) {
		return strings.ToUpper(m.String())
	}
	return ""
}

func requestHeaders(v goja.Value) *headersData {
	o, ok := v.(*goja.Object)
	if !ok {
		return nil
	}
	if d, ok := marked[*jsResponseData](o, requestMark); ok {
		return d.headers
	}
	return headersOf(o.Get("headers"))
}

// --- Streams ---------------------------------------------------------------

// jsReadableStream is a buffered byte stream. Because every producer in this
// browser is synchronous, a read either has data waiting or the stream is
// closed; waiters is only needed for a reader that arrives before the first
// enqueue, which a page's own `new ReadableStream({start})` can do.
type jsReadableStream struct {
	e       *jsEnv
	obj     *goja.Object
	chunks  [][]byte
	closed  bool
	err     goja.Value
	pending []func(interface{}) error
	locked  bool
}

func (e *jsEnv) newReadableStream() *jsReadableStream {
	s := &jsReadableStream{e: e}
	o := e.vm.NewObject()
	e.tagHost(o, "ReadableStream")
	s.obj = o
	e.mark(o, streamMark, s)
	_ = o.Set("locked", false)
	_ = o.Set("getReader", func(goja.FunctionCall) goja.Value {
		s.locked = true
		_ = o.Set("locked", true)
		return e.vm.ToValue(s.newReader())
	})
	_ = o.Set("cancel", func(goja.FunctionCall) goja.Value {
		s.chunks = nil
		s.closed = true
		s.resolveWaiters()
		return e.resolvedPromise(goja.Undefined())
	})
	_ = o.Set("tee", func(goja.FunctionCall) goja.Value {
		a := e.newReadableStream()
		b := e.newReadableStream()
		for _, c := range s.chunks {
			a.enqueue(c)
			b.enqueue(c)
		}
		if s.closed {
			a.close()
			b.close()
		}
		arr := e.vm.NewArray()
		_ = arr.Set("0", a.obj)
		_ = arr.Set("1", b.obj)
		return e.vm.ToValue(arr)
	})
	_ = o.Set("pipeTo", func(call goja.FunctionCall) goja.Value {
		dest, ok := call.Argument(0).(*goja.Object)
		if !ok {
			return e.rejectedPromiseValue(e.vm.NewTypeError("pipeTo: not a WritableStream"))
		}
		writer := dest.Get("getWriter")
		wfn, ok := goja.AssertFunction(writer)
		if !ok {
			return e.rejectedPromiseValue(e.vm.NewTypeError("pipeTo: not writable"))
		}
		w, err := wfn(dest)
		if err != nil {
			return e.rejectedPromiseValue(e.vm.NewGoError(err))
		}
		wo, _ := w.(*goja.Object)
		for _, c := range s.chunks {
			if write := wo.Get("write"); write != nil {
				if fn, ok := goja.AssertFunction(write); ok {
					_, _ = fn(wo, e.newUint8Array(c))
				}
			}
		}
		if closeFn := wo.Get("close"); closeFn != nil {
			if fn, ok := goja.AssertFunction(closeFn); ok {
				_, _ = fn(wo)
			}
		}
		return e.resolvedPromise(goja.Undefined())
	})
	_ = o.SetSymbol(goja.SymIterator, func(goja.FunctionCall) goja.Value {
		it := e.vm.NewObject()
		idx := 0
		_ = it.Set("next", func(goja.FunctionCall) goja.Value {
			r := e.vm.NewObject()
			if idx < len(s.chunks) {
				_ = r.Set("value", e.newUint8Array(s.chunks[idx]))
				_ = r.Set("done", false)
				idx++
			} else {
				_ = r.Set("value", goja.Undefined())
				_ = r.Set("done", true)
			}
			p, resolve, _ := e.vm.NewPromise()
			_ = resolve(e.vm.ToValue(r))
			return e.vm.ToValue(p)
		})
		return e.vm.ToValue(it)
	})
	return s
}

func (s *jsReadableStream) newReader() *goja.Object {
	e := s.e
	o := e.vm.NewObject()
	e.tagObject(o, "ReadableStreamDefaultReader")
	_ = o.Set("read", func(goja.FunctionCall) goja.Value {
		r := e.vm.NewObject()
		switch {
		case s.err != nil:
			p, _, reject := e.vm.NewPromise()
			_ = reject(s.err)
			return e.vm.ToValue(p)
		case len(s.chunks) > 0:
			c := s.chunks[0]
			s.chunks = s.chunks[1:]
			_ = r.Set("value", e.newUint8Array(c))
			_ = r.Set("done", false)
		case s.closed:
			_ = r.Set("value", goja.Undefined())
			_ = r.Set("done", true)
		default:
			// Nothing yet: hand back a promise the next enqueue resolves.
			p, resolve, _ := e.vm.NewPromise()
			s.pending = append(s.pending, resolve)
			return e.vm.ToValue(p)
		}
		p, resolve, _ := e.vm.NewPromise()
		_ = resolve(e.vm.ToValue(r))
		return e.vm.ToValue(p)
	})
	_ = o.Set("releaseLock", func(goja.FunctionCall) goja.Value {
		s.locked = false
		_ = s.obj.Set("locked", false)
		return goja.Undefined()
	})
	_ = o.Set("cancel", func(goja.FunctionCall) goja.Value {
		s.chunks = nil
		s.closed = true
		s.resolveWaiters()
		return e.resolvedPromise(goja.Undefined())
	})
	closedPromise, resolveClosed, _ := e.vm.NewPromise()
	if s.closed {
		_ = resolveClosed(goja.Undefined())
	}
	_ = o.Set("closed", e.vm.ToValue(closedPromise))
	return o
}

func (s *jsReadableStream) resolveWaiters() {
	for _, resolve := range s.pending {
		r := s.e.vm.NewObject()
		if len(s.chunks) > 0 {
			c := s.chunks[0]
			s.chunks = s.chunks[1:]
			_ = r.Set("value", s.e.newUint8Array(c))
			_ = r.Set("done", false)
		} else {
			_ = r.Set("value", goja.Undefined())
			_ = r.Set("done", true)
		}
		_ = resolve(s.e.vm.ToValue(r))
	}
	s.pending = nil
}

func (s *jsReadableStream) enqueue(b []byte) {
	if s.closed {
		return
	}
	s.chunks = append(s.chunks, append([]byte(nil), b...))
	if len(s.pending) > 0 {
		s.resolveWaiters()
	}
}

func (s *jsReadableStream) close() {
	s.closed = true
	s.resolveWaiters()
}

// newReadableStreamCtor implements `new ReadableStream(underlyingSource)`.
func (e *jsEnv) newReadableStreamCtor(src goja.Value) *goja.Object {
	s := e.newReadableStream()
	ctrl := e.vm.NewObject()
	_ = ctrl.Set("enqueue", func(call goja.FunctionCall) goja.Value {
		s.enqueue(toBytes(call.Argument(0)))
		return goja.Undefined()
	})
	_ = ctrl.Set("close", func(goja.FunctionCall) goja.Value {
		s.close()
		return goja.Undefined()
	})
	_ = ctrl.Set("error", func(call goja.FunctionCall) goja.Value {
		s.err = call.Argument(0)
		s.closed = true
		s.resolveWaiters()
		return goja.Undefined()
	})
	_ = ctrl.Set("desiredSize", 1)
	if so, ok := src.(*goja.Object); ok {
		if start := so.Get("start"); start != nil {
			if fn, ok := goja.AssertFunction(start); ok {
				_, _ = fn(so, ctrl)
			}
		}
	}
	return s.obj
}

func (e *jsEnv) newWritableStreamCtor(src goja.Value) *goja.Object {
	o := e.vm.NewObject()
	e.tagHost(o, "WritableStream")
	var writeFn, closeFn, abortFn goja.Callable
	if so, ok := src.(*goja.Object); ok {
		if v := so.Get("write"); v != nil {
			writeFn, _ = goja.AssertFunction(v)
		}
		if v := so.Get("close"); v != nil {
			closeFn, _ = goja.AssertFunction(v)
		}
		if v := so.Get("abort"); v != nil {
			abortFn, _ = goja.AssertFunction(v)
		}
	}
	_ = o.Set("locked", false)
	_ = o.Set("getWriter", func(goja.FunctionCall) goja.Value {
		_ = o.Set("locked", true)
		w := e.vm.NewObject()
		_ = w.Set("write", func(call goja.FunctionCall) goja.Value {
			if writeFn != nil {
				_, _ = writeFn(goja.Undefined(), call.Argument(0), e.vm.NewObject())
			}
			return e.resolvedPromise(goja.Undefined())
		})
		_ = w.Set("close", func(goja.FunctionCall) goja.Value {
			if closeFn != nil {
				_, _ = closeFn(goja.Undefined())
			}
			return e.resolvedPromise(goja.Undefined())
		})
		_ = w.Set("abort", func(call goja.FunctionCall) goja.Value {
			if abortFn != nil {
				_, _ = abortFn(goja.Undefined(), call.Argument(0))
			}
			return e.resolvedPromise(goja.Undefined())
		})
		_ = w.Set("releaseLock", func(goja.FunctionCall) goja.Value {
			_ = o.Set("locked", false)
			return goja.Undefined()
		})
		_ = w.Set("ready", e.resolvedPromise(goja.Undefined()))
		_ = w.Set("closed", e.resolvedPromise(goja.Undefined()))
		return e.vm.ToValue(w)
	})
	_ = o.Set("close", func(goja.FunctionCall) goja.Value {
		if closeFn != nil {
			_, _ = closeFn(goja.Undefined())
		}
		return e.resolvedPromise(goja.Undefined())
	})
	_ = o.Set("abort", func(call goja.FunctionCall) goja.Value {
		if abortFn != nil {
			_, _ = abortFn(goja.Undefined(), call.Argument(0))
		}
		return e.resolvedPromise(goja.Undefined())
	})
	return o
}

func (e *jsEnv) newTransformStreamCtor(src goja.Value) *goja.Object {
	readable := e.newReadableStream()
	var transformFn, flushFn goja.Callable
	if so, ok := src.(*goja.Object); ok {
		if v := so.Get("transform"); v != nil {
			transformFn, _ = goja.AssertFunction(v)
		}
		if v := so.Get("flush"); v != nil {
			flushFn, _ = goja.AssertFunction(v)
		}
	}
	writable := e.newWritableStreamCtor(e.vm.ToValue(e.vm.NewObject()))
	// Rebuild the writable sink so writes drive the transform.
	wo := writable
	writer := e.vm.NewObject()
	_ = writer.Set("write", func(call goja.FunctionCall) goja.Value {
		if transformFn != nil {
			ctrl := e.vm.NewObject()
			_ = ctrl.Set("enqueue", func(c goja.FunctionCall) goja.Value {
				readable.enqueue(toBytes(c.Argument(0)))
				return goja.Undefined()
			})
			_ = ctrl.Set("terminate", func(goja.FunctionCall) goja.Value {
				readable.close()
				return goja.Undefined()
			})
			_ = ctrl.Set("error", func(c goja.FunctionCall) goja.Value {
				readable.err = c.Argument(0)
				readable.closed = true
				return goja.Undefined()
			})
			_, _ = transformFn(goja.Undefined(), call.Argument(0), ctrl)
		} else {
			readable.enqueue(toBytes(call.Argument(0)))
		}
		return e.resolvedPromise(goja.Undefined())
	})
	_ = writer.Set("close", func(goja.FunctionCall) goja.Value {
		if flushFn != nil {
			ctrl := e.vm.NewObject()
			_ = ctrl.Set("enqueue", func(c goja.FunctionCall) goja.Value {
				readable.enqueue(toBytes(c.Argument(0)))
				return goja.Undefined()
			})
			_, _ = flushFn(goja.Undefined(), ctrl)
		}
		readable.close()
		return e.resolvedPromise(goja.Undefined())
	})
	_ = writer.Set("abort", func(goja.FunctionCall) goja.Value {
		readable.close()
		return e.resolvedPromise(goja.Undefined())
	})
	_ = writer.Set("releaseLock", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = writer.Set("ready", e.resolvedPromise(goja.Undefined()))
	_ = writer.Set("closed", e.resolvedPromise(goja.Undefined()))
	_ = wo.Set("getWriter", func(goja.FunctionCall) goja.Value { return e.vm.ToValue(writer) })
	_ = wo.Set("locked", true)

	out := e.vm.NewObject()
	e.tagHost(out, "TransformStream")
	_ = out.Set("readable", readable.obj)
	_ = out.Set("writable", wo)
	return out
}

// --- registration ----------------------------------------------------------

func (e *jsEnv) setupBodies() {
	rt := e.vm
	_ = rt.Set("Blob", func(call goja.ConstructorCall) *goja.Object {
		opts, _ := call.Argument(1).(*goja.Object)
		o := e.newBlob(call.Argument(0), opts)
		_ = o.DefineDataProperty("constructor", call.This, goja.FLAG_TRUE, goja.FLAG_FALSE, goja.FLAG_TRUE)
		return o
	})
	_ = rt.Set("File", func(call goja.ConstructorCall) *goja.Object {
		opts, _ := call.Argument(2).(*goja.Object)
		return e.newFile(call.Argument(0), argString(call.Argument(1)), opts)
	})
	// File extends Blob, so a File has to answer instanceof Blob too.
	e.linkPrototype("File", "Blob")
	_ = rt.Set("FileReader", func(goja.ConstructorCall) *goja.Object {
		return e.newFileReader()
	})
	_ = rt.Set("Headers", func(call goja.ConstructorCall) *goja.Object {
		return e.newHeadersObject(e.headersFromInit(call.Argument(0)))
	})
	_ = rt.Set("FormData", func(call goja.ConstructorCall) *goja.Object {
		if form, ok := call.Argument(0).(*goja.Object); ok && e.nodeArg(form) != nil {
			return e.newFormDataFromForm(form)
		}
		return e.newFormDataObject(&formData{})
	})
	_ = rt.Set("Request", func(call goja.ConstructorCall) *goja.Object {
		init, _ := call.Argument(1).(*goja.Object)
		return e.newRequestObject(argString(call.Argument(0)), init)
	})
	responseCtor := func(call goja.ConstructorCall) *goja.Object {
		init, _ := call.Argument(1).(*goja.Object)
		d := &jsResponseData{status: 200, statusText: "", typ: "default", headers: newHeadersData()}
		if init != nil {
			if s := init.Get("status"); s != nil && !goja.IsUndefined(s) {
				d.status = int(s.ToInteger())
			}
			if s := init.Get("statusText"); s != nil && !goja.IsUndefined(s) {
				d.statusText = argString(s)
			}
			d.headers = e.headersFromInit(init.Get("headers"))
		}
		d.body, _ = e.bodyToBytes(call.Argument(0), d.headers)
		return e.newResponseObject(d)
	}
	_ = rt.Set("Response", responseCtor)
	// Statics live on the global constructor itself.
	if ro, ok := rt.Get("Response").(*goja.Object); ok {
		_ = ro.Set("json", func(call goja.FunctionCall) goja.Value {
			init, _ := call.Argument(1).(*goja.Object)
			d := &jsResponseData{status: 200, typ: "default", headers: newHeadersData()}
			if init != nil {
				d.headers = e.headersFromInit(init.Get("headers"))
			}
			b, err := json.Marshal(call.Argument(0).Export())
			if err != nil {
				return e.rejectedPromise(err)
			}
			d.body = b
			d.headers.set("content-type", "application/json")
			return e.resolvedPromise(e.vm.ToValue(e.newResponseObject(d)))
		})
		_ = ro.Set("error", func(goja.ConstructorCall) *goja.Object {
			return e.newResponseObject(&jsResponseData{status: 0, typ: "error", headers: newHeadersData()})
		})
		_ = ro.Set("redirect", func(call goja.FunctionCall) goja.Value {
			status := 302
			if s := call.Argument(1); s != nil && !goja.IsUndefined(s) {
				status = int(s.ToInteger())
			}
			d := &jsResponseData{status: status, typ: "default", headers: newHeadersData(), url: argString(call.Argument(0))}
			d.headers.set("location", argString(call.Argument(0)))
			return e.vm.ToValue(e.newResponseObject(d))
		})
	}
	_ = rt.Set("ReadableStream", func(call goja.ConstructorCall) *goja.Object {
		return e.newReadableStreamCtor(call.Argument(0))
	})
	_ = rt.Set("WritableStream", func(call goja.ConstructorCall) *goja.Object {
		return e.newWritableStreamCtor(call.Argument(0))
	})
	_ = rt.Set("TransformStream", func(call goja.ConstructorCall) *goja.Object {
		return e.newTransformStreamCtor(call.Argument(0))
	})
	_ = rt.Set("ByteLengthQueuingStrategy", func(call goja.ConstructorCall) *goja.Object {
		o := e.vm.NewObject()
		_ = o.Set("highWaterMark", call.Argument(0).ToInteger())
		_ = o.Set("size", func(c goja.FunctionCall) goja.Value { return e.vm.ToValue(len(toBytes(c.Argument(0)))) })
		return o
	})
	_ = rt.Set("CountQueuingStrategy", func(call goja.ConstructorCall) *goja.Object {
		o := e.vm.NewObject()
		_ = o.Set("highWaterMark", call.Argument(0).ToInteger())
		_ = o.Set("size", func(goja.FunctionCall) goja.Value { return e.vm.ToValue(1) })
		return o
	})
}

// collectFormEntries walks a form subtree gathering successful controls.
func (e *jsEnv) collectFormEntries(n *html.Node, fd *formData) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			switch strings.ToLower(c.Data) {
			case "input":
				typ := strings.ToLower(attrOf(c, "type"))
				name := attrOf(c, "name")
				if name != "" && typ != "submit" && typ != "button" && typ != "file" && typ != "reset" && typ != "image" {
					if (typ == "checkbox" || typ == "radio") && !hasAttr(c, "checked") {
						break
					}
					fd.entries = append(fd.entries, formEntry{name: name, value: attrOf(c, "value")})
				}
			case "textarea":
				if name := attrOf(c, "name"); name != "" {
					fd.entries = append(fd.entries, formEntry{name: name, value: textContent(c)})
				}
			case "select":
				if name := attrOf(c, "name"); name != "" {
					fd.entries = append(fd.entries, formEntry{name: name, value: selectedValue(c)})
				}
			}
		}
		e.collectFormEntries(c, fd)
	}
}

func selectedValue(sel *html.Node) string {
	var walk func(n *html.Node) string
	walk = func(n *html.Node) string {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && strings.EqualFold(c.Data, "option") && hasAttr(c, "selected") {
				if v := attrOf(c, "value"); v != "" {
					return v
				}
				return textContent(c)
			}
			if v := walk(c); v != "" {
				return v
			}
		}
		return ""
	}
	return walk(sel)
}

// base64Std encodes without pulling in encoding/base64 twice.
func base64Std(b []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var sb strings.Builder
	for i := 0; i < len(b); i += 3 {
		var n uint32
		rem := len(b) - i
		n = uint32(b[i]) << 16
		if rem > 1 {
			n |= uint32(b[i+1]) << 8
		}
		if rem > 2 {
			n |= uint32(b[i+2])
		}
		sb.WriteByte(alphabet[(n>>18)&0x3f])
		sb.WriteByte(alphabet[(n>>12)&0x3f])
		if rem > 1 {
			sb.WriteByte(alphabet[(n>>6)&0x3f])
		} else {
			sb.WriteByte('=')
		}
		if rem > 2 {
			sb.WriteByte(alphabet[n&0x3f])
		} else {
			sb.WriteByte('=')
		}
	}
	return sb.String()
}
