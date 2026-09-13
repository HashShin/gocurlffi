package browser

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
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
