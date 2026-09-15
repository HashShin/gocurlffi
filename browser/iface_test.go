package browser

import "testing"

// Object.prototype.toString.call(x) is the oldest way to ask what something is,
// and a browser answers with the interface name. Everything here used to answer
// "[object Object]", which one comparison is enough to catch.
func TestInterfaceTagsMatchBrowser(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	checkAll(t, p, []struct{ expr, want string }{
		{`Object.prototype.toString.call(window)`, "[object Window]"},
		{`Object.prototype.toString.call(document)`, "[object HTMLDocument]"},
		{`Object.prototype.toString.call(navigator)`, "[object Navigator]"},
		{`Object.prototype.toString.call(location)`, "[object Location]"},
		{`Object.prototype.toString.call(screen)`, "[object Screen]"},
		{`Object.prototype.toString.call(history)`, "[object History]"},
		{`Object.prototype.toString.call(localStorage)`, "[object Storage]"},
		{`Object.prototype.toString.call(performance)`, "[object Performance]"},
		{`Object.prototype.toString.call(document.body)`, "[object HTMLBodyElement]"},
		{`Object.prototype.toString.call(document.createElement("div"))`, "[object HTMLDivElement]"},
		{`Object.prototype.toString.call(document.createElement("img"))`, "[object HTMLImageElement]"},
		{`Object.prototype.toString.call(document.createElement("a"))`, "[object HTMLAnchorElement]"},
		{`Object.prototype.toString.call(document.createElement("h1"))`, "[object HTMLHeadingElement]"},
		{`Object.prototype.toString.call(document.createElement("p"))`, "[object HTMLParagraphElement]"},
		{`Object.prototype.toString.call(document.createTextNode("x"))`, "[object Text]"},
		{`Object.prototype.toString.call(document.createDocumentFragment())`, "[object DocumentFragment]"},
		{`Object.prototype.toString.call(navigator.plugins)`, "[object PluginArray]"},
	})
}

func TestConstructorNamesMatchBrowser(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	checkAll(t, p, []struct{ expr, want string }{
		{`window.constructor.name`, "Window"},
		{`document.constructor.name`, "HTMLDocument"},
		{`navigator.constructor.name`, "Navigator"},
		{`location.constructor.name`, "Location"},
		{`document.body.constructor.name`, "HTMLBodyElement"},
		{`document.createElement("div").constructor.name`, "HTMLDivElement"},
		{`document.createElement("img").constructor.name`, "HTMLImageElement"},
		{`document.createTextNode("x").constructor.name`, "Text"},
		{`navigator.plugins.constructor.name`, "PluginArray"},
	})
}

// The element interfaces are globals in a browser, and instanceof has to agree.
func TestElementInterfacesAreGlobals(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	checkAll(t, p, []struct{ expr, want string }{
		{`typeof HTMLDivElement`, "function"},
		{`typeof HTMLBodyElement`, "function"},
		{`typeof HTMLImageElement`, "function"},
		{`typeof HTMLAnchorElement`, "function"},
		{`typeof HTMLElement`, "function"},
		{`typeof Window`, "function"},
		{`typeof Navigator`, "function"},
		{`document.createElement("div") instanceof HTMLDivElement`, "true"},
		{`document.body instanceof HTMLBodyElement`, "true"},
	})
}

// The Node constants defined by setupConstructors must survive the later
// interface pass, which publishes globals of its own.
func TestNodeConstantsSurviveInterfacePass(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	checkAll(t, p, []struct{ expr, want string }{
		{`Node.ELEMENT_NODE`, "1"},
		{`Node.TEXT_NODE`, "3"},
		{`Node.DOCUMENT_NODE`, "9"},
		{`Node.DOCUMENT_FRAGMENT_NODE`, "11"},
		{`document.body instanceof Node`, "true"},
	})
}

// V8 exposes these two on Error; their absence is a cheap Chrome check.
func TestErrorStatics(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	checkAll(t, p, []struct{ expr, want string }{
		{`typeof Error.captureStackTrace`, "function"},
		{`typeof Error.stackTraceLimit`, "number"},
		{`(function () { var o = {}; Error.captureStackTrace(o); return typeof o.stack; })()`, "string"},
		{`(function () { var o = {}; Error.captureStackTrace(o); return o.stack.indexOf("    at ") >= 0; })()`, "true"},
	})
}
