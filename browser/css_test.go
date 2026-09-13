package browser

import (
	"image"
	"net/http"
	"net/http/httptest"
	"strings"
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

// Font-size resolution matches a browser: the absolute keywords come from the
// medium-keyed table (large is 18px, not 1.2x the parent), percentages and em
// resolve against the parent, and the user-agent <small> is "smaller".
func TestCSSFontSizeResolution(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><head><style>
		h1 { font-size: 2em; }
		#c { font-size: 20px; }
		#d { font-size: large; }
		#e { font-size: 150%; }
		#f { font-size: 2em; }
		#g { font-size: smaller; }
		#h { font-size: 87%; }
	</style></head><body>
		<h1 id="h1">h1</h1>
		<p id="a" style="font-size: small">a</p>
		<p id="b" style="font-size: x-large">b</p>
		<div id="c"><span id="d">d</span><span id="e">e</span><span id="f">f</span>
		<span id="g">g</span><small id="h">h</small></div>
	</body></html>`, "https://example.test/")
	// Every expectation below is what Chromium reports for the same document.
	cases := []struct{ expr, want string }{
		{`getComputedStyle(document.getElementById('a')).fontSize`, "13px"},
		{`getComputedStyle(document.getElementById('b')).fontSize`, "24px"},
		{`getComputedStyle(document.getElementById('h1')).fontSize`, "32px"},
		{`getComputedStyle(document.getElementById('d')).fontSize`, "18px"},
		{`getComputedStyle(document.getElementById('e')).fontSize`, "30px"},
		{`getComputedStyle(document.getElementById('f')).fontSize`, "40px"},
		{`getComputedStyle(document.getElementById('g')).fontSize`, "16.666666666666668px"},
		{`getComputedStyle(document.getElementById('h')).fontSize`, "17.4px"},
	}
	for _, c := range cases {
		v, err := p.Eval(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got := v.String(); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// With no colour declared anywhere, text is black, as in a browser's
// user-agent stylesheet.
func TestCSSDefaultTextColor(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body><p id="a">a</p></body></html>`, "https://example.test/")
	v, err := p.Eval(`getComputedStyle(document.getElementById('a')).color`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got := v.String(); got != "rgba(0, 0, 0, 1.000)" {
		t.Fatalf("default color = %q, want black", got)
	}
}

// document.write output belongs at the writing script's position, not at the
// end of the body, and successive writes keep their order.
func TestDocumentWriteInsertsAtScript(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<!doctype html><html><body>
		<div id="head">head</div>
		<script>
			document.write("<p id='w1'>one</p>");
			document.write("<p id='w2'>two</p>");
		</script>
		<div id="tail">tail</div>
	</body></html>`, "https://example.test/")
	out := p.Text()
	if !strings.Contains(out, "head") || !strings.Contains(out, "tail") {
		t.Fatalf("lost content: %q", out)
	}
	order := func(a, b string) bool {
		return strings.Index(out, a) >= 0 && strings.Index(out, a) < strings.Index(out, b)
	}
	if !order("head", "one") || !order("one", "two") || !order("two", "tail") {
		t.Fatalf("document.write landed in the wrong place: %q", out)
	}
	// Chromium puts the written nodes directly after the writing script:
	// div#head, script, p#w1, p#w2, div#tail.
	v, err := p.Eval(`Array.prototype.map.call(document.body.children, function(e){return e.tagName+':'+e.id}).join(',')`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	want := "DIV:head,SCRIPT:,P:w1,P:w2,DIV:tail"
	if got := v.String(); got != want {
		t.Fatalf("body children = %s, want %s", got, want)
	}
}

// Media queries are evaluated against the layout width, and a query the engine
// cannot parse must not match. Treating an unparsed query as matching applied
// every mobile rule on a desktop page: on Hacker News that gave 9pt text,
// block pagetop links and centre alignment; Chromium keeps them at 10pt.
func TestCSSMediaQueryWidths(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><head><style>
		#a { font-size: 20px; }
		@media only screen
		and (min-width : 300px)
		and (max-width : 750px) {
			#a { font-size: 9pt; }
			#b { font-size: 9pt; }
		}
		@media (min-width: 1000px) { #c { font-size: 30px; } }
		@media print { #d { font-size: 40px; } }
		@media (hover: hover) { #e { font-size: 50px; } }
	</style></head><body>
		<p id="a">a</p><p id="b">b</p><p id="c">c</p><p id="d">d</p><p id="e">e</p>
	</body></html>`, "https://example.test/")
	cases := []struct {
		width float64
		want  map[string]string
	}{
		{1280, map[string]string{"a": "20px", "b": "16px", "c": "30px", "d": "16px", "e": "16px"}},
		{700, map[string]string{"a": "12px", "b": "12px", "c": "16px", "d": "16px", "e": "16px"}},
	}
	for _, c := range cases {
		p.layoutWidth = c.width
		for id, want := range c.want {
			v, err := p.Eval(`getComputedStyle(document.getElementById('` + id + `')).fontSize`)
			if err != nil {
				t.Fatalf("width %g %s: %v", c.width, id, err)
			}
			if got := v.String(); got != want {
				t.Errorf("width %g: #%s font-size = %q, want %q", c.width, id, got, want)
			}
		}
	}
}

// A document without a doctype is in quirks mode, where tables take the medium
// font size and normal weight instead of inheriting them.
func TestCSSQuirksModeTables(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body style="font-size:10pt;font-weight:bold">
		<table id="t"><tr><td id="d">x</td></tr></table>
	</body></html>`, "https://example.test/")
	for _, id := range []string{"t", "d"} {
		v, err := p.Eval(`[getComputedStyle(document.getElementById('` + id + `')).fontSize, getComputedStyle(document.getElementById('` + id + `')).fontWeight].join(' ')`)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if got := v.String(); got != "16px 400" {
			t.Errorf("#%s = %q, want %q", id, got, "16px 400")
		}
	}
	// The same document with a doctype is standards mode and inherits.
	p2 := b.NewPage("https://example.test/")
	_ = p2.SetContent(`<!doctype html><html><body style="font-size:10pt;font-weight:bold">
		<table id="t"><tr><td id="d">x</td></tr></table>
	</body></html>`, "https://example.test/")
	v, err := p2.Eval(`[getComputedStyle(document.getElementById('d')).fontSize, getComputedStyle(document.getElementById('d')).fontWeight].join(' ')`)
	if err != nil {
		t.Fatalf("standards: %v", err)
	}
	if got := v.String(); got != "13.333333333333334px 700" {
		t.Errorf("standards mode #d = %q, want inherited 10pt bold", got)
	}
}

// Legacy presentation attributes act as low-priority author rules, which is how
// old table layouts paint themselves.
func TestCSSPresentationalHints(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body>
		<table id="t" bgcolor="#ff6600" width="100%">
			<tr><td id="d" align="right"><font id="f" color="#0000ff">x</font></td></tr>
		</table>
		<style>#d { background-color: rgb(1, 2, 3); }</style>
	</body></html>`, "https://example.test/")
	v, err := p.Eval(`getComputedStyle(document.getElementById('t')).backgroundColor`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got := v.String(); got != "rgba(255, 102, 0, 1.000)" {
		t.Errorf("bgcolor = %q, want the attribute colour", got)
	}
	v, _ = p.Eval(`getComputedStyle(document.getElementById('d')).textAlign`)
	if got := v.String(); got != "right" {
		t.Errorf("align = %q, want right", got)
	}
	v, _ = p.Eval(`getComputedStyle(document.getElementById('f')).color`)
	if got := v.String(); got != "rgba(0, 0, 255, 1.000)" {
		t.Errorf("font color = %q, want blue", got)
	}
	// An author rule outranks the hint.
	v, _ = p.Eval(`getComputedStyle(document.getElementById('d')).backgroundColor`)
	if got := v.String(); got != "rgba(1, 2, 3, 1.000)" {
		t.Errorf("author rule lost to a hint: %q", got)
	}
}

// The display value a browser computes, including blockification by float.
func TestCSSDisplayValues(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<!doctype html><html><head><style>
		tr { float: left; }
		span { position: absolute; }
	</style></head><body>
		<table id="t"><tbody id="tb"><tr id="r"><td id="d">x</td></tr></tbody></table>
		<center id="c">c</center><span id="s">s</span>
	</body></html>`, "https://example.test/")
	// tr is floated, so it blockifies to block, while the table keeps "table"
	// and its unfocused children keep their table-internal display.
	cases := map[string]string{"t": "table", "tb": "table-row-group", "d": "table-cell", "r": "block", "c": "block", "s": "block"}
	for id, want := range cases {
		v, err := p.Eval(`getComputedStyle(document.getElementById('` + id + `')).display`)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if got := v.String(); got != want {
			t.Errorf("#%s display = %q, want %q", id, got, want)
		}
	}
}

// A theme built from custom properties, which is how Wikipedia, Bootstrap 5 and
// most modern sites are written. Without var() support every such declaration
// is dropped, and the page falls back to the user-agent defaults.
func TestCSSCustomProperties(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><head><style>
		:root { --ink: #202122; --accent: #3366cc; --size: 1.25rem; }
		body { color: var(--ink, #ff0000); font-size: var(--size); }
		a { color: var(--accent); }
		#fallback { color: var(--missing, rgb(1, 2, 3)); }
		#nested { color: var(--accent, var(--ink)); }
		#undefined { color: var(--nope); }
		#own { --ink: #00ff00; color: var(--ink); }
		#shorthand { margin: var(--m, 4px 8px); }
	</style></head><body>
		<a id="a" href="#">a</a>
		<p id="fallback">f</p><p id="nested">n</p><p id="undefined">u</p>
		<p id="own">o</p><p id="shorthand">s</p>
	</body></html>`, "https://example.test/")
	cases := []struct{ expr, want string }{
		{`getComputedStyle(document.body).color`, "rgba(32, 33, 34, 1.000)"},
		{`getComputedStyle(document.body).fontSize`, "20px"},
		{`getComputedStyle(document.getElementById('a')).color`, "rgba(51, 102, 204, 1.000)"},
		{`getComputedStyle(document.getElementById('fallback')).color`, "rgba(1, 2, 3, 1.000)"},
		{`getComputedStyle(document.getElementById('nested')).color`, "rgba(51, 102, 204, 1.000)"},
		// No value and no fallback: the declaration does not apply, so the
		// element inherits from the body.
		{`getComputedStyle(document.getElementById('undefined')).color`, "rgba(32, 33, 34, 1.000)"},
		{`getComputedStyle(document.getElementById('own')).color`, "rgba(0, 255, 0, 1.000)"},
		{`getComputedStyle(document.getElementById('shorthand')).getPropertyValue('margin-top')`, "4px"},
		{`getComputedStyle(document.getElementById('shorthand')).getPropertyValue('margin-left')`, "8px"},
	}
	for _, c := range cases {
		v, err := p.Eval(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got := v.String(); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// The user-agent monospace size applies only while the font family is exactly
// the monospace keyword, and "inherit" maps a property back to its parent.
func TestCSSMonospaceSizeAndInherit(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><head><style>
		#m { font-family: monospace; }
		#list { font-family: monospace, monospace; }
		#sans { font-family: sans-serif; }
		#inh { color: inherit; font-weight: inherit; }
		th { font-weight: normal; }
	</style></head><body style="color: rgb(10, 20, 30); font-weight: bold">
		<p><code id="plain">a</code><code id="m">b</code><code id="list">c</code>
		<code id="sans">d</code><code id="inh">e</code></p>
		<table><tr><th id="th">h</th></tr></table>
	</body></html>`, "https://example.test/")
	cases := []struct{ expr, want string }{
		{`getComputedStyle(document.getElementById('plain')).fontSize`, "13px"},
		{`getComputedStyle(document.getElementById('m')).fontSize`, "13px"},
		{`getComputedStyle(document.getElementById('list')).fontSize`, "16px"},
		{`getComputedStyle(document.getElementById('sans')).fontSize`, "16px"},
		{`getComputedStyle(document.getElementById('inh')).color`, "rgba(10, 20, 30, 1.000)"},
		{`getComputedStyle(document.getElementById('inh')).fontWeight`, "700"},
		// th is bold by default, and an author rule can take that away.
		{`getComputedStyle(document.getElementById('th')).fontWeight`, "400"},
	}
	for _, c := range cases {
		v, err := p.Eval(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got := v.String(); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// A fingerprinted build asset linked from a nested page with a root-relative
// href, plus the integrity and crossorigin attributes a static site generator
// emits. The path must resolve against the origin, not against the page's
// directory, and the extra attributes must not stop the sheet being used.
func TestCSSRootRelativeHashedAssetLink(t *testing.T) {
	const asset = "/static-assets/css/main.min.25ad059cc60643b238e15af1e04f401113b2bc27f054ca6a091fe87a4be47330.css"
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == asset {
			atomic.AddInt32(&hits, 1)
			w.Header().Set("Content-Type", "text/css")
			_, _ = w.Write([]byte(`body { background-color: #102030; color: #ffffff }
				article h1 { font-size: 2em; color: #ffcc00 }
				article p { font-size: 18px }`))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><head>
			<link rel="stylesheet" href="` + asset + `" integrity="sha256-Ja0FnMYGQ7I44Vrx4E9AEROyvCfwVMpqCR/oekvkczA=" crossorigin="anonymous" />
			</head><body><article><h1>Title</h1><p>Body</p></article></body></html>`))
	}))
	defer srv.Close()

	b := New(Options{})
	defer b.Close()
	// Fetched from a nested directory, so a naive join would look for
	// /blog/post/static-assets/...
	p, err := b.Open(srv.URL + "/blog/post/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	cases := []struct{ expr, want string }{
		{`getComputedStyle(document.body).backgroundColor`, "rgba(16, 32, 48, 1.000)"},
		{`getComputedStyle(document.body).color`, "rgba(255, 255, 255, 1.000)"},
		{`getComputedStyle(document.querySelector('h1')).fontSize`, "32px"},
		{`getComputedStyle(document.querySelector('h1')).color`, "rgba(255, 204, 0, 1.000)"},
		{`getComputedStyle(document.querySelector('p')).fontSize`, "18px"},
		{`document.styleSheets.length`, "1"},
	}
	for _, c := range cases {
		v, err := p.Eval(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got := v.String(); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("the stylesheet was fetched %d times, want 1", got)
	}
}

// document.styleSheets reports the sheets the page declared, with their rules,
// so a page can check its own CSS. Reading an href must not fetch anything.
func TestCSSStyleSheetsObject(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a.css" {
			atomic.AddInt32(&hits, 1)
			w.Header().Set("Content-Type", "text/css")
			_, _ = w.Write([]byte("p { color: #123456; }\n.lead { font-size: 20px !important; }"))
			return
		}
		_, _ = w.Write([]byte(`<html><head>
			<style>h1 { color: red }</style>
			<link rel="stylesheet" href="/a.css">
			<link rel="stylesheet" href="/print.css" media="print">
			</head><body><h1>h</h1><p class="lead">p</p></body></html>`))
	}))
	defer srv.Close()

	b := New(Options{})
	defer b.Close()
	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// A print-only sheet is still a declared sheet, and a browser lists it in
	// document.styleSheets even though it does not apply.
	if got := evalString(t, p, `document.styleSheets.length`); got != "3" {
		t.Fatalf("styleSheets.length = %s, want 3", got)
	}
	if got := evalString(t, p, `document.styleSheets[2].media.mediaText`); got != "print" {
		t.Fatalf("print sheet media = %s", got)
	}
	if got := evalString(t, p, `document.styleSheets[2].cssRules.length`); got != "0" {
		t.Fatalf("a print sheet must not be fetched or parsed, got %s rules", got)
	}
	if got := evalString(t, p, `document.styleSheets[0].href`); got != "null" {
		t.Fatalf("inline sheet href = %s, want null", got)
	}
	if got := evalString(t, p, `document.styleSheets[1].href`); got != srv.URL+"/a.css" {
		t.Fatalf("linked sheet href = %s", got)
	}
	if got := evalString(t, p, `document.styleSheets[1].ownerNode.tagName`); got != "LINK" {
		t.Fatalf("ownerNode = %s, want LINK", got)
	}
	// Listing sheets and reading their hrefs must not fetch anything.
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("reading hrefs fetched the sheet %d times; it must be lazy", got)
	}
	if got := evalString(t, p, `document.styleSheets[0].cssRules[0].selectorText`); got != "h1" {
		t.Fatalf("selectorText = %s, want h1", got)
	}
	if got := evalString(t, p, `document.styleSheets[1].cssRules.length`); got != "2" {
		t.Fatalf("cssRules.length = %s, want 2", got)
	}
	if got := evalString(t, p, `document.styleSheets[1].cssRules[1].selectorText`); got != ".lead" {
		t.Fatalf("second rule selector = %s", got)
	}
	if got := evalString(t, p, `document.styleSheets[1].cssRules[0].style.getPropertyValue('color')`); got != "#123456" {
		t.Fatalf("declared color = %s", got)
	}
	if got := evalString(t, p, `document.styleSheets[1].cssRules[1].style.getPropertyPriority('font-size')`); got != "important" {
		t.Fatalf("priority = %s, want important", got)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("cssRules fetched the sheet %d times, want 1", got)
	}
}

// A stylesheet the page adds from JavaScript is part of the page's own CSS, so
// it must be applied, not answered from a cache taken before it existed.
func TestCSSAddedByScript(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/late.css" {
			atomic.AddInt32(&hits, 1)
			w.Header().Set("Content-Type", "text/css")
			_, _ = w.Write([]byte("p { color: #00ff00; font-size: 22px }"))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><style>p { color: #0000ff }</style></head>
			<body><p id="p">text</p>
			<script>
				// Read the style first, so a naive cache would be built here.
				var before = getComputedStyle(document.getElementById('p')).color;
				var link = document.createElement('link');
				link.rel = 'stylesheet';
				link.href = '/late.css';
				document.head.appendChild(link);
				var style = document.createElement('style');
				style.textContent = 'p { font-size: 30px }';
				document.head.appendChild(style);
				document.getElementById('p').style.fontStyle = 'italic';
				window.__before = before;
			</script></body></html>`))
	}))
	defer srv.Close()

	b := New(Options{})
	defer b.Close()
	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	cases := []struct{ expr, want string }{
		{`window.__before`, "rgba(0, 0, 255, 1.000)"},
		// The linked sheet wins on colour, the later <style> on size, and the
		// inline write on style, all of which arrived after the first read.
		{`getComputedStyle(document.getElementById('p')).color`, "rgba(0, 255, 0, 1.000)"},
		{`getComputedStyle(document.getElementById('p')).fontSize`, "30px"},
		{`getComputedStyle(document.getElementById('p')).fontStyle`, "italic"},
		{`document.styleSheets.length`, "3"},
	}
	for _, c := range cases {
		v, err := p.Eval(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got := v.String(); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("the script-added sheet was fetched %d times, want 1", got)
	}
	// A screenshot must see the same styles as getComputedStyle.
	if _, err := p.Screenshot(ScreenshotOptions{Width: 400}); err != nil {
		t.Fatalf("screenshot: %v", err)
	}
}

func evalString(t *testing.T, p *Page, expr string) string {
	t.Helper()
	v, err := p.Eval(expr)
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	return v.String()
}

// StyleSheets is the Go view of what the page declared: every sheet, whether it
// applied, and why not when it did not.
func TestPageStyleSheets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.css":
			w.Header().Set("Content-Type", "text/css")
			_, _ = w.Write([]byte("p { color: red }\n.lead { font-size: 20px }"))
		case "/print.css":
			w.Header().Set("Content-Type", "text/css")
			_, _ = w.Write([]byte("p { color: black }"))
		default:
			if strings.HasSuffix(r.URL.Path, ".css") {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(`<html><head>
				<style>h1 { color: navy }</style>
				<link rel="stylesheet" href="/ok.css">
				<link rel="stylesheet" href="/print.css" media="print">
				<link rel="stylesheet" href="/missing.css">
				</head><body><h1>h</h1><p id="p">p</p></body></html>`))
		}
	}))
	defer srv.Close()

	b := New(Options{})
	defer b.Close()
	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	got := p.StyleSheets()
	const width = 1280 // the default viewport rule counts are taken at
	want := []StyleSheet{
		{Inline: true, Bytes: 18, Rules: 1, Width: width},
		{Href: srv.URL + "/ok.css", Bytes: 42, Rules: 2, Width: width},
		{Href: srv.URL + "/print.css", Media: "print", Width: width, Err: "media print does not match"},
		{Href: srv.URL + "/missing.css", Width: width, Err: "404 Not Found"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d sheets, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sheet %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	// Only the applied sheets reach the cascade: the print sheet's colour and
	// the missing sheet must not apply.
	if v := evalString(t, p, `getComputedStyle(document.querySelector('p')).color`); v != "rgba(255, 0, 0, 1.000)" {
		t.Errorf("paragraph colour = %s, want the applied sheet's red", v)
	}
}

// A CSS escape outside a string must not start one. Tailwind writes
// arbitrary-value classes as ".font-\[\'Poppins\'\2c sans\]"; taking that quote
// as the start of a string swallowed the rest of brave.com's 415KB stylesheet,
// which parsed as 1110 rules instead of 2332 and lost every rule after it.
func TestCSSEscapedSelectors(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><head><style>
		.font-\[\'Poppins\'\2c sans\] { color: rgb(1, 2, 3) }
		.text-\[30px\] { font-size: 30px }
		.after-it { color: rgb(4, 5, 6) }
		@media (min-width: 100px) { .in-media { color: rgb(7, 8, 9) } }
	</style></head><body>
		<p id="a" class="font-['Poppins',sans]">a</p>
		<p id="b" class="text-[30px]">b</p>
		<p id="c" class="after-it">c</p>
		<p id="d" class="in-media">d</p>
	</body></html>`, "https://example.test/")
	cases := []struct{ id, prop, want string }{
		{"a", "color", "rgba(1, 2, 3, 1.000)"},
		{"b", "fontSize", "30px"},
		// The rules after the escaped selector are still parsed, which is
		// what the swallow bug destroyed.
		{"c", "color", "rgba(4, 5, 6, 1.000)"},
		{"d", "color", "rgba(7, 8, 9, 1.000)"},
	}
	for _, c := range cases {
		expr := `getComputedStyle(document.getElementById('` + c.id + `')).` + c.prop
		v, err := p.Eval(expr)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		if got := v.String(); got != c.want {
			t.Errorf("%s = %q, want %q", expr, got, c.want)
		}
	}
	// All four rules survived the escaped one, in the sheet's own view too.
	if got := evalString(t, p, `document.styleSheets[0].cssRules.length`); got != "4" {
		t.Fatalf("parsed %s rules, want 4: a selector with an escaped quote lost the rest", got)
	}
}

// The font shorthand puts the weight, style and variant before the size, and
// the size must carry a unit or be a keyword: a bare number there is the
// weight. Brave sets its buttons with "font: var(--leo-font-...)" whose value
// starts with "600", which was being read as a 600px font size.
func TestCSSFontShorthand(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><head><style>
		:root { --btn: 600 14px/22px system-ui, sans-serif; }
		#a { font: var(--btn); }
		#b { font: italic 700 12px Poppins, sans-serif; }
		#c { font: normal 500 1.5rem/2 Georgia, serif; }
		#d { font: 100% sans-serif; }
	</style></head><body>
		<p id="a">a</p><p id="b">b</p><p id="c">c</p><p id="d">d</p>
	</body></html>`, "https://example.test/")
	cases := []struct{ id, prop, want string }{
		{"a", "fontWeight", "600"},
		{"a", "fontSize", "14px"},
		{"a", "lineHeight", "22px"},
		{"b", "fontWeight", "700"},
		{"b", "fontStyle", "italic"},
		{"b", "fontSize", "12px"},
		{"c", "fontWeight", "500"},
		{"c", "fontSize", "24px"},
		{"d", "fontSize", "16px"},
	}
	for _, c := range cases {
		expr := `getComputedStyle(document.getElementById('` + c.id + `')).` + c.prop
		v, err := p.Eval(expr)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		if got := v.String(); got != c.want {
			t.Errorf("%s = %q, want %q", expr, got, c.want)
		}
	}
}

// A flex or grid item is blockified, so a span inside a flex container reports
// display:block, and the user-agent colours of form controls are per control
// type rather than one white box for all of them.
func TestCSSBlockifiedItemsAndFormControls(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><head><style>
		#flex { display: flex }
		#grid { display: grid }
	</style></head><body>
		<div id="flex"><span id="fs">s</span><b id="fb">b</b></div>
		<div id="grid"><span id="gs">s</span></div>
		<button id="btn">b</button><input id="txt"><input id="chk" type="checkbox">
		<input id="sub" type="submit"><select id="sel"><option id="opt">o</option></select>
		<textarea id="ta">t</textarea>
	</body></html>`, "https://example.test/")
	cases := []struct{ id, prop, want string }{
		{"fs", "display", "block"},
		{"fb", "display", "block"},
		{"gs", "display", "block"},
		// Measured in Chromium for the same document.
		{"btn", "backgroundColor", "rgba(239, 239, 239, 1.000)"},
		{"btn", "textAlign", "center"},
		{"txt", "backgroundColor", "rgba(255, 255, 255, 1.000)"},
		{"chk", "backgroundColor", "rgba(0, 0, 0, 0)"},
		{"sub", "backgroundColor", "rgba(239, 239, 239, 1.000)"},
		{"sub", "textAlign", "center"},
		{"sel", "backgroundColor", "rgba(239, 239, 239, 1.000)"},
		{"ta", "backgroundColor", "rgba(255, 255, 255, 1.000)"},
		{"opt", "display", "block"},
		{"opt", "backgroundColor", "rgba(0, 0, 0, 0)"},
	}
	for _, c := range cases {
		expr := `getComputedStyle(document.getElementById('` + c.id + `')).` + c.prop
		v, err := p.Eval(expr)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		if got := v.String(); got != c.want {
			t.Errorf("%s = %q, want %q", expr, got, c.want)
		}
	}
}

func TestCSSMathFunctions(t *testing.T) {
	cases := []struct {
		expr string
		base float64
		vw   float64
		want float64
		ok   bool
	}{
		// clamp lower, middle and upper bound at a 900px viewport.
		{"clamp(24px, 4vw, 64px)", 16, 300, 24, true},
		{"clamp(24px, 4vw, 64px)", 16, 900, 36, true},
		{"clamp(24px, 4vw, 64px)", 16, 2000, 64, true},
		{"min(10px, 2em)", 16, 0, 10, true},
		{"max(10px, 2em)", 16, 0, 32, true},
		{"2em", 16, 0, 32, true},
		// A vw length needs a viewport, and one unresolvable argument makes
		// the whole expression fail, as a browser drops the declaration.
		{"4vw", 16, 0, 0, false},
		{"clamp(24px, 4vw, 64px)", 16, 0, 0, false},
	}
	for _, c := range cases {
		got, ok := cssLengthToPxV(c.expr, c.base, c.vw)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("cssLengthToPxV(%q, base=%g, vw=%g) = (%g, %v), want (%g, %v)",
				c.expr, c.base, c.vw, got, ok, c.want, c.ok)
		}
	}
}

// An explicit "background: transparent" must clear the user-agent face a form
// control gets, not be ignored because its alpha is zero.
func TestBackgroundTransparentOverridesUADefault(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><head><style>
		#btn { background: transparent }
		#txt { background-color: transparent }
	</style></head><body>
		<button id="btn">b</button><button id="plain">p</button>
		<input id="txt">
	</body></html>`, "https://example.test/")
	cases := []struct{ id, want string }{
		{"btn", "rgba(0, 0, 0, 0)"},
		{"txt", "rgba(0, 0, 0, 0)"},
		{"plain", "rgba(239, 239, 239, 1.000)"}, // untouched UA face
	}
	for _, c := range cases {
		expr := `getComputedStyle(document.getElementById('` + c.id + `')).backgroundColor`
		v, err := p.Eval(expr)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		if got := v.String(); got != c.want {
			t.Errorf("%s = %q, want %q", expr, got, c.want)
		}
	}
}

func TestTextTransformChangesRenderedText(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><head><style>
		.u { text-transform: uppercase }
		.t { text-transform: capitalize }
	</style></head><body>
		<p class="u">hello world</p><p class="t">hello world</p>
	</body></html>`, "https://example.test/")
	out, err := p.RenderOutline(ScreenshotOptions{Width: 400}, 20)
	if err != nil {
		t.Fatalf("RenderOutline: %v", err)
	}
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "HELLO WORLD") {
		t.Errorf("uppercase text missing from render:\n%s", joined)
	}
	if !strings.Contains(joined, "Hello World") {
		t.Errorf("capitalized text missing from render:\n%s", joined)
	}
}

func TestLetterSpacingWidensText(t *testing.T) {
	base := renderStyle{size: 16}
	spaced := renderStyle{size: 16, letterSpacing: 2}
	want := measureText(styleKey(base), "abc") + 2*3
	if got := textWidth(spaced, "abc"); got != want {
		t.Errorf("textWidth with letter-spacing = %g, want %g", got, want)
	}
	if textWidth(base, "abc") == textWidth(spaced, "abc") {
		t.Error("letter-spacing did not change the measured width")
	}
}

func TestApplyTextTransform(t *testing.T) {
	cases := []struct{ mode, in, want string }{
		{"uppercase", "Hello there", "HELLO THERE"},
		{"lowercase", "Hello There", "hello there"},
		{"capitalize", "hello wORLD", "Hello WORLD"},
		{"", "Hello", "Hello"},
	}
	for _, c := range cases {
		if got := applyTextTransform(c.mode, c.in); got != c.want {
			t.Errorf("applyTextTransform(%q, %q) = %q, want %q", c.mode, c.in, got, c.want)
		}
	}
}

func TestCSSSizeParts(t *testing.T) {
	cases := []struct {
		in      string
		vw      float64
		pct, px float64
		ok      bool
	}{
		{"50%", 0, 0.5, 0, true},
		{"12px", 0, 0, 12, true},
		{"calc(50% - 7px)", 0, 0.5, -7, true},
		{"calc(33.333% - 10px)", 0, 0.33333, -10, true},
		{"calc(50% + 4px)", 0, 0.5, 4, true},
		{"auto", 0, 0, 0, false},
	}
	for _, c := range cases {
		pct, px, ok := cssSizeParts(c.in, 16, c.vw)
		if ok != c.ok {
			t.Errorf("cssSizeParts(%q) ok = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if diff(pct, c.pct) > 1e-4 || diff(px, c.px) > 1e-4 {
			t.Errorf("cssSizeParts(%q) = (%g, %g), want (%g, %g)", c.in, pct, px, c.pct, c.px)
		}
	}
}

func diff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

func TestFlexShorthand(t *testing.T) {
	cases := []struct {
		in       string
		grow     float64
		basisPct float64
		basisPx  float64
		hasBasis bool
	}{
		{"1", 1, 0, 0, false},
		{"none", 0, 0, 0, false},
		{"0 0 auto", 0, 0, 0, false},
		{"0 0 calc(50% - 7px)", 0, 0.5, -7, true},
		{"1 1 calc(33.333% - 10px)", 1, 0.33333, -10, true},
		{"2 1 40%", 2, 0.4, 0, true},
	}
	for _, c := range cases {
		var cs computedStyle
		applyFlexShorthand(&cs, c.in, 16, 0)
		if cs.flexGrow != c.grow {
			t.Errorf("flex %q grow = %g, want %g", c.in, cs.flexGrow, c.grow)
		}
		if cs.hasFlexBasis != c.hasBasis {
			t.Errorf("flex %q hasBasis = %v, want %v", c.in, cs.hasFlexBasis, c.hasBasis)
			continue
		}
		if c.hasBasis && (diff(cs.flexBasisPct, c.basisPct) > 1e-4 || diff(cs.flexBasisPx, c.basisPx) > 1e-4) {
			t.Errorf("flex %q basis = (%g, %g), want (%g, %g)", c.in, cs.flexBasisPct, cs.flexBasisPx, c.basisPct, c.basisPx)
		}
	}
}

// The cascade must keep gap and flex; expandShorthands used to drop them.
func TestGapAndFlexSurviveShorthandExpansion(t *testing.T) {
	out := expandShorthands([]cssDecl{
		{prop: "gap", val: "14px"},
		{prop: "flex", val: "0 0 calc(50% - 7px)"},
	})
	got := map[string]bool{}
	for _, d := range out {
		got[d.prop] = true
	}
	if !got["gap"] || !got["flex"] {
		t.Errorf("gap/flex dropped by expandShorthands: %v", out)
	}
}
