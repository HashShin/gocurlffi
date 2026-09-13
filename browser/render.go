package browser

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
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

	for _, ln := range doc.lines {
		if ln.pic != nil {
			drawScaledImage(img, ln.pic, s(ln.picX), s(ln.y), s(ln.picW), s(ln.height), scale)
			continue
		}
		if ln.hasBG {
			fillRect(img, s(ln.bgX), s(ln.y), s(ln.bgW), s(ln.height), ln.bg)
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
}

type renderBlockKind int

const (
	blockText renderBlockKind = iota
	blockRule
	blockImage
)

type renderSpan struct {
	text  string
	style renderStyle
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
	pre      bool
	nowrap   bool
	align    string
	lineH    float64 // explicit line-height in px, 0 = auto

	bg     color.RGBA
	hasBG  bool
	bgFull bool // background spans the full content width

	// An image block draws a picture instead of text, at its aspect ratio.
	pic  *pageImage
	picW float64 // natural width, 0 when unknown
	picH float64 // natural height
}

// layoutBlocks flows blocks into a single column of the given width.
func layoutBlocks(blocks []renderBlock, width int, baseSize float64) *renderDoc {
	const (
		margin   = 24.0
		lineFact = 1.45
	)
	doc := &renderDoc{width: width, baseSize: baseSize}
	y := margin
	contentW := float64(width) - 2*margin
	if contentW < 40 {
		contentW = 40
	}

	for _, b := range blocks {
		y += b.leading
		if b.kind == blockImage {
			w := contentW - (b.boxLeft - margin)
			if w < 40 {
				w = 40
			}
			if b.picW > 0 && b.picH > 0 {
				// Scale to the available width, but never up past the natural
				// size: a small icon should stay small.
				if b.picW > w {
					y = y
					w = w
				} else {
					w = b.picW
				}
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
			doc.lines = append(doc.lines, drawLine{
				y: y, height: h, pic: b.pic, picX: b.boxLeft, picW: w,
			})
			y += h + b.trailing
			continue
		}
		if b.kind == blockRule {
			doc.lines = append(doc.lines, drawLine{
				y:      y,
				height: 1,
				rule:   true,
				ruleX:  b.boxLeft,
				ruleW:  contentW - (b.boxLeft - margin),
			})
			y += 12 + b.trailing
			continue
		}
		textStart := b.textX
		limit := float64(width) - margin - textStart
		if limit < 40 {
			limit = 40
		}

		var lines []renderSpan
		switch {
		case b.pre:
			lines = splitPreLines(b.spans)
		case b.nowrap:
			lines = []renderSpan{{text: joinSpanText(b.spans), style: firstStyle(b.spans)}}
		default:
			lines = wrapSpans(b.spans, limit)
		}

		for i, ln := range lines {
			k := styleKey(ln.style)
			ascent, _, h := lineMetrics(k)
			lh := h * lineFact
			if b.lineH > 0 {
				lh = b.lineH
			}
			if lh < ln.style.size {
				lh = ln.style.size
			}
			dl := drawLine{
				y:        y,
				height:   lh,
				baseline: y + ascent + (lh-h)/2,
				quote:    b.quote,
				indent:   textStart,
			}
			if b.hasBG {
				dl.bg = b.bg
				dl.hasBG = true
				if b.bgFull {
					dl.bgX = b.boxLeft
					dl.bgW = contentW - (b.boxLeft - margin)
				} else {
					wpx := textWidth(ln.style, ln.text)
					dl.bgX = textStart
					dl.bgW = wpx
				}
				if dl.bgW < 0 {
					dl.bgW = 0
				}
			}
			runX := textStart
			if b.align == "center" || b.align == "right" {
				wpx := textWidth(ln.style, ln.text)
				space := limit - wpx
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
				dl.markerX = b.boxLeft
			}
			dl.runs = append(dl.runs, drawRun{x: runX, text: ln.text, style: ln.style})
			doc.lines = append(doc.lines, dl)
			y += lh
		}
		if len(lines) == 0 && b.marker != "" {
			k := faceKey{size: baseSize}
			_, _, h := lineMetrics(k)
			lh := h * lineFact
			doc.lines = append(doc.lines, drawLine{
				y: y, height: lh, baseline: y + h*0.8,
				marker: b.marker, markerX: b.boxLeft, quote: b.quote, indent: textStart,
			})
			y += lh
		}
		y += b.trailing
	}
	y += margin
	doc.height = int(y + 0.5)
	if doc.height < 1 {
		doc.height = 1
	}
	return doc
}

func firstStyle(spans []renderSpan) renderStyle {
	if len(spans) > 0 {
		return spans[0].style
	}
	return defaultRenderStyle()
}

func joinSpanText(spans []renderSpan) string {
	var b strings.Builder
	for _, s := range spans {
		b.WriteString(s.text)
	}
	return b.String()
}

// wrapSpans greedily fills lines, breaking between words.
func wrapSpans(spans []renderSpan, limit float64) []renderSpan {
	type word struct {
		text  string
		style renderStyle
		w     float64
	}
	var words []word
	spaceStyles := map[int]renderStyle{}
	for _, sp := range spans {
		fields := strings.Fields(sp.text)
		if len(fields) == 0 {
			if len(words) > 0 {
				spaceStyles[len(words)-1] = sp.style
			}
			continue
		}
		for i, w := range fields {
			if i > 0 {
				spaceStyles[len(words)-1] = sp.style
			}
			words = append(words, word{text: w, style: sp.style,
				w: textWidth(sp.style, w)})
		}
	}
	if len(words) == 0 {
		return nil
	}

	var out []renderSpan
	var cur strings.Builder
	var curStyle renderStyle
	curW := 0.0
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, renderSpan{text: cur.String(), style: curStyle})
			cur.Reset()
		}
		curW = 0
	}
	for i, w := range words {
		if i > 0 {
			spStyle := spaceStyles[i-1]
			spaceW := textWidth(spStyle, " ")
			if curW+spaceW+w.w > limit && curW > 0 {
				flush()
			} else if curW > 0 {
				cur.WriteString(" ")
				curW += spaceW
			}
		}
		if w.w > limit && curW == 0 {
			// A word wider than the column: hard-break it.
			var piece strings.Builder
			pieceW := 0.0
			for _, r := range w.text {
				rs := string(r)
				rw := textWidth(w.style, rs)
				if pieceW+rw > limit && piece.Len() > 0 {
					out = append(out, renderSpan{text: piece.String(), style: w.style})
					piece.Reset()
					pieceW = 0
				}
				piece.WriteString(rs)
				pieceW += rw
			}
			cur.WriteString(piece.String())
			curStyle = w.style
			curW = pieceW
			continue
		}
		if cur.Len() == 0 {
			curStyle = w.style
		}
		cur.WriteString(w.text)
		curW += w.w
	}
	flush()
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
