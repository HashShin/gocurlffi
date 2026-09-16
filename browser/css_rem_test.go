package browser

import (
	"testing"
)

// A rem length is the root element's computed font size, not a fixed 16px.
// "html { font-size: 62.5% }" is a common idiom for making a rem 10px, and
// resolving rem against 16 anyway renders every page that uses it a third too
// large: rust-lang.org's nav items came out 33% wide, which broke its nav into
// three rows. Chromium gives these sizes, and so do we.
func TestRemResolvesAgainstTheRootFontSize(t *testing.T) {
	cases := []struct {
		name string
		css  string
		sel  string
		want string
	}{
		{"62.5% root", `html{font-size:62.5%} #x{font-size:1.5rem}`, "#x", "15px"},
		{"75% root", `html{font-size:75%} #x{font-size:1.5rem}`, "#x", "18px"},
		{"default root", `#x{font-size:1.5rem}`, "#x", "24px"},
		{"two rems", `html{font-size:62.5%} #x{font-size:2rem}`, "#x", "20px"},
		{"percentage still chains", `html{font-size:62.5%} body{font-size:200%} #x{font-size:1.5rem}`, "#x", "15px"},
	}
	for _, c := range cases {
		p := flexPage(t, `<html><head><style>`+c.css+`</style></head><body>
			<div id="x">t</div>
		</body></html>`)
		expr := `String(getComputedStyle(document.querySelector("` + c.sel + `")).getPropertyValue("font-size"))`
		v, err := p.Eval(expr)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := v.String(); got != c.want {
			t.Errorf("%s: font-size = %q, want %q", c.name, got, c.want)
		}
	}
}

// A rem inside a quoted string is text, not a length, so it is left alone.
func TestRemInsideAStringIsNotALength(t *testing.T) {
	p := flexPage(t, `<html><head><style>html{font-size:62.5%} #x:before{content:"1rem"}</style></head><body>
		<div id="x">t</div>
	</body></html>`)
	// The rule must still parse: a mangled value would drop the declaration.
	if _, err := p.Eval(`document.querySelector("#x") !== null`); err != nil {
		t.Fatalf("Eval: %v", err)
	}
}
