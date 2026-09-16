package browser

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/dop251/goja"
	"golang.org/x/net/html"
)

// A bounded ES module loader. goja has no module system, so a module is fetched,
// its import and export statements are rewritten into a wrapper function the
// runtime can call, and its dependencies are evaluated first. This covers the
// shapes real sites ship: default, named and namespace imports, re-exports,
// side-effect imports, an import map for bare specifiers, and dynamic import().
// Live bindings are approximated by copying values at module evaluation time,
// which is what almost every bundle relies on.

type module struct {
	url       string
	source    string
	imports   map[string]*module // specifier as written -> module
	specs     map[string]string  // specifier as written -> resolved url
	exports   *goja.Object
	evaluated bool
}

type moduleLoader struct {
	page    *Page
	modules map[string]*module
	imports map[string]string // import map: specifier prefix -> url
	loading map[string]bool
}

func newModuleLoader(p *Page) *moduleLoader {
	return &moduleLoader{
		page:    p,
		modules: map[string]*module{},
		imports: parseImportMap(p.doc),
		loading: map[string]bool{},
	}
}

// parseImportMap reads <script type="importmap"> JSON so a bare specifier can
// resolve. Only the "imports" map is used.
func parseImportMap(doc *html.Node) map[string]string {
	out := map[string]string{}
	for _, s := range getElementsByTagName(doc, "script") {
		typ := strings.ToLower(strings.TrimSpace(attrOf(s, "type")))
		if typ != "importmap" {
			continue
		}
		var parsed struct {
			Imports map[string]string `json:"imports"`
		}
		if err := json.Unmarshal([]byte(textContent(s)), &parsed); err != nil {
			continue
		}
		for k, v := range parsed.Imports {
			out[k] = v
		}
	}
	return out
}

// resolve turns a module specifier into an absolute URL: relative specifiers
// against the importer, bare ones through the import map.
func (l *moduleLoader) resolve(importer, spec string) (string, bool) {
	if spec == "" {
		return "", false
	}
	switch {
	case strings.HasPrefix(spec, "./"), strings.HasPrefix(spec, "../"), strings.HasPrefix(spec, "/"):
		return resolveURL(importer, spec), true
	case strings.Contains(spec, "://"):
		return spec, true
	}
	if u, ok := l.imports[spec]; ok {
		return resolveURL(importer, u), true
	}
	// Prefix match: an import map entry ending in "/" maps a whole package.
	for k, v := range l.imports {
		if strings.HasSuffix(k, "/") && strings.HasPrefix(spec, k) {
			return resolveURL(importer, v) + strings.TrimPrefix(spec, k), true
		}
	}
	return "", false
}

// runModuleScript loads and evaluates the module at url (an <script src>), or
// the inline source when src is empty.
func (p *Page) runModuleScript(src, inline string, el *html.Node) {
	loader := newModuleLoader(p)
	url := src
	if url == "" {
		url = p.URL
	}
	m, err := loader.load(url, inline)
	if err != nil {
		p.log("error", "module load: "+err.Error())
		return
	}
	if err := loader.evaluate(m); err != nil {
		p.log("error", "module evaluate: "+err.Error())
	}
}

// load fetches and parses a module, recursively loading its static imports.
func (l *moduleLoader) load(url, inline string) (*module, error) {
	if m, ok := l.modules[url]; ok {
		return m, nil
	}
	source := inline
	if source == "" {
		resp, err := l.page.browser.fetch(url, map[string]string{"Accept": "*/*"}, "script")
		if err != nil {
			return nil, err
		}
		source = string(resp.Content)
	}
	m := &module{url: url, source: source, imports: map[string]*module{}, specs: map[string]string{}}
	l.modules[url] = m

	body, staticImports, err := rewriteModule(source)
	if err != nil {
		return nil, err
	}
	m.source = body
	for spec := range staticImports {
		resolved, ok := l.resolve(url, spec)
		if !ok {
			l.page.debugf("module: unresolved specifier %q in %s", spec, url)
			continue
		}
		m.specs[spec] = resolved
		dep, err := l.load(resolved, "")
		if err != nil {
			l.page.debugf("module: failed to load %s: %v", resolved, err)
			continue
		}
		m.imports[spec] = dep
	}
	return m, nil
}

// evaluate runs a module after its dependencies, building the imports object
// from each dependency's exports.
func (l *moduleLoader) evaluate(m *module) error {
	if m.evaluated {
		return nil
	}
	m.evaluated = true
	m.exports = l.page.env.vm.NewObject()
	for _, dep := range m.imports {
		if err := l.evaluate(dep); err != nil {
			return err
		}
	}
	importsObj := l.page.env.vm.NewObject()
	for spec, dep := range m.imports {
		_ = importsObj.Set(spec, dep.exports)
	}
	vm := l.page.env.vm
	prog := "(function(__imports, __exports, __dynamicImport){\n" + m.source + "\n})"
	fnVal, err := vm.RunString(prog)
	if err != nil {
		return fmt.Errorf("%s: %w", m.url, err)
	}
	fn, ok := goja.AssertFunction(fnVal)
	if !ok {
		return fmt.Errorf("%s: module wrapper is not callable", m.url)
	}
	dyn := vm.ToValue(func(call goja.FunctionCall) goja.Value {
		spec := call.Argument(0).String()
		resolved, ok := l.resolve(m.url, spec)
		if !ok {
			return l.page.env.rejectedPromise(fmt.Errorf("cannot resolve module %q", spec))
		}
		dep, ok := l.modules[resolved]
		if !ok {
			d, err := l.load(resolved, "")
			if err != nil {
				return l.page.env.rejectedPromise(err)
			}
			dep = d
		}
		if err := l.evaluate(dep); err != nil {
			return l.page.env.rejectedPromise(err)
		}
		return l.page.env.resolvedPromise(dep.exports)
	})
	_, err = fn(goja.Undefined(), importsObj, m.exports, dyn)
	return err
}

var (
	reImportFrom    = regexp.MustCompile(`^import\s+([\s\S]*?)\s+from\s+["']([^"']*)["']\s*;?$`)
	reImportSide    = regexp.MustCompile(`^import\s+["']([^"']*)["']\s*;?$`)
	reExportDefault = regexp.MustCompile(`^export\s+default\s+([\s\S]*)$`)
	reExportNamed   = regexp.MustCompile(`^export\s*\{([\s\S]*?)\}\s*(?:from\s+["']([^"']*)["'])?\s*;?$`)
	reExportStar    = regexp.MustCompile(`^export\s*\*\s*from\s+["']([^"']*)["']\s*;?$`)
	reExportDecl    = regexp.MustCompile(`^export\s+(const|let|var|function|class|async)\b`)
)

// rewriteModule turns module source into a function body and reports the static
// import specifiers it needs. It works on top-level statements only, tracking
// string, template and comment state so `import` inside a string is ignored.
func rewriteModule(src string) (string, map[string]bool, error) {
	stmts := splitTopLevelStatements(src)
	var b strings.Builder
	specs := map[string]bool{}
	for _, st := range stmts {
		text := strings.TrimSpace(st)
		if text == "" {
			b.WriteString(st)
			continue
		}
		switch {
		case strings.HasPrefix(text, "import") && !strings.HasPrefix(text, "import("):
			if m := reImportSide.FindStringSubmatch(text); m != nil {
				specs[m[1]] = true
				fmt.Fprintf(&b, "__imports[%q];\n", m[1])
				continue
			}
			if m := reImportFrom.FindStringSubmatch(text); m != nil {
				spec := m[2]
				specs[spec] = true
				b.WriteString(rewriteImportClause(m[1], spec))
				b.WriteByte('\n')
				continue
			}
			b.WriteString(st)
		case strings.HasPrefix(text, "export"):
			if m := reExportStar.FindStringSubmatch(text); m != nil {
				specs[m[1]] = true
				fmt.Fprintf(&b, "Object.assign(__exports, __imports[%q]);\n", m[1])
				continue
			}
			if m := reExportNamed.FindStringSubmatch(text); m != nil {
				if m[2] != "" { // export {a as b} from 'spec'
					specs[m[2]] = true
					for _, name := range exportNames(m[1]) {
						src := name
						if i := strings.Index(name, " as "); i >= 0 {
							src = strings.TrimSpace(name[:i])
							name = strings.TrimSpace(name[i+4:])
						}
						fmt.Fprintf(&b, "__exports[%q] = __imports[%q][%q];\n", name, m[2], src)
					}
				} else {
					for _, name := range exportNames(m[1]) {
						src := name
						dst := name
						if i := strings.Index(name, " as "); i >= 0 {
							src = strings.TrimSpace(name[:i])
							dst = strings.TrimSpace(name[i+4:])
						}
						fmt.Fprintf(&b, "__exports[%q] = %s;\n", dst, src)
					}
				}
				continue
			}
			if m := reExportDefault.FindStringSubmatch(text); m != nil {
				// A function or class form is still a valid expression when
				// parenthesized, so one assignment covers every shape.
				val := strings.TrimSpace(m[1])
				fmt.Fprintf(&b, "__exports[\"default\"] = (%s);\n", val)
				continue
			}
			if m := reExportDecl.FindStringSubmatch(text); m != nil {
				rest := strings.TrimSpace(text[len("export"):])
				b.WriteString(rest)
				b.WriteByte('\n')
				for _, name := range declaredNames(rest) {
					fmt.Fprintf(&b, "__exports[%q] = %s;\n", name, name)
				}
				continue
			}
			b.WriteString(st)
		default:
			b.WriteString(st)
		}
	}
	body := b.String()
	body = reImportParenReplacer(body)
	return body, specs, nil
}

// rewriteImportClause turns the clause before "from" into destructuring and
// namespace bindings.
func rewriteImportClause(clause, spec string) string {
	clause = strings.TrimSpace(clause)
	var out strings.Builder
	if strings.HasPrefix(clause, "{") {
		names := strings.TrimSuffix(strings.TrimPrefix(clause, "{"), "}")
		out.WriteString("const {")
		out.WriteString(toDestructure(names))
		fmt.Fprintf(&out, "} = __imports[%q];", spec)
		return out.String()
	}
	if strings.HasPrefix(clause, "*") {
		ns := strings.TrimSpace(strings.TrimPrefix(clause, "*"))
		ns = strings.TrimSpace(strings.TrimPrefix(ns, "as"))
		fmt.Fprintf(&out, "const %s = __imports[%q];", ns, spec)
		return out.String()
	}
	// default, {named}
	if i := strings.Index(clause, ","); i >= 0 {
		def := strings.TrimSpace(clause[:i])
		rest := strings.TrimSpace(clause[i+1:])
		fmt.Fprintf(&out, "const %s = __imports[%q].default;", def, spec)
		if strings.HasPrefix(rest, "{") {
			names := strings.TrimSuffix(strings.TrimPrefix(rest, "{"), "}")
			out.WriteString(" const {")
			out.WriteString(toDestructure(names))
			fmt.Fprintf(&out, "} = __imports[%q];", spec)
		}
		return out.String()
	}
	fmt.Fprintf(&out, "const %s = __imports[%q].default;", clause, spec)
	return out.String()
}

// toDestructure turns "a, b as c" into "a, b: c".
func toDestructure(names string) string {
	parts := splitCommas(names)
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if j := strings.Index(p, " as "); j >= 0 {
			parts[i] = strings.TrimSpace(p[:j]) + ": " + strings.TrimSpace(p[j+4:])
		} else {
			parts[i] = p
		}
	}
	return strings.Join(parts, ", ")
}

// exportNames splits an export list, keeping "a as b" pairs.
func exportNames(names string) []string {
	var out []string
	for _, p := range splitCommas(names) {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitCommas splits on commas that are not inside braces or brackets.
func splitCommas(s string) []string {
	var out []string
	depth := 0
	last := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[last:i])
				last = i + 1
			}
		}
	}
	out = append(out, s[last:])
	return out
}

// declName returns the name of a function or class declaration.
func declName(s string) string {
	re := regexp.MustCompile(`(?:function|class)\s+(?:async\s+)?([A-Za-z_$][\w$]*)`)
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// declaredNames returns the bindings a const/let/var/function/class declaration
// introduces.
func declaredNames(s string) []string {
	s = strings.TrimSpace(s)
	kw := ""
	for _, k := range []string{"async function", "function", "class", "const", "let", "var"} {
		if strings.HasPrefix(s, k) {
			kw = k
			break
		}
	}
	switch kw {
	case "function", "async function", "class":
		if n := declName(s); n != "" {
			return []string{n}
		}
		return nil
	}
	rest := strings.TrimSpace(strings.TrimPrefix(s, kw))
	if i := strings.Index(rest, "="); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.Index(rest, ";"); i >= 0 {
		rest = rest[:i]
	}
	var names []string
	for _, p := range splitCommas(rest) {
		p = strings.TrimSpace(p)
		if i := strings.IndexAny(p, " \t"); i > 0 {
			p = p[:i]
		}
		p = strings.TrimRight(p, "=")
		p = strings.TrimSpace(p)
		if p != "" {
			names = append(names, p)
		}
	}
	return names
}

var reDynamicImport = regexp.MustCompile(`\bimport\s*\(`)

func reImportParenReplacer(s string) string {
	return reDynamicImport.ReplaceAllString(s, "__dynamicImport(")
}

// splitTopLevelStatements splits source into statements at top-level semicolons
// and newlines, skipping strings, template literals and comments. It is not a
// full parser: it is enough to find the import and export statements at the top
// level, which is where the language allows them.
func splitTopLevelStatements(src string) []string {
	var out []string
	depth := 0
	start := 0
	i := 0
	for i < len(src) {
		c := src[i]
		switch c {
		case '/':
			if i+1 < len(src) && src[i+1] == '/' {
				j := strings.IndexByte(src[i:], '\n')
				if j < 0 {
					i = len(src)
				} else {
					i += j + 1
				}
				continue
			}
			if i+1 < len(src) && src[i+1] == '*' {
				j := strings.Index(src[i+2:], "*/")
				if j < 0 {
					i = len(src)
				} else {
					i += j + 4
				}
				continue
			}
		case '\'', '"', '`':
			i = skipString(src, i)
			continue
		case '{', '(', '[':
			depth++
		case '}', ')', ']':
			depth--
		case ';':
			if depth == 0 {
				out = append(out, src[start:i+1])
				start = i + 1
			}
		case '\n':
			if depth == 0 {
				out = append(out, src[start:i+1])
				start = i + 1
			}
		}
		i++
	}
	if start < len(src) {
		out = append(out, src[start:])
	}
	return out
}

// skipString returns the index just past the string or template literal that
// starts at i.
func skipString(src string, i int) int {
	q := src[i]
	i++
	for i < len(src) {
		if src[i] == '\\' {
			i += 2
			continue
		}
		if src[i] == q {
			return i + 1
		}
		i++
	}
	return i
}
