package browser

import (
	"testing"
)

// A percentage height resolves against the containing block's declared height:
// "h-full" is how a page fills a sized panel. Chromium holds the text after
// the box 300px down in each case but the third, where the percentage has
// nothing to resolve against and the box stays empty.
func TestPercentageHeightResolvesAgainstTheBox(t *testing.T) {
	cases := []struct {
		name string
		html string
		want float64
	}{
		{"full", `<div style="height:300px"><div style="height:100%;background:#999"></div></div>`, 300},
		{"half", `<div style="height:300px"><div style="height:50%;background:#999"></div></div>`, 300},
		{"no sized container", `<div><div style="height:100%;background:#999"></div></div>`, 0},
		{"content before the child", `<div style="height:300px"><p style="margin:0">x</p><div style="height:100%;background:#999"></div></div>`, 300},
		{"percentage inside a percentage", `<div style="height:300px"><div style="height:50%"><div style="height:100%;background:#999"></div></div></div>`, 300},
	}
	for _, c := range cases {
		p := flexPage(t, `<html><head><style>body{margin:0;font:16px sans-serif}</style></head><body>
			`+c.html+`<div id="after">after</div>
		</body></html>`)
		lines := outlineLines(t, p, 800)
		found := false
		for _, l := range lines {
			if l.text == "after" {
				found = true
				if l.y != c.want {
					t.Errorf("%s: the text after sits at y=%g, want %g", c.name, l.y, c.want)
				}
			}
		}
		if !found {
			t.Errorf("%s: the text after the box was not drawn: %+v", c.name, lines)
		}
	}
}
