package browser

import (
	"encoding/json"
	"strings"

	"golang.org/x/net/html"
)

// Structured data extraction. Pages describe themselves with JSON-LD in a
// <script type="application/ld+json"> element (and with microdata attributes).
// Both are exposed so a scraper can read the page's own description of itself
// instead of guessing from the markup.

// StructuredData returns every JSON-LD object the document declares, in
// document order, including those in child frames. A block that fails to parse
// is skipped rather than failing the whole call.
func (p *Page) StructuredData() []any {
	var out []any
	for _, s := range getElementsByTagName(p.doc, "script") {
		typ := strings.ToLower(strings.TrimSpace(attrOf(s, "type")))
		if typ != "application/ld+json" {
			continue
		}
		out = append(out, decodeJSONLD(textContent(s))...)
	}
	for _, f := range p.frames {
		out = append(out, f.StructuredData()...)
	}
	return out
}

// decodeJSONLD parses one JSON-LD block into a list of values. The document
// may be a single object or an array of them.
func decodeJSONLD(source string) []any {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil
	}
	// Some pages wrap the JSON in an HTML comment or a CDATA section.
	source = strings.TrimPrefix(source, "<!--")
	source = strings.TrimSuffix(source, "-->")
	source = strings.TrimSpace(source)

	var v any
	if err := json.Unmarshal([]byte(source), &v); err != nil {
		// A trailing semicolon or a stray character is common; try to trim to
		// the last closing brace.
		if i := strings.LastIndexAny(source, "}]"); i >= 0 {
			if err2 := json.Unmarshal([]byte(source[:i+1]), &v); err2 == nil {
				return asList(v)
			}
		}
		return nil
	}
	return asList(v)
}

func asList(v any) []any {
	if arr, ok := v.([]any); ok {
		return arr
	}
	return []any{v}
}

// MicrodataItem is one item in a microdata graph: an element with itemscope and
// its itemtype and named properties.
type MicrodataItem struct {
	Type       string
	Properties map[string]string
}

// Microdata extracts itemscope elements and their itemprop values, which is the
// older alternative to JSON-LD (schema.org's itemscope/itemprop markup).
func (p *Page) Microdata() []MicrodataItem {
	var out []MicrodataItem
	for _, el := range descendants(p.doc) {
		if el.Type != html.ElementNode {
			continue
		}
		if !hasAttr(el, "itemscope") {
			continue
		}
		item := MicrodataItem{Type: attrOf(el, "itemtype"), Properties: map[string]string{}}
		for _, child := range descendants(el) {
			name := attrOf(child, "itemprop")
			if name == "" {
				continue
			}
			val := microdataValue(child)
			if val != "" {
				item.Properties[name] = val
			}
		}
		out = append(out, item)
	}
	for _, f := range p.frames {
		out = append(out, f.Microdata()...)
	}
	return out
}

func microdataValue(n *html.Node) string {
	switch strings.ToLower(n.Data) {
	case "meta":
		return attrOf(n, "content")
	case "img", "audio", "video", "source", "iframe", "embed":
		if v := attrOf(n, "src"); v != "" {
			return v
		}
		return attrOf(n, "content")
	case "a", "link", "area":
		return attrOf(n, "href")
	case "time":
		if v := attrOf(n, "datetime"); v != "" {
			return v
		}
		return strings.TrimSpace(textContent(n))
	case "data", "meter":
		if v := attrOf(n, "value"); v != "" {
			return v
		}
		return strings.TrimSpace(textContent(n))
	default:
		return strings.TrimSpace(textContent(n))
	}
}
