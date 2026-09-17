package browser

import (
	"errors"
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"unicode"

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
// The renderer then flows the document at the requested width: block flow with
// margins, padding and borders, flex rows, grid columns, tables, absolute
// positioning, images and backgrounds, and draws the text runs that result.
//
// It is still a document renderer, not a web renderer: there are no floats, no
// multi-column layout, and no transforms or animations. Layout, stylesheet
// loading, font parsing and rasterization all happen inside this call, so
// loading pages is unaffected.
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
	p.setViewportWidth(float64(width))
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
	root := &absFrame{}
	// Seed the containing-block content width with the page column, so a
	// percentage width at the top of the document resolves against it.
	c := &collector{page: p, engine: eng, absOwner: root, contW: float64(width), hasContW: true}
	if p.geomOn {
		c.geom = true
		c.geomNodes = map[int]*html.Node{}
		c.geomNode = map[*html.Node]int{}
	}
	c.walkChildren(p.doc)
	c.flush()
	if len(root.children) > 0 {
		// Page-level out-of-flow content is placed from the page origin.
		c.blocks = append([]renderBlock{{kind: blockText, abs: root.children}}, c.blocks...)
	}

	doc := layoutBlocks(c.blocks, width, eng.baseSize)
	if doc.height > maxHeight {
		doc.height = maxHeight
	}

	// The document background comes from <html> or <body>.
	pageBG := renderPageBG
	if bg, ok := p.documentBackground(eng); ok {
		pageBG = bg
	}
	if p.geomOn {
		p.geomCache = &layoutGeom{
			stamp: domMutations.Load(),
			width: width,
			rects: doc.geom,
			nodes: c.geomNode,
		}
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
	// The boxes the layout drew, after the text: an element's own rectangle is
	// what a page script would measure with getBoundingClientRect, and without
	// it the only way to see where a container ended was to read its text.
	for _, bx := range doc.boxes {
		if len(out) > limit {
			break
		}
		out = append(out, fmt.Sprintf("box x=%-6.0f y=%-6.0f w=%-6.0f h=%-6.0f id=%d",
			bx.x, bx.y, bx.w, bx.h, bx.ref))
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

// defaultLayoutHeight pairs with defaultLayoutWidth. Nothing lays a page out
// by height yet, so it is the viewport height a page script sees.
const defaultLayoutHeight = 720

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
	p.fontFaces = nil
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
	resp, err := p.browser.fetch(s.href, nil, "stylesheet")
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
			abs := resolveURL(p.baseURL(), imp)
			resp, err := p.browser.fetch(abs, nil, "stylesheet")
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
		p.addFontFaces(st.fontFaces)
		// The width is part of the number: media queries are evaluated while
		// parsing, so one sheet yields different rule counts at 900 and 1280.
		p.debugf("stylesheet %d at width %g: %d rules, %d/%d selectors unsupported (%d target pseudo-elements)",
			i, width, st.rules, st.skipped, st.selectors, st.pseudoSkipped)
		for _, sel := range st.skipSample {
			p.debugf("  unsupported selector: %s", sel)
		}
		rules = append(rules, rs...)
	}
	p.debugf("style engine: %d rules at width %g", len(rules), width)
	if len(p.fontFaces) > 0 {
		p.debugf("page fonts: %d @font-face rules", len(p.fontFaces))
	}
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
// setViewportWidth records the width the page is laid out at and tells the
// page's own scripts, so innerWidth and the layout cannot disagree.
func (p *Page) setViewportWidth(w float64) {
	p.layoutWidth = w
	if p.env == nil || p.env.vm == nil {
		return
	}
	_ = p.env.vm.Set("innerWidth", w)
	_ = p.env.vm.Set("outerWidth", w)
	if screen := p.env.vm.Get("screen"); screen != nil {
		if o := screen.ToObject(p.env.vm); o != nil {
			_ = o.Set("width", w)
			_ = o.Set("availWidth", w)
		}
	}
}

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

	// boxSeq numbers the element boxes so each one is drawn once.
	boxSeq int
	// geom, when set, records one rectangle per block-level element even when
	// it paints nothing, so getBoundingClientRect can answer for any element.
	// It is off unless a caller asks for geometry, so screenshots pay nothing.
	geom      bool
	geomSeq   int
	geomNodes map[int]*html.Node
	geomNode  map[*html.Node]int
	// absOwner collects position:absolute/fixed children of the nearest
	// positioned ancestor.
	absOwner *absFrame

	// The current block-sizing context: the content-left, width, max-width and
	// auto margins of the nearest ancestor that set one. Blocks inherit it, so
	// max-width:1100px;margin:0 auto centers a whole subtree.
	sizeLeft                  float64
	sizeWidthPx, sizeWidthPct float64
	hasSizeWidth              bool
	// sizeWidthIsOwn is set when the declared width is the box's own fixed
	// size rather than a percentage or a viewport length.
	sizeWidthIsOwn              bool
	sizeMaxPx, sizeMaxPct       float64
	hasSizeMax                  bool
	sizeAutoLeft, sizeAutoRight bool
	// The declaring element's box, so layout can measure the width from it
	// rather than from the block that inherits the context.
	hasSizeOwner    bool
	sizeBoxLeft     float64
	sizePadLeft     float64
	sizePadRight    float64
	sizeMarginRight float64

	// contW is the content box width of the current containing block, when it
	// is known from an ancestor's explicit width. A percentage width resolves
	// against it instead of the page width.
	contW    float64
	hasContW bool
	// contH is the height a percentage height resolves against: the containing
	// block's own declared height, when it has one.
	contH        float64
	hasContH     bool
	ownHeightPx  float64
	hasOwnHeight bool

	// rightInset is how far the containing block's content edge sits inside the
	// column's right edge, accumulated over the containers between them. It is
	// zero at the top of the document, where the column itself is the
	// containing block, and grows with a container's right padding, border and
	// margin. The column's width is only known at layout time (a flex item
	// column differs from the page), so the inset is what is recorded.
	rightInset float64
	// lineH is the computed line-height of the element being walked, which
	// text collected straight into a flex row or grid still inherits.
	lineH float64
	// floatRoot is the element currently being collected as a float, which
	// must not be floated a second time by its own collection.
	floatRoot *html.Node
	// blockInset is the inset that applies to the blocks the element being
	// walked produces: the inset of the containing block they sit in, which is
	// their parent's content box, not their own.
	blockInset float64
	// elSeq numbers the block-level elements in document order, and elID is
	// the one being walked, so a block can be traced back to the element that
	// produced it. depth is how many block-level elements enclose it, which
	// is the order their backgrounds paint in.
	elSeq int
	elID  int
	depth int
	// elGeomID is the geometry id of the element being walked, so a box it
	// declares can be recorded for it even when it produces no block.
	elGeomID int
	// declaresSizing records that the element being walked is the one that
	// declares the current sizing context. The block it starts is then the
	// block the context's box belongs to, which the layout has to know: a
	// descendant that happens to begin at the same edge is measured inside the
	// box, not as the box.
	declaresSizing bool
	// sizeInset is the right inset of the containing block the sizing context
	// was declared in, so the context's available width can be measured from
	// the column without a container inside it shrinking the box.
	sizeInset float64
}

// finishBlock applies an element's box edges, sizing context and box to the
// blocks it produced, creating an empty block when a boxed element has no
// content of its own (a sized, empty div).
func (c *collector) finishBlock(start int, cs *computedStyle, boxed bool) {
	// A declared height makes a box even with nothing in it: a spacer is
	// written "<div style=height:448px></div>", and a panel that paints
	// nothing of its own still holds the next section down the page.
	sized := boxed || cs.hasHeight || cs.minHeight > 0
	if sized && len(c.blocks) == start {
		b := renderBlock{
			kind: blockText, boxLeft: c.content, textX: c.content, quote: c.quote,
			ownsSizing: c.declaresSizing, ownerSeq: c.elID, depth: c.depth,
			ownerGeomID: c.elGeomID,
		}
		c.stampRight(&b)
		c.blocks = append(c.blocks, b)
	}
	c.applyBoxEdges(start, cs)
	ownHeight := 0.0
	if c.hasOwnHeight {
		ownHeight = c.ownHeightPx
	}
	c.assignSizing(start, cs, ownHeight)
	if boxed {
		c.assignBox(start, cs)
	}
}

// assignSizing copies the current sizing context onto the blocks an element
// produced, unless a nested element already gave them their own.
func (c *collector) assignSizing(start int, cs *computedStyle, ownHeight float64) {
	for i := start; i < len(c.blocks); i++ {
		b := &c.blocks[i]
		// A declared height sizes the element's own box, so it reaches the
		// block the element starts with even when that block brought a
		// sizing context of its own: a panel written "grid h-112" holds one
		// child, and the panel's height belongs to the panel.
		if cs != nil && i == start && cs.hasHeight && cs.heightPx > b.minHeight {
			b.minHeight = cs.heightPx
			b.hasDeclaredHeight = true
		}
		// The declared height is the box's own, and the box ends with the
		// element's last block: the layout needs both to let content taller
		// than the box overflow it.
		if cs != nil && i == start && cs.hasHeight && cs.heightPx > 0 {
			b.boxHeight = cs.heightPx
			b.boxLast = len(c.blocks) - start - 1
		}
		if b.hasSizing {
			continue
		}
		b.hasSizing = true
		b.sizeLeft = c.sizeLeft
		b.boxWidthPx, b.boxWidthPct, b.hasBoxWidth = c.sizeWidthPx, c.sizeWidthPct, c.hasSizeWidth
		// The width is the box's own only when this element declares it: the
		// sizing context an ancestor pinned otherwise travels down as the
		// box's, which made a nested row measure itself at the ancestor's
		// width.
		b.boxWidthIsOwn = c.sizeWidthIsOwn && cs != nil && cs.hasWidth
		b.boxMaxWidthPx, b.boxMaxWidthPct, b.hasBoxMaxWidth = c.sizeMaxPx, c.sizeMaxPct, c.hasSizeMax
		b.boxAutoLeft, b.boxAutoRight = c.sizeAutoLeft, c.sizeAutoRight
		b.hasSizeOwner = c.hasSizeOwner
		b.sizeBoxLeft, b.sizePadLeft = c.sizeBoxLeft, c.sizePadLeft
		b.sizePadRight, b.sizeMarginRight = c.sizePadRight, c.sizeMarginRight
		b.sizeInset = c.sizeInset
		if cs != nil {
			b.borderBox = cs.boxSizingBorderBox
			if ownHeight > b.minHeight {
				b.minHeight = ownHeight
				b.hasDeclaredHeight = true
			}
			// A declared min-height holds a box open the same way, and is
			// how a page writes a section that must be at least a screen
			// tall.
			if cs.minHeight > b.minHeight {
				b.minHeight = cs.minHeight
				b.hasDeclaredHeight = true
			}
			if cs.hasMaxHeight {
				b.maxHeight = cs.maxHeightPx
			}
		}
	}
}

// absFrame is the set of out-of-flow children of one positioned ancestor.
type absFrame struct{ children []absChild }

// nextBoxID returns a fresh box id.
func (c *collector) nextBoxID() int {
	c.boxSeq++
	return c.boxSeq
}

// nextGeomID numbers an element for geometry recording, kept in its own
// sequence so it never collides with a paint box id.
func (c *collector) nextGeomID() int {
	c.geomSeq++
	return c.geomSeq
}

// assignBox marks the blocks an element produced as one box: the element's
// background, border and radius are drawn as a single rectangle by the layout.
// Blocks that already carry a box (a nested boxed element or a control) are
// left alone.
func (c *collector) assignBox(start int, cs *computedStyle) {
	gid := c.nextBoxID()
	left := c.content - cs.paddingLeft - cs.borderW
	for i := start; i < len(c.blocks); i++ {
		b := &c.blocks[i]
		if b.boxID != 0 {
			continue
		}
		b.boxID = gid
		b.borderLeft = left
		b.boxPadRight = cs.paddingRight + cs.borderW
		// The box's paint order is its owning element's, not the depth of
		// whichever descendant block happened to carry it.
		b.boxDepth = c.depth
		// Only this element's own blocks are the box's own: for a descendant
		// the box merely shows through, and its padding is beyond the
		// descendant's edge.
		if b.ownerSeq == c.elID {
			b.ownsBox = true
		}
		b.hasBorder = cs.hasBorder
		b.borderW = cs.borderW
		b.borderColor = scaleAlpha(cs.borderColor, cs.opacity)
		b.radiusPx, b.radiusPct = cs.radiusPx, cs.radiusPct
		if cs.hasBackground {
			b.boxBG = scaleAlpha(cs.background, cs.opacity)
			b.boxHasBG = true
		}
		b.shadowX, b.shadowY = cs.shadowX, cs.shadowY
		b.shadowBlur, b.shadowSpread = cs.shadowBlur, cs.shadowSpread
		b.shadowColor, b.hasShadow = scaleAlpha(cs.shadowColor, cs.opacity), cs.hasShadow
	}
}

// isInlineFlexDisplay reports whether an element that lays its children out in
// a row is inline-level. Such a box is one atomic item on a line: it shrinks to
// its content and the containing block's text-align places it, instead of
// filling the line and stacking its items at the left.
func isInlineFlexDisplay(d string) bool {
	return d == "inline-flex" || d == "inline-grid"
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
		if cs := c.engine.compute(n); cs != nil && (cs.position == "absolute" || cs.position == "fixed") {
			c.collectPositioned(n, cs)
			return
		}
		c.walkElement(n)
	}
}

// collectPositioned takes an out-of-flow element's content out of the flow and
// files it with the current positioned ancestor, to be placed at layout time.
func (c *collector) collectPositioned(el *html.Node, cs *computedStyle) {
	parent := c.absOwner
	frame := &absFrame{}
	c.absOwner = frame
	savedContent := c.content
	c.content = 0
	c.flush()
	start := len(c.blocks)
	c.walkElement(el)
	c.flush()
	blocks := append([]renderBlock(nil), c.blocks[start:]...)
	c.blocks = c.blocks[:start]
	c.content = savedContent
	if len(frame.children) > 0 && len(blocks) > 0 {
		blocks[0].abs = append(blocks[0].abs, frame.children...)
	}
	c.absOwner = parent
	child := absChild{
		blocks: blocks,
		left:   cs.left, top: cs.top, right: cs.right, bottom: cs.bottom,
		hasLeft: cs.hasLeft, hasTop: cs.hasTop, hasRight: cs.hasRight, hasBottom: cs.hasBottom,
		hasWidth: cs.hasWidth, widthPx: cs.widthPx, widthPct: cs.widthPct,
		z:             cs.zIndex,
		translateX:    cs.translateX,
		translateY:    cs.translateY,
		translateXPct: cs.translateXPct,
		translateYPct: cs.translateYPct,
		hasTranslate:  cs.hasTranslate,
	}
	if c.absOwner != nil {
		c.absOwner.children = append(c.absOwner.children, child)
	}
}

// stampRight records the containing block's content right edge on a block, so
// the layout can wrap its text against the container it actually sits in.
func (c *collector) stampRight(b *renderBlock) {
	b.rightInset, b.hasRightInset = c.blockInset, c.blockInset > 0
}

func (c *collector) ensure(cs *computedStyle) *renderBlock {
	if c.cur == nil {
		b := &renderBlock{
			kind:        blockText,
			boxLeft:     c.content,
			textX:       c.content,
			quote:       c.quote,
			pre:         c.pre,
			marker:      c.marker,
			ownsSizing:  c.declaresSizing,
			ownerSeq:    c.elID,
			depth:       c.depth,
			ownerGeomID: c.elGeomID,
		}
		c.stampRight(b)
		if cs != nil {
			b.align = cs.textAlign
			b.lineH = cs.lineHeight
		} else {
			// A text node: its line height is the one it inherited from the
			// element it sits in. go.dev's footer links are "display: flex"
			// anchors with "line-height: 2rem", and without this the links
			// stack 17px apart instead of 32.
			b.lineH = c.lineH
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
	s = applyTextTransform(c.style.textTransform, s)
	if c.pre || c.style.mono && c.cur != nil && c.cur.pre {
		b := c.ensure(nil)
		b.spans = append(b.spans, renderSpan{text: s, style: c.style})
		return
	}
	collapsed := collapseWhitespace(s)
	if collapsed == "" {
		return
	}
	// A run of nothing but whitespace only separates what is already on the
	// line. With nothing before it, it is a leading space that the line wrapper
	// drops anyway, and it must not be the thing that makes an otherwise empty
	// block non-empty: flush skips a block with no spans, so appending here
	// turned every whitespace node between two elements into a zero-height box.
	if strings.TrimSpace(collapsed) == "" && (c.cur == nil || len(c.cur.spans) == 0) {
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

	// Geometry: every block-level element (and boxes that lay out children)
	// gets an id, and every block it produces carries it, so the layout can
	// report the element's rectangle even when it paints nothing.
	var geomID, geomStart int
	if c.geom {
		geomStart = len(c.blocks)
		defer func() {
			if geomID == 0 {
				return
			}
			for i := geomStart; i < len(c.blocks); i++ {
				c.blocks[i].geomIDs = append(c.blocks[i].geomIDs, geomID)
			}
		}()
	}

	saved := struct {
		style                       renderStyle
		content                     float64
		quote                       int
		pre                         bool
		lineH                       float64
		bg                          color.RGBA
		hasBG                       bool
		sizeLeft                    float64
		sizeWidthPx, sizeWidthPct   float64
		hasSizeWidth                bool
		sizeMaxPx, sizeMaxPct       float64
		hasSizeMax                  bool
		sizeAutoLeft, sizeAutoRight bool
		contW                       float64
		hasContW                    bool
		contH                       float64
		hasContH                    bool
		ownHeightPx                 float64
		hasOwnHeight                bool
		rightInset                  float64
		hasSizeOwner                bool
		sizeBoxLeft                 float64
		sizePadLeft                 float64
		sizePadRight                float64
		sizeMarginRight             float64
		sizeWidthIsOwn              bool
		declaresSizing              bool
		sizeInset                   float64
		elID                        int
		depth                       int
		elGeomID                    int
	}{c.style, c.content, c.quote, c.pre, c.lineH, c.bg, c.hasBG,
		c.sizeLeft, c.sizeWidthPx, c.sizeWidthPct, c.hasSizeWidth,
		c.sizeMaxPx, c.sizeMaxPct, c.hasSizeMax, c.sizeAutoLeft, c.sizeAutoRight,
		c.contW, c.hasContW, c.contH, c.hasContH, c.ownHeightPx, c.hasOwnHeight, c.rightInset,
		c.hasSizeOwner, c.sizeBoxLeft, c.sizePadLeft, c.sizePadRight, c.sizeMarginRight,
		c.sizeWidthIsOwn, c.declaresSizing, c.sizeInset, c.elID, c.depth, c.elGeomID}

	if cs.hasBackground {
		c.bg = scaleAlpha(cs.background, cs.opacity)
		c.hasBG = true
	}

	c.style = renderStyle{
		size:          cs.fontSize,
		bold:          cs.bold,
		italic:        cs.italic,
		mono:          cs.mono,
		link:          cs.link,
		underline:     cs.underline,
		strike:        cs.strike,
		color:         scaleAlpha(cs.textColor, cs.opacity),
		font:          c.page.pageFont(cs.fontFamily, cs.weight, cs.italic),
		textTransform: cs.textTransform,
		letterSpacing: cs.letterSpacing,
	}
	// An inline element's background belongs to its text runs. A block's is its
	// box, which finishBlock draws, so painting it here as well would draw it
	// twice and darken a translucent colour.
	if cs.hasBackground && !isBlockDisplay(cs.display) {
		c.style.bg = scaleAlpha(cs.background, cs.opacity)
		c.style.hasBG = true
	}
	// Text that is a direct child of a flex row or grid has no element of its
	// own to carry the line height, so it is kept here for ensure.
	c.lineH = cs.lineHeight
	block := isBlockDisplay(cs.display)
	c.ownHeightPx, c.hasOwnHeight = 0, false
	if cs.hasHeight {
		switch {
		case cs.heightPct > 0 && c.hasContH:
			// A percentage, including a calc() that mixes one with a length:
			// the length part can be negative, as in calc(100% - 40px).
			c.ownHeightPx, c.hasOwnHeight = cs.heightPx+cs.heightPct*c.contH, true
		case cs.heightPx > 0:
			c.ownHeightPx, c.hasOwnHeight = cs.heightPx, true
		case cs.heightPct > 0:
			// Nothing to resolve the percentage against: the box stays auto,
			// as a browser leaves it.
		}
	}
	if block {
		if c.hasOwnHeight {
			c.contH, c.hasContH = c.ownHeightPx, true
		} else if cs.hasHeight {
			c.contH, c.hasContH = 0, false
		}
	}
	// A <summary> is display:list-item, but inside an inline-block <details>
	// the block formatting context it starts is contained by the atomic
	// inline box, so the summary reads as the box's one label and the text
	// after the details stays on the line. lobste.rs writes each story's
	// "caches" dropdown this way, and the block summary broke the byline into
	// three lines.
	if block && tag == "summary" && el.Parent != nil && el.Parent.Type == html.ElementNode {
		if pcs := c.engine.compute(el.Parent); pcs != nil && !isBlockDisplay(pcs.display) {
			block = false
		}
	}
	// The blocks this element produces live in its containing block, so they
	// carry the inset of the parent's content box, not this element's own.
	savedBlockInset := c.blockInset
	c.blockInset = c.rightInset
	// A block with a background, border or radius is drawn as one box: its
	// background is painted as a (possibly rounded) rectangle instead of per
	// line, so its own background must not propagate to the lines it contains.
	// A button is inline-block but produces its own blocks, so it can carry a
	// box like a block element.
	boxed := (block || tag == "button") && (cs.hasBackground || cs.hasBorder || cs.hasRadius() || cs.hasShadow)
	if boxed {
		c.hasBG = false
	}
	if c.geom && (block || tag == "button" || isFlexRowContainer(cs) || isGridContainer(cs) || cs.display == "table") {
		geomID = c.nextGeomID()
		c.geomNodes[geomID] = el
		c.geomNode[el] = geomID
	}
	// The id of the element a box declared here would belong to, so the box can
	// be recorded for it even when it produces no block of its own.
	c.elGeomID = geomID
	// Every element is numbered, so a block can name the element that produced
	// it. Only a block-level element declares a sizing context, though: an
	// inline one inherits the context it sits in without owning it.
	c.elSeq++
	c.elID = c.elSeq
	c.declaresSizing = block &&
		(cs.hasWidth || cs.hasMaxWidth || cs.marginLeftAuto || cs.marginRightAuto)
	if block {
		c.depth++
		// Box edges: margin then padding, relative to the parent's content box.
		// The border box starts at boxLeft; the content is inside the border and
		// then the padding.
		boxLeft := c.content + cs.marginLeft
		c.content = boxLeft + cs.borderW + cs.paddingLeft
		if c.declaresSizing {
			// A percentage width resolves against the containing block's content
			// width, which an ancestor pins either with an explicit width or a
			// max-width that clamps its auto width. Without one it stays a
			// percentage and the layout resolves it against the column.
			parentW, haveParent := c.contW, c.hasContW
			edge := cs.paddingLeft + cs.paddingRight + 2*cs.borderW
			c.sizeLeft = c.content
			c.hasSizeOwner = true
			c.sizeBoxLeft = boxLeft
			c.sizePadLeft = cs.paddingLeft + cs.borderW
			c.sizePadRight = cs.paddingRight + cs.borderW
			c.sizeMarginRight = cs.marginRight
			// The inset of the frame the context is declared in, which is
			// where its own box is measured from: the containers below it add
			// their own insets to the blocks they produce.
			c.sizeInset = c.rightInset
			wPx, wPct := cs.widthPx, cs.widthPct
			resolved := true
			if wPct != 0 {
				if haveParent {
					wPx += wPct * parentW
					wPct = 0
				} else {
					resolved = false
				}
			}
			c.sizeWidthPx, c.sizeWidthPct, c.hasSizeWidth = wPx, wPct, cs.hasWidth
			c.sizeWidthIsOwn = cs.hasWidth && cs.widthPct == 0 && !cs.widthFromViewport
			mPx, mPct := cs.maxWidthPx, cs.maxWidthPct
			resolvedMax := true
			if mPct != 0 {
				if haveParent {
					mPx += mPct * parentW
					mPct = 0
				} else {
					resolvedMax = false
				}
			}
			c.sizeMaxPx, c.sizeMaxPct, c.hasSizeMax = mPx, mPct, cs.hasMaxWidth
			c.sizeAutoLeft, c.sizeAutoRight = cs.marginLeftAuto, cs.marginRightAuto

			// The content width descendants resolve percentages against: the
			// element's used width, or the containing block's when it has no
			// explicit width. A block's auto width fills its containing block.
			usedW, hasUsed := parentW, haveParent
			// usedBorderBox records whether usedW still includes the padding and
			// border, which content-box widths do not.
			usedBorderBox := true
			switch {
			case cs.hasWidth && resolved:
				usedW, hasUsed, usedBorderBox = wPx, true, cs.boxSizingBorderBox
			case cs.hasWidth:
				hasUsed = false
			}
			if cs.hasMaxWidth && resolvedMax && mPx >= 0 {
				if !hasUsed || mPx < usedW {
					usedW, hasUsed, usedBorderBox = mPx, true, cs.boxSizingBorderBox
				}
			}
			if hasUsed {
				cw := usedW
				if usedBorderBox {
					cw -= edge
				}
				if cw < 0 {
					cw = 0
				}
				c.contW, c.hasContW = cw, true
			} else {
				c.contW, c.hasContW = 0, false
			}
		} else {
			// A container that does not declare its own width still insets what
			// it contains: its padding, border and right margin pull the content
			// edge in from the containing block's. Without this the text inside
			// a padded wrapper would wrap against the page, not the wrapper.
			c.rightInset += cs.marginRight + cs.paddingRight + cs.borderW
			if c.hasContW {
				// Its own content width is what its children resolve a
				// percentage against, and that is the space left after its
				// margins, padding and border. A row of columns carries
				// "margin: 0 -15px", so its content box is wider than the one
				// it sits in, and its columns are as wide as that.
				if w := c.contW - cs.marginLeft - cs.marginRight -
					cs.paddingLeft - cs.paddingRight - 2*cs.borderW; w > 0 {
					c.contW = w
				} else {
					c.hasContW = false
				}
			}
		}
	}
	// An inline-block control or button lays its label out inside its own
	// padding and border, but never takes the block branch that applies those
	// edges. Without this its box would start at the padding and its label
	// would sit at the outer edge.
	if !block && (tag == "button" || tag == "input" || tag == "select" || tag == "textarea") {
		c.content += cs.marginLeft + cs.borderW + cs.paddingLeft
	}
	pre := cs.whiteSpace == "pre" || cs.whiteSpace == "pre-wrap" || tag == "pre"
	c.pre = c.pre || pre

	defer func() {
		c.style, c.content, c.quote, c.pre = saved.style, saved.content, saved.quote, saved.pre
		c.lineH = saved.lineH
		c.bg, c.hasBG = saved.bg, saved.hasBG
		c.sizeLeft = saved.sizeLeft
		c.sizeWidthPx, c.sizeWidthPct, c.hasSizeWidth = saved.sizeWidthPx, saved.sizeWidthPct, saved.hasSizeWidth
		c.sizeWidthIsOwn = saved.sizeWidthIsOwn
		c.sizeMaxPx, c.sizeMaxPct, c.hasSizeMax = saved.sizeMaxPx, saved.sizeMaxPct, saved.hasSizeMax
		c.sizeAutoLeft, c.sizeAutoRight = saved.sizeAutoLeft, saved.sizeAutoRight
		c.hasSizeOwner, c.sizeBoxLeft = saved.hasSizeOwner, saved.sizeBoxLeft
		c.sizePadLeft, c.sizePadRight = saved.sizePadLeft, saved.sizePadRight
		c.sizeMarginRight = saved.sizeMarginRight
		c.contW, c.hasContW = saved.contW, saved.hasContW
		c.contH, c.hasContH = saved.contH, saved.hasContH
		c.ownHeightPx, c.hasOwnHeight = saved.ownHeightPx, saved.hasOwnHeight
		c.rightInset = saved.rightInset
		c.blockInset = savedBlockInset
		c.declaresSizing, c.sizeInset = saved.declaresSizing, saved.sizeInset
		c.elID = saved.elID
		c.depth = saved.depth
		c.elGeomID = saved.elGeomID
	}()

	// A floated element leaves the flow: its own subtree is collected as a
	// unit, laid out at the width it asks for, and placed against one edge of
	// the column. The blocks after it flow beside it. Wikipedia's infobox is
	// the case this exists for.
	// floatRoot is the element whose own subtree is being collected for a
	// float, so that collecting it does not start a nested float of itself.
	if side := cs.floatSide; side != "" && isBlockDisplay(cs.display) && el != c.floatRoot {
		c.flush()
		savedRoot := c.floatRoot
		c.floatRoot = el
		// The subtree is collected from its containing block's frame again, so
		// the element's own width is resolved against the same parent as
		// before: a "width: 66%" column must not resolve against itself.
		curContW, curHasContW := c.contW, c.hasContW
		c.contW, c.hasContW = saved.contW, saved.hasContW
		blocks := c.collectNode(el)
		c.contW, c.hasContW = curContW, curHasContW
		c.floatRoot = savedRoot
		if len(blocks) > 0 {
			fb := renderBlock{
				kind:        blockFloat,
				floatSide:   side,
				floatBlocks: blocks,
				quote:       c.quote,
				// The float's own left edge, so the layout can put it against
				// its containing block's content edge rather than the column's.
				boxLeft: c.content - cs.borderW - cs.paddingLeft,
				// The width the collector resolved, not the style's: a
				// percentage resolves against the containing block it was
				// measured in, and re-resolving it here would use the column,
				// which is wider than the content box of a row carrying
				// "margin: 0 -15px".
				floatW:    c.sizeWidthPx,
				floatPct:  c.sizeWidthPct,
				hasFloatW: c.declaresSizing && c.hasSizeWidth,
			}
			c.stampRight(&fb)
			c.blocks = append(c.blocks, fb)
		}
		return
	}

	switch tag {
	case "br":
		c.flush()
		return
	case "hr":
		c.flush()
		rb := renderBlock{
			kind: blockRule, boxLeft: c.content, textX: c.content,
			quote: c.quote,
		}
		c.stampRight(&rb)
		c.blocks = append(c.blocks, rb)
		return
	case "img", "svg":
		// A block-level image or svg is a box on a line of its own: a logo
		// written "display:block" must not share a line with the text after
		// it, which would also let that text sit over the picture.
		if block {
			c.flush()
			defer c.flush()
		}
		if tag == "svg" {
			w, h := svgSize(el, cs, c.contW, c.hasContW)
			if w > 0 && h > 0 {
				if pic := c.page.rasterSVG(el, w, h, c.style.color); pic != nil {
					c.ensure(cs).spans = append(c.ensure(cs).spans, renderSpan{pic: pic, picW: w, picH: h})
				}
			}
			// The children of <svg> are shapes, not text or layout.
			return
		}
		if pic := c.page.image(resolveURL(c.page.baseURL(), imageSource(el))); pic != nil {
			if w, h := inlineImageSize(cs, pic, c.contW, c.hasContW); w > 0 && h > 0 {
				c.ensure(cs).spans = append(c.ensure(cs).spans, renderSpan{pic: pic, picW: w, picH: h})
				return
			}
		}
		// Without the bytes, the alt text is all there is to draw.
		if alt, ok := getAttr(el, "alt"); ok && strings.TrimSpace(alt) != "" {
			s := c.style
			s.italic = true
			c.ensure(cs).spans = append(c.ensure(cs).spans, renderSpan{text: "[" + strings.TrimSpace(alt) + "]", style: s})
		}
		return
	}

	if c.emitFormControl(el, cs, tag) {
		return
	}
	// A button is inline-block, but its own text-align applies to its content:
	// the user agent centers button text, and a full-width button with
	// text-align:center is common.
	if tag == "button" {
		c.flush()
		start := len(c.blocks)
		c.walkChildren(el)
		c.flush()
		align := cs.textAlign
		if align == "" {
			align = "center"
		}
		applyColumnAlign(c.blocks[start:], align)
		c.finishBlock(start, cs, boxed)
		return
	}

	// A list that is a flex or grid container is not a list: its items are
	// laid out in a row. go.dev's header menu is <ul style="display:flex">,
	// which stacks the menu entries down the page when the list path wins.
	if (tag == "ul" || tag == "ol") && (isFlexRowContainer(cs) || isGridContainer(cs)) &&
		c.collectFlexRow(el, cs, isGridContainer(cs)) {
		return
	}

	switch tag {
	case "ul", "ol":
		if !block {
			// An inline or inline-block list flows with the text around it:
			// lobste.rs writes its story tags as an inline-block <ul>, and
			// starting a block there puts every list on a line of its own.
			c.walkChildren(el)
			return
		}
		c.flush()
		start := len(c.blocks)
		c.lists = append(c.lists, listState{ordered: tag == "ol"})
		c.walkChildren(el)
		c.lists = c.lists[:len(c.lists)-1]
		c.flush()
		c.finishBlock(start, cs, boxed)
		return
	case "li":
		if !block {
			// An inline or inline-block list item flows with the text on its
			// line: a lobste.rs story lays its tags out as inline-block <li>
			// items, and flushing each one put every tag on its own line.
			c.walkChildren(el)
			return
		}
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
			start := len(c.blocks)
			c.ensure(cs)
			c.walkChildren(el)
			c.flush()
			c.finishBlock(start, cs, boxed)
		} else {
			c.walkChildren(el)
		}
		return
	case "blockquote":
		c.flush()
		start := len(c.blocks)
		c.quote++
		c.walkChildren(el)
		c.quote--
		c.flush()
		c.finishBlock(start, cs, boxed)
		return
	}

	if block && cs.display == "table" && c.collectTable(el, cs) {
		return
	}

	if block && (isFlexRowContainer(cs) || isGridContainer(cs)) &&
		c.collectFlexRow(el, cs, isGridContainer(cs)) {
		return
	}

	if block {
		c.flush()
		start := len(c.blocks)
		// This element's own block, so a box it declares is measured from its
		// content edge rather than from whichever descendant starts there.
		c.ensure(cs)
		c.cur.pre = c.pre
		c.cur.nowrap = cs.whiteSpace == "nowrap"
		// A positioned ancestor is the containing block for its absolute
		// descendants; attach them to its first block once it is built.
		var frame *absFrame
		frameParent := c.absOwner
		if cs.position == "relative" || cs.position == "sticky" {
			frame = &absFrame{}
			c.absOwner = frame
		}
		c.walkChildren(el)
		c.flush()
		if isFlexColumnContainer(cs) {
			applyColumnAlign(c.blocks[start:], cs.alignItems)
		}
		c.finishBlock(start, cs, boxed)
		if frame != nil {
			c.absOwner = frameParent
			if len(frame.children) > 0 && len(c.blocks) > start {
				c.blocks[start].abs = append(c.blocks[start].abs, frame.children...)
			} else if len(frame.children) > 0 && frameParent != nil {
				frameParent.children = append(frameParent.children, frame.children...)
			}
		}
		return
	}
	c.walkChildren(el)
}

// controlBlock flushes the current block and starts a fresh box-like block for a
// form control, returning its index so spans can be added to it.
func (c *collector) controlBlock(cs *computedStyle) int {
	c.flush()
	b := renderBlock{
		kind:      blockText,
		boxLeft:   c.content,
		textX:     c.content,
		quote:     c.quote,
		align:     cs.textAlign,
		leading:   cs.marginTop + cs.paddingTop,
		trailing:  cs.marginBottom + cs.paddingBottom,
		minHeight: cs.minHeight,
		marginTop: cs.marginTop, marginBottom: cs.marginBottom,
		paddingTop: cs.paddingTop, paddingBottom: cs.paddingBottom,
		// Inherit the current block-sizing context.
		hasSizing:       true,
		ownsSizing:      c.declaresSizing,
		ownerSeq:        c.elID,
		depth:           c.depth,
		ownerGeomID:     c.elGeomID,
		boxDepth:        c.depth,
		sizeInset:       c.sizeInset,
		sizeLeft:        c.sizeLeft,
		boxWidthPx:      c.sizeWidthPx,
		boxWidthPct:     c.sizeWidthPct,
		hasBoxWidth:     c.hasSizeWidth,
		boxMaxWidthPx:   c.sizeMaxPx,
		boxMaxWidthPct:  c.sizeMaxPct,
		hasBoxMaxWidth:  c.hasSizeMax,
		boxAutoLeft:     c.sizeAutoLeft,
		boxAutoRight:    c.sizeAutoRight,
		hasSizeOwner:    c.hasSizeOwner,
		sizeBoxLeft:     c.sizeBoxLeft,
		sizePadLeft:     c.sizePadLeft,
		sizePadRight:    c.sizePadRight,
		sizeMarginRight: c.sizeMarginRight,
		borderBox:       cs.boxSizingBorderBox,
		marginRight:     cs.marginRight,
		paddingRight:    cs.paddingRight,
		shadowX:         cs.shadowX,
		shadowY:         cs.shadowY,
		shadowBlur:      cs.shadowBlur,
		shadowSpread:    cs.shadowSpread,
		shadowColor:     scaleAlpha(cs.shadowColor, cs.opacity),
		hasShadow:       cs.hasShadow,
		// The control's box is drawn by the layout, not per line.
		boxID:       c.nextBoxID(),
		borderLeft:  c.content - cs.paddingLeft - cs.borderW,
		hasBorder:   cs.hasBorder,
		borderW:     cs.borderW,
		borderColor: scaleAlpha(cs.borderColor, cs.opacity),
		radiusPx:    cs.radiusPx,
		radiusPct:   cs.radiusPct,
	}
	if c.hasBG {
		b.boxBG, b.boxHasBG = c.bg, true
	}
	c.stampRight(&b)
	c.blocks = append(c.blocks, b)
	return len(c.blocks) - 1
}

// isFlexColumnContainer reports whether an element stacks its children with a
// column flex layout. align-items then aligns them horizontally, which a plain
// block stack does not do, so the produced blocks are centered.
func isFlexColumnContainer(cs *computedStyle) bool {
	return (cs.display == "flex" || cs.display == "inline-flex") && cs.flexDirection == "column"
}

func applyColumnAlign(blocks []renderBlock, align string) {
	switch align {
	case "center":
		for i := range blocks {
			blocks[i].align = "center"
		}
	case "end", "right":
		for i := range blocks {
			blocks[i].align = "right"
		}
	}
}

// isFlexRowContainer reports whether an element lays its children out in a row.
// A column flex container needs no special handling: a column of blocks is what
// ordinary flow already produces.
func isFlexRowContainer(cs *computedStyle) bool {
	return (cs.display == "flex" || cs.display == "inline-flex") && cs.flexDirection != "column"
}

// isGridContainer reports whether an element lays its children out in a grid.
// A grid with no template-columns is a single-column stack, which is what
// ordinary flow already produces.
func isGridContainer(cs *computedStyle) bool {
	return (cs.display == "grid" || cs.display == "inline-grid") &&
		(len(cs.grid.tracks) > 0 || cs.grid.autoFill)
}

// collectTable builds a table block from an element's rows and cells. Rows are
// gathered through the section elements (tbody/thead/tfoot); the cells of a row
// become a tableRow held as a list of block columns, so the layout can put them
// side by side in shared columns.
func (c *collector) collectTable(el *html.Node, cs *computedStyle) bool {
	c.flush()
	spacing := 0.0
	if v := attrOf(el, "cellspacing"); v != "" {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && f >= 0 {
			spacing = f
		}
	}
	b := renderBlock{
		kind:          blockFlex,
		flexRow:       true,
		table:         true,
		inlineBox:     isInlineFlexDisplay(cs.display),
		align:         cs.textAlign,
		boxLeft:       c.content,
		textX:         c.content,
		quote:         c.quote,
		ownsSizing:    c.declaresSizing,
		ownerSeq:      c.elID,
		depth:         c.depth,
		ownerGeomID:   c.elGeomID,
		gap:           spacing,
		rowGap:        spacing,
		justify:       cs.justifyContent,
		alignItems:    cs.alignItems,
		leading:       cs.marginTop + cs.paddingTop,
		trailing:      cs.marginBottom + cs.paddingBottom,
		marginTop:     cs.marginTop,
		marginBottom:  cs.marginBottom,
		paddingTop:    cs.paddingTop,
		paddingBottom: cs.paddingBottom,
		// The table's own right edges; nothing else sets them for a table.
		marginRight:   cs.marginRight,
		paddingRight:  cs.paddingRight,
		hasRightEdges: true,
	}
	// A table with a declared width fills it, distributing the extra space
	// across its columns; one without shrinks to its content.
	if cs.hasWidth && (cs.widthPx > 0 || cs.widthPct > 0) {
		w := cs.widthPx
		if cs.widthPct > 0 {
			w += cs.widthPct / 100 * c.contW
		}
		if w > 0 {
			b.tableStretch = true
		}
	}
	if c.hasBG {
		b.bg, b.hasBG, b.bgFull = c.bg, true, true
	}
	var walkRows func(n *html.Node)
	walkRows = func(n *html.Node) {
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			if ch.Type != html.ElementNode {
				continue
			}
			ccs := c.engine.compute(ch)
			if ccs == nil || ccs.display == "none" {
				continue
			}
			switch ccs.display {
			case "table-row-group", "table-row":
				if strings.EqualFold(ch.Data, "tr") {
					var row tableRow
					for cell := ch.FirstChild; cell != nil; cell = cell.NextSibling {
						if cell.Type != html.ElementNode {
							continue
						}
						ct := strings.ToLower(cell.Data)
						if ct != "td" && ct != "th" {
							continue
						}
						span := 1
						if v := attrOf(cell, "colspan"); v != "" {
							if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
								span = n
							}
						}
						// An empty cell still occupies its columns, which is
						// how a colspan spacer keeps later cells aligned.
						row.cells = append(row.cells, c.collectNode(cell))
						row.spans = append(row.spans, span)
					}
					if len(row.cells) > 0 {
						b.rows = append(b.rows, row)
					}
					continue
				}
				walkRows(ch)
			}
		}
	}
	walkRows(el)
	if len(b.rows) == 0 {
		return false
	}
	c.stampRight(&b)
	c.blocks = append(c.blocks, b)
	// The table's own declared width has to reach the layout: it is the width
	// the columns are distributed over.
	c.assignSizing(len(c.blocks)-1, cs, 0)
	c.content = b.boxLeft
	return true
}

// collectFlexRow builds a flex row block from an element's children and appends
// it, returning true when it produced at least one child. Each child is
// collected with its offsets relative to its own left edge, so the row layout
// can place it at any x.
func (c *collector) collectFlexRow(el *html.Node, cs *computedStyle, grid bool) bool {
	c.flush()
	b := renderBlock{
		kind:          blockFlex,
		flexRow:       true,
		grid:          grid,
		inlineBox:     isInlineFlexDisplay(cs.display),
		align:         cs.textAlign,
		gridTmpl:      cs.grid,
		boxLeft:       c.content,
		textX:         c.content,
		quote:         c.quote,
		ownsSizing:    c.declaresSizing,
		ownerSeq:      c.elID,
		depth:         c.depth,
		ownerGeomID:   c.elGeomID,
		gap:           cs.columnGap,
		rowGap:        cs.rowGap,
		wrap:          cs.flexWrap,
		justify:       cs.justifyContent,
		alignItems:    cs.alignItems,
		leading:       cs.marginTop + cs.paddingTop,
		trailing:      cs.marginBottom + cs.paddingBottom,
		marginTop:     cs.marginTop,
		marginBottom:  cs.marginBottom,
		paddingTop:    cs.paddingTop,
		paddingBottom: cs.paddingBottom,
		// The row's own right edges. Without them the box was measured short
		// of its padding, and an ancestor's edges replaced the row's own.
		marginRight:   cs.marginRight,
		paddingRight:  cs.paddingRight,
		hasRightEdges: true,
	}
	if c.hasBG {
		b.bg, b.hasBG, b.bgFull = c.bg, true, true
	}
	for ch := el.FirstChild; ch != nil; ch = ch.NextSibling {
		var (
			col   []renderBlock
			child *computedStyle
		)
		switch ch.Type {
		case html.TextNode:
			if strings.TrimSpace(ch.Data) == "" {
				continue
			}
			col = c.collectNode(ch)
		case html.ElementNode:
			child = c.engine.compute(ch)
			if child == nil || child.display == "none" || child.visibility == "hidden" {
				continue
			}
			col = c.collectNode(ch)
		default:
			continue
		}
		if len(col) == 0 {
			continue
		}
		b.children = append(b.children, col)
		grow, shrink, bx, bp, has := 0.0, 1.0, 0.0, 0.0, false
		if child != nil {
			grow = child.flexGrow
			shrink = child.flexShrink
			switch {
			case child.hasFlexBasis:
				bx, bp, has = child.flexBasisPx, child.flexBasisPct, true
			case child.hasWidth:
				bx, bp, has = child.widthPx, child.widthPct, true
			}
		}
		b.grow = append(b.grow, grow)
		b.shrink = append(b.shrink, shrink)
		b.basisPx = append(b.basisPx, bx)
		b.basisPct = append(b.basisPct, bp)
		b.hasBasis = append(b.hasBasis, has)
		// A grid-column value only means something against the container's
		// own template, so the track and span are read from b.gridTmpl.
		gridCol, span, gridRow, rowSpan := -1, 1, -1, 1
		if child != nil {
			// A named area gives both axes; a grid-column or grid-row on its
			// own gives one. Anything left over is auto-placed.
			if r, c, rs, cs2, ok := b.gridTmpl.areaRect(child.gridAreaName); ok {
				gridRow, gridCol, rowSpan, span = r, c, rs, cs2
			} else {
				gridCol, span = b.gridTmpl.place(child.gridColumn, len(b.gridTmpl.tracks))
				gridRow, rowSpan = b.gridTmpl.placeRow(child.gridRow, len(b.gridTmpl.rows))
			}
		}
		b.gridCols = append(b.gridCols, gridCol)
		b.gridSpans = append(b.gridSpans, span)
		b.gridRows = append(b.gridRows, gridRow)
		b.gridRowSpans = append(b.gridRowSpans, rowSpan)
	}
	if len(b.children) == 0 {
		return false
	}
	c.stampRight(&b)
	c.blocks = append(c.blocks, b)
	// The row's own width, max-width and auto margins size and center it like
	// any other block: a "max-width: 300px; margin: 0 auto" row is centered and
	// its items share 300px, not the whole column.
	c.assignSizing(len(c.blocks)-1, cs, 0)
	// A row is a box like any other block, so its own background, border and
	// shadow are drawn: a colored page header is a flex row.
	if cs.hasBackground || cs.hasBorder || cs.hasRadius() || cs.hasShadow {
		c.assignBox(len(c.blocks)-1, cs)
	}
	return true
}

// collectNode walks one node with its horizontal origin reset to zero, and
// returns the blocks it produced (not appended to the main list).
func (c *collector) collectNode(n *html.Node) []renderBlock {
	saved := c.content
	savedInset, savedBlockInset := c.rightInset, c.blockInset
	c.content = 0
	// The subtree is measured from its own left edge, so the page column's
	// right edge no longer applies: the caller sets the width instead.
	c.rightInset, c.blockInset = 0, 0
	// A float or a flex item is a containing block of its own, whose width the
	// layout decides. An ancestor's width context must not reach inside it: a
	// "width:1200px" wrapper around a flex row made every item in the row
	// measure itself against the wrapper instead of its own column.
	savedSizing := sizingContext{
		owner: c.hasSizeOwner, left: c.sizeLeft,
		widthPx: c.sizeWidthPx, widthPct: c.sizeWidthPct, hasWidth: c.hasSizeWidth,
		widthIsOwn: c.sizeWidthIsOwn,
		maxPx:      c.sizeMaxPx, maxPct: c.sizeMaxPct, hasMax: c.hasSizeMax,
		autoLeft: c.sizeAutoLeft, autoRight: c.sizeAutoRight,
		boxLeft: c.sizeBoxLeft, padLeft: c.sizePadLeft, padRight: c.sizePadRight,
		marginRight: c.sizeMarginRight, inset: c.sizeInset,
	}
	c.hasSizeOwner, c.sizeLeft = false, 0
	c.sizeWidthPx, c.sizeWidthPct, c.hasSizeWidth, c.sizeWidthIsOwn = 0, 0, false, false
	c.sizeMaxPx, c.sizeMaxPct, c.hasSizeMax = 0, 0, false
	c.sizeAutoLeft, c.sizeAutoRight = false, false
	c.sizeBoxLeft, c.sizePadLeft, c.sizePadRight, c.sizeMarginRight, c.sizeInset = 0, 0, 0, 0, 0
	defer func() {
		c.hasSizeOwner, c.sizeLeft = savedSizing.owner, savedSizing.left
		c.sizeWidthPx, c.sizeWidthPct, c.hasSizeWidth = savedSizing.widthPx, savedSizing.widthPct, savedSizing.hasWidth
		c.sizeWidthIsOwn = savedSizing.widthIsOwn
		c.sizeMaxPx, c.sizeMaxPct, c.hasSizeMax = savedSizing.maxPx, savedSizing.maxPct, savedSizing.hasMax
		c.sizeAutoLeft, c.sizeAutoRight = savedSizing.autoLeft, savedSizing.autoRight
		c.sizeBoxLeft, c.sizePadLeft, c.sizePadRight = savedSizing.boxLeft, savedSizing.padLeft, savedSizing.padRight
		c.sizeMarginRight, c.sizeInset = savedSizing.marginRight, savedSizing.inset
	}()
	c.flush()
	start := len(c.blocks)
	c.walkNode(n)
	c.flush()
	out := append([]renderBlock(nil), c.blocks[start:]...)
	c.blocks = c.blocks[:start]
	c.content = saved
	c.rightInset, c.blockInset = savedInset, savedBlockInset
	return out
}

// sizingContext is the block-sizing context of the element being walked, saved
// around a subtree that lays out in a box of its own.
type sizingContext struct {
	owner                                   bool
	left                                    float64
	widthPx, widthPct                       float64
	hasWidth, widthIsOwn                    bool
	maxPx, maxPct                           float64
	hasMax                                  bool
	autoLeft, autoRight                     bool
	boxLeft, padLeft, padRight, marginRight float64
	inset                                   float64
}

// applyBoxEdges adds an element's vertical margin and padding to the first and
// last blocks its subtree produced. Doing it by index rather than on c.cur
// matters because a block whose first child is itself a block is flushed away
// before it has any spans, and its edges would be lost with it.
func (c *collector) applyBoxEdges(start int, cs *computedStyle) {
	if cs == nil || start >= len(c.blocks) {
		return
	}
	// A block a descendant produced is not inside this element's own box: the
	// edges added here are the element's, and its rectangle is the block's
	// grown by them.
	owns := func(b *renderBlock) bool { return b.ownerSeq == c.elID }
	edge := cs.paddingTop + cs.borderW
	if !owns(&c.blocks[start]) {
		addAncPad(&c.blocks[start], c.elGeomID, 0, 0, edge, 0)
	}
	if last := &c.blocks[len(c.blocks)-1]; !owns(last) {
		addAncPad(last, c.elGeomID, 0, 0, 0, cs.paddingBottom+cs.borderW)
	}
	first := &c.blocks[start]
	first.marginTop += cs.marginTop
	first.paddingTop += cs.paddingTop
	first.leading = first.marginTop + first.paddingTop
	last := &c.blocks[len(c.blocks)-1]
	last.marginBottom += cs.marginBottom
	last.paddingBottom += cs.paddingBottom
	last.trailing = last.marginBottom + last.paddingBottom
	// Right edges are assigned rather than added, and an element that produces
	// no blocks of its own hands its range to its first descendant's blocks:
	// those already carry their own element's right edges, so keep them.
	for i := start; i < len(c.blocks); i++ {
		if !owns(&c.blocks[i]) {
			addAncPad(&c.blocks[i], c.elGeomID,
				cs.paddingLeft+cs.borderW, cs.paddingRight+cs.borderW, 0, 0)
		}
		if c.blocks[i].hasRightEdges {
			continue
		}
		c.blocks[i].hasRightEdges = true
		c.blocks[i].marginRight = cs.marginRight
		c.blocks[i].paddingRight = cs.paddingRight
	}
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

// applyTextTransform applies CSS text-transform to a text node's content, which
// a browser does at paint time and getComputedStyle does not expose.
func applyTextTransform(mode, s string) string {
	switch mode {
	case "uppercase":
		return strings.ToUpper(s)
	case "lowercase":
		return strings.ToLower(s)
	case "capitalize":
		return capitalizeWords(s)
	}
	return s
}

func capitalizeWords(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	start := true
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v' {
			start = true
			b.WriteRune(r)
			continue
		}
		if start {
			b.WriteRune(unicode.ToUpper(r))
			start = false
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// --- text helpers ---

func collapseWhitespace(s string) string {
	trimmedLeft := strings.TrimLeft(s, " \t\r\n\f\v")
	trimmedRight := strings.TrimRight(s, " \t\r\n\f\v")
	fields := strings.Fields(s)
	if len(fields) == 0 {
		// A node that is nothing but whitespace still separates the runs on
		// either side of it, so it collapses to one space rather than to
		// nothing. Dropping it ran "by" into "Albert Einstein" and every tag
		// into the next one. The line wrapper suppresses a space at the start
		// or end of a line, so this cannot leave a visible edge space.
		return " "
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
	// Rules is the number of rules parsed from it. Media queries are evaluated
	// while parsing, so this number is only meaningful with Width.
	Rules int
	// Fonts is the number of @font-face rules in the sheet. They are not style
	// rules, so a font provider's sheet can be all fonts and no rules.
	Fonts int
	// Width is the layout width the rules were counted at.
	Width float64
	// Err is why the sheet is not applied, empty when it is.
	Err string
}

// StyleSheets lists the stylesheets the page itself declares, in document
// order, fetching the linked ones the first time it is called. Nothing is
// injected, and nothing the page declares is left out: a sheet that failed or
// was skipped appears with Err set, so "the site's CSS did not load" can be
// told apart from "the page has no CSS to load".
func (p *Page) StyleSheets() []StyleSheet {
	return p.StyleSheetsAt(p.viewportWidth())
}

// StyleSheetsAt is StyleSheets counted at a given layout width, so a report can
// name the same width a screenshot renders at. The counts differ because @media
// is evaluated while parsing.
func (p *Page) StyleSheetsAt(width float64) []StyleSheet {
	if width <= 0 {
		width = defaultLayoutWidth
	}
	sheets := p.styleSheets()
	out := make([]StyleSheet, 0, len(sheets))
	for _, s := range sheets {
		info := StyleSheet{Href: s.href, Media: s.media, Inline: s.href == "", Width: width}
		if p.loadSheet(s) {
			order := 0
			rules, _, stats := parseCSSStylesheet(s.source, width, &order)
			info.Bytes = len(s.source)
			info.Rules = len(rules)
			info.Fonts = len(stats.fontFaces)
		}
		if info.Err = s.err; info.Err == "" && !s.loaded {
			info.Err = "not loaded"
		}
		out = append(out, info)
	}
	return out
}
