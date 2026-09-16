package browser

import (
	"reflect"
	"strings"
	"testing"
)

// The selector compiler has no :is(), :where() or multi-argument :not(). These
// rewrites stand in for them, and each one has to keep the meaning exactly.
func TestExpandSelector(t *testing.T) {
	cases := []struct {
		sel  string
		want []string
	}{
		// A list of single compounds inlines textually: the alternatives
		// merge into the compound they sit in.
		{`:is(.a, .b) .c`, []string{".a .c", ".b .c"}},
		{`.foo:is(.bar)`, []string{".foo.bar"}},
		{`:is(.a, .b) > .c:hover`, []string{".a > .c:hover", ".b > .c:hover"}},
		{`:where(.dark, .dark *)`, []string{".dark", ".dark *"}},
		// Tailwind's "every descendant" variant: "**(...)" compiles to
		// ":is(.escaped *)". A "*" before the suffix is what keeps the
		// suffix on the subject compound.
		{`:is(.\*\*\:\[\.line\]\:block *).line`, []string{`.\*\*\:\[\.line\]\:block *.line`}},
		// :not(a, b) matches exactly what :not(a):not(b) matches.
		{`:not(.a, .b)`, []string{":not(.a)", ":not(.b)"}},
		{`li:not(:last-child,.x)`, []string{"li:not(:last-child)", "li:not(.x)"}},
		// An escaped colon is part of a class name, not a function.
		{`.md\:flex`, []string{`.md\:flex`}},
		// A complex argument would have its first compound glued to what came
		// before it, so the selector is left for the compiler to reject.
		{`:is(div p)`, nil},
		{`.p :is(.a .b)`, nil},
	}
	for _, c := range cases {
		if got := expandSelector(c.sel); !reflect.DeepEqual(got, c.want) {
			t.Errorf("expandSelector(%q) = %q, want %q", c.sel, got, c.want)
		}
	}
}

// A rule written with :is() applies, and a :where() rule applies without
// gaining specificity from what it wraps.
func TestIsAndWhereRulesApply(t *testing.T) {
	p := flexPage(t, `<html><head><style>
		.box { color: rgb(1, 2, 3) }
		:is(#a, #b) { color: rgb(4, 5, 6) }
		* { background-color: rgb(1, 2, 3) }
		:where(#a) { background-color: rgb(4, 5, 6) }
	</style></head><body>
		<div id="a" class="box">one</div><div id="b" class="box">two</div>
	</body></html>`)
	// :is() carries the specificity of its argument, so it beats the class
	// rule; :where() carries none, so it only wins by coming later.
	want := []struct{ id, prop, color string }{
		{"a", "color", "4, 5, 6"},
		{"b", "color", "4, 5, 6"},
		{"a", "background-color", "4, 5, 6"},
		{"b", "background-color", "1, 2, 3"},
	}
	for _, w := range want {
		expr := `getComputedStyle(document.getElementById('` + w.id + `')).getPropertyValue('` + w.prop + `')`
		v, err := p.Eval(expr)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		if got := v.String(); !strings.Contains(got, w.color) {
			t.Errorf("%s of #%s = %q, want %s", w.prop, w.id, got, w.color)
		}
	}
}

// The shape Tailwind v4 emits for a highlighted code block: every line is a
// <span class="line">, and the line breaks come from a rule reached through
// :is(). Inline spans put the whole block on one line.
func TestIsFunctionSelectorBreaksCodeLines(t *testing.T) {
	p := flexPage(t, `<html><head><style>
		:is(.\*\*\:\[\.line\]\:block *).line { display: block }
	</style></head><body>
		<div class="**:[.line]:block"><code><code>
			<span class="line">one</span><span class="line">two</span><span class="line">three</span>
		</code></code></div>
	</body></html>`)
	lines := outlineLines(t, p, 400)
	var texts []string
	for _, l := range lines {
		texts = append(texts, l.text)
	}
	want := []string{"one", "two", "three"}
	if len(texts) != len(want) {
		t.Fatalf("the code block broke into %d lines (%v), want %d", len(texts), texts, len(want))
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, texts[i], want[i])
		}
	}
}
