package browser

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// Native form controls are drawn as small rasterized graphics, then flow as
// inline replaced boxes like an icon. There is no platform widget toolkit here,
// so a checkbox is a square with a check, a radio is a dot in a ring, a range is
// a track with a thumb and a color input is a swatch. Text-bearing controls
// (text inputs, buttons, file, select) emit their value as text instead.

const widgetScale = 2.0

var (
	widgetBorder = color.RGBA{R: 0x8a, G: 0x8a, B: 0x8a, A: 0xff}
	widgetFace   = color.RGBA{R: 0xfa, G: 0xfa, B: 0xfa, A: 0xff}
	widgetAccent = color.RGBA{R: 0x0b, G: 0x57, B: 0xd0, A: 0xff}
)

// rasterWidget renders one control at 2x, so scaling it down to the CSS size in
// the draw pass gives smooth edges.
func (p *Page) rasterWidget(kind string, w, h float64, checked bool, accent color.RGBA) *pageImage {
	if w < 1 || h < 1 {
		return nil
	}
	key := kind + "|" + strconv.FormatFloat(w, 'f', 1, 64) + "x" + strconv.FormatFloat(h, 'f', 1, 64) +
		"|" + strconv.FormatBool(checked) + "|" + cssHexString(accent)
	if p.widgetImages == nil {
		p.widgetImages = map[string]*pageImage{}
	}
	if img, ok := p.widgetImages[key]; ok {
		return img
	}
	rw := int(w*widgetScale + 0.5)
	rh := int(h*widgetScale + 0.5)
	if rw < 1 || rh < 1 {
		return nil
	}
	dst := image.NewRGBA(image.Rect(0, 0, rw, rh))
	switch kind {
	case "checkbox":
		strokeRect(dst, 0, 0, rw, rh, max1(rw/13), widgetBorder)
		fillRectI(dst, 1, 1, rw-2, rh-2, widgetFace)
		if checked {
			checkMark(dst, rw, rh, accent)
		}
	case "radio":
		fillCircle(dst, float64(rw)/2, float64(rh)/2, float64(rw)/2-1, widgetFace)
		ringCircle(dst, float64(rw)/2, float64(rh)/2, float64(rw)/2-1, float64(max1(rw/13)), widgetBorder)
		if checked {
			fillCircle(dst, float64(rw)/2, float64(rh)/2, float64(rw)*0.24, accent)
		}
	case "range":
		trackH := max1(rh / 6)
		fillRectI(dst, 0, rh/2-trackH/2, rw, trackH, widgetBorder)
		fillCircle(dst, float64(rw)*0.3, float64(rh)/2, float64(rh)/2-1, widgetFace)
		ringCircle(dst, float64(rw)*0.3, float64(rh)/2, float64(rh)/2-1, float64(max1(rw/40)), widgetBorder)
	case "color":
		strokeRect(dst, 0, 0, rw, rh, max1(rw/24), widgetBorder)
		fillRectI(dst, 1, 1, rw-2, rh-2, accent)
	case "caret":
		// The downward triangle a select shows. It is drawn rather than set
		// in a glyph: the fonts have no "\u25be", so every select used to
		// show a missing-glyph box where its arrow belongs.
		for y := 0; y < rh; y++ {
			half := (rw / 2) * (rh - y) / rh
			fillRectI(dst, rw/2-half, y, 2*half, 1, accent)
		}
	default:
		return nil
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil
	}
	img := &pageImage{src: "widget:" + kind, data: buf.Bytes(), size: image.Point{X: rw, Y: rh}}
	p.widgetImages[key] = img
	return img
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

func fillRectI(img *image.RGBA, x, y, w, h int, c color.RGBA) {
	if w <= 0 || h <= 0 {
		return
	}
	fillRect(img, float64(x), float64(y), float64(w), float64(h), c)
}

// strokeRect draws a one-colour border of thickness t inside the rectangle.
func strokeRect(img *image.RGBA, x, y, w, h, t int, c color.RGBA) {
	fillRectI(img, x, y, w, t, c)     // top
	fillRectI(img, x, y+h-t, w, t, c) // bottom
	fillRectI(img, x, y, t, h, c)     // left
	fillRectI(img, x+w-t, y, t, h, c) // right
}

func fillCircle(img *image.RGBA, cx, cy, r float64, c color.RGBA) {
	if r <= 0 {
		return
	}
	for y := int(cy - r); y <= int(cy+r)+1; y++ {
		for x := int(cx - r); x <= int(cx+r)+1; x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			if dx*dx+dy*dy <= r*r {
				setPixel(img, x, y, c)
			}
		}
	}
}

func ringCircle(img *image.RGBA, cx, cy, r, t float64, c color.RGBA) {
	if r <= 0 {
		return
	}
	inner := r - t
	if inner < 0 {
		inner = 0
	}
	for y := int(cy - r); y <= int(cy+r)+1; y++ {
		for x := int(cx - r); x <= int(cx+r)+1; x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			d2 := dx*dx + dy*dy
			if d2 <= r*r && d2 >= inner*inner {
				setPixel(img, x, y, c)
			}
		}
	}
}

func setPixel(img *image.RGBA, x, y int, c color.RGBA) {
	if !(image.Point{x, y}.In(img.Bounds())) {
		return
	}
	img.SetRGBA(x, y, color.RGBA{R: c.R, G: c.G, B: c.B, A: 0xff})
}

// checkMark draws a tick inside the box from (x0.25,y0.55) to (x0.45,y0.75) to
// (x0.78,y0.28), as a few thick segments.
func checkMark(img *image.RGBA, w, h int, c color.RGBA) {
	t := max1(w / 7)
	thickLine(img, float64(w)*0.22, float64(h)*0.52, float64(w)*0.42, float64(h)*0.72, t, c)
	thickLine(img, float64(w)*0.42, float64(h)*0.72, float64(w)*0.80, float64(h)*0.26, t, c)
}

func thickLine(img *image.RGBA, x0, y0, x1, y1 float64, t int, c color.RGBA) {
	n := int(maxF(absF(x1-x0), absF(y1-y0))) + 1
	for i := 0; i <= n; i++ {
		f := float64(i) / float64(n)
		x := x0 + (x1-x0)*f
		y := y0 + (y1-y0)*f
		fillRectI(img, int(x)-t/2, int(y)-t/2, t, t, c)
	}
}

func absF(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// emitFormControl writes a form control's visible content as spans: a widget
// graphic for the box-like controls, and text for the ones that show a value.
func (c *collector) emitFormControl(el *html.Node, cs *computedStyle, tag string) bool {
	// A control's padding belongs to the box the layout draws around its
	// value, so its own text runs must not carry it too: the run and the box
	// would each add it, and the control came out padding wider than a
	// browser's.
	style := c.style
	style.padLeft, style.padRight, style.padTop, style.padBottom, style.hasPad = 0, 0, 0, 0, false
	// The box carries the control's background too, so a run behind the text
	// would paint it a second time and darken a translucent colour.
	style.hasBG = false
	typ := strings.ToLower(strings.TrimSpace(attrOf(el, "type")))
	switch tag {
	case "select":
		idx := c.controlBlock(cs)
		if label := selectedOptionText(el); label != "" {
			c.blocks[idx].spans = append(c.blocks[idx].spans, renderSpan{text: label, style: style})
		}
		// The arrow is a drawn triangle, not the "\u25be" the fonts have no
		// glyph for; that character drew a missing-glyph box on every page
		// with a select.
		c.blocks[idx].spans = append(c.blocks[idx].spans, renderSpan{text: " ", style: style})
		if pic := c.page.rasterWidget("caret", 9, 5, false, style.color); pic != nil {
			c.blocks[idx].spans = append(c.blocks[idx].spans, renderSpan{pic: pic, picW: 9, picH: 5})
		}
		return true
	case "textarea":
		idx := c.controlBlock(cs)
		c.blocks[idx].pre = true
		v := textContent(el)
		if strings.TrimSpace(v) == "" {
			v = attrOf(el, "placeholder")
		}
		if v != "" {
			c.blocks[idx].spans = append(c.blocks[idx].spans, renderSpan{text: v, style: style})
		}
		c.controlPlaceholder(idx, style)
		return true
	case "input":
		switch typ {
		case "hidden":
			return true
		case "checkbox", "radio":
			size := widgetSize(cs, 13)
			if pic := c.page.rasterWidget(typ, size, size, hasAttr(el, "checked"), widgetAccent); pic != nil {
				c.ensure(cs).spans = append(c.ensure(cs).spans, renderSpan{pic: pic, picW: size, picH: size})
			}
			return true
		case "range":
			w := widgetWidth(cs, 129)
			h := widgetHeight(cs, 16)
			if pic := c.page.rasterWidget("range", w, h, false, widgetAccent); pic != nil {
				c.ensure(cs).spans = append(c.ensure(cs).spans, renderSpan{pic: pic, picW: w, picH: h})
			}
			return true
		case "color":
			w := widgetWidth(cs, 24)
			h := widgetHeight(cs, 24)
			fill := widgetFace
			if col, ok := parseCSSColor(attrOf(el, "value")); ok {
				fill = col
			}
			if pic := c.page.rasterWidget("color", w, h, false, fill); pic != nil {
				c.ensure(cs).spans = append(c.ensure(cs).spans, renderSpan{pic: pic, picW: w, picH: h})
			}
			return true
		case "file":
			idx := c.controlBlock(cs)
			c.blocks[idx].spans = append(c.blocks[idx].spans, renderSpan{text: "Choose File", style: style})
			return true
		case "submit", "button", "reset":
			v := attrOf(el, "value")
			if v == "" {
				v = strings.ToUpper(typ)
			}
			idx := c.controlBlock(cs)
			c.blocks[idx].spans = append(c.blocks[idx].spans, renderSpan{text: v, style: style})
			// A submit control is inline-block and shrinks to its label, like
			// a <button>. Left to fill the line, quotes.toscrape.com's
			// "Login" button was drawn the width of the whole form, where
			// Bootstrap's ".btn" is a rounded box around its text. A declared
			// width (".btn-block") still wins.
			if !cs.hasWidth && !c.flexItem(el) {
				c.blocks[idx].inlineBox = true
				c.blocks[idx].boxAlign = c.placeAlign(el)
			}
			return true
		default:
			// A text-like input shows its value, or its placeholder. An empty
			// one still draws its box.
			v := attrOf(el, "value")
			if v == "" {
				v = attrOf(el, "placeholder")
			}
			idx := c.controlBlock(cs)
			if v != "" {
				c.blocks[idx].spans = append(c.blocks[idx].spans, renderSpan{text: v, style: style})
			}
			c.controlPlaceholder(idx, style)
			return true
		}
	}
	return false
}

// controlPlaceholder gives an otherwise empty control block a zero-width span so
// it still lays out one line, and therefore draws its box. The painter skips
// the character itself: the embedded fonts have no U+200B and drew a
// missing-glyph box inside every empty field.
func (c *collector) controlPlaceholder(idx int, style renderStyle) {
	if len(c.blocks[idx].spans) == 0 {
		c.blocks[idx].spans = append(c.blocks[idx].spans, renderSpan{text: "\u200b", style: style})
	}
}

// widgetSize is the square size of a checkbox or radio: the CSS width when it
// is a pixel value, else the user-agent default.
func widgetSize(cs *computedStyle, def float64) float64 {
	if v := widgetWidth(cs, def); v > 0 {
		return v
	}
	return def
}

func widgetWidth(cs *computedStyle, def float64) float64 {
	if cs.hasWidth && cs.widthPx > 0 {
		return cs.widthPx
	}
	return def
}

func widgetHeight(cs *computedStyle, def float64) float64 {
	if cs.hasHeight && cs.heightPx > 0 {
		return cs.heightPx
	}
	if cs.hasWidth && cs.widthPx > 0 {
		return cs.widthPx
	}
	return def
}

// selectedOptionText returns the label a <select> shows: the selected option,
// or the first one.
func selectedOptionText(sel *html.Node) string {
	first := ""
	for ch := sel.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type != html.ElementNode || !strings.EqualFold(ch.Data, "option") {
			continue
		}
		label := strings.TrimSpace(textContent(ch))
		if hasAttr(ch, "selected") {
			return label
		}
		if first == "" {
			first = label
		}
	}
	return first
}
