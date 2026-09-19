package browser

import (
	"mime"
	"strings"

	"golang.org/x/net/html"
)

// How a fetched document is turned into a DOM depends on its media type. A
// browser parses markup and runs its scripts; anything else it shows as text,
// and nothing in that text can execute. Getting this wrong is not cosmetic: a
// text/plain body containing <script> would run if it were parsed as HTML.

// plainTextViewerStyle is the inline style Chrome's plain-text viewer puts on
// its <pre>, kept verbatim so the layout matches a real browser's.
const plainTextViewerStyle = "word-wrap: break-word; white-space: pre-wrap;"

// documentContentType is the media type a document was served as, which is what
// document.contentType reports. Parameters (charset) are dropped, and a
// response without a Content-Type is treated as HTML, as a browser does when it
// has to fall back.
func documentContentType(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return "text/html"
	}
	if mt, _, err := mime.ParseMediaType(header); err == nil && mt != "" {
		return strings.ToLower(mt)
	}
	// A malformed header still names a type before its first parameter; keep
	// that rather than defaulting to HTML, which would run scripts.
	if i := strings.IndexByte(header, ';'); i >= 0 {
		header = header[:i]
	}
	if mt := strings.ToLower(strings.TrimSpace(header)); mt != "" {
		return mt
	}
	return "text/html"
}

// isMarkupType reports whether a document of this media type is parsed and
// scripted. XML is left to the HTML parser, which is how this package has always
// treated it; everything else goes to the plain-text viewer.
func isMarkupType(mediaType string) bool {
	switch mediaType {
	case "text/html", "application/xhtml+xml", "text/xml", "application/xml":
		return true
	}
	return strings.HasSuffix(mediaType, "+xml")
}

// plainTextDocument builds the document a browser shows for a response that is
// not markup: the bytes as text inside a <pre>, with no elements and no scripts
// to find. The nodes are built directly rather than parsed, so nothing in the
// text can be mistaken for markup however it is written.
func plainTextDocument(text string) *html.Node {
	// The root is a document node wrapping <html>, as html.Parse returns; the
	// JavaScript document object is wrapped from it, and an element root would
	// be wrapped as an element instead.
	doc := &html.Node{Type: html.DocumentNode}
	htmlEl := &html.Node{Type: html.ElementNode, Data: "html"}
	head := &html.Node{Type: html.ElementNode, Data: "head"}
	body := &html.Node{Type: html.ElementNode, Data: "body"}
	pre := &html.Node{
		Type: html.ElementNode,
		Data: "pre",
		Attr: []html.Attribute{{Key: "style", Val: plainTextViewerStyle}},
	}
	pre.AppendChild(&html.Node{Type: html.TextNode, Data: text})
	htmlEl.AppendChild(head)
	htmlEl.AppendChild(body)
	body.AppendChild(pre)
	doc.AppendChild(htmlEl)
	return doc
}
