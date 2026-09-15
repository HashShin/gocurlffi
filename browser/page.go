package browser

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"

	"gocurlffi/requests"
)

// defaultUserAgent is the fallback when no impersonation preset supplies one.
// It mirrors the Android Chrome profile of the "custom" target.
const defaultUserAgent = "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36"

// ConsoleEntry is a captured console.* message.
type ConsoleEntry struct {
	Level   string
	Message string
	Time    time.Time
}

// Page is a single loaded document plus its JavaScript environment.
type Page struct {
	browser *Browser

	URL      string
	Referrer string

	doc        *html.Node
	env        *jsEnv
	readyState string

	userAgent string
	platform  string

	resp    *requests.Response
	console []ConsoleEntry

	// currentScript is the <script> element being executed, exposed as
	// document.currentScript. Bundlers use it to resolve chunk paths.
	currentScript *html.Node

	// writePoint is the last node document.write inserted for the running
	// script, so a second write continues where the first stopped.
	writePoint *html.Node

	// layoutWidth is the viewport width getComputedStyle resolves media
	// queries against. Screenshot sets it to the width it renders at.
	layoutWidth float64

	// deadline bounds the script-loading phase (see Options.LoadTimeout).
	deadline time.Time

	// templateContent maps each <template> to the fragment holding its
	// content, which is kept out of the document tree.
	templateContent map[*html.Node]*html.Node

	// styleSources holds the page's CSS, fetched on first use, and
	// styleEngines caches the cascade per layout width. Both are dropped when
	// styleDirty is set, which the script-facing DOM helpers do: a script can
	// add a <style> element or a <link rel=stylesheet> at any time, and the
	// page's own CSS must then be picked up.
	styleSources []string
	styleEngines map[float64]*styleEngine
	sheets       []*pageStyleSheet
	styleDirty   bool
	// styleStamp is the DOM mutation count the caches above were built at.
	styleStamp uint64
	// sheetSources caches a fetched <link>'s text by element, so a script that
	// moves nodes around does not refetch.
	sheetSources map[*html.Node]string

	// images holds the page's fetched image bytes, guarded because a
	// screenshot prefetches them in parallel.
	imageMu sync.Mutex
	images  map[string]*pageImage
	// svgImages caches rasterized inline <svg> elements by node and size.
	svgImages map[string]*pageImage
	// widgetImages caches the drawn form-control graphics.
	widgetImages map[string]*pageImage

	// fontFaces are the @font-face rules the page declares, in document order.
	// fontLoaded and fontFailed cache fetching and decoding their files, which
	// only a screenshot does.
	fontFaces  []fontFace
	fontLoaded map[string]*webFont
	fontFailed map[string]bool

	loadedScripts map[string]bool

	// geomOn asks the next layout to record one rectangle per element. The
	// geometry accessors set it for the duration of a layout; screenshots and
	// text extraction leave it off, so they never pay for it.
	geomOn bool

	// geomCache is the element geometry of the last layout, reused until the
	// DOM or the viewport width changes.
	geomCache *layoutGeom
	// geomLayouts counts the layouts the geometry accessors performed, so a
	// test can prove that repeated measurements reuse one layout.
	geomLayouts int
}

// newPage creates an empty page bound to a browser.
func newPage(b *Browser, url string) *Page {
	p := &Page{
		browser:         b,
		URL:             url,
		readyState:      "loading",
		userAgent:       defaultUserAgent,
		platform:        "Linux armv8l",
		loadedScripts:   map[string]bool{},
		templateContent: map[*html.Node]*html.Node{},
	}
	if b.opts.UserAgent != "" {
		p.userAgent = b.opts.UserAgent
	} else if ua := b.sess.UserAgent(); ua != "" {
		// The page must report the browser the server was told about: with
		// the "chrome" preset that is a desktop Chrome, and a page that sees
		// a mobile UA while the server served it desktop markup renders the
		// wrong variant of itself.
		p.userAgent = ua
	}
	p.platform = platformForUserAgent(p.userAgent)
	return p
}

// platformForUserAgent maps a user agent to the navigator.platform it
// advertises, so the two do not contradict each other.
func platformForUserAgent(ua string) string {
	switch {
	case strings.Contains(ua, "Android"):
		return "Linux armv8l"
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"):
		return "iPhone"
	case strings.Contains(ua, "Macintosh"):
		return "MacIntel"
	case strings.Contains(ua, "Windows"):
		return "Win32"
	case strings.Contains(ua, "Linux"):
		return "Linux x86_64"
	}
	return "Linux armv8l"
}

// session returns the browser's HTTP session.
func (b *Browser) session() *requests.Session { return b.sess }

// Document returns the root DOM node.
func (p *Page) Document() *html.Node { return p.doc }

// DocumentNode satisfies internal callers.
func (p *Page) docNode() *html.Node { return p.doc }

// originString returns the page origin (scheme://host).
func (p *Page) originString() string {
	l := parseLocation(p.URL)
	return l.origin
}

// Response returns the document response, if loaded over the network.
func (p *Page) Response() *requests.Response { return p.resp }

// ReadyState returns the current document readyState.
func (p *Page) ReadyState() string { return p.readyState }

// Console returns captured console output.
func (p *Page) Console() []ConsoleEntry { return p.console }

// Log appends a message to the page console (used by scripts and the CLI).
func (p *Page) Log(level, message string) { p.log(level, message) }

func (p *Page) log(level, message string) {
	p.console = append(p.console, ConsoleEntry{Level: level, Message: message, Time: time.Now()})
	if p.browser != nil && p.browser.opts.Console != nil {
		p.browser.opts.Console(level, message)
	}
}

// debugf logs a page-load phase when Options.Debug is set.
func (p *Page) debugf(format string, args ...any) {
	if p.browser != nil && p.browser.opts.Debug {
		fmt.Fprintf(os.Stderr, "[browser] "+format+"\n", args...)
	}
}

// timerWait returns how long the loader may wait for pending timers.
func (p *Page) timerWait() time.Duration {
	if p.browser != nil && p.browser.opts.TimerBudget > 0 {
		return p.browser.opts.TimerBudget
	}
	return defaultTimerBudget
}

func (p *Page) maxScriptTime() time.Duration {
	if p.browser != nil && p.browser.opts.JavaScriptTimeout > 0 {
		return p.browser.opts.JavaScriptTimeout
	}
	return 10 * time.Second
}

// Open loads a URL, parses it, and runs its scripts. It is the main entry
// point for a one-shot page.
func (b *Browser) Open(rawURL string) (*Page, error) {
	p := newPage(b, rawURL)
	if err := p.load(rawURL, nil); err != nil {
		return p, err
	}
	return p, nil
}

// Load (re)loads a document into an existing page.
func (p *Page) load(rawURL string, headers map[string]string) error {
	resp, err := p.browser.get(rawURL, headers)
	if err != nil {
		return err
	}
	p.resp = resp
	p.URL = resp.URL
	contentType := ""
	if resp.Headers != nil {
		contentType = resp.Headers.Get("Content-Type")
	}
	// charset.NewReader honours the Content-Type charset, a BOM and <meta
	// charset>, transcoding to UTF-8 so non-UTF-8 pages are not mojibake.
	body, berr := charset.NewReader(bytes.NewReader(resp.Content), contentType)
	if berr != nil {
		body = bytes.NewReader(resp.Content)
	}
	doc, perr := html.Parse(body)
	if perr != nil {
		return perr
	}
	p.doc = doc
	p.templateContent = extractTemplateContents(doc)
	// A new document means the old stylesheets and rules are meaningless.
	p.markStyleDirty()
	return p.run()
}

// SetContent loads HTML from a string, running scripts unless disabled.
func (p *Page) SetContent(source, url string) error {
	p.URL = url
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return err
	}
	p.doc = doc
	p.templateContent = extractTemplateContents(doc)
	// A new document means the old stylesheets and rules are meaningless.
	p.markStyleDirty()
	return p.run()
}

// run sets up the JS environment and executes the document lifecycle.
func (p *Page) run() error {
	p.readyState = "loading"
	// A fresh document must reload its scripts; otherwise a second Load of
	// the same page would skip every external script.
	p.loadedScripts = map[string]bool{}
	if !p.browser.opts.scriptsEnabled() {
		p.readyState = "complete"
		return nil
	}
	p.env = newJSEnv(p)

	budget := p.browser.opts.LoadTimeout
	if budget <= 0 {
		budget = 30 * time.Second
	}
	p.deadline = time.Now().Add(budget)

	scripts := p.scripts()
	p.debugf("run: %d script(s), budget %s", len(scripts), budget)
	for _, s := range scripts {
		if p.outOfBudget() {
			p.log("warn", "page load budget exceeded; skipped remaining scripts")
			break
		}
		typ := strings.ToLower(strings.TrimSpace(strings.Split(attrOf(s, "type"), ";")[0]))
		if typ == "module" {
			p.log("warn", "skipping ES module script (not supported)")
			continue
		}
		if typ != "" && !strings.Contains(typ, "javascript") && !strings.Contains(typ, "ecmascript") {
			continue
		}
		if src := attrOf(s, "src"); src != "" {
			p.runExternalScript(s, src)
			continue
		}
		src := textContent(s)
		if strings.TrimSpace(src) == "" {
			continue
		}
		p.debugf("inline script: %d bytes", len(src))
		p.currentScript = s
		p.writePoint = nil
		err := p.env.runScript(src, p.URL)
		p.currentScript = nil
		p.writePoint = nil
		if err != nil {
			p.log("error", "script error: "+err.Error())
		}
	}

	p.readyState = "interactive"
	p.debugf("scripts done; firing DOMContentLoaded")
	p.env.fireDOMContentLoaded()
	p.debugf("running timers (interactive)")
	p.env.runTimers(1000)

	p.readyState = "complete"
	p.env.fireLoad()
	p.debugf("flushing intersection observers")
	p.env.flushIntersection()
	p.debugf("running timers (load)")
	p.env.runTimers(1000)
	p.debugf("page load complete")
	return nil
}

func (p *Page) outOfBudget() bool {
	return !p.deadline.IsZero() && time.Now().After(p.deadline)
}

func (p *Page) runExternalScript(el *html.Node, src string) {
	if p.outOfBudget() {
		p.log("warn", "page load budget exceeded; skipped "+src)
		return
	}
	abs := resolveURL(p.baseURL(), src)
	if p.loadedScripts[abs] {
		return
	}
	p.loadedScripts[abs] = true
	p.debugf("external script: %s", abs)
	resp, err := p.browser.get(abs, map[string]string{"Accept": "*/*"})
	if err != nil {
		p.log("error", "failed to load script "+abs+": "+err.Error())
		return
	}
	code := string(resp.Content)
	p.debugf("loaded %d bytes from %s", len(code), abs)
	p.currentScript = el
	p.writePoint = nil
	err = p.env.runScript(code, abs)
	p.currentScript = nil
	if err != nil {
		p.log("error", "script error in "+abs+": "+err.Error())
	}
}

// scripts returns all <script> elements in document order.
func (p *Page) scripts() []*html.Node {
	return getElementsByTagName(p.doc, "script")
}

func attrOf(n *html.Node, key string) string {
	v, _ := getAttr(n, key)
	return v
}

// baseURL returns the document base URL: the first <base href> resolved
// against the document URL, or the document URL itself.
func (p *Page) baseURL() string {
	href := ""
	for _, b := range getElementsByTagName(p.doc, "base") {
		if v, ok := getAttr(b, "href"); ok && v != "" {
			href = v
			break
		}
	}
	if href == "" {
		return p.URL
	}
	return resolveURL(p.URL, href)
}

// markStyleDirty records that the set of stylesheets may have changed, so the
// next style-dependent call re-reads the document. DOM writes do this on their
// own (see domMutations); this is for the loader, which replaces the document.
func (p *Page) markStyleDirty() {
	p.styleDirty = true
}

// styleCacheValid reports whether the cached stylesheets and rules still
// describe the document, dropping them if the DOM changed since they were read.
func (p *Page) styleCacheValid() bool {
	if p.styleStamp != domMutations.Load() {
		p.styleStamp = domMutations.Load()
		p.sheets = nil
		p.styleSources = nil
		p.styleEngines = nil
		p.fontFaces = nil
		return false
	}
	return p.sheets != nil
}

// quirksMode reports whether the document is parsed in quirks mode, which a
// missing doctype causes.
func (p *Page) quirksMode() bool {
	if p.doc == nil {
		return false
	}
	for c := p.doc.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.DoctypeNode {
			return false
		}
	}
	return true
}

// documentWrite implements document.write(ln). A script's output belongs at the
// script element's position in the document, not at the end of the body: pages
// that render a list with document.write must keep that list where the script
// sits, or the content lands after the footer. Successive writes from the same
// script continue after the previous insertion, as they stream into one parser.
func (p *Page) documentWrite(s string) {
	if p.doc == nil || s == "" {
		return
	}
	parent := (*html.Node)(nil)
	insertAfter := (*html.Node)(nil)
	if p.currentScript != nil && p.currentScript.Parent != nil {
		parent = p.currentScript.Parent
		insertAfter = p.currentScript
		// Continue after the last node this run already inserted, if it is
		// still in place (a write can wipe the parent's children).
		if p.writePoint != nil && p.writePoint.Parent == parent {
			insertAfter = p.writePoint
		}
	} else {
		parent = findElement(p.doc, "body")
		if parent == nil {
			parent = p.doc
		}
		insertAfter = parent.LastChild
	}
	nodes, err := html.ParseFragment(strings.NewReader(s), parent)
	if err != nil {
		return
	}
	at := insertAfter
	for _, c := range nodes {
		insertAfterNode(parent, at, c)
		at = c
	}
	if p.currentScript != nil && parent == p.currentScript.Parent {
		p.writePoint = at
	}
}

// insertAfterNode puts child directly after ref in parent, or appends when ref
// is nil.
func insertAfterNode(parent, ref, child *html.Node) {
	if parent == nil || child == nil || isAncestor(child, parent) {
		return
	}
	if ref == nil {
		appendChild(parent, child)
		return
	}
	if child.Parent != nil {
		removeChild(child)
	}
	child.Parent = parent
	child.PrevSibling = ref
	child.NextSibling = ref.NextSibling
	if ref.NextSibling != nil {
		ref.NextSibling.PrevSibling = child
	} else {
		parent.LastChild = child
	}
	ref.NextSibling = child
}

// --- cookies ---

func (p *Page) cookieString() string {
	if p.browser == nil || p.browser.sess == nil {
		return ""
	}
	m := p.browser.sess.Cookies().Map()
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "; ")
}

func (p *Page) setCookieString(s string) {
	if p.browser == nil || p.browser.sess == nil || s == "" {
		return
	}
	pair := strings.SplitN(s, ";", 2)[0]
	name, value, ok := strings.Cut(strings.TrimSpace(pair), "=")
	if !ok || name == "" {
		return
	}
	jar := p.browser.sess.Cookies()
	if strings.TrimSpace(value) == "" {
		jar.Del(strings.TrimSpace(name))
		return
	}
	jar.Set(strings.TrimSpace(name), strings.TrimSpace(value))
}

// templateContentNode returns the content fragment for a template, creating an
// empty one if the page did not record the template (for example a template
// built at runtime).
func (p *Page) templateContentNode(n *html.Node) *html.Node {
	if frag, ok := p.templateContent[n]; ok {
		return frag
	}
	frag := &html.Node{Type: html.ElementNode, Data: "#document-fragment"}
	p.templateContent[n] = frag
	return frag
}

// serialize renders a node with template contents re-attached, because the DOM
// stores them in a detached fragment while serialization must include them.
func (p *Page) serialize(n *html.Node) string {
	if n == nil {
		return ""
	}
	if len(p.templateContent) == 0 {
		return outerHTML(n)
	}
	grafted := p.graftTemplates(n)
	var b bytes.Buffer
	_ = html.Render(&b, n)
	p.ungraftTemplates(grafted)
	return b.String()
}

// serializeInner renders a node's children with template contents attached.
func (p *Page) serializeInner(n *html.Node) string {
	if n == nil {
		return ""
	}
	if len(p.templateContent) == 0 {
		return innerHTML(n)
	}
	grafted := p.graftTemplates(n)
	var b bytes.Buffer
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		_ = html.Render(&b, c)
	}
	p.ungraftTemplates(grafted)
	return b.String()
}

type graftedTemplate struct {
	tmpl *html.Node
	frag *html.Node
}

func (p *Page) graftTemplates(n *html.Node) []graftedTemplate {
	var out []graftedTemplate
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if isTemplate(x) {
			if frag, ok := p.templateContent[x]; ok {
				if frag.FirstChild != nil {
					out = append(out, graftedTemplate{tmpl: x, frag: frag})
					for ch := frag.FirstChild; ch != nil; {
						next := ch.NextSibling
						removeChild(ch)
						appendChild(x, ch)
						ch = next
					}
				}
			}
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}

func (p *Page) ungraftTemplates(grafted []graftedTemplate) {
	for _, g := range grafted {
		for ch := g.tmpl.FirstChild; ch != nil; {
			next := ch.NextSibling
			removeChild(ch)
			appendChild(g.frag, ch)
			ch = next
		}
	}
}

// --- public API ---

// HTML returns the serialized rendered DOM.
func (p *Page) HTML() string {
	if p.doc == nil {
		return ""
	}
	return p.serialize(p.doc)
}

// Text returns the visible text of the rendered DOM, skipping script and
// style content.
func (p *Page) Text() string { return visibleText(p.doc) }

// Query returns the first element matching a CSS selector.
func (p *Page) Query(sel string) *html.Node { return querySelector(p.doc, sel) }

// QueryAll returns all elements matching a CSS selector.
func (p *Page) QueryAll(sel string) []*html.Node { return querySelectorAll(p.doc, sel) }

// GetElementByID returns the element with the given id.
func (p *Page) GetElementByID(idv string) *html.Node { return getElementById(p.doc, idv) }

// Eval runs additional JavaScript in the page context.
func (p *Page) Eval(script string) (goja.Value, error) {
	if p.env == nil {
		return nil, nil
	}
	return p.env.vm.RunString(script)
}

// WaitForSelector polls until a selector matches or the timeout expires.
func (p *Page) WaitForSelector(sel string, timeout time.Duration) *html.Node {
	deadline := time.Now().Add(timeout)
	for {
		if n := querySelector(p.doc, sel); n != nil {
			return n
		}
		if time.Now().After(deadline) {
			return nil
		}
		if p.env != nil {
			p.env.runTimers(50)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// NewPage creates a page without loading anything. Use SetContent or Load.
func (b *Browser) NewPage(url string) *Page { return newPage(b, url) }

// Load fetches a URL into an existing page and runs its scripts.
func (p *Page) Load(rawURL string) error { return p.load(rawURL, nil) }
