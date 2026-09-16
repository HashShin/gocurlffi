package browser

import (
	"testing"
)

// A container's declared height sizes its own box even when the block inside
// it carries a sizing context of its own. tailwindcss.com writes its demo
// panels as "grid h-112" around one child, and go.dev's case-study carousel is
// a strip of fixed height holding sized items. Chromium holds the following
// text 300px down in each case below.
func TestContainerKeepsItsDeclaredHeight(t *testing.T) {
	cases := []struct {
		name string
		html string
	}{
		{"child with a width", `<div style="height:300px"><div style="width:50px;background:#999"></div></div>`},
		{"child with a width and height", `<div style="height:300px"><div style="width:50px;height:20px;background:#999"></div></div>`},
		{"child with a background", `<div style="height:300px"><div style="background:#999"></div></div>`},
		{"container inside a container", `<div style="height:300px"><div style="width:50px"><div style="background:#999;width:20px"></div></div></div>`},
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
				if l.y != 300 {
					t.Errorf("%s: the text after sits at y=%g, want 300", c.name, l.y)
				}
			}
		}
		if !found {
			t.Errorf("%s: the text after the container was not drawn: %+v", c.name, lines)
		}
	}
}
