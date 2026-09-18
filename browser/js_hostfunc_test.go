package browser

import (
	"strings"
	"testing"
)

// A host function must not publish the Go symbol that implements it. A browser
// never produces "function shade/browser.(*jsEnv)...() { [native code] }",
// and both Function.prototype.toString and .name exposed it on every function
// in the environment.
func TestHostFunctionsLookNative(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	checkAll(t, p, []struct{ expr, want string }{
		{`Function.prototype.toString.call(btoa)`, "function btoa() { [native code] }"},
		{`btoa.name`, "btoa"},
		{`Function.prototype.toString.call(atob)`, "function atob() { [native code] }"},
		{`Function.prototype.toString.call(navigator.sendBeacon)`, "function sendBeacon() { [native code] }"},
		{`navigator.sendBeacon.name`, "sendBeacon"},
		{`Function.prototype.toString.call(document.querySelector)`, "function querySelector() { [native code] }"},
		{`Function.prototype.toString.call(document.createElement)`, "function createElement() { [native code] }"},
		{`Function.prototype.toString.call(setTimeout)`, "function setTimeout() { [native code] }"},
		{`Function.prototype.toString.call(fetch)`, "function fetch() { [native code] }"},
		{`Function.prototype.toString.call(Element.prototype.getAttribute)`, "function getAttribute() { [native code] }"},
		{`Function.prototype.toString.call(JSON.stringify)`, "function stringify() { [native code] }"},
		{`Function.prototype.toString.call(Object)`, "function Object() { [native code] }"},
	})
}

// Nothing in the environment may name the library, on any function reachable
// from the objects a page normally touches.
func TestNoHostFunctionLeaksGoSymbol(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := stealthEval(t, p, `(function () {
		var bad = [];
		function check(obj, label) {
			if (!obj) { return; }
			var keys;
			try { keys = Object.getOwnPropertyNames(obj); } catch (e) { return; }
			for (var i = 0; i < keys.length; i++) {
				var k = keys[i], v;
				try { v = obj[k]; } catch (e) { continue; }
				if (typeof v !== "function") { continue; }
				var s = Function.prototype.toString.call(v) + "|" + String(v.name);
				if (s.indexOf("shade") >= 0 || s.indexOf("/browser") >= 0) {
					bad.push(label + "." + k);
				}
			}
		}
		check(window, "window");
		check(navigator, "navigator");
		check(document, "document");
		check(Element.prototype, "Element");
		check(Node.prototype, "Node");
		check(Document.prototype, "Document");
		check(console, "console");
		check(performance, "performance");
		return bad.slice(0, 12).join(",") || "clean";
	})()`)
	if got != "clean" {
		t.Errorf("host functions still leaking Go symbols: %s", got)
	}
}

// A function the page itself defines must still report its own source.
func TestPageFunctionSourceSurvives(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := stealthEval(t, p, `(function named() { return 1; }).toString()`)
	if !strings.Contains(got, "return 1") || !strings.Contains(got, "named") {
		t.Errorf("page function toString = %q, want its own source", got)
	}
}

// Calling a host function still works after it has been renamed.
func TestHostFunctionsStillCallable(t *testing.T) {
	p := flexPage(t, `<html><body><div id="d" class="c">x</div></body></html>`)
	checkAll(t, p, []struct{ expr, want string }{
		{`btoa("hi")`, "aGk="},
		{`atob("aGk=")`, "hi"},
		{`document.getElementById("d").getAttribute("class")`, "c"},
		{`typeof setTimeout`, "function"},
		{`navigator.sendBeacon("")`, "false"},
	})
}
