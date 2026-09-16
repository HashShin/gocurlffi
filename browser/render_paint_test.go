package browser

import (
	"strings"
	"testing"
)

// lineText is the text a rendered line actually draws, which is not always what
// the extractors return: Page.Text walks the DOM, so it kept spaces that the
// painter dropped.
func lineText(doc *renderDoc) string {
	var b strings.Builder
	for _, ln := range doc.lines {
		for _, r := range ln.runs {
			b.WriteString(r.text)
		}
	}
	return b.String()
}

// Whitespace between inline elements separates their runs. Discarding it ran
// words together: "by <small>Albert Einstein</small>" was drawn "byAlbert", and
// a run of tag links came out as one word.
func TestRenderKeepsSpacesBetweenInlineElements(t *testing.T) {
	cases := []struct{ name, html, want string }{
		{
			"space at the end of the run before an element",
			`<p>by <small>Albert Einstein</small></p>`,
			"by Albert Einstein",
		},
		{
			"whitespace-only node between two elements",
			`<p>Tags: <a>one</a> <a>two</a></p>`,
			"Tags: one two",
		},
		{
			"space at the start of the run after an element",
			`<p><a>link</a> after</p>`,
			"link after",
		},
		{
			"spaces inside one run",
			`<p>plain words here</p>`,
			"plain words here",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := layoutDoc(t, "<html><body>"+tc.html+"</body></html>", 400)
			if got := lineText(doc); got != tc.want {
				t.Errorf("drawn text = %q, want %q", got, tc.want)
			}
		})
	}

	// Whitespace that the source does not have must stay absent.
	doc := layoutDoc(t, `<html><body><p><span>plain</span><span>by</span></p></body></html>`, 400)
	if got := lineText(doc); got != "plainby" {
		t.Errorf("drawn text = %q, want plainby: the source has no space there", got)
	}
}

// The same whitespace must not become a box. A whitespace-only node ahead of
// any content is a leading space, which the line wrapper drops, so appending it
// made an otherwise empty block non-empty and flush drew a zero-height box.
func TestLeadingWhitespaceAddsNoBox(t *testing.T) {
	doc := layoutDoc(t, `<html><body style="margin:0;background:#fff">
		<div style="width:100%;background:#eee;height:10px"></div></body></html>`, 400)
	if len(doc.boxes) != 1 {
		t.Fatalf("boxes = %d, want 1; a whitespace node between the elements became a box",
			len(doc.boxes))
	}
}

// An outer box-shadow is clipped to outside the border box, so a shadowed
// element with no background of its own stays transparent behind its text.
// Without the clip the grown shadow rectangle covered the box as well as the
// ring around it, and every quote card on quotes.toscrape.com came out flat
// grey under its #333 shadow.
func TestBoxShadowDoesNotFillItsBox(t *testing.T) {
	img := screenshotOf(t, `<html><body style="margin:0;background:#fff">`+
		`<div style="width:300px;height:80px;box-shadow:2px 2px 3px #333333"></div>`+
		`</body></html>`, ScreenshotOptions{Width: 400, NoImages: true})

	// Inside the box, which has no background: the page's white.
	if r, g, b, _ := img.At(150, 40).RGBA(); r>>8 != 255 || g>>8 != 255 || b>>8 != 255 {
		t.Errorf("inside the box = %d,%d,%d, want white; the shadow was painted over it",
			r>>8, g>>8, b>>8)
	}

	// Beyond the bottom-right corner, where the shadow falls.
	r, g, b, _ := img.At(304, 84).RGBA()
	if r>>8 > 240 && g>>8 > 240 && b>>8 > 240 {
		t.Errorf("at the shadow's corner = %d,%d,%d, want the shadow visible there",
			r>>8, g>>8, b>>8)
	}
}

// An inline element's background belongs to its text runs, because nothing else
// draws a box for an inline element. It used to be dropped entirely, so a tag
// pill rendered as plain text.
func TestInlineBackgroundIsPainted(t *testing.T) {
	img := screenshotOf(t, `<html><body style="margin:0;background:#fff">`+
		`<p><span style="background:#7ca3e6;color:#fff">pill</span></p>`+
		`</body></html>`, ScreenshotOptions{Width: 400, NoImages: true})

	// A pixel inside the run's box should carry the background.
	found := false
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y && !found; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r>>8 < 160 && g>>8 > 130 && g>>8 < 200 && b>>8 > 200 {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("no pixel carried the inline background; the pill was drawn as plain text")
	}
}

// A block's background is its box, so it must not also be painted behind every
// run: a translucent colour would be drawn twice and come out darker.
func TestBlockBackgroundIsNotPaintedTwice(t *testing.T) {
	img := screenshotOf(t, `<html><body style="margin:0;background:#fff">`+
		`<div style="background:rgba(0,0,0,0.25);padding:20px">text</div>`+
		`</body></html>`, ScreenshotOptions{Width: 400, NoImages: true})

	// Over the text and beside it, inside the same box: the same colour.
	r1, g1, b1, _ := img.At(200, 10).RGBA()
	r2, g2, b2, _ := img.At(200, 40).RGBA()
	if r1 != r2 || g1 != g2 || b1 != b2 {
		t.Errorf("box interior %d,%d,%d differs from %d,%d,%d: the background was painted twice",
			r1>>8, g1>>8, b1>>8, r2>>8, g2>>8, b2>>8)
	}
}
