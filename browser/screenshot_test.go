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

func TestNestedPercentageWidth(t *testing.T) {
	// A percentage width resolves against the parent's content box, not the
	// page. The red child is measured against parents of known width.
	cases := []struct {
		name string
		html string
		want int
	}{
		{
			name: "half of a 200px parent",
			html: `<div style="width:200px;background:#00ff00;height:20px">
				<div style="width:50%;background:#ff0000;height:20px"></div></div>`,
			want: 100,
		},
		{
			name: "percentage of a percentage",
			html: `<div style="width:200px;background:#00ff00;height:20px">
				<div style="width:50%;height:20px">
					<div style="width:50%;background:#ff0000;height:20px"></div></div></div>`,
			want: 50,
		},
		{
			name: "border-box parent subtracts its padding",
			html: `<div style=" box-sizing:border-box;width:200px;padding:0 20px;background:#00ff00;height:20px">
				<div style="width:50%;background:#ff0000;height:20px"></div></div>`,
			want: 80,
		},
		{
			name: "content-box padding is outside the width",
			html: `<div style="width:200px;padding:0 20px;background:#00ff00;height:20px">
				<div style="width:50%;background:#ff0000;height:20px"></div></div>`,
			want: 100,
		},
		{
			name: "max-width clamps the containing width",
			html: `<div style="max-width:160px;background:#00ff00;height:20px">
				<div style="width:50%;background:#ff0000;height:20px"></div></div>`,
			want: 80,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			img := screenshotOf(t, `<html><body style="margin:0">`+tc.html+`</body></html>`,
				ScreenshotOptions{Width: 400, NoImages: true})
			minX, _, maxX, _, ok := redBounds(img)
			if !ok {
				t.Fatal("the child block was not drawn")
			}
			if w := maxX - minX; w < tc.want-5 || w > tc.want+5 {
				t.Errorf("child block width %d, want %d", w, tc.want)
			}
		})
	}
}

// layoutDoc lays the page out and returns the document, for asserting box
// geometry and text placement.
func layoutDoc(t *testing.T, html string, width int) *renderDoc {
	t.Helper()
	p := flexPage(t, html)
	doc, _, err := p.layOut(ScreenshotOptions{Width: width, NoImages: true})
	if err != nil {
		t.Fatalf("layOut: %v", err)
	}
	return doc
}

// The page column is the viewport. An element's own margins (including the
// body's 8px default) are the only inset; a page-wide gutter would make every
// page's text wrap early.
func TestColumnIsTheViewport(t *testing.T) {
	doc := layoutDoc(t, `<html><body style="margin:0;background:#fff">
		<div style="width:100%;background:#eee;height:10px"></div></body></html>`, 400)
	if len(doc.boxes) != 1 {
		t.Fatalf("boxes = %d, want 1", len(doc.boxes))
	}
	if w := doc.boxes[0].w; w != 400 {
		t.Errorf("width:100%% block is %g wide, want the 400px viewport", w)
	}

	// A default body margin insets the text by 8px on each side.
	doc = layoutDoc(t, `<html><body><p>text</p></body></html>`, 400)
	if len(doc.lines) == 0 {
		t.Fatal("no text drawn")
	}
	if x := doc.lines[0].indent; x != 8 {
		t.Errorf("text starts at x=%g, want the body's 8px margin", x)
	}
}

// max-width with auto margins centers the container in the containing block,
// and its content is measured from the container's own content box.
func TestMaxWidthAutoMarginsCenterContainer(t *testing.T) {
	doc := layoutDoc(t, `<html><body style="margin:0;background:#fff"><style>
		* { box-sizing: border-box }
		.wrap { max-width: 1200px; margin: 0 auto; padding: 0 40px; background: #eee }
	</style><div class="wrap"><p>hello</p></div></body></html>`, 1280)
	if len(doc.boxes) != 1 {
		t.Fatalf("boxes = %d, want just the wrap: %+v", len(doc.boxes), doc.boxes)
	}
	wrap := doc.boxes[0]
	// (1280 - 1200) / 2 = 40 of slack on each side.
	if wrap.x != 40 || wrap.w != 1200 {
		t.Errorf("wrap box x=%g w=%g, want x=40 w=1200", wrap.x, wrap.w)
	}
	if len(doc.lines) == 0 {
		t.Fatal("no text drawn")
	}
	// The text starts at the wrap's content edge: 40 + 40 of padding.
	if x := doc.lines[0].indent; x != 80 {
		t.Errorf("text x=%g, want 80 (the wrap's content edge)", x)
	}
}

// box-sizing and auto margins on a sized block, checked against the geometry
// Chromium reports for the same page.
func TestBoxSizingAndAutoMargins(t *testing.T) {
	doc := layoutDoc(t, `<html><body style="margin:0;background:#fff"><style>
		* { box-sizing: border-box }
		.a { width: 300px; padding: 20px; background: #eee }
		.b { width: 300px; padding: 20px; box-sizing: content-box; background: #ddd }
		.c { max-width: 400px; margin: 0 auto; padding: 0 30px; background: #ccc }
	</style>
	<div class="a">a</div><div class="b">b</div><div class="c"><p>c</p></div>
	</body></html>`, 1280)
	if len(doc.boxes) != 3 {
		t.Fatalf("boxes = %d, want 3", len(doc.boxes))
	}
	// border-box: 300 wide including its padding.
	if a := doc.boxes[0]; a.w != 300 {
		t.Errorf("border-box block w=%g, want 300", a.w)
	}
	// content-box: 300 of content plus 20 of padding on each side.
	if b := doc.boxes[1]; b.w != 340 {
		t.Errorf("content-box block w=%g, want 340", b.w)
	}
	// max-width:400 with auto margins: centered in the 1280 viewport.
	c := doc.boxes[2]
	if c.x != 440 || c.w != 400 {
		t.Errorf("centered block x=%g w=%g, want x=440 w=400", c.x, c.w)
	}
}

// auto overrides an inherited margin length. The default body margin is 8px on
// every side, so "margin: 0 auto" has to clear the right margin as well as the
// left; otherwise the centering slack is measured 8px short.
func TestAutoMarginClearsDefault(t *testing.T) {
	// example.com sizes its body to 60vw and centers it with auto margins.
	// Chromium puts that body at x = (1280 - 768) / 2 = 256.
	doc := layoutDoc(t, `<html><body style="width:60vw;margin:15vh auto">`+
		`<div><h1>Example Domain</h1></div></body></html>`, 1280)
	if len(doc.lines) == 0 {
		t.Fatal("no text drawn")
	}
	if x := doc.lines[0].indent; x != 256 {
		t.Errorf("centered body text x=%g, want 256", x)
	}
}

// An unset body margin is 8px on all four sides, so a block fills the body's
// 384px of content between the two margins, as in a browser.
func TestBodyDefaultMarginInsertsBothEdges(t *testing.T) {
	doc := layoutDoc(t, `<html><body style="background:#fff">`+
		`<div style="background:#eee">one two</div></body></html>`, 400)
	if len(doc.boxes) != 1 {
		t.Fatalf("boxes = %d, want 1: %+v", len(doc.boxes), doc.boxes)
	}
	if b := doc.boxes[0]; b.x != 8 || b.w != 384 {
		t.Errorf("block x=%g w=%g, want x=8 w=384 (the body's content box)", b.x, b.w)
	}
	if len(doc.lines) == 0 {
		t.Fatal("no text drawn")
	}
	if x := doc.lines[0].indent; x != 8 {
		t.Errorf("text x=%g, want 8 (the body's default left margin)", x)
	}
}

// A padded wrapper that declares no width still insets what it contains: the
// text inside wraps against the wrapper's content box, not the page. Chromium
// reports the inner block at x=40 w=320 for this page.
func TestPaddedWrapperInsetsContent(t *testing.T) {
	doc := layoutDoc(t, `<html><body style="margin:0">`+
		`<div style="padding:0 40px;background:#eee">`+
		`<div style="background:#ddd">one two three four</div></div></body></html>`, 400)
	if len(doc.boxes) == 0 {
		t.Fatal("no boxes drawn")
	}
	// The inner block starts at the wrapper's content edge and ends 40px short
	// of the page, where the wrapper's right padding begins.
	if inner := doc.boxes[len(doc.boxes)-1]; inner.x != 40 || inner.w != 320 {
		t.Errorf("inner block x=%g w=%g, want x=40 w=320", inner.x, inner.w)
	}
}

// Text-align moves the runs inside the block's content box: left keeps them at
// the content edge, center splits the slack, right fills it.
func TestTextAlignPositionsRuns(t *testing.T) {
	const width = 400
	bounds := func(align string) (int, int) {
		img := screenshotOf(t, `<html><body style="margin:0;background:#fff">`+
			`<div style="text-align:`+align+`;color:#ff0000">MMMMMM</div></body></html>`,
			ScreenshotOptions{Width: width, NoImages: true})
		minX, _, maxX, _, ok := redBounds(img)
		if !ok {
			t.Fatalf("text-align:%s drew nothing", align)
		}
		return minX, maxX
	}
	lmin, lmax := bounds("left")
	cmin, cmax := bounds("center")
	rmin, rmax := bounds("right")
	if lmin > 8 {
		t.Errorf("left-aligned text starts at %d, want near the content edge", lmin)
	}
	// The center of the centered run sits near the middle of the page.
	if c := (cmin + cmax) / 2; c < width/2-12 || c > width/2+12 {
		t.Errorf("centered run midpoint %d, want near %d", c, width/2)
	}
	if rmax < width-40 {
		t.Errorf("right-aligned text ends at %d, want near %d", rmax, width)
	}
	// The three runs must not coincide: alignment has to move them.
	if lmin == cmin || cmin == rmin {
		t.Errorf("left/center/right runs did not move: %d %d %d", lmin, cmin, rmin)
	}
	if lmax > cmin+40 {
		t.Errorf("left run %d overlaps centered run %d", lmax, cmin)
	}
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

// Overflow in a flex row is shared out in proportion to flex-shrink times each
// item's own size, and flex-shrink: 0 keeps an item's width outright. The
// expected geometry is Chromium's getBoundingClientRect for the same page.
func TestFlexShrinkSharesOverflow(t *testing.T) {
	doc := layoutDoc(t, `<!doctype html><html><head><style>
		* { margin: 0; padding: 0 }
		.row { display: flex; width: 300px }
		.item { width: 200px; height: 10px; background: #ccc }
		.keep { flex: none; width: 200px; height: 10px; background: #ddd }
	</style></head><body>
		<div class="row"><div class="item"></div><div class="item"></div></div>
		<div class="row"><div class="item"></div><div class="keep"></div></div>
	</body></html>`, 400)
	want := []struct{ x, y, w float64 }{
		{0, 0, 150}, {150, 0, 150}, // 400px of items in a 300px row
		{0, 10, 100}, {100, 10, 200}, // the item gives way, the flex:none one does not
	}
	if len(doc.boxes) != len(want) {
		t.Fatalf("got %d boxes, want %d: %+v", len(doc.boxes), len(want), doc.boxes)
	}
	for i, w := range want {
		got := doc.boxes[i]
		if diff(got.x, w.x) > 0.5 || diff(got.y, w.y) > 0.5 || diff(got.w, w.w) > 0.5 {
			t.Errorf("box %d: got x=%g y=%g w=%g, want x=%g y=%g w=%g",
				i, got.x, got.y, got.w, w.x, w.y, w.w)
		}
	}
}

// A declared height holds for a flex row as well as for a block. A page header
// is usually a flex row with a fixed height, and everything below it starts
// after that height. The expected y is also Chromium's for the same page.
func TestFlexRowKeepsDeclaredHeight(t *testing.T) {
	doc := layoutDoc(t, `<!doctype html><html><head><style>
		* { margin: 0; padding: 0 }
		.nav { display: flex; height: 56px; background: #eee }
		.item { width: 40px; height: 20px; background: #ccc }
	</style></head><body>
		<div class="nav"><div class="item"></div></div>
		<p id="after">after</p>
	</body></html>`, 400)
	if len(doc.lines) != 1 {
		t.Fatalf("got %d lines, want just the paragraph: %+v", len(doc.lines), doc.lines)
	}
	if y := doc.lines[0].y; diff(y, 56) > 0.5 {
		t.Errorf("the paragraph is at y=%g, want 56 below the header", y)
	}
	if len(doc.boxes) != 2 {
		t.Fatalf("got %d boxes, want the row and its item: %+v", len(doc.boxes), doc.boxes)
	}
	row := false
	for _, box := range doc.boxes {
		if diff(box.w, 400) < 0.5 && diff(box.h, 56) < 0.5 {
			row = true
		}
	}
	if !row {
		t.Errorf("no 400x56 row box: %+v", doc.boxes)
	}
}

// A list that is a flex container lays its items out in a row, not down the
// page. go.dev's header menu is <ul style="display:flex">, and rendering it as
// a list stacks the entries and pushes the whole page down.
func TestFlexListLaysItemsInARow(t *testing.T) {
	doc := layoutDoc(t, `<!doctype html><html><head><style>
		* { margin: 0; padding: 0 }
		ul { display: flex }
		li { flex: none; width: 50px; height: 10px; background: #ccc; list-style: none }
	</style></head><body>
		<ul><li></li><li></li><li></li></ul>
	</body></html>`, 400)
	want := []struct{ x, y, w float64 }{
		{0, 0, 50}, {50, 0, 50}, {100, 0, 50},
	}
	if len(doc.boxes) != len(want) {
		t.Fatalf("got %d boxes, want %d: %+v", len(doc.boxes), len(want), doc.boxes)
	}
	for i, w := range want {
		got := doc.boxes[i]
		if diff(got.x, w.x) > 0.5 || diff(got.y, w.y) > 0.5 || diff(got.w, w.w) > 0.5 {
			t.Errorf("box %d: got x=%g y=%g w=%g, want x=%g y=%g w=%g",
				i, got.x, got.y, got.w, w.x, w.y, w.w)
		}
	}
}

// "flex: none" is "0 0 auto": it cancels an earlier flex-basis:0 and lets the
// item's width size it. A 2/3 column whose width was dropped collapsed to a
// sliver and wrapped to a few characters per line.
func TestFlexNoneKeepsWidth(t *testing.T) {
	p := flexPage(t, `<html><head><style>
		.cols { display: flex }
		.col { flex-basis: 0; flex-grow: 1 }
		.col.wide { flex: none; width: 60% }
	</style></head><body style="margin:0">
		<div class="cols"><div class="col wide">alpha bravo charlie delta echo foxtrot</div></div>
	</body></html>`)
	const phrase = "alpha bravo charlie delta echo foxtrot"
	pos := outlinePositions(t, p, 800)
	if _, ok := pos[phrase]; !ok {
		t.Fatalf("the column did not keep its width; outline: %v", pos)
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

// A button draws its background, border and radius like any box, even though it
// is display:inline-block.
func TestButtonDrawsItsBox(t *testing.T) {
	img := screenshotOf(t, `<html><body style="margin:0;background:#000">
		<button style="border:3px solid #ff0000;border-radius:8px;background:#000;padding:6px 12px">Go</button>
		</body></html>`, ScreenshotOptions{Width: 300, NoImages: true})
	if _, _, _, _, ok := redBounds(img); !ok {
		t.Error("the button border was not drawn")
	}
}

// An absolutely positioned child is placed relative to its positioned ancestor.
func TestAbsolutePositioning(t *testing.T) {
	img := screenshotOf(t, `<html><body style="margin:0;background:#000">
		<div style="position:relative;height:80px;background:#111">
			<div style="position:absolute;left:20px;top:10px;width:30px;height:30px;background:#ff0000"></div>
			<div style="position:absolute;right:10px;top:40px;width:20px;height:20px;background:#00ff00"></div>
		</div></body></html>`, ScreenshotOptions{Width: 400, NoImages: true})
	minX, minY, maxX, maxY, ok := redBounds(img)
	if !ok {
		t.Fatal("absolute child not drawn")
	}
	if minX < 18 || minX > 24 {
		t.Errorf("absolute left = %d, want about 20", minX)
	}
	if w := maxX - minX; w < 28 || w > 32 {
		t.Errorf("absolute width = %d, want 30", w)
	}
	// top: the relative parent's content top (0 here) + 10. The renderer no
	// longer adds a page-wide top inset, matching a browser.
	if minY < 6 || minY > 14 {
		t.Errorf("absolute top = %d, want about 10", minY)
	}
	_ = maxY
	if _, _, _, _, ok := redBounds(img); !ok {
		t.Error("no red")
	}
	// green anchored to the right edge
	gminX, _, gmaxX, _, gok := greenBounds(img)
	if !gok {
		t.Fatal("right-anchored child not drawn")
	}
	_ = gminX
	// right:10 in a 400px-wide container: the child ends 10px from the edge.
	if gmaxX < 386 || gmaxX > 394 {
		t.Errorf("right-anchored child ends at %d, want about 390", gmaxX)
	}
}

func greenBounds(img image.Image) (minX, minY, maxX, maxY int, ok bool) {
	b := img.Bounds()
	minX, minY, maxX, maxY = b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if g>>8 > 0xd0 && r>>8 < 0x40 && bl>>8 < 0x40 {
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

// A unitless line-height inherits as its factor, not as the length it computed
// to in the parent, so it resolves against each descendant's own font size:
// body{line-height:1.6} gives a 12px control 19.2px lines, not the body's
// 25.6px, which is what Chromium lays out.
func TestUnitlessLineHeightInheritsAsFactor(t *testing.T) {
	doc := layoutDoc(t, `<html><head><style>
		* { margin: 0 }
		body { font-size: 16px; line-height: 1.6 }
		.small { font-size: 12px }
	</style></head><body>
		<div class="small">Website URL</div>
		<div>Body text</div>
	</body></html>`, 400)
	if len(doc.lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(doc.lines))
	}
	if h := doc.lines[0].height; h < 19.1 || h > 19.3 {
		t.Errorf("12px child line height %g, want 19.2", h)
	}
	if h := doc.lines[1].height; h < 25.5 || h > 25.7 {
		t.Errorf("16px child line height %g, want 25.6", h)
	}
}

// A padded, bordered block still spans the whole column: padding and border
// come out of the content box, they are not an extra inset. The border also
// pushes the text in and the next block down.
func TestPaddedBorderedBlockSpansTheColumn(t *testing.T) {
	doc := layoutDoc(t, `<html><head><style>
		* { margin: 0 }
		body { font-size: 12px; line-height: 1.6 }
		.tab { padding: 10px 28px; border: 1px solid #444; background: #222; color: #fff }
	</style></head><body>
		<div class="tab">Website URL</div>
		<div>Next</div>
	</body></html>`, 400)
	if len(doc.boxes) != 1 {
		t.Fatalf("got %d boxes, want 1", len(doc.boxes))
	}
	b := doc.boxes[0]
	if b.x != 0 || b.w != 400 {
		t.Errorf("tab box x=%g w=%g, want x=0 w=400", b.x, b.w)
	}
	if b.y != 0 {
		t.Errorf("tab box y=%g, want 0", b.y)
	}
	if b.h < 41.1 || b.h > 41.3 {
		t.Errorf("tab box height %g, want 41.2 (19.2 line + 20 padding + 2 border)", b.h)
	}
	if ind := doc.lines[0].indent; ind != 29 {
		t.Errorf("text indent %g, want 29 (28 padding + 1 border)", ind)
	}
	if y := doc.lines[1].y; y < 41.1 || y > 41.3 {
		t.Errorf("block after the tab starts at y=%g, want 41.2", y)
	}
}

// A container's right padding insets the content of everything inside it, even
// when the container itself produces no block of its own.
func TestWrappedTextStopsAtTheContainingBlocksPadding(t *testing.T) {
	doc := layoutDoc(t, `<html><head><style>
		* { margin: 0 }
		body { font-size: 16px; line-height: 1.6 }
		.outer { padding-right: 60px; background: #eee }
		.inner { background: #fff }
	</style></head><body>
		<div class="outer"><div class="inner">one two three four five six seven eight nine ten eleven twelve</div></div>
	</body></html>`, 400)
	var inner *drawBox
	for i := range doc.boxes {
		if doc.boxes[i].w == 340 {
			inner = &doc.boxes[i]
		}
	}
	if inner == nil {
		t.Fatalf("no 340px box for the inner element: %+v", doc.boxes)
	}
	if len(doc.lines) != 2 {
		t.Errorf("got %d lines, want 2 (Chromium wraps this text into 2 lines at 340px)", len(doc.lines))
	}
}

// A grid container places its children in the tracks of
// grid-template-columns, left to right, starting a new row when the tracks run
// out. The expected geometry is Chromium's getBoundingClientRect for the same
// page at a 400px viewport.
func TestGridPlacesChildrenInTracks(t *testing.T) {
	doc := layoutDoc(t, `<!doctype html><html><head><style>
		* { margin: 0; padding: 0 }
		body { font: 12px/1.6 monospace }
		.g { display: grid; grid-template-columns: repeat(3, 100px); gap: 10px }
		.c { background: #ccc }
		.af { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 10px }
		.af .c { background: #ddd }
		.span { display: grid; grid-template-columns: 1fr 1fr; gap: 10px }
		.wide { grid-column: 1 / -1; background: #eee }
		.col { background: #ddd }
	</style></head><body>
		<div class="g"><div class="c">a</div><div class="c">b</div><div class="c">c</div><div class="c">d</div></div>
		<div class="af"><div class="c">one</div><div class="c">two</div><div class="c">three</div></div>
		<div class="span"><div class="wide">whole row</div><div class="col">left</div><div class="col">right</div></div>
	</body></html>`, 400)
	want := []struct{ x, y, w float64 }{
		{0, 0, 100}, {110, 0, 100}, {220, 0, 100}, // three fixed tracks
		{0, 29.2, 100},                   // wrapped to a second row
		{0, 48.4, 195}, {205, 48.4, 195}, // repeat(auto-fill, minmax(150px,1fr))
		{0, 77.6, 195},                 // third item wraps
		{0, 96.8, 400},                 // grid-column: 1 / -1 spans the row
		{0, 126, 195}, {205, 126, 195}, // the row after the spanning item
	}
	if len(doc.boxes) != len(want) {
		t.Fatalf("got %d boxes, want %d: %+v", len(doc.boxes), len(want), doc.boxes)
	}
	for i, w := range want {
		got := doc.boxes[i]
		if diff(got.x, w.x) > 0.5 || diff(got.y, w.y) > 0.5 || diff(got.w, w.w) > 0.5 {
			t.Errorf("box %d: got x=%g y=%g w=%g, want x=%g y=%g w=%g",
				i, got.x, got.y, got.w, w.x, w.y, w.w)
		}
	}
}

// A "grid-column: main-content" picks the line named main-content, which a
// template reaches through the -start/-end line names, and runs to the next
// line with that name: the item fills the whole main-content area. The tracks
// themselves can be written as calc() over a custom property, which is how a
// "bleed" layout centres a fixed-width content column in a wide viewport.
// The expected geometry is Chromium's getBoundingClientRect for the same page
// at a 400px viewport.
func TestGridNamedLinesSpanTheContentArea(t *testing.T) {
	doc := layoutDoc(t, `<!doctype html><html><head><style>
		* { margin: 0; padding: 0 }
		body { font: 12px/1.6 monospace }
		.g { display: grid;
			--bleed: minmax(0, calc((100% - 300px) / 2));
			grid-template-columns: var(--bleed) [main-content-start] minmax(0,1fr) [article-start] minmax(0,120px) [article-end] minmax(0,1fr) [main-content-end] var(--bleed);
			column-gap: 10px }
		.g > * { grid-column: main-content; background: #eee }
		.wide { grid-column: 1 / -1; background: #ddd }
	</style></head><body>
		<div class="g"><div class="wide">bleed</div><div>one</div><div>two</div></div>
	</body></html>`, 400)
	want := []struct{ x, y, w float64 }{
		{0, 0, 400},     // grid-column: 1 / -1 spans every track
		{60, 19.2, 280}, // 50px bleed + 10px gap, main-content area
		{60, 38.4, 280}, // the next row of the same area
	}
	if len(doc.boxes) != len(want) {
		t.Fatalf("got %d boxes, want %d: %+v", len(doc.boxes), len(want), doc.boxes)
	}
	for i, w := range want {
		got := doc.boxes[i]
		if diff(got.x, w.x) > 0.5 || diff(got.y, w.y) > 0.5 || diff(got.w, w.w) > 0.5 {
			t.Errorf("box %d: got x=%g y=%g w=%g, want x=%g y=%g w=%g",
				i, got.x, got.y, got.w, w.x, w.y, w.w)
		}
	}
}

// A track whose size cannot be resolved must not collapse to nothing: a
// zero-width column wraps every word to a single character, which turns a
// 7000px page into a 40000px one. "minmax(0, max-content)" is not a sizing we
// can compute, so the track takes a share of the leftover space instead.
func TestGridUnresolvableTrackKeepsContentReadable(t *testing.T) {
	doc := layoutDoc(t, `<!doctype html><html><head><style>
		* { margin: 0; padding: 0 }
		body { font: 12px/1.6 monospace }
		.g { display: grid; grid-template-columns: minmax(0, max-content) 200px }
		.a { background: #ccc }
		.b { background: #ddd }
	</style></head><body>
		<div class="g"><div class="a">the quick brown fox</div><div class="b">two</div></div>
	</body></html>`, 400)
	if len(doc.boxes) != 2 {
		t.Fatalf("got %d boxes, want 2: %+v", len(doc.boxes), doc.boxes)
	}
	if w := doc.boxes[0].w; w < 100 {
		t.Errorf("unresolvable track is %g wide, want a share of the leftover space", w)
	}
}

// A floated box leaves the flow: it sits against one edge of its container and
// the text after it wraps beside it, then fills the column again once the
// float's bottom passes. The expected geometry is Chromium's
// getBoundingClientRect and per-line range rects for the same page at 400px.
func TestFloatWrapsTextBesideIt(t *testing.T) {
	doc := layoutDoc(t, `<!doctype html><html><head><style>
		* { margin: 0; padding: 0 }
		body { font: 16px/20px monospace; width: 400px }
		.f { float: right; width: 100px; height: 60px; background: #ccc }
		.g { float: left; width: 50px; height: 40px; background: #ddd }
	</style></head><body>
		<div class="f"></div><div class="g"></div>
		<p>aaaa bbbb cccc dddd eeee ffff gggg hhhh iiii jjjj kkkk llll mmmm nnnn oooo pppp qqqq rrrr</p>
	</body></html>`, 400)
	want := []struct{ x, y, w, h float64 }{
		{300, 0, 100, 60}, // the right float
		{0, 0, 50, 40},    // the left float
	}
	if len(doc.boxes) != len(want) {
		t.Fatalf("got %d boxes, want %d: %+v", len(doc.boxes), len(want), doc.boxes)
	}
	for i, w := range want {
		got := doc.boxes[i]
		if diff(got.x, w.x) > 0.5 || diff(got.y, w.y) > 0.5 ||
			diff(got.w, w.w) > 0.5 || diff(got.h, w.h) > 0.5 {
			t.Errorf("box %d: got x=%g y=%g w=%g h=%g, want x=%g y=%g w=%g h=%g",
				i, got.x, got.y, got.w, got.h, w.x, w.y, w.w, w.h)
		}
	}
	if len(doc.lines) != 4 {
		t.Fatalf("got %d lines, want 4: %+v", len(doc.lines), doc.lines)
	}
	// The first two lines clear the left float and stop at the right one, the
	// third clears the left float only, and the last is beside neither.
	for _, c := range []struct {
		line int
		y    float64
		x    float64
		end  float64
	}{
		{0, 0, 50, 290},
		{1, 20, 50, 290},
		{2, 40, 0, 240},
		{3, 60, 0, 140},
	} {
		l := doc.lines[c.line]
		if diff(l.y, c.y) > 0.5 {
			t.Errorf("line %d is at y=%g, want %g", c.line, l.y, c.y)
			continue
		}
		if diff(l.runs[0].x, c.x) > 0.5 {
			t.Errorf("line %d starts at x=%g, want %g", c.line, l.runs[0].x, c.x)
		}
		end := 0.0
		for _, r := range l.runs {
			if w := r.x + textWidth(r.style, r.text); w > end {
				end = w
			}
		}
		if diff(end, c.end) > 0.5 {
			t.Errorf("line %d ends at x=%g, want %g", c.line, end, c.end)
		}
	}
}

// A float inside a flex item has to contribute its own width to the row: the
// float itself has no spans, so measuring it as an empty block gave the item
// no width at all and the float collapsed to nothing.
func TestFloatInsideFlexItemKeepsItsWidth(t *testing.T) {
	doc := layoutDoc(t, `<!doctype html><html><head><style>
		* { margin: 0; padding: 0 }
		body { font: 16px/20px monospace }
		.row { display: flex; width: 400px }
		.f { float: right; width: 120px; height: 40px; background: #ccc }
		.t { background: #ddd }
	</style></head><body>
		<div class="row"><div><div class="f"></div><span class="t">text</span></div></div>
	</body></html>`, 400)
	found := false
	for _, b := range doc.boxes {
		if diff(b.w, 120) < 0.5 && diff(b.h, 40) < 0.5 && b.x > 30 {
			found = true
		}
	}
	if !found {
		t.Errorf("the float is not a 120x40 box pushed right by the text beside it: %+v", doc.boxes)
	}
}

// The translation part of "transform" moves a positioned element from where it
// would have been. A percentage is a fraction of the element's own box, which
// is how a drawer is parked beside the page instead of over it: go.dev's
// navigation drawer is a fixed panel at translateX(100%), and without the
// transform its links are drawn across the article. The expected geometry is
// Chromium's getBoundingClientRect for the same page at 400px.
func TestTransformTranslatesPositionedElements(t *testing.T) {
	doc := layoutDoc(t, `<!doctype html><html><head><style>
		* { margin: 0; padding: 0 }
		body { font: 16px/20px monospace }
		.drawer { position: fixed; top: 0; right: 0; width: 200px; height: 100px;
			transform: translateX(100%); background: #ccc }
		.shifted { position: absolute; top: 50px; left: 10px; transform: translate(5px, 5px) }
	</style></head><body>
		<div class="drawer">drawer</div>
		<div class="shifted">shifted</div>
		<p>page text</p>
	</body></html>`, 400)
	if len(doc.boxes) != 1 {
		t.Fatalf("got %d boxes, want the drawer: %+v", len(doc.boxes), doc.boxes)
	}
	if b := doc.boxes[0]; diff(b.x, 400) > 0.5 || diff(b.y, 0) > 0.5 ||
		diff(b.w, 200) > 0.5 || diff(b.h, 100) > 0.5 {
		t.Errorf("drawer box = x=%g y=%g w=%g h=%g, want x=400 y=0 w=200 h=100", b.x, b.y, b.w, b.h)
	}
	at := map[string]drawLine{}
	for _, l := range doc.lines {
		if len(l.runs) > 0 {
			at[l.runs[0].text] = l
		}
	}
	if l, ok := at["drawer"]; !ok || diff(l.runs[0].x, 400) > 0.5 {
		t.Errorf("the drawer text is at %+v, want x=400, off the page", l)
	}
	if l, ok := at["shifted"]; !ok || diff(l.runs[0].x, 15) > 0.5 || diff(l.y, 55) > 0.5 {
		t.Errorf("the translated block is at %+v, want x=15 y=55", l)
	}
}

// Five "width: 20%" columns fit one line in a flex-wrap row, and the 100%
// row that follows wraps below them. With box-sizing: border-box the declared
// width is the whole border box: counting its padding a second time made the
// last column wrap onto its own line and stacked brave.com's whole footer down
// the page. The expected geometry is Chromium's getBoundingClientRect for the
// same page at 500px.
func TestFlexWrapKeepsPercentageColumnsOnOneLine(t *testing.T) {
	doc := layoutDoc(t, `<!doctype html><html><head><style>
		* { margin: 0; padding: 0; box-sizing: border-box }
		body { font: 12px/16px monospace }
		.row { display: flex; flex-wrap: wrap; width: 500px }
		.col { width: 20%; height: 10px; padding-right: 20px; background: #ccc }
		.bottom { width: 100%; height: 5px; background: #eee }
	</style></head><body>
		<div class="row">
			<div class="col"></div><div class="col"></div><div class="col"></div>
			<div class="col"></div><div class="col"></div><div class="bottom"></div>
		</div>
	</body></html>`, 500)
	want := []struct{ x, y, w, h float64 }{
		{0, 0, 100, 10}, {100, 0, 100, 10}, {200, 0, 100, 10},
		{300, 0, 100, 10}, {400, 0, 100, 10},
		{0, 10, 500, 5}, // the 100% row wraps below the columns
	}
	if len(doc.boxes) != len(want) {
		t.Fatalf("got %d boxes, want %d: %+v", len(doc.boxes), len(want), doc.boxes)
	}
	for i, w := range want {
		got := doc.boxes[i]
		if diff(got.x, w.x) > 0.5 || diff(got.y, w.y) > 0.5 ||
			diff(got.w, w.w) > 0.5 || diff(got.h, w.h) > 0.5 {
			t.Errorf("box %d: got x=%g y=%g w=%g h=%g, want x=%g y=%g w=%g h=%g",
				i, got.x, got.y, got.w, got.h, w.x, w.y, w.w, w.h)
		}
	}
}

// Text that is a direct child of a flex row has no element of its own to carry
// the line height, so it inherits the row's. go.dev's footer is built from
// "display: flex" links with "line-height: 2rem": 32px apart, not 17. The
// expected y values are Chromium's for the same page.
func TestFlexContainerLineHeightReachesItsText(t *testing.T) {
	doc := layoutDoc(t, `<!doctype html><html><head><style>
		* { margin: 0; padding: 0 }
		body { font: 16px/20px monospace }
		a { display: flex; font-size: 14px; line-height: 32px; color: #000 }
	</style></head><body>
		<a href="#">Why Go</a><a href="#">Use Cases</a>
	</body></html>`, 400)
	want := []float64{0, 32}
	if len(doc.lines) != len(want) {
		t.Fatalf("got %d lines, want %d: %+v", len(doc.lines), len(want), doc.lines)
	}
	for i, y := range want {
		if diff(doc.lines[i].y, y) > 0.5 || diff(doc.lines[i].height, 32) > 0.5 {
			t.Errorf("line %d: y=%g h=%g, want y=%g h=32",
				i, doc.lines[i].y, doc.lines[i].height, y)
		}
	}
}

// Floats stack along the side they are floated to, and one that does not fit
// beside them drops below them. All of Wikipedia's footer links are left
// floats, so placing each at the column's left edge hides all but the last.
// The expected geometry is Chromium's getBoundingClientRect for the same page.
func TestFloatsStackAlongTheirSide(t *testing.T) {
	doc := layoutDoc(t, `<!doctype html><html><head><style>
		* { margin: 0; padding: 0 }
		body { font: 16px/20px monospace; width: 400px }
		.f { float: left; width: 100px; height: 30px; background: #ccc; margin: 0 5px 0 0 }
		.g { float: left; width: 220px; height: 30px; background: #ddd }
	</style></head><body>
		<div class="f"></div><div class="f"></div><div class="f"></div><div class="g"></div>
	</body></html>`, 400)
	want := []struct{ x, y, w, h float64 }{
		{0, 0, 100, 30}, {105, 0, 100, 30}, {210, 0, 100, 30},
		{0, 30, 220, 30}, // 220 more does not fit beside the third
	}
	if len(doc.boxes) != len(want) {
		t.Fatalf("got %d boxes, want %d: %+v", len(doc.boxes), len(want), doc.boxes)
	}
	for i, w := range want {
		got := doc.boxes[i]
		if diff(got.x, w.x) > 0.5 || diff(got.y, w.y) > 0.5 ||
			diff(got.w, w.w) > 0.5 || diff(got.h, w.h) > 0.5 {
			t.Errorf("box %d: got x=%g y=%g w=%g h=%g, want x=%g y=%g w=%g h=%g",
				i, got.x, got.y, got.w, got.h, w.x, w.y, w.w, w.h)
		}
	}
}

// grid-template's area map places items by name, and a minifier splits the
// declaration into "grid-template: <rows> / <columns>" plus a separate
// "grid-template-areas" with single-quoted strings. Wikipedia's desktop skin
// is built from this: the article column, the left menu and the page tools are
// grid areas, and reading only grid-template-columns stacks them. The expected
// geometry is Chromium's getBoundingClientRect for the same page.
func TestGridTemplateAreasPlaceItems(t *testing.T) {
	doc := layoutDoc(t, `<!doctype html><html><head><style>
		* { margin: 0; padding: 0 }
		body { font: 16px/20px monospace }
		.g { display: grid; grid-template: min-content 1fr / 196px minmax(0,1fr); column-gap: 24px;
			grid-template-areas: 'top top' 'side main'; width: 600px }
		.top { grid-area: top; background: #ccc; height: 20px }
		.side { grid-area: side; background: #ddd; height: 30px }
		.main { grid-area: main; background: #eee; height: 40px }
	</style></head><body>
		<div class="g"><div class="side"></div><div class="main"></div><div class="top"></div></div>
	</body></html>`, 600)
	// The DOM order is side, main, top; the areas put top first, spanning.
	want := []struct{ x, y, w, h float64 }{
		{0, 0, 600, 20},
		{0, 20, 196, 30},
		{220, 20, 380, 40},
	}
	if len(doc.boxes) != len(want) {
		t.Fatalf("got %d boxes, want %d: %+v", len(doc.boxes), len(want), doc.boxes)
	}
	for i, w := range want {
		got := doc.boxes[i]
		if diff(got.x, w.x) > 0.5 || diff(got.y, w.y) > 0.5 ||
			diff(got.w, w.w) > 0.5 || diff(got.h, w.h) > 0.5 {
			t.Errorf("box %d: got x=%g y=%g w=%g h=%g, want x=%g y=%g w=%g h=%g",
				i, got.x, got.y, got.w, got.h, w.x, w.y, w.w, w.h)
		}
	}
}

// An area name is an identifier and keeps its case. Lowercasing it left
// Wikipedia's "grid-area: pageContent" unmatched against the template's
// "pageContent", so the article column fell back to auto placement.
func TestGridAreaNameIsCaseSensitive(t *testing.T) {
	doc := layoutDoc(t, `<!doctype html><html><head><style>
		* { margin: 0; padding: 0 }
		body { font: 16px/20px monospace }
		.g { display: grid; grid-template: 40px 40px / 100px 100px; width: 200px;
			grid-template-areas: 'pageContent columnEnd' 'footer footer' }
		.main { grid-area: pageContent; background: #ccc }
	</style></head><body>
		<div class="g"><div class="main"></div></div>
	</body></html>`, 400)
	if len(doc.boxes) != 1 {
		t.Fatalf("got %d boxes, want 1: %+v", len(doc.boxes), doc.boxes)
	}
	if b := doc.boxes[0]; diff(b.x, 0) > 0.5 || diff(b.y, 0) > 0.5 || diff(b.w, 100) > 0.5 {
		t.Errorf("the item is at x=%g y=%g w=%g, want the pageContent area at 0,0 100 wide", b.x, b.y, b.w)
	}
}

// A table lays its rows out as cells in shared columns: the column widths come
// from the widest cell in each column, scaled to the table's declared width.
// The expected geometry is Chromium's getBoundingClientRect for the same page
// at a 400px viewport.
func TestTableSharesColumnsBetweenRows(t *testing.T) {
	doc := layoutDoc(t, `<!doctype html><html><head><style>
		* { margin: 0; padding: 0 }
		body { font: 12px/1.6 monospace }
		td { background: #ddd }
		table { width: 300px }
	</style></head><body>
		<table cellspacing="0">
			<tr><td>one</td><td>two two</td></tr>
			<tr><td>three three</td><td>four</td></tr>
			<tr><td colspan="2">spans both</td></tr>
		</table>
	</body></html>`, 400)
	if len(doc.boxes) != 5 {
		t.Fatalf("got %d boxes, want 5: %+v", len(doc.boxes), doc.boxes)
	}
	// Cells of both rows share the same two columns.
	want := []struct{ x, y, w float64 }{
		{0, 0, 183.3}, {183.3, 0, 116.7},
		{0, 19.2, 183.3}, {183.3, 19.2, 116.7},
		{0, 38.4, 300}, // the colspan cell spans the whole table
	}
	for i, w := range want {
		got := doc.boxes[i]
		if diff(got.x, w.x) > 0.5 || diff(got.y, w.y) > 0.5 || diff(got.w, w.w) > 0.5 {
			t.Errorf("box %d: got x=%g y=%g w=%g, want x=%g y=%g w=%g",
				i, got.x, got.y, got.w, w.x, w.y, w.w)
		}
	}
}

// A flex row is sized and centered like any other block: max-width shrinks it
// and "margin: 0 auto" centers it, so its items share the row's width and not
// the whole column. The expected geometry is Chromium's
// getBoundingClientRect for the same page at a 400px viewport.
func TestFlexRowHonoursItsOwnWidthAndAutoMargins(t *testing.T) {
	doc := layoutDoc(t, `<!doctype html><html><head><style>
		* { margin: 0; padding: 0 }
		body { font: 12px/1.6 monospace }
		.row { display: flex; gap: 22px; max-width: 300px; margin: 0 auto }
		.item { background: #ddd; flex: 1 }
	</style></head><body>
		<div class="row"><div class="item">one</div><div class="item">two</div></div>
	</body></html>`, 400)
	if len(doc.boxes) != 2 {
		t.Fatalf("got %d boxes, want 2: %+v", len(doc.boxes), doc.boxes)
	}
	want := []struct{ x, w float64 }{{50, 139}, {211, 139}}
	for i, w := range want {
		got := doc.boxes[i]
		if diff(got.x, w.x) > 0.5 || diff(got.w, w.w) > 0.5 {
			t.Errorf("item %d: got x=%g w=%g, want x=%g w=%g", i, got.x, got.w, w.x, w.w)
		}
	}
}
