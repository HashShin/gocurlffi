package browser

import (
	"testing"
)

// A width belongs to the box that declares it. Three "width: 150px" spans
// measure 150 each, so a wrapping row inside a "flex: none" item keeps them on
// one line even though the item is wider than the row around it. Chromium puts
// them at x=0, 150 and 300 on one line.
//
// The declared width used to be ignored whenever the box had text in it, so
// the spans measured as their text (about 30 each), the row inside the item
// wrapped, and the item's own width was inherited from an ancestor's declared
// width instead of being its own.
func TestDeclaredWidthCountsInIntrinsicSizing(t *testing.T) {
	p := flexPage(t, `<html><head><style>body { margin: 0; font: 16px sans-serif }</style></head><body>
		<div style="display: flex; width: 400px">
			<div style="flex: none">
				<div style="display: flex; flex-wrap: wrap">
					<span style="width: 150px">one</span><span style="width: 150px">two</span><span style="width: 150px">three</span>
				</div>
			</div>
		</div>
	</body></html>`)
	lines := outlineLines(t, p, 1200)
	byText := map[string]outlineLine{}
	for _, l := range lines {
		byText[l.text] = l
	}
	for text, wantX := range map[string]float64{"one": 0, "two": 150, "three": 300} {
		l, ok := byText[text]
		if !ok {
			t.Fatalf("%q missing from the outline: %+v", text, lines)
		}
		if l.y != 0 {
			t.Errorf("%q wrapped onto a second line (y=%g)", text, l.y)
		}
		if l.x != wantX {
			t.Errorf("%q sits at x=%g, want %g", text, l.x, wantX)
		}
	}
}
