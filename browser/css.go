package browser

import (
	"strconv"
	"strings"

	"github.com/andybalholm/cascadia"
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
}

// parseCSSStylesheet parses CSS text into rules. mediaWidth is the layout width
// used to evaluate @media; if it is 0, width-dependent queries are treated as
// matching. Imports are returned for the caller to fetch first.
func parseCSSStylesheet(src string, mediaWidth float64, order *int) (rules []cssRule, imports []string) {
	p := &cssParser{src: src, order: order, mediaWidth: mediaWidth}
	p.parseRules(&rules, &imports, false)
	return rules, imports
}

type cssParser struct {
	src        string
	pos        int
	order      *int
	mediaWidth float64
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
		if quote != 0 {
			if c == '\\' {
				p.pos += 2
				continue
			}
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
		if quote != 0 {
			if c == '\\' {
				p.pos += 2
				continue
			}
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
		sel, err := cascadia.Compile(selText)
		if err != nil {
			continue
		}
		*p.order++
		*out = append(*out, cssRule{sel: sel, spec: cssSpecificity(selText), order: *p.order, decls: decls})
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

// matchMedia evaluates the media queries the renderer cares about. A zero
// mediaWidth means "unknown", in which case width features match.
func (p *cssParser) matchMedia(q string) bool {
	q = strings.TrimSpace(strings.ToLower(q))
	if q == "" {
		return true
	}
	// Comma is OR.
	for _, part := range strings.Split(q, ",") {
		if p.matchMediaQuery(strings.TrimSpace(part)) {
			return true
		}
	}
	return false
}

func (p *cssParser) matchMediaQuery(q string) bool {
	if q == "" {
		return true
	}
	for _, cond := range strings.Split(q, " and ") {
		cond = strings.TrimSpace(cond)
		switch {
		case cond == "" || cond == "all" || cond == "screen":
			continue
		case cond == "print" || cond == "speech":
			return false
		case strings.HasPrefix(cond, "(") && strings.HasSuffix(cond, ")"):
			feat := strings.TrimSuffix(strings.TrimPrefix(cond, "("), ")")
			name, val, ok := strings.Cut(feat, ":")
			name = strings.TrimSpace(name)
			val = strings.TrimSpace(val)
			if !ok {
				// e.g. (hover) — treat unknown features as not matching
				return false
			}
			px, ok := cssLengthToPx(val, 16)
			if !ok {
				return false
			}
			if p.mediaWidth <= 0 {
				continue
			}
			switch name {
			case "min-width":
				if p.mediaWidth < px {
					return false
				}
			case "max-width":
				if p.mediaWidth > px {
					return false
				}
			default:
				continue
			}
		case strings.HasPrefix(cond, "not "):
			return false
		default:
			continue
		}
	}
	return true
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

// --- shorthand expansion ---

func expandShorthands(in []cssDecl) []cssDecl {
	var out []cssDecl
	for _, d := range in {
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
		case "border", "border-top", "border-bottom", "border-left", "border-right":
			// Borders are not rendered; ignoring them avoids false spacing.
			continue
		case "flex", "grid", "flex-flow", "place-items", "gap", "columns":
			continue
		default:
			out = append(out, d)
		}
	}
	return out
}

// expandFontShorthand pulls the size, weight, style, line-height and family out
// of a `font` shorthand.
func expandFontShorthand(d cssDecl) []cssDecl {
	var out []cssDecl
	parts := strings.Fields(d.val)
	for _, p := range parts {
		low := strings.ToLower(p)
		switch {
		case low == "italic" || low == "oblique":
			out = append(out, cssDecl{prop: "font-style", val: "italic", important: d.important})
		case low == "bold" || low == "bolder":
			out = append(out, cssDecl{prop: "font-weight", val: "bold", important: d.important})
		case low == "small-caps" || low == "caption" || low == "menu" ||
			low == "message-box" || low == "status-bar" || low == "icon":
			// unsupported keywords
		default:
			size, lh, ok := strings.Cut(p, "/")
			if px, ok2 := cssFontSizeToPx(size, 16); ok2 {
				out = append(out, cssDecl{prop: "font-size", val: formatPx(px), important: d.important})
				_ = ok
				if lh != "" {
					out = append(out, cssDecl{prop: "line-height", val: lh, important: d.important})
				}
			}
		}
	}
	// Everything after the size is the family list; approximate by taking the
	// text after the first size token.
	if idx := cssFindSizeIndex(parts); idx >= 0 && idx+1 < len(parts) {
		fam := strings.Join(parts[idx+1:], " ")
		if i := strings.Index(fam, "/"); i >= 0 {
			fam = strings.TrimSpace(fam[i+1:])
		}
		if fam != "" {
			out = append(out, cssDecl{prop: "font-family", val: fam, important: d.important})
		}
	}
	return out
}

func cssFindSizeIndex(parts []string) int {
	for i, p := range parts {
		base := p
		if j := strings.IndexByte(p, '/'); j > 0 {
			base = p[:j]
		}
		if _, ok := cssFontSizeToPx(base, 16); ok {
			return i
		}
	}
	return -1
}

func formatPx(px float64) string {
	return strconv.FormatFloat(px, 'f', -1, 64) + "px"
}
