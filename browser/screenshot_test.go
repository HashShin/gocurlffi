package browser

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	if len(data) < 8 || !bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("not a PNG (first bytes %q)", data[:min(8, len(data))])
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	return img
}

// nonWhite counts pixels that were drawn, proving the render is not blank.
func nonWhite(img image.Image) int {
	b := img.Bounds()
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r>>8 < 250 || g>>8 < 250 || bl>>8 < 250 {
				n++
			}
		}
	}
	return n
}

func screenshotOf(t *testing.T, html string, opts ScreenshotOptions) image.Image {
	t.Helper()
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	if err := p.SetContent(html, "https://example.test/"); err != nil {
		t.Fatalf("SetContent: %v", err)
	}
	data, err := p.Screenshot(opts)
	if err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	return decodePNG(t, data)
}

func TestScreenshotBasic(t *testing.T) {
	img := screenshotOf(t, `<html><body><h1>Title</h1><p>Some body text that should be drawn.</p></body></html>`,
		ScreenshotOptions{Width: 800})
	if got := img.Bounds().Dx(); got != 800 {
		t.Fatalf("width = %d, want 800", got)
	}
	if img.Bounds().Dy() < 20 {
		t.Fatalf("height = %d, too small", img.Bounds().Dy())
	}
	if n := nonWhite(img); n < 100 {
		t.Fatalf("only %d drawn pixels; render looks blank", n)
	}
}

func TestScreenshotScale(t *testing.T) {
	const html = `<html><body><p>scaled</p></body></html>`
	one := screenshotOf(t, html, ScreenshotOptions{Width: 400, Scale: 1})
	two := screenshotOf(t, html, ScreenshotOptions{Width: 400, Scale: 2})
	if two.Bounds().Dx() != 800 {
		t.Fatalf("2x width = %d, want 800", two.Bounds().Dx())
	}
	if two.Bounds().Dy() < one.Bounds().Dy() {
		t.Fatalf("2x height %d < 1x height %d", two.Bounds().Dy(), one.Bounds().Dy())
	}
}

// Wrapping means more text needs more height; display:none must not render.
func TestScreenshotLayoutEffects(t *testing.T) {
	short := screenshotOf(t, `<html><body><p>one line</p></body></html>`, ScreenshotOptions{Width: 400})
	long := screenshotOf(t, `<html><body><p>`+repeat("word ", 400)+`</p></body></html>`, ScreenshotOptions{Width: 400})
	if long.Bounds().Dy() <= short.Bounds().Dy() {
		t.Fatalf("long text height %d not greater than short %d", long.Bounds().Dy(), short.Bounds().Dy())
	}

	withHidden := screenshotOf(t, `<html><body><p>a</p><div style="display:none"><p>b</p></div><p>c</p></body></html>`, ScreenshotOptions{Width: 400})
	without := screenshotOf(t, `<html><body><p>a</p><p>c</p></body></html>`, ScreenshotOptions{Width: 400})
	if withHidden.Bounds().Dy() != without.Bounds().Dy() {
		t.Fatalf("display:none changed layout: %d vs %d", withHidden.Bounds().Dy(), without.Bounds().Dy())
	}
}

func TestScreenshotMaxHeight(t *testing.T) {
	img := screenshotOf(t, `<html><body><p>`+repeat("word ", 2000)+`</p></body></html>`,
		ScreenshotOptions{Width: 400, MaxHeight: 200})
	if got := img.Bounds().Dy(); got != 200 {
		t.Fatalf("height = %d, want the 200px cap", got)
	}
}

func TestScreenshotListsAndPre(t *testing.T) {
	img := screenshotOf(t, `<html><body><ul><li>one</li><li>two</li></ul>
<ol><li>first</li></ol><pre>a  b
  c</pre><blockquote><p>quoted</p></blockquote><hr>
<p>after</p></body></html>`, ScreenshotOptions{Width: 600})
	if n := nonWhite(img); n < 200 {
		t.Fatalf("lists/pre render looks blank (%d px)", n)
	}
}

func TestScreenshotNoDocument(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	if _, err := p.Screenshot(ScreenshotOptions{}); err == nil {
		t.Fatalf("expected an error when the page has no document")
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Text must stay inside the margins; an earlier bug let lines run to the last
// pixel column and clip.
func TestScreenshotRespectsRightMargin(t *testing.T) {
	const width = 520
	img := screenshotOf(t, `<html><body><p>`+repeat("alpha bravo charlie delta echo ", 60)+`</p></body></html>`,
		ScreenshotOptions{Width: width})
	b := img.Bounds()
	maxX := -1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Max.X - 1; x >= 0; x-- {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r>>8 < 250 || g>>8 < 250 || bl>>8 < 250 {
				if x > maxX {
					maxX = x
				}
				break
			}
		}
	}
	if maxX > width-16 {
		t.Fatalf("ink reaches x=%d of %d; right margin not respected", maxX, width)
	}
}

func BenchmarkScreenshot(b *testing.B) {
	br := New(Options{})
	defer br.Close()
	p := br.NewPage("https://example.test/")
	var sb []byte
	sb = append(sb, `<html><body><h1>Heading</h1>`...)
	for i := 0; i < 40; i++ {
		sb = append(sb, `<p>Paragraph with <b>bold</b> and <i>italic</i> text that wraps across lines.</p><ul><li>item</li></ul>`...)
	}
	sb = append(sb, `</body></html>`...)
	_ = p.SetContent(string(sb), "https://example.test/")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := p.Screenshot(ScreenshotOptions{Width: 900}); err != nil {
			b.Fatal(err)
		}
	}
}

// A page's pictures are drawn into the render, and the bytes are only fetched
// when images are asked for.
func TestScreenshotDrawsImages(t *testing.T) {
	pic := image.NewRGBA(image.Rect(0, 0, 120, 60))
	draw.Draw(pic, pic.Bounds(), image.NewUniform(color.RGBA{R: 0xff, G: 0x00, B: 0xff, A: 0xff}), image.Point{}, draw.Src)
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, pic); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pic.png":
			atomic.AddInt32(&hits, 1)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBuf.Bytes())
		case "/pic2x.png":
			atomic.AddInt32(&hits, 1)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBuf.Bytes())
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body>
				<p>before</p>
				<img src="/pic.png" srcset="/pic.png 1x, /pic2x.png 2x" alt="a picture">
				<p>after</p></body></html>`))
		}
	}))
	defer srv.Close()

	b := New(Options{})
	defer b.Close()
	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	out, err := p.Screenshot(ScreenshotOptions{Width: 400, NoImages: true})
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("NoImages still fetched %d images", got)
	}
	if magenta(out) != 0 {
		t.Fatalf("NoImages still drew the picture")
	}
	// The alt text is drawn in place of a picture when there is none to draw;
	// that path is covered by the img handling in the collector, and the alt
	// is not part of the page's text for extraction.
	if strings.Contains(p.Text(), "a picture") {
		t.Errorf("alt text leaked into the extracted text")
	}

	out, err = p.Screenshot(ScreenshotOptions{Width: 400})
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got == 0 {
		t.Fatalf("no image was fetched")
	}
	// The srcset's 2x candidate is preferred, and it is 120x60, so at the
	// natural size the drawn area is 7200 magenta pixels.
	if n := magenta(out); n < 3000 {
		t.Fatalf("drew %d magenta pixels, want the picture at its natural size", n)
	}
}

// magenta counts the pixels of the test picture's colour.
func magenta(pngBytes []byte) int {
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return -1
	}
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r>>8 > 0xf0 && g>>8 < 0x20 && bl>>8 > 0xf0 {
				n++
			}
		}
	}
	return n
}

// A translucent background must blend with what is behind it and stay opaque,
// not be written over the page with alpha (which read as near-white lines on a
// dark page).
func TestScreenshotCompositesTranslucentColors(t *testing.T) {
	img := screenshotOf(t, `<html><body style="margin:0;padding:0;background:#000">
		<div style="background:rgba(255,255,255,0.5)">x</div></body></html>`,
		ScreenshotOptions{Width: 200, NoImages: true})
	b := img.Bounds()
	found := false
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a != 0xffff {
				t.Fatalf("pixel (%d,%d) has alpha %d, want opaque", x, y, a>>8)
			}
			if r>>8 == g>>8 && g>>8 == bl>>8 && r>>8 >= 120 && r>>8 <= 136 {
				found = true
			}
		}
	}
	if !found {
		t.Error("no ~50% grey pixel: rgba(255,255,255,0.5) was not composited over black")
	}
}

// Vertical padding is part of the box model: it must push content down and add
// space after it, even when the element's first child is a block.
func TestScreenshotAppliesVerticalPadding(t *testing.T) {
	opts := ScreenshotOptions{Width: 400, NoImages: true}
	plain := screenshotOf(t, `<html><body style="margin:0"><div>x</div></body></html>`, opts)
	direct := screenshotOf(t, `<html><body style="margin:0"><div style="padding-top:40px;padding-bottom:40px">x</div></body></html>`, opts)
	nested := screenshotOf(t, `<html><body style="margin:0"><div style="padding-top:40px;padding-bottom:40px"><div>x</div></div></body></html>`, opts)

	for _, c := range []struct {
		name string
		img  image.Image
	}{
		{"text child", direct},
		{"block child", nested},
	} {
		if diff := c.img.Bounds().Dy() - plain.Bounds().Dy(); diff < 70 || diff > 90 {
			t.Errorf("%s: height grew by %d, want about 80 from 40px top + 40px bottom padding", c.name, diff)
		}
	}
}

// outlinePositions parses RenderOutline output into a map from the drawn text
// to its x and y, for asserting relative placement.
func outlinePositions(t *testing.T, p *Page, width int) map[string][2]float64 {
	t.Helper()
	out, err := p.RenderOutline(ScreenshotOptions{Width: width, NoImages: true}, 200)
	if err != nil {
		t.Fatalf("RenderOutline: %v", err)
	}
	re := regexp.MustCompile(`^y=(-?\d+)\s+h=\d+\s+text\s+x=(-?\d+)\s+(.*)$`)
	pos := map[string][2]float64{}
	for _, line := range out {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		y, _ := strconv.ParseFloat(m[1], 64)
		x, _ := strconv.ParseFloat(m[2], 64)
		text := strings.TrimSpace(m[3])
		if i := strings.Index(text, " bg="); i >= 0 {
			text = strings.TrimSpace(text[:i])
		}
		pos[text] = [2]float64{x, y}
	}
	return pos
}

func flexPage(t *testing.T, html string) *Page {
	t.Helper()
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	if err := p.SetContent(html, "https://example.test/"); err != nil {
		t.Fatalf("SetContent: %v", err)
	}
	return p
}

// A flex row places its children side by side on one line.
func TestFlexRowPlacesChildrenSideBySide(t *testing.T) {
	p := flexPage(t, `<html><head><style>
		.row { display: flex; gap: 20px }
	</style></head><body>
		<div class="row"><div>Left</div><div>Right</div></div>
	</body></html>`)
	pos := outlinePositions(t, p, 400)
	l, ok1 := pos["Left"]
	r, ok2 := pos["Right"]
	if !ok1 || !ok2 {
		t.Fatalf("missing items in outline: %v", pos)
	}
	if l[1] != r[1] {
		t.Errorf("children not on the same line: Left y=%g, Right y=%g", l[1], r[1])
	}
	if r[0] <= l[0] {
		t.Errorf("Right x=%g is not right of Left x=%g", r[0], l[0])
	}
}

// flex-grow splits the free space; two "flex: 1" items end up equal halves.
func TestFlexGrowSplitsSpace(t *testing.T) {
	p := flexPage(t, `<html><head><style>
		.row { display: flex }
		.row > div { flex: 1 }
	</style></head><body>
		<div class="row"><div>A</div><div>B</div></div>
	</body></html>`)
	pos := outlinePositions(t, p, 400)
	a, ok1 := pos["A"]
	b, ok2 := pos["B"]
	if !ok1 || !ok2 {
		t.Fatalf("missing items: %v", pos)
	}
	// B starts near the middle of the 400px page.
	if b[0] < 150 || b[0] > 250 {
		t.Errorf("grow did not split the row: B x=%g, want about 200", b[0])
	}
	if a[0] > 30 {
		t.Errorf("A x=%g, want it at the left edge", a[0])
	}
}

// flex-basis with calc() sets the main size, and flex-wrap breaks to a new row
// when the items no longer fit.
func TestFlexBasisCalcAndWrap(t *testing.T) {
	p := flexPage(t, `<html><head><style>
		.wrap { display: flex; flex-wrap: wrap }
		.wrap > div { flex: 0 0 calc(50% - 5px) }
	</style></head><body>
		<div class="wrap"><div>one</div><div>two</div><div>three</div></div>
	</body></html>`)
	pos := outlinePositions(t, p, 400)
	one, ok1 := pos["one"]
	two, ok2 := pos["two"]
	three, ok3 := pos["three"]
	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("missing items: %v", pos)
	}
	if one[1] != two[1] {
		t.Errorf("one and two should share the first row: y=%g, %g", one[1], two[1])
	}
	if three[1] <= one[1] {
		t.Errorf("three should wrap to a later row: y=%g, want > %g", three[1], one[1])
	}
	if two[0] < 180 {
		t.Errorf("two x=%g, want it in the right half", two[0])
	}
}

// justify-content: center centers the row's content.
func TestFlexJustifyCenter(t *testing.T) {
	p := flexPage(t, `<html><head><style>
		.row { display: flex; justify-content: center; gap: 10px }
	</style></head><body>
		<div class="row"><div>xx</div></div>
	</body></html>`)
	pos := outlinePositions(t, p, 400)
	x := pos["xx"][0]
	if x < 150 {
		t.Errorf("centered item x=%g, want it near the middle", x)
	}
}

// An inline <svg> is rasterized and drawn; a red square must appear in it.
func TestScreenshotDrawsInlineSVG(t *testing.T) {
	img := screenshotOf(t, `<html><body style="margin:0">
		<svg width="40" height="40" viewBox="0 0 40 40" xmlns="http://www.w3.org/2000/svg">
			<rect width="40" height="40" fill="#ff0000"/>
		</svg></body></html>`, ScreenshotOptions{Width: 200, NoImages: true})
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r>>8 > 0xd0 && g>>8 < 0x40 && bl>>8 < 0x40 {
				n++
			}
		}
	}
	if n < 800 {
		t.Errorf("drew %d red pixels, want the 40x40 inline SVG", n)
	}
}

// currentColor in an inline SVG follows the element's computed colour.
func TestScreenshotInlineSVGUsesCurrentColor(t *testing.T) {
	img := screenshotOf(t, `<html><head><style>body{color:#00ff00}</style></head>
		<body style="margin:0">
		<svg width="40" height="40" viewBox="0 0 40 40" xmlns="http://www.w3.org/2000/svg">
			<rect width="40" height="40" fill="currentColor"/>
		</svg></body></html>`, ScreenshotOptions{Width: 200, NoImages: true})
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if g>>8 > 0xd0 && r>>8 < 0x40 && bl>>8 < 0x40 {
				n++
			}
		}
	}
	if n < 800 {
		t.Errorf("drew %d green pixels, want currentColor green", n)
	}
}

// An <img> with a CSS width is drawn at that width, not its natural size.
func TestScreenshotInlineImageUsesCSSWidth(t *testing.T) {
	pic := image.NewRGBA(image.Rect(0, 0, 120, 60))
	draw.Draw(pic, pic.Bounds(), image.NewUniform(color.RGBA{R: 0xff, G: 0x00, B: 0xff, A: 0xff}), image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, pic); err != nil {
		t.Fatalf("encode: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/pic.png" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(buf.Bytes())
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body style="margin:0"><img src="/pic.png" style="width:20px"></body></html>`))
	}))
	defer srv.Close()

	b := New(Options{})
	defer b.Close()
	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	out, err := p.Screenshot(ScreenshotOptions{Width: 200})
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if n := magenta(out); n < 100 || n > 600 {
		t.Errorf("drew %d magenta pixels, want about 20x10=200", n)
	}
}

// Native controls draw something: a checked checkbox shows accent pixels, a
// range shows a track, a color input shows its swatch, and text-like controls
// and selects show their value.
func TestScreenshotDrawsFormControls(t *testing.T) {
	img := screenshotOf(t, `<html><body style="margin:0;background:#fff">
		<input type="checkbox" checked>
		<input type="radio" checked>
		<input type="range">
		<input type="color" value="#ff0000">
		<input type="text" value="hello">
		<select><option>a</option><option selected>b</option></select>
		<input type="submit" value="Go">
		</body></html>`, ScreenshotOptions{Width: 500, NoImages: true})
	b := img.Bounds()
	accent, red := 0, 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r>>8 < 0x30 && g>>8 > 0x40 && g>>8 < 0x80 && bl>>8 > 0xb0 {
				accent++
			}
			if r>>8 > 0xd0 && g>>8 < 0x40 && bl>>8 < 0x40 {
				red++
			}
		}
	}
	if accent < 20 {
		t.Errorf("drew %d accent pixels, want the checkbox/range/radio widgets", accent)
	}
	if red < 100 {
		t.Errorf("drew %d red pixels, want the color swatch", red)
	}
}

// A column flex container with align-items:center centers its children. The
// centering shifts the drawn run, not the block indent, so this checks pixels.
func TestFlexColumnCentersChildren(t *testing.T) {
	img := screenshotOf(t, `<html><head><style>
		.col { display: flex; flex-direction: column; align-items: center }
	</style></head><body style="margin:0;background:#fff">
		<div class="col"><div>mid</div></div>
	</body></html>`, ScreenshotOptions{Width: 400, NoImages: true})
	b := img.Bounds()
	minX, maxX := b.Max.X, b.Min.X
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r>>8 < 0x80 && g>>8 < 0x80 && bl>>8 < 0x80 {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
			}
		}
	}
	if minX > 190 {
		t.Errorf("text spans x=%d..%d, want it centered in 0..%d", minX, maxX, b.Dx())
	}
}

// An empty textarea reserves its min-height and draws a border, so it is a
// visible box rather than nothing.
func TestTextareaReservesBox(t *testing.T) {
	img := screenshotOf(t, `<html><body style="margin:0;background:#000">
		<textarea style="min-height:100px;border:2px solid #ffffff;background:#000;width:300px"></textarea>
		</body></html>`, ScreenshotOptions{Width: 400, NoImages: true})
	if h := img.Bounds().Dy(); h < 90 || h > 160 {
		t.Errorf("render height %d, want the textarea's ~100px box", h)
	}
	white := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r>>8 > 0xd0 && g>>8 > 0xd0 && bl>>8 > 0xd0 {
				white++
			}
		}
	}
	if white < 200 {
		t.Errorf("drew %d border pixels, want a visible border", white)
	}
}

// redBounds returns the bounding box of red pixels, or ok=false.
func redBounds(img image.Image) (minX, minY, maxX, maxY int, ok bool) {
	b := img.Bounds()
	minX, minY, maxX, maxY = b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r>>8 > 0xd0 && g>>8 < 0x40 && bl>>8 < 0x40 {
				ok = true
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	return
}

// An element border is drawn, and border-radius rounds its corners.
func TestElementBorderAndRadius(t *testing.T) {
	const page = `<html><body style="margin:0;background:#000">
		<div style="border:%d solid #ff0000;%s">x</div></body></html>`
	square := screenshotOf(t, fmtSprintf(page, 3, ""), ScreenshotOptions{Width: 300, NoImages: true})
	minX, minY, maxX, _, ok := redBounds(square)
	if !ok {
		t.Fatal("square border was not drawn")
	}
	if !isRed(square.At(minX, minY)) {
		t.Error("square border: the bounding-box corner is not red")
	}
	if maxX-minX < 100 {
		t.Errorf("border width %d, want it to span the column", maxX-minX)
	}

	round := screenshotOf(t, fmtSprintf(page, 3, "border-radius:24px;"), ScreenshotOptions{Width: 300, NoImages: true})
	minX, minY, _, _, ok = redBounds(round)
	if !ok {
		t.Fatal("rounded border was not drawn")
	}
	if isRed(round.At(minX, minY)) {
		t.Error("rounded border: the bounding-box corner is still red (not rounded)")
	}
}

func isRed(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r>>8 > 0xd0 && g>>8 < 0x40 && b>>8 < 0x40
}

func fmtSprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// outlineLineY returns the y of the rendered line whose text is label.
func outlineLineY(t *testing.T, p *Page, label string) float64 {
	t.Helper()
	out, err := p.RenderOutline(ScreenshotOptions{Width: 400, NoImages: true}, 50)
	if err != nil {
		t.Fatalf("RenderOutline: %v", err)
	}
	for _, l := range out {
		m := reOutline.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if strings.TrimSpace(m[2]) == label {
			y, _ := strconv.ParseFloat(m[1], 64)
			return y
		}
	}
	t.Fatalf("line %q not found in outline", label)
	return 0
}

var reOutline = regexp.MustCompile(`^y=(-?\d+)\s+h=\d+\s+text\s+x=-?\d+\s+(.*)$`)

// Adjacent vertical margins collapse to the larger one, not their sum.
func TestVerticalMarginsCollapse(t *testing.T) {
	gap := func(m1, m2 string) float64 {
		p := flexPage(t, `<html><body style="margin:0">
			<div style="`+m1+`">alpha</div><div style="`+m2+`">beta</div></body></html>`)
		return outlineLineY(t, p, "beta") - outlineLineY(t, p, "alpha")
	}
	collapsed := gap("margin-bottom:30px", "margin-top:20px")
	if same30 := gap("margin-bottom:30px", "margin-top:0"); same30 != collapsed {
		t.Errorf("30px + 20px gave %g but 30px + 0 gave %g, want equal (collapse)", collapsed, same30)
	}
	none := gap("margin-bottom:0", "margin-top:0")
	if none >= collapsed {
		t.Errorf("no margins gave %g, want less than the collapsed %g", none, collapsed)
	}
}

// width, max-width and margin:auto size and center a block instead of filling
// the column.
func TestBlockSizingAndAutoCentering(t *testing.T) {
	img := screenshotOf(t, `<html><body style="margin:0">
		<div style="width:100px;margin:0 auto;background:#ff0000;height:20px"></div>
		</body></html>`, ScreenshotOptions{Width: 400, NoImages: true})
	minX, _, maxX, _, ok := redBounds(img)
	if !ok {
		t.Fatal("the sized block was not drawn")
	}
	if w := maxX - minX; w < 95 || w > 105 {
		t.Errorf("block width %d, want 100", w)
	}
	if center := (minX + maxX) / 2; center < 170 || center > 205 {
		t.Errorf("block center %d, want it centered near 188-200", center)
	}

	// Without auto margins the block stays left aligned.
	left := screenshotOf(t, `<html><body style="margin:0">
		<div style="width:100px;background:#ff0000;height:20px"></div>
		</body></html>`, ScreenshotOptions{Width: 400, NoImages: true})
	minX, _, _, _, ok = redBounds(left)
	if !ok || minX > 5 {
		t.Errorf("left-aligned block starts at %d, want 0", minX)
	}
}

// max-height caps a block's box and clips the lines that overflow it.
func TestMaxHeightClips(t *testing.T) {
	img := screenshotOf(t, `<html><body style="margin:0">
		<div style="max-height:40px;width:120px">aaaa aaaa aaaa aaaa aaaa aaaa aaaa aaaa</div>
		</body></html>`, ScreenshotOptions{Width: 400, NoImages: true})
	if h := img.Bounds().Dy(); h > 90 {
		t.Errorf("render height %d, want the 40px max-height to clip the block", h)
	}
}

// box-sizing:border-box makes width include padding and border.
func TestBoxSizingBorderBox(t *testing.T) {
	img := screenshotOf(t, `<html><body style="margin:0">
		<div style="box-sizing:border-box;width:100px;padding:0 20px;background:#ff0000;height:20px"></div>
		</body></html>`, ScreenshotOptions{Width: 400, NoImages: true})
	minX, _, maxX, _, ok := redBounds(img)
	if !ok {
		t.Fatal("the box was not drawn")
	}
	if w := maxX - minX; w < 95 || w > 105 {
		t.Errorf("border-box width %d, want 100", w)
	}
	_ = minX
}

// box-shadow draws behind the box: a spread ring shows around it.
func TestBoxShadowRing(t *testing.T) {
	img := screenshotOf(t, `<html><body style="margin:0;background:#000">
		<div style="width:40px;height:40px;background:#ffffff;box-shadow:0 0 0 6px #ff0000"></div>
		</body></html>`, ScreenshotOptions{Width: 200, NoImages: true})
	_, _, _, _, ok := redBounds(img)
	if !ok {
		t.Error("box-shadow ring was not drawn")
	}
}

// A padded flex item is as wide as its text plus both paddings, so its label
// stays on one line instead of wrapping one word per line.
func TestPaddedFlexItemKeepsOneLine(t *testing.T) {
	p := flexPage(t, `<html><head><style>
		.row { display: flex }
		.tab { padding: 8px 20px }
	</style></head><body>
		<div class="row"><div class="tab">Website URL</div></div>
	</body></html>`)
	pos := outlinePositions(t, p, 400)
	if _, ok := pos["Website URL"]; !ok {
		t.Errorf("the padded label wrapped; outline has: %v", pos)
	}
}
