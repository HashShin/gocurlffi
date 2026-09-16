package browser

import (
	"sync"

	"github.com/antchfx/xpath"
	"golang.org/x/net/html"
)

// xpathCache holds compiled expressions. Compilation dominates the cost of a
// query, and pages issue the same ones repeatedly, so it is process-wide like
// the CSS selector cache.
var xpathCache sync.Map // string -> *xpath.Expr

func compileXPath(expr string) (*xpath.Expr, error) {
	if v, ok := xpathCache.Load(expr); ok {
		return v.(*xpath.Expr), nil
	}
	e, err := xpath.Compile(expr)
	if err != nil {
		return nil, err
	}
	xpathCache.Store(expr, e)
	return e, nil
}

// nodeNav is an xpath.NodeNavigator over the DOM, which is the x/net/html node
// tree itself. A cursor is (element, attribute index); attr is -1 while the
// cursor is on the node itself.
type nodeNav struct {
	root *html.Node
	node *html.Node
	attr int
}

func navRoot(n *html.Node) *html.Node {
	for n != nil && n.Parent != nil {
		n = n.Parent
	}
	return n
}

func (n *nodeNav) NodeType() xpath.NodeType {
	if n.attr >= 0 {
		return xpath.AttributeNode
	}
	switch n.node.Type {
	case html.DocumentNode:
		return xpath.RootNode
	case html.ElementNode:
		return xpath.ElementNode
	case html.TextNode:
		return xpath.TextNode
	case html.CommentNode:
		return xpath.CommentNode
	}
	return xpath.ElementNode
}

func (n *nodeNav) LocalName() string {
	if n.attr >= 0 && n.attr < len(n.node.Attr) {
		return n.node.Attr[n.attr].Key
	}
	if n.node.Type == html.ElementNode {
		return n.node.Data
	}
	return ""
}

func (n *nodeNav) Prefix() string { return "" }

func (n *nodeNav) Value() string {
	if n.attr >= 0 && n.attr < len(n.node.Attr) {
		return n.node.Attr[n.attr].Val
	}
	switch n.node.Type {
	case html.TextNode, html.CommentNode:
		return n.node.Data
	default:
		return textContent(n.node)
	}
}

func (n *nodeNav) Copy() xpath.NodeNavigator {
	c := *n
	return &c
}

func (n *nodeNav) MoveToRoot() {
	n.node = n.root
	n.attr = -1
}

func (n *nodeNav) MoveToParent() bool {
	if n.attr >= 0 {
		n.attr = -1
		return true
	}
	if n.node.Parent == nil {
		return false
	}
	n.node = n.node.Parent
	return true
}

func (n *nodeNav) MoveToNextAttribute() bool {
	if n.attr >= 0 {
		return false
	}
	if len(n.node.Attr) == 0 {
		return false
	}
	n.attr = 0
	return true
}

func (n *nodeNav) MoveToChild() bool {
	c := contentChild(n.node.FirstChild)
	if c == nil {
		return false
	}
	n.node = c
	n.attr = -1
	return true
}

func (n *nodeNav) MoveToFirst() bool {
	return n.MoveToPrevious()
}

func (n *nodeNav) MoveToNext() bool {
	s := contentSibling(n.node.NextSibling, 1)
	if s == nil {
		return false
	}
	n.node = s
	n.attr = -1
	return true
}

func (n *nodeNav) MoveToPrevious() bool {
	s := contentSibling(n.node.PrevSibling, -1)
	if s == nil {
		return false
	}
	n.node = s
	n.attr = -1
	return true
}

func (n *nodeNav) MoveTo(other xpath.NodeNavigator) bool {
	o, ok := other.(*nodeNav)
	if !ok {
		return false
	}
	n.node, n.attr = o.node, o.attr
	return true
}

// contentChild returns the first child the navigator exposes: elements, text
// and comments, but not doctypes.
func contentChild(n *html.Node) *html.Node {
	for n != nil && !isContent(n) {
		n = n.NextSibling
	}
	return n
}

func contentSibling(n *html.Node, dir int) *html.Node {
	for n != nil && !isContent(n) {
		if dir > 0 {
			n = n.NextSibling
		} else {
			n = n.PrevSibling
		}
	}
	return n
}

func isContent(n *html.Node) bool {
	switch n.Type {
	case html.ElementNode, html.TextNode, html.CommentNode:
		return true
	}
	return false
}

// XPathResult is the outcome of evaluating an expression: a node set, a number,
// a string or a boolean.
type XPathResult struct {
	Nodes   []*html.Node
	Number  float64
	String  string
	Boolean bool
	Kind    string // "nodes", "number", "string", "boolean"
}

func evalXPath(root *html.Node, expr string) (XPathResult, error) {
	e, err := compileXPath(expr)
	if err != nil {
		return XPathResult{}, err
	}
	if root == nil {
		return XPathResult{Kind: "nodes"}, nil
	}
	switch v := e.Evaluate(&nodeNav{root: navRoot(root), node: root, attr: -1}).(type) {
	case *xpath.NodeIterator:
		var nodes []*html.Node
		for v.MoveNext() {
			if n, ok := v.Current().(*nodeNav); ok {
				nodes = append(nodes, n.node)
			}
		}
		return XPathResult{Nodes: nodes, Kind: "nodes"}, nil
	case float64:
		return XPathResult{Number: v, Kind: "number"}, nil
	case bool:
		return XPathResult{Boolean: v, Kind: "boolean"}, nil
	case string:
		return XPathResult{String: v, Kind: "string"}, nil
	default:
		return XPathResult{Kind: "nodes"}, nil
	}
}

// QueryXPath evaluates an XPath expression against the document and returns the
// matched nodes. A compile error or a scalar result yields no nodes; use
// Page.XPath when the type of the result matters.
func (p *Page) QueryXPath(expr string) []*html.Node {
	r, err := evalXPath(p.doc, expr)
	if err != nil {
		return nil
	}
	return r.Nodes
}

// QueryXPathFirst returns the first node matching expr, searching from `from`
// (the document when from is nil).
func (p *Page) QueryXPathFirst(expr string, from *html.Node) *html.Node {
	if from == nil {
		from = p.doc
	}
	nodes := p.QueryXPathFrom(expr, from)
	if len(nodes) == 0 {
		return nil
	}
	return nodes[0]
}

// QueryXPathFrom evaluates expr with `from` as the context node, so a relative
// expression such as ".//a" searches only that subtree.
func (p *Page) QueryXPathFrom(expr string, from *html.Node) []*html.Node {
	r, err := evalXPath(from, expr)
	if err != nil {
		return nil
	}
	return r.Nodes
}

// XPath evaluates expr as a general expression, returning its typed result.
func (p *Page) XPath(expr string) (XPathResult, error) {
	return evalXPath(p.doc, expr)
}
