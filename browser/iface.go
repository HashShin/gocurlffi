package browser

import (
	"strings"

	"github.com/dop251/goja"
	"golang.org/x/net/html"
)

// Object.prototype.toString.call(x) is the oldest way to ask what something is,
// and a browser answers with the interface name:
//
//	[object HTMLDocument]   [object Navigator]   [object HTMLDivElement]
//
// Every object in this environment answered "[object Object]" (and the global
// answered "[object global]"), and no HTML*Element, Window or Navigator
// constructor existed at all. A single comparison is enough to tell that the
// document is not a document, so these tags are wired up here.

// htmlElementInterfaces maps a tag to the interface a browser reports for it,
// where the generic HTML<Tag>Element rule does not hold.
var htmlElementInterfaces = map[string]string{
	"a": "HTMLAnchorElement", "abbr": "HTMLElement", "address": "HTMLElement",
	"area": "HTMLAreaElement", "article": "HTMLElement", "aside": "HTMLElement",
	"audio": "HTMLAudioElement", "b": "HTMLElement", "base": "HTMLBaseElement",
	"bdi": "HTMLElement", "bdo": "HTMLElement", "blockquote": "HTMLQuoteElement",
	"body": "HTMLBodyElement", "br": "HTMLBRElement", "button": "HTMLButtonElement",
	"canvas": "HTMLCanvasElement", "caption": "HTMLTableCaptionElement",
	"cite": "HTMLElement", "code": "HTMLElement", "col": "HTMLTableColElement",
	"colgroup": "HTMLTableColElement", "data": "HTMLDataElement",
	"datalist": "HTMLDataListElement", "dd": "HTMLElement", "del": "HTMLModElement",
	"details": "HTMLDetailsElement", "dfn": "HTMLElement", "dialog": "HTMLDialogElement",
	"div": "HTMLDivElement", "dl": "HTMLDListElement", "dt": "HTMLElement",
	"em": "HTMLElement", "embed": "HTMLEmbedElement", "fieldset": "HTMLFieldSetElement",
	"figcaption": "HTMLElement", "figure": "HTMLElement", "footer": "HTMLElement",
	"form": "HTMLFormElement", "h1": "HTMLHeadingElement", "h2": "HTMLHeadingElement",
	"h3": "HTMLHeadingElement", "h4": "HTMLHeadingElement", "h5": "HTMLHeadingElement",
	"h6": "HTMLHeadingElement", "head": "HTMLHeadElement", "header": "HTMLElement",
	"hgroup": "HTMLElement", "hr": "HTMLHRElement", "html": "HTMLHtmlElement",
	"i": "HTMLElement", "iframe": "HTMLIFrameElement", "img": "HTMLImageElement",
	"input": "HTMLInputElement", "ins": "HTMLModElement", "kbd": "HTMLElement",
	"label": "HTMLLabelElement", "legend": "HTMLLegendElement", "li": "HTMLLIElement",
	"link": "HTMLLinkElement", "main": "HTMLElement", "map": "HTMLMapElement",
	"mark": "HTMLElement", "menu": "HTMLMenuElement", "meta": "HTMLMetaElement",
	"meter": "HTMLMeterElement", "nav": "HTMLElement", "noscript": "HTMLElement",
	"object": "HTMLObjectElement", "ol": "HTMLOListElement",
	"optgroup": "HTMLOptGroupElement", "option": "HTMLOptionElement",
	"output": "HTMLOutputElement", "p": "HTMLParagraphElement",
	"picture": "HTMLPictureElement", "pre": "HTMLPreElement",
	"progress": "HTMLProgressElement", "q": "HTMLQuoteElement",
	"rp": "HTMLElement", "rt": "HTMLElement", "ruby": "HTMLElement",
	"s": "HTMLElement", "samp": "HTMLElement", "script": "HTMLScriptElement",
	"section": "HTMLElement", "select": "HTMLSelectElement",
	"slot": "HTMLSlotElement", "small": "HTMLElement", "source": "HTMLSourceElement",
	"span": "HTMLSpanElement", "strong": "HTMLElement", "style": "HTMLStyleElement",
	"sub": "HTMLElement", "summary": "HTMLElement", "sup": "HTMLElement",
	"table": "HTMLTableElement", "tbody": "HTMLTableSectionElement",
	"td": "HTMLTableCellElement", "template": "HTMLTemplateElement",
	"textarea": "HTMLTextAreaElement", "tfoot": "HTMLTableSectionElement",
	"th": "HTMLTableCellElement", "thead": "HTMLTableSectionElement",
	"time": "HTMLTimeElement", "title": "HTMLTitleElement",
	"tr": "HTMLTableRowElement", "track": "HTMLTrackElement",
	"u": "HTMLElement", "ul": "HTMLUListElement", "var": "HTMLElement",
	"video": "HTMLVideoElement", "wbr": "HTMLElement",
}

// elementInterfaceName is the interface a browser would report for a node.
func elementInterfaceName(n *html.Node) string {
	if n == nil {
		return "HTMLElement"
	}
	if isFragment(n) {
		return "DocumentFragment"
	}
	if n.Type != html.ElementNode {
		switch n.Type {
		case html.TextNode:
			return "Text"
		case html.CommentNode:
			return "Comment"
		case html.DocumentNode:
			return "HTMLDocument"
		}
		return "HTMLElement"
	}
	if n.Namespace == "svg" {
		return "SVGElement"
	}
	tag := strings.ToLower(n.Data)
	if name, ok := htmlElementInterfaces[tag]; ok {
		return name
	}
	if strings.Contains(tag, "-") {
		// A valid custom element name reports the base interface.
		return "HTMLElement"
	}
	return "HTML" + strings.ToUpper(tag[:1]) + tag[1:] + "Element"
}

// tagProto gives a prototype a fixed Symbol.toStringTag.
func (e *jsEnv) tagProto(proto *goja.Object, tag string) {
	_ = proto.DefineDataPropertySymbol(goja.SymToStringTag, e.vm.ToValue(tag),
		goja.FLAG_FALSE, goja.FLAG_TRUE, goja.FLAG_FALSE)
}

// tagObject gives a single object a fixed Symbol.toStringTag.
func (e *jsEnv) tagObject(o *goja.Object, tag string) {
	if o == nil {
		return
	}
	_ = o.DefineDataPropertySymbol(goja.SymToStringTag, e.vm.ToValue(tag),
		goja.FLAG_FALSE, goja.FLAG_TRUE, goja.FLAG_FALSE)
}

// tagDynamicProto makes a prototype report a tag computed from its node, so a
// div answers HTMLDivElement and an img answers HTMLImageElement.
func (e *jsEnv) tagDynamicProto(proto *goja.Object) {
	get := e.vm.ToValue(func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue(elementInterfaceName(e.thisNode(call)))
	})
	_ = proto.DefineAccessorPropertySymbol(goja.SymToStringTag, get, nil,
		goja.FLAG_TRUE, goja.FLAG_FALSE)
}

// namedConstructor returns a DOM interface constructor: not constructible per
// spec, but carrying the right name so `x.constructor.name` reads like a
// browser's rather than "Object".
func (e *jsEnv) namedConstructor(name string, proto *goja.Object) *goja.Object {
	fn := e.vm.ToValue(func(call goja.ConstructorCall) *goja.Object {
		return call.This
	}).(*goja.Object)
	_ = fn.DefineDataProperty("name", e.vm.ToValue(name), goja.FLAG_FALSE, goja.FLAG_TRUE, goja.FLAG_TRUE)
	if proto != nil {
		_ = fn.Set("prototype", proto)
	}
	return fn
}

// installElementInterfaces makes element.constructor.name follow the element,
// and publishes the HTML*Element interfaces as globals so `typeof
// HTMLDivElement` is "function" rather than "undefined".
func (e *jsEnv) installElementInterfaces() {
	p := e.protosRef
	if p == nil {
		return
	}
	ctors := map[string]*goja.Object{}
	ensure := func(name string) *goja.Object {
		if c, ok := ctors[name]; ok {
			return c
		}
		c := e.namedConstructor(name, p.element)
		ctors[name] = c
		return c
	}
	for _, name := range htmlElementInterfaces {
		ensure(name)
	}
	for _, name := range []string{
		"HTMLElement", "HTMLUnknownElement", "Element", "Node",
		"DocumentFragment", "Text", "Comment", "HTMLDocument",
	} {
		ensure(name)
	}
	for name, c := range ctors {
		// setupConstructors already published Node, Element, Text and friends
		// with their prototypes and constants; only fill in the rest.
		if existing := e.vm.Get(name); existing != nil && !goja.IsUndefined(existing) && !goja.IsNull(existing) {
			continue
		}
		_ = e.vm.Set(name, c)
	}
	_ = e.vm.Set("HTMLElement", ensure("HTMLElement"))

	get := e.vm.ToValue(func(call goja.FunctionCall) goja.Value {
		return ensure(elementInterfaceName(e.thisNode(call)))
	})
	_ = p.element.DefineAccessorProperty("constructor", get, nil, goja.FLAG_TRUE, goja.FLAG_FALSE)
}

// installInterfaceTags gives the prototypes and the well-known host objects the
// tag a browser reports for them. Called once the environment is fully built,
// because it reaches into the global object for the objects it tags.
func (e *jsEnv) installInterfaceTags() {
	p := e.protosRef
	if p == nil {
		return
	}
	e.tagDynamicProto(p.element)
	e.tagProto(p.node, "Node")
	e.tagProto(p.document, "HTMLDocument")
	e.tagProto(p.text, "Text")
	e.tagProto(p.comment, "Comment")
	e.tagObject(e.vm.GlobalObject(), "Window")

	for _, t := range []struct{ name, tag string }{
		{"navigator", "Navigator"},
		{"location", "Location"},
		{"screen", "Screen"},
		{"history", "History"},
		{"localStorage", "Storage"},
		{"sessionStorage", "Storage"},
		{"performance", "Performance"},
	} {
		o, ok := e.vm.Get(t.name).(*goja.Object)
		if !ok || o == nil {
			continue
		}
		e.tagObject(o, t.tag)
		_ = o.DefineDataProperty("constructor",
			e.namedConstructor(t.tag, nil), goja.FLAG_TRUE, goja.FLAG_FALSE, goja.FLAG_TRUE)
	}
	_ = e.vm.Set("Window", e.namedConstructor("Window", nil))
	_ = e.vm.Set("Navigator", e.namedConstructor("Navigator", nil))
	for _, name := range []string{
		"WebGLRenderingContext", "WebGL2RenderingContext", "CanvasRenderingContext2D",
		"AudioContext", "webkitAudioContext", "RTCPeerConnection", "Notification",
		"Permissions", "NetworkInformation", "Blob", "File", "FileReader", "FormData",
		"Headers", "Request", "Response", "EventSource", "BroadcastChannel",
		"MessageChannel", "MessagePort", "caches", "SharedWorker",
	} {
		if existing := e.vm.Get(name); existing == nil || goja.IsUndefined(existing) {
			_ = e.vm.Set(name, e.namedConstructor(name, nil))
		}
	}
	e.installElementInterfaces()

	// The global's constructor is Window, and the document's is HTMLDocument:
	// Document is in the chain but is not the constructor.
	win := e.namedConstructor("Window", nil)
	_ = e.vm.GlobalObject().DefineDataProperty("constructor", win, goja.FLAG_TRUE, goja.FLAG_FALSE, goja.FLAG_TRUE)
	_ = e.vm.Set("Window", win)
	htmlDoc := e.namedConstructor("HTMLDocument", p.document)
	_ = p.document.DefineDataProperty("constructor", htmlDoc, goja.FLAG_TRUE, goja.FLAG_FALSE, goja.FLAG_TRUE)
	_ = e.vm.Set("HTMLDocument", htmlDoc)
}
