package browser

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"sort"
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

	bg    color.RGBA
	hasBG bool
	bgX   float64
	bgW   float64

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
	return image.NewUniform(color.NRGBA(c))
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
	// ref is the box's identity within one column layout, used to keep a
	// container's box in front of the boxes of the children it laid out.
	ref                      int
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
	// geom maps an element box id to the rectangle the layout gave it, present
	// only when the layout ran with geometry recording on.
	geom map[int]Rect
}

// geomSink accumulates one rectangle per element box while a column is laid
// out. It is threaded through the recursive layout calls so a flex, grid or
// table child contributes to the same rectangles as the flow.
type geomSink struct {
	accs  map[int]*geomAcc
	order []int
}

type geomAcc struct {
	x, top, right, bottom float64
	started               bool
}

func newGeomSink() *geomSink {
	return &geomSink{accs: map[int]*geomAcc{}}
}

func (g *geomSink) record(ids []int, x, top, right, bottom float64) {
	for _, id := range ids {
		acc := g.accs[id]
		if acc == nil {
			acc = &geomAcc{x: x, top: top, right: right, bottom: bottom, started: true}
			g.accs[id] = acc
			g.order = append(g.order, id)
			continue
		}
		if x < acc.x {
			acc.x = x
		}
		if top < acc.top {
			acc.top = top
		}
		if right > acc.right {
			acc.right = right
		}
		if bottom > acc.bottom {
			acc.bottom = bottom
		}
	}
}

// rects returns the accumulated rectangles keyed by element box id.
func (g *geomSink) rects() map[int]Rect {
	if g == nil {
		return nil
	}
	out := make(map[int]Rect, len(g.accs))
	for id, acc := range g.accs {
		out[id] = Rect{X: acc.x, Y: acc.top, Width: acc.right - acc.x, Height: acc.bottom - acc.top}
	}
	return out
}

type renderBlockKind int

const (
	blockText renderBlockKind = iota
	blockRule
	blockImage
	blockFlex
	blockFloat
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
	flexRow bool
	// A floated block leaves the flow. Its own content is held in
	// floatBlocks and measured at the float's own width, and the blocks that
	// follow it in the column flow beside it.
	floatSide   string
	floatW      float64
	floatPct    float64
	hasFloatW   bool
	floatBlocks []renderBlock
	// A grid container places its children in the tracks of grid-template
	// columns, left to right, filling a new row when the tracks run out.
	grid     bool
	gridTmpl gridTemplate
	// gridCols and gridSpans are each child's column placement in a grid:
	// the 0-based track it starts in (-1 when it is auto-placed) and how
	// many tracks it spans. gridRows and gridRowSpans are the same for the
	// explicit rows a grid-template area asks for.
	gridCols     []int
	gridSpans    []int
	gridRows     []int
	gridRowSpans []int
	// A table lays its rows out as cells in shared columns.
	table bool
	rows  []tableRow
	// tableStretch is set when the table declared a width, so the columns grow
	// to fill the containing block instead of shrinking to their content.
	tableStretch bool
	children     [][]renderBlock
	grow         []float64
	// shrink is each flex item's flex-shrink: how much of a row's overflow it
	// absorbs, so "flex: none" items keep their width.
	shrink     []float64
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
	// hasDeclaredHeight records that the element set its own height, which
	// stops a grid item from stretching to its row: a "height: 30px" item
	// stays 30px in a taller row.
	hasDeclaredHeight bool
	// boxHeight is an element's declared height, carried by the first block it
	// produced, and boxLast is how many blocks follow it in that element. The
	// layout clamps the cursor to the box's bottom once the element's blocks
	// are done, which is what lets content taller than the box overflow it.
	boxHeight float64
	boxLast   int
	// boxID groups the blocks of one element so its background and border are
	// drawn once as a (possibly rounded) rectangle. 0 means no box.
	boxID     int
	boxBG     color.RGBA
	boxHasBG  bool
	radiusPx  [4]float64
	radiusPct [4]float64
	// geomIDs are the element boxes this block belongs to, innermost first,
	// collected only when geometry is requested. Every enclosing element's id
	// is present, so an element's rectangle spans its descendants, which is
	// what getBoundingClientRect reports. Empty on the paint-only fast path.
	geomIDs []int
	// Block sizing: width, max-width and auto horizontal margins. When set, the
	// block's used width is min(available, width, max-width) and auto margins
	// center it, instead of always filling the column.
	hasSizing   bool
	sizeLeft    float64
	boxWidthPx  float64
	boxWidthPct float64
	hasBoxWidth bool
	// boxWidthIsOwn records that the declared width is the box's own fixed
	// size, rather than a percentage or a viewport length that resolves
	// against something else. Only that kind feeds intrinsic sizing, where
	// otherwise a "width: 100%" box (stored as the parent's px width) would
	// inflate any shrink-to-fit container it sits in.
	boxWidthIsOwn  bool
	boxMaxWidthPx  float64
	boxMaxWidthPct float64
	hasBoxMaxWidth bool
	boxAutoLeft    bool
	boxAutoRight   bool
	borderBox      bool
	// The block that carries the sizing may be a descendant of the element
	// that declared it: a plain container produces no block of its own, so its
	// width lands on the blocks inside it. These record the declaring
	// element's box so the width is measured from it and not from the block.
	hasSizeOwner    bool
	sizeBoxLeft     float64 // declaring element's border-box left
	sizePadLeft     float64 // its left padding plus border
	sizePadRight    float64 // its right padding plus border
	sizeMarginRight float64
	// Right edges and a height cap (with clipping) for scroll boxes like a log.
	marginRight  float64
	paddingRight float64
	maxHeight    float64
	// rightInset is how far the containing block's content edge sits inside the
	// column's right edge, from the padding, border and right margin of the
	// containers between them.
	rightInset    float64
	hasRightInset bool
	// hasRightEdges marks a block whose right margin and padding came from its
	// own element, so an ancestor without blocks of its own cannot overwrite
	// them.
	hasRightEdges bool
	// Out-of-flow children (position:absolute/fixed) placed relative to this
	// block's content box, and the offset from position:relative.
	abs   []absChild
	relDy float64
	// box-shadow, drawn behind the box background.
	shadowX, shadowY         float64
	shadowBlur, shadowSpread float64
	shadowColor              color.RGBA
	hasShadow                bool
}

// absChild is an absolutely or fixed positioned element: its content, the
// insets that place it, and its width when the page set one.
type absChild struct {
	blocks                               []renderBlock
	left, top, right, bottom             float64
	hasLeft, hasTop, hasRight, hasBottom bool
	hasWidth                             bool
	widthPx, widthPct                    float64
	z                                    int
	// translateX/translateY are the element's "transform: translate()"
	// offset. A percentage is a fraction of its own box, which is how an
	// off-screen drawer is parked with translateX(100%).
	translateX, translateY       float64
	translateXPct, translateYPct float64
	hasTranslate                 bool
}

// layoutBlocks flows blocks into a single column of the given width.
func layoutBlocks(blocks []renderBlock, width int, baseSize float64) *renderDoc {
	doc := &renderDoc{width: width, baseSize: baseSize}
	// The column is the viewport: an element's own margins (the body's 8px
	// default included) are what inset it, exactly as a browser does. An extra
	// gutter here would narrow every page's text and wrap it early.
	colW := float64(width)
	if colW < 40 {
		colW = 40
	}
	var boxes []drawBox
	g := newGeomSink()
	lines, endY := layoutColumn(blocks, 0, colW, 0, baseSize, &boxes, g)
	doc.lines = lines
	doc.boxes = boxes
	doc.geom = g.rects()
	doc.height = int(endY + 0.5)
	if doc.height < 1 {
		doc.height = 1
	}
	return doc
}

// layoutColumn flows a column of blocks starting at x=colX and wrapping text to
// colW, returning the draw lines (absolute positions) and the y after the last
// block. A block's own offsets (boxLeft, textX) are relative to the column.
func layoutColumn(blocks []renderBlock, colX, colW, startY, baseSize float64, boxes *[]drawBox, g *geomSink) ([]drawLine, float64) {
	var out []drawLine
	var absLines []drawLine
	var absBoxes []drawBox
	type boxAcc struct {
		dx          drawBox
		top, bottom float64
		started     bool
		radiusPx    [4]float64
		radiusPct   [4]float64
	}
	accs := map[int]*boxAcc{}
	var boxOrder []int
	// boxInserts records where a container's own box must end up: at the
	// position its children's boxes start in the output.
	var boxInserts []struct {
		ref int
		at  int
	}
	y := startY
	recordBox := func(b renderBlock, top, bottom, x, w float64) {
		if g != nil && len(b.geomIDs) > 0 {
			g.record(b.geomIDs, x, top, x+w, bottom)
		}
		if b.boxID == 0 {
			return
		}
		acc := accs[b.boxID]
		if acc == nil {
			acc = &boxAcc{
				dx: drawBox{
					ref: b.boxID,
					x:   x, w: w,
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
	var prevMarginBottom, prevPaddingBottom, prevBorderBottom float64
	havePrev := false
	// Floated boxes in this column: the blocks after one flow beside it until
	// its bottom passes, which is what wraps text around an image or an
	// infobox. Each inset is measured from the column's own content edges.
	type floatBox struct {
		side         string
		x, w, bottom float64
		margin       float64
	}
	var floats []floatBox
	floatBottom := func() float64 {
		bottom := 0.0
		for _, f := range floats {
			if f.bottom > bottom {
				bottom = f.bottom
			}
		}
		return bottom
	}
	// floatInsets returns how far the content edge is pulled in on each side
	// by the floats still open at y.
	floatInsets := func(at float64) (left, right float64) {
		for _, f := range floats {
			if at >= f.bottom {
				continue
			}
			if f.side == "left" {
				if e := f.x + f.w - colX; e > left {
					left = e
				}
			} else if e := colX + colW - f.x; e > right {
				right = e
			}
		}
		return left, right
	}
	// A declared height ends at the element's last block: the cursor is set
	// back to the box's bottom there, so content taller than the box overflows
	// instead of pushing the page down. Ranges nest, so the clamps are a stack.
	type boxClamp struct {
		end    int
		bottom float64
	}
	var clamps []boxClamp
	applyClamps := func(bi int) {
		for len(clamps) > 0 && clamps[0].end < bi {
			if y > clamps[0].bottom {
				y = clamps[0].bottom
			}
			clamps = clamps[1:]
		}
	}
	for bi, b := range blocks {
		applyClamps(bi)
		// Vertical margins of adjacent blocks collapse to the larger one;
		// padding and border always add.
		if b.boxHeight > 0 {
			// The box starts at the element's top margin edge, and a declared
			// height is its content height unless box-sizing says otherwise.
			bottom := y + b.marginTop + b.boxHeight
			if !b.borderBox {
				bottom += b.paddingTop + b.paddingBottom + 2*b.borderW
			}
			clamps = append(clamps, boxClamp{end: bi + b.boxLast, bottom: bottom})
		}
		gap := b.marginTop + b.borderW + b.paddingTop
		if havePrev {
			gap = prevPaddingBottom + prevBorderBottom +
				math.Max(prevMarginBottom, b.marginTop) + b.borderW + b.paddingTop
		}
		y += gap
		if b.relDy != 0 {
			y += b.relDy
		}
		contentTop := y
		// Block sizing. bb is the border-box width available from the block's
		// left edge (after its left margin) to the containing block's right
		// edge (before its right margin). edge is the padding and border total.
		// The used content width is min(bb, width, max-width) with
		// box-sizing:border-box subtracting the edge, and auto horizontal
		// margins center whatever is left over.
		var bb, edge, contentW, inner, boxX, boxWidthOuter, contentRight float64
		var extra, shift float64
		// The containing block's content right edge: the column's, less any
		// inset an ancestor container put on it.
		contRight := colW
		if b.hasRightInset {
			contRight -= b.rightInset
		}
		if b.hasSizeOwner {
			// A width context is active. Resolve the box of the element that
			// declared it: "max-width:1200px;margin:0 auto" centers that
			// element, and everything inside it follows the same shift and is
			// measured against its content edge.
			obb := contRight - b.sizeBoxLeft - b.sizeMarginRight
			if obb < 0 {
				obb = 0
			}
			oedge := b.sizePadLeft + b.sizePadRight
			outer := obb
			specOwner := func(px, pct float64) float64 {
				w := px + pct*obb
				if !b.borderBox {
					w += oedge
				}
				if w < 0 {
					w = 0
				}
				return w
			}
			if b.hasBoxWidth {
				if w := specOwner(b.boxWidthPx, b.boxWidthPct); w < outer {
					outer = w
				}
			}
			if b.hasBoxMaxWidth {
				if w := specOwner(b.boxMaxWidthPx, b.boxMaxWidthPct); w < outer {
					outer = w
				}
			}
			oContent := outer - oedge
			if oContent < 0 {
				oContent = 0
			}
			oExtra := obb - outer
			if oExtra < 0 {
				oExtra = 0
			}
			oShift := 0.0
			switch {
			case b.boxAutoLeft && b.boxAutoRight:
				oShift = oExtra / 2
			case b.boxAutoLeft:
				oShift = oExtra
			}
			// The declaring element's own block: the textX it carries is
			// already its content edge.
			own := b.boxLeft == b.sizeLeft
			switch {
			case own:
				bb, edge, contentW = obb, oedge, oContent
				shift = oShift
				inner = b.textX - b.sizeLeft
				boxX = colX + b.sizeBoxLeft + shift
				boxWidthOuter = outer
			default:
				// A block inside the declaring element: it is measured from
				// where it starts to the declaring element's content edge, and
				// inherits the declaring element's shift.
				bb = b.sizeLeft + oContent - b.boxLeft
				if bb < 0 {
					bb = 0
				}
				edge = (b.textX - b.boxLeft) + b.paddingRight + 2*b.borderW
				contentW = bb
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
				extra = bb - (contentW + edge)
				if extra < 0 {
					extra = 0
				}
				shift = oShift
				switch {
				case b.boxAutoLeft && b.boxAutoRight:
					shift += extra / 2
				case b.boxAutoLeft:
					shift += extra
				}
				inner = b.textX - b.boxLeft
				contentRight = b.paddingRight
				boxX = colX + b.borderLeft + shift
				boxWidthOuter = contentW + edge
			}
		} else {
			bb = contRight - b.boxLeft - b.marginRight
			if bb < 0 {
				bb = 0
			}
			edge = (b.textX - b.boxLeft) + b.paddingRight + 2*b.borderW
			contentW = bb
			// specified records that a declared width or max-width decided
			// the box, rather than the box filling the column.
			specified := false
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
					specified = true
				}
			}
			if b.hasBoxMaxWidth {
				if w := specW(b.boxMaxWidthPx, b.boxMaxWidthPct); w < contentW {
					contentW = w
					specified = true
				}
			}
			extra = bb - (contentW + edge)
			if extra < 0 {
				extra = 0
			}
			switch {
			case b.boxAutoLeft && b.boxAutoRight:
				shift = extra / 2
			case b.boxAutoLeft:
				shift = extra
			}
			inner = b.textX - b.boxLeft
			boxX = colX + b.borderLeft + shift
			// The border box: from the block's left edge to the content edge
			// plus the right padding and border.
			contentRight = b.paddingRight + b.borderW
			leftEdge := b.textX - b.boxLeft
			if b.boxID != 0 {
				leftEdge = b.boxLeft - b.borderLeft
			}
			boxWidthOuter = contentW - inner - contentRight + leftEdge + contentRight
			if specified {
				// A declared width is the whole border box, not a line
				// filling one, so the expression above does not apply.
				// Drawing only the content of five "width: 20%" flex
				// columns hid the gutter between them.
				boxWidthOuter = contentW + edge
			}
		}
		blockTop := y
		lineStart := len(out)
		childBoxStart := len(*boxes)
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
			if b.table {
				ls, h := layoutTable(b, colX+shift, contentW+b.boxLeft, y, baseSize, boxes, g)
				out = append(out, ls...)
				y += h
				continue
			}
			if b.grid {
				ls, h := layoutGrid(b, colX+shift, contentW+b.boxLeft, y, baseSize, boxes, g)
				out = append(out, ls...)
				y += h
				continue
			}
			ls, h := layoutFlex(b, colX+shift, contentW+b.boxLeft, y, baseSize, boxes, g)
			out = append(out, ls...)
			y += h
		case blockFloat:
			// A float is sized to its content unless it declares a width,
			// then placed against its side of the column. It does not move y:
			// the blocks after it flow beside it.
			fw := floatWidth(b, colW, baseSize)
			// A float goes as far to its side as it can without overlapping
			// the floats already open at that height: Wikipedia's footer is a
			// row of left floats ("Privacy policy", "About Wikipedia", ...),
			// and stacking them all at the same x hides all but the last.
			fy := y
			fx := colX
			if b.floatSide == "right" {
				fx = colX + colW - fw
			}
			// A float's margins are part of the space it takes up.
			fMargin := 0.0
			if len(b.floatBlocks) > 0 {
				fMargin = b.floatBlocks[0].marginRight
			}
			for _, f := range floats {
				if f.bottom <= fy || f.side != b.floatSide {
					continue
				}
				if b.floatSide == "right" {
					if f.x-fw-fMargin < fx {
						fx = f.x - fw - fMargin
					}
				} else if f.x+f.w+f.margin > fx {
					fx = f.x + f.w + f.margin
				}
			}
			if fx < colX || fx+fw > colX+colW {
				// No room beside them: this float drops below the ones in its
				// way, as the float placement rules say.
				fy = floatBottom()
				fx = colX
				if b.floatSide == "right" {
					fx = colX + colW - fw
				}
			}
			// The float's own layout reserves its margin, so it is measured
			// over the border box plus that margin.
			fl, fend := layoutColumn(b.floatBlocks, fx, fw+fMargin, fy, baseSize, boxes, g)
			out = append(out, fl...)
			fh := fend - fy
			if fh < 0 {
				fh = 0
			}
			floats = append(floats, floatBox{side: b.floatSide, x: fx, w: fw, bottom: fy + fh, margin: fMargin})
			continue
		default:
			textStart := colX + b.textX + shift
			limit := contentW - inner - contentRight
			if limit < 40 {
				limit = 40
			}
			// Text beside a float is narrower until the float's bottom
			// passes. The line height is estimated from the block's first
			// span, which is what the wrap uses to know where the float ends.
			estLH := 0.0
			if len(b.spans) > 0 {
				st := firstStyle(b.spans)
				_, _, estLH = lineMetrics(styleKey(st))
				if b.lineH > 0 {
					estLH = b.lineH
				}
				if estLH < st.size {
					estLH = st.size
				}
			}
			fBottom := floatBottom()
			fl, fr := floatInsets(y)
			narrowLimit := limit - fl - fr
			if narrowLimit < 40 {
				narrowLimit = 40
			}
			limitAt := func(k int) float64 {
				if len(floats) == 0 || estLH <= 0 {
					return limit
				}
				if y+float64(k)*estLH < fBottom {
					return narrowLimit
				}
				return limit
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
				lines = wrapSpansWidth(b.spans, limitAt)
			}
			for i, line := range lines {
				style := firstStyle(line)
				k := styleKey(style)
				ascent, _, h := lineMetrics(k)
				// This line's own available width, and the x its text starts
				// at, from the floats still open across it.
				li, ri := floatInsets(y)
				lineLimit := limit - li - ri
				if lineLimit < 40 {
					lineLimit = 40
				}
				lineStart := textStart + li
				if ih := lineImageHeight(line, lineLimit); ih > h {
					h = ih
				}
				if ascent < h {
					ascent = h
				}
				// "line-height: normal" is the font's own line height, which
				// is what the glyph raster's height already is.
				lh := h
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
					indent:   lineStart,
				}
				lw := lineWidth(line)
				if b.hasBG {
					dl.bg = b.bg
					dl.hasBG = true
					if b.bgFull {
						dl.bgX = colX + b.boxLeft + shift
						dl.bgW = contentW + b.paddingRight
					} else {
						dl.bgX = lineStart
						dl.bgW = lw
					}
					if dl.bgW < 0 {
						dl.bgW = 0
					}
				}
				runX := lineStart
				if b.align == "center" || b.align == "right" {
					space := lineLimit - lw
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
						if w > lineLimit && w > 0 {
							hh = hh * lineLimit / w
							w = lineLimit
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
				lh := h
				out = append(out, drawLine{
					y: y, height: lh, baseline: y + h*0.8,
					marker: b.marker, markerX: colX + b.boxLeft + shift, quote: b.quote, indent: textStart,
				})
				y += lh
			}
		}
		// A declared height reserves space below the content, and a max-height
		// clips it. Both apply to any block: a header is a flex row with a
		// height, and a control with no text yet is sized by its min-height.
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
		if len(b.abs) > 0 {
			originX := boxX + b.borderW + (b.textX - b.boxLeft)
			absLines = append(absLines, layoutAbsChildren(b.abs, originX, contentTop, contentW, y-contentTop, baseSize, &absBoxes, g)...)
		}
		// The box is the border box: from the top of the content box up over
		// the top padding and border, and down past the bottom ones.
		recordBox(b, contentTop-b.paddingTop-b.borderW,
			y+b.paddingBottom+b.borderW, boxX, boxWidthOuter)
		if b.boxID != 0 && (b.flexRow || b.grid || b.table) && len(*boxes) > childBoxStart {
			boxInserts = append(boxInserts, struct {
				ref int
				at  int
			}{ref: b.boxID, at: childBoxStart})
		}
		if b.relDy != 0 {
			y -= b.relDy
		}
		prevMarginBottom, prevPaddingBottom = b.marginBottom, b.paddingBottom
		prevBorderBottom = b.borderW
		havePrev = true
	}
	if havePrev {
		y += prevPaddingBottom + prevBorderBottom + prevMarginBottom
	}
	// A float that outlives the blocks beside it still takes up room: the
	// next section starts below it rather than over it.
	if fb := floatBottom(); fb > y {
		y = fb
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
	// A container that lays its children out in a box of its own (a flex row,
	// a grid, a table) collects their boxes during that call, so its own box
	// would be appended after them and paint over them. Put it back where its
	// children start, which is where a browser paints it: behind them.
	for _, ins := range boxInserts {
		at, found := ins.at, -1
		for i := at; i < len(*boxes); i++ {
			if (*boxes)[i].ref == ins.ref {
				found = i
				break
			}
		}
		if found < 0 {
			continue
		}
		dx := (*boxes)[found]
		copy((*boxes)[at+1:], (*boxes)[at:found])
		(*boxes)[at] = dx
	}
	// Out-of-flow content paints above the flow: its boxes after the flow's
	// boxes, its lines after the flow's lines.
	*boxes = append(*boxes, absBoxes...)
	out = append(out, absLines...)
	return out, y
}

// layoutAbsChildren places out-of-flow children relative to a containing block
// whose content origin is (ox, oy) and content size is cw x ch. A child with
// left/top is placed from the origin; right/bottom anchor to the far edge; with
// no inset it uses the origin, like a browser's static position for a box that
// has left its flow.
func layoutAbsChildren(children []absChild, ox, oy, cw, ch, baseSize float64, boxes *[]drawBox, g *geomSink) []drawLine {
	var out []drawLine
	for _, child := range children {
		w := intrinsicColumnWidth(child.blocks, cw, baseSize)
		if child.hasWidth {
			if spec := child.widthPx + child.widthPct*cw; spec > 0 {
				w = spec
			}
		}
		if w > cw {
			w = cw
		}
		x := ox
		switch {
		case child.hasLeft:
			x = ox + child.left
		case child.hasRight:
			x = ox + cw - w - child.right
		}
		y := oy
		switch {
		case child.hasTop:
			y = oy + child.top
		case child.hasBottom:
			y = oy + ch - child.bottom
		}
		// A translated element moves from wherever it would have been. The
		// vertical percentage is not applied: it needs the element's own
		// height, which is only known once it has been laid out.
		if child.hasTranslate {
			x += child.translateX + child.translateXPct*w
			y += child.translateY
		}
		lines, _ := layoutColumn(child.blocks, x, w, y, baseSize, boxes, g)
		out = append(out, lines...)
	}
	return out
}

// layoutFlex lays a flex row out inside the column, returning its lines and
// height. Only the main size is distributed: children keep their natural height
// and align-items positions them vertically.
func layoutFlex(b renderBlock, colX, colW, y, baseSize float64, boxes *[]drawBox, g *geomSink) ([]drawLine, float64) {
	n := len(b.children)
	if n == 0 {
		return nil, 0
	}
	contentX := colX + b.boxLeft
	avail := colW - b.boxLeft
	// A row that declares its own width is what its items resolve against,
	// even when that is wider than the containing block: go.dev's testimonial
	// carousel is a 10000px row of 1000px slides inside a 1000px wrapper that
	// clips it, and measuring the row at the wrapper's width squeezed ten
	// slides into a sixth of the space and wrapped their text.
	if b.hasBoxWidth || b.hasBoxMaxWidth {
		w := b.boxWidthPx + b.boxWidthPct*avail
		if b.hasBoxMaxWidth {
			mw := b.boxMaxWidthPx + b.boxMaxWidthPct*avail
			if !b.hasBoxWidth || mw < w {
				w = mw
			}
		}
		if b.borderBox {
			w -= b.sizePadLeft + b.sizePadRight
		}
		if w > 0 {
			avail = w
		}
	}
	if avail < 10 {
		avail = 10
	}
	base := make([]float64, n)
	for i, col := range b.children {
		if b.hasBasis[i] {
			base[i] = b.basisPct[i]*avail + b.basisPx[i]
		} else {
			// A flex item's base size is its max-content width, which the
			// container's width does not cap: an item wider than the row
			// overflows or shrinks, it does not re-wrap. Capping it at the
			// row's width made rust-lang.org's nav measure its own item list
			// at the row's width, so the list wrapped its last item onto a
			// second line.
			base[i] = intrinsicColumnWidth(col, flexBaseLimit, baseSize)
		}
		// A flex item takes up its outer size. Its margins count, and with
		// box-sizing: content-box so do its padding and border, which the
		// declared width does not include. With border-box the declared width
		// is already the border-box size, and adding the padding again is what
		// pushes one column of a five-column footer onto its own line.
		if len(col) > 0 {
			head := col[0]
			base[i] += head.marginRight
			if !head.borderBox {
				base[i] += head.boxLeft
			}
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
		ls, h := layoutFlexRun(b, run, base, contentX, avail, y+total, baseSize, boxes, g)
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

// tableRow is one table row: its cells, each a column of blocks, and the
// number of table columns each cell spans.
type tableRow struct {
	cells [][]renderBlock
	spans []int
}

// layoutTable lays a table's rows out as cells in shared columns. Column
// widths come from the widest cell in each column (max-content), scaled to the
// table's used width: the declared width, or the content width when narrower.
// Each row is as tall as its tallest cell.
func layoutTable(b renderBlock, colX, colW, y, baseSize float64, boxes *[]drawBox, g *geomSink) ([]drawLine, float64) {
	if len(b.rows) == 0 {
		return nil, 0
	}
	contentX := colX + b.boxLeft
	avail := colW - b.boxLeft
	if avail < 10 {
		avail = 10
	}
	gap := b.gap
	spanAt := func(row tableRow, ci int) int {
		if ci < len(row.spans) && row.spans[ci] > 1 {
			return row.spans[ci]
		}
		return 1
	}
	// cols is the widest row's column count.
	cols := 0
	for _, row := range b.rows {
		used := 0
		for ci := range row.cells {
			used += spanAt(row, ci)
		}
		if used > cols {
			cols = used
		}
	}
	if cols == 0 {
		return nil, 0
	}
	widths := make([]float64, cols)
	for _, row := range b.rows {
		ci := 0
		for k, cell := range row.cells {
			sp := spanAt(row, k)
			if ci+sp > cols {
				sp = cols - ci
			}
			if sp < 1 {
				break
			}
			w := intrinsicColumnWidth(cell, avail, baseSize)
			if sp == 1 {
				if w > widths[ci] {
					widths[ci] = w
				}
			} else {
				per := w / float64(sp)
				for j := 0; j < sp; j++ {
					if per > widths[ci+j] {
						widths[ci+j] = per
					}
				}
			}
			ci += sp
		}
	}
	sum := 0.0
	for _, w := range widths {
		sum += w
	}
	target := sum
	if b.tableStretch {
		target = avail - gap*float64(cols-1)
	} else if sum > avail {
		target = avail - gap*float64(cols-1)
	}
	if sum > 0 && target > 0 && math.Abs(target-sum) > 0.5 {
		scale := target / sum
		for j := range widths {
			widths[j] *= scale
			if widths[j] < 0 {
				widths[j] = 0
			}
		}
	}

	var lines []drawLine
	total := 0.0
	for ri, row := range b.rows {
		if ri > 0 {
			total += gap
		}
		// x of each column.
		xs := make([]float64, cols)
		x := contentX
		for j := 0; j < cols; j++ {
			xs[j] = x
			x += widths[j] + gap
		}
		var childLines [][]drawLine
		childH := []float64{}
		maxH := 0.0
		ci := 0
		for k, cell := range row.cells {
			sp := spanAt(row, k)
			if ci+sp > cols {
				sp = cols - ci
			}
			if sp < 1 {
				break
			}
			cx := xs[ci]
			cw := gap * float64(sp-1)
			for j := 0; j < sp; j++ {
				cw += widths[ci+j]
			}
			for j := range cell {
				cell[j].hasSizeOwner = false
			}
			cl, endY := layoutColumn(cell, cx, cw, y+total, baseSize, boxes, g)
			childLines = append(childLines, cl)
			h := endY - (y + total)
			childH = append(childH, h)
			if h > maxH {
				maxH = h
			}
			ci += sp
		}
		for k := range childLines {
			dy := 0.0
			switch b.alignItems {
			case "center":
				dy = (maxH - childH[k]) / 2
			case "end":
				dy = maxH - childH[k]
			}
			if dy != 0 {
				for j := range childLines[k] {
					childLines[k][j].y += dy
				}
			}
			lines = append(lines, childLines[k]...)
		}
		total += maxH
	}
	if b.hasBG {
		lines = append([]drawLine{{
			y: y, height: total, hasBG: true, bg: b.bg, bgX: contentX, bgW: avail,
		}}, lines...)
	}
	return lines, total
}

// layoutGrid places a grid container's children in the column tracks of its
// grid-template-columns. Auto placement is row-major: items fill the tracks
// left to right and a new row starts when the remaining tracks cannot hold the
// next item's span. Each row is as tall as its tallest item.
func layoutGrid(b renderBlock, colX, colW, y, baseSize float64, boxes *[]drawBox, g *geomSink) ([]drawLine, float64) {
	n := len(b.children)
	if n == 0 {
		return nil, 0
	}
	contentX := colX + b.boxLeft
	avail := colW - b.boxLeft
	if avail < 10 {
		avail = 10
	}
	gap := b.gap
	tracks := b.gridTmpl.resolve(avail, gap)
	if len(tracks) == 0 {
		return nil, 0
	}
	cols := len(tracks)
	// row-gap defaults to zero, not to column-gap: "column-gap: 10px" alone
	// leaves the rows touching.
	rowGap := b.rowGap
	spanOf := func(i int) int {
		sp := 1
		if i < len(b.gridSpans) && b.gridSpans[i] > 1 {
			sp = b.gridSpans[i]
		}
		if sp > cols {
			sp = cols
		}
		return sp
	}
	// colOf is the track the item's grid-column puts it in, or -1 when it
	// is auto-placed and has to fill the first free track instead.
	colOf := func(i int) int {
		if i < len(b.gridCols) && b.gridCols[i] >= 0 {
			return b.gridCols[i]
		}
		return -1
	}
	// trackX is the x offset and width of a run of tracks.
	trackX := func(col, sp int) (x, w float64) {
		x = contentX
		for k := 0; k < col; k++ {
			x += tracks[k] + gap
		}
		w = gap * float64(sp-1)
		for k := 0; k < sp; k++ {
			w += tracks[col+k]
		}
		return x, w
	}

	// Placement. An item with a grid-area, or with both a grid-row and a
	// grid-column, is fixed where it says; anything else is auto-placed into
	// the first free cell, in row-major order.
	type gridCell struct{ col, span, row, rowSpan int }
	cells := make([]gridCell, n)
	for i := range cells {
		sp := spanOf(i)
		col := colOf(i)
		if col >= 0 && col+sp > cols {
			col = cols - sp
		}
		row := -1
		if i < len(b.gridRows) && b.gridRows[i] >= 0 {
			row = b.gridRows[i]
		}
		rowSpan := 1
		if i < len(b.gridRowSpans) && b.gridRowSpans[i] > 1 {
			rowSpan = b.gridRowSpans[i]
		}
		cells[i] = gridCell{col: col, span: sp, row: row, rowSpan: rowSpan}
	}
	occupied := map[[2]int]bool{}
	for i, it := range cells {
		if it.col < 0 || it.row < 0 {
			continue
		}
		cells[i].col = it.col
		for r := it.row; r < it.row+it.rowSpan; r++ {
			for c := it.col; c < it.col+it.span && c < cols; c++ {
				occupied[[2]int{r, c}] = true
			}
		}
	}
	cursorRow, cursorCol := 0, 0
	for i := range cells {
		it := &cells[i]
		if it.col >= 0 && it.row >= 0 {
			continue
		}
		if it.col >= 0 {
			// A definite column: the first row where it fits.
			for r := 0; ; r++ {
				free := true
				for c := it.col; c < it.col+it.span && c < cols; c++ {
					if occupied[[2]int{r, c}] {
						free = false
						break
					}
				}
				if free {
					it.row = r
					break
				}
			}
			for c := it.col; c < it.col+it.span && c < cols; c++ {
				occupied[[2]int{it.row, c}] = true
			}
			continue
		}
		// Fully auto: the first free run from the cursor on.
		for {
			if cursorCol+it.span > cols {
				cursorRow++
				cursorCol = 0
				continue
			}
			free := true
			for c := cursorCol; c < cursorCol+it.span; c++ {
				if occupied[[2]int{cursorRow, c}] {
					free = false
					break
				}
			}
			if free {
				it.row, it.col = cursorRow, cursorCol
				for c := cursorCol; c < cursorCol+it.span; c++ {
					occupied[[2]int{cursorRow, c}] = true
				}
				cursorCol += it.span
				break
			}
			cursorCol++
		}
	}
	rowOrder := make([]int, 0, n)
	rowItems := map[int][]int{}
	for i, it := range cells {
		if _, ok := rowItems[it.row]; !ok {
			rowOrder = append(rowOrder, it.row)
		}
		rowItems[it.row] = append(rowItems[it.row], i)
	}
	sort.Ints(rowOrder)

	var lines []drawLine
	total := 0.0
	for _, r := range rowOrder {
		// The row's items, left to right.
		var row []int
		var xs, ws []float64
		idxs := rowItems[r]
		sort.SliceStable(idxs, func(a, b int) bool { return cells[idxs[a]].col < cells[idxs[b]].col })
		for _, i := range idxs {
			x, w := trackX(cells[i].col, cells[i].span)
			row = append(row, i)
			xs = append(xs, x)
			ws = append(ws, w)
		}
		if len(row) == 0 {
			continue
		}
		if total > 0 {
			total += rowGap
		}
		// A row's declared track size is its height even when its items hold
		// little: "grid-template-rows: 40px 60px" is what makes a grid of
		// empty cells stand 100px tall. A flexible row keeps its content
		// height, since this layout gives a grid no height of its own to
		// share out.
		rowH := 0.0
		if r < len(b.gridTmpl.rows) {
			// Only a fixed row size is known here: a percentage resolves
			// against the grid's own height, which this layout has not
			// decided, and a flexible row shares out a height that does not
			// exist either. Brave's rows are "100%", and reading that against
			// the grid's width made every row as tall as the page is wide.
			if rt := b.gridTmpl.rows[r]; rt.fr == 0 && rt.pct == 0 {
				rowH = rt.px
			}
		}
		// Measure the row's items first: the row is as tall as its tallest
		// one, and then every item stretches to it, which is what the default
		// "align-self: stretch" does and what makes a grid area's background
		// cover the whole cell.
		measured := make([]float64, len(row))
		for k, ci := range row {
			col := b.children[ci]
			for j := range col {
				col[j].hasSizeOwner = false
			}
			var scratch []drawBox
			_, endY := layoutColumn(col, xs[k], ws[k], y+total, baseSize, &scratch, g)
			measured[k] = endY - (y + total)
			if measured[k] > rowH {
				rowH = measured[k]
			}
		}
		stretch := b.alignItems == "" || b.alignItems == "stretch" || b.alignItems == "normal"
		maxH := 0.0
		childLines := make([][]drawLine, len(row))
		childH := make([]float64, len(row))
		for k, ci := range row {
			// A grid item's width comes from its tracks, so an ancestor's
			// width context no longer applies.
			col := b.children[ci]
			for j := range col {
				col[j].hasSizeOwner = false
			}
			// The stretch is set for this call and put back afterwards: the
			// blocks belong to the collected tree, which a page is laid out
			// from more than once.
			// An empty item stretches to the row, which is what fills a cell
			// with a background: a grid of coloured cells is drawn by these
			// boxes, and "align-self: stretch" is why they cover the cell.
			// An item with content keeps its content height, which is this
			// layout's model everywhere else.
			stretched := 0.0
			if stretch && len(col) > 0 && measured[k] == 0 {
				if last := &col[len(col)-1]; rowH > last.minHeight {
					stretched = last.minHeight
					last.minHeight = rowH
				}
			}
			cl, endY := layoutColumn(col, xs[k], ws[k], y+total, baseSize, boxes, g)
			if stretched != 0 {
				col[len(col)-1].minHeight = stretched
			}
			childLines[k] = cl
			childH[k] = endY - (y + total)
			if childH[k] > maxH {
				maxH = childH[k]
			}
		}
		if rowH > maxH {
			maxH = rowH
		}

		for k := range childLines {
			dy := 0.0
			switch b.alignItems {
			case "center":
				dy = (maxH - childH[k]) / 2
			case "end":
				dy = maxH - childH[k]
			}
			if dy != 0 {
				for j := range childLines[k] {
					childLines[k][j].y += dy
				}
			}
			lines = append(lines, childLines[k]...)
		}
		total += maxH
	}
	// The container's own background spans every track.
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
		// A half-pixel of slack keeps a row from wrapping over the rounding
		// error of a percentage width: five "width: 20%" columns of 204.6
		// sum to 1023.0000000000001 in a 1023px row.
		if len(cur) > 0 && curW+add > avail+0.5 {
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
func layoutFlexRun(b renderBlock, idx []int, base []float64, contentX, avail, y, baseSize float64, boxes *[]drawBox, g *geomSink) ([]drawLine, float64) {
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
		// Shrink to fit the row. Each item gives up a share of the overflow
		// proportional to flex-shrink times its own size, so a "flex: none"
		// item keeps its width while the rest of the row gives way.
		room := avail - sumGaps
		if room < 0 {
			room = 0
		}
		overflow := sum - room
		weight := 0.0
		for i, ci := range idx {
			s := 1.0
			if ci < len(b.shrink) {
				s = b.shrink[ci]
			}
			weight += s * widths[i]
		}
		if weight > 0 {
			for i, ci := range idx {
				s := 1.0
				if ci < len(b.shrink) {
					s = b.shrink[ci]
				}
				w := widths[i] - overflow*s*widths[i]/weight
				if w < 0 {
					w = 0
				}
				widths[i] = w
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
		// A flex item's width comes from the row, so an ancestor's width
		// context no longer applies: reset it before laying the column out.
		// The declared width an ancestor pinned travelled down as the item's
		// own, which made a nested row measure itself at the ancestor's width
		// instead of the item's and wrap early.
		col := b.children[ci]
		for j := range col {
			col[j].hasSizeOwner = false
			if !col[j].boxWidthIsOwn {
				col[j].hasBoxWidth = false
			}
		}
		cl, endY := layoutColumn(col, x, widths[i], y, baseSize, boxes, g)
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
// floatWidth is the width a float takes: the width it declares, or its widest
// content, clamped to the space it floats in.
func floatWidth(b renderBlock, limit, baseSize float64) float64 {
	w := 0.0
	if b.hasFloatW {
		w = b.floatW + b.floatPct*limit
	}
	if w <= 0 {
		w = intrinsicColumnWidth(b.floatBlocks, limit, baseSize)
	}
	if w > limit {
		w = limit
	}
	if w < 0 {
		w = 0
	}
	return w
}

// flexBaseLimit is the limit a flex item's max-content measurement is taken
// with: no limit, as far as the arithmetic is concerned.
const flexBaseLimit = 1e9

func intrinsicColumnWidth(col []renderBlock, limit, baseSize float64) float64 {
	w := 0.0
	// A float sits beside the content rather than replacing it, so the
	// column's max-content width is the content plus the floats on it.
	left, right := 0.0, 0.0
	for _, b := range col {
		switch b.kind {
		case blockImage:
			if tw := b.boxLeft + b.picW; tw > w {
				w = tw
			}
		case blockFlex:
			// The row's border-box width: from its left edge, over the
			// padding and border, then its items and the gaps between them.
			// Using boxLeft here dropped the row's own horizontal padding,
			// which is what a nav item's "padding: 0 12px" is.
			sub := b.textX
			for i, ch := range b.children {
				if i > 0 {
					sub += b.gap
				}
				sub += intrinsicColumnWidth(ch, limit, baseSize)
			}
			sub += b.paddingRight + b.borderW
			if sub > w {
				w = sub
			}
		case blockFloat:
			// The float itself has no spans, so without this it measures as
			// nothing and the item or cell it sits in collapses.
			if tw := b.boxLeft + floatWidth(b, limit, baseSize); b.floatSide == "left" {
				if tw > left {
					left = tw
				}
			} else if tw > right {
				right = tw
			}
		default:
			// The item's border-box width: text plus left/right padding and
			// borders. Missing the right padding made a padded flex item too
			// narrow, so its text wrapped one word per line.
			tw := b.textX + spansIntrinsicWidth(b.spans) + b.paddingRight + 2*b.borderW
			// A box that declares its own width measures as that width: the
			// max-content contribution of a definite size is the size, not the
			// text inside it. That is how an empty icon <span> of
			// "width: 18px" counts, and equally how three "width: 150px"
			// spans count. Only a width that is the box's own is used; a
			// percentage or a viewport length resolves against something
			// else, and feeding those in inflated every shrink-to-fit
			// container that held a "width: 100%" box.
			if b.boxWidthIsOwn && b.boxWidthPx > tw {
				tw = b.boxWidthPx
			}
			if tw > w {
				w = tw
			}
		}
	}
	w += left + right
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

// wrapSpansWidth greedily fills lines, breaking between words, with a width
// that can change from line to line. That is how a paragraph wraps around a
// float and then fills the column again once the float has passed. Every line
// is a sequence of spans, so an inline replaced box (an icon) can sit between
// text runs on the same line.
func wrapSpansWidth(spans []renderSpan, limitAt func(line int) float64) [][]renderSpan {
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
		limit := limitAt(len(lines))
		spaceW := 0.0
		if i > 0 {
			if st, ok := spaceStyles[i-1]; ok {
				spaceW = textWidth(st, " ")
				if curW > 0 && curW+spaceW+it.w > limit {
					flush()
					limit = limitAt(len(lines))
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
