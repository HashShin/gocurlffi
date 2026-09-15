package browser

import (
	"testing"
)

// A block with a declared height is that tall whether or not it paints
// anything: a spacer is written "<div style=height:448px></div>", and a panel
// often puts its background on a child instead of on itself. Chromium holds
// the following text 448px down in every case below.
func TestEmptyBlockKeepsItsDeclaredHeight(t *testing.T) {
	cases := []struct {
		name string
		html string
	}{
		{"plain spacer", `<div style="height:448px"></div>`},
		{"with background", `<div style="height:448px;background:#999"></div>`},
		{"min-height", `<div style="min-height:448px"></div>`},
		{"nested container", `<div style="height:448px"><div></div></div>`},
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
				if l.y != 448 {
					t.Errorf("%s: the text after sits at y=%g, want 448", c.name, l.y)
				}
			}
		}
		if !found {
			t.Errorf("%s: the text after the spacer was not drawn: %+v", c.name, lines)
		}
	}
}

// A border adds to the box unless box-sizing says otherwise, so the element
// below is 450 tall: the same rule a browser follows.
func TestEmptyBlockBorderAddsToItsHeight(t *testing.T) {
	p := flexPage(t, `<html><head><style>body{margin:0;font:16px sans-serif}</style></head><body>
		<div style="height:448px;border:1px solid #999"></div><div id="after">after</div>
	</body></html>`)
	lines := outlineLines(t, p, 800)
	for _, l := range lines {
		if l.text == "after" {
			if l.y != 450 {
				t.Errorf("the text after the bordered spacer sits at y=%g, want 450", l.y)
			}
			return
		}
	}
	t.Fatalf("the text after the spacer was not drawn: %+v", lines)
}
