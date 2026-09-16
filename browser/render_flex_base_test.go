package browser

import (
	"testing"
)

// A flex item's base size is its max-content width, which the row's own width
// does not cap: an item wider than the row overflows it. Capping the base at
// the row's width made the item lay out at 200 here, where Chromium lays it out
// at its declared 300. rust-lang.org's nav measures its link list this way, and
// the list wrapped its last link onto a second line.
func TestFlexItemBaseIsNotCappedByTheRow(t *testing.T) {
	p := flexPage(t, `<html><head><style>body { margin: 0; font: 16px sans-serif }</style></head><body>
		<div style="display: flex; width: 200px">
			<div style="flex: none"><div style="width: 300px;height: 20px;background: #999"></div></div>
		</div>
	</body></html>`)
	doc, _, err := p.layOut(ScreenshotOptions{Width: 1200, NoImages: true})
	if err != nil {
		t.Fatal(err)
	}
	var widths []float64
	for i := range doc.boxes {
		widths = append(widths, doc.boxes[i].w)
	}
	if len(widths) != 1 {
		t.Fatalf("drew %d boxes, want the item's own: %v", len(widths), widths)
	}
	if widths[0] != 300 {
		t.Errorf("the item's box is %g wide, want 300 (its max-content, not the row's 200)", widths[0])
	}
}
