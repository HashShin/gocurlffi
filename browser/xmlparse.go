package browser

import (
	"encoding/xml"
	"errors"
	"strings"

	"github.com/dop251/goja"
	"golang.org/x/net/html"
)

// errUnclosedXML reports XML that ends inside an element.
var errUnclosedXML = errors.New("XML parse error: unclosed element")

// XML parsing for DOMParser. The parser used to ignore its type argument
// entirely, so `parseFromString(source, 'text/xml')` returned an HTML document
// whose documentElement was <html>. Code that sniffs text/xml to tell a feed
// from a page silently got the wrong answer.
//
// encoding/xml is a namespace-aware parser, so XML input is parsed with it and
// lowered into the same *html.Node tree the rest of the browser walks. Names
// keep their case, because XML is case-sensitive.

// xmlTypes are the MIME types that select the XML parser.
var xmlTypes = map[string]bool{
	"text/xml":              true,
	"application/xml":       true,
	"application/xhtml+xml": true,
	"image/svg+xml":         true,
}

// isXMLType reports whether a DOMParser type selects the XML parser.
func isXMLType(typ string) bool {
	// Strip parameters such as ";charset=utf-8".
	if i := strings.IndexByte(typ, ';'); i >= 0 {
		typ = typ[:i]
	}
	return xmlTypes[strings.ToLower(strings.TrimSpace(typ))]
}

// parseXMLDocument parses source as XML and returns a fragment holder whose
// children are the XML roots. A parse error yields a holder containing
// <parsererror>, which is what a browser returns rather than throwing.
func (e *jsEnv) parseXMLDocument(source string) *html.Node {
	holder, err := parseXMLInto(source)
	if err != nil {
		holder = &html.Node{Type: html.ElementNode, Data: "#xml-holder"}
		holder.AppendChild(parserErrorNode(err.Error()))
	}
	return holder
}

// parseXMLInto builds an *html.Node tree from XML source. The returned node is
// a synthetic document element wrapper holding the XML root, so it can be
// appended to a Document node.
func parseXMLInto(source string) (*html.Node, error) {
	dec := xml.NewDecoder(strings.NewReader(source))
	// Keep names verbatim: XML is case-sensitive, unlike HTML.
	dec.Strict = true

	// A fragment holder mirrors the document element that encoding/xml does not
	// emit; the XML root becomes its only child.
	holder := &html.Node{Type: html.ElementNode, Data: "#xml-holder"}
	var stack []*html.Node
	stack = append(stack, holder)

	for {
		tok, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &html.Node{Type: html.ElementNode, Data: t.Name.Local}
			if t.Name.Space != "" {
				n.Attr = append(n.Attr, html.Attribute{Key: "xmlns", Val: t.Name.Space})
			}
			for _, a := range t.Attr {
				key := a.Name.Local
				if a.Name.Space != "" {
					key = a.Name.Space + ":" + a.Name.Local
				}
				n.Attr = append(n.Attr, html.Attribute{Key: key, Val: a.Value})
			}
			parent := stack[len(stack)-1]
			parent.AppendChild(n)
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			text := string(t)
			if strings.TrimSpace(text) == "" && len(stack) == 1 {
				continue
			}
			stack[len(stack)-1].AppendChild(&html.Node{Type: html.TextNode, Data: text})
		case xml.Comment:
			stack[len(stack)-1].AppendChild(&html.Node{Type: html.CommentNode, Data: string(t)})
		case xml.ProcInst, xml.Directive:
			// Declarations and processing instructions are not part of the tree.
		}
	}
	if len(stack) != 1 {
		return nil, errUnclosedXML
	}
	return holder, nil
}

// parserErrorNode builds the <parsererror> a browser returns for bad XML.
func parserErrorNode(message string) *html.Node {
	root := &html.Node{Type: html.ElementNode, Data: "parsererror"}
	root.AppendChild(&html.Node{Type: html.TextNode, Data: message})
	return root
}

// newXMLDocumentObject wraps a parsed XML document so it behaves like the
// document a browser hands back: documentElement, querySelector, getElementById
// and getElementsByTagName work, and the interface reports XMLDocument.
//
// The holder element is transparent: documentElement is the XML root itself.
func (e *jsEnv) newXMLDocumentObject(holder *html.Node) *goja.Object {
	// The holder is a real element in the tree so the existing traversal
	// helpers can walk it. Replace it with the document node the rest of the
	// code expects, keeping its children.
	doc := &html.Node{Type: html.DocumentNode}
	for c := holder.FirstChild; c != nil; {
		next := c.NextSibling
		holder.RemoveChild(c)
		doc.AppendChild(c)
		c = next
	}

	o := e.vm.NewObject()
	_ = o.SetPrototype(e.protosRef.document)
	e.tagObject(o, "XMLDocument")
	_ = o.DefineDataPropertySymbol(e.nodeSym, e.vm.ToValue(doc),
		goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_FALSE)
	// documentElement answers the XML root rather than a synthetic <html>.
	_ = o.DefineAccessorProperty("documentElement",
		e.vm.ToValue(func(goja.FunctionCall) goja.Value {
			for c := doc.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode {
					return e.wrap(c)
				}
			}
			return goja.Null()
		}), nil, goja.FLAG_TRUE, goja.FLAG_FALSE)
	_ = o.Set("documentURI", e.page.URL)
	if e.page.xmlDocs == nil {
		e.page.xmlDocs = map[*html.Node]bool{}
	}
	e.page.xmlDocs[doc] = true
	e.nodes[doc] = o
	return o
}
