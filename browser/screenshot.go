package browser

import (
	"errors"
	"fmt"
	"image/color"
	"math"
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
}

// Screenshot renders the page to a PNG.
//
// This is a document renderer: it flows text at the requested width and draws
// headings, paragraphs, lists, preformatted blocks, quotes and rules, with
// bold/italic/monospace runs, link styling and inline colors. It is not a web
// renderer: there is no CSS box model, no images, no backgrounds and no
// borders. Layout, font parsing and rasterization happen only in this call, so
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
	maxHeight := opts.MaxHeight
	if maxHeight <= 0 {
		maxHeight = 20000
	}

	base := defaultRenderStyle()
	c := &collector{style: base}
	c.walkChildren(p.doc)
	c.flush()

	doc := layoutBlocks(c.blocks, width, base.size)
	if doc.height > maxHeight {
		doc.height = maxHeight
	}
	return renderPNG(doc, scale)
}

// --- DOM to blocks ---

type listState struct {
	ordered bool
	index   int
}

type collector struct {
	blocks []renderBlock
	cur    *renderBlock
	style  renderStyle
	indent float64
	quote  int
	pre    bool
	lists  []listState
	marker string
}

var renderHeadingSizes = map[string]float64{
	"h1": 32, "h2": 26, "h3": 22, "h4": 19, "h5": 17, "h6": 15,
}

var renderBlockTags = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"body": true, "dd": true, "div": true, "dl": true, "dt": true,
	"fieldset": true, "figcaption": true, "figure": true, "footer": true,
	"form": true, "header": true, "html": true, "li": true, "main": true,
	"nav": true, "ol": true, "p": true, "pre": true, "section": true,
	"table": true, "tbody": true, "td": true, "tfoot": true, "th": true,
	"thead": true, "tr": true, "ul": true, "hgroup": true, "details": true,
	"summary": true, "caption": true,
}

var renderSkipTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "template": true,
	"head": true, "title": true, "meta": true, "link": true, "base": true,
	"iframe": true, "svg": true, "canvas": true, "audio": true, "video": true,
	"object": true, "embed": true, "input": true, "textarea": true,
	"select": true, "option": true, "source": true, "track": true,
	"area": true, "map": true, "col": true, "colgroup": true, "param": true,
	"dialog": true, "slot": true, "math": true,
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

func (c *collector) leading() float64 {
	if len(c.blocks) == 0 && c.cur == nil {
		return 0
	}
	return 10
}

func (c *collector) ensure() *renderBlock {
	if c.cur == nil {
		c.cur = &renderBlock{
			kind:    blockText,
			indent:  c.indent,
			quote:   c.quote,
			pre:     c.pre,
			marker:  c.marker,
			leading: c.leading(),
		}
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
	if c.pre {
		b := c.ensure()
		b.spans = append(b.spans, renderSpan{text: s, style: c.style})
		return
	}
	collapsed := collapseWhitespace(s)
	if collapsed == "" {
		return
	}
	b := c.ensure()
	b.spans = append(b.spans, renderSpan{text: collapsed, style: c.style})
}

func (c *collector) walkElement(el *html.Node) {
	tag := strings.ToLower(el.Data)
	if renderSkipTags[tag] {
		return
	}
	if hasAttr(el, "hidden") {
		return
	}

	// A block's own style, then children on top of it.
	savedStyle := c.style
	st := c.style
	if s, ok := getAttr(el, "style"); ok {
		inl := parseInlineStyle(s)
		if inl.hidden {
			return
		}
		applyInlineStyle(&st, inl)
	}
	c.style = st
	defer func() { c.style = savedStyle }()

	switch tag {
	case "br":
		// A hard break ends the current visual block.
		c.flush()
		return
	case "hr":
		c.flush()
		c.blocks = append(c.blocks, renderBlock{
			kind: blockRule, indent: c.indent, quote: c.quote, leading: 10,
		})
		return
	case "img":
		if alt, ok := getAttr(el, "alt"); ok && strings.TrimSpace(alt) != "" {
			s := c.style
			s.italic = true
			c.ensure().spans = append(c.ensure().spans, renderSpan{text: "[" + strings.TrimSpace(alt) + "]", style: s})
		}
		return
	}

	if size, ok := renderHeadingSizes[tag]; ok {
		c.flush()
		c.style.size = size
		c.style.bold = true
		c.style.mono = false
		c.cur = &renderBlock{kind: blockText, indent: c.indent, quote: c.quote, leading: 20}
		c.walkChildren(el)
		c.flush()
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
		if n := len(c.lists); n > 0 && c.lists[n-1].ordered {
			c.lists[n-1].index++
			c.marker = fmt.Sprintf("%d.", c.lists[n-1].index)
		} else {
			c.marker = "\u2022"
		}
		savedIndent := c.indent
		c.indent += 22
		c.walkChildren(el)
		c.flush()
		c.indent = savedIndent
		return
	case "blockquote":
		c.flush()
		savedIndent, savedQuote := c.indent, c.quote
		c.indent += 10
		c.quote++
		c.walkChildren(el)
		c.indent, c.quote = savedIndent, savedQuote
		c.flush()
		return
	case "pre":
		c.flush()
		savedPre := c.pre
		c.pre = true
		c.style.mono = true
		if c.style.size > 15 {
			c.style.size = 14
		}
		c.walkChildren(el)
		c.pre = savedPre
		c.flush()
		return
	}

	if renderBlockTags[tag] {
		c.flush()
		c.walkChildren(el)
		c.flush()
		return
	}

	// Inline elements adjust the current style only.
	switch tag {
	case "b", "strong":
		c.style.bold = true
	case "i", "em", "cite", "var", "dfn":
		c.style.italic = true
	case "code", "kbd", "samp", "tt":
		c.style.mono = true
	case "u", "ins":
		c.style.underline = true
	case "s", "del", "strike":
		c.style.underline = true
	case "a":
		if href, ok := getAttr(el, "href"); ok && href != "" && !strings.HasPrefix(strings.TrimSpace(href), "javascript:") {
			if !c.style.link {
				c.style.color = renderLinkColor
			}
			c.style.link = true
			c.style.underline = true
		}
	case "small":
		c.style.size = math.Max(10, c.style.size-2)
	case "big":
		c.style.size += 2
	}
	c.walkChildren(el)
}

// --- inline style parsing ---

type inlineStyle struct {
	hidden    bool
	color     string
	fontSize  float64
	fontBold  bool
	fontItal  bool
	underline bool
	mono      bool
}

func parseInlineStyle(s string) inlineStyle {
	var out inlineStyle
	for _, decl := range strings.Split(s, ";") {
		key, val, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)
		low := strings.ToLower(val)
		switch key {
		case "display":
			if low == "none" {
				out.hidden = true
			}
		case "visibility":
			if low == "hidden" {
				out.hidden = true
			}
		case "color":
			out.color = low
		case "font-size":
			if px, ok := parseCSSPx(low); ok {
				out.fontSize = px
			}
		case "font-weight":
			if low == "bold" || low == "bolder" {
				out.fontBold = true
			} else if n, err := strconv.Atoi(low); err == nil && n >= 600 {
				out.fontBold = true
			}
		case "font-style":
			if strings.Contains(low, "italic") || strings.Contains(low, "oblique") {
				out.fontItal = true
			}
		case "text-decoration", "text-decoration-line":
			if strings.Contains(low, "underline") {
				out.underline = true
			}
		case "font-family":
			if strings.Contains(low, "mono") || strings.Contains(low, "courier") || strings.Contains(low, "consol") {
				out.mono = true
			}
		}
	}
	return out
}

func parseCSSPx(v string) (float64, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimSuffix(v, "px")
	v = strings.TrimSuffix(v, "pt")
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || f <= 0 {
		return 0, false
	}
	if f > 96 {
		f = 96
	}
	return f, true
}

func applyInlineStyle(st *renderStyle, in inlineStyle) {
	if in.color != "" {
		if c, ok := parseCSSColor(in.color); ok {
			st.color = c
		}
	}
	if in.fontSize > 0 {
		st.size = in.fontSize
	}
	if in.fontBold {
		st.bold = true
	}
	if in.fontItal {
		st.italic = true
	}
	if in.underline {
		st.underline = true
	}
	if in.mono {
		st.mono = true
	}
}

// parseCSSColor understands #rgb, #rrggbb and a few keyword colors.
func parseCSSColor(v string) (color.RGBA, bool) {
	v = strings.TrimSpace(strings.ToLower(v))
	switch v {
	case "black":
		return color.RGBA{0, 0, 0, 0xff}, true
	case "white":
		return color.RGBA{0xff, 0xff, 0xff, 0xff}, true
	case "red":
		return color.RGBA{0xc0, 0x39, 0x2b, 0xff}, true
	case "gray", "grey":
		return color.RGBA{0x80, 0x80, 0x80, 0xff}, true
	case "blue":
		return color.RGBA{0x0b, 0x57, 0xd0, 0xff}, true
	case "green":
		return color.RGBA{0x1e, 0x7e, 0x34, 0xff}, true
	}
	if !strings.HasPrefix(v, "#") {
		return color.RGBA{}, false
	}
	hexs := v[1:]
	switch len(hexs) {
	case 3:
		r := hexs[0:1]
		g := hexs[1:2]
		b := hexs[2:3]
		hexs = r + r + g + g + b + b
	case 6:
	default:
		return color.RGBA{}, false
	}
	n, err := strconv.ParseUint(hexs, 16, 32)
	if err != nil {
		return color.RGBA{}, false
	}
	return color.RGBA{uint8(n >> 16), uint8(n >> 8), uint8(n), 0xff}, true
}

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
