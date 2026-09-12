package browser

import (
	"strings"

	"golang.org/x/net/html"
)

// Link is an anchor found in a rendered page.
type Link struct {
	Text string
	Href string
}

// Links returns every anchor with a resolvable href.
func (p *Page) Links() []Link {
	var out []Link
	for _, a := range getElementsByTagName(p.doc, "a") {
		href := attrOf(a, "href")
		if href == "" || strings.HasPrefix(href, "javascript:") {
			continue
		}
		out = append(out, Link{Text: strings.TrimSpace(textContent(a)), Href: resolveURL(p.URL, href)})
	}
	return out
}

// Attr returns an element attribute value.
func Attr(n *html.Node, key string) string { return attrOf(n, key) }

// Markdown renders the current DOM as markdown, useful for feeding an LLM.
func (p *Page) Markdown() string {
	var b strings.Builder
	renderMarkdown(&b, p.doc, p.URL)
	return strings.TrimSpace(b.String())
}

func renderMarkdown(b *strings.Builder, n *html.Node, base string) {
	if n == nil {
		return
	}
	if n.Type == html.TextNode {
		t := collapseSpaces(n.Data)
		if t != "" {
			b.WriteString(t)
		}
		return
	}
	if n.Type != html.ElementNode && n.Type != html.DocumentNode {
		return
	}
	switch n.Data {
	case "script", "style", "noscript", "template", "svg", "head", "iframe":
		return
	}
	block := func() {
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
	}
	switch n.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		block()
		level := int(n.Data[1] - '0')
		b.WriteString(strings.Repeat("#", level) + " ")
		renderChildren(b, n, base)
		b.WriteString("\n\n")
		return
	case "p":
		block()
		renderChildren(b, n, base)
		b.WriteString("\n\n")
		return
	case "br":
		b.WriteString("\n")
		return
	case "hr":
		b.WriteString("\n---\n\n")
		return
	case "li":
		block()
		b.WriteString("- ")
		renderChildren(b, n, base)
		b.WriteString("\n")
		return
	case "a":
		href := resolveURL(base, attrOf(n, "href"))
		text := collapseSpaces(textContent(n))
		if text == "" {
			return
		}
		if href != "" {
			b.WriteString("[" + text + "](" + href + ")")
		} else {
			b.WriteString(text)
		}
		return
	case "strong", "b":
		b.WriteString("**")
		renderChildren(b, n, base)
		b.WriteString("**")
		return
	case "em", "i":
		b.WriteString("*")
		renderChildren(b, n, base)
		b.WriteString("*")
		return
	case "code":
		b.WriteString("`" + textContent(n) + "`")
		return
	case "pre":
		block()
		b.WriteString("```\n" + textContent(n) + "\n```\n\n")
		return
	case "tr":
		b.WriteString("| ")
		renderChildren(b, n, base)
		b.WriteString("\n")
		return
	case "td", "th":
		b.WriteString(collapseSpaces(textContent(n)) + " | ")
		return
	}
	renderChildren(b, n, base)
}

func renderChildren(b *strings.Builder, n *html.Node, base string) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		renderMarkdown(b, c, base)
	}
}

func collapseSpaces(s string) string {
	if s == "" {
		return ""
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	out := strings.Join(fields, " ")
	if strings.ContainsAny(s[:1], " \t\n\r") {
		out = " " + out
	}
	if strings.ContainsAny(s[len(s)-1:], " \t\n\r") {
		out += " "
	}
	return out
}
