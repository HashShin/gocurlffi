package browser

import (
	"errors"
	"fmt"
	"image/color"
	"strings"

	"golang.org/x/net/html"
)

// ScreenshotOptions configures Page.Screenshot.
type ScreenshotOptions struct {
	// Width is the layout width in CSS px. Default 1280, clamped to 320..4096.
	Width int

	// Scale multiplies the output size (2 gives a 2x image). Default 1.
	Scale float64

	// MaxHeight bounds the output height in CSS px, so a very long page cannot
	// produce a huge image. Default 20000. The content is cropped.
	MaxHeight int
}

// Screenshot renders the page to a PNG.
//
// The page's CSS is applied first: <style> blocks and <link rel=stylesheet>
// sheets are fetched, parsed and cascaded (specificity, !important, source
// order, inheritance, simple media queries), including the user-agent defaults.
// The renderer then flows the document at the requested width and draws
// headings, paragraphs, lists, preformatted blocks, blockquotes, rules and
// styled runs, with flat block background colours and text alignment.
//
// It is still a document renderer, not a web renderer: there is no CSS box
// model, no images, no borders or shadows, and no positioning or floats.
// Layout, stylesheet loading, font parsing and rasterization all happen inside
// this call, so loading pages is unaffected.
func (p *Page) Screenshot(opts ScreenshotOptions) ([]byte, error) {
	if p.doc == nil {
		return nil, errors.New("browser: page has no document")
	}
	width := opts.Width
	if width <= 0 {
		width = 1280
	}
	if width < 320 {
		width = 320
	}
	if width > 4096 {
		width = 4096
	}
	scale := opts.Scale
	if scale <= 0 {
		scale = 1
	}
	maxHeight := opts.MaxHeight
	if maxHeight <= 0 {
		maxHeight = 20000
	}

	// Media queries must be evaluated against the width being rendered, and
	// getComputedStyle called from page scripts should agree with it.
	p.layoutWidth = float64(width)
	eng := p.styleEngineFor(float64(width))
	c := &collector{engine: eng}
	c.walkChildren(p.doc)
	c.flush()

	doc := layoutBlocks(c.blocks, width, eng.baseSize)
	if doc.height > maxHeight {
		doc.height = maxHeight
	}

	// The document background comes from <html> or <body>.
	pageBG := renderPageBG
	if bg, ok := p.documentBackground(eng); ok {
		pageBG = bg
	}
	return renderPNG(doc, scale, pageBG)
}

func (p *Page) documentBackground(eng *styleEngine) (color.RGBA, bool) {
	for _, tag := range []string{"body", "html"} {
		if n := findElement(p.doc, tag); n != nil {
			if cs := eng.compute(n); cs != nil && cs.hasBackground {
				return cs.background, true
			}
		}
	}
	return color.RGBA{}, false
}

// defaultLayoutWidth is the viewport getComputedStyle assumes before a render.
const defaultLayoutWidth = 1280

// --- stylesheet loading ---

// cssTexts returns the page's CSS in document order, with @imports resolved.
// It is cached: sources are fetched at most once per page.
//
// Only what the page itself declares is used - every <style> element and every
// <link rel=stylesheet> it contains, in document order. Nothing is injected.
// With Options.Debug each declaration is logged with what became of it, so a
// page that renders unstyled can be diagnosed instead of guessed at.
func (p *Page) cssTexts() []string {
	if p.styleSources != nil {
		return p.styleSources
	}
	var sources []string
	declared, applied := 0, 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode {
				switch c.Data {
				case "style":
					declared++
					if media := attrOf(c, "media"); !cssMediaMatches(media, 0) {
						p.debugf("style element skipped: media %q does not match", media)
					} else {
						applied++
						p.debugf("style element: %d bytes", len(textContent(c)))
						sources = append(sources, textContent(c))
					}
				case "link":
					if !hasStylesheetRel(c) {
						continue
					}
					declared++
					href := attrOf(c, "href")
					switch {
					case href == "":
						p.debugf("stylesheet skipped: <link rel=stylesheet> without href")
						continue
					case hasAttr(c, "disabled"):
						p.debugf("stylesheet skipped: disabled (%s)", href)
						continue
					case !cssMediaMatches(attrOf(c, "media"), 0):
						p.debugf("stylesheet skipped: media %q does not match (%s)", attrOf(c, "media"), href)
						continue
					}
					abs := resolveURL(p.baseURL(), href)
					resp, err := p.browser.get(abs, nil)
					if err != nil {
						p.debugf("stylesheet FAILED: %s: %v", abs, err)
						continue
					}
					applied++
					p.debugf("stylesheet: %d bytes from %s", len(resp.Content), abs)
					sources = append(sources, string(resp.Content))
				}
			}
			walk(c)
		}
	}
	walk(p.doc)
	p.debugf("page stylesheets: %d declared, %d applied", declared, applied)

	expanded := make([]string, 0, len(sources))
	for _, src := range sources {
		order := 0
		_, imports, _ := parseCSSStylesheet(src, 0, &order)
		for _, imp := range imports {
			if isFontOnlyStylesheet(imp) {
				// We render with embedded fonts, so a font provider's CSS is a
				// wasted request.
				p.debugf("@import skipped (font provider): %s", imp)
				continue
			}
			abs := resolveURL(p.baseURL(), imp)
			resp, err := p.browser.get(abs, nil)
			if err != nil {
				p.debugf("@import FAILED: %s: %v", abs, err)
				continue
			}
			p.debugf("@import: %d bytes from %s", len(resp.Content), abs)
			expanded = append(expanded, string(resp.Content))
		}
		expanded = append(expanded, src)
	}
	p.styleSources = expanded
	return expanded
}

func isFontOnlyStylesheet(u string) bool {
	l := strings.ToLower(u)
	return strings.Contains(l, "fonts.googleapis.com") || strings.Contains(l, "fonts.gstatic.com")
}

// hasStylesheetRel reports whether rel lists stylesheet. The other checks that
// decide whether the sheet is used live in cssTexts, so each can be logged.
func hasStylesheetRel(n *html.Node) bool {
	for _, tok := range strings.Fields(strings.ToLower(attrOf(n, "rel"))) {
		if tok == "stylesheet" {
			return true
		}
	}
	return false
}

func cssMediaMatches(q string, width float64) bool {
	if strings.TrimSpace(q) == "" {
		return true
	}
	p := &cssParser{mediaWidth: width}
	return p.matchMedia(q)
}

// styleEngineFor builds (and caches) the cascade for a layout width, because
// media queries depend on it.
func (p *Page) styleEngineFor(width float64) *styleEngine {
	if p.styleEngines == nil {
		p.styleEngines = map[float64]*styleEngine{}
	}
	if e, ok := p.styleEngines[width]; ok {
		return e
	}
	order := 0
	var rules []cssRule
	for i, src := range p.cssTexts() {
		rs, _, st := parseCSSStylesheet(src, width, &order)
		p.debugf("stylesheet %d: %d rules, %d/%d selectors unsupported", i, st.rules, st.skipped, st.selectors)
		for _, sel := range st.skipSample {
			p.debugf("  unsupported selector: %s", sel)
		}
		rules = append(rules, rs...)
	}
	p.debugf("style engine: %d rules at width %g", len(rules), width)
	e := newStyleEngine(rules, width, p.quirksMode())
	p.styleEngines[width] = e
	return e
}

// computedStyle returns the cascaded style of an element, loading the page's
// stylesheets on first use. It is what getComputedStyle is built on.
func (p *Page) computedStyle(n *html.Node) *computedStyle {
	if n == nil || n.Type != html.ElementNode {
		return nil
	}
	return p.styleEngineFor(p.viewportWidth()).compute(n)
}

// viewportWidth is the layout width media queries are resolved against.
func (p *Page) viewportWidth() float64 {
	if p.layoutWidth > 0 {
		return p.layoutWidth
	}
	return defaultLayoutWidth
}

// --- DOM to blocks ---

type listState struct {
	ordered bool
	index   int
}

type collector struct {
	engine  *styleEngine
	blocks  []renderBlock
	cur     *renderBlock
	style   renderStyle
	content float64 // left edge of the parent's content box
	quote   int
	pre     bool
	lists   []listState
	marker  string

	// bg is the background inherited from the nearest ancestor block that has
	// one; a descendant block paints it too, which is how a parent's
	// background shows behind its children.
	bg    color.RGBA
	hasBG bool
}

func isBlockDisplay(d string) bool {
	switch d {
	case "block", "flex", "grid", "list-item", "table", "table-row", "table-cell", "inline-flex":
		return true
	}
	return false
}

func (c *collector) walkChildren(n *html.Node) {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		c.walkNode(ch)
	}
}

func (c *collector) walkNode(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		c.addText(n.Data)
	case html.ElementNode:
		c.walkElement(n)
	}
}

func (c *collector) ensure(cs *computedStyle) *renderBlock {
	if c.cur == nil {
		b := &renderBlock{
			kind:    blockText,
			boxLeft: c.content,
			textX:   c.content,
			quote:   c.quote,
			pre:     c.pre,
			marker:  c.marker,
		}
		if cs != nil {
			b.align = cs.textAlign
			b.lineH = cs.lineHeight
		}
		if c.hasBG {
			b.bg = c.bg
			b.hasBG = true
			b.bgFull = true
		}
		c.cur = b
		c.marker = ""
	}
	return c.cur
}

func (c *collector) flush() {
	if c.cur != nil && len(c.cur.spans) > 0 {
		c.blocks = append(c.blocks, *c.cur)
	}
	c.cur = nil
}

func (c *collector) addText(s string) {
	if s == "" {
		return
	}
	if c.pre || c.style.mono && c.cur != nil && c.cur.pre {
		b := c.ensure(nil)
		b.spans = append(b.spans, renderSpan{text: s, style: c.style})
		return
	}
	collapsed := collapseWhitespace(s)
	if collapsed == "" {
		return
	}
	b := c.ensure(nil)
	b.spans = append(b.spans, renderSpan{text: collapsed, style: c.style})
}

func (c *collector) walkElement(el *html.Node) {
	cs := c.engine.compute(el)
	if cs == nil {
		return
	}
	if cs.display == "none" || cs.visibility == "hidden" {
		return
	}
	tag := strings.ToLower(el.Data)

	saved := struct {
		style   renderStyle
		content float64
		quote   int
		pre     bool
		bg      color.RGBA
		hasBG   bool
	}{c.style, c.content, c.quote, c.pre, c.bg, c.hasBG}

	if cs.hasBackground {
		c.bg = cs.background
		c.hasBG = true
	}

	c.style = renderStyle{
		size:      cs.fontSize,
		bold:      cs.bold,
		italic:    cs.italic,
		mono:      cs.mono,
		link:      cs.link,
		underline: cs.underline,
		strike:    cs.strike,
		color:     cs.textColor,
	}
	block := isBlockDisplay(cs.display)
	if block {
		// Box edges: margin then padding, relative to the parent's content box.
		boxLeft := c.content + cs.marginLeft
		c.content = boxLeft + cs.paddingLeft
	}
	pre := cs.whiteSpace == "pre" || cs.whiteSpace == "pre-wrap" || tag == "pre"
	c.pre = c.pre || pre

	defer func() {
		c.style, c.content, c.quote, c.pre = saved.style, saved.content, saved.quote, saved.pre
		c.bg, c.hasBG = saved.bg, saved.hasBG
	}()

	switch tag {
	case "br":
		c.flush()
		return
	case "hr":
		c.flush()
		c.blocks = append(c.blocks, renderBlock{
			kind: blockRule, boxLeft: c.content, textX: c.content,
			quote: c.quote,
		})
		return
	case "img":
		if alt, ok := getAttr(el, "alt"); ok && strings.TrimSpace(alt) != "" {
			s := c.style
			s.italic = true
			c.ensure(cs).spans = append(c.ensure(cs).spans, renderSpan{text: "[" + strings.TrimSpace(alt) + "]", style: s})
		}
		return
	}

	switch tag {
	case "ul", "ol":
		c.flush()
		c.lists = append(c.lists, listState{ordered: tag == "ol"})
		c.walkChildren(el)
		c.lists = c.lists[:len(c.lists)-1]
		c.flush()
		return
	case "li":
		c.flush()
		if cs.listStyle != "none" {
			if n := len(c.lists); n > 0 && c.lists[n-1].ordered {
				c.lists[n-1].index++
				c.marker = fmt.Sprintf("%d.", c.lists[n-1].index)
			} else {
				c.marker = listMarker(cs.listStyle)
			}
		}
		if block {
			c.cur = nil
			c.ensure(cs)
			c.cur.leading = cs.marginTop
			c.cur.trailing = cs.marginBottom
		}
		c.walkChildren(el)
		c.flush()
		return
	case "blockquote":
		c.flush()
		c.quote++
		c.walkChildren(el)
		c.flush()
		return
	}

	if block {
		c.flush()
		c.ensure(cs)
		c.cur.leading = cs.marginTop
		c.cur.trailing = cs.marginBottom
		c.cur.pre = c.pre
		c.cur.nowrap = cs.whiteSpace == "nowrap"
		c.walkChildren(el)
		c.flush()
		return
	}
	c.walkChildren(el)
}

func listMarker(style string) string {
	switch style {
	case "none":
		return ""
	case "circle":
		return "\u25e6"
	case "square":
		return "\u25aa"
	default:
		return "\u2022"
	}
}

// --- text helpers ---

func collapseWhitespace(s string) string {
	trimmedLeft := strings.TrimLeft(s, " \t\r\n\f\v")
	trimmedRight := strings.TrimRight(s, " \t\r\n\f\v")
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	out := strings.Join(fields, " ")
	if trimmedLeft != s {
		out = " " + out
	}
	if trimmedRight != s {
		out += " "
	}
	return out
}
