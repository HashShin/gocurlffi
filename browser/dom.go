package browser

import (
	"bytes"
	"strings"
	"sync"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// The DOM is golang.org/x/net/html's node tree. These helpers give it the
// mutating, querying behaviour that page scripts expect.

// childNodes returns n's child nodes as a slice.
func childNodes(n *html.Node) []*html.Node {
	if n == nil {
		return nil
	}
	out := make([]*html.Node, 0, 4)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		out = append(out, c)
	}
	return out
}

// elementChildren returns only element children.
func elementChildren(n *html.Node) []*html.Node {
	out := make([]*html.Node, 0, 4)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			out = append(out, c)
		}
	}
	return out
}

// isAncestor reports whether a is b or an ancestor of b.
func isAncestor(a, b *html.Node) bool {
	for cur := b; cur != nil; cur = cur.Parent {
		if cur == a {
			return true
		}
	}
	return false
}

func appendChild(parent, child *html.Node) *html.Node {
	if parent == nil || child == nil || isAncestor(child, parent) {
		// Refuse to create a cycle, which a real DOM also rejects.
		return child
	}
	if child.Parent != nil {
		removeChild(child)
	}
	child.Parent = parent
	child.PrevSibling = parent.LastChild
	child.NextSibling = nil
	if parent.LastChild != nil {
		parent.LastChild.NextSibling = child
	} else {
		parent.FirstChild = child
	}
	parent.LastChild = child
	return child
}

func removeChild(child *html.Node) *html.Node {
	if child.Parent == nil {
		return child
	}
	p := child.Parent
	if child.PrevSibling != nil {
		child.PrevSibling.NextSibling = child.NextSibling
	} else {
		p.FirstChild = child.NextSibling
	}
	if child.NextSibling != nil {
		child.NextSibling.PrevSibling = child.PrevSibling
	} else {
		p.LastChild = child.PrevSibling
	}
	child.Parent = nil
	child.PrevSibling = nil
	child.NextSibling = nil
	return child
}

func insertBefore(parent, child, ref *html.Node) *html.Node {
	if ref == nil {
		return appendChild(parent, child)
	}
	if parent == nil || child == nil || isAncestor(child, parent) {
		return child
	}
	if child.Parent != nil {
		removeChild(child)
	}
	child.Parent = parent
	child.NextSibling = ref
	child.PrevSibling = ref.PrevSibling
	if ref.PrevSibling != nil {
		ref.PrevSibling.NextSibling = child
	} else {
		parent.FirstChild = child
	}
	ref.PrevSibling = child
	return child
}

func replaceChild(parent, newNode, oldNode *html.Node) *html.Node {
	if parent == nil || newNode == nil || oldNode == nil || isAncestor(newNode, parent) {
		return oldNode
	}
	insertBefore(parent, newNode, oldNode)
	removeChild(oldNode)
	return oldNode
}

// getAttr returns the attribute value and whether it exists.
func getAttr(n *html.Node, key string) (string, bool) {
	if n == nil {
		return "", false
	}
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

func setAttr(n *html.Node, key, val string) {
	if n == nil {
		return
	}
	for i := range n.Attr {
		if n.Attr[i].Key == key {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func removeAttr(n *html.Node, key string) {
	if n == nil {
		return
	}
	for i := range n.Attr {
		if n.Attr[i].Key == key {
			n.Attr = append(n.Attr[:i], n.Attr[i+1:]...)
			return
		}
	}
}

func hasAttr(n *html.Node, key string) bool {
	_, ok := getAttr(n, key)
	return ok
}

// id returns the element's id attribute.
func id(n *html.Node) string { v, _ := getAttr(n, "id"); return v }

// classList returns the whitespace separated classes.
func classList(n *html.Node) []string {
	v, _ := getAttr(n, "class")
	return strings.Fields(v)
}

func hasClass(n *html.Node, c string) bool {
	for _, x := range classList(n) {
		if x == c {
			return true
		}
	}
	return false
}

// tagName returns the uppercase tag name a DOM element exposes.
func tagName(n *html.Node) string {
	if n == nil {
		return ""
	}
	return strings.ToUpper(n.Data)
}

// textContent returns the concatenated text of a subtree.
func textContent(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
			return
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// setTextContent replaces a node's children with a single text node.
func setTextContent(n *html.Node, s string) {
	if n == nil {
		return
	}
	n.FirstChild = nil
	n.LastChild = nil
	if s == "" {
		return
	}
	appendChild(n, &html.Node{Type: html.TextNode, Data: s, Parent: n})
}

// innerHTML returns the serialized children of n.
func innerHTML(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b bytes.Buffer
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		_ = html.Render(&b, c)
	}
	return b.String()
}

// outerHTML serializes n including itself.
func outerHTML(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b bytes.Buffer
	if err := html.Render(&b, n); err != nil {
		return ""
	}
	return b.String()
}

// setInnerHTML parses a fragment in the context of n and replaces children.
func setInnerHTML(n *html.Node, s string) {
	if n == nil {
		return
	}
	n.FirstChild = nil
	n.LastChild = nil
	ctx := n
	if n.Type == html.DocumentNode {
		if b := findElement(n, "body"); b != nil {
			ctx = b
		}
	}
	nodes, err := html.ParseFragment(strings.NewReader(s), ctx)
	if err != nil {
		return
	}
	for _, c := range nodes {
		c.Parent = nil
		appendChild(n, c)
	}
}

// findElement returns the first descendant element with the given tag.
func findElement(n *html.Node, tag string) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == tag {
			return c
		}
		if got := findElement(c, tag); got != nil {
			return got
		}
	}
	return nil
}

func createElement(tag string) *html.Node {
	return &html.Node{
		Type:     html.ElementNode,
		DataAtom: atom.Lookup([]byte(strings.ToLower(tag))),
		Data:     strings.ToLower(tag),
	}
}

// --- Selector helpers (cascadia) ---

// Selector compilation is comparatively expensive and pages call
// querySelector/tAll thousands of times, so results are cached. Invalid
// selectors are cached too, so they are not re-parsed on every call.
var (
	selectorCacheMu sync.RWMutex
	selectorCache   = map[string]cascadia.Selector{}
	selectorInvalid = map[string]bool{}
)

func compileSelector(sel string) (cascadia.Selector, bool) {
	selectorCacheMu.RLock()
	if m, ok := selectorCache[sel]; ok {
		selectorCacheMu.RUnlock()
		return m, true
	}
	if selectorInvalid[sel] {
		selectorCacheMu.RUnlock()
		return nil, false
	}
	selectorCacheMu.RUnlock()

	m, err := cascadia.Compile(sel)
	selectorCacheMu.Lock()
	if err != nil {
		selectorInvalid[sel] = true
	} else {
		selectorCache[sel] = m
	}
	selectorCacheMu.Unlock()
	return m, err == nil
}

func querySelector(n *html.Node, sel string) *html.Node {
	m, ok := compileSelector(sel)
	if !ok {
		return nil
	}
	for _, c := range m.MatchAll(n) {
		if c != n {
			return c
		}
	}
	return nil
}

func querySelectorAll(n *html.Node, sel string) []*html.Node {
	m, ok := compileSelector(sel)
	if !ok {
		return nil
	}
	out := make([]*html.Node, 0, 8)
	for _, c := range m.MatchAll(n) {
		if c != n {
			out = append(out, c)
		}
	}
	return out
}

func matches(n *html.Node, sel string) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	m, ok := compileSelector(sel)
	if !ok {
		return false
	}
	return m.Match(n)
}

// descendants yields all element nodes under n in document order.
func descendants(n *html.Node) []*html.Node {
	out := make([]*html.Node, 0, 16)
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode {
				out = append(out, c)
			}
			walk(c)
		}
	}
	walk(n)
	return out
}

// getElementsByTagName returns descendant elements with tag ("*" = all).
func getElementsByTagName(n *html.Node, tag string) []*html.Node {
	tag = strings.ToLower(tag)
	out := make([]*html.Node, 0, 8)
	for _, e := range descendants(n) {
		if tag == "*" || e.Data == tag {
			out = append(out, e)
		}
	}
	return out
}

// getElementsByClassName returns descendants carrying all given classes.
func getElementsByClassName(n *html.Node, names string) []*html.Node {
	want := strings.Fields(names)
	out := make([]*html.Node, 0, 8)
outer:
	for _, e := range descendants(n) {
		for _, w := range want {
			if !hasClass(e, w) {
				continue outer
			}
		}
		out = append(out, e)
	}
	return out
}

// getElementById finds the first descendant element with the id.
func getElementById(n *html.Node, idv string) *html.Node {
	if idv == "" {
		return nil
	}
	for _, e := range descendants(n) {
		if id(e) == idv {
			return e
		}
	}
	return nil
}

// Nil-safe node navigators used by the JS bindings.

func parentOf(n *html.Node) *html.Node {
	if n == nil {
		return nil
	}
	return n.Parent
}

func firstChildOf(n *html.Node) *html.Node {
	if n == nil {
		return nil
	}
	return n.FirstChild
}

func lastChildOf(n *html.Node) *html.Node {
	if n == nil {
		return nil
	}
	return n.LastChild
}

func nextSiblingOf(n *html.Node) *html.Node {
	if n == nil {
		return nil
	}
	return n.NextSibling
}

func prevSiblingOf(n *html.Node) *html.Node {
	if n == nil {
		return nil
	}
	return n.PrevSibling
}

// visibleText returns text content, skipping non-rendered elements.
func visibleText(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
			return
		}
		if x.Type == html.ElementNode {
			switch x.Data {
			case "script", "style", "noscript", "template", "head", "svg":
				return
			}
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}
