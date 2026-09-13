package browser

import (
	"errors"
	"fmt"
	"image/color"
	"strconv"
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

	// NoImages turns off drawing the page's pictures, which is the only part
	// of a render that fetches anything beyond the page's own stylesheets.
	// Images are on by default, bounded by MaxImages and MaxImageBytes.
	NoImages bool

	// MaxImages caps how many images are fetched for one render. Default 48.
	MaxImages int

	// MaxImageBytes caps the total image bytes kept for drawing. Default 16MB.
	MaxImageBytes int
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

	doc, pageBG, err := p.layOut(opts)
	if err != nil {
		return nil, err
	}
	return renderPNG(doc, scale, pageBG)
}

// layOut loads what a render needs, computes styles and flows the document,
// returning the laid-out lines and the page background.
func (p *Page) layOut(opts ScreenshotOptions) (*renderDoc, color.RGBA, error) {
	if p.doc == nil {
		return nil, color.RGBA{}, errors.New("browser: page has no document")
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
	maxHeight := opts.MaxHeight
	if maxHeight <= 0 {
		maxHeight = 20000
	}

	// Media queries must be evaluated against the width being rendered, and
	// getComputedStyle called from page scripts should agree with it.
	p.layoutWidth = float64(width)
	if !opts.NoImages {
		maxImages, maxBytes := opts.MaxImages, opts.MaxImageBytes
		if maxImages <= 0 {
			maxImages = defaultMaxImages
		}
		if maxBytes <= 0 {
			maxBytes = defaultMaxImageBytes
		}
		p.prefetchImages(p.doc, maxImages, maxBytes)
	}
	eng := p.styleEngineFor(float64(width))
	c := &collector{page: p, engine: eng}
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
	return doc, pageBG, nil
}

// RenderOutline lays the page out exactly as Screenshot would and returns one
// line per drawn block, without rasterizing anything. It is how a render is
// compared against a browser's geometry: Chromium answers with
// getBoundingClientRect (tools/cssdiff/geom) and this answers with the boxes
// this renderer used.
func (p *Page) RenderOutline(opts ScreenshotOptions, limit int) ([]string, error) {
	doc, _, err := p.layOut(opts)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, limit+1)
	out = append(out, fmt.Sprintf("page height %dpx at width %dpx", doc.height, doc.width))
	for _, ln := range doc.lines {
		if len(out) > limit {
			break
		}
		kind := "text"
		var detail string
		switch {
		case ln.pic != nil:
			kind, detail = "image", fmt.Sprintf("w=%.0f %s", ln.picW, ln.pic.src)
		case ln.rule:
			kind = "rule"
		default:
			for _, r := range ln.runs {
				detail += r.text
			}
			detail = strings.TrimSpace(detail)
		}
		bg := ""
		if ln.hasBG {
			bg = " bg=" + cssColorString(ln.bg)
		}
		out = append(out, fmt.Sprintf("y=%-6.0f h=%-5.0f %-6s x=%-5.0f %s%s",
			ln.y, ln.height, kind, ln.indent, truncateOutline(detail), bg))
	}
	return out, nil
}

func truncateOutline(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
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

// pageStyleSheet is one stylesheet the document declares: a <style> element's
// text, or a <link rel=stylesheet> whose text is fetched on first use.
type pageStyleSheet struct {
	href   string     // absolute URL, empty for an inline <style>
	node   *html.Node // the <style> or <link> element that declared it
	media  string
	source string // the CSS text, once it is known
	loaded bool   // source is valid
	failed bool   // the fetch failed and must not be retried
	err    string // why the sheet is not applied
}

// styleSheets lists the document's stylesheets in document order. Only what the
// page itself declares is used: nothing is injected, and a sheet belongs here
// because the page contains a <style> element or a <link rel=stylesheet>. The
// list is re-read after a script touches the DOM, since the page's own styles
// may arrive that way.
func (p *Page) styleSheets() []*pageStyleSheet {
	if !p.styleDirty && p.styleCacheValid() {
		return p.sheets
	}
	if p.doc == nil {
		return nil
	}
	// Skipped sheets stay in the list, marked with the reason, so the list is
	// complete and in document order; cssTexts only uses the applicable ones.
	var sheets []*pageStyleSheet
	declared := 0
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode {
				switch c.Data {
				case "style":
					declared++
					if media := attrOf(c, "media"); !cssMediaMatches(media, 0) {
						p.debugf("style element skipped: media %q does not match", media)
						sheets = append(sheets, &pageStyleSheet{
							node: c, media: media, failed: true,
							err: "media " + media + " does not match",
						})
					} else {
						sheets = append(sheets, &pageStyleSheet{
							node: c, media: media, source: textContent(c), loaded: true,
						})
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
						sheets = append(sheets, &pageStyleSheet{
							node: c, media: attrOf(c, "media"),
							err: "no href", failed: true,
						})
						continue
					case hasAttr(c, "disabled"):
						p.debugf("stylesheet skipped: disabled (%s)", href)
						sheets = append(sheets, &pageStyleSheet{
							node: c, href: resolveURL(p.baseURL(), href), err: "disabled", failed: true,
						})
						continue
					case !cssMediaMatches(attrOf(c, "media"), 0):
						p.debugf("stylesheet skipped: media %q does not match (%s)", attrOf(c, "media"), href)
						sheets = append(sheets, &pageStyleSheet{
							node: c, href: resolveURL(p.baseURL(), href), media: attrOf(c, "media"),
							err: "media " + attrOf(c, "media") + " does not match", failed: true,
						})
						continue
					}
					sheets = append(sheets, &pageStyleSheet{
						node: c, href: resolveURL(p.baseURL(), href), media: attrOf(c, "media"),
					})
				}
			}
			walk(c)
		}
	}
	walk(p.doc)
	usable := 0
	for _, s := range sheets {
		if !s.failed {
			usable++
		}
	}
	p.debugf("page stylesheets: %d declared, %d usable", declared, usable)
	p.sheets = sheets
	p.styleDirty = false
	// The rule caches were built from the previous set.
	p.styleSources = nil
	p.styleEngines = nil
	return p.sheets
}

// loadSheet fetches a linked stylesheet once. An inline <style> is already
// loaded.
func (p *Page) loadSheet(s *pageStyleSheet) bool {
	if s.loaded {
		return !s.failed
	}
	if s.failed {
		return false
	}
	if cached, ok := p.sheetSources[s.node]; ok {
		s.source, s.loaded = cached, true
		return true
	}
	resp, err := p.browser.get(s.href, nil)
	if err != nil {
		s.failed, s.err = true, err.Error()
		p.debugf("stylesheet FAILED: %s: %v", s.href, err)
		return false
	}
	if resp.StatusCode >= 400 {
		// A 4xx/5xx body is not a stylesheet; browsers do not apply one.
		s.failed = true
		s.err = strconv.Itoa(resp.StatusCode)
		if reason := strings.TrimSpace(resp.Reason); reason != "" {
			s.err += " " + reason
		}
		p.debugf("stylesheet FAILED: %s: HTTP %s", s.href, s.err)
		return false
	}
	if p.sheetSources == nil {
		p.sheetSources = map[*html.Node]string{}
	}
	s.source = string(resp.Content)
	s.loaded = true
	p.sheetSources[s.node] = s.source
	p.debugf("stylesheet: %d bytes from %s", len(s.source), s.href)
	return true
}

// cssTexts returns the page's CSS in document order, with @imports resolved,
// fetching linked sheets on first use.
func (p *Page) cssTexts() []string {
	if p.styleSources != nil {
		return p.styleSources
	}
	var sources []string
	for _, s := range p.styleSheets() {
		if p.loadSheet(s) {
			sources = append(sources, s.source)
		}
	}

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
	p.styleCacheValid()
	if e, ok := p.styleEngines[width]; ok {
		return e
	}
	// cssTexts may drop the caches (a script changed the document), so the map
	// is created after it, not before.
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
	if p.styleEngines == nil {
		p.styleEngines = map[float64]*styleEngine{}
	}
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
	page    *Page
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
		if pic := c.page.image(resolveURL(c.page.baseURL(), imageSource(el))); pic != nil {
			c.flush()
			c.blocks = append(c.blocks, renderBlock{
				kind:     blockImage,
				pic:      pic,
				picW:     float64(pic.size.X),
				picH:     float64(pic.size.Y),
				boxLeft:  c.content,
				leading:  cs.marginTop,
				trailing: cs.marginBottom,
			})
			return
		}
		// Without the bytes, the alt text is all there is to draw.
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

// StyleSheet describes one stylesheet the document declares. It is the Go
// answer to document.styleSheets, and the way to check that a page's own CSS
// was loaded rather than guessed at.
type StyleSheet struct {
	// Href is the absolute URL, empty for an inline <style> element.
	Href string
	// Media is the media attribute, or the <style> element's.
	Media string
	// Inline is true for a <style> element.
	Inline bool
	// Bytes is the size of the CSS text.
	Bytes int
	// Rules is the number of rules parsed from it at the current layout width.
	Rules int
	// Err is why the sheet is not applied, empty when it is.
	Err string
}

// StyleSheets lists the stylesheets the page itself declares, in document
// order, fetching the linked ones the first time it is called. Nothing is
// injected, and nothing the page declares is left out: a sheet that failed or
// was skipped appears with Err set, so "the site's CSS did not load" can be
// told apart from "the page has no CSS to load".
func (p *Page) StyleSheets() []StyleSheet {
	sheets := p.styleSheets()
	out := make([]StyleSheet, 0, len(sheets))
	for _, s := range sheets {
		info := StyleSheet{Href: s.href, Media: s.media, Inline: s.href == ""}
		if p.loadSheet(s) {
			order := 0
			rules, _, _ := parseCSSStylesheet(s.source, p.viewportWidth(), &order)
			info.Bytes = len(s.source)
			info.Rules = len(rules)
		}
		if info.Err = s.err; info.Err == "" && !s.loaded {
			info.Err = "not loaded"
		}
		out = append(out, info)
	}
	return out
}
