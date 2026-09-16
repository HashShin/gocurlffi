package browser

import (
	"testing"
)

// A flex row that declares its own width is what its items resolve against,
// even when that width is wider than the containing block. go.dev's
// testimonial carousel is a 10000px row of 1000px slides inside a 1000px
// wrapper that clips it; measuring the row at the wrapper's width squeezed the
// slides into a fraction of the space and wrapped every quote's text.
//
// Chromium puts the second item at x=500 here.
func TestWideFlexRowKeepsItsDeclaredWidth(t *testing.T) {
	p := flexPage(t, `<html><head><style>
		body { margin: 0; font: 16px sans-serif }
		.clip { width: 400px; overflow: hidden }
		ul { display: flex; width: 1000px; list-style: none; margin: 0; padding: 0 }
		li { width: 500px }
	</style></head><body>
		<div class="clip"><ul><li>one</li><li>two</li></ul></div>
	</body></html>`)
	pos := outlinePositions(t, p, 800)
	one, ok1 := pos["one"]
	two, ok2 := pos["two"]
	if !ok1 || !ok2 {
		t.Fatalf("missing items in the outline: %v", pos)
	}
	if one[1] != two[1] {
		t.Errorf("the slides did not stay on one row: one y=%g, two y=%g", one[1], two[1])
	}
	if two[0] != 500 {
		t.Errorf("the second item sits at x=%g, want 500 (half of the row's own 1000px width)", two[0])
	}
}
