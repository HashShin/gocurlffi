package browser

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/image/font/gofont/gomono"
)

func TestParseFontFacePicksRawSfnt(t *testing.T) {
	body := `font-family: 'Geist Mono'; font-style: normal; font-weight: 700; ` +
		`src: url(https://cdn/a.woff2) format('woff2'), url(https://cdn/a.ttf) format('truetype');`
	f, ok := parseFontFace(body)
	if !ok {
		t.Fatal("parseFontFace returned ok=false")
	}
	if f.family != "Geist Mono" {
		t.Errorf("family = %q, want %q", f.family, "Geist Mono")
	}
	if f.weight != 700 {
		t.Errorf("weight = %d, want 700", f.weight)
	}
	if f.italic {
		t.Error("italic = true, want false")
	}
	if f.src != "https://cdn/a.ttf" || f.format != "truetype" {
		t.Errorf("src = %q (%s), want the truetype source", f.src, f.format)
	}
}

func TestParseFontFaceRequiresFamilyAndSrc(t *testing.T) {
	if _, ok := parseFontFace(`font-weight: 400; src: url(x.ttf)`); ok {
		t.Error("accepted a face with no family")
	}
	if _, ok := parseFontFace(`font-family: X; src: local(Helvetica)`); ok {
		t.Error("accepted a face whose only source is local()")
	}
}

func TestFontFaceFormatRank(t *testing.T) {
	cases := []struct {
		format, url string
		want        int
	}{
		{"truetype", "https://x/a.ttf", 0},
		{"", "https://x/a.otf", 0},
		{"", "https://x/a.woff", 2},
		{"woff2", "https://x/a", 3},
		{"woff2-variations", "https://x/a", 3},
		{"woff", "https://x/a", 2},
	}
	for _, c := range cases {
		if got := fontFormatRank(c.format, c.url); got != c.want {
			t.Errorf("fontFormatRank(%q, %q) = %d, want %d", c.format, c.url, got, c.want)
		}
	}
}

// TestPageFontWeightAndSlope checks the closest-face selection without any
// network: the parsed fonts are placed in the cache directly.
func TestPageFontWeightAndSlope(t *testing.T) {
	r400 := &webFont{}
	r800 := &webFont{}
	i400 := &webFont{}
	p := &Page{
		fontFaces: []fontFace{
			{family: "Nunito", weight: 400, src: "u400", format: "truetype"},
			{family: "Nunito", weight: 800, src: "u800", format: "truetype"},
			{family: "Nunito", weight: 400, italic: true, src: "i400", format: "truetype"},
		},
		fontLoaded: map[string]*webFont{"u400": r400, "u800": r800, "i400": i400},
	}
	if got := p.pageFont("nunito", 700, false); got != r800 {
		t.Errorf("weight 700 picked %p, want the 800 face", got)
	}
	if got := p.pageFont("Nunito", 400, false); got != r400 {
		t.Errorf("weight 400 picked %p, want the 400 face", got)
	}
	if got := p.pageFont("Nunito", 400, true); got != i400 {
		t.Errorf("italic picked %p, want the italic face", got)
	}
	if got := p.pageFont("Unknown", 400, false); got != nil {
		t.Errorf("unknown family picked %p, want nil", got)
	}
}

// TestPageFontSkipsWoff2 checks a face that is only offered as WOFF2 is not
// fetched, since the renderer cannot decode it.
func TestPageFontSkipsWoff2(t *testing.T) {
	p := &Page{fontFaces: []fontFace{{family: "X", weight: 400, src: "x.woff2", format: "woff2"}}}
	if got := p.pageFont("X", 400, false); got != nil {
		t.Errorf("woff2-only face returned %p, want nil", got)
	}
	if len(p.fontLoaded) != 0 {
		t.Error("a woff2 file was loaded")
	}
}

// TestAddFontFacesDedupes keeps the first face per family/weight/slope, which is
// what document order means in a browser.
func TestAddFontFacesDedupes(t *testing.T) {
	p := &Page{}
	p.addFontFaces([]fontFace{
		{family: "X", weight: 400, src: "a.ttf"},
		{family: "X", weight: 400, src: "b.ttf"},
		{family: "X", weight: 700, src: "c.ttf"},
	})
	if len(p.fontFaces) != 2 {
		t.Fatalf("got %d faces, want 2", len(p.fontFaces))
	}
	if p.fontFaces[0].src != "a.ttf" {
		t.Errorf("first face src = %q, want a.ttf", p.fontFaces[0].src)
	}
}

// TestScreenshotLoadsPageFont drives a real load and render against a local
// server: the @font-face is parsed from the page's own <style>, the file is
// fetched, and the run is drawn with it instead of the embedded font.
func TestScreenshotLoadsPageFont(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/font.ttf" {
			w.Header().Set("Content-Type", "font/ttf")
			_, _ = w.Write(gomono.TTF)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><style>
@font-face { font-family: 'TestFace'; font-weight: 400; src: url(/font.ttf) format('truetype'); }
body { font-family: 'TestFace', sans-serif; }
</style><p>Hello webfont</p>`))
	}))
	defer srv.Close()

	b := New(Options{})
	defer b.Close()
	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := p.Screenshot(ScreenshotOptions{Width: 400}); err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if len(p.fontFaces) != 1 {
		t.Fatalf("fontFaces = %d, want 1", len(p.fontFaces))
	}
	if f := p.pageFont("TestFace", 400, false); f == nil {
		t.Fatal("pageFont did not resolve the page's @font-face")
	}
	if p.pageFont("TestFace", 400, false) != p.fontLoaded[srv.URL+"/font.ttf"] {
		t.Error("pageFont did not use the fetched file")
	}
	if f := p.pageFont("NoSuchFace", 400, false); f != nil {
		t.Error("pageFont matched an unknown family")
	}
}

// TestCSSStatsReportFontFaces confirms a font provider's sheet is no longer
// counted as containing nothing.
func TestCSSStatsReportFontFaces(t *testing.T) {
	src := `@font-face{font-family:'A';src:url(a.ttf) format('truetype');font-weight:400}
@font-face{font-family:'A';src:url(b.woff2) format('woff2');font-weight:700}
body{color:#000}`
	order := 0
	rules, _, stats := parseCSSStylesheet(src, 900, &order)
	if len(rules) != 1 {
		t.Errorf("rules = %d, want 1", len(rules))
	}
	if len(stats.fontFaces) != 2 {
		t.Fatalf("font faces = %d, want 2", len(stats.fontFaces))
	}
	if stats.fontFaces[0].family != "A" || stats.fontFaces[0].weight != 400 {
		t.Errorf("first face = %+v", stats.fontFaces[0])
	}
}
