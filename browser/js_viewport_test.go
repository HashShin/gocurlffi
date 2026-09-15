package browser

import (
	"testing"
)

// A page script must see the same viewport the layout used: innerWidth is the
// width the document is laid out at, not a fixed number, and window.matchMedia
// answers the way the stylesheet's own media queries do.
func TestScriptViewportMatchesTheLayout(t *testing.T) {
	p := flexPage(t, `<html><head><style>
		@media (min-width: 64rem) { #wide { color: rgb(1, 2, 3) } }
		@media (max-width: 64rem) { #narrow { color: rgb(1, 2, 3) } }
	</style></head><body>
		<div id="wide">wide</div><div id="narrow">narrow</div>
	</body></html>`)
	// Before any layout the viewport is the default, and the default width is
	// what the style engine has been using all along.
	v, err := p.Eval(`innerWidth`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.ToInteger(); got != defaultLayoutWidth {
		t.Errorf("innerWidth before a layout = %d, want %d", got, defaultLayoutWidth)
	}

	if _, err := p.Screenshot(ScreenshotOptions{Width: 1200, NoImages: true}); err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	v, err = p.Eval(`String(innerWidth) + " " + String(outerWidth) + " " + String(screen.width)`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.String(); got != "1200 1200 1200" {
		t.Errorf("viewport globals = %q, want 1200 for innerWidth, outerWidth and screen.width", got)
	}

	// The narrow rule matched at the layout width, so matchMedia has to agree.
	v, err = p.Eval(`String(matchMedia("(min-width: 64rem)").matches) + " " + String(matchMedia("(max-width: 64rem)").matches)`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.String(); got != "true false" {
		t.Errorf("matchMedia at 1200 = %q, want true false", got)
	}
	v, err = p.Eval(`getComputedStyle(document.getElementById("wide")).getPropertyValue("color")`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.String(); got != "rgba(1, 2, 3, 1.000)" {
		t.Errorf("the wide rule did not apply at 1200: %q", got)
	}

	// A narrower render moves both the layout and the script's answer.
	if _, err := p.Screenshot(ScreenshotOptions{Width: 800, NoImages: true}); err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	v, err = p.Eval(`String(innerWidth) + " " + String(matchMedia("(min-width: 64rem)").matches)`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.String(); got != "800 false" {
		t.Errorf("after a render at 800 = %q, want 800 false", got)
	}
}

// matchMedia answers the same way the stylesheet parser does, because both go
// through one matcher.
func TestMatchMediaSharesTheStylesheetMatcher(t *testing.T) {
	cases := []struct {
		query string
		width float64
		want  bool
	}{
		{"(min-width: 64rem)", 1200, true},
		{"(min-width: 64rem)", 900, false},
		{"(max-width: 30em)", 480, true},
		{"(max-width: 30em)", 1200, false},
		{"screen and (min-width: 64rem)", 1200, true},
		{"print", 1200, false},
		{"not screen", 1200, false},
	}
	for _, c := range cases {
		if got := matchMediaQuery(c.query, c.width, defaultLayoutHeight); got != c.want {
			t.Errorf("matchMediaQuery(%q at %g) = %v, want %v", c.query, c.width, got, c.want)
		}
	}
}
