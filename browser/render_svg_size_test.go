package browser

import (
	"testing"
)

// An inline SVG with a viewBox and no width attribute is sized by the CSS
// width and the viewBox ratio. "w-full" is the usual way to write that, so a
// percentage that never resolves leaves the box with no width and collapses
// whatever it sits in: a page's logos, icons and diagrams are all written this
// way. Chromium gives this fixture a 400x100 SVG and a 119px body.
func TestInlineSVGSizesFromViewBoxAndPercentWidth(t *testing.T) {
	p := flexPage(t, `<html><head><style>
		body { margin: 0 }
		#w { width: 400px }
		svg { display: block; width: 100% }
	</style></head><body>
		<div id="w"><svg viewBox="0 0 200 50"><rect width="200" height="50"/></svg><span>after</span></div>
	</body></html>`)
	lines := outlineLines(t, p, 800)
	var after *outlineLine
	for i := range lines {
		if lines[i].text == "after" {
			after = &lines[i]
		}
	}
	if after == nil {
		t.Fatalf("missing the text after the svg: %+v", lines)
	}
	if after.y != 100 {
		t.Errorf("the text after the svg sits at y=%g, want 100 (the viewBox ratio of a 400px width)", after.y)
	}
}

// A width in pixels still sizes the box, and the height follows the ratio.
func TestInlineSVGSizesFromPixelWidth(t *testing.T) {
	p := flexPage(t, `<html><head><style>
		body { margin: 0 }
		svg { display: block; width: 150px }
	</style></head><body>
		<div><svg viewBox="0 0 200 50"><rect width="200" height="50"/></svg><span>after</span></div>
	</body></html>`)
	lines := outlineLines(t, p, 800)
	for _, l := range lines {
		if l.text == "after" {
			// The outline rounds to whole pixels.
			if l.y < 37 || l.y > 38 {
				t.Errorf("the text after the svg sits at y=%g, want 37.5 (150px wide at 4:1)", l.y)
			}
			return
		}
	}
	t.Fatalf("missing the text after the svg: %+v", lines)
}
