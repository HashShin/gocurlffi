package browser

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// outlineLine is one drawn line of text: where it starts and what it says.
type outlineLine struct {
	x, y float64
	text string
}

func outlineLines(t *testing.T, p *Page, width int) []outlineLine {
	t.Helper()
	out, err := p.RenderOutline(ScreenshotOptions{Width: width, NoImages: true}, 200)
	if err != nil {
		t.Fatalf("RenderOutline: %v", err)
	}
	re := regexp.MustCompile(`^y=(-?\d+)\s+h=\d+\s+text\s+x=(-?\d+)\s+(.*)$`)
	var lines []outlineLine
	for _, line := range out {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		y, _ := strconv.ParseFloat(m[1], 64)
		x, _ := strconv.ParseFloat(m[2], 64)
		text := m[3]
		if i := strings.Index(text, " bg="); i >= 0 {
			text = text[:i]
		}
		lines = append(lines, outlineLine{x: x, y: y, text: strings.TrimSpace(text)})
	}
	return lines
}

// Only a block-level list starts a block. lobste.rs writes each story's tags as
// an inline-block <ul> of inline-block <li> items, and the list path flushed a
// line for the list and another for every item, so a story whose title, tags,
// domain and byline share two lines in Chromium took six.
func TestInlineBlockListFlowsWithItsLine(t *testing.T) {
	p := flexPage(t, `<html><head><style>
		body { margin: 0; font: 16px sans-serif }
		#w { width: 600px }
		ul.tags { display: inline-block; list-style: none; margin: 0; padding: 0 }
		ul.tags li { display: inline-block }
	</style></head><body>
		<div id="w">Story title <ul class="tags"><li>tag-one</li><li>tag-two</li></ul> example.com</div>
	</body></html>`)
	lines := outlineLines(t, p, 800)
	if len(lines) != 1 {
		t.Fatalf("the line broke %d ways: %+v", len(lines), lines)
	}
	for _, want := range []string{"Story title", "tag-one", "tag-two", "example.com"} {
		if !strings.Contains(lines[0].text, want) {
			t.Errorf("%q is not on the line %q", want, lines[0].text)
		}
	}
}

// A closed <details> contributes only its summary to sizing, and the summary of
// an inline-block details is the box's one label, so the text after the details
// stays on the line. lobste.rs reports a comment count this way, and the block
// summary broke each story's byline into three lines.
func TestClosedDetailsLabelStaysOnTheLine(t *testing.T) {
	p := flexPage(t, `<html><head><style>
		body { margin: 0; font: 16px sans-serif }
		#w { width: 600px }
		details.c { display: inline-block }
	</style></head><body>
		<div id="w">alpha <span>beta</span> | <details class="c"><summary>caches</summary><p>hidden words hidden words hidden words hidden words</p></details> | 21 comments</div>
	</body></html>`)
	lines := outlineLines(t, p, 800)
	if len(lines) != 1 {
		t.Fatalf("the line broke %d ways: %+v", len(lines), lines)
	}
	// The outline joins the runs of a line, so the spaces between them are
	// part of what it drops; the words are what matter here.
	for _, want := range []string{"alpha", "beta", "caches", "21 comments"} {
		if !strings.Contains(lines[0].text, want) {
			t.Errorf("%q is not on the line %q", want, lines[0].text)
		}
	}
	if strings.Contains(lines[0].text, "hidden words") {
		t.Errorf("the closed details drew its hidden content: %q", lines[0].text)
	}
}
