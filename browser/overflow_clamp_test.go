package browser

import (
	"regexp"
	"testing"
)

// A box with a declared height is that tall even when its content is taller:
// the content overflows and the next element follows the box's bottom, which
// is what a browser does and what lets a percentage-height child resolve
// later. Every case here matches Chromium's offset for the text after the box.
func TestDeclaredHeightClampsItsBox(t *testing.T) {
	cases := []struct {
		name string
		html string
		want string
	}{
		{"content before a percentage child",
			`<div style="height:300px"><p style="margin:0">x</p><div style="height:100%;background:#999"></div></div>`, "300"},
		{"child taller than the box",
			`<div style="height:100px"><div style="height:400px;width:50px;background:#999"></div></div>`, "100"},
		{"several blocks over the box",
			`<div style="height:100px"><p style="margin:0">1</p><p style="margin:0">2</p><p style="margin:0">3</p><p style="margin:0">4</p><p style="margin:0">5</p><p style="margin:0">6</p></div>`, "100"},
		{"content shorter than the box",
			`<div style="height:300px"><p style="margin:0">x</p></div>`, "300"},
		{"border adds to the box",
			`<div style="height:448px;border:1px solid #999"></div>`, "450"},
		{"box with a top margin",
			`<div style="height:100px;margin-top:20px"><p style="margin:0">1</p><p style="margin:0">2</p><p style="margin:0">3</p><p style="margin:0">4</p><p style="margin:0">5</p><p style="margin:0">6</p></div>`, "120"},
		{"min-height still grows with content",
			`<div style="min-height:100px"><p style="margin:0">1</p><p style="margin:0">2</p><p style="margin:0">3</p><p style="margin:0">4</p><p style="margin:0">5</p><p style="margin:0">6</p><p style="margin:0">7</p></div>`, "133"},
	}
	for _, c := range cases {
		p := flexPage(t, `<html><head><style>body{margin:0;font:16px sans-serif}</style></head><body>
			`+c.html+`<div id="after">after</div>
		</body></html>`)
		out, err := p.RenderOutline(ScreenshotOptions{Width: 800, NoImages: true}, 200)
		if err != nil {
			t.Fatal(err)
		}
		re := regexp.MustCompile(`^y=(-?\d+).*after`)
		at := "?"
		for _, l := range out {
			if m := re.FindStringSubmatch(l); m != nil {
				at = m[1]
			}
		}
		if at != c.want {
			t.Errorf("%s: the text after the box sits at y=%s, want %s", c.name, at, c.want)
		}
	}
}
