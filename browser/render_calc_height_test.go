package browser

import (
	"regexp"
	"testing"
)

// A calc() that mixes a percentage with a length resolves both parts against
// the containing block's height: in a 300px box, calc(50% + 10px) is 160 and
// calc(100% - 40px) is 260, which is what Chromium gives the child in each
// case below.
//
// Each fixture puts a block before the child on purpose. With the child as the
// container's first block the container's own height lands on it as a minimum,
// so the child's declared height is invisible and the fixture proves nothing.
func TestCalcHeightMixingPercentageAndLength(t *testing.T) {
	cases := []struct {
		height string
		want   string
	}{
		{"calc(50% + 10px)", "160"},
		{"calc(100% - 40px)", "260"},
		{"100%", "300"},
	}
	for _, c := range cases {
		p := flexPage(t, `<html><head><style>body{margin:0;font:16px sans-serif}</style></head><body>
			<div style="height:300px"><p style="margin:0">x</p>
			<div style="height:`+c.height+`;background:#999"></div></div>
		</body></html>`)
		out, err := p.RenderOutline(ScreenshotOptions{Width: 800, NoImages: true}, 100)
		if err != nil {
			t.Fatal(err)
		}
		re := regexp.MustCompile(`^box x=-?\d+\s+y=-?\d+\s+w=-?\d+\s+h=(-?\d+)`)
		got := "?"
		for _, l := range out {
			if m := re.FindStringSubmatch(l); m != nil {
				got = m[1]
			}
		}
		if got != c.want {
			t.Errorf("height:%s gave a box %s tall, want %s", c.height, got, c.want)
		}
	}
}
