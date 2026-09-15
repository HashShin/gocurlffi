package server

import (
	"sync"

	"github.com/dop251/goja"
	"golang.org/x/net/html"
)

// refTable stores the JavaScript objects a client holds an objectId for, so
// Runtime.callFunctionOn and getProperties can reach them again. Ids are
// per-target, matching CDP's scoping.
type refTable struct {
	mu  sync.Mutex
	n   int
	val map[string]goja.Value
}

func newRefTable() *refTable { return &refTable{val: map[string]goja.Value{}} }

func (r *refTable) add(v goja.Value) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.n++
	id := "obj:" + itoa(r.n)
	r.val[id] = v
	return id
}

func (r *refTable) get(id string) goja.Value {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.val[id]
}

func (r *refTable) release(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.val, id)
}

// nodeRef identifies a DOM node for the DOM domain.
type nodeRef struct {
	id   int
	node *html.Node
}

func (t *target) addNode(n *html.Node) int {
	if n == nil {
		return 0
	}
	for id, ref := range t.domNodes {
		if ref.node == n {
			return id
		}
	}
	t.domSeq++
	t.domNodes[t.domSeq] = &nodeRef{id: t.domSeq, node: n}
	return t.domSeq
}

func (t *target) nodeByID(id int) *html.Node {
	if ref, ok := t.domNodes[id]; ok {
		return ref.node
	}
	return nil
}

// remoteObject serializes a JavaScript value the way CDP reports it. A primitive
// carries its value; anything else carries an objectId the client can use again.
func (t *target) remoteObject(v goja.Value) map[string]any {
	if v == nil || goja.IsUndefined(v) {
		return map[string]any{"type": "undefined"}
	}
	if goja.IsNull(v) {
		return map[string]any{"type": "object", "subtype": "null", "value": nil}
	}
	switch x := v.Export().(type) {
	case bool:
		return map[string]any{"type": "boolean", "value": x}
	case string:
		return map[string]any{"type": "string", "value": x}
	case int64:
		return map[string]any{"type": "number", "value": x, "description": itoa64(x)}
	case int:
		return map[string]any{"type": "number", "value": x}
	case float64:
		return map[string]any{"type": "number", "value": x}
	case float32:
		return map[string]any{"type": "number", "value": float64(x)}
	case nil:
		return map[string]any{"type": "object", "subtype": "null", "value": nil}
	}
	if _, ok := goja.AssertFunction(v); ok {
		return map[string]any{
			"type":        "function",
			"className":   "Function",
			"description": "function",
			"objectId":    t.refs.add(v),
		}
	}
	desc := v.String()
	className := "Object"
	if rt := t.page.Runtime(); rt != nil {
		if o := v.ToObject(rt); o != nil && o.ClassName() != "" {
			className = o.ClassName()
		}
	}
	return map[string]any{
		"type":        "object",
		"className":   className,
		"description": desc,
		"objectId":    t.refs.add(v),
	}
}

// describeNode serializes a DOM node for the DOM domain.
func (t *target) describeNode(n *html.Node, depth int) map[string]any {
	if n == nil {
		return nil
	}
	out := map[string]any{
		"nodeId":        t.addNode(n),
		"backendNodeId": t.addNode(n),
		"nodeType":      cdpNodeType(n),
		"nodeName":      cdpNodeName(n),
		"nodeValue":     cdpNodeValue(n),
	}
	if n.Type == html.ElementNode {
		out["localName"] = n.Data
		var attrs []string
		for _, a := range n.Attr {
			attrs = append(attrs, a.Key, a.Val)
		}
		out["attributes"] = attrs
	}
	var children []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode || c.Type == html.TextNode || c.Type == html.CommentNode {
			children = append(children, c)
		}
	}
	out["childNodeCount"] = len(children)
	if depth != 0 {
		var kids []map[string]any
		for _, c := range children {
			kids = append(kids, t.describeNode(c, depth-1))
		}
		if kids == nil {
			kids = []map[string]any{}
		}
		out["children"] = kids
	}
	return out
}

func cdpNodeType(n *html.Node) int {
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
	default:
		return 11
	}
}

func cdpNodeName(n *html.Node) string {
	switch n.Type {
	case html.DocumentNode:
		return "#document"
	case html.TextNode:
		return "#text"
	case html.CommentNode:
		return "#comment"
	case html.DoctypeNode:
		return n.Data
	default:
		return n.Data
	}
}

func cdpNodeValue(n *html.Node) string {
	if n.Type == html.TextNode || n.Type == html.CommentNode {
		return n.Data
	}
	return ""
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
