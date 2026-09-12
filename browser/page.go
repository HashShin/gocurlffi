package browser

import (
	"bytes"
	"strings"
	"time"

	"github.com/dop251/goja"
	"gocurlffi/requests"
	"golang.org/x/net/html"
)

// defaultUserAgent mirrors the Android Chrome profile used by the "custom"
// impersonation target, so navigator.userAgent and the HTTP UA agree.
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

	loadedScripts map[string]bool
}

// newPage creates an empty page bound to a browser.
func newPage(b *Browser, url string) *Page {
	p := &Page{
		browser:       b,
		URL:           url,
		readyState:    "loading",
		userAgent:     defaultUserAgent,
		platform:      "Linux armv8l",
		loadedScripts: map[string]bool{},
	}
	if b.opts.UserAgent != "" {
		p.userAgent = b.opts.UserAgent
	}
	return p
}

// session returns the browser's HTTP session.
func (b *Browser) session() *requests.Session { return b.sess }

// Document returns the root DOM node.
func (p *Page) Document() *html.Node { return p.doc }

// DocumentNode satisfies internal callers.
func (p *Page) docNode() *html.Node { return p.doc }

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
	doc, perr := html.Parse(bytes.NewReader(resp.Content))
	if perr != nil {
		return perr
	}
	p.doc = doc
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
	return p.run()
}

// run sets up the JS environment and executes the document lifecycle.
func (p *Page) run() error {
	p.readyState = "loading"
	if !p.browser.opts.scriptsEnabled() {
		p.readyState = "complete"
		return nil
	}
	p.env = newJSEnv(p)

	scripts := p.scripts()
	for _, s := range scripts {
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
		if err := p.env.runScript(src, p.URL); err != nil {
			p.log("error", "script error: "+err.Error())
		}
	}

	p.readyState = "interactive"
	p.env.fireDOMContentLoaded()
	p.env.runTimers(1000)

	p.readyState = "complete"
	p.env.fireLoad()
	p.env.runTimers(1000)
	return nil
}

func (p *Page) runExternalScript(el *html.Node, src string) {
	abs := resolveURL(p.URL, src)
	if p.loadedScripts[abs] {
		return
	}
	p.loadedScripts[abs] = true
	resp, err := p.browser.get(abs, map[string]string{"Accept": "*/*"})
	if err != nil {
		p.log("error", "failed to load script "+abs+": "+err.Error())
		return
	}
	code := string(resp.Content)
	if err := p.env.runScript(code, abs); err != nil {
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

// documentWrite implements document.write(ln): parse and append to the body.
func (p *Page) documentWrite(s string) {
	if p.doc == nil || s == "" {
		return
	}
	body := findElement(p.doc, "body")
	target := body
	if target == nil {
		target = p.doc
	}
	nodes, err := html.ParseFragment(strings.NewReader(s), target)
	if err != nil {
		return
	}
	for _, c := range nodes {
		appendChild(target, c)
	}
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

// --- public API ---

// HTML returns the serialized rendered DOM.
func (p *Page) HTML() string {
	if p.doc == nil {
		return ""
	}
	return outerHTML(p.doc)
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
