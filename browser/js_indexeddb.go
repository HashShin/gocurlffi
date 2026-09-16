package browser

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/dop251/goja"
)

// A minimal in-memory IndexedDB. Pages use it to cache data client-side; the
// API is promise- and callback-shaped, so the requests resolve on a later timer
// turn (see scheduleDelay) rather than synchronously, which is what lets a
// script assign onsuccess after the call returns. It models the parts pages
// actually use: open with an upgrade, object stores with a keyPath, put/add/
// get/getAll/delete/clear/count, simple indexes, and transactions.

type idbDatabase struct {
	name    string
	version int
	stores  map[string]*idbStore
	order   []string
}

type idbStore struct {
	name       string
	keyPath    string
	autoInc    bool
	recs       map[string]any
	order      []string
	indexes    map[string]string
	indexOrder []string
}

// installIndexedDB exposes the indexedDB global.
func (e *jsEnv) installIndexedDB(rt *goja.Runtime) {
	if e.idb == nil {
		e.idb = map[string]*idbDatabase{}
	}
	idb := e.vm.NewObject()
	_ = idb.Set("open", func(call goja.FunctionCall) goja.Value {
		return e.idbOpen(argString(call.Argument(0)), int(call.Argument(1).ToInteger()))
	})
	_ = idb.Set("deleteDatabase", func(call goja.FunctionCall) goja.Value {
		name := argString(call.Argument(0))
		req := e.idbRequest()
		e.schedule(func() {
			delete(e.idb, name)
			req.resolve(nil)
		})
		return req.obj
	})
	_ = idb.Set("databases", func(call goja.FunctionCall) goja.Value {
		req := e.idbRequest()
		e.schedule(func() {
			var out []any
			for name, db := range e.idb {
				out = append(out, map[string]any{"name": name, "version": db.version})
			}
			req.resolve(out)
		})
		return req.obj
	})
	_ = rt.Set("indexedDB", idb)
	_ = rt.Set("IDBKeyRange", e.idbKeyRange())
}

func (e *jsEnv) idbKeyRange() *goja.Object {
	o := e.vm.NewObject()
	mk := func(lo, hi any) goja.Value {
		r := e.vm.NewObject()
		_ = r.Set("lower", lo)
		_ = r.Set("upper", hi)
		return r
	}
	_ = o.Set("only", func(call goja.FunctionCall) goja.Value {
		return mk(call.Argument(0).Export(), call.Argument(0).Export())
	})
	_ = o.Set("lowerBound", func(call goja.FunctionCall) goja.Value { return mk(call.Argument(0).Export(), nil) })
	_ = o.Set("upperBound", func(call goja.FunctionCall) goja.Value { return mk(nil, call.Argument(0).Export()) })
	_ = o.Set("bound", func(call goja.FunctionCall) goja.Value {
		return mk(call.Argument(0).Export(), call.Argument(1).Export())
	})
	return o
}

func (e *jsEnv) idbOpen(name string, version int) goja.Value {
	req := e.idbRequest()
	db, existed := e.idb[name]
	if !existed {
		db = &idbDatabase{name: name, stores: map[string]*idbStore{}}
		e.idb[name] = db
	}
	upgrading := !existed || (version > 0 && version > db.version)
	if version <= 0 {
		version = db.version
		if version == 0 {
			version = 1
			upgrading = true
		}
	}
	e.schedule(func() {
		if upgrading {
			db.version = version
			req.setResult(db.obj(e))
			req.fire("upgradeneeded")
		}
		e.scheduleDelay(time.Millisecond, func() {
			req.setResult(db.obj(e))
			req.fire("success")
		})
	})
	return req.obj
}

// obj builds the IDBDatabase view for a raw database.
func (d *idbDatabase) obj(e *jsEnv) *goja.Object {
	o := e.vm.NewObject()
	_ = o.Set("name", d.name)
	_ = o.Set("version", d.version)
	_ = o.Set("objectStoreNames", e.idbNameListFunc(func() []string { return d.order }))
	_ = o.Set("createObjectStore", func(call goja.FunctionCall) goja.Value {
		name := argString(call.Argument(0))
		keyPath := ""
		autoInc := false
		if opts, ok := call.Argument(1).(*goja.Object); ok {
			keyPath = argString(opts.Get("keyPath"))
			autoInc = opts.Get("autoIncrement") != nil && opts.Get("autoIncrement").ToBoolean()
		}
		if _, ok := d.stores[name]; ok {
			panic(e.vm.NewTypeError("object store already exists: " + name))
		}
		d.stores[name] = &idbStore{name: name, keyPath: keyPath, autoInc: autoInc, recs: map[string]any{}, indexes: map[string]string{}}
		d.order = append(d.order, name)
		return d.stores[name].obj(e)
	})
	_ = o.Set("deleteObjectStore", func(call goja.FunctionCall) goja.Value {
		name := argString(call.Argument(0))
		delete(d.stores, name)
		for i, n := range d.order {
			if n == name {
				d.order = append(d.order[:i], d.order[i+1:]...)
				break
			}
		}
		return goja.Undefined()
	})
	_ = o.Set("transaction", func(call goja.FunctionCall) goja.Value {
		var names []string
		if arr, ok := call.Argument(0).Export().([]any); ok {
			for _, n := range arr {
				names = append(names, fmt.Sprint(n))
			}
		} else if s := argString(call.Argument(0)); s != "" {
			names = []string{s}
		}
		return e.idbTransaction(d, names, argString(call.Argument(1)))
	})
	_ = o.Set("close", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	return o
}

// idbNameListFunc builds a DOMStringList that reads the names live, so a store
// created after the list was obtained is still visible through it.
func (e *jsEnv) idbNameListFunc(names func() []string) *goja.Object {
	arr := e.vm.NewArray()
	_ = arr.Set("contains", func(call goja.FunctionCall) goja.Value {
		target := argString(call.Argument(0))
		for _, n := range names() {
			if n == target {
				return e.vm.ToValue(true)
			}
		}
		return e.vm.ToValue(false)
	})
	_ = arr.Set("item", func(call goja.FunctionCall) goja.Value {
		i := int(call.Argument(0).ToInteger())
		ns := names()
		if i < 0 || i >= len(ns) {
			return goja.Null()
		}
		return e.vm.ToValue(ns[i])
	})
	e.accessor(arr, "length", func(goja.FunctionCall) goja.Value {
		return e.vm.ToValue(len(names()))
	}, nil)
	return arr
}

func (e *jsEnv) idbNameList(names []string) *goja.Object {
	arr := e.vm.NewArray()
	for i, n := range names {
		_ = arr.Set(strconv.Itoa(i), n)
	}
	_ = arr.Set("length", len(names))
	_ = arr.Set("contains", func(call goja.FunctionCall) goja.Value {
		target := argString(call.Argument(0))
		for _, n := range names {
			if n == target {
				return e.vm.ToValue(true)
			}
		}
		return e.vm.ToValue(false)
	})
	_ = arr.Set("item", func(call goja.FunctionCall) goja.Value {
		i := int(call.Argument(0).ToInteger())
		if i < 0 || i >= len(names) {
			return goja.Null()
		}
		return e.vm.ToValue(names[i])
	})
	return arr
}

func (e *jsEnv) idbTransaction(db *idbDatabase, names []string, mode string) goja.Value {
	t := e.eventTarget()
	tx := t.obj
	if mode == "" {
		mode = "readonly"
	}
	_ = tx.Set("mode", mode)
	_ = tx.Set("db", goja.Null())
	_ = tx.Set("objectStore", func(call goja.FunctionCall) goja.Value {
		name := argString(call.Argument(0))
		st := db.stores[name]
		if st == nil {
			panic(e.vm.NewTypeError("no such object store: " + name))
		}
		return st.obj(e)
	})
	_ = tx.Set("objectStoreNames", e.idbNameList(names))
	_ = tx.Set("abort", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	// Completion fires after the requests a caller issues synchronously.
	e.scheduleDelay(2*time.Millisecond, func() { t.fire("complete") })
	return tx
}

// obj builds the IDBObjectStore view.
func (s *idbStore) obj(e *jsEnv) *goja.Object {
	o := e.vm.NewObject()
	_ = o.Set("name", s.name)
	_ = o.Set("keyPath", s.keyPath)
	_ = o.Set("indexNames", e.idbNameList(s.indexOrder))

	keyOf := func(args []goja.Value, value any) (string, bool) {
		if len(args) >= 2 && !goja.IsUndefined(args[1]) && !goja.IsNull(args[1]) {
			return keyString(args[1].Export()), true
		}
		if s.keyPath != "" {
			if m, ok := value.(map[string]any); ok {
				if v, ok := m[s.keyPath]; ok {
					return keyString(v), true
				}
			}
			if obj, ok := args[0].(*goja.Object); ok {
				if v := obj.Get(s.keyPath); v != nil && !goja.IsUndefined(v) {
					return keyString(v.Export()), true
				}
			}
		}
		if s.autoInc {
			return strconv.Itoa(len(s.order) + 1), true
		}
		return "", false
	}

	put := func(call goja.FunctionCall) goja.Value {
		req := e.idbRequest()
		val := call.Argument(0).Export()
		key, ok := keyOf(call.Arguments, val)
		e.schedule(func() {
			if !ok {
				req.fail("no key for value and no keyPath")
				return
			}
			if _, exists := s.recs[key]; !exists {
				s.order = append(s.order, key)
			}
			s.recs[key] = val
			req.setResult(key)
			req.fire("success")
		})
		return req.obj
	}
	_ = o.Set("put", put)
	_ = o.Set("add", put)

	_ = o.Set("get", func(call goja.FunctionCall) goja.Value {
		req := e.idbRequest()
		key := keyString(call.Argument(0).Export())
		e.schedule(func() {
			if v, ok := s.recs[key]; ok {
				req.resolve(v)
			} else {
				req.resolve(nil)
			}
		})
		return req.obj
	})
	_ = o.Set("getAll", func(call goja.FunctionCall) goja.Value {
		req := e.idbRequest()
		e.schedule(func() {
			out := []any{}
			for _, k := range s.order {
				out = append(out, s.recs[k])
			}
			req.resolve(out)
		})
		return req.obj
	})
	_ = o.Set("getAllKeys", func(call goja.FunctionCall) goja.Value {
		req := e.idbRequest()
		e.schedule(func() {
			out := []any{}
			for _, k := range s.order {
				out = append(out, k)
			}
			req.resolve(out)
		})
		return req.obj
	})
	_ = o.Set("count", func(goja.FunctionCall) goja.Value {
		req := e.idbRequest()
		e.schedule(func() { req.resolve(len(s.order)) })
		return req.obj
	})
	_ = o.Set("delete", func(call goja.FunctionCall) goja.Value {
		req := e.idbRequest()
		key := keyString(call.Argument(0).Export())
		e.schedule(func() {
			delete(s.recs, key)
			for i, k := range s.order {
				if k == key {
					s.order = append(s.order[:i], s.order[i+1:]...)
					break
				}
			}
			req.resolve(nil)
		})
		return req.obj
	})
	_ = o.Set("clear", func(goja.FunctionCall) goja.Value {
		req := e.idbRequest()
		e.schedule(func() {
			s.recs = map[string]any{}
			s.order = nil
			req.resolve(nil)
		})
		return req.obj
	})
	_ = o.Set("createIndex", func(call goja.FunctionCall) goja.Value {
		name := argString(call.Argument(0))
		keyPath := argString(call.Argument(1))
		s.indexes[name] = keyPath
		s.indexOrder = append(s.indexOrder, name)
		get := e.vm.NewObject()
		_ = get.Set("name", name)
		_ = get.Set("keyPath", keyPath)
		return get
	})
	_ = o.Set("index", func(call goja.FunctionCall) goja.Value {
		name := argString(call.Argument(0))
		idx := e.vm.NewObject()
		_ = idx.Set("name", name)
		_ = idx.Set("keyPath", s.indexes[name])
		_ = idx.Set("get", func(call goja.FunctionCall) goja.Value {
			req := e.idbRequest()
			key := keyString(call.Argument(0).Export())
			kp := s.indexes[name]
			e.schedule(func() {
				for _, k := range s.order {
					if m, ok := s.recs[k].(map[string]any); ok {
						if keyString(m[kp]) == key {
							req.resolve(s.recs[k])
							return
						}
					}
				}
				req.resolve(nil)
			})
			return req.obj
		})
		_ = idx.Set("getAll", func(goja.FunctionCall) goja.Value {
			req := e.idbRequest()
			kp := s.indexes[name]
			e.schedule(func() {
				out := []any{}
				for _, k := range s.order {
					if m, ok := s.recs[k].(map[string]any); ok && m[kp] != nil {
						out = append(out, s.recs[k])
					}
				}
				sort.SliceStable(out, func(i, j int) bool { return true })
				req.resolve(out)
			})
			return req.obj
		})
		return idx
	})
	_ = o.Set("openCursor", func(call goja.FunctionCall) goja.Value {
		req := e.idbRequest()
		keys := append([]string(nil), s.order...)
		idx := 0
		var advance func(first bool)
		advance = func(first bool) {
			e.schedule(func() {
				if idx >= len(keys) {
					req.resolve(nil)
					return
				}
				key := keys[idx]
				cur := e.vm.NewObject()
				_ = cur.Set("key", key)
				_ = cur.Set("primaryKey", key)
				_ = cur.Set("value", s.recs[key])
				_ = cur.Set("continue", func(goja.FunctionCall) goja.Value {
					idx++
					advance(false)
					return goja.Undefined()
				})
				req.setResult(cur)
				req.fire("success")
			})
		}
		advance(true)
		return req.obj
	})
	return o
}

// eventTarget is a small DOM-style event target for IDB requests,
// transactions and open requests: addEventListener plus the on* properties.
type eventTarget struct {
	e        *jsEnv
	obj      *goja.Object
	handlers map[string][]goja.Callable
	props    map[string]goja.Value
}

func (e *jsEnv) eventTarget() *eventTarget {
	t := &eventTarget{e: e, handlers: map[string][]goja.Callable{}, props: map[string]goja.Value{}}
	o := e.vm.NewObject()
	t.obj = o
	_ = o.Set("addEventListener", func(call goja.FunctionCall) goja.Value {
		typ := argString(call.Argument(0))
		if len(call.Arguments) > 1 {
			if fn, ok := goja.AssertFunction(call.Argument(1)); ok {
				t.handlers[typ] = append(t.handlers[typ], fn)
			}
		}
		return goja.Undefined()
	})
	_ = o.Set("removeEventListener", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("dispatchEvent", func(call goja.FunctionCall) goja.Value {
		t.fire(argString(call.Argument(0)))
		return e.vm.ToValue(true)
	})
	for _, name := range []string{"onsuccess", "onerror", "oncomplete", "onupgradeneeded", "onabort"} {
		name := name
		e.accessor(o, name,
			func(goja.FunctionCall) goja.Value {
				if v := t.props[name]; v != nil {
					return v
				}
				return goja.Null()
			},
			func(call goja.FunctionCall) goja.Value {
				t.props[name] = call.Argument(0)
				return goja.Undefined()
			})
	}
	return t
}

// fire delivers an event to the target's listeners and its on* handler.
func (t *eventTarget) fire(typ string) {
	ev := t.e.vm.NewObject()
	_ = ev.Set("type", typ)
	_ = ev.Set("target", t.obj)
	_ = ev.Set("currentTarget", t.obj)
	for _, h := range t.handlers[typ] {
		_, _ = h(t.obj, ev)
	}
	if v := t.props["on"+typ]; v != nil {
		if fn, ok := goja.AssertFunction(v); ok {
			_, _ = fn(t.obj, ev)
		}
	}
}

// idbRequest is an IDBRequest view: a result delivered on a later turn.
type idbRequest struct {
	e   *jsEnv
	et  *eventTarget
	obj *goja.Object
}

func (e *jsEnv) idbRequest() *idbRequest {
	et := e.eventTarget()
	_ = et.obj.Set("readyState", "pending")
	_ = et.obj.Set("result", goja.Undefined())
	_ = et.obj.Set("error", goja.Null())
	return &idbRequest{e: e, et: et, obj: et.obj}
}

func (r *idbRequest) setResult(v any) {
	_ = r.obj.Set("result", r.e.vm.ToValue(v))
	_ = r.obj.Set("readyState", "done")
}

func (r *idbRequest) resolve(v any) { r.setResult(v); r.et.fire("success") }

func (r *idbRequest) fail(msg string) {
	_ = r.obj.Set("error", r.e.vm.ToValue(msg))
	_ = r.obj.Set("readyState", "done")
	r.et.fire("error")
}

func (r *idbRequest) fire(typ string) { r.et.fire(typ) }

func keyString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprint(x)
	}
}
