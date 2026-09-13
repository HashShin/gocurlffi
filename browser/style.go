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
	return cssLengthToPxV(v, base, 0)
}

// cssLengthToPxV resolves a length, additionally resolving vw against a
// viewport width (0 means the viewport is unknown and vw lengths fail). It also
// understands the CSS math functions clamp(), min() and max(), whose arguments
// are lengths in the same grammar.
func cssLengthToPxV(v string, base, vw float64) (float64, bool) {
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
	if name, inner, ok := cssFunctionCall(v); ok {
		return cssMathFunction(name, inner, base, vw)
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
	case "vw":
		if vw <= 0 {
			return 0, false
		}
		return f * vw / 100, true
	}
	return 0, false
}

// cssFunctionCall splits "name(inner)" into its parts, or reports false for a
// plain value. The inner text keeps any nested parentheses.
func cssFunctionCall(v string) (name, inner string, ok bool) {
	i := strings.IndexByte(v, '(')
	if i <= 0 || !strings.HasSuffix(v, ")") {
		return "", "", false
	}
	return strings.TrimSpace(v[:i]), v[i+1 : len(v)-1], true
}

// cssMathFunction evaluates clamp(), min() and max(). A single unresolvable
// argument makes the whole expression unresolvable, matching a browser that
// would drop the declaration.
func cssMathFunction(name, inner string, base, vw float64) (float64, bool) {
	switch name {
	case "clamp", "min", "max":
	default:
		return 0, false
	}
	args := splitTopLevel(inner, ',')
	vals := make([]float64, 0, len(args))
	for _, a := range args {
		x, ok := cssLengthToPxV(a, base, vw)
		if !ok {
			return 0, false
		}
		vals = append(vals, x)
	}
	switch name {
	case "clamp":
		if len(vals) != 3 {
			return 0, false
		}
		lo, val, hi := vals[0], vals[1], vals[2]
		if val < lo {
			val = lo
		}
		if val > hi {
			val = hi
		}
		return val, true
	case "min":
		if len(vals) == 0 {
			return 0, false
		}
		m := vals[0]
		for _, x := range vals[1:] {
			if x < m {
				m = x
			}
		}
		return m, true
	default: // max
		if len(vals) == 0 {
			return 0, false
		}
		m := vals[0]
		for _, x := range vals[1:] {
			if x > m {
				m = x
			}
		}
		return m, true
	}
}

// cssSizeParts resolves a length that may be a percentage, a calc() over a
// percentage and a fixed length, or a plain length. It returns the percentage
// as a fraction (0.5 for 50%) plus the fixed px part.
func cssSizeParts(v string, base, vw float64) (pct, px float64, ok bool) {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" || v == "auto" || v == "none" || v == "content" {
		return 0, 0, false
	}
	if strings.HasSuffix(v, "%") {
		if f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64); err == nil {
			return f / 100, 0, true
		}
		return 0, 0, false
	}
	if name, inner, isFn := cssFunctionCall(v); isFn && name == "calc" {
		return cssCalcParts(inner, vw)
	}
	if x, ok2 := cssLengthToPxV(v, base, vw); ok2 {
		return 0, x, true
	}
	return 0, 0, false
}

// cssCalcParts resolves the inside of a calc() to a percentage plus a fixed
// length, which is the form flex-basis and width need. Only addition and
// subtraction of length atoms are understood.
func cssCalcParts(inner string, vw float64) (pct, px float64, ok bool) {
	terms, signs := splitCalcTerms(inner)
	if len(terms) == 0 {
		return 0, 0, false
	}
	for i, term := range terms {
		p, x, okAtom := calcAtom(term, vw)
		if !okAtom {
			return 0, 0, false
		}
		pct += signs[i] * p
		px += signs[i] * x
	}
	return pct, px, true
}

// splitCalcTerms splits a calc() body on top-level + and -, returning each term
// and the sign that applies to it.
func splitCalcTerms(s string) (terms []string, signs []float64) {
	var cur strings.Builder
	sign := 1.0
	depth := 0
	flush := func() {
		if t := strings.TrimSpace(cur.String()); t != "" {
			terms = append(terms, t)
			signs = append(signs, sign)
		}
		cur.Reset()
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '(':
			depth++
			cur.WriteByte(c)
		case c == ')':
			depth--
			cur.WriteByte(c)
		case depth == 0 && (c == '+' || c == '-') && i > 0 && (s[i-1] == ' ' || s[i-1] == ')'):
			flush()
			if c == '-' {
				sign = -1
			} else {
				sign = 1
			}
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return terms, signs
}

// calcAtom resolves one term of a calc(): a percentage, or a length.
func calcAtom(term string, vw float64) (pct, px float64, ok bool) {
	term = strings.TrimSpace(strings.ToLower(term))
	if strings.HasSuffix(term, "%") {
		f, err := strconv.ParseFloat(strings.TrimSuffix(term, "%"), 64)
		if err != nil {
			return 0, 0, false
		}
		return f / 100, 0, true
	}
	if x, ok2 := cssLengthToPxV(term, 16, vw); ok2 {
		return 0, x, true
	}
	return 0, 0, false
}

// normalizeFlexAlign reduces the alignment keywords to the few the layout
// distinguishes: start, center, end, stretch and the space-* values.
func normalizeFlexAlign(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "center":
		return "center"
	case "flex-end", "end", "right":
		return "end"
	case "flex-start", "start", "left":
		return "start"
	case "space-between":
		return "space-between"
	case "space-around":
		return "space-around"
	case "space-evenly":
		return "space-evenly"
	case "stretch":
		return "stretch"
	case "baseline":
		return "baseline"
	}
	return ""
}

// applyFlexShorthand reads the flex shorthand: the grow factor, the shrink
// factor and the basis. "flex: 1" grows, "flex: none" does not, and a later
// length or percentage is the basis.
func applyFlexShorthand(cs *computedStyle, v string, base, vw float64) {
	lv := strings.ToLower(v)
	// "flex: 0 0 calc(50% - 7px)" contains spaces inside calc(), so the value
	// cannot be split on whitespace. Take the calc() expression whole and read
	// the grow factor from the tokens before it.
	if i := strings.Index(lv, "calc("); i >= 0 {
		if head := strings.Fields(strings.TrimSpace(lv[:i])); len(head) > 0 {
			if f, err := strconv.ParseFloat(head[0], 64); err == nil {
				cs.flexGrow = f
			}
		}
		if expr, ok := balancedCall(lv[i:]); ok {
			if pct, px, ok2 := cssSizeParts(expr, base, vw); ok2 {
				cs.flexBasisPct, cs.flexBasisPx, cs.hasFlexBasis = pct, px, true
			}
		}
		return
	}
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return
	}
	switch strings.ToLower(fields[0]) {
	case "none":
		cs.flexGrow = 0
		return
	case "auto":
		cs.flexGrow = 1
		return
	}
	if f, err := strconv.ParseFloat(fields[0], 64); err == nil {
		cs.flexGrow = f
	}
	for _, tok := range fields[1:] {
		if strings.EqualFold(tok, "auto") {
			continue
		}
		// A bare number is the shrink factor, not the basis.
		if _, unit := splitCSSNumber(strings.ToLower(tok)); unit == "" {
			if _, err := strconv.ParseFloat(tok, 64); err == nil {
				continue
			}
		}
		if pct, px, ok := cssSizeParts(tok, base, vw); ok {
			cs.flexBasisPct, cs.flexBasisPx, cs.hasFlexBasis = pct, px, true
		}
	}
}

// balancedCall returns the function call at the start of s, from its name
// through the matching close parenthesis.
func balancedCall(s string) (string, bool) {
	open := strings.IndexByte(s, '(')
	if open <= 0 {
		return "", false
	}
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[:i+1], true
			}
		}
	}
	return "", false
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

// The absolute font-size keywords, keyed to the medium (16px) size. Browsers
// take these from a fixed table rather than scaling by a ratio, which is what
// Chromium reports through getComputedStyle (small is 13px, not 13.28px).
var cssFontSizeTable = []float64{9, 10, 13, 16, 18, 24, 32, 48}

var cssFontSizeKeyword = map[string]int{
	"xx-small": 0, "x-small": 1, "small": 2, "medium": 3,
	"large": 4, "x-large": 5, "xx-large": 6, "xxx-large": 7,
}

// cssFontSizeToPx resolves a font-size declaration. parent is the parent
// element's computed size, which is the base for percentages, em and the
// smaller/larger keywords. The absolute keywords instead come from the table
// above: "large" is 18px whether the parent is 16px or 60px.
func cssFontSizeToPx(v string, parent float64) (float64, bool) {
	return cssFontSizeToPxV(v, parent, 0)
}

// cssFontSizeToPxV is cssFontSizeToPx with a viewport width for vw units.
func cssFontSizeToPxV(v string, parent, vw float64) (float64, bool) {
	kw := strings.ToLower(strings.TrimSpace(v))
	if i, ok := cssFontSizeKeyword[kw]; ok {
		return cssFontSizeTable[i], true
	}
	switch kw {
	case "smaller":
		return parent / 1.2, true
	case "larger":
		return parent * 1.2, true
	}
	return cssLengthToPxV(v, parent, vw)
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
	// weight is the computed font-weight, 100..900. bold is the coarse flag
	// the renderer uses, kept in step with it.
	weight int
	bold   bool
	italic bool
	mono   bool
	// fontFamily is the first family of the computed font-family, which is the
	// name a @font-face is matched against. Empty means the user-agent default.
	fontFamily string
	underline  bool
	strike     bool
	link       bool
	textAlign  string
	lineHeight float64
	whiteSpace string
	// textTransform is uppercase/lowercase/capitalize, "" for none.
	textTransform string
	// letterSpacing is extra space between characters, in px.
	letterSpacing float64
	marginTop     float64
	marginBottom  float64
	marginLeft    float64
	paddingTop    float64
	paddingBottom float64
	paddingLeft   float64
	listStyle     string

	// Flex container and item properties the row layout uses.
	flexDirection string // row (default), column
	flexWrap      bool
	flexGrow      float64
	// flexBasisPx and flexBasisPct are the two parts of the item's main size:
	// a percentage of the container plus a fixed length. calc() over the two
	// is the common form ("flex: 0 0 calc(50% - 7px)").
	flexBasisPx    float64
	flexBasisPct   float64
	hasFlexBasis   bool
	widthPx        float64
	widthPct       float64
	hasWidth       bool
	heightPx       float64
	heightPct      float64
	hasHeight      bool
	columnGap      float64
	rowGap         float64
	justifyContent string
	alignItems     string

	// monoDefault records that the user-agent sheet gave this element its
	// 13px monospace size, which an author font-family takes away again.
	monoDefault bool

	// props are the CSS custom properties in scope on this element, which
	// var() references resolve against. The chain is shared with the parent,
	// so inheriting them costs nothing until an element declares its own.
	props *customProps
}

// customProps is an immutable chain of custom property values: each link holds
// the names an element declares, and looks up through its parent.
type customProps struct {
	parent *customProps
	own    map[string]string
}

func (c *customProps) lookup(name string) (string, bool) {
	for p := c; p != nil; p = p.parent {
		if v, ok := p.own[name]; ok {
			return v, true
		}
	}
	return "", false
}

// expandVars substitutes var(--name[, fallback]) references. depth bounds the
// recursion so that a cycle terminates. A reference with no value and no
// fallback makes the declaration invalid, which the caller drops.
func expandVars(v string, props *customProps, depth int) (string, bool) {
	if props == nil || !strings.Contains(v, "var(") {
		return v, true
	}
	if depth > 8 {
		return "", false
	}
	var b strings.Builder
	for i := 0; i < len(v); {
		start := indexFold(v[i:], "var(")
		if start < 0 {
			b.WriteString(v[i:])
			break
		}
		start += i
		b.WriteString(v[i:start])
		open := start + 3 // the '('
		end := matchingParen(v, open)
		if end < 0 {
			return "", false
		}
		name, fallback, hasFallback := cutTopLevelArg(v[open+1 : end])
		name = strings.TrimSpace(name)
		val, ok := props.lookup(name)
		if !ok {
			if !hasFallback {
				return "", false
			}
			val = fallback
		} else if strings.TrimSpace(val) == "" && hasFallback {
			val = fallback
		}
		expanded, ok := expandVars(strings.TrimSpace(val), props, depth+1)
		if !ok {
			return "", false
		}
		b.WriteString(expanded)
		i = end + 1
	}
	return b.String(), true
}

// indexFold finds needle case-insensitively.
func indexFold(s, needle string) int {
	if len(needle) == 0 {
		return 0
	}
	low := strings.ToLower(s)
	return strings.Index(low, strings.ToLower(needle))
}

// matchingParen returns the index of the ')' matching the '(' at open.
func matchingParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// cutTopLevelArg splits a var() argument into its name and fallback at the
// first comma outside parentheses.
func cutTopLevelArg(s string) (name, fallback string, ok bool) {
	parts := splitTopLevel(s, ',')
	if len(parts) == 0 {
		return "", "", false
	}
	if len(parts) == 1 {
		return parts[0], "", false
	}
	return parts[0], strings.Join(parts[1:], ","), true
}

func defaultComputedStyle() computedStyle {
	return computedStyle{
		display:    "inline",
		visibility: "visible",
		textColor:  renderTextColor,
		fontSize:   16,
		weight:     400,
		whiteSpace: "normal",
		textAlign:  "left",
	}
}

// setWeight records a computed font weight and the renderer's bold flag.
func (cs *computedStyle) setWeight(w int) {
	cs.weight = w
	cs.bold = w >= 600
}

// --- engine ---

type styleEngine struct {
	rules    []cssRule
	cache    map[*html.Node]*computedStyle
	baseSize float64
	width    float64
	quirks   bool
}

func newStyleEngine(rules []cssRule, width float64, quirks bool) *styleEngine {
	return &styleEngine{
		rules:    rules,
		cache:    map[*html.Node]*computedStyle{},
		baseSize: 16,
		width:    width,
		quirks:   quirks,
	}
}

// mediumSize is the "medium" font size for this element's context. Browsers
// resolve the absolute keywords against it, and in quirks mode it is 13px for
// monospace text rather than 16px.
func (e *styleEngine) mediumSize(parent *computedStyle) float64 {
	if parent != nil && parent.mono {
		return 13
	}
	return e.baseSize
}

var styleInherited = map[string]bool{
	"color": true, "font-family": true, "font-size": true, "font-weight": true,
	"font-style": true, "line-height": true, "text-align": true,
	"visibility": true, "white-space": true, "list-style-type": true,
	"text-transform": true, "letter-spacing": true,
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
		cs.bold, cs.weight = parent.bold, parent.weight
		cs.italic = parent.italic
		cs.mono = parent.mono
		cs.fontFamily = parent.fontFamily
		cs.whiteSpace = parent.whiteSpace
		cs.textAlign = parent.textAlign
		cs.visibility = parent.visibility
		cs.textTransform = parent.textTransform
		cs.letterSpacing = parent.letterSpacing
	}
	tag := n.Data

	// User-agent defaults.
	applyUADefaults(&cs, n, tag, parent)

	// A document without a doctype is in quirks mode, where browsers give
	// tables the medium font size and normal weight and style instead of
	// inheriting them. Old table-layout pages depend on this: without it a
	// 10pt body would shrink every table on the page.
	if e.quirks && tag == "table" {
		cs.fontSize = e.mediumSize(parent)
		cs.setWeight(400)
		cs.italic = false
		cs.textAlign = "left"
	}

	// HTML presentational attributes (bgcolor, color, align) act as author
	// rules of the lowest priority, so they sit above the user-agent sheet and
	// below the cascade. Old table-layout pages depend on them: Hacker News
	// draws its orange bar with bgcolor="#ff6600".
	applyPresentationalHints(&cs, n, tag)

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

	// Custom properties cascade and inherit like any other declaration, but
	// they must be collected before the rest, because those declarations can
	// reference them through var(). Modern pages theme almost everything this
	// way: Wikipedia sets "color: var(--color-base, #202122)" on body and
	// Bootstrap 5 styles every component with --bs-* variables.
	own := map[string]string{}
	for _, m := range matched {
		if strings.HasPrefix(m.decl.prop, "--") {
			own[m.decl.prop] = m.decl.val
		}
	}
	parentProps := (*customProps)(nil)
	if parent != nil {
		parentProps = parent.props
	}
	props := &customProps{parent: parentProps, own: own}
	for name, val := range own {
		if expanded, ok := expandVars(val, props, 0); ok {
			own[name] = expanded
		} else {
			delete(own, name)
		}
	}
	cs.props = props

	// Then the ordinary declarations, in cascade order, so that the winning
	// shorthand or longhand is the last to write each longhand property.
	declared := map[string]string{}
	for _, m := range matched {
		if strings.HasPrefix(m.decl.prop, "--") {
			continue
		}
		val, ok := expandVars(m.decl.val, props, 0)
		if !ok {
			// A var() with no value and no fallback makes the declaration
			// invalid at computed-value time, so it does not apply.
			continue
		}
		for _, d := range expandShorthands([]cssDecl{{prop: m.decl.prop, val: val, important: m.decl.important}}) {
			declared[d.prop] = d.val
		}
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
	// "color: inherit" and friends reset a property to the parent's computed
	// value instead of setting one. Pages use it to opt out of a default, as
	// Wikipedia's "a.mw-selflink { color: inherit }" does to make the current
	// page link look like text.
	for prop, val := range d {
		if !strings.EqualFold(strings.TrimSpace(val), "inherit") || parent == nil {
			continue
		}
		switch prop {
		case "color":
			cs.textColor = parent.textColor
		case "background-color":
			cs.background, cs.hasBackground = parent.background, parent.hasBackground
		case "font-size":
			cs.fontSize = parent.fontSize
		case "font-weight":
			cs.setWeight(parent.weight)
		case "font-style":
			cs.italic = parent.italic
		case "font-family":
			cs.mono = parent.mono
			cs.fontFamily = parent.fontFamily
		case "text-align":
			cs.textAlign = parent.textAlign
		case "line-height":
			cs.lineHeight = parent.lineHeight
		case "visibility":
			cs.visibility = parent.visibility
		case "white-space":
			cs.whiteSpace = parent.whiteSpace
		case "text-transform":
			cs.textTransform = parent.textTransform
		case "letter-spacing":
			cs.letterSpacing = parent.letterSpacing
		}
		delete(d, prop)
	}
	// A font-size percentage or em resolves against the parent's computed
	// size, not this element's own: cs.fontSize already holds the user-agent
	// size (32px for an h1), and using it would turn "h1 { font-size: 2em }"
	// into 64px and Bootstrap's "small { font-size: 87% }" into 87% of the
	// user-agent 13px instead of 87% of the parent.
	inherited := e.baseSize
	if parent != nil && parent.fontSize > 0 {
		inherited = parent.fontSize
	}
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
		if c, ok2 := parseCSSColor(v); ok2 {
			// An explicit transparent colour clears the background, including
			// the user-agent face a form control gets. Skipping the alpha-0
			// value here left buttons painted with the default face even
			// though the page asked for "background: transparent".
			cs.background = c
			cs.hasBackground = c.A > 0
		}
	}
	if v, ok := d["font-size"]; ok {
		if px, ok2 := cssFontSizeToPxV(v, inherited, e.width); ok2 {
			cs.fontSize = px
		}
	}
	if v, ok := d["font-weight"]; ok {
		switch lv := strings.ToLower(strings.TrimSpace(v)); {
		case lv == "normal":
			cs.setWeight(400)
		case lv == "bold":
			cs.setWeight(700)
		case lv == "bolder":
			cs.setWeight(bolderWeight(cs.weight))
		case lv == "lighter":
			cs.setWeight(lighterWeight(cs.weight))
		default:
			if n, err := strconv.Atoi(lv); err == nil {
				// Keep the number: a browser reports 600 for font-semibold,
				// and the renderer only needs to know it is not regular.
				cs.setWeight(n)
			}
		}
	}
	if v, ok := d["font-style"]; ok {
		lv := strings.ToLower(v)
		cs.italic = strings.Contains(lv, "italic") || strings.Contains(lv, "oblique")
	}
	if v, ok := d["font-family"]; ok {
		cs.mono = cssFamilyIsMono(v)
		cs.fontFamily = cssFirstFamily(v)
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
		} else if px, ok2 := cssLengthToPxV(lv, cs.fontSize, e.width); ok2 {
			cs.lineHeight = px
		}
	}
	if v, ok := d["white-space"]; ok {
		cs.whiteSpace = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := d["text-transform"]; ok {
		lv := strings.ToLower(strings.TrimSpace(v))
		switch lv {
		case "uppercase", "lowercase", "capitalize", "none":
			cs.textTransform = lv
		}
	}
	if v, ok := d["letter-spacing"]; ok {
		lv := strings.TrimSpace(strings.ToLower(v))
		if lv == "normal" {
			cs.letterSpacing = 0
		} else if px, ok2 := cssLengthToPxV(lv, cs.fontSize, e.width); ok2 {
			cs.letterSpacing = px
		}
	}
	if v, ok := d["display"]; ok {
		lv := strings.ToLower(strings.TrimSpace(v))
		switch lv {
		case "none", "block", "inline", "inline-block", "flex", "grid",
			"list-item", "flow-root", "inline-grid", "inline-table", "table",
			"table-row", "table-row-group", "table-header-group",
			"table-footer-group", "table-cell", "table-caption",
			"inline-flex", "contents":
			cs.display = lv
		}
	}
	// A floated or absolutely positioned element is blockified: its computed
	// display is block, whatever the cascade said. Browsers report the
	// blockified value from getComputedStyle, so Bootstrap's
	// ".pager .next > a { float: right }" reads as display:block there while
	// the earlier "display: inline-block" still governs the used value.
	floated := false
	if v, ok := d["float"]; ok {
		lv := strings.ToLower(strings.TrimSpace(v))
		floated = lv == "left" || lv == "right"
	}
	outOfFlow := false
	if v, ok := d["position"]; ok {
		lv := strings.ToLower(strings.TrimSpace(v))
		outOfFlow = lv == "absolute" || lv == "fixed"
	}
	if floated || outOfFlow {
		cs.display = blockifyDisplay(cs.display)
	}
	// A flex or grid item is blockified too, which is why a <span> inside a
	// flex container reports display:block in a browser.
	if parent != nil && !outOfFlow && isFlexOrGridContainer(parent.display) {
		cs.display = blockifyDisplay(cs.display)
	}
	// Flex container properties. Only a row is laid out specially; a column
	// flex container behaves like ordinary block flow, which is what a
	// column of blocks already is.
	if v, ok := d["flex-direction"]; ok {
		lv := strings.ToLower(strings.TrimSpace(v))
		switch {
		case strings.HasPrefix(lv, "column"):
			cs.flexDirection = "column"
		case strings.HasPrefix(lv, "row"):
			cs.flexDirection = "row"
		}
	}
	if v, ok := d["flex-wrap"]; ok {
		lv := strings.ToLower(strings.TrimSpace(v))
		cs.flexWrap = strings.Contains(lv, "wrap") && !strings.Contains(lv, "nowrap")
	}
	if v, ok := d["justify-content"]; ok {
		cs.justifyContent = normalizeFlexAlign(v)
	}
	if v, ok := d["align-items"]; ok {
		cs.alignItems = normalizeFlexAlign(v)
	}
	if v, ok := d["column-gap"]; ok {
		if px, ok2 := cssLengthToPxV(v, base, e.width); ok2 {
			cs.columnGap = px
		}
	}
	if v, ok := d["row-gap"]; ok {
		if px, ok2 := cssLengthToPxV(v, base, e.width); ok2 {
			cs.rowGap = px
		}
	}
	if v, ok := d["gap"]; ok {
		parts := strings.Fields(v)
		switch len(parts) {
		case 1:
			if px, ok2 := cssLengthToPxV(parts[0], base, e.width); ok2 {
				cs.rowGap, cs.columnGap = px, px
			}
		case 2:
			if px, ok2 := cssLengthToPxV(parts[0], base, e.width); ok2 {
				cs.rowGap = px
			}
			if px, ok2 := cssLengthToPxV(parts[1], base, e.width); ok2 {
				cs.columnGap = px
			}
		}
	}
	if v, ok := d["flex-basis"]; ok {
		if pct, px, ok2 := cssSizeParts(v, base, e.width); ok2 {
			cs.flexBasisPct, cs.flexBasisPx, cs.hasFlexBasis = pct, px, true
		}
	}
	if v, ok := d["width"]; ok {
		if pct, px, ok2 := cssSizeParts(v, base, e.width); ok2 {
			cs.widthPct, cs.widthPx, cs.hasWidth = pct, px, true
		}
	}
	if v, ok := d["height"]; ok {
		if pct, px, ok2 := cssSizeParts(v, base, e.width); ok2 {
			cs.heightPct, cs.heightPx, cs.hasHeight = pct, px, true
		}
	}
	if v, ok := d["flex-grow"]; ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			cs.flexGrow = f
		}
	}
	if v, ok := d["flex"]; ok {
		applyFlexShorthand(cs, v, base, e.width)
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
		{"padding-top", &cs.paddingTop},
		{"padding-bottom", &cs.paddingBottom},
		{"padding-left", &cs.paddingLeft},
	} {
		if v, ok := d[side.prop]; ok {
			if px, ok2 := cssLengthToPxV(v, base, e.width); ok2 {
				*side.dst = px
			}
		}
	}
	// A browser keeps its 13px monospace size for <pre> and <code> only while
	// the font family is exactly the `monospace` keyword. Any other author
	// family, including "monospace, monospace" or a quoted "monospace",
	// restores the inherited size. Wikipedia's
	// "pre,code,tt,kbd,samp{font-family:monospace,monospace}" turns on this:
	// without it every inline code sample renders at 13px instead of 16px.
	if cs.monoDefault && cs.fontSize == 13 {
		if _, hasSize := d["font-size"]; !hasSize {
			if fam, hasFam := d["font-family"]; hasFam && !strings.EqualFold(strings.TrimSpace(fam), "monospace") {
				cs.fontSize = inherited
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
func applyUADefaults(cs *computedStyle, n *html.Node, tag string, parent *computedStyle) {
	// Display.
	switch tag {
	case "head", "title", "meta", "link", "style", "script", "template",
		"noscript", "base", "param", "source", "track", "col", "colgroup",
		"dialog":
		cs.display = "none"
	case "iframe", "svg", "canvas", "audio", "video", "object", "embed",
		"input", "textarea", "select", "button":
		cs.display = "inline-block"
	case "option":
		cs.display = "block"
	case "table":
		cs.display = "table"
	case "thead", "tbody", "tfoot":
		cs.display = "table-row-group"
	case "tr":
		cs.display = "table-row"
	case "caption":
		cs.display = "table-caption"
	case "td":
		cs.display = "table-cell"
	case "th":
		cs.display = "table-cell"
		cs.setWeight(700)
		cs.textAlign = "center"
	case "li":
		cs.display = "list-item"
	case "center":
		cs.display = "block"
		cs.textAlign = "center"
	case "html", "body", "address", "article", "aside", "blockquote", "div",
		"dl", "dd", "dt", "fieldset", "figcaption", "figure", "footer", "form",
		"header", "main", "nav", "p", "pre", "section", "hgroup", "details",
		"summary", "ul", "ol", "hr", "h1", "h2", "h3", "h4", "h5", "h6":
		cs.display = "block"
	}

	// Typography.
	switch tag {
	case "h1":
		cs.fontSize = 32
		cs.setWeight(700)
	case "h2":
		cs.fontSize = 24
		cs.setWeight(700)
	case "h3":
		cs.fontSize = 18.7
		cs.setWeight(700)
	case "h4":
		cs.fontSize = 16
		cs.setWeight(700)
	case "h5":
		cs.fontSize = 13.3
		cs.setWeight(700)
	case "h6":
		cs.fontSize = 10.7
		cs.setWeight(700)
	case "th", "b", "strong":
		cs.setWeight(700)
	case "i", "em", "cite", "var", "dfn", "address":
		cs.italic = true
	case "code", "kbd", "samp", "tt", "pre":
		cs.mono = true
		if cs.fontSize > 15 {
			cs.fontSize = 13
			cs.monoDefault = true
		}
	case "small":
		// Browsers implement small as "font-size: smaller", i.e. parent/1.2;
		// Chromium reports 13.33px for it inside a 16px parent.
		cs.fontSize = math.Max(9, cs.fontSize/1.2)
	case "u", "ins":
		cs.underline = true
	case "a":
		cs.link = true
		cs.underline = true
		cs.textColor = renderLinkColor
	case "button", "input", "select", "textarea":
		// Form controls do not inherit text colour or alignment from the page.
		// A browser gives text-ish controls white on black; buttons and
		// selects keep the platform face, and a checkbox or radio is
		// transparent unless the page paints it.
		cs.textColor = color.RGBA{R: 0, G: 0, B: 0, A: 0xff}
		cs.textAlign = "left"
		cs.background = renderFieldBG
		cs.hasBackground = true
		switch tag {
		case "button":
			cs.textAlign = "center"
			cs.background = renderButtonFace
		case "select":
			cs.background = renderButtonFace
		case "input":
			switch strings.ToLower(attrOf(n, "type")) {
			case "checkbox", "radio":
				cs.hasBackground = false
			case "submit", "reset", "button":
				cs.textAlign = "center"
				cs.background = renderButtonFace
			}
		}
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

// applyPresentationalHints maps the HTML legacy presentation attributes onto
// the CSS properties they stand for. Browsers treat them as author-origin
// rules of the lowest priority, which is why an explicit author rule always
// wins and they are applied here, before the cascade.
func applyPresentationalHints(cs *computedStyle, n *html.Node, tag string) {
	if v := attrOf(n, "bgcolor"); v != "" {
		switch tag {
		case "table", "tr", "td", "th", "body", "tbody", "thead", "tfoot":
			if c, ok := parseCSSColor(v); ok {
				cs.background = c
				cs.hasBackground = true
			}
		}
	}
	if v := attrOf(n, "color"); v != "" {
		if tag == "font" || tag == "basefont" {
			if c, ok := parseCSSColor(v); ok {
				cs.textColor = c
			}
		}
	}
	if v := attrOf(n, "align"); v != "" {
		a := strings.ToLower(strings.TrimSpace(v))
		if a == "left" || a == "right" || a == "center" || a == "justify" {
			switch tag {
			case "p", "h1", "h2", "h3", "h4", "h5", "h6", "div", "td", "th",
				"tr", "table", "caption", "col", "colgroup", "thead", "tbody",
				"tfoot", "center":
				cs.textAlign = a
			}
		}
	}
}

// blockifyDisplay returns the computed display of a floated or absolutely
// positioned element, which CSS blockifies. Chromium reports the blockified
// value, so "float: left" on an inline-block reads as block and on a <tr> also
// block, while an <table> keeps "table" and an inline-table becomes "table".
func blockifyDisplay(d string) string {
	switch d {
	case "inline", "inline-block", "table-row-group", "table-header-group",
		"table-footer-group", "table-row", "table-cell", "table-caption",
		"table-column", "table-column-group":
		return "block"
	case "inline-table":
		return "table"
	case "inline-flex":
		return "flex"
	case "inline-grid":
		return "grid"
	}
	return d
}

// bolder and lighter step through the weight table rather than adding a fixed
// amount: bolder than 400 is 700, lighter than 700 is 400. This is the CSS
// table {100, 400, 700, 900}.
func bolderWeight(w int) int {
	switch {
	case w < 350:
		return 400
	case w < 550:
		return 700
	}
	return 900
}

func lighterWeight(w int) int {
	switch {
	case w < 550:
		return 100
	case w < 750:
		return 400
	}
	return 700
}

// isFlexOrGridContainer reports whether a display value makes its children
// flex or grid items, which are blockified.
func isFlexOrGridContainer(display string) bool {
	switch display {
	case "flex", "inline-flex", "grid", "inline-grid":
		return true
	}
	return false
}
