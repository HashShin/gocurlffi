package browser

import (
	"testing"
)

// A cascade layer groups rules for ordering; it does not switch them off. A
// sheet that wraps everything in @layer parsed as a handful of rules, because
// every block was skipped. tailwindcss.com serves one 654KB sheet built from
// layers, and the page collapsed into a single column.
func TestLayerRulesAreParsed(t *testing.T) {
	order := 0
	rules, _, st := parseCSSStylesheet(`@layer a, b;
@layer utilities { .x { color: red } .y { display: flex } }
@layer components { .z { color: blue } }
.unlayered { color: green }`, 1200, &order)
	if len(rules) != 4 {
		t.Fatalf("parsed %d rules, want 4 (skipped %d/%d selectors)", len(rules), st.skipped, st.selectors)
	}
	for _, r := range rules {
		if r.text != ".x" {
			continue
		}
		color := ""
		for _, d := range r.decls {
			if d.prop == "color" {
				color = d.val
			}
		}
		if color != "red" {
			t.Errorf(".x color = %q, want red", color)
		}
	}
}

// The rules in a layer apply, including the ones after the layer block.
func TestLayerRulesApply(t *testing.T) {
	p := flexPage(t, `<html><head><style>
		@layer reset, utilities;
		@layer utilities { #nav { display: flex } }
		#after { color: red }
	</style></head><body>
		<div id="nav"><span>Left</span><span>Right</span></div>
		<div id="after">tail</div>
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
	if _, ok := pos["tail"]; !ok {
		t.Errorf("the rule after the layer blocks did not apply: %v", pos)
	}
}
