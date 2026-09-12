package browser

import (
	"strconv"
	"strings"

	"github.com/dop251/goja"
	"golang.org/x/net/html"
)

// domNodeType maps x/net/html node types to DOM Level 2 nodeType numbers.
func domNodeType(n *html.Node) int {
	switch n.Type {
	case html.ElementNode:
		return 1
	case html.TextNode:
		return 3
	case html.CommentNode:
		return 8
	case html.DocumentNode:
		return 9
	case html.DoctypeNode:
		return 10
	}
	return 0
}

func nodeNameOf(n *html.Node) string {
	switch n.Type {
	case html.ElementNode:
		return strings.ToUpper(n.Data)
	case html.TextNode:
		return "#text"
	case html.CommentNode:
		return "#comment"
	case html.DocumentNode:
		return "#document"
	case html.DoctypeNode:
		return html.UnescapeString(n.Data)
	}
	return ""
}

func (e *jsEnv) nodeList(nodes []*html.Node) goja.Value {
	items := make([]interface{}, 0, len(nodes))
	for _, n := range nodes {
		items = append(items, e.wrap(n))
	}
	arr := e.vm.NewArray(items...)
	_ = arr.Set("item", func(call goja.FunctionCall) goja.Value {
		i := int(call.Argument(0).ToInteger())
		if i < 0 || i >= len(nodes) {
			return goja.Null()
		}
		return e.wrap(nodes[i])
	})
	if len(nodes) == 0 {
		// NewArray() with no items yields an empty array.
	}
	return arr
}

// --- Node prototype ---

func (e *jsEnv) defineNodeProto(p *goja.Object) {
	e.accessor(p, "nodeType", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n == nil {
			return e.vm.ToValue(0)
		}
		return e.vm.ToValue(domNodeType(n))
	}, nil)
	e.accessor(p, "nodeName", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue(nodeNameOf(e.thisNode(call)))
	}, nil)
	e.accessor(p, "tagName", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n == nil || n.Type != html.ElementNode {
			return goja.Undefined()
		}
		return e.vm.ToValue(strings.ToUpper(n.Data))
	}, nil)
	e.accessor(p, "localName", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n == nil {
			return goja.Undefined()
		}
		return e.vm.ToValue(n.Data)
	}, nil)

	e.accessor(p, "parentNode", func(call goja.FunctionCall) goja.Value {
		return e.wrap(parentOf(e.thisNode(call)))
	}, nil)
	e.accessor(p, "parentElement", func(call goja.FunctionCall) goja.Value {
		return e.wrap(parentOf(e.thisNode(call)))
	}, nil)
	e.accessor(p, "ownerDocument", func(call goja.FunctionCall) goja.Value {
		return e.wrap(e.page.doc)
	}, nil)
	e.accessor(p, "firstChild", func(call goja.FunctionCall) goja.Value {
		return e.wrap(firstChildOf(e.thisNode(call)))
	}, nil)
	e.accessor(p, "lastChild", func(call goja.FunctionCall) goja.Value {
		return e.wrap(lastChildOf(e.thisNode(call)))
	}, nil)
	e.accessor(p, "nextSibling", func(call goja.FunctionCall) goja.Value {
		return e.wrap(nextSiblingOf(e.thisNode(call)))
	}, nil)
	e.accessor(p, "previousSibling", func(call goja.FunctionCall) goja.Value {
		return e.wrap(prevSiblingOf(e.thisNode(call)))
	}, nil)
	e.accessor(p, "childNodes", func(call goja.FunctionCall) goja.Value {
		return e.nodeList(childNodes(e.thisNode(call)))
	}, nil)
	e.accessor(p, "nodeValue", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n == nil || (n.Type != html.TextNode && n.Type != html.CommentNode) {
			return goja.Null()
		}
		return e.vm.ToValue(n.Data)
	}, func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n != nil && (n.Type == html.TextNode || n.Type == html.CommentNode) {
			n.Data = argString(call.Argument(0))
		}
		return goja.Undefined()
	})
	e.accessor(p, "textContent", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue(textContent(e.thisNode(call)))
	}, func(call goja.FunctionCall) goja.Value {
		setTextContent(e.thisNode(call), argString(call.Argument(0)))
		return goja.Undefined()
	})
	e.accessor(p, "isConnected", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue(true)
	}, nil)

	e.method(p, "appendChild", func(call goja.FunctionCall) goja.Value {
		parent := e.thisNode(call)
		child := e.nodeArg(call.Argument(0))
		if parent == nil || child == nil {
			panic(e.vm.NewTypeError("appendChild: invalid node"))
		}
		appendChild(parent, child)
		return e.wrap(child)
	})
	e.method(p, "removeChild", func(call goja.FunctionCall) goja.Value {
		child := e.nodeArg(call.Argument(0))
		if child == nil {
			panic(e.vm.NewTypeError("removeChild: invalid node"))
		}
		removeChild(child)
		return e.wrap(child)
	})
	e.method(p, "insertBefore", func(call goja.FunctionCall) goja.Value {
		parent := e.thisNode(call)
		child := e.nodeArg(call.Argument(0))
		ref := e.nodeArg(call.Argument(1))
		if parent == nil || child == nil {
			panic(e.vm.NewTypeError("insertBefore: invalid node"))
		}
		insertBefore(parent, child, ref)
		return e.wrap(child)
	})
	e.method(p, "replaceChild", func(call goja.FunctionCall) goja.Value {
		parent := e.thisNode(call)
		newN := e.nodeArg(call.Argument(0))
		oldN := e.nodeArg(call.Argument(1))
		if parent == nil || newN == nil || oldN == nil {
			panic(e.vm.NewTypeError("replaceChild: invalid node"))
		}
		replaceChild(parent, newN, oldN)
		return e.wrap(oldN)
	})
	e.method(p, "hasChildNodes", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		return e.vm.ToValue(n != nil && n.FirstChild != nil)
	})
	e.method(p, "contains", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		other := e.nodeArg(call.Argument(0))
		if n == nil || other == nil {
			return e.vm.ToValue(false)
		}
		for cur := other; cur != nil; cur = cur.Parent {
			if cur == n {
				return e.vm.ToValue(true)
			}
		}
		return e.vm.ToValue(false)
	})
	e.method(p, "cloneNode", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n == nil {
			return goja.Null()
		}
		return e.wrap(cloneNode(n, call.Argument(0).ToBoolean()))
	})
	e.method(p, "getRootNode", func(call goja.FunctionCall) goja.Value {
		return e.wrap(e.page.doc)
	})
	e.method(p, "addEventListener", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		typ := strings.ToLower(argString(call.Argument(0)))
		if n != nil {
			e.addNodeListener(n, typ, call.Argument(1))
		}
		return goja.Undefined()
	})
	e.method(p, "removeEventListener", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		typ := strings.ToLower(argString(call.Argument(0)))
		if n != nil {
			e.removeNodeListener(n, typ, call.Argument(1))
		}
		return goja.Undefined()
	})
	e.method(p, "dispatchEvent", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		ev := e.normalizeEvent(call.Argument(0))
		if n != nil {
			e.dispatchNode(n, strings.ToLower(argString(ev.Get("type"))), ev)
		}
		return e.vm.ToValue(true)
	})
}

// --- Element prototype ---

func (e *jsEnv) defineElementProto(p *goja.Object) {
	e.accessor(p, "id", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue(id(e.thisNode(call)))
	}, func(call goja.FunctionCall) goja.Value {
		if n := e.thisNode(call); n != nil {
			setAttr(n, "id", argString(call.Argument(0)))
		}
		return goja.Undefined()
	})
	e.accessor(p, "className", func(call goja.FunctionCall) goja.Value {
		v, _ := getAttr(e.thisNode(call), "class")
		return e.vm.ToValue(v)
	}, func(call goja.FunctionCall) goja.Value {
		if n := e.thisNode(call); n != nil {
			setAttr(n, "class", argString(call.Argument(0)))
		}
		return goja.Undefined()
	})
	e.accessor(p, "classList", func(call goja.FunctionCall) goja.Value {
		return e.classListObject(e.thisNode(call))
	}, nil)
	e.accessor(p, "attributes", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n == nil {
			return goja.Null()
		}
		arr := e.vm.NewArray()
		for i, a := range n.Attr {
			o := e.vm.NewObject()
			_ = o.Set("name", a.Key)
			_ = o.Set("value", a.Val)
			_ = o.Set("nodeName", a.Key)
			_ = arr.Set(strconv.Itoa(i), o)
		}
		_ = arr.Set("length", len(n.Attr))
		return arr
	}, nil)
	e.accessor(p, "innerHTML", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue(innerHTML(e.thisNode(call)))
	}, func(call goja.FunctionCall) goja.Value {
		setInnerHTML(e.thisNode(call), argString(call.Argument(0)))
		return goja.Undefined()
	})
	e.accessor(p, "outerHTML", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue(outerHTML(e.thisNode(call)))
	}, func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n == nil || n.Parent == nil {
			return goja.Undefined()
		}
		parent := n.Parent
		insertBefore(parent, createElement("div"), n)
		div := n.PrevSibling
		setInnerHTML(div, argString(call.Argument(0)))
		nodes := childNodes(div)
		for _, c := range nodes {
			insertBefore(parent, c, n)
		}
		removeChild(div)
		removeChild(n)
		return goja.Undefined()
	})
	e.accessor(p, "innerText", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue(textContent(e.thisNode(call)))
	}, func(call goja.FunctionCall) goja.Value {
		setTextContent(e.thisNode(call), argString(call.Argument(0)))
		return goja.Undefined()
	})
	e.accessor(p, "style", func(call goja.FunctionCall) goja.Value {
		return e.styleObject(e.thisNode(call))
	}, nil)
	e.accessor(p, "dataset", func(call goja.FunctionCall) goja.Value {
		return e.datasetObject(e.thisNode(call))
	}, nil)
	e.accessor(p, "children", func(call goja.FunctionCall) goja.Value {
		return e.nodeList(elementChildren(e.thisNode(call)))
	}, nil)
	e.accessor(p, "childElementCount", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue(len(elementChildren(e.thisNode(call))))
	}, nil)
	e.accessor(p, "firstElementChild", func(call goja.FunctionCall) goja.Value {
		cs := elementChildren(e.thisNode(call))
		if len(cs) == 0 {
			return goja.Null()
		}
		return e.wrap(cs[0])
	}, nil)
	e.accessor(p, "lastElementChild", func(call goja.FunctionCall) goja.Value {
		cs := elementChildren(e.thisNode(call))
		if len(cs) == 0 {
			return goja.Null()
		}
		return e.wrap(cs[len(cs)-1])
	}, nil)
	e.accessor(p, "nextElementSibling", func(call goja.FunctionCall) goja.Value {
		for c := nextSiblingOf(e.thisNode(call)); c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode {
				return e.wrap(c)
			}
		}
		return goja.Null()
	}, nil)
	e.accessor(p, "previousElementSibling", func(call goja.FunctionCall) goja.Value {
		for c := prevSiblingOf(e.thisNode(call)); c != nil; c = c.PrevSibling {
			if c.Type == html.ElementNode {
				return e.wrap(c)
			}
		}
		return goja.Null()
	}, nil)

	// Attributes
	e.method(p, "getAttribute", func(call goja.FunctionCall) goja.Value {
		v, ok := getAttr(e.thisNode(call), strings.ToLower(argString(call.Argument(0))))
		if !ok {
			return goja.Null()
		}
		return e.vm.ToValue(v)
	})
	e.method(p, "setAttribute", func(call goja.FunctionCall) goja.Value {
		if n := e.thisNode(call); n != nil {
			setAttr(n, strings.ToLower(argString(call.Argument(0))), argString(call.Argument(1)))
		}
		return goja.Undefined()
	})
	e.method(p, "removeAttribute", func(call goja.FunctionCall) goja.Value {
		if n := e.thisNode(call); n != nil {
			removeAttr(n, strings.ToLower(argString(call.Argument(0))))
		}
		return goja.Undefined()
	})
	e.method(p, "hasAttribute", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue(hasAttr(e.thisNode(call), strings.ToLower(argString(call.Argument(0)))))
	})
	e.method(p, "toggleAttribute", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		key := strings.ToLower(argString(call.Argument(0)))
		if hasAttr(n, key) {
			removeAttr(n, key)
			return e.vm.ToValue(false)
		}
		setAttr(n, key, "")
		return e.vm.ToValue(true)
	})
	e.method(p, "getAttributeNames", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		names := make([]interface{}, 0)
		if n != nil {
			for _, a := range n.Attr {
				names = append(names, a.Key)
			}
		}
		return e.vm.NewArray(names...)
	})
	e.method(p, "remove", func(call goja.FunctionCall) goja.Value {
		if n := e.thisNode(call); n != nil {
			removeChild(n)
		}
		return goja.Undefined()
	})
	e.method(p, "insertAdjacentHTML", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n == nil {
			return goja.Undefined()
		}
		pos := strings.ToLower(argString(call.Argument(0)))
		s := argString(call.Argument(1))
		nodes, err := html.ParseFragment(strings.NewReader(s), n)
		if err != nil {
			return goja.Undefined()
		}
		switch pos {
		case "beforebegin":
			for _, c := range nodes {
				insertBefore(n.Parent, c, n)
			}
		case "afterbegin":
			ref := n.FirstChild
			for _, c := range nodes {
				insertBefore(n, c, ref)
			}
		case "beforeend":
			for _, c := range nodes {
				appendChild(n, c)
			}
		case "afterend":
			ref := n.NextSibling
			for _, c := range nodes {
				insertBefore(n.Parent, c, ref)
			}
		}
		return goja.Undefined()
	})
	e.method(p, "append", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		for _, a := range call.Arguments {
			if node := e.nodeArg(a); node != nil {
				appendChild(n, node)
			} else {
				appendChild(n, &html.Node{Type: html.TextNode, Data: a.String()})
			}
		}
		return goja.Undefined()
	})
	e.method(p, "prepend", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		ref := n.FirstChild
		for _, a := range call.Arguments {
			node := e.nodeArg(a)
			if node == nil {
				node = &html.Node{Type: html.TextNode, Data: a.String()}
			}
			insertBefore(n, node, ref)
		}
		return goja.Undefined()
	})

	// Queries
	e.method(p, "querySelector", func(call goja.FunctionCall) goja.Value {
		return e.wrap(querySelector(e.thisNode(call), argString(call.Argument(0))))
	})
	e.method(p, "querySelectorAll", func(call goja.FunctionCall) goja.Value {
		return e.nodeList(querySelectorAll(e.thisNode(call), argString(call.Argument(0))))
	})
	e.method(p, "getElementsByTagName", func(call goja.FunctionCall) goja.Value {
		return e.nodeList(getElementsByTagName(e.thisNode(call), argString(call.Argument(0))))
	})
	e.method(p, "getElementsByClassName", func(call goja.FunctionCall) goja.Value {
		return e.nodeList(getElementsByClassName(e.thisNode(call), argString(call.Argument(0))))
	})
	e.method(p, "matches", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue(matches(e.thisNode(call), argString(call.Argument(0))))
	})
	e.method(p, "closest", func(call goja.FunctionCall) goja.Value {
		sel := argString(call.Argument(0))
		for n := e.thisNode(call); n != nil; n = n.Parent {
			if n.Type == html.ElementNode && matches(n, sel) {
				return e.wrap(n)
			}
		}
		return goja.Null()
	})

	// Interaction / geometry stubs
	e.method(p, "click", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n == nil {
			return goja.Undefined()
		}
		e.dispatchNode(n, "click", e.newEvent("click"))
		return goja.Undefined()
	})
	e.method(p, "focus", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	e.method(p, "blur", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	e.method(p, "getBoundingClientRect", func(call goja.FunctionCall) goja.Value {
		o := e.vm.NewObject()
		for _, k := range []string{"x", "y", "top", "left", "right", "bottom", "width", "height"} {
			_ = o.Set(k, 0)
		}
		return o
	})
	e.method(p, "getAttributeNS", func(call goja.FunctionCall) goja.Value {
		v, ok := getAttr(e.thisNode(call), strings.ToLower(argString(call.Argument(1))))
		if !ok {
			return goja.Null()
		}
		return e.vm.ToValue(v)
	})
	e.method(p, "setAttributeNS", func(call goja.FunctionCall) goja.Value {
		if n := e.thisNode(call); n != nil {
			setAttr(n, strings.ToLower(argString(call.Argument(1))), argString(call.Argument(2)))
		}
		return goja.Undefined()
	})
	e.method(p, "removeAttributeNS", func(call goja.FunctionCall) goja.Value {
		if n := e.thisNode(call); n != nil {
			removeAttr(n, strings.ToLower(argString(call.Argument(1))))
		}
		return goja.Undefined()
	})

	// Common reflective properties.
	for _, prop := range []string{"value", "checked", "disabled", "name", "type", "href", "src", "placeholder", "title", "alt", "rel", "target"} {
		prop := prop
		e.accessor(p, prop, func(call goja.FunctionCall) goja.Value {
			n := e.thisNode(call)
			if prop == "checked" || prop == "disabled" {
				return e.vm.ToValue(hasAttr(n, prop))
			}
			if n == nil || n.Type != html.ElementNode {
				return e.vm.ToValue("")
			}
			if v, ok := getAttr(n, prop); ok {
				return e.vm.ToValue(v)
			}
			if prop == "href" || prop == "src" {
				if v, ok := getAttr(n, prop); ok {
					return e.vm.ToValue(resolveURL(e.page.URL, v))
				}
			}
			if prop == "value" && n.Data == "textarea" {
				return e.vm.ToValue(textContent(n))
			}
			return e.vm.ToValue("")
		}, func(call goja.FunctionCall) goja.Value {
			n := e.thisNode(call)
			if n == nil {
				return goja.Undefined()
			}
			if prop == "checked" || prop == "disabled" {
				if call.Argument(0).ToBoolean() {
					setAttr(n, prop, "")
				} else {
					removeAttr(n, prop)
				}
				return goja.Undefined()
			}
			if prop == "value" && n.Data == "textarea" {
				setTextContent(n, argString(call.Argument(0)))
				return goja.Undefined()
			}
			setAttr(n, prop, argString(call.Argument(0)))
			return goja.Undefined()
		})
	}
}

// --- Text prototype ---

func (e *jsEnv) defineTextProto(p *goja.Object) {
	e.accessor(p, "data", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n == nil {
			return e.vm.ToValue("")
		}
		return e.vm.ToValue(n.Data)
	}, func(call goja.FunctionCall) goja.Value {
		if n := e.thisNode(call); n != nil {
			n.Data = argString(call.Argument(0))
		}
		return goja.Undefined()
	})
	e.accessor(p, "length", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n == nil {
			return e.vm.ToValue(0)
		}
		return e.vm.ToValue(len(n.Data))
	}, nil)
	e.accessor(p, "wholeText", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n == nil {
			return e.vm.ToValue("")
		}
		return e.vm.ToValue(n.Data)
	}, nil)
}

// --- Document prototype ---

func (e *jsEnv) defineDocumentProto(p *goja.Object) {
	e.method(p, "getElementById", func(call goja.FunctionCall) goja.Value {
		return e.wrap(getElementById(e.page.doc, argString(call.Argument(0))))
	})
	e.method(p, "getElementsByTagName", func(call goja.FunctionCall) goja.Value {
		return e.nodeList(getElementsByTagName(e.page.doc, argString(call.Argument(0))))
	})
	e.method(p, "getElementsByClassName", func(call goja.FunctionCall) goja.Value {
		return e.nodeList(getElementsByClassName(e.page.doc, argString(call.Argument(0))))
	})
	e.method(p, "querySelector", func(call goja.FunctionCall) goja.Value {
		return e.wrap(querySelector(e.page.doc, argString(call.Argument(0))))
	})
	e.method(p, "querySelectorAll", func(call goja.FunctionCall) goja.Value {
		return e.nodeList(querySelectorAll(e.page.doc, argString(call.Argument(0))))
	})
	e.method(p, "getElementsByName", func(call goja.FunctionCall) goja.Value {
		name := argString(call.Argument(0))
		var out []*html.Node
		for _, el := range descendants(e.page.doc) {
			if v, _ := getAttr(el, "name"); v == name {
				out = append(out, el)
			}
		}
		return e.nodeList(out)
	})
	e.method(p, "createElement", func(call goja.FunctionCall) goja.Value {
		return e.wrap(createElement(argString(call.Argument(0))))
	})
	e.method(p, "createElementNS", func(call goja.FunctionCall) goja.Value {
		return e.wrap(createElement(argString(call.Argument(1))))
	})
	e.method(p, "createTextNode", func(call goja.FunctionCall) goja.Value {
		return e.wrap(&html.Node{Type: html.TextNode, Data: argString(call.Argument(0))})
	})
	e.method(p, "createComment", func(call goja.FunctionCall) goja.Value {
		return e.wrap(&html.Node{Type: html.CommentNode, Data: argString(call.Argument(0))})
	})
	e.method(p, "createDocumentFragment", func(call goja.FunctionCall) goja.Value {
		frag := &html.Node{Type: html.ElementNode, Data: "#document-fragment"}
		o := e.wrap(frag)
		if obj, ok := o.(*goja.Object); ok {
			_ = obj.SetPrototype(e.protosRef.fragment)
		}
		return o
	})
	e.method(p, "write", func(call goja.FunctionCall) goja.Value {
		var b strings.Builder
		for _, a := range call.Arguments {
			b.WriteString(a.String())
		}
		e.page.documentWrite(b.String())
		return goja.Undefined()
	})
	e.method(p, "writeln", func(call goja.FunctionCall) goja.Value {
		var b strings.Builder
		for _, a := range call.Arguments {
			b.WriteString(a.String())
		}
		b.WriteString("\n")
		e.page.documentWrite(b.String())
		return goja.Undefined()
	})

	e.accessor(p, "documentElement", func(call goja.FunctionCall) goja.Value {
		return e.wrap(findElement(e.page.doc, "html"))
	}, nil)
	e.accessor(p, "head", func(call goja.FunctionCall) goja.Value {
		return e.wrap(findElement(e.page.doc, "head"))
	}, nil)
	e.accessor(p, "body", func(call goja.FunctionCall) goja.Value {
		return e.wrap(findElement(e.page.doc, "body"))
	}, nil)
	e.accessor(p, "title", func(call goja.FunctionCall) goja.Value {
		if t := findElement(e.page.doc, "title"); t != nil {
			return e.vm.ToValue(textContent(t))
		}
		return e.vm.ToValue("")
	}, func(call goja.FunctionCall) goja.Value {
		t := findElement(e.page.doc, "title")
		if t == nil {
			head := findElement(e.page.doc, "head")
			if head == nil {
				return goja.Undefined()
			}
			t = createElement("title")
			appendChild(head, t)
		}
		setTextContent(t, argString(call.Argument(0)))
		return goja.Undefined()
	})
	e.accessor(p, "cookie", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue(e.page.cookieString())
	}, func(call goja.FunctionCall) goja.Value {
		e.page.setCookieString(argString(call.Argument(0)))
		return goja.Undefined()
	})
	e.accessor(p, "readyState", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue(e.page.readyState)
	}, nil)
	e.accessor(p, "URL", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue(e.page.URL)
	}, nil)
	e.accessor(p, "documentURI", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue(e.page.URL)
	}, nil)
	e.accessor(p, "referrer", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue(e.page.Referrer)
	}, nil)
	e.accessor(p, "characterSet", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue("UTF-8")
	}, nil)
	e.accessor(p, "charset", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue("UTF-8")
	}, nil)
	e.accessor(p, "contentType", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue("text/html")
	}, nil)
	e.accessor(p, "location", func(call goja.FunctionCall) goja.Value {
		return e.locationObject()
	}, nil)
	e.accessor(p, "defaultView", func(call goja.FunctionCall) goja.Value {
		return e.vm.GlobalObject()
	}, nil)
	e.accessor(p, "activeElement", func(call goja.FunctionCall) goja.Value {
		return e.wrap(findElement(e.page.doc, "body"))
	}, nil)
	e.accessor(p, "forms", func(call goja.FunctionCall) goja.Value {
		return e.nodeList(getElementsByTagName(e.page.doc, "form"))
	}, nil)
	e.accessor(p, "links", func(call goja.FunctionCall) goja.Value {
		return e.nodeList(getElementsByTagName(e.page.doc, "a"))
	}, nil)
	e.accessor(p, "images", func(call goja.FunctionCall) goja.Value {
		return e.nodeList(getElementsByTagName(e.page.doc, "img"))
	}, nil)
	e.accessor(p, "scripts", func(call goja.FunctionCall) goja.Value {
		return e.nodeList(getElementsByTagName(e.page.doc, "script"))
	}, nil)
}

// --- classList / style / dataset ---

func (e *jsEnv) classListObject(n *html.Node) goja.Value {
	if n == nil {
		return goja.Null()
	}
	o := e.vm.NewObject()
	_ = o.Set("add", func(call goja.FunctionCall) goja.Value {
		cs := classList(n)
		for _, a := range call.Arguments {
			c := a.String()
			if !containsStr(cs, c) {
				cs = append(cs, c)
			}
		}
		setAttr(n, "class", strings.Join(cs, " "))
		return goja.Undefined()
	})
	_ = o.Set("remove", func(call goja.FunctionCall) goja.Value {
		cs := classList(n)
		for _, a := range call.Arguments {
			cs = removeStr(cs, a.String())
		}
		setAttr(n, "class", strings.Join(cs, " "))
		return goja.Undefined()
	})
	_ = o.Set("toggle", func(call goja.FunctionCall) goja.Value {
		c := argString(call.Argument(0))
		cs := classList(n)
		if containsStr(cs, c) {
			cs = removeStr(cs, c)
			setAttr(n, "class", strings.Join(cs, " "))
			return e.vm.ToValue(false)
		}
		cs = append(cs, c)
		setAttr(n, "class", strings.Join(cs, " "))
		return e.vm.ToValue(true)
	})
	_ = o.Set("contains", func(call goja.FunctionCall) goja.Value {
		return e.vm.ToValue(containsStr(classList(n), argString(call.Argument(0))))
	})
	_ = o.Set("item", func(call goja.FunctionCall) goja.Value {
		i := int(call.Argument(0).ToInteger())
		cs := classList(n)
		if i < 0 || i >= len(cs) {
			return goja.Null()
		}
		return e.vm.ToValue(cs[i])
	})
	_ = o.Set("length", len(classList(n)))
	_ = o.Set("value", strings.Join(classList(n), " "))
	return o
}

func styleProps(n *html.Node) map[string]string {
	m := map[string]string{}
	v, _ := getAttr(n, "style")
	for _, decl := range strings.Split(v, ";") {
		k, val, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		m[strings.TrimSpace(k)] = strings.TrimSpace(val)
	}
	return m
}

func writeStyle(n *html.Node, prop, val string) {
	m := styleProps(n)
	key := camelToKebab(prop)
	if val == "" {
		delete(m, key)
	} else {
		m[key] = val
	}
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, k+": "+v)
	}
	setAttr(n, "style", strings.Join(parts, "; "))
}

func (e *jsEnv) styleObject(n *html.Node) goja.Value {
	if n == nil {
		return goja.Null()
	}
	target := e.vm.NewObject()
	proxy := e.vm.NewProxy(target, &goja.ProxyTrapConfig{
		Get: func(_ *goja.Object, prop string, _ goja.Value) goja.Value {
			switch prop {
			case "cssText":
				v, _ := getAttr(n, "style")
				return e.vm.ToValue(v)
			case "length":
				return e.vm.ToValue(len(styleProps(n)))
			case "getPropertyValue":
				return e.vm.ToValue(func(call goja.FunctionCall) goja.Value {
					return e.vm.ToValue(styleProps(n)[camelToKebab(argString(call.Argument(0)))])
				})
			case "setProperty":
				return e.vm.ToValue(func(call goja.FunctionCall) goja.Value {
					writeStyle(n, argString(call.Argument(0)), argString(call.Argument(1)))
					return goja.Undefined()
				})
			case "removeProperty":
				return e.vm.ToValue(func(call goja.FunctionCall) goja.Value {
					writeStyle(n, argString(call.Argument(0)), "")
					return goja.Undefined()
				})
			}
			return e.vm.ToValue(styleProps(n)[camelToKebab(prop)])
		},
		Set: func(_ *goja.Object, prop string, value goja.Value, _ goja.Value) bool {
			if prop == "cssText" {
				setAttr(n, "style", argString(value))
				return true
			}
			writeStyle(n, prop, argString(value))
			return true
		},
		Has: func(_ *goja.Object, prop string) bool {
			_, ok := styleProps(n)[camelToKebab(prop)]
			return ok
		},
	})
	return e.vm.ToValue(proxy)
}

func (e *jsEnv) datasetObject(n *html.Node) goja.Value {
	if n == nil {
		return goja.Null()
	}
	target := e.vm.NewObject()
	proxy := e.vm.NewProxy(target, &goja.ProxyTrapConfig{
		Get: func(_ *goja.Object, prop string, _ goja.Value) goja.Value {
			v, _ := getAttr(n, "data-"+camelToKebab(prop))
			return e.vm.ToValue(v)
		},
		Set: func(_ *goja.Object, prop string, value goja.Value, _ goja.Value) bool {
			setAttr(n, "data-"+camelToKebab(prop), argString(value))
			return true
		},
		Has: func(_ *goja.Object, prop string) bool {
			return hasAttr(n, "data-"+camelToKebab(prop))
		},
	})
	return e.vm.ToValue(proxy)
}

// newStyleObject is used for window.getComputedStyle stubs.
func (e *jsEnv) newStyleObject(n *html.Node) goja.Value {
	if n == nil {
		o := e.vm.NewObject()
		_ = o.Set("getPropertyValue", func(goja.FunctionCall) goja.Value { return e.vm.ToValue("") })
		_ = o.Set("cssText", "")
		return o
	}
	return e.styleObject(n)
}

// --- helpers ---

func cloneNode(n *html.Node, deep bool) *html.Node {
	c := &html.Node{
		Type:      n.Type,
		DataAtom:  n.DataAtom,
		Data:      n.Data,
		Namespace: n.Namespace,
	}
	c.Attr = append(c.Attr, n.Attr...)
	if deep {
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			appendChild(c, cloneNode(ch, true))
		}
	}
	return c
}

func camelToKebab(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r + 32)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func removeStr(s []string, v string) []string {
	out := s[:0]
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}
