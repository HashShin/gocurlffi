package browser

import (
	"strconv"
	"strings"

	"github.com/dop251/goja"
	"golang.org/x/net/html"
)

// Shadow DOM. Before this, attachShadow did not exist and getRootNode always
// answered the document, so a web component either threw on construction or
// put its content somewhere no extraction could see it. Here a shadow tree is
// kept beside the host, traversal reads through it the way rendering does, and
// the declarative form (<template shadowrootmode="open">) is honoured on load
// so server-rendered components are not invisible.

// shadowRoot is one attached shadow tree.
type shadowRoot struct {
	host    *html.Node
	content *html.Node // a #document-fragment holding the shadow children
	mode    string     // "open" or "closed"
}

// attachShadow creates (or returns) the shadow root of a host element.
func (p *Page) attachShadow(host *html.Node, mode string) *shadowRoot {
	if host == nil {
		return nil
	}
	if sr, ok := p.shadowOf(host); ok {
		return sr
	}
	if mode != "closed" {
		mode = "open"
	}
	sr := &shadowRoot{
		host:    host,
		content: &html.Node{Type: html.ElementNode, Data: "#document-fragment"},
		mode:    mode,
	}
	if p.shadows == nil {
		p.shadows = map[*html.Node]*shadowRoot{}
	}
	p.shadows[host] = sr
	if p.shadowsByContent == nil {
		p.shadowsByContent = map[*html.Node]*shadowRoot{}
	}
	p.shadowsByContent[sr.content] = sr
	return sr
}

// shadowRootOfNode returns the shadow root a node lives in, if any, by walking
// up to the fragment that holds it.
func (p *Page) shadowRootOfNode(n *html.Node) (*shadowRoot, bool) {
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.Parent == nil {
			return nil, false
		}
		if sr, ok := p.shadowsByContent[cur.Parent]; ok {
			return sr, true
		}
	}
	return nil, false
}

// shadowOf reports the shadow root attached to a host.
func (p *Page) shadowOf(host *html.Node) (*shadowRoot, bool) {
	if p.shadows == nil || host == nil {
		return nil, false
	}
	sr, ok := p.shadows[host]
	return sr, ok
}

// isXMLNode reports whether a node came from an XML parse. tagName uppercases
// for HTML but must preserve case for XML, so the two have to be told apart.
// The map is nil on every page that never parsed XML, so this costs a length
// check there.
func (p *Page) isXMLNode(n *html.Node) bool {
	if len(p.xmlDocs) == 0 || n == nil {
		return false
	}
	for cur := n; cur != nil; cur = cur.Parent {
		if p.xmlDocs[cur] {
			return true
		}
	}
	return false
}

// isConnected reports whether a node is in the document, following a shadow
// boundary back to its host. A node with a parent is not necessarily connected:
// a tree built with innerHTML and never inserted is detached.
func (p *Page) isConnected(n *html.Node) bool {
	for cur := n; cur != nil; cur = cur.Parent {
		if cur == p.doc {
			return true
		}
		if sr, ok := p.shadowsByContent[cur]; ok {
			return p.isConnected(sr.host)
		}
	}
	return false
}

// ShadowRoots returns every attached shadow root in document order, so a
// caller can inspect the trees extraction walked through.
func (p *Page) ShadowRoots() []*shadowRoot {
	if len(p.shadows) == 0 {
		return nil
	}
	var out []*shadowRoot
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if sr, ok := p.shadowOf(c); ok {
				out = append(out, sr)
				walk(sr.content)
			}
			walk(c)
		}
	}
	walk(p.doc)
	return out
}

// Host returns the element the shadow root is attached to.
func (sr *shadowRoot) Host() *html.Node { return sr.host }

// Mode reports "open" or "closed".
func (sr *shadowRoot) Mode() string { return sr.mode }

// Content returns the fragment holding the shadow children.
func (sr *shadowRoot) Content() *html.Node { return sr.content }

// --- rendered tree ----------------------------------------------------------

// flattenShadow returns a copy of n in which each shadow host's rendered
// subtree replaces the host's light children, and each <slot> is replaced by the
// light children assigned to it. The copy is an ordinary tree, so the existing
// extraction code walks web-component content without knowing about shadow DOM.
//
// The copy exists only for reading: mutating it does not touch the page. The
// query helpers below therefore resolve against the live tree instead of it.
func (p *Page) flattenShadow(n *html.Node) *html.Node { return p.flattenInto(n, nil) }

func (p *Page) flattenInto(n, host *html.Node) *html.Node {
	if n == nil {
		return nil
	}
	cp := &html.Node{Type: n.Type, Data: n.Data, DataAtom: n.DataAtom}
	if len(n.Attr) > 0 {
		cp.Attr = append([]html.Attribute(nil), n.Attr...)
	}
	// Only leaf nodes stop the descent. A Document or a shadow fragment is not
	// an element, but its children must still be copied.
	if n.Type == html.TextNode || n.Type == html.CommentNode || n.Type == html.DoctypeNode {
		return cp
	}
	var kids []*html.Node
	switch {
	case strings.EqualFold(n.Data, "slot") && host != nil:
		kids = childNodes(host)
	case true:
		if sr, ok := p.shadowOf(n); ok {
			kids = childNodes(sr.content)
			host = n
		} else {
			kids = childNodes(n)
		}
	}
	for _, k := range kids {
		c := p.flattenInto(k, host)
		if c == nil {
			continue
		}
		c.Parent = cp
		if cp.LastChild == nil {
			cp.FirstChild = c
		} else {
			cp.LastChild.NextSibling = c
			c.PrevSibling = cp.LastChild
		}
		cp.LastChild = c
	}
	return cp
}

// renderedText is the visible text of the rendered tree rooted at n. A document
// with no shadow roots takes the plain path, so the common case does not pay for
// the copy or the extra walk.
func (p *Page) renderedText(n *html.Node) string {
	if len(p.shadows) == 0 {
		return visibleText(n)
	}
	return visibleText(p.flattenShadow(n))
}

// allRendered returns every element with the given tag in the rendered tree.
func (p *Page) allRendered(n *html.Node, tag string) []*html.Node {
	if len(p.shadows) == 0 {
		return getElementsByTagName(n, tag)
	}
	return getElementsByTagName(p.flattenShadow(n), tag)
}

// queryRendered finds the first match in the rendered tree, returning a node
// from the live document rather than from the copy, so it is safe to act on.
func (p *Page) queryRendered(sel string) *html.Node {
	if n := querySelector(p.doc, sel); n != nil {
		return n
	}
	for _, sr := range p.ShadowRoots() {
		if n := querySelector(sr.content, sel); n != nil {
			return n
		}
	}
	return nil
}

// queryRenderedAll is queryRendered for every match.
func (p *Page) queryRenderedAll(sel string) []*html.Node {
	out := querySelectorAll(p.doc, sel)
	for _, sr := range p.ShadowRoots() {
		out = append(out, querySelectorAll(sr.content, sel)...)
	}
	return out
}

// --- declarative shadow DOM ------------------------------------------------

// attachDeclarativeShadowRoots honours <template shadowrootmode="...">, the form
// a server-rendered component ships. The parser leaves the template in the
// light tree, so its content is moved into a real shadow root and the template
// is dropped, which is what a browser's parser does.
func (p *Page) attachDeclarativeShadowRoots() {
	// Roots to scan. Attaching one shadow tree can reveal another template
	// inside it, so the queue grows as it is walked.
	roots := []*html.Node{p.doc}
	for i := 0; i < len(roots); i++ {
		for _, t := range getElementsByTagName(roots[i], "template") {
			mode := strings.ToLower(attrOf(t, "shadowrootmode"))
			if mode == "" {
				mode = strings.ToLower(attrOf(t, "shadowroot"))
			}
			if mode != "open" && mode != "closed" {
				continue
			}
			host := t.Parent
			if host == nil {
				continue
			}
			sr := p.attachShadow(host, mode)
			if sr == nil {
				continue
			}
			content := p.templateContentNode(t)
			for c := content.FirstChild; c != nil; {
				next := c.NextSibling
				removeChild(c)
				appendChild(sr.content, c)
				c = next
			}
			removeChild(t)
			roots = append(roots, sr.content)
		}
	}
}

// --- JS surface -------------------------------------------------------------

// shadowRootObject wraps a shadow root. The content fragment already carries the
// DocumentFragment prototype (so querySelector, innerHTML, append and friends
// work), and this adds the ShadowRoot-only members.
func (e *jsEnv) shadowRootObject(sr *shadowRoot) *goja.Object {
	o, ok := e.wrapFragment(sr.content).(*goja.Object)
	if !ok {
		return nil
	}
	e.tagObject(o, "ShadowRoot")
	_ = o.Set("host", e.wrap(sr.host))
	_ = o.Set("mode", sr.mode)
	_ = o.Set("getElementById", func(call goja.FunctionCall) goja.Value {
		id := argString(call.Argument(0))
		var found *html.Node
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if found != nil {
					return
				}
				if c.Type == html.ElementNode && attrOf(c, "id") == id {
					found = c
					return
				}
				walk(c)
			}
		}
		walk(sr.content)
		if found == nil {
			return goja.Null()
		}
		return e.wrap(found)
	})
	_ = o.Set("elementFromPoint", func(goja.FunctionCall) goja.Value { return goja.Null() })
	_ = o.Set("styleSheets", e.vm.NewArray())
	return o
}

// installShadow adds attachShadow and the shadowRoot accessor to the element
// prototype, and teaches getRootNode about shadow boundaries.
func (e *jsEnv) installShadow() {
	p := e.protosRef
	if p == nil {
		return
	}
	e.method(p.element, "attachShadow", func(call goja.FunctionCall) goja.Value {
		host := e.thisNode(call)
		if host == nil {
			panic(e.vm.NewTypeError("attachShadow: no host element"))
		}
		mode := "open"
		if opts, ok := call.Argument(0).(*goja.Object); ok {
			if m := argString(opts.Get("mode")); m != "" {
				mode = m
			}
		}
		if _, exists := e.page.shadowOf(host); exists {
			panic(e.vm.NewTypeError("attachShadow: this element already hosts a shadow root"))
		}
		sr := e.page.attachShadow(host, mode)
		if sr == nil {
			panic(e.vm.NewTypeError("attachShadow: invalid host"))
		}
		return e.vm.ToValue(e.shadowRootObject(sr))
	})
	e.accessor(p.element, "shadowRoot", func(call goja.FunctionCall) goja.Value {
		sr, ok := e.page.shadowOf(e.thisNode(call))
		// A closed root is invisible to page script, as in a browser.
		if !ok || sr.mode != "open" {
			return goja.Null()
		}
		return e.vm.ToValue(e.shadowRootObject(sr))
	}, nil)
	// getRootNode returns the shadow root a node lives in, not always the
	// document, which is how a component finds its own tree.
	e.method(p.node, "getRootNode", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n == nil {
			return e.wrap(e.page.doc)
		}
		if sr, ok := e.page.shadowRootOfNode(n); ok {
			return e.vm.ToValue(e.shadowRootObject(sr))
		}
		return e.wrap(e.page.doc)
	})
}

// slotAssignment reports the light children a slot renders, for pages that ask.
func slotAssignedNodes(host *html.Node) []*html.Node {
	if host == nil {
		return nil
	}
	return childNodes(host)
}

// shadowCount is a small helper for diagnostics and tests.
func (p *Page) shadowCount() int { return len(p.shadows) }

// shadowDebug renders the shadow tree membership, used by --debug output.
func (p *Page) shadowDebug() []string {
	var out []string
	for _, sr := range p.ShadowRoots() {
		out = append(out, "shadow "+sr.mode+" on <"+nodeNameOf(sr.host)+">: "+
			strconv.Itoa(len(childNodes(sr.content)))+" child node(s)")
	}
	return out
}
