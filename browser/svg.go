package browser

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strconv"
	"strings"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	"golang.org/x/net/html"
)

// Inline SVG is rasterized with a pure-Go renderer (oksvg + rasterx) so icons
// draw instead of being skipped. It is scaled to the CSS size at collection
// time and cached per element and size.
//
// The subset oksvg understands covers the common icon: paths, rects, circles,
// groups, transforms and fills. Anything it cannot parse is ignored, so a
// complex illustration may render partially rather than not at all.

// svgScale renders at this multiple and lets the draw pass scale down, which
// keeps small icons crisp at scale 2 without knowing the output scale here.
const svgScale = 2.0

func (p *Page) rasterSVG(n *html.Node, w, h float64, textColor color.RGBA) *pageImage {
	if w < 1 || h < 1 {
		return nil
	}
	key := svgCacheKey(n, w, h, textColor)
	if p.svgImages == nil {
		p.svgImages = map[string]*pageImage{}
	}
	if img, ok := p.svgImages[key]; ok {
		return img
	}
	var sb strings.Builder
	if err := html.Render(&sb, n); err != nil {
		return nil
	}
	icon, err := oksvg.ReadReplacingCurrentColor(strings.NewReader(sb.String()), cssHexString(textColor), oksvg.IgnoreErrorMode)
	if err != nil {
		p.debugf("svg: parse failed: %v", err)
		return nil
	}
	rw := int(w*svgScale + 0.5)
	rh := int(h*svgScale + 0.5)
	if rw < 1 || rh < 1 {
		return nil
	}
	// Target the supersampled raster: the draw pass scales it back down to the
	// CSS size, so the icon fills its box.
	icon.SetTarget(0, 0, float64(rw), float64(rh))
	dst := image.NewRGBA(image.Rect(0, 0, rw, rh))
	scanner := rasterx.NewScannerGV(rw, rh, dst, dst.Bounds())
	icon.Draw(rasterx.NewDasher(rw, rh, scanner), 1.0)

	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil
	}
	img := &pageImage{src: "inline-svg", data: buf.Bytes(), size: image.Point{X: rw, Y: rh}}
	p.svgImages[key] = img
	return img
}

func svgCacheKey(n *html.Node, w, h float64, c color.RGBA) string {
	return fmt.Sprintf("%p|%.1fx%.1f|%s", n, w, h, cssHexString(c))
}

// cssHexString formats a colour the way an SVG fill expects.
func cssHexString(c color.RGBA) string {
	return "#" + hex2(c.R) + hex2(c.G) + hex2(c.B)
}

func hex2(b uint8) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[b>>4], digits[b&0xf]})
}

// inlineImageSize returns the drawn size of an <img>: its natural size, scaled
// to a CSS width or height when the page sets one.
func inlineImageSize(cs *computedStyle, pic *pageImage) (float64, float64) {
	nw, nh := float64(pic.size.X), float64(pic.size.Y)
	if nw <= 0 || nh <= 0 {
		return 0, 0
	}
	w, h := nw, nh
	if cs.hasWidth && cs.widthPx > 0 {
		w = cs.widthPx
		h = nh * w / nw
	}
	if cs.hasHeight && cs.heightPx > 0 {
		hh := cs.heightPx
		if !cs.hasWidth || cs.widthPx <= 0 {
			w = nw * hh / nh
		}
		h = hh
	}
	return w, h
}

// svgSize resolves the CSS pixel size of an inline <svg>: the width/height
// attributes, the CSS width/height, or the viewBox aspect. A viewBox with no
// size is treated as the icon's design size.
func svgSize(el *html.Node, cs *computedStyle) (float64, float64) {
	vbW, vbH := svgViewBox(el)
	var w, h float64
	if cs.hasWidth && cs.widthPx > 0 {
		w = cs.widthPx
	}
	if cs.hasHeight && cs.heightPx > 0 {
		h = cs.heightPx
	}
	if w == 0 {
		w, _ = svgAttrLength(el, "width")
	}
	if h == 0 {
		h, _ = svgAttrLength(el, "height")
	}
	switch {
	case w > 0 && h > 0:
	case w > 0 && vbW > 0 && vbH > 0:
		h = w * vbH / vbW
	case h > 0 && vbW > 0 && vbH > 0:
		w = h * vbW / vbH
	case vbW > 0 && vbH > 0:
		w, h = vbW, vbH
		if w > 64 || h > 64 {
			scale := 24 / math.Max(w, h)
			w, h = w*scale, h*scale
		}
	default:
		w, h = 300, 150
	}
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	return w, h
}

// svgAttrLength parses a width/height attribute: a bare number is CSS px, a px
// suffix is accepted, and a percentage is not resolved here.
func svgAttrLength(el *html.Node, name string) (float64, bool) {
	v, ok := getAttr(el, name)
	if !ok {
		return 0, false
	}
	v = strings.TrimSpace(strings.ToLower(v))
	if strings.HasSuffix(v, "%") {
		return 0, false
	}
	v = strings.TrimSuffix(v, "px")
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || f <= 0 {
		return 0, false
	}
	return f, true
}

// svgViewBox returns the viewBox width and height, or zeros.
func svgViewBox(el *html.Node) (float64, float64) {
	v, ok := getAttr(el, "viewBox")
	if !ok {
		return 0, 0
	}
	fields := strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == ',' })
	if len(fields) != 4 {
		return 0, 0
	}
	w, err1 := strconv.ParseFloat(fields[2], 64)
	h, err2 := strconv.ParseFloat(fields[3], 64)
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0
	}
	return w, h
}
