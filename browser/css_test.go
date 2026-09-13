package browser

import (
	"image"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// getComputedStyle must report the cascaded value, not the tag default.
func TestCSSFromStylesheetApplies(t *testing.T) {
	var cssHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/style.css" {
			atomic.AddInt32(&cssHits, 1)
			w.Header().Set("Content-Type", "text/css")
			_, _ = w.Write([]byte(`
				body { background-color: #f0f0f0; }
				p { color: #111111; font-size: 20px; }
				.quote { color: #c0392b; font-style: italic; }
				#big { font-size: 40px; }
			`))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><link rel="stylesheet" href="/style.css"></head>
<body><p class="quote">quoted</p><p id="big">big</p></body></html>`))
	}))
	defer srv.Close()

	b := New(Options{})
	defer b.Close()
	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	cases := []struct{ expr, want string }{
		{`getComputedStyle(document.querySelector('.quote')).color`, "rgba(192, 57, 43, 1.000)"},
		{`getComputedStyle(document.querySelector('.quote')).fontStyle`, "italic"},
		{`getComputedStyle(document.getElementById('big')).fontSize`, "40px"},
		{`getComputedStyle(document.querySelector('.quote')).fontSize`, "20px"},
		{`getComputedStyle(document.body).backgroundColor`, "rgba(240, 240, 240, 1.000)"},
	}
	for _, c := range cases {
		v, err := p.Eval(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got := v.String(); got != c.want {
			t.Fatalf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
	if atomic.LoadInt32(&cssHits) == 0 {
		t.Fatalf("the external stylesheet was never fetched")
	}
}

func TestCSSCascadeOrder(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><head><style>
		p { color: blue; }
		.c { color: green; }
		#x { color: red; }
		.i { color: orange !important; }
	</style></head><body>
	<p class="c" id="x">a</p>
	<p class="i" id="y" style="color: purple">b</p>
	<p style="color: #123456">c</p>
	</body></html>`, "https://example.test/")

	cases := []struct{ expr, want string }{
		// class beats type, id beats class
		{`getComputedStyle(document.getElementById('x')).color`, "rgba(255, 0, 0, 1.000)"},
		// !important beats inline
		{`getComputedStyle(document.getElementById('y')).color`, "rgba(255, 165, 0, 1.000)"},
		// inline beats the stylesheet for normal declarations
		{`getComputedStyle(document.querySelectorAll('p')[2]).color`, "rgba(18, 52, 86, 1.000)"},
	}
	for _, c := range cases {
		v, err := p.Eval(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got := v.String(); got != c.want {
			t.Fatalf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// CSS must reach the renderer: display:none removes content, backgrounds draw.
func TestCSSAffectsScreenshot(t *testing.T) {
	shown := screenshotOf(t, `<html><head><style>.hide{display:none}</style></head>
<body><p>kept</p><div class="hide"><p>`+repeat("hidden ", 200)+`</p></div></body></html>`,
		ScreenshotOptions{Width: 500})

	hidden := screenshotOf(t, `<html><head><style>.hide{display:none}</style></head>
<body><p>kept</p><div><p>`+repeat("hidden ", 200)+`</p></div></body></html>`,
		ScreenshotOptions{Width: 500})

	if shown.Bounds().Dy() >= hidden.Bounds().Dy() {
		t.Fatalf("display:none did not shrink the render: %d vs %d", shown.Bounds().Dy(), hidden.Bounds().Dy())
	}

	// A background colour must actually be painted.
	withBG := screenshotOf(t, `<html><body><div style="background-color:#00ff00"><p>green</p></div></body></html>`,
		ScreenshotOptions{Width: 400})
	if !hasGreenPixel(withBG) {
		t.Fatalf("background-color was not painted")
	}
}

func TestCSSMediaQuery(t *testing.T) {
	page := `<html><head><style>
		@media (max-width: 600px) { .wide { display: none; } }
	</style></head><body>
	<div class="wide"><p>` + repeat("wrap ", 60) + `</p></div>
	<p>tail</p></body></html>`

	narrow := screenshotOf(t, page, ScreenshotOptions{Width: 500})
	wide := screenshotOf(t, page, ScreenshotOptions{Width: 900})
	if narrow.Bounds().Dy() >= wide.Bounds().Dy() {
		t.Fatalf("max-width media query not applied: narrow %d, wide %d",
			narrow.Bounds().Dy(), wide.Bounds().Dy())
	}
}

func TestCSSInheritance(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><head><style>#outer { color: #008000; font-size: 24px; }</style></head>
<body><div id="outer"><span id="inner">text</span></div></body></html>`, "https://example.test/")

	cases := []struct{ expr, want string }{
		{`getComputedStyle(document.getElementById('inner')).color`, "rgba(0, 128, 0, 1.000)"},
		{`getComputedStyle(document.getElementById('inner')).fontSize`, "24px"},
		// display is not inherited, so the span stays inline
		{`getComputedStyle(document.getElementById('inner')).display`, "inline"},
	}
	for _, c := range cases {
		v, err := p.Eval(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got := v.String(); got != c.want {
			t.Fatalf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestCSSStyleElementOnly(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	if err := p.SetContent(`<html><head><style>h1 { color: #ff0000; text-align: center }</style></head>
<body><h1>hi</h1></body></html>`, "https://example.test/"); err != nil {
		t.Fatalf("SetContent: %v", err)
	}
	v, _ := p.Eval(`getComputedStyle(document.querySelector('h1')).color`)
	if v.String() != "rgba(255, 0, 0, 1.000)" {
		t.Fatalf("inline <style> not applied: %q", v.String())
	}
	v, _ = p.Eval(`getComputedStyle(document.querySelector('h1')).textAlign`)
	if v.String() != "center" {
		t.Fatalf("text-align = %q, want center", v.String())
	}
}

func hasGreenPixel(img image.Image) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if g>>8 > 200 && r>>8 < 100 && bl>>8 < 100 {
				return true
			}
		}
	}
	return false
}

// Loading a page must not touch its stylesheets; only a style-dependent
// operation should, and then only once.
func TestCSSLoadingIsLazy(t *testing.T) {
	var cssHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/s.css" {
			atomic.AddInt32(&cssHits, 1)
			_, _ = w.Write([]byte(`p { color: #ff0000 }`))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><link rel="stylesheet" href="/s.css"></head><body><p>hi</p></body></html>`))
	}))
	defer srv.Close()

	b := New(Options{})
	defer b.Close()
	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got := atomic.LoadInt32(&cssHits); got != 0 {
		t.Fatalf("loading fetched the stylesheet %d times; it must be lazy", got)
	}
	// Extraction does not need styles either.
	_ = p.Text()
	_ = p.Markdown()
	if got := atomic.LoadInt32(&cssHits); got != 0 {
		t.Fatalf("extraction fetched the stylesheet %d times", got)
	}
	if _, err := p.Screenshot(ScreenshotOptions{Width: 400}); err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if got := atomic.LoadInt32(&cssHits); got != 1 {
		t.Fatalf("screenshot fetched the stylesheet %d times, want 1", got)
	}
	if _, err := p.Screenshot(ScreenshotOptions{Width: 400}); err != nil {
		t.Fatalf("second screenshot: %v", err)
	}
	if got := atomic.LoadInt32(&cssHits); got != 1 {
		t.Fatalf("stylesheets were refetched: %d", got)
	}
}
