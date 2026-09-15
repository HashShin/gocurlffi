package browser

import (
	"regexp"
	"strconv"
	"testing"
)

// The outline lists the boxes the layout drew after the lines of text: an
// element's own rectangle is what a page script would measure, and without it
// the only way to see where a container ended was to read the text inside it.
func TestOutlineListsBoxRectangles(t *testing.T) {
	p := flexPage(t, `<html><head><style>body{margin:0;font:16px sans-serif}</style></head><body>
		<div style="width:150px;height:50px;background:#999">inner</div>
		<div id="after">after</div>
	</body></html>`)
	out, err := p.RenderOutline(ScreenshotOptions{Width: 800, NoImages: true}, 100)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`^box x=(-?\d+)\s+y=(-?\d+)\s+w=(-?\d+)\s+h=(-?\d+)\s+id=\d+$`)
	var boxes [][4]float64
	for _, l := range out {
		m := re.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		var b [4]float64
		for i := 0; i < 4; i++ {
			b[i], _ = strconv.ParseFloat(m[i+1], 64)
		}
		boxes = append(boxes, b)
	}
	if len(boxes) != 1 {
		t.Fatalf("the outline listed %d boxes, want the one painted div: %v", len(boxes), out)
	}
	if boxes[0] != [4]float64{0, 0, 150, 50} {
		t.Errorf("box = %v, want x=0 y=0 w=150 h=50", boxes[0])
	}
}
