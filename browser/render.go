package browser

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// The screenshot renderer is a document renderer, not a web renderer: it flows
// text at a given width and draws it to a PNG, mirroring what Lightpanda's
// screenshot does. There is no CSS box model, no images and no backgrounds
// beyond flat block colours.
//
// Nothing here runs unless Page.Screenshot is called: font parsing is behind a
// sync.Once and faces are built lazily, so the normal load path is unaffected.

// webFont is a parsed page font (from @font-face), used in place of the
// embedded Go fonts when a run's family matches one.
type webFont = opentype.Font

// renderStyle is the subset of styling the renderer understands.
type renderStyle struct {
	size      float64
	bold      bool
	italic    bool
	mono      bool
	link      bool
	underline bool
	strike    bool
	color     color.RGBA
	// font is the page's webfont for this run, or nil to use the embedded Go
	// font. It already carries the weight and slope the family asked for.
	font *webFont
	// textTransform is uppercase/lowercase/capitalize, "" for none. It is
	// applied to the text when the run is collected.
	textTransform string
	// letterSpacing is extra space after each character, in px.
	letterSpacing float64
}

var (
	// renderTextColor is the default text colour, which browsers define as
	// black in their user-agent stylesheet.
	renderTextColor = color.RGBA{R: 0, G: 0, B: 0, A: 0xff}
	renderLinkColor = color.RGBA{R: 0x0b, G: 0x57, B: 0xd0, A: 0xff}
	renderRuleColor = color.RGBA{R: 0xcc, G: 0xcc, B: 0xcc, A: 0xff}
	renderQuoteBar  = color.RGBA{R: 0xd0, G: 0xd0, B: 0xd0, A: 0xff}
	renderPageBG    = color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}

	// The user-agent colours of form controls: a text field is white on
	// black, while a button or select keeps the platform face.
	renderFieldBG    = color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	renderButtonFace = color.RGBA{R: 0xef, G: 0xef, B: 0xef, A: 0xff}
)

func defaultRenderStyle() renderStyle {
	return renderStyle{size: 16, color: renderTextColor}
}

// --- font handling (lazy) ---

type faceKey struct {
	size   float64
	bold   bool
	italic bool
	mono   bool
	font   *webFont
}

var (
	renderFontsOnce sync.Once
	renderRegular   *opentype.Font
	renderBold      *opentype.Font
	renderItalic    *opentype.Font
	renderMono      *opentype.Font

	renderFaceMu sync.Mutex
	renderFaces  = map[faceKey]font.Face{}
)

func loadRenderFonts() {
	renderFontsOnce.Do(func() {
		parse := func(ttf []byte) *opentype.Font {
			f, err := opentype.Parse(ttf)
			if err != nil {
				return nil
			}
			return f
		}
		renderRegular = parse(goregular.TTF)
		renderBold = parse(gobold.TTF)
		renderItalic = parse(goitalic.TTF)
		renderMono = parse(gomono.TTF)
	})
}

func renderFace(k faceKey) font.Face {
	loadRenderFonts()
	renderFaceMu.Lock()
	defer renderFaceMu.Unlock()
	if f, ok := renderFaces[k]; ok {
		return f
	}
	src := renderRegular
	switch {
	case k.font != nil:
		src = k.font
	case k.mono && renderMono != nil:
		src = renderMono
	case k.bold && renderBold != nil:
		src = renderBold
	case k.italic && renderItalic != nil:
		src = renderItalic
	}
	if src == nil {
		return nil
	}
	size := k.size
	if size < 4 {
		size = 4
	}
	f, err := opentype.NewFace(src, &opentype.FaceOptions{
		Size:    size,
		DPI:     72, // 1pt == 1px, so CSS px and face points line up
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil
	}
	renderFaces[k] = f
	return f
}

func measureText(k faceKey, s string) float64 {
	f := renderFace(k)
	if f == nil || s == "" {
		return 0
	}
	return float64(font.MeasureString(f, s)) / 64
}

// textWidth measures a styled run including letter-spacing, which a browser
// adds after every character. Every measurement in layout goes through it so
// the drawn text and the wrap width agree.
func textWidth(s renderStyle, text string) float64 {
	w := measureText(styleKey(s), text)
	if s.letterSpacing != 0 && text != "" {
		w += s.letterSpacing * float64(len([]rune(text)))
	}
	return w
}

// styleKey maps a text style to the font face that renders it.
func styleKey(s renderStyle) faceKey {
	return faceKey{size: s.size, bold: s.bold, italic: s.italic, mono: s.mono, font: s.font}
}

// lineMetrics returns ascent, descent and total height in px.
func lineMetrics(k faceKey) (ascent, descent, height float64) {
	f := renderFace(k)
	if f == nil {
		return k.size * 0.8, k.size * 0.2, k.size
	}
	m := f.Metrics()
	return float64(m.Ascent) / 64, float64(m.Descent) / 64, float64(m.Height) / 64
}

// --- draw list ---

type drawRun struct {
	x     float64
	text  string
	style renderStyle
	// An inline replaced box (an <img>, an inline <svg> or a form-control
	// widget) is drawn in place of text. imgW/imgH are the drawn size.
	img  *pageImage
	imgW float64
	imgH float64
}

type drawLine struct {
	pic  *pageImage // drawn into picX/y size picW x height
	picX float64
	picW float64

	y        float64 // top of the line box
	height   float64
	baseline float64
	runs     []drawRun
	marker   string
	markerX  float64
	rule     bool
	ruleX    float64
	ruleW    float64
	quote    int
	indent   float64

	bg        color.RGBA
	hasBG     bool
	bgX       float64
	bgW       float64
	underline bool // draw a rule under the whole line (blockquote bar reuses this shape)

	// A uniform box border drawn around a rectangle.
	border   bool
	borderX  float64
	borderY  float64
	borderW2 float64
	borderH  float64
	borderT  float64
	borderC  color.RGBA
}

// --- rasterizing ---

func renderPNG(doc *renderDoc, scale float64, pageBG color.RGBA) ([]byte, error) {
	if scale <= 0 {
		scale = 1
	}
	w := int(float64(doc.width) * scale)
	h := int(float64(doc.height) * scale)
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(pageBG), image.Point{}, draw.Src)

	s := func(v float64) float64 { return v * scale }

	// Element boxes (backgrounds and borders) first, so their content draws
	// over them.
	for _, bx := range doc.boxes {
		bx.draw(img, scale)
	}

	for _, ln := range doc.lines {
		if ln.pic != nil {
			drawScaledImage(img, ln.pic, s(ln.picX), s(ln.y), s(ln.picW), s(ln.height), scale)
			continue
		}
		if ln.hasBG {
			fillRect(img, s(ln.bgX), s(ln.y), s(ln.bgW), s(ln.height), ln.bg)
		}
		if ln.border {
			drawBorder(img, s(ln.borderX), s(ln.borderY), s(ln.borderW2), s(ln.borderH), s(ln.borderT), ln.borderC)
		}
	}
	for _, ln := range doc.lines {
		if ln.rule {
			fillRect(img, s(ln.ruleX), s(ln.y), s(ln.ruleW), 1*scale, renderRuleColor)
			continue
		}
		if ln.quote > 0 {
			fillRect(img, s(ln.indent-10), s(ln.y), 3*scale, s(ln.height), renderQuoteBar)
		}
		if ln.marker != "" {
			drawString(img, s(ln.markerX), s(ln.baseline), ln.marker, faceKey{size: doc.baseSize}, renderTextColor)
		}
		for _, r := range ln.runs {
			if r.img != nil {
				h := s(r.imgH)
				drawScaledImage(img, r.img, s(r.x), s(ln.baseline)-h, s(r.imgW), h, scale)
				continue
			}
			drawRunText(img, s(r.x), s(ln.baseline), r.text, r.style, s(r.style.letterSpacing))
			wpx := textWidth(r.style, r.text)
			if r.style.underline {
				fillRect(img, s(r.x), s(ln.baseline)+1.5*scale, s(wpx), 1*scale, r.style.color)
			}
			if r.style.strike {
				fillRect(img, s(r.x), s(ln.baseline)-s(r.style.size)*0.3, s(wpx), 1*scale, r.style.color)
			}
		}
	}

	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// drawScaledImage blits a picture into a box, scaling it to fit. A browser
// stretches an <img> to its box unless object-fit says otherwise, and the box
// here was sized from the picture's own aspect ratio, so a plain scale is
// right.
func drawScaledImage(dst *image.RGBA, pic *pageImage, x, y, w, h, scale float64) {
	if pic == nil || w < 1 || h < 1 {
		return
	}
	decoded, _, err := image.Decode(bytes.NewReader(pic.data))
	if err != nil {
		return
	}
	box := image.Rect(int(x+0.5), int(y+0.5), int(x+w+0.5), int(y+h+0.5)).Intersect(dst.Bounds())
	if box.Empty() {
		return
	}
	xdraw.CatmullRom.Scale(dst, box, decoded, decoded.Bounds(), draw.Src, nil)
}

// cssUniform converts a CSS colour (stored non-premultiplied, as rgba() is
// written) into an image source. color.RGBA fields are premultiplied by
// definition, so passing them straight to the draw package would treat an
// rgba(255,255,255,0.08) surface as opaque white; color.NRGBA carries the
// non-premultiplied intent.
func cssUniform(c color.RGBA) image.Image {
	return image.NewUniform(color.NRGBA{R: c.R, G: c.G, B: c.B, A: c.A})
}

func fillRect(img *image.RGBA, x, y, w, h float64, c color.RGBA) {
	x0, y0 := int(x+0.5), int(y+0.5)
	x1, y1 := int(x+w+0.5), int(y+h+0.5)
	if x1 <= x0 {
		x1 = x0 + 1
	}
	if y1 <= y0 {
		y1 = y0 + 1
	}
	r := image.Rect(x0, y0, x1, y1).Intersect(img.Bounds())
	if r.Empty() {
		return
	}
	// Over, not Src: a translucent background must blend with what is behind
	// it (the page background), not erase it. The output then stays opaque,
	// which is what a screenshot should be.
	draw.Draw(img, r, cssUniform(c), image.Point{}, draw.Over)
}

// drawBorder draws a uniform box border of thickness t inside the rectangle.
func drawBorder(img *image.RGBA, x, y, w, h, t float64, c color.RGBA) {
	if t < 1 {
		t = 1
	}
	if w < 1 || h < 1 {
		return
	}
	fillRect(img, x, y, w, t, c)
	fillRect(img, x, y+h-t, w, t, c)
	fillRect(img, x, y, t, h, c)
	fillRect(img, x+w-t, y, t, h, c)
}

// drawBox is an element's background and/or border, drawn once as a rectangle
// (rounded when it has a radius) instead of once per line. Boxes are collected
// during layout and painted parent-first.
type drawBox struct {
	x, y, w, h               float64
	bg                       color.RGBA
	hasBG                    bool
	borderW                  float64
	borderC                  color.RGBA
	hasBorder                bool
	radius                   [4]float64 // resolved corner radii, in px
	shadowX, shadowY         float64
	shadowBlur, shadowSpread float64
	shadowC                  color.RGBA
	hasShadow                bool
}

func (b drawBox) draw(img *image.RGBA, scale float64) {
	x, y, w, h := b.x*scale, b.y*scale, b.w*scale, b.h*scale
	if w < 1 || h < 1 {
		return
	}
	r := [4]float64{b.radius[0] * scale, b.radius[1] * scale, b.radius[2] * scale, b.radius[3] * scale}
	if b.hasShadow {
		drawShadow(img, b.x*scale+b.shadowX*scale, b.y*scale+b.shadowY*scale, w, h, r, b.shadowSpread*scale, b.shadowBlur*scale, b.shadowC)
	}
	if b.hasBG {
		if rounded(r) {
			roundRectMaskDraw(img, x, y, w, h, r, 0, b.bg)
		} else {
			fillRect(img, x, y, w, h, b.bg)
		}
	}
	if b.hasBorder && b.borderW > 0 {
		t := b.borderW * scale
		if t < 1 {
			t = 1
		}
		if rounded(r) {
			roundRectMaskDraw(img, x, y, w, h, r, t, b.borderC)
		} else {
			drawBorder(img, x, y, w, h, t, b.borderC)
		}
	}
}

// drawShadow paints a soft rectangle behind a box: a solid expanded rectangle
// when the blur is zero, otherwise a few expanding layers with falling alpha,
// which reads as a glow at these sizes.
func drawShadow(img *image.RGBA, x, y, w, h float64, r [4]float64, spread, blur float64, c color.RGBA) {
	if c.A == 0 || w < 1 || h < 1 {
		return
	}
	layers := 1
	if blur > 0 {
		layers = 4
	}
	for i := 0; i < layers; i++ {
		grow := spread
		alpha := float64(c.A)
		if blur > 0 {
			grow = spread + blur*float64(i+1)/float64(layers)
			alpha = float64(c.A) * (1 - float64(i)/float64(layers)) * 0.4
		}
		cc := c
		cc.A = uint8(alpha + 0.5)
		if cc.A == 0 {
			continue
		}
		rr := r
		for j := range rr {
			rr[j] += grow
		}
		roundRectMaskDraw(img, x-grow, y-grow, w+2*grow, h+2*grow, rr, 0, cc)
	}
}

func rounded(r [4]float64) bool {
	return r[0] > 0 || r[1] > 0 || r[2] > 0 || r[3] > 0
}

// roundRectMaskDraw draws a rounded rectangle, or with inset>0 the border ring
// between the rectangle and an inset copy of it.
func roundRectMaskDraw(img *image.RGBA, x, y, w, h float64, r [4]float64, inset float64, c color.RGBA) {
	intW, intH := int(w+0.5), int(h+0.5)
	if intW < 1 || intH < 1 {
		return
	}
	if inset > 0 && (w-2*inset < 1 || h-2*inset < 1) {
		fillRect(img, x, y, w, h, c)
		return
	}
	ir := insetRadius(r, inset)
	x0, y0 := int(x), int(y)
	mask := image.NewAlpha(image.Rect(0, 0, intW, intH))
	for j := 0; j < intH; j++ {
		for i := 0; i < intW; i++ {
			px, py := float64(x0+i), float64(y0+j)
			cov := roundCoverage(px, py, x, y, w, h, r)
			if inset > 0 {
				inner := roundCoverage(px, py, x+inset, y+inset, w-2*inset, h-2*inset, ir)
				cov -= inner
			}
			if cov <= 0 {
				continue
			}
			if cov > 1 {
				cov = 1
			}
			mask.SetAlpha(i, j, color.Alpha{A: uint8(cov*255 + 0.5)})
		}
	}
	dst := image.Rect(x0, y0, x0+intW, y0+intH).Intersect(img.Bounds())
	if dst.Empty() {
		return
	}
	draw.DrawMask(img, dst, cssUniform(c), image.Point{}, mask, dst.Min.Sub(image.Point{X: x0, Y: y0}), draw.Over)
}

func insetRadius(r [4]float64, inset float64) [4]float64 {
	out := r
	for i := range out {
		if out[i] -= inset; out[i] < 0 {
			out[i] = 0
		}
	}
	return out
}

// roundCoverage is the anti-aliased coverage of the rounded rectangle at a
// pixel, by 2x2 supersampling.
func roundCoverage(px, py, x, y, w, h float64, r [4]float64) float64 {
	n := 0
	for _, dy := range []float64{0.25, 0.75} {
		for _, dx := range []float64{0.25, 0.75} {
			if pointInRoundRect(px+dx, py+dy, x, y, w, h, r) {
				n++
			}
		}
	}
	return float64(n) / 4
}

func pointInRoundRect(px, py, x, y, w, h float64, r [4]float64) bool {
	if px < x || px > x+w || py < y || py > y+h {
		return false
	}
	switch {
	case px < x+r[0] && py < y+r[0]:
		return sq(px-(x+r[0]))+sq(py-(y+r[0])) <= sq(r[0])
	case px > x+w-r[1] && py < y+r[1]:
		return sq(px-(x+w-r[1]))+sq(py-(y+r[1])) <= sq(r[1])
	case px > x+w-r[2] && py > y+h-r[2]:
		return sq(px-(x+w-r[2]))+sq(py-(y+h-r[2])) <= sq(r[2])
	case px < x+r[3] && py > y+h-r[3]:
		return sq(px-(x+r[3]))+sq(py-(y+h-r[3])) <= sq(r[3])
	}
	return true
}

func sq(f float64) float64 { return f * f }

func drawString(img *image.RGBA, x, baseline float64, text string, k faceKey, c color.RGBA) {
	f := renderFace(k)
	if f == nil || text == "" {
		return
	}
	d := font.Drawer{
		Dst:  img,
		Src:  cssUniform(c),
		Face: f,
		Dot:  fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(baseline * 64)},
	}
	d.DrawString(text)
}

// drawRunText draws a run, adding letter-spacing after every character when the
// style asks for it. spacing is already scaled to device pixels.
func drawRunText(img *image.RGBA, x, baseline float64, text string, style renderStyle, spacing float64) {
	if spacing == 0 {
		drawString(img, x, baseline, text, styleKey(style), style.color)
		return
	}
	f := renderFace(styleKey(style))
	if f == nil || text == "" {
		return
	}
	d := font.Drawer{
		Dst:  img,
		Src:  cssUniform(style.color),
		Face: f,
		Dot:  fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(baseline * 64)},
	}
	for _, r := range text {
		d.DrawString(string(r))
		d.Dot.X += fixed.Int26_6(spacing * 64)
	}
}

// --- layout ---

// renderDoc is the result of laying out collected blocks.
type renderDoc struct {
	width    int
	height   int
	baseSize float64
	lines    []drawLine
	boxes    []drawBox
}

type renderBlockKind int

const (
	blockText renderBlockKind = iota
	blockRule
	blockImage
	blockFlex
)

type renderSpan struct {
	text  string
	style renderStyle
	// A replaced box: when pic is set this span is an inline image of size
	// picW x picH instead of text.
	pic  *pageImage
	picW float64
	picH float64
}

type renderBlock struct {
	kind     renderBlockKind
	spans    []renderSpan
	boxLeft  float64 // left edge of the block box
	textX    float64 // where text starts (boxLeft + padding)
	marker   string
	quote    int
	leading  float64 // space above the block
	trailing float64 // space below the block
	// The margin and padding parts of leading/trailing. Adjacent vertical
	// margins collapse (max, not sum) the way a browser lays them out; padding
	// never collapses.
	marginTop     float64
	marginBottom  float64
	paddingTop    float64
	paddingBottom float64
	pre           bool
	nowrap        bool
	align         string
	lineH         float64 // explicit line-height in px, 0 = auto

	bg     color.RGBA
	hasBG  bool
	bgFull bool // background spans the full content width

	// An image block draws a picture instead of text, at its aspect ratio.
	pic  *pageImage
	picW float64 // natural width, 0 when unknown
	picH float64 // natural height

	// A flex row lays its children side by side. Each child is a column of
	// blocks, with its own flex-grow and main-size basis. Basis is a fraction
	// of the container (basisPct) plus a fixed length (basisPx), which covers
	// "flex: 1" as well as "flex: 0 0 calc(50% - 7px)". Children are collected
	// with their internal offsets relative to their own left edge.
	flexRow    bool
	children   [][]renderBlock
	grow       []float64
	basisPx    []float64
	basisPct   []float64
	hasBasis   []bool
	gap        float64
	rowGap     float64
	wrap       bool
	justify    string
	alignItems string

	// A uniform border around the block, and a minimum height. Both are used
	// for box-like blocks such as form controls and panels; borderLeft is the
	// outer left edge relative to the column.
	borderW     float64
	borderColor color.RGBA
	hasBorder   bool
	borderLeft  float64
	minHeight   float64
	// boxID groups the blocks of one element so its background and border are
	// drawn once as a (possibly rounded) rectangle. 0 means no box.
	boxID     int
	boxBG     color.RGBA
	boxHasBG  bool
	radiusPx  [4]float64
	radiusPct [4]float64
	// Block sizing: width, max-width and auto horizontal margins. When set, the
	// block's used width is min(available, width, max-width) and auto margins
	// center it, instead of always filling the column.
	hasSizing      bool
	sizeLeft       float64
	boxWidthPx     float64
	boxWidthPct    float64
	hasBoxWidth    bool
	boxMaxWidthPx  float64
	boxMaxWidthPct float64
	hasBoxMaxWidth bool
	boxAutoLeft    bool
	boxAutoRight   bool
	borderBox      bool
	// Right edges and a height cap (with clipping) for scroll boxes like a log.
	marginRight  float64
	paddingRight float64
	maxHeight    float64
	// box-shadow, drawn behind the box background.
	shadowX, shadowY         float64
	shadowBlur, shadowSpread float64
	shadowColor              color.RGBA
	hasShadow                bool
}

// layoutBlocks flows blocks into a single column of the given width.
func layoutBlocks(blocks []renderBlock, width int, baseSize float64) *renderDoc {
	const margin = 24.0
	doc := &renderDoc{width: width, baseSize: baseSize}
	// The right edge keeps the gutter the flat layout used, so a block at
	// offset x can use colW-x without recomputing the page margin.
	colW := float64(width) - margin
	if colW < 40 {
		colW = 40
	}
	var boxes []drawBox
	lines, endY := layoutColumn(blocks, 0, colW, margin, baseSize, &boxes)
	doc.lines = lines
	doc.boxes = boxes
	doc.height = int(endY + margin + 0.5)
	if doc.height < 1 {
		doc.height = 1
	}
	return doc
}

// layoutColumn flows a column of blocks starting at x=colX and wrapping text to
// colW, returning the draw lines (absolute positions) and the y after the last
// block. A block's own offsets (boxLeft, textX) are relative to the column.
func layoutColumn(blocks []renderBlock, colX, colW, startY, baseSize float64, boxes *[]drawBox) ([]drawLine, float64) {
	const lineFact = 1.45
	var out []drawLine
	type boxAcc struct {
		dx           drawBox
		top, bottom  float64
		has, started bool
		radiusPx     [4]float64
		radiusPct    [4]float64
	}
	accs := map[int]*boxAcc{}
	var boxOrder []int
	y := startY
	recordBox := func(b renderBlock, top, bottom, x, w float64) {
		if b.boxID == 0 {
			return
		}
		acc := accs[b.boxID]
		if acc == nil {
			acc = &boxAcc{
				dx: drawBox{
					x: x, w: w,
					bg: b.boxBG, hasBG: b.boxHasBG,
					borderW: b.borderW, borderC: b.borderColor, hasBorder: b.hasBorder,
					shadowX: b.shadowX, shadowY: b.shadowY,
					shadowBlur: b.shadowBlur, shadowSpread: b.shadowSpread,
					hasShadow: b.hasShadow, shadowC: b.shadowColor,
				},
				radiusPx: b.radiusPx, radiusPct: b.radiusPct,
			}
			accs[b.boxID] = acc
			boxOrder = append(boxOrder, b.boxID)
		}
		if !acc.started || top < acc.top {
			acc.top = top
		}
		if !acc.started || bottom > acc.bottom {
			acc.bottom = bottom
		}
		acc.started = true
	}
	var prevMarginBottom, prevPaddingBottom float64
	havePrev := false
	for _, b := range blocks {
		bTop := y
		// Vertical margins of adjacent blocks collapse to the larger one;
		// padding always adds.
		gap := b.marginTop + b.paddingTop
		if havePrev {
			gap = prevPaddingBottom + math.Max(prevMarginBottom, b.marginTop) + b.paddingTop
		}
		y += gap
		// Block sizing. bb is the border-box width available from the block's
		// left edge (after its left margin) to the containing block's right
		// edge (before its right margin). edge is the padding and border total.
		// The used content width is min(bb, width, max-width) with
		// box-sizing:border-box subtracting the edge, and auto horizontal
		// margins center whatever is left over.
		bb := colW - b.boxLeft - b.marginRight
		if bb < 0 {
			bb = 0
		}
		edge := (b.boxLeft - b.borderLeft) + b.paddingRight + b.borderW
		contentW := bb
		specW := func(px, pct float64) float64 {
			w := px + pct*bb
			if b.borderBox {
				w -= edge
			}
			if w < 0 {
				w = 0
			}
			return w
		}
		if b.hasBoxWidth {
			if w := specW(b.boxWidthPx, b.boxWidthPct); w < contentW {
				contentW = w
			}
		}
		if b.hasBoxMaxWidth {
			if w := specW(b.boxMaxWidthPx, b.boxMaxWidthPct); w < contentW {
				contentW = w
			}
		}
		extra := bb - (contentW + edge)
		if extra < 0 {
			extra = 0
		}
		shift := 0.0
		switch {
		case b.boxAutoLeft && b.boxAutoRight:
			shift = extra / 2
		case b.boxAutoLeft:
			shift = extra
		}
		inner := b.textX - b.boxLeft
		boxX := colX + b.borderLeft + shift
		boxWidthOuter := contentW + edge
		switch b.kind {
		case blockImage:
			w := contentW
			if w < 8 {
				w = 8
			}
			// Scale down to the column, but never up past the natural size: a
			// small icon should stay small.
			if b.picW > 0 && b.picH > 0 && b.picW < w {
				w = b.picW
			}
			h := 120.0
			if b.picW > 0 && b.picH > 0 {
				h = b.picH * w / b.picW
			}
			if h > maxImageHeight {
				h = maxImageHeight
				if b.picW > 0 && b.picH > 0 {
					w = b.picW * h / b.picH
				}
			}
			if h < 8 {
				h = 8
			}
			out = append(out, drawLine{y: y, height: h, pic: b.pic, picX: colX + b.boxLeft + shift, picW: w})
			y += h
		case blockRule:
			out = append(out, drawLine{
				y: y, height: 1, rule: true,
				ruleX: colX + b.boxLeft + shift, ruleW: contentW,
			})
			y += 12
		case blockFlex:
			ls, h := layoutFlex(b, colX+shift, contentW+b.boxLeft, y, baseSize, boxes)
			out = append(out, ls...)
			y += h
		default:
			blockTop := y
			lineStart := len(out)
			textStart := colX + b.textX + shift
			limit := contentW - inner - b.paddingRight
			if limit < 40 {
				limit = 40
			}
			var lines [][]renderSpan
			switch {
			case b.pre:
				for _, ln := range splitPreLines(b.spans) {
					lines = append(lines, []renderSpan{ln})
				}
			case b.nowrap:
				lines = [][]renderSpan{b.spans}
			default:
				lines = wrapSpans(b.spans, limit)
			}
			for i, line := range lines {
				style := firstStyle(line)
				k := styleKey(style)
				ascent, _, h := lineMetrics(k)
				if ih := lineImageHeight(line, limit); ih > h {
					h = ih
				}
				if ascent < h {
					ascent = h
				}
				lh := h * lineFact
				if b.lineH > 0 {
					lh = b.lineH
				}
				if lh < style.size {
					lh = style.size
				}
				dl := drawLine{
					y:        y,
					height:   lh,
					baseline: y + ascent + (lh-h)/2,
					quote:    b.quote,
					indent:   textStart,
				}
				lw := lineWidth(line)
				if b.hasBG {
					dl.bg = b.bg
					dl.hasBG = true
					if b.bgFull {
						dl.bgX = colX + b.boxLeft + shift
						dl.bgW = contentW + b.paddingRight
					} else {
						dl.bgX = textStart
						dl.bgW = lw
					}
					if dl.bgW < 0 {
						dl.bgW = 0
					}
				}
				runX := textStart
				if b.align == "center" || b.align == "right" {
					space := limit - lw
					if space > 0 {
						if b.align == "center" {
							runX += space / 2
						} else {
							runX += space
						}
					}
				}
				if i == 0 && b.marker != "" {
					dl.marker = b.marker
					dl.markerX = colX + b.boxLeft + shift
				}
				for _, sp := range line {
					if sp.pic != nil {
						w, hh := sp.picW, sp.picH
						if w > limit && w > 0 {
							hh = hh * limit / w
							w = limit
						}
						dl.runs = append(dl.runs, drawRun{x: runX, img: sp.pic, imgW: w, imgH: hh})
						runX += w
						continue
					}
					dl.runs = append(dl.runs, drawRun{x: runX, text: sp.text, style: sp.style})
					runX += textWidth(sp.style, sp.text)
				}
				out = append(out, dl)
				y += lh
			}
			if len(lines) == 0 && b.marker != "" {
				k := faceKey{size: baseSize}
				_, _, h := lineMetrics(k)
				lh := h * lineFact
				out = append(out, drawLine{
					y: y, height: lh, baseline: y + h*0.8,
					marker: b.marker, markerX: colX + b.boxLeft + shift, quote: b.quote, indent: textStart,
				})
				y += lh
			}
			// A minimum height reserves space for a control or panel that has
			// no text yet, such as an empty textarea.
			if b.minHeight > 0 && y-blockTop < b.minHeight {
				gapH := b.minHeight - (y - blockTop)
				if b.hasBG {
					out = append(out, drawLine{
						y: y, height: gapH, hasBG: true, bg: b.bg,
						bgX: colX + b.boxLeft + shift, bgW: contentW + b.paddingRight,
					})
				}
				y += gapH
			}
			// max-height clips the box, like overflow on a scroll container.
			if b.maxHeight > 0 && y-blockTop > b.maxHeight {
				bottom := blockTop + b.maxHeight
				kept := out[:lineStart]
				for _, dl := range out[lineStart:] {
					if dl.y >= bottom {
						continue
					}
					if dl.y+dl.height > bottom {
						dl.height = bottom - dl.y
					}
					kept = append(kept, dl)
				}
				out = kept
				y = bottom
			}
		}
		recordBox(b, bTop, y, boxX, boxWidthOuter)
		prevMarginBottom, prevPaddingBottom = b.marginBottom, b.paddingBottom
		havePrev = true
	}
	if havePrev {
		y += prevPaddingBottom + prevMarginBottom
	}
	// Emit the boxes gathered in this column, parent (first seen) first.
	for _, id := range boxOrder {
		acc := accs[id]
		if !acc.started {
			continue
		}
		dx := acc.dx
		dx.y = acc.top
		dx.h = acc.bottom - acc.top
		if dx.h < 0 {
			dx.h = 0
		}
		minDim := math.Min(dx.w, dx.h)
		maxR := math.Min(dx.w/2, dx.h/2)
		for i := 0; i < 4; i++ {
			r := acc.radiusPx[i] + acc.radiusPct[i]*minDim
			if r > maxR {
				r = maxR
			}
			dx.radius[i] = r
		}
		*boxes = append(*boxes, dx)
	}
	return out, y
}

// layoutFlex lays a flex row out inside the column, returning its lines and
// height. Only the main size is distributed: children keep their natural height
// and align-items positions them vertically.
func layoutFlex(b renderBlock, colX, colW, y, baseSize float64, boxes *[]drawBox) ([]drawLine, float64) {
	n := len(b.children)
	if n == 0 {
		return nil, 0
	}
	contentX := colX + b.boxLeft
	avail := colW - b.boxLeft
	if avail < 10 {
		avail = 10
	}
	base := make([]float64, n)
	for i, col := range b.children {
		if b.hasBasis[i] {
			base[i] = b.basisPct[i]*avail + b.basisPx[i]
		} else {
			base[i] = intrinsicColumnWidth(col, avail, baseSize)
		}
		if base[i] < 0 {
			base[i] = 0
		}
	}

	runs := flexRuns(base, b.gap, avail, b.wrap)

	var (
		lines []drawLine
		total float64
	)
	rowGap := b.rowGap
	if rowGap == 0 {
		rowGap = b.gap
	}
	for ri, run := range runs {
		if ri > 0 {
			total += rowGap
		}
		ls, h := layoutFlexRun(b, run, base, contentX, avail, y+total, baseSize, boxes)
		lines = append(lines, ls...)
		total += h
	}
	// The container's own background spans the whole row.
	if b.hasBG {
		lines = append([]drawLine{{
			y: y, height: total, hasBG: true, bg: b.bg, bgX: contentX, bgW: avail,
		}}, lines...)
	}
	return lines, total
}

// flexRuns partitions item indexes into rows. Without wrap everything is one
// run; with wrap, items are added until the run's base widths plus gaps exceed
// the container.
func flexRuns(base []float64, gap, avail float64, wrap bool) [][]int {
	all := make([]int, len(base))
	for i := range all {
		all[i] = i
	}
	if !wrap {
		return [][]int{all}
	}
	var runs [][]int
	var cur []int
	curW := 0.0
	for i, w := range base {
		add := w
		if len(cur) > 0 {
			add += gap
		}
		if len(cur) > 0 && curW+add > avail {
			runs = append(runs, cur)
			cur = nil
			curW = 0
			add = w
		}
		cur = append(cur, i)
		curW += add
	}
	if len(cur) > 0 {
		runs = append(runs, cur)
	}
	return runs
}

// layoutFlexRun distributes one row's widths and lays out its children.
func layoutFlexRun(b renderBlock, idx []int, base []float64, contentX, avail, y, baseSize float64, boxes *[]drawBox) ([]drawLine, float64) {
	n := len(idx)
	gap := b.gap
	widths := make([]float64, n)
	sum := 0.0
	for i, ci := range idx {
		widths[i] = base[ci]
		sum += widths[i]
	}
	sumGaps := gap * float64(n-1)
	if sum+sumGaps > avail {
		// Shrink to fit the row.
		room := avail - sumGaps
		if room < 0 {
			room = 0
		}
		if sum > 0 {
			scale := room / sum
			for i := range widths {
				widths[i] *= scale
			}
		}
	} else if room := avail - sum - sumGaps; room > 0 {
		tg := 0.0
		for _, ci := range idx {
			tg += b.grow[ci]
		}
		if tg > 0 {
			for i, ci := range idx {
				widths[i] += room * b.grow[ci] / tg
			}
		}
	}

	// justify-content positions the row's content, adding extra gap for the
	// space-* values.
	used := sumGaps
	for _, w := range widths {
		used += w
	}
	offset := 0.0
	if extra := avail - used; extra > 0 {
		switch b.justify {
		case "center":
			offset = extra / 2
		case "end":
			offset = extra
		case "space-between":
			if n > 1 {
				gap += extra / float64(n-1)
			}
		case "space-around":
			offset = extra / float64(n) / 2
			gap += extra / float64(n)
		case "space-evenly":
			offset = extra / float64(n+1)
			gap += extra / float64(n+1)
		}
	}

	x := contentX + offset
	var lines []drawLine
	childLines := make([][]drawLine, n)
	childH := make([]float64, n)
	maxH := 0.0
	for i, ci := range idx {
		cl, endY := layoutColumn(b.children[ci], x, widths[i], y, baseSize, boxes)
		childLines[i] = cl
		childH[i] = endY - y
		if childH[i] > maxH {
			maxH = childH[i]
		}
		x += widths[i] + gap
	}
	for i := range childLines {
		dy := 0.0
		switch b.alignItems {
		case "center":
			dy = (maxH - childH[i]) / 2
		case "end":
			dy = maxH - childH[i]
		}
		if dy != 0 {
			for j := range childLines[i] {
				childLines[i][j].y += dy
			}
		}
		lines = append(lines, childLines[i]...)
	}
	return lines, maxH
}

// intrinsicColumnWidth measures a column's max-content width: the widest joined
// text, image or nested row, capped at limit. It is the flex base size of an
// item with no explicit basis.
func intrinsicColumnWidth(col []renderBlock, limit, baseSize float64) float64 {
	w := 0.0
	for _, b := range col {
		switch b.kind {
		case blockImage:
			if tw := b.boxLeft + b.picW; tw > w {
				w = tw
			}
		case blockFlex:
			sub := b.boxLeft
			for i, ch := range b.children {
				if i > 0 {
					sub += b.gap
				}
				sub += intrinsicColumnWidth(ch, limit, baseSize)
			}
			if sub > w {
				w = sub
			}
		default:
			// The block's own offset (margin + padding) is part of the item's
			// width, or the text wraps inside its own padding.
			if tw := b.boxLeft + spansIntrinsicWidth(b.spans); tw > w {
				w = tw
			}
		}
	}
	if w > limit {
		w = limit
	}
	return w
}

func firstStyle(spans []renderSpan) renderStyle {
	for _, sp := range spans {
		if sp.pic == nil {
			return sp.style
		}
	}
	return defaultRenderStyle()
}

// spansIntrinsicWidth is the max-content width of a run of spans: text widths
// plus the natural width of any replaced boxes.
func spansIntrinsicWidth(spans []renderSpan) float64 {
	w := 0.0
	for _, sp := range spans {
		if sp.pic != nil {
			w += sp.picW
		} else {
			w += textWidth(sp.style, sp.text)
		}
	}
	return w
}

// lineWidth is the drawn width of a line: text runs plus replaced boxes.
func lineWidth(line []renderSpan) float64 { return spansIntrinsicWidth(line) }

// lineImageHeight is the height of the tallest replaced box on a line, 0 when
// there is none. A box wider than the column is scaled down, so its height is
// scaled the same way.
func lineImageHeight(line []renderSpan, limit float64) float64 {
	h := 0.0
	for _, sp := range line {
		if sp.pic == nil {
			continue
		}
		hh := sp.picH
		if sp.picW > limit && sp.picW > 0 {
			hh = hh * limit / sp.picW
		}
		if hh > h {
			h = hh
		}
	}
	return h
}

func joinSpanText(spans []renderSpan) string {
	var b strings.Builder
	for _, s := range spans {
		if s.pic == nil {
			b.WriteString(s.text)
		}
	}
	return b.String()
}

// wrapSpans greedily fills lines, breaking between words. Every line is a
// sequence of spans, so an inline replaced box (an icon) can sit between text
// runs on the same line.
func wrapSpans(spans []renderSpan, limit float64) [][]renderSpan {
	type item struct {
		span renderSpan
		w    float64
	}
	var items []item
	spaceStyles := map[int]renderStyle{}
	for _, sp := range spans {
		if sp.pic != nil {
			items = append(items, item{span: sp, w: sp.picW})
			continue
		}
		fields := strings.Fields(sp.text)
		if len(fields) == 0 {
			if len(items) > 0 {
				spaceStyles[len(items)-1] = sp.style
			}
			continue
		}
		for i, w := range fields {
			if i > 0 {
				spaceStyles[len(items)-1] = sp.style
			}
			items = append(items, item{span: renderSpan{text: w, style: sp.style}, w: textWidth(sp.style, w)})
		}
	}
	if len(items) == 0 {
		return nil
	}

	var lines [][]renderSpan
	var cur []renderSpan
	curW := 0.0
	flush := func() {
		if len(cur) > 0 {
			lines = append(lines, mergeTextSpans(cur))
		}
		cur = nil
		curW = 0
	}
	for i, it := range items {
		spaceW := 0.0
		if i > 0 {
			if st, ok := spaceStyles[i-1]; ok {
				spaceW = textWidth(st, " ")
				if curW > 0 && curW+spaceW+it.w > limit {
					flush()
					spaceW = 0
				}
			}
		}
		if spaceW > 0 && curW > 0 {
			cur = append(cur, renderSpan{text: " ", style: spaceStyles[i-1]})
			curW += spaceW
		}
		if it.span.pic == nil && it.w > limit && curW == 0 {
			// A word wider than the column: hard-break it.
			var piece strings.Builder
			pieceW := 0.0
			for _, r := range it.span.text {
				rs := string(r)
				rw := textWidth(it.span.style, rs)
				if pieceW+rw > limit && piece.Len() > 0 {
					cur = append(cur, renderSpan{text: piece.String(), style: it.span.style})
					flush()
					piece.Reset()
					pieceW = 0
				}
				piece.WriteString(rs)
				pieceW += rw
			}
			cur = append(cur, renderSpan{text: piece.String(), style: it.span.style})
			curW = pieceW
			continue
		}
		cur = append(cur, it.span)
		curW += it.w
	}
	flush()
	return lines
}

// mergeTextSpans joins adjacent text spans of the same style, so a line of
// plain text stays a single run and keeps its shape.
func mergeTextSpans(line []renderSpan) []renderSpan {
	out := make([]renderSpan, 0, len(line))
	for _, sp := range line {
		if sp.pic == nil && len(out) > 0 {
			last := &out[len(out)-1]
			if last.pic == nil && last.style == sp.style {
				last.text += sp.text
				continue
			}
		}
		out = append(out, sp)
	}
	return out
}

// splitPreLines splits preformatted spans on newlines without re-wrapping.
func splitPreLines(spans []renderSpan) []renderSpan {
	var out []renderSpan
	var cur strings.Builder
	var style renderStyle
	has := false
	flush := func() {
		out = append(out, renderSpan{text: cur.String(), style: style})
		cur.Reset()
		has = false
	}
	for _, sp := range spans {
		parts := strings.Split(sp.text, "\n")
		for i, p := range parts {
			if i > 0 {
				flush()
			}
			if !has {
				style = sp.style
				has = true
			}
			cur.WriteString(p)
		}
	}
	if cur.Len() > 0 || !has {
		flush()
	}
	return out
}
