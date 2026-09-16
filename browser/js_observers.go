package browser

import (
	"strconv"
	"strings"

	"github.com/dop251/goja"
	"golang.org/x/net/html"
)

// MutationObserver previously only stored its callback: observe() did nothing,
// so a page that builds its content in a mutation callback saw an empty tree.
// This file records mutations at the DOM entry points in jsdom.go and delivers
// them the way a browser does, as a batch on the microtask checkpoint.

type mutationRecordData struct {
	typ      string // childList, attributes, characterData
	target   *html.Node
	added    []*html.Node
	removed  []*html.Node
	attrName string
	oldValue string
	hasOld   bool
}

// mutationOptions is one observe() call.
type mutationOptions struct {
	target        *html.Node
	childList     bool
	attributes    bool
	characterData bool
	subtree       bool
	attrOldValue  bool
	charOldValue  bool
	filter        map[string]bool
}

// matches reports whether a mutation on target belongs to this observation,
// walking ancestors when subtree was requested.
func (o mutationOptions) matches(target *html.Node) bool {
	if target == nil || o.target == nil {
		return false
	}
	if target == o.target {
		return true
	}
	if !o.subtree {
		return false
	}
	for n := target.Parent; n != nil; n = n.Parent {
		if n == o.target {
			return true
		}
	}
	return false
}

type jsMutationObserver struct {
	e       *jsEnv
	obj     *goja.Object
	cb      goja.Callable
	opts    []mutationOptions
	records []mutationRecordData
}

func (e *jsEnv) newMutationObserver(cbValue goja.Value) *goja.Object {
	o := e.vm.NewObject()
	e.tagObject(o, "MutationObserver")
	m := &jsMutationObserver{e: e, obj: o}
	if fn, ok := goja.AssertFunction(cbValue); ok {
		m.cb = fn
	}
	e.mutations = append(e.mutations, m)

	_ = o.Set("observe", func(call goja.FunctionCall) goja.Value {
		target := e.nodeArg(call.Argument(0))
		if target == nil {
			panic(e.vm.NewTypeError("observe: argument 1 is not a Node"))
		}
		opt, _ := call.Argument(1).(*goja.Object)
		mo := mutationOptions{target: target}
		if opt != nil {
			mo.childList = boolProp(opt, "childList")
			mo.attributes = boolProp(opt, "attributes")
			mo.characterData = boolProp(opt, "characterData")
			mo.subtree = boolProp(opt, "subtree")
			mo.attrOldValue = boolProp(opt, "attributeOldValue")
			mo.charOldValue = boolProp(opt, "characterDataOldValue")
			if f := opt.Get("attributeFilter"); f != nil && !goja.IsUndefined(f) && !goja.IsNull(f) {
				mo.filter = map[string]bool{}
				if fo, ok := f.(*goja.Object); ok {
					for i := 0; i < int(fo.Get("length").ToInteger()); i++ {
						mo.filter[strings.ToLower(argString(fo.Get(strconv.Itoa(i))))] = true
					}
				}
			}
			// attributeOldValue implies attributes, as in the spec.
			if mo.attrOldValue {
				mo.attributes = true
			}
			if mo.charOldValue {
				mo.characterData = true
			}
		}
		m.opts = append(m.opts, mo)
		return goja.Undefined()
	})
	_ = o.Set("disconnect", func(goja.FunctionCall) goja.Value {
		m.opts = nil
		m.records = nil
		return goja.Undefined()
	})
	_ = o.Set("takeRecords", func(goja.FunctionCall) goja.Value {
		return e.mutationRecordArray(m.takeRecords())
	})
	return o
}

func boolProp(o *goja.Object, name string) bool {
	v := o.Get(name)
	return v != nil && !goja.IsUndefined(v) && v.ToBoolean()
}

func (m *jsMutationObserver) takeRecords() []mutationRecordData {
	recs := m.records
	m.records = nil
	return recs
}

// --- recording -------------------------------------------------------------

// queueMutation offers a record to every observer that asked for it. One
// mutation produces at most one record per observer, which is why the inner
// loop breaks on the first matching observation.
func (e *jsEnv) queueMutation(rec mutationRecordData) {
	if len(e.mutations) == 0 || rec.target == nil {
		return
	}
	for _, m := range e.mutations {
		for _, o := range m.opts {
			if !o.matches(rec.target) {
				continue
			}
			switch rec.typ {
			case "childList":
				if !o.childList {
					continue
				}
			case "attributes":
				if !o.attributes {
					continue
				}
				if o.filter != nil && !o.filter[rec.attrName] {
					continue
				}
			case "characterData":
				if !o.characterData {
					continue
				}
			default:
				continue
			}
			m.records = append(m.records, rec)
			break
		}
	}
}

// noteChildList records nodes added to or removed from parent.
func (e *jsEnv) noteChildList(parent *html.Node, added, removed []*html.Node) {
	if len(e.mutations) == 0 || parent == nil {
		return
	}
	e.queueMutation(mutationRecordData{
		typ: "childList", target: parent, added: added, removed: removed,
	})
}

// noteAttr records an attribute change. old is reported only when the observer
// asked for attributeOldValue.
func (e *jsEnv) noteAttr(n *html.Node, name, old string, hadOld bool) {
	if len(e.mutations) == 0 || n == nil {
		return
	}
	e.queueMutation(mutationRecordData{
		typ: "attributes", target: n, attrName: strings.ToLower(name),
		oldValue: old, hasOld: hadOld,
	})
}

// noteText records a characterData change.
func (e *jsEnv) noteText(n *html.Node, old string) {
	if len(e.mutations) == 0 || n == nil {
		return
	}
	e.queueMutation(mutationRecordData{typ: "characterData", target: n, oldValue: old, hasOld: true})
}

// setAttrNoted is setAttribute with the mutation recorded.
func (e *jsEnv) setAttrNoted(n *html.Node, name, value string) {
	if n == nil {
		return
	}
	old, had := getAttr(n, name)
	setAttr(n, name, value)
	e.noteAttr(n, name, old, had)
	if def := e.customElementAt(n, name); def != nil {
		e.customElements.attributeChanged(n, name, old, value)
	}
}

// removeAttrNoted is removeAttribute with the mutation recorded.
func (e *jsEnv) removeAttrNoted(n *html.Node, name string) {
	if n == nil {
		return
	}
	old, had := getAttr(n, name)
	if !had {
		return
	}
	removeAttr(n, name)
	e.noteAttr(n, name, old, true)
	if def := e.customElementAt(n, name); def != nil {
		e.customElements.attributeChanged(n, name, old, "")
	}
}

// --- delivery --------------------------------------------------------------

// flushMutations delivers queued batches. It is called at every microtask
// checkpoint: after a script, after each timer, after an event and at the end
// of the load phases.
func (e *jsEnv) flushMutations() {
	for _, m := range e.mutations {
		if m.cb == nil || len(m.records) == 0 {
			continue
		}
		recs := m.takeRecords()
		arr := e.mutationRecordArray(recs)
		func() {
			defer func() { _ = recover() }()
			_, _ = m.cb(goja.Undefined(), arr, e.vm.ToValue(m.obj))
		}()
	}
}

func (e *jsEnv) mutationRecordArray(recs []mutationRecordData) goja.Value {
	arr := e.vm.NewArray()
	for i, r := range recs {
		_ = arr.Set(strconv.Itoa(i), e.mutationRecordObject(r))
	}
	return e.vm.ToValue(arr)
}

func (e *jsEnv) mutationRecordObject(r mutationRecordData) *goja.Object {
	o := e.vm.NewObject()
	e.tagObject(o, "MutationRecord")
	_ = o.Set("type", r.typ)
	_ = o.Set("target", e.wrap(r.target))
	_ = o.Set("addedNodes", e.nodeList(r.added))
	_ = o.Set("removedNodes", e.nodeList(r.removed))
	_ = o.Set("attributeNamespace", goja.Null())
	_ = o.Set("previousSibling", goja.Null())
	_ = o.Set("nextSibling", goja.Null())
	if r.typ == "attributes" {
		_ = o.Set("attributeName", r.attrName)
	} else {
		_ = o.Set("attributeName", goja.Null())
	}
	if r.hasOld {
		_ = o.Set("oldValue", e.vm.ToValue(r.oldValue))
	} else {
		_ = o.Set("oldValue", goja.Null())
	}
	return o
}

// --- ResizeObserver --------------------------------------------------------

// newResizeObserver reports each observed element once, with its laid-out size,
// which is the notification a lazy component waits for.
func (e *jsEnv) newResizeObserver(cbValue goja.Value) *goja.Object {
	o := e.vm.NewObject()
	e.tagObject(o, "ResizeObserver")
	cb, _ := goja.AssertFunction(cbValue)
	var targets []*html.Node
	nudge := func() {
		if cb == nil || len(targets) == 0 {
			return
		}
		t := targets
		targets = nil
		func() {
			defer func() { _ = recover() }()
			entries := e.vm.NewArray()
			for i, n := range t {
				rect := e.page.ElementRect(n)
				entry := e.vm.NewObject()
				_ = entry.Set("target", e.wrap(n))
				_ = entry.Set("contentRect", e.domRect(rect))
				if i == 0 {
					_ = entries.Set("0", entry)
				} else {
					_ = entries.Set(strconv.Itoa(i), entry)
				}
			}
			_, _ = cb(goja.Undefined(), entries, e.vm.ToValue(o))
		}()
	}
	_ = o.Set("observe", func(call goja.FunctionCall) goja.Value {
		if n := e.nodeArg(call.Argument(0)); n != nil {
			targets = append(targets, n)
		}
		return goja.Undefined()
	})
	_ = o.Set("unobserve", func(call goja.FunctionCall) goja.Value {
		n := e.nodeArg(call.Argument(0))
		for i, t := range targets {
			if t == n {
				targets = append(targets[:i], targets[i+1:]...)
				break
			}
		}
		return goja.Undefined()
	})
	_ = o.Set("disconnect", func(goja.FunctionCall) goja.Value {
		targets = nil
		return goja.Undefined()
	})
	e.resizeObservers = append(e.resizeObservers, nudge)
	return o
}

// flushResize reports pending resize observations.
func (e *jsEnv) flushResize() {
	for _, f := range e.resizeObservers {
		f()
	}
}
