package browser

import (
	"image/color"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
)

// --- value parsing ---

func cssLengthToPx(v string, base float64) (float64, bool) {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return 0, false
	}
	if v == "0" {
		return 0, true
	}
	if v == "auto" || v == "none" || v == "normal" || v == "inherit" ||
		v == "initial" || v == "unset" || v == "revert" || v == "transparent" {
		return 0, false
	}
	num, unit := splitCSSNumber(v)
	if num == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, false
	}
	switch unit {
	case "", "px":
		return f, true
	case "pt":
		return f * 4 / 3, true
	case "pc":
		return f * 16, true
	case "in":
		return f * 96, true
	case "cm":
		return f * 96 / 2.54, true
	case "mm":
		return f * 96 / 25.4, true
	case "q":
		return f * 96 / 101.6, true
	case "em":
		return f * base, true
	case "rem":
		return f * 16, true
	case "%":
		return f * base / 100, true
	}
	return 0, false
}

func splitCSSNumber(v string) (num, unit string) {
	i := 0
	if i < len(v) && (v[i] == '+' || v[i] == '-') {
		i++
	}
	for i < len(v) && (v[i] >= '0' && v[i] <= '9' || v[i] == '.') {
		i++
	}
	return v[:i], strings.TrimSpace(v[i:])
}

// cssFontSizeToPx resolves absolute keywords as well as lengths.
func cssFontSizeToPx(v string, base float64) (float64, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "xx-small":
		return base * 0.58, true
	case "x-small":
		return base * 0.69, true
	case "small":
		return base * 0.83, true
	case "medium":
		return base, true
	case "large":
		return base * 1.2, true
	case "x-large":
		return base * 1.5, true
	case "xx-large":
		return base * 2, true
	case "smaller":
		return base * 0.83, true
	case "larger":
		return base * 1.2, true
	}
	return cssLengthToPx(v, base)
}

var cssNamedColors = map[string]color.RGBA{
	"black":          {0, 0, 0, 0xff},
	"white":          {0xff, 0xff, 0xff, 0xff},
	"red":            {0xff, 0x00, 0x00, 0xff},
	"green":          {0x00, 0x80, 0x00, 0xff},
	"blue":           {0x00, 0x00, 0xff, 0xff},
	"gray":           {0x80, 0x80, 0x80, 0xff},
	"grey":           {0x80, 0x80, 0x80, 0xff},
	"silver":         {0xc0, 0xc0, 0xc0, 0xff},
	"maroon":         {0x80, 0x00, 0x00, 0xff},
	"olive":          {0x80, 0x80, 0x00, 0xff},
	"lime":           {0x00, 0xff, 0x00, 0xff},
	"aqua":           {0x00, 0xff, 0xff, 0xff},
	"teal":           {0x00, 0x80, 0x80, 0xff},
	"navy":           {0x00, 0x00, 0x80, 0xff},
	"fuchsia":        {0xff, 0x00, 0xff, 0xff},
	"purple":         {0x80, 0x00, 0x80, 0xff},
	"orange":         {0xff, 0xa5, 0x00, 0xff},
	"yellow":         {0xff, 0xff, 0x00, 0xff},
	"darkgray":       {0xa9, 0xa9, 0xa9, 0xff},
	"darkgrey":       {0xa9, 0xa9, 0xa9, 0xff},
	"lightgray":      {0xd3, 0xd3, 0xd3, 0xff},
	"lightgrey":      {0xd3, 0xd3, 0xd3, 0xff},
	"whitesmoke":     {0xf5, 0xf5, 0xf5, 0xff},
	"ghostwhite":     {0xf8, 0xf8, 0xff, 0xff},
	"snow":           {0xff, 0xfa, 0xfa, 0xff},
	"beige":          {0xf5, 0xf5, 0xdc, 0xff},
	"ivory":          {0xff, 0xff, 0xf0, 0xff},
	"azure":          {0xf0, 0xff, 0xff, 0xff},
	"khaki":          {0xf0, 0xe6, 0x8c, 0xff},
	"pink":           {0xff, 0xc0, 0xcb, 0xff},
	"crimson":        {0xdc, 0x14, 0x3c, 0xff},
	"tomato":         {0xff, 0x63, 0x47, 0xff},
	"gold":           {0xff, 0xd7, 0x00, 0xff},
	"dimgray":        {0x69, 0x69, 0x69, 0xff},
	"dimgrey":        {0x69, 0x69, 0x69, 0xff},
	"slategray":      {0x70, 0x80, 0x90, 0xff},
	"slategrey":      {0x70, 0x80, 0x90, 0xff},
	"steelblue":      {0x46, 0x82, 0xb4, 0xff},
	"royalblue":      {0x41, 0x69, 0xe1, 0xff},
	"cornflowerblue": {0x64, 0x95, 0xed, 0xff},
	"dodgerblue":     {0x1e, 0x90, 0xff, 0xff},
	"seagreen":       {0x2e, 0x8b, 0x57, 0xff},
	"forestgreen":    {0x22, 0x8b, 0x22, 0xff},
	"firebrick":      {0xb2, 0x22, 0x22, 0xff},
	"brown":          {0xa5, 0x2a, 0x2a, 0xff},
	"chocolate":      {0xd2, 0x69, 0x1e, 0xff},
	"peru":           {0xcd, 0x85, 0x3f, 0xff},
	"tan":            {0xd2, 0xb4, 0x8c, 0xff},
	"wheat":          {0xf5, 0xde, 0xb3, 0xff},
	"linen":          {0xfa, 0xf0, 0xe6, 0xff},
	"seashell":       {0xff, 0xf5, 0xee, 0xff},
	"lavender":       {0xe6, 0xe6, 0xfa, 0xff},
	"thistle":        {0xd8, 0xbf, 0xd8, 0xff},
	"plum":           {0xdd, 0xa0, 0xdd, 0xff},
	"orchid":         {0xda, 0x70, 0xd6, 0xff},
	"transparent":    {0, 0, 0, 0},
}

func parseCSSColor(v string) (color.RGBA, bool) {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return color.RGBA{}, false
	}
	if c, ok := cssNamedColors[v]; ok {
		return c, true
	}
	if strings.HasPrefix(v, "#") {
		hexs := v[1:]
		switch len(hexs) {
		case 3:
			hexs = string([]byte{hexs[0], hexs[0], hexs[1], hexs[1], hexs[2], hexs[2]})
		case 4:
			hexs = string([]byte{hexs[0], hexs[0], hexs[1], hexs[1], hexs[2], hexs[2], hexs[3], hexs[3]})
		case 6, 8:
		default:
			return color.RGBA{}, false
		}
		n, err := strconv.ParseUint(hexs, 16, 64)
		if err != nil {
			return color.RGBA{}, false
		}
		switch len(hexs) {
		case 6:
			return color.RGBA{uint8(n >> 16), uint8(n >> 8), uint8(n), 0xff}, true
		case 8:
			return color.RGBA{uint8(n >> 24), uint8(n >> 16), uint8(n >> 8), uint8(n)}, true
		}
	}
	for _, fn := range []string{"rgb(", "rgba("} {
		if strings.HasPrefix(v, fn) {
			inner := strings.TrimSuffix(v[len(fn):], ")")
			parts := splitTopLevel(inner, ',')
			if len(parts) < 3 {
				return color.RGBA{}, false
			}
			vals := make([]float64, 0, 4)
			for i := 0; i < 3; i++ {
				f, err := strconv.ParseFloat(strings.TrimSpace(parts[i]), 64)
				if err != nil {
					return color.RGBA{}, false
				}
				vals = append(vals, f)
			}
			a := 1.0
			if len(parts) >= 4 {
				if f, err := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64); err == nil {
					a = f
				}
			}
			return color.RGBA{
				uint8(clampF(vals[0])), uint8(clampF(vals[1])), uint8(clampF(vals[2])),
				uint8(a * 255),
			}, true
		}
	}
	return color.RGBA{}, false
}

func clampF(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 255 {
		return 255
	}
	return f
}

// cssColorToken finds a color-looking token in a shorthand value.
func cssColorToken(v string) string {
	for _, tok := range splitTopLevel(v, ' ') {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if _, ok := parseCSSColor(tok); ok {
			return tok
		}
	}
	return ""
}

// --- computed style ---

type computedStyle struct {
	display       string
	visibility    string
	textColor     color.RGBA
	background    color.RGBA
	hasBackground bool
	fontSize      float64
	bold          bool
	italic        bool
	mono          bool
	underline     bool
	strike        bool
	link          bool
	textAlign     string
	lineHeight    float64
	whiteSpace    string
	marginTop     float64
	marginBottom  float64
	marginLeft    float64
	paddingLeft   float64
	listStyle     string
}

func defaultComputedStyle() computedStyle {
	return computedStyle{
		display:    "inline",
		visibility: "visible",
		textColor:  renderTextColor,
		fontSize:   16,
		whiteSpace: "normal",
		textAlign:  "left",
	}
}

// --- engine ---

type styleEngine struct {
	rules    []cssRule
	cache    map[*html.Node]*computedStyle
	baseSize float64
	width    float64
}

func newStyleEngine(rules []cssRule, width float64) *styleEngine {
	return &styleEngine{rules: rules, cache: map[*html.Node]*computedStyle{}, baseSize: 16, width: width}
}

var styleInherited = map[string]bool{
	"color": true, "font-family": true, "font-size": true, "font-weight": true,
	"font-style": true, "line-height": true, "text-align": true,
	"visibility": true, "white-space": true, "list-style-type": true,
}

func (e *styleEngine) compute(n *html.Node) *computedStyle {
	if n == nil {
		return nil
	}
	if c, ok := e.cache[n]; ok {
		return c
	}
	parent := (*computedStyle)(nil)
	if n.Parent != nil && n.Parent.Type == html.ElementNode {
		parent = e.compute(n.Parent)
	}
	cs := e.computeNode(n, parent)
	e.cache[n] = cs
	return cs
}

func (e *styleEngine) computeNode(n *html.Node, parent *computedStyle) *computedStyle {
	cs := defaultComputedStyle()
	if parent != nil {
		// Inherited properties start from the parent.
		cs.textColor = parent.textColor
		cs.fontSize = parent.fontSize
		cs.bold = parent.bold
		cs.italic = parent.italic
		cs.mono = parent.mono
		cs.whiteSpace = parent.whiteSpace
		cs.textAlign = parent.textAlign
		cs.visibility = parent.visibility
	}
	tag := n.Data

	// User-agent defaults.
	applyUADefaults(&cs, tag, parent)

	// Inline style attribute wins over the cascade for authors, but is part of
	// it: treated as specificity above any selector.
	type applied struct {
		important bool
		spec      cascadia.Specificity
		order     int
		decl      cssDecl
	}
	var matched []applied
	for _, r := range e.rules {
		if !r.sel.Match(n) {
			continue
		}
		for _, d := range r.decls {
			matched = append(matched, applied{important: d.important, spec: r.spec, order: r.order, decl: d})
		}
	}
	for _, d := range inlineDecls(n) {
		matched = append(matched, applied{important: d.important, spec: cascadia.Specificity{1 << 20, 0, 0}, order: 1 << 30, decl: d})
	}
	sort.SliceStable(matched, func(i, j int) bool {
		a, b := matched[i], matched[j]
		if a.important != b.important {
			return !a.important
		}
		if a.spec.Less(b.spec) {
			return true
		}
		if b.spec.Less(a.spec) {
			return false
		}
		return a.order < b.order
	})

	declared := map[string]string{}
	for _, m := range matched {
		declared[m.decl.prop] = m.decl.val
	}
	// Inherited values from the parent where the element declares nothing.
	if parent != nil {
		if _, ok := declared["color"]; !ok && cs.textColor == (color.RGBA{}) {
			cs.textColor = parent.textColor
		}
	}

	e.applyDecls(&cs, declared, parent)
	return &cs
}

func (e *styleEngine) applyDecls(cs *computedStyle, d map[string]string, parent *computedStyle) {
	base := cs.fontSize
	if base <= 0 {
		base = e.baseSize
	}
	if v, ok := d["color"]; ok {
		if c, ok2 := parseCSSColor(v); ok2 {
			cs.textColor = c
		}
	}
	if v, ok := d["background-color"]; ok {
		if c, ok2 := parseCSSColor(v); ok2 && c.A > 0 {
			cs.background = c
			cs.hasBackground = true
		}
	}
	if v, ok := d["font-size"]; ok {
		if px, ok2 := cssFontSizeToPx(v, base); ok2 {
			cs.fontSize = px
		}
	}
	if v, ok := d["font-weight"]; ok {
		lv := strings.ToLower(strings.TrimSpace(v))
		if lv == "bold" || lv == "bolder" {
			cs.bold = true
		} else if n, err := strconv.Atoi(lv); err == nil {
			cs.bold = n >= 600
		} else if lv == "normal" || lv == "lighter" {
			cs.bold = false
		}
	}
	if v, ok := d["font-style"]; ok {
		lv := strings.ToLower(v)
		cs.italic = strings.Contains(lv, "italic") || strings.Contains(lv, "oblique")
	}
	if v, ok := d["font-family"]; ok {
		cs.mono = cssFamilyIsMono(v)
	}
	if v, ok := d["text-decoration-line"]; ok {
		lv := strings.ToLower(v)
		cs.underline = strings.Contains(lv, "underline")
		cs.strike = strings.Contains(lv, "line-through")
	} else if v, ok := d["text-decoration"]; ok {
		lv := strings.ToLower(v)
		cs.underline = strings.Contains(lv, "underline")
		cs.strike = strings.Contains(lv, "line-through")
	}
	if v, ok := d["text-align"]; ok {
		lv := strings.ToLower(strings.TrimSpace(v))
		switch lv {
		case "left", "center", "right", "justify", "start", "end":
			if lv == "start" {
				lv = "left"
			}
			if lv == "end" {
				lv = "right"
			}
			if lv != "justify" {
				cs.textAlign = lv
			}
		}
	}
	if v, ok := d["line-height"]; ok {
		lv := strings.TrimSpace(strings.ToLower(v))
		if lv == "normal" {
			cs.lineHeight = 0
		} else if f, err := strconv.ParseFloat(lv, 64); err == nil {
			cs.lineHeight = cs.fontSize * f
		} else if px, ok2 := cssLengthToPx(lv, cs.fontSize); ok2 {
			cs.lineHeight = px
		}
	}
	if v, ok := d["white-space"]; ok {
		cs.whiteSpace = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := d["display"]; ok {
		lv := strings.ToLower(strings.TrimSpace(v))
		switch lv {
		case "none", "block", "inline", "inline-block", "flex", "grid",
			"list-item", "table", "table-row", "table-cell", "inline-flex", "contents":
			cs.display = lv
		}
	}
	if v, ok := d["visibility"]; ok {
		lv := strings.ToLower(strings.TrimSpace(v))
		if lv == "hidden" || lv == "collapse" || lv == "visible" {
			cs.visibility = lv
		}
	}
	if v, ok := d["list-style-type"]; ok {
		cs.listStyle = strings.ToLower(strings.TrimSpace(v))
	}
	for _, side := range []struct {
		prop string
		dst  *float64
	}{
		{"margin-top", &cs.marginTop},
		{"margin-bottom", &cs.marginBottom},
		{"margin-left", &cs.marginLeft},
		{"padding-left", &cs.paddingLeft},
	} {
		if v, ok := d[side.prop]; ok {
			if px, ok2 := cssLengthToPx(v, base); ok2 {
				*side.dst = px
			}
		}
	}
}

func cssFamilyIsMono(v string) bool {
	lv := strings.ToLower(v)
	for _, mono := range []string{"monospace", "mono", "courier", "consol", "menlo", "monaco", "fixed", "source code", "ubuntu mono"} {
		if strings.Contains(lv, mono) {
			return true
		}
	}
	return false
}

// applyUADefaults is the built-in stylesheet: what a browser applies before any
// author CSS. Author rules override these through the cascade.
func applyUADefaults(cs *computedStyle, tag string, parent *computedStyle) {
	// Display.
	switch tag {
	case "head", "title", "meta", "link", "style", "script", "template",
		"noscript", "base", "param", "source", "track", "col", "colgroup",
		"iframe", "svg", "canvas", "audio", "video", "object", "embed",
		"input", "textarea", "select", "option", "dialog":
		cs.display = "none"
	case "td", "th":
		cs.display = "table-cell"
	case "li":
		cs.display = "list-item"
	case "html", "body", "address", "article", "aside", "blockquote", "div",
		"dl", "dd", "dt", "fieldset", "figcaption", "figure", "footer", "form",
		"header", "main", "nav", "p", "pre", "section", "table", "tbody",
		"tfoot", "thead", "tr", "hgroup", "details", "summary", "caption",
		"ul", "ol", "hr", "h1", "h2", "h3", "h4", "h5", "h6":
		cs.display = "block"
	}

	// Typography.
	switch tag {
	case "h1":
		cs.fontSize, cs.bold = 32, true
	case "h2":
		cs.fontSize, cs.bold = 24, true
	case "h3":
		cs.fontSize, cs.bold = 18.7, true
	case "h4":
		cs.fontSize, cs.bold = 16, true
	case "h5":
		cs.fontSize, cs.bold = 13.3, true
	case "h6":
		cs.fontSize, cs.bold = 10.7, true
	case "b", "strong":
		cs.bold = true
	case "i", "em", "cite", "var", "dfn", "address":
		cs.italic = true
	case "code", "kbd", "samp", "tt", "pre":
		cs.mono = true
		if cs.fontSize > 15 {
			cs.fontSize = 13
		}
	case "small":
		cs.fontSize = math.Max(9, cs.fontSize*0.83)
	case "u", "ins":
		cs.underline = true
	case "a":
		cs.link = true
		cs.underline = true
		cs.textColor = renderLinkColor
	}
	if tag == "pre" {
		cs.whiteSpace = "pre"
	}

	// Boxes.
	switch tag {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		cs.marginTop = 0.75 * cs.fontSize
		cs.marginBottom = 0.6 * cs.fontSize
	case "body":
		cs.marginTop, cs.marginBottom = 8, 8
		cs.marginLeft = 8
	case "ul", "ol":
		cs.marginTop, cs.marginBottom = 16, 16
		cs.paddingLeft = 40
	case "blockquote":
		cs.marginTop, cs.marginBottom = 16, 16
		cs.marginLeft, cs.paddingLeft = 40, 10
	case "hr":
		cs.marginTop, cs.marginBottom = 8, 8
	case "p", "pre", "dl", "figure", "table":
		cs.marginTop, cs.marginBottom = 16, 16
	}

	// List markers.
	switch tag {
	case "ul":
		cs.listStyle = "disc"
	case "ol":
		cs.listStyle = "decimal"
	}

}

// inlineDecls parses an element's style attribute.
func inlineDecls(n *html.Node) []cssDecl {
	v, ok := getAttr(n, "style")
	if !ok || v == "" {
		return nil
	}
	return parseCSSDeclarations(v)
}
