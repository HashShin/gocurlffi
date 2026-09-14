package browser

import (
	"strings"
	"testing"
)

// A comment is stripped even when it holds a quote character. An apostrophe in
// "/* the browser's default */" used to open a string that never closed, which
// swallowed the rest of the sheet: lobste.rs lost every rule after its first
// comment and rendered as plain stacked text.
func TestCommentWithApostropheDoesNotSwallowTheSheet(t *testing.T) {
	got := stripCSSComments(`a{color:red} /* the browser's default */ b{color:blue}`)
	if !strings.Contains(got, "b{color:blue}") {
		t.Errorf("the rule after the comment was lost: %q", got)
	}
	if strings.Contains(got, "browser") {
		t.Errorf("the comment survived: %q", got)
	}
}

// A comment marker inside a string is text, not a comment: content:"/*" and a
// url with a comment marker in it are both strings a sheet may carry.
func TestCommentMarkerInsideStringIsText(t *testing.T) {
	for _, src := range []string{
		`a{content:"/* not a comment */"}`,
		`a{background:url("/*.png")}`,
		`a{content:'a */ b'}`,
	} {
		if got := stripCSSComments(src); got != src {
			t.Errorf("stripCSSComments(%q) = %q, want it unchanged", src, got)
		}
	}
}

// The end-to-end shape of the bug: a comment carrying an apostrophe sits above
// the rule that drives the layout, and that rule still applies.
func TestRuleAfterApostropheCommentApplies(t *testing.T) {
	p := flexPage(t, `<html><head><style>
		#nav { /* the browser's default: a nav bar is a row */ display: flex }
		#nav span { flex: 1 /* and the labels share the row */ }
	</style></head><body>
		<div id="nav"><span>Left</span><span>Right</span></div>
	</body></html>`)
	v, err := p.Eval(`getComputedStyle(document.getElementById('nav')).display`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.String(); got != "flex" {
		t.Errorf("#nav display = %q, want flex", got)
	}
	pos := outlinePositions(t, p, 400)
	l, ok1 := pos["Left"]
	r, ok2 := pos["Right"]
	if !ok1 || !ok2 {
		t.Fatalf("missing items in outline: %v", pos)
	}
	if l[1] != r[1] {
		t.Errorf("the row wrapped: Left y=%g, Right y=%g", l[1], r[1])
	}
}
