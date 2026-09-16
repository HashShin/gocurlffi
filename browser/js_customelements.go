package browser

import (
	"strings"

	"github.com/dop251/goja"
	"golang.org/x/net/html"
)

// Custom elements. `customElements.define` used to record the constructor in a
// Go map and then never use it, so a component defined by the page was created
// as an inert element and its constructor, connectedCallback and
// attributeChangedCallback never ran. Here define() upgrades the elements that
// already exist, every later createElement/parse/insert upgrades too, and the
// lifecycle callbacks fire.

// customElementDefinition is one define() call.
type customElementDefinition struct {
	name       string
	ctor       goja.Value
	observed   map[string]bool
	observedAt []string
	// state is "undefined" until the definition exists, then "custom".
	// "failed" is unused: a constructor that throws is reported as an error but
	// the element is still marked custom, as in a browser.
}

// customElementState is attached to a host node once it has been upgraded.
type customElementState struct {
	def        *customElementDefinition
	instance   *goja.Object
	upgraded   bool
	connected  bool
	attributes map[string]string
}

type customElementRegistry struct {
	e      *jsEnv
	defs   map[string]*customElementDefinition
	states map[*html.Node]*customElementState
	// waiting holds whenDefined resolvers per name.
	waiting map[string][]func(interface{}) error
	obj     *goja.Object
}

func (e *jsEnv) customElementsObject() *goja.Object {
	o := e.vm.NewObject()
	e.tagObject(o, "CustomElementRegistry")
	reg := &customElementRegistry{
		e:       e,
		defs:    map[string]*customElementDefinition{},
		states:  map[*html.Node]*customElementState{},
		waiting: map[string][]func(interface{}) error{},
		obj:     o,
	}
	e.customElements = reg

	_ = o.Set("define", func(call goja.FunctionCall) goja.Value {
		name := argString(call.Argument(0))
		if name == "" {
			panic(e.vm.NewTypeError("define: a name is required"))
		}
		if _, exists := reg.defs[name]; exists {
			panic(e.vm.NewTypeError("define: custom element already defined: " + name))
		}
		ctor := call.Argument(1)
		if _, ok := goja.AssertFunction(ctor); !ok {
			panic(e.vm.NewTypeError("define: the second argument is not a constructor"))
		}
		def := &customElementDefinition{name: name, ctor: ctor, observed: map[string]bool{}}
		// observedAttributes is a static accessor on the constructor, which is
		// where the spec puts it. Reading it here also runs a page's getter
		// exactly once, as a browser does.
		if ctorObj, ok := ctor.(*goja.Object); ok {
			if oa := ctorObj.Get("observedAttributes"); oa != nil && !goja.IsUndefined(oa) && !goja.IsNull(oa) {
				def.readObserved(oa)
			}
		}
		if opts, ok := call.Argument(2).(*goja.Object); ok {
			if oa := opts.Get("observedAttributes"); oa != nil && !goja.IsUndefined(oa) {
				if arr, ok := oa.(*goja.Object); ok {
					for i := 0; i < int(arr.Get("length").ToInteger()); i++ {
						a := argString(arr.Get(intToString(i)))
						def.observed[strings.ToLower(a)] = true
						def.observedAt = append(def.observedAt, a)
					}
				}
			}
		}
		reg.defs[name] = def
		// Elements created before define() are upgraded, which is what makes
		// `<my-el>` written in the HTML work at all.
		reg.upgradeTree(e.page.doc, def)
		for _, resolve := range reg.waiting[name] {
			_ = resolve(ctor)
		}
		delete(reg.waiting, name)
		return goja.Undefined()
	})
	_ = o.Set("get", func(call goja.FunctionCall) goja.Value {
		if def, ok := reg.defs[argString(call.Argument(0))]; ok {
			return def.ctor
		}
		return goja.Undefined()
	})
	_ = o.Set("whenDefined", func(call goja.FunctionCall) goja.Value {
		name := argString(call.Argument(0))
		if def, ok := reg.defs[name]; ok {
			return e.resolvedPromise(def.ctor)
		}
		p, resolve, _ := e.vm.NewPromise()
		reg.waiting[name] = append(reg.waiting[name], resolve)
		return e.vm.ToValue(p)
	})
	_ = o.Set("upgrade", func(call goja.FunctionCall) goja.Value {
		if root := e.nodeArg(call.Argument(0)); root != nil {
			reg.upgradeTree(root, nil)
		}
		return goja.Undefined()
	})
	return o
}

// readObserved records the attribute names a definition watches.
func (d *customElementDefinition) readObserved(v goja.Value) {
	arr, ok := v.(*goja.Object)
	if !ok {
		return
	}
	n := 0
	if lv := arr.Get("length"); lv != nil && !goja.IsUndefined(lv) {
		n = int(lv.ToInteger())
	}
	for i := 0; i < n; i++ {
		name := argString(arr.Get(intToString(i)))
		if name == "" || d.observed[strings.ToLower(name)] {
			continue
		}
		d.observed[strings.ToLower(name)] = true
		d.observedAt = append(d.observedAt, name)
	}
}

// define reports the definition for a tag name, if the page registered one.
func (r *customElementRegistry) define(name string) *customElementDefinition {
	if r == nil {
		return nil
	}
	return r.defs[strings.ToLower(name)]
}

// upgradeTree upgrades every matching element at or below root. A nil def means
// every defined name.
func (r *customElementRegistry) upgradeTree(root *html.Node, only *customElementDefinition) {
	if r == nil || root == nil {
		return
	}
	if only == nil && len(r.defs) == 0 {
		return
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if def := r.define(n.Data); def != nil && (only == nil || def == only) {
				r.upgrade(n, def)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	// Shadow trees are not reachable through the light children.
	for _, sr := range r.e.page.ShadowRoots() {
		walk(sr.content)
	}
}

// upgrade constructs a custom element's instance once and wires its lifecycle.
func (r *customElementRegistry) upgrade(n *html.Node, def *customElementDefinition) {
	if st, ok := r.states[n]; ok && st.upgraded {
		return
	}
	e := r.e
	st := &customElementState{def: def, upgraded: true, attributes: map[string]string{}}
	r.states[n] = st

	// A class constructor cannot be invoked without `new`, so the instance has
	// to be produced by the engine. While it runs, `this` does not carry the
	// node symbol yet -- a constructor that calls this.attachShadow() before we
	// can stamp it would fail -- so thisNode falls back to the element being
	// upgraded (see jsEnv.constructing).
	e.constructing = n
	inst, err := e.vm.New(def.ctor)
	e.constructing = nil
	if err != nil {
		e.page.log("error", "custom element "+def.name+": "+err.Error())
		delete(r.states, n)
		return
	}

	if existing, ok := e.nodes[n]; ok && existing != nil {
		// The page may already hold this element (getElementById before
		// define). Keep that object -- its identity should not change -- and
		// give it the definition's prototype chain and own properties.
		_ = existing.SetPrototype(inst.Prototype())
		for _, k := range inst.Keys() {
			_ = existing.Set(k, inst.Get(k))
		}
		st.instance = existing
	} else {
		_ = inst.DefineDataPropertySymbol(e.nodeSym, e.vm.ToValue(n),
			goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
		e.tagObject(inst, elementInterfaceName(n))
		e.nodes[n] = inst
		st.instance = inst
	}

	// attributeChangedCallback for attributes already present. Microtask timing
	// is not modelled, so these are delivered now rather than queued.
	for _, name := range def.observedAt {
		if v, ok := getAttr(n, strings.ToLower(name)); ok {
			st.attributes[strings.ToLower(name)] = v
			r.callLifecycle(st, "attributeChangedCallback", name, "", v)
		}
	}
	// An element already in the document is connected straight away. A parent
	// is not enough: a tree built with innerHTML and never inserted is detached.
	if e.page.isConnected(n) {
		r.connect(n, st)
	}
}

// connect fires connectedCallback once per element.
func (r *customElementRegistry) connect(n *html.Node, st *customElementState) {
	if st.connected {
		return
	}
	st.connected = true
	r.callLifecycle(st, "connectedCallback")
}

// disconnect fires disconnectedCallback when a connected element leaves the tree.
func (r *customElementRegistry) disconnect(n *html.Node) {
	st, ok := r.states[n]
	if !ok || !st.connected {
		return
	}
	st.connected = false
	r.callLifecycle(st, "disconnectedCallback")
}

// attributeChanged fires attributeChangedCallback for an observed attribute.
func (r *customElementRegistry) attributeChanged(n *html.Node, name, old, value string) {
	st, ok := r.states[n]
	if !ok || st.def == nil || !st.def.observed[strings.ToLower(name)] {
		return
	}
	st.attributes[strings.ToLower(name)] = value
	r.callLifecycle(st, "attributeChangedCallback", name, old, value)
}

// noteConnected fires connectedCallback for a subtree that was just inserted,
// and noteDisconnected for a subtree that was just removed.
func (r *customElementRegistry) noteConnected(n *html.Node) {
	if r == nil || n == nil || len(r.states) == 0 {
		return
	}
	if st, ok := r.states[n]; ok {
		r.connect(n, st)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		r.noteConnected(c)
	}
}

func (r *customElementRegistry) noteDisconnected(n *html.Node) {
	if r == nil || n == nil || len(r.states) == 0 {
		return
	}
	if _, ok := r.states[n]; ok {
		r.disconnect(n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		r.noteDisconnected(c)
	}
}

// callLifecycle invokes a callback on the element if the page defined one.
func (r *customElementRegistry) callLifecycle(st *customElementState, name string, args ...any) {
	if st == nil {
		return
	}
	fn := st.instance.Get(name)
	if fn == nil || goja.IsUndefined(fn) || goja.IsNull(fn) {
		return
	}
	callable, ok := goja.AssertFunction(fn)
	if !ok {
		return
	}
	// The element is the receiver, not an argument: the callbacks are declared
	// attributeChangedCallback(name, oldValue, newValue) and goja would shift
	// every argument if the element were passed positionally.
	vals := make([]goja.Value, 0, len(args))
	for _, a := range args {
		vals = append(vals, r.e.vm.ToValue(a))
	}
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				r.e.page.log("error", "custom element "+st.def.name+"."+name+
					": "+formatJSValue(r.e.vm.ToValue(rec)))
			}
		}()
		_, _ = callable(st.instance, vals...)
	}()
}

// customElementAt reports the definition that observes an attribute, so the
// attribute path can skip the callback entirely for a normal element.
func (e *jsEnv) customElementAt(n *html.Node, attr string) *customElementDefinition {
	if e.customElements == nil || n == nil {
		return nil
	}
	st, ok := e.customElements.states[n]
	if !ok || st.def == nil || !st.def.observed[strings.ToLower(attr)] {
		return nil
	}
	return st.def
}

// registerCustomElement upgrades an element that was just created or parsed,
// and records it so a later define() can find it.
func (e *jsEnv) registerCustomElement(n *html.Node) {
	if e.customElements == nil || n == nil || n.Type != html.ElementNode {
		return
	}
	if len(e.customElements.defs) == 0 {
		return
	}
	if def := e.customElements.define(n.Data); def != nil {
		e.customElements.upgrade(n, def)
	}
}

// upgradeConnectedSubtree is called after a node is inserted into the document.
func (e *jsEnv) upgradeConnectedSubtree(n *html.Node) {
	if e.customElements == nil || n == nil || len(e.customElements.defs) == 0 {
		return
	}
	e.customElements.upgradeTree(n, nil)
	e.customElements.noteConnected(n)
}

// upgradeRemovedSubtree is called after a node is removed from the document.
func (e *jsEnv) upgradeRemovedSubtree(n *html.Node) {
	if e.customElements == nil || n == nil || len(e.customElements.states) == 0 {
		return
	}
	e.customElements.noteDisconnected(n)
}

// upgradeDocument walks a freshly parsed document so `<my-el>` in the HTML is a
// custom element by the time the page's scripts run.
func (e *jsEnv) upgradeDocument() {
	if e.customElements == nil {
		return
	}
	e.customElements.upgradeTree(e.page.doc, nil)
	e.customElements.noteConnected(e.page.doc)
}
