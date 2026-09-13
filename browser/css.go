package browser

import (
	"math"
	"strconv"
	"strings"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
)

// A small CSS engine: enough of tokenizing, rule parsing, media evaluation,
// shorthand expansion and cascade to drive styling. It is not a full CSS
// implementation; unsupported constructs are ignored rather than approximated.

type cssDecl struct {
	prop      string
	val       string
	important bool
}

type cssRule struct {
	sel   cascadia.Selector
	spec  cascadia.Specificity
	order int
	decls []cssDecl
	// text is the selector text, kept for document.styleSheets.
	text string
}

// cssStats counts what a stylesheet contained, so a caller can tell whether a
// sheet was consumed or silently dropped. A high skipped count means most of a
// page's styling came from selectors the engine cannot compile.
type cssStats struct {
	rules     int
	selectors int
	skipped   int
	// pseudoSkipped counts the skipped selectors that target a pseudo-element
	// ("X::before", "::-webkit-inner-spin-button"). They carry no element
	// styling, so they are not a loss, and counting them with the rest makes
	// coverage look far worse than it is.
	pseudoSkipped int
	skipSample    []string
	// fontFaces are the @font-face rules found in the sheet, in source order.
	fontFaces []fontFace
}

// fontFace is one @font-face rule: a family, the weight and slope it covers,
// and the file that provides it.
type fontFace struct {
	family string
	weight int // 100..900
	italic bool
	src    string // absolute after resolution; the url() the rule names
	format string // truetype, opentype, woff, woff2, or "" when unlabelled
}

// parseCSSStylesheet parses CSS text into rules. mediaWidth is the layout width
// used to evaluate @media; if it is 0, width-dependent queries are treated as
// matching. Imports are returned for the caller to fetch first.
func parseCSSStylesheet(src string, mediaWidth float64, order *int) (rules []cssRule, imports []string, stats cssStats) {
	p := &cssParser{src: src, order: order, mediaWidth: mediaWidth}
	p.parseRules(&rules, &imports, false)
	stats.rules = len(rules)
	stats.selectors = p.selectors
	stats.skipped = p.skipped
	stats.pseudoSkipped = p.pseudoSkipped
	stats.skipSample = p.skipSample
	stats.fontFaces = p.fontFaces
	return rules, imports, stats
}

type cssParser struct {
	src           string
	pos           int
	order         *int
	mediaWidth    float64
	mediaHeightV  float64
	selectors     int      // selector texts considered
	skipped       int      // selector texts cascadia could not compile
	pseudoSkipped int      // of those, the ones targeting a pseudo-element
	skipSample    []string // a few of the rest, for diagnostics
	fontFaces     []fontFace
}

func (p *cssParser) eof() bool { return p.pos >= len(p.src) }

func (p *cssParser) skipSpace() {
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' {
			p.pos++
			continue
		}
		// comments
		if c == '/' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '*' {
			end := strings.Index(p.src[p.pos+2:], "*/")
			if end < 0 {
				p.pos = len(p.src)
				return
			}
			p.pos += 2 + end + 2
			continue
		}
		return
	}
}

// readUntil reads raw text until one of the stop bytes, honouring quotes,
// parentheses and nested braces.
func (p *cssParser) readUntil(stops string) string {
	start := p.pos
	depthParen, depthBrace := 0, 0
	var quote byte
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '\\' {
			// A CSS escape makes the next character literal, and it is used
			// outside strings too: Tailwind writes arbitrary-value classes as
			// ".font-\[\'Poppins\'\]" and ".\!text-\[14px\]". Treating that
			// quote as the start of a string swallowed the rest of the file,
			// which is how a 415KB stylesheet parsed as 1110 rules and lost
			// every rule after the first escaped selector.
			p.pos += 2
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			p.pos++
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '(':
			depthParen++
		case ')':
			if depthParen > 0 {
				depthParen--
			}
		case '{':
			if depthParen == 0 && depthBrace == 0 && strings.ContainsRune(stops, '{') {
				return p.src[start:p.pos]
			}
			depthBrace++
		case '}':
			if depthBrace > 0 {
				depthBrace--
			} else if depthParen == 0 && strings.ContainsRune(stops, '}') {
				return p.src[start:p.pos]
			}
		case ';':
			if depthParen == 0 && depthBrace == 0 && strings.ContainsRune(stops, ';') {
				return p.src[start:p.pos]
			}
		}
		p.pos++
	}
	return p.src[start:p.pos]
}

// skipBlock consumes a balanced { ... } block.
func (p *cssParser) skipBlock() {
	p.skipSpace()
	if p.eof() || p.src[p.pos] != '{' {
		return
	}
	depth := 0
	var quote byte
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '\\' {
			// A CSS escape makes the next character literal, and it is used
			// outside strings too: Tailwind writes arbitrary-value classes as
			// ".font-\[\'Poppins\'\]" and ".\!text-\[14px\]". Treating that
			// quote as the start of a string swallowed the rest of the file,
			// which is how a 415KB stylesheet parsed as 1110 rules and lost
			// every rule after the first escaped selector.
			p.pos += 2
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			p.pos++
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				p.pos++
				return
			}
		}
		p.pos++
	}
}

func (p *cssParser) parseRules(out *[]cssRule, imports *[]string, inMedia bool) {
	for {
		p.skipSpace()
		if p.eof() {
			return
		}
		c := p.src[p.pos]
		if c == '}' {
			if inMedia {
				p.pos++
				return
			}
			p.pos++
			continue
		}
		if c == '@' {
			p.parseAtRule(out, imports)
			continue
		}
		prelude := strings.TrimSpace(p.readUntil("{"))
		if prelude == "" {
			if p.eof() {
				return
			}
			p.pos++
			continue
		}
		p.readBlockInto(prelude, out)
	}
}

func (p *cssParser) parseAtRule(out *[]cssRule, imports *[]string) {
	p.pos++ // '@'
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		// An at-rule name is an identifier: stop at '{', ';', '(' or space,
		// otherwise "@font-face{...}" would swallow the rest of the file.
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			break
		}
		p.pos++
	}
	name := strings.ToLower(p.src[start:p.pos])
	prelude := strings.TrimSpace(p.readUntil("{;"))

	switch name {
	case "media":
		if p.matchMedia(prelude) {
			p.skipSpace()
			if !p.eof() && p.src[p.pos] == '{' {
				p.pos++ // enter the block
				p.parseRules(out, imports, true)
			}
		} else {
			p.skipBlock()
		}
	case "import":
		if u := cssImportURL(prelude); u != "" {
			*imports = append(*imports, u)
		}
	case "font-face":
		p.skipSpace()
		if p.eof() || p.src[p.pos] != '{' {
			break
		}
		p.pos++
		body := p.readUntil("}")
		if p.pos < len(p.src) && p.src[p.pos] == '}' {
			p.pos++
		}
		if f, ok := parseFontFace(body); ok {
			p.fontFaces = append(p.fontFaces, f)
		}
	case "supports":
		// Conservative: skip @supports blocks.
		p.skipBlock()
	default:
		// @font-face, @keyframes, @charset, @page and friends: skipped.
		if p.pos < len(p.src) {
			if p.src[p.pos] == ';' {
				p.pos++
			} else {
				p.skipBlock()
			}
		}
	}
}

func (p *cssParser) readBlockInto(prelude string, out *[]cssRule) {
	p.skipSpace()
	if p.eof() || p.src[p.pos] != '{' {
		return
	}
	p.pos++ // '{'
	body := p.readUntil("}")
	if p.pos < len(p.src) && p.src[p.pos] == '}' {
		p.pos++
	}
	decls := parseCSSDeclarations(body)
	if len(decls) == 0 {
		return
	}
	for _, selText := range splitTopLevel(prelude, ',') {
		selText = strings.TrimSpace(selText)
		if selText == "" {
			continue
		}
		p.selectors++
		// A single class selector whose name holds escapes cascadia cannot
		// match (it compiles them but never matches): Tailwind writes
		// ".font-\[\'Poppins\'\2c sans\]". Those are matched by name
		// instead, so arbitrary-value classes still style the element.
		if name, ok := escapedClassSelector(selText); ok {
			*p.order++
			*out = append(*out, cssRule{
				sel: namedClassSelector(name), spec: cssSpecificity(selText),
				order: *p.order, decls: decls, text: selText,
			})
			continue
		}
		sel, err := cascadia.Compile(selText)
		if err != nil {
			p.skipped++
			if targetsPseudoElement(selText) {
				p.pseudoSkipped++
			} else if len(p.skipSample) < 12 {
				p.skipSample = append(p.skipSample, selText)
			}
			continue
		}
		*p.order++
		*out = append(*out, cssRule{
			sel: sel, spec: cssSpecificity(selText), order: *p.order,
			decls: decls, text: selText,
		})
	}
}

func parseCSSDeclarations(body string) []cssDecl {
	var out []cssDecl
	for _, raw := range splitTopLevel(body, ';') {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		name, val, ok := strings.Cut(raw, ":")
		if !ok {
			continue
		}
		prop := strings.ToLower(strings.TrimSpace(name))
		val = strings.TrimSpace(val)
		important := false
		if idx := strings.LastIndex(strings.ToLower(val), "!important"); idx >= 0 {
			important = true
			val = strings.TrimSpace(val[:idx])
		}
		if prop == "" || val == "" {
			continue
		}
		out = append(out, cssDecl{prop: prop, val: val, important: important})
	}
	return expandShorthands(out)
}

// splitTopLevel splits on sep outside quotes, parentheses and brackets.
func splitTopLevel(s string, sep byte) []string {
	var out []string
	depth := 0
	var quote byte
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '(', '[':
			depth++
		case ')', ']':
			if depth > 0 {
				depth--
			}
		default:
			if c == sep && depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}

// matchMedia evaluates a media query list against the layout the caller asked
// about. Comma-separated queries are ORed.
func (p *cssParser) matchMedia(q string) bool {
	q = cssStripComments(q)
	if strings.TrimSpace(q) == "" {
		return true
	}
	for _, part := range splitTopLevel(q, ',') {
		if p.matchMediaQuery(part) {
			return true
		}
	}
	return false
}

// matchMediaQuery evaluates one media query: an optional "not" or "only", a
// media type, and an "and"/"or" chain of features in parentheses.
func (p *cssParser) matchMediaQuery(q string) bool {
	q = strings.ToLower(strings.Join(strings.Fields(q), " "))
	if q == "" {
		return true
	}
	negate := false
	if strings.HasPrefix(q, "not ") {
		negate = true
		q = strings.TrimSpace(q[4:])
	} else if strings.HasPrefix(q, "only ") {
		q = strings.TrimSpace(q[5:])
	}
	val, ok := p.matchMediaChain(q)
	if !ok {
		// A query we cannot parse must not match: treating it as matching
		// applies rules that only a narrower viewport should get.
		return false
	}
	if negate {
		return !val
	}
	return val
}

// matchMediaChain evaluates "and"/"or" chains of a media type and features.
func (p *cssParser) matchMediaChain(q string) (val, ok bool) {
	if i := topLevelKeyword(q, "or"); i >= 0 {
		left, ok1 := p.matchMediaChain(q[:i])
		right, ok2 := p.matchMediaChain(q[i+4:])
		if !ok1 || !ok2 {
			return false, false
		}
		return left || right, true
	}
	result, seen := true, false
	for {
		i := topLevelKeyword(q, "and")
		atom := q
		if i >= 0 {
			atom = q[:i]
			q = q[i+5:]
		}
		if atom = strings.TrimSpace(atom); atom != "" {
			v, ok := p.matchMediaAtom(atom)
			if !ok {
				return false, false
			}
			result = result && v
			seen = true
		}
		if i < 0 {
			break
		}
	}
	if !seen {
		return false, false
	}
	return result, true
}

// matchMediaAtom evaluates one part of a chain: a media type or a feature.
func (p *cssParser) matchMediaAtom(a string) (val, ok bool) {
	if strings.HasPrefix(a, "(") && strings.HasSuffix(a, ")") {
		return p.matchMediaFeature(strings.TrimSpace(a[1 : len(a)-1])), true
	}
	switch a {
	case "all", "screen":
		return true, true
	case "print", "speech", "tv", "projection", "handheld", "braille",
		"embossed", "aural", "tty":
		return false, true
	}
	// An unknown word is a syntax error, and does not match.
	return false, false
}

// matchMediaFeature evaluates a feature test such as "max-width: 750px". The
// deprecated device-* features map onto their viewport counterparts, since
// there is no separate screen geometry. A zero viewport dimension means the
// caller does not know it, and the matching features then succeed.
func (p *cssParser) matchMediaFeature(feat string) bool {
	name, val, hasVal := strings.Cut(feat, ":")
	name = strings.TrimSpace(strings.ToLower(name))
	val = strings.TrimSpace(strings.ToLower(val))
	if !hasVal {
		// Range syntax, e.g. (width >= 600px) or (600px <= width).
		return p.matchMediaRange(strings.TrimSpace(strings.ToLower(feat)))
	}
	switch name {
	case "device-width":
		name = "width"
	case "min-device-width":
		name = "min-width"
	case "max-device-width":
		name = "max-width"
	case "device-height":
		name = "height"
	case "min-device-height":
		name = "min-height"
	case "max-device-height":
		name = "max-height"
	}
	px, ok := cssLengthToPx(val, 16)
	if !ok {
		return false
	}
	switch name {
	case "width":
		return p.widthKnown() && math.Abs(p.mediaWidth-px) < 0.5
	case "min-width":
		return !p.widthKnown() || p.mediaWidth >= px
	case "max-width":
		return !p.widthKnown() || p.mediaWidth <= px
	case "height":
		return false
	case "min-height", "max-height":
		return true
	case "orientation":
		if !p.widthKnown() {
			return true
		}
		landscape := strings.HasPrefix(val, "landscape")
		return landscape == (p.mediaWidth > p.mediaHeight())
	}
	// Unknown feature: not matching is what browsers do for unsupported
	// media features, and it keeps desktop rules off a narrow page.
	return false
}

// matchMediaRange handles the "(width > 600px)" form.
func (p *cssParser) matchMediaRange(feat string) bool {
	name, op, rhs, ok := splitMediaRange(feat)
	if !ok {
		return false
	}
	px, ok := cssLengthToPx(rhs, 16)
	if !ok {
		return false
	}
	known := p.widthKnown()
	switch name {
	case "width":
		if !known {
			return true
		}
		return p.compareWidth(op, px)
	case "height":
		return false
	}
	return false
}

func (p *cssParser) compareWidth(op string, px float64) bool {
	switch op {
	case ">":
		return p.mediaWidth > px
	case ">=":
		return p.mediaWidth >= px
	case "<":
		return p.mediaWidth < px
	case "<=":
		return p.mediaWidth <= px
	case "=":
		return math.Abs(p.mediaWidth-px) < 0.5
	}
	return false
}

// splitMediaRange splits "width >= 600px" or "600px < width".
func splitMediaRange(s string) (name, op, val string, ok bool) {
	for _, o := range []string{">=", "<=", ">", "<", "="} {
		if i := strings.Index(s, o); i >= 0 {
			left := strings.TrimSpace(s[:i])
			right := strings.TrimSpace(s[i+len(o):])
			if isMediaFeatureName(left) {
				return left, o, right, true
			}
			if isMediaFeatureName(right) {
				return right, flipMediaOp(o), left, true
			}
			return "", "", "", false
		}
	}
	return "", "", "", false
}

func isMediaFeatureName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c == '-' {
			continue
		}
		return false
	}
	return true
}

func flipMediaOp(op string) string {
	switch op {
	case ">":
		return "<"
	case "<":
		return ">"
	case ">=":
		return "<="
	case "<=":
		return ">="
	}
	return op
}

func (p *cssParser) widthKnown() bool { return p.mediaWidth > 0 }

func (p *cssParser) mediaHeight() float64 { return p.mediaHeightV }

// topLevelKeyword finds " kw " outside parentheses and returns its index, or
// -1.
func topLevelKeyword(s, kw string) int {
	depth := 0
	needle := " " + kw + " "
	for i := 0; i+len(needle) <= len(s); i++ {
		switch s[i] {
		case '(':
			depth++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth == 0 && strings.HasPrefix(s[i:], needle) {
			return i
		}
	}
	return -1
}

// cssStripComments removes /* ... */ comments.
func cssStripComments(s string) string {
	for {
		i := strings.Index(s, "/*")
		if i < 0 {
			return s
		}
		j := strings.Index(s[i+2:], "*/")
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + " " + s[i+2+j+2:]
	}
}

// cssSpecificity computes [ids, classes/attrs/pseudo-classes, types/pseudo-elements]
// from a selector, since cascadia does not expose it.
func cssSpecificity(sel string) cascadia.Specificity {
	var spec cascadia.Specificity
	inCompoundStart := true
	for i := 0; i < len(sel); i++ {
		ch := sel[i]
		switch {
		case ch == '\\':
			i++
		case ch == '#':
			spec[0]++
			inCompoundStart = false
		case ch == '.':
			spec[1]++
			inCompoundStart = false
		case ch == '[':
			spec[1]++
			for i < len(sel) && sel[i] != ']' {
				i++
			}
			inCompoundStart = false
		case ch == ':':
			if i+1 < len(sel) && sel[i+1] == ':' {
				spec[2]++
				i++
			} else {
				spec[1]++
			}
			inCompoundStart = false
		case ch == '(':
			depth := 1
			for i+1 < len(sel) && depth > 0 {
				i++
				if sel[i] == '(' {
					depth++
				} else if sel[i] == ')' {
					depth--
				}
			}
			inCompoundStart = false
		case ch == ' ' || ch == '>' || ch == '+' || ch == '~' || ch == ',':
			inCompoundStart = true
		case (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '*':
			if inCompoundStart {
				if ch != '*' {
					spec[2]++
				}
				inCompoundStart = false
			}
		}
	}
	return spec
}

func cssImportURL(prelude string) string {
	prelude = strings.TrimSpace(prelude)
	if strings.HasPrefix(strings.ToLower(prelude), "url(") {
		if end := strings.Index(prelude, ")"); end > 4 {
			return strings.Trim(strings.TrimSpace(prelude[4:end]), "'\"")
		}
	}
	return strings.Trim(prelude, "'\"")
}

// --- @font-face ---

// parseFontFace reads an @font-face block body. Only the descriptors that pick
// a file matter for rendering: the family, the weight and slope it covers, and
// the url() of the best source.
func parseFontFace(body string) (fontFace, bool) {
	f := fontFace{weight: 400}
	var src, format string
	for _, d := range parseCSSDeclarations(body) {
		switch d.prop {
		case "font-family":
			f.family = cssFirstFamily(d.val)
		case "font-weight":
			f.weight = cssWeightValue(d.val, 400)
		case "font-style":
			lv := strings.ToLower(d.val)
			f.italic = strings.Contains(lv, "italic") || strings.Contains(lv, "oblique")
		case "src":
			src, format = firstFontSource(d.val)
		}
	}
	if f.family == "" || src == "" {
		return fontFace{}, false
	}
	f.src, f.format = src, format
	return f, true
}

// cssFirstFamily returns the first family name of a font-family value, without
// quotes: `"Geist Mono", sans-serif` yields "Geist Mono". The first name is the
// one a browser uses, and the one a @font-face is matched against.
func cssFirstFamily(v string) string {
	for _, part := range splitTopLevel(v, ',') {
		if name := strings.Trim(strings.TrimSpace(part), "'\""); name != "" {
			return name
		}
	}
	return ""
}

// cssWeightValue resolves a font-weight descriptor: a number, a keyword, or a
// two-value range ("100 900"), where the first number is taken.
func cssWeightValue(v string, def int) int {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "normal":
		return 400
	case "bold", "bolder":
		return 700
	case "lighter":
		return 300
	}
	if fields := strings.Fields(v); len(fields) > 0 {
		if n, err := strconv.Atoi(fields[0]); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// firstFontSource picks the best url() from a src descriptor. Raw sfnt is
// preferred, because that is what the renderer decodes; a WOFF2-only face still
// reports its URL so the failure can be named rather than hidden.
func firstFontSource(v string) (url, format string) {
	best := 1 << 30
	for _, item := range splitTopLevel(v, ',') {
		item = strings.TrimSpace(item)
		if item == "" || strings.HasPrefix(strings.ToLower(item), "local(") {
			continue
		}
		u := fontURL(item)
		if u == "" {
			continue
		}
		f := fontFormat(item)
		if rank := fontFormatRank(f, u); rank < best {
			best, url, format = rank, u, f
		}
	}
	return url, format
}

func fontURL(item string) string {
	l := strings.ToLower(item)
	i := strings.Index(l, "url(")
	if i < 0 {
		return ""
	}
	rest := item[i+4:]
	end := strings.IndexByte(rest, ')')
	if end < 0 {
		return ""
	}
	return strings.Trim(strings.TrimSpace(rest[:end]), "'\"")
}

func fontFormat(item string) string {
	l := strings.ToLower(item)
	i := strings.Index(l, "format(")
	if i < 0 {
		return ""
	}
	rest := item[i+7:]
	end := strings.IndexByte(rest, ')')
	if end < 0 {
		return ""
	}
	return strings.ToLower(strings.Trim(strings.TrimSpace(rest[:end]), "'\""))
}

// fontFormatRank orders sources by how directly this renderer can use them.
// Lower is better; raw sfnt (0) needs no conversion. Labels are matched by
// prefix so "woff2-variations" and "woff2" are both ranked as WOFF2.
func fontFormatRank(format, url string) int {
	f := strings.ToLower(strings.TrimSpace(format))
	switch {
	case strings.Contains(f, "truetype"), strings.Contains(f, "opentype"),
		f == "ttf", f == "otf", f == "sfnt":
		return 0
	case strings.HasPrefix(f, "woff2"):
		return 3
	case strings.HasPrefix(f, "woff"):
		return 2
	case f == "":
		l := strings.ToLower(url)
		switch {
		case strings.HasSuffix(l, ".ttf"), strings.HasSuffix(l, ".otf"), strings.HasSuffix(l, ".ttc"):
			return 0
		case strings.HasSuffix(l, ".woff2"):
			return 3
		case strings.HasSuffix(l, ".woff"):
			return 2
		}
	}
	return 1
}

// --- shorthand expansion ---

func expandShorthands(in []cssDecl) []cssDecl {
	var out []cssDecl
	for _, d := range in {
		if strings.Contains(d.val, "var(") {
			// Splitting "margin: var(--m, 1px 2px)" here would cut the value
			// into nonsense. Keep the shorthand and expand it in the cascade,
			// once var() has been substituted.
			out = append(out, d)
			continue
		}
		switch d.prop {
		case "margin", "padding":
			parts := strings.Fields(d.val)
			var t, r, b, l string
			switch len(parts) {
			case 1:
				t, r, b, l = parts[0], parts[0], parts[0], parts[0]
			case 2:
				t, b = parts[0], parts[0]
				r, l = parts[1], parts[1]
			case 3:
				t, r, l, b = parts[0], parts[1], parts[1], parts[2]
			case 4:
				t, r, b, l = parts[0], parts[1], parts[2], parts[3]
			default:
				continue
			}
			for _, kv := range []struct{ suffix, v string }{
				{"-top", t}, {"-right", r}, {"-bottom", b}, {"-left", l},
			} {
				out = append(out, cssDecl{prop: d.prop + kv.suffix, val: kv.v, important: d.important})
			}
		case "background":
			if c := cssColorToken(d.val); c != "" {
				out = append(out, cssDecl{prop: "background-color", val: c, important: d.important})
			}
		case "text-decoration":
			ds := strings.ToLower(d.val)
			switch {
			case strings.Contains(ds, "underline"):
				out = append(out, cssDecl{prop: "text-decoration-line", val: "underline", important: d.important})
			case strings.Contains(ds, "line-through"):
				out = append(out, cssDecl{prop: "text-decoration-line", val: "line-through", important: d.important})
			case strings.Contains(ds, "none"):
				out = append(out, cssDecl{prop: "text-decoration-line", val: "none", important: d.important})
			}
		case "font":
			out = append(out, expandFontShorthand(d)...)
		case "border-top", "border-bottom", "border-left", "border-right":
			// Only a uniform border is drawn; a single side would need the box
			// model, and applying one side's width as a uniform border would be
			// wrong.
			continue
		case "grid", "flex-flow", "place-items", "columns":
			// Grid and the remaining flow shorthands are not laid out; skipping
			// them avoids inventing spacing. gap and flex are kept: the cascade
			// turns them into the row layout's inputs.
			continue
		default:
			out = append(out, d)
		}
	}
	return out
}

// expandFontShorthand pulls style, weight, size, line-height and family out of
// a `font` shorthand. The grammar is:
//
//	[ <style> || <variant> || <weight> || <stretch> ]? <size> [ / <line-height> ]? <family>
//
// so a token before the size is a weight or style, and the size must carry a
// unit or be a keyword: a bare number there is the weight. Treating "600" as a
// size is how ".leoButton { font: var(--leo-font-components-button-large) }"
// lost the button's semibold weight on brave.com.
func expandFontShorthand(d cssDecl) []cssDecl {
	var out []cssDecl
	parts := strings.Fields(d.val)
	sizeIdx := -1
	for i, tok := range parts {
		base, _, _ := strings.Cut(tok, "/")
		base = strings.ToLower(base)
		if isFontSizeToken(base) {
			sizeIdx = i
			break
		}
		switch {
		case base == "italic" || base == "oblique" || strings.HasPrefix(base, "oblique"):
			out = append(out, cssDecl{prop: "font-style", val: "italic", important: d.important})
		case base == "bold" || base == "bolder" || base == "lighter" || isFontWeightNumber(base):
			out = append(out, cssDecl{prop: "font-weight", val: base, important: d.important})
		case base == "normal" || base == "small-caps" || base == "all-small-caps" ||
			base == "condensed" || base == "expanded" || base == "semi-condensed" ||
			base == "ultra-condensed":
			// font-variant, font-stretch or the "normal" that stands for any
			// of the prefix properties: none of them change what we render.
		default:
			// An unrecognised token before the size means this shorthand is
			// not one we understand, so it contributes nothing.
			return nil
		}
	}
	if sizeIdx < 0 {
		return nil
	}
	size, lineHeight, _ := strings.Cut(parts[sizeIdx], "/")
	if px, ok := cssFontSizeToPx(size, 16); ok {
		out = append(out, cssDecl{prop: "font-size", val: formatPx(px), important: d.important})
	}
	if lineHeight != "" {
		out = append(out, cssDecl{prop: "line-height", val: lineHeight, important: d.important})
	}
	if fam := strings.Join(parts[sizeIdx+1:], " "); fam != "" {
		out = append(out, cssDecl{prop: "font-family", val: fam, important: d.important})
	}
	return out
}

// isFontSizeToken reports whether a token is the size in a `font` shorthand.
func isFontSizeToken(tok string) bool {
	if tok == "" {
		return false
	}
	if _, ok := cssFontSizeKeyword[tok]; ok {
		return true
	}
	if tok == "smaller" || tok == "larger" {
		return true
	}
	if tok == "0" {
		return true
	}
	num, unit := splitCSSNumber(tok)
	return num != "" && unit != ""
}

// isFontWeightNumber reports whether a token is a numeric font weight.
func isFontWeightNumber(tok string) bool {
	n, err := strconv.Atoi(tok)
	return err == nil && n >= 1 && n <= 1000
}

func formatPx(px float64) string {
	return strconv.FormatFloat(px, 'f', -1, 64) + "px"
}

// namedClassSelector matches an element by class name. It is used where a
// selector is a lone class name written with escapes that cascadia compiles but
// does not match.
func namedClassSelector(name string) cascadia.Selector {
	return func(n *html.Node) bool {
		return n != nil && n.Type == html.ElementNode && hasClass(n, name)
	}
}

// escapedClassSelector returns the class name of a selector that is exactly one
// class selector containing a CSS escape, which is the shape Tailwind's
// arbitrary-value utilities take.
func escapedClassSelector(sel string) (string, bool) {
	if len(sel) < 2 || sel[0] != '.' {
		return "", false
	}
	body := sel[1:]
	hasEscape := false
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c == '\\' {
			hasEscape = true
			break
		}
	}
	if !hasEscape {
		return "", false
	}
	// Anything else in the selector means it is not a lone class name: a
	// combinator, a comma, a pseudo-class or an attribute selector can all
	// contain characters that an escape would hide from this check.
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c == '\\' {
			consumed, ok := cssEscapeLen(body[i:])
			if !ok {
				return "", false
			}
			i += consumed - 1
			continue
		}
		if !isIdentChar(c) {
			return "", false
		}
	}
	name := unescapeCSSIdent(body)
	if name == "" {
		return "", false
	}
	return name, true
}

func isIdentChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '-' || c == '_' || c >= 0x80
}

// cssEscapeLen returns how many bytes a CSS escape starting at s[0] ('\\')
// occupies, or false when it is truncated.
func cssEscapeLen(s string) (int, bool) {
	if len(s) < 2 {
		return 0, false
	}
	if isHexDigit(s[1]) {
		n := 0
		for n < 6 && 1+n < len(s) && isHexDigit(s[1+n]) {
			n++
		}
		length := 1 + n
		// A hex escape may be terminated by one whitespace character, which
		// is part of it and not of the identifier.
		if 1+length < len(s) && (s[1+length] == ' ' || s[1+length] == '\t' || s[1+length] == '\n') {
			length++
		}
		return length, true
	}
	return 2, true
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// unescapeCSSIdent decodes CSS escapes: \X is X, and \HHHHH (one to six hex
// digits, optionally followed by one space) is the code point.
func unescapeCSSIdent(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			i++
			continue
		}
		n, ok := cssEscapeLen(s[i:])
		if !ok {
			break
		}
		body := s[i+1 : i+n]
		if isHexDigit(body[0]) {
			hex := strings.TrimRight(body, " \t\n")
			if v, err := strconv.ParseInt(hex, 16, 32); err == nil && v > 0 {
				b.WriteRune(rune(v))
			}
		} else {
			b.WriteByte(body[0])
		}
		i += n
	}
	return b.String()
}

// targetsPseudoElement reports whether a selector's subject is a pseudo-element
// such as "::before" or "::-webkit-inner-spin-button", which styles a generated
// box rather than an element in the DOM.
func targetsPseudoElement(sel string) bool {
	if strings.Contains(sel, "::") {
		return true
	}
	for _, prefix := range []string{":-webkit-", ":-moz-", ":-ms-", ":-o-"} {
		if strings.Contains(sel, prefix) {
			return true
		}
	}
	return false
}
