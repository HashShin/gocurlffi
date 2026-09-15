// Package gsearch is a raw-CDP Google search scraper. It drives Chromium over
// the Chrome DevTools Protocol (no chromedp library), uses a virtual display
// so Google doesn't serve anti-bot interstitials, and walks start=N
// pagination to collect as many results as a search allows.
package gsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

// browserCandidates are the Chromium builds this tool drives, in order of
// preference. The first that exists is used unless Config.Browser names one.
// Termux is first so behaviour there is unchanged.
var browserCandidates = []string{
	"/data/data/com.termux/files/usr/bin/chromium-browser",
	"/usr/bin/google-chrome-stable",
	"/usr/bin/google-chrome",
	"/usr/bin/chromium",
	"/usr/bin/chromium-browser",
	"/snap/bin/chromium",
	"/opt/google/chrome/chrome",
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
}

// browserNames are looked up on PATH when no candidate path exists.
var browserNames = []string{"google-chrome-stable", "google-chrome", "chromium", "chromium-browser"}

// findBrowser resolves the browser binary. An explicit path wins and must exist;
// otherwise the first known location, then PATH. The hardcoded Termux path this
// used to carry made the tool unusable anywhere else.
func findBrowser(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("browser %q: %w", explicit, err)
		}
		return explicit, nil
	}
	for _, c := range browserCandidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	for _, name := range browserNames {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no chromium found; pass -browser /path/to/chrome")
}

// browserFlags keep the browser footprint small while still being
// anti-detection friendly.
var browserFlags = []string{
	"--no-sandbox",
	"--disable-dev-shm-usage",
	"--disable-gpu",
	"--ignore-certificate-errors",
	"--remote-allow-origins=*",
	"--no-first-run",
	"--no-default-browser-check",
	"--disable-infobars",
	"--disable-blink-features=AutomationControlled", // anti-detection
	"--password-store=basic",
	"--homepage=about:blank",
	// Lower memory/CPU footprint:
	"--disable-extensions",
	"--disable-background-networking",
	"--disable-background-timer-throttling",
	"--disable-renderer-backgrounding",
	"--disable-backgrounding-occluded-windows",
	"--disable-component-update",
	"--disable-default-apps",
	"--mute-audio",
	"--metrics-recording-only",
	"--window-size=1280,720",
}

// ---- CDP primitives ----

type cdpMsg struct {
	ID     int            `json:"id"`
	Method string         `json:"method,omitempty"`
	Params map[string]any `json:"params,omitempty"`
	Result map[string]any `json:"result,omitempty"`
	Error  *cdpError      `json:"error,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ---- Browser ----

type Browser struct {
	cmd     *exec.Cmd
	port    int
	conn    net.Conn
	msgID   int
	respCh  map[int]chan cdpMsg
	writeMu sync.Mutex
	respMu  sync.Mutex
}

func freePort() int {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// displayInUse reports whether an X server (e.g. Xvfb) is already listening on
// the given display like ":99", so we don't try to start a second one.
func displayInUse(display string) bool {
	num := strings.TrimPrefix(display, ":")
	// Classic X lock file / socket.
	for _, p := range []string{"/tmp/.X" + num + "-lock", "/tmp/.X11-unix/X" + num} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	// Fall back to scanning for a running Xvfb with that display.
	out, _ := exec.Command("pgrep", "-f", "Xvfb "+display).Output()
	return len(strings.TrimSpace(string(out))) > 0
}

// startBrowserWithRetry tries startBrowser a few times, killing any stray
// Chromium between attempts, to ride out flaky browser startup.
func startBrowserWithRetry(browserPath, display string) (*Browser, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		b, err := startBrowser(browserPath, display)
		if err == nil {
			return b, nil
		}
		lastErr = err
		exec.Command("pkill", "-9", "-f", filepath.Base(browserPath)).Run()
		time.Sleep(2 * time.Second)
	}
	return nil, lastErr
}

// profileDir is the browser profile gsearch keeps between runs. Chrome 136 and
// later refuse to open the DevTools remote debugging port against the default
// profile ("DevTools remote debugging requires a non-default data directory"),
// so this is required, not an optimisation. Keeping it across runs also lets
// consent and session cookies accumulate, which is what keeps a warmed profile
// out of the interstitial.
func profileDir() string {
	if d := os.Getenv("GSEARCH_PROFILE"); d != "" {
		return d
	}
	return filepath.Join(os.TempDir(), "gsearch-chrome-profile")
}

func startBrowser(browserPath, display string) (*Browser, error) {
	port := freePort()
	dir := profileDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create browser profile %s: %w", dir, err)
	}
	args := append(browserFlags,
		fmt.Sprintf("--user-data-dir=%s", dir),
		fmt.Sprintf("--remote-debugging-port=%d", port))

	cmd := exec.Command(browserPath, args...)
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start browser: %w", err)
	}

	var wsURL string
	for i := 0; i < 80; i++ { // up to ~20s; Chromium can be slow to start
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", port))
		if err == nil {
			var v map[string]string
			json.NewDecoder(resp.Body).Decode(&v)
			resp.Body.Close()
			wsURL = v["webSocketDebuggerUrl"]
			if wsURL != "" {
				break
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	if wsURL == "" {
		cmd.Process.Kill()
		return nil, fmt.Errorf("browser CDP not ready on port %d", port)
	}

	b := &Browser{cmd: cmd, port: port, respCh: make(map[int]chan cdpMsg)}
	if err := b.connect(wsURL); err != nil {
		cmd.Process.Kill()
		return nil, err
	}
	return b, nil
}

func (b *Browser) connect(wsURL string) error {
	conn, _, _, err := ws.Dial(context.Background(), wsURL)
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}
	b.conn = conn
	go b.readLoop()
	return nil
}

func (b *Browser) readLoop() {
	for {
		data, err := wsutil.ReadServerText(b.conn)
		if err != nil {
			return
		}
		var msg cdpMsg
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		if msg.ID > 0 {
			b.respMu.Lock()
			ch, ok := b.respCh[msg.ID]
			b.respMu.Unlock()
			if ok {
				ch <- msg
			}
		}
	}
}

func (b *Browser) send(method string, params map[string]any) (map[string]any, error) {
	b.respMu.Lock()
	b.msgID++
	id := b.msgID
	ch := make(chan cdpMsg, 1)
	b.respCh[id] = ch
	b.respMu.Unlock()
	defer func() {
		b.respMu.Lock()
		delete(b.respCh, id)
		b.respMu.Unlock()
	}()

	data, _ := json.Marshal(cdpMsg{ID: id, Method: method, Params: params})
	b.writeMu.Lock()
	err := wsutil.WriteClientText(b.conn, data)
	b.writeMu.Unlock()
	if err != nil {
		return nil, err
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("CDP error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("timeout waiting for %s", method)
	}
}

// newTab opens a fresh tab target and returns a ready-to-use *Tab.
func (b *Browser) newTab() (*Tab, error) {
	req, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("http://127.0.0.1:%d/json/new", b.port), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	var target map[string]string
	json.NewDecoder(resp.Body).Decode(&target)
	resp.Body.Close()

	wsURL := target["webSocketDebuggerUrl"]
	if wsURL == "" {
		return nil, fmt.Errorf("no wsURL for new tab")
	}
	conn, _, _, err := ws.Dial(context.Background(), wsURL)
	if err != nil {
		return nil, fmt.Errorf("tab ws dial: %w", err)
	}
	t := &Tab{id: target["id"], conn: conn, respCh: make(map[int]chan cdpMsg), events: make(chan cdpMsg, 50)}
	go t.readLoop()
	return t, nil
}

func (b *Browser) stop() {
	if b.conn != nil {
		b.conn.Close()
	}
	if b.cmd != nil && b.cmd.Process != nil {
		b.cmd.Process.Kill()
	}
}

// ---- Tab ----

type Tab struct {
	id      string
	conn    net.Conn
	msgID   int
	respCh  map[int]chan cdpMsg
	events  chan cdpMsg
	writeMu sync.Mutex
	respMu  sync.Mutex
	dead    atomic.Bool // set when the CDP connection drops
}

func (t *Tab) isDead() bool { return t.dead.Load() }

func (t *Tab) readLoop() {
	defer t.dead.Store(true)
	for {
		data, err := wsutil.ReadServerText(t.conn)
		if err != nil {
			return
		}
		var msg cdpMsg
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		if msg.ID > 0 {
			t.respMu.Lock()
			ch, ok := t.respCh[msg.ID]
			t.respMu.Unlock()
			if ok {
				ch <- msg
			}
		} else if msg.Method != "" {
			select {
			case t.events <- msg:
			default:
			}
		}
	}
}

func (t *Tab) send(method string, params map[string]any) (map[string]any, error) {
	t.respMu.Lock()
	t.msgID++
	id := t.msgID
	ch := make(chan cdpMsg, 1)
	t.respCh[id] = ch
	t.respMu.Unlock()
	defer func() {
		t.respMu.Lock()
		delete(t.respCh, id)
		t.respMu.Unlock()
	}()

	data, _ := json.Marshal(cdpMsg{ID: id, Method: method, Params: params})
	t.writeMu.Lock()
	err := wsutil.WriteClientText(t.conn, data)
	t.writeMu.Unlock()
	if err != nil {
		return nil, err
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("CDP %s error: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("timeout: %s", method)
	}
}

func (t *Tab) eval(expr string) (any, error) {
	result, err := t.send("Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
	})
	if err != nil {
		return nil, err
	}
	res, ok := result["result"].(map[string]any)
	if !ok {
		return nil, nil
	}
	return res["value"], nil
}

func (t *Tab) close() {
	if t.conn != nil {
		t.conn.Close()
	}
}

// init enables CDP domains, injects stealth JS, and sets a random UA once.
func (t *Tab) init() error {
	for _, m := range [][]any{{"Page.enable", nil}, {"Runtime.enable", nil}, {"Network.enable", nil}} {
		if _, err := t.send(m[0].(string), nil); err != nil {
			return err
		}
	}
	if _, err := t.send("Page.addScriptToEvaluateOnNewDocument", map[string]any{
		"source": stealthScript,
	}); err != nil {
		return err
	}
	ua := userAgents[rand.Intn(len(userAgents))]
	_, err := t.send("Network.setUserAgentOverride", map[string]any{"userAgent": ua})
	return err
}

// ---- Anti-detection ----

var stealthScript = `(function() {
	Object.defineProperty(navigator, 'webdriver', { get: () => undefined });
	Object.defineProperty(navigator, 'plugins', { get: () => [1, 2, 3, 4, 5] });
	Object.defineProperty(navigator, 'languages', { get: () => ['en-US', 'en'] });
	window.chrome = { runtime: {} };
	const q = window.navigator.permissions.query;
	window.navigator.permissions.query = (p) => (
		p.name === 'notifications' ? Promise.resolve({ state: Notification.permission }) : q(p)
	);
})();`

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
}

// ---- Result / extraction ----

// Result is one search result. JSON fields are stable for the web UI / API.
type Result struct {
	Query     string `json:"query"`
	Position  int    `json:"position"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Domain    string `json:"domain"`
	Snippet   string `json:"snippet,omitempty"`
	ImageURL  string `json:"image_url,omitempty"`
	Thumbnail string `json:"thumbnail,omitempty"`
}

// Progress describes one step of an in-flight search, streamed to a UI.
type Progress struct {
	Kind    string `json:"kind"` // "info" | "page" | "done" | "error"
	Message string `json:"message"`
	Count   int    `json:"count,omitempty"`
}

type pageState struct {
	HasResults bool   `json:"has"`
	URL        string `json:"url"`
	Captcha    bool   `json:"captcha"`
}

var stateExpr = `(function(){
	var bi = document.body ? document.body.innerHTML : '';
	return JSON.stringify({
		has: document.querySelectorAll('.tF2Cxc').length > 0,
		url: location.href,
		captcha: /sorry|recaptcha|unusual traffic/.test(bi)
	});
})()`

// navigateAndWait polls the DOM for results (or a CAPTCHA/sorry state) instead
// of sleeping a fixed amount.
func (t *Tab) navigateAndWait(searchURL string, timeout time.Duration) error {
	if _, err := t.send("Page.navigate", map[string]any{"url": searchURL}); err != nil {
		return fmt.Errorf("navigate: %w", err)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		v, err := t.eval(stateExpr)
		if err == nil {
			if s, ok := v.(string); ok {
				var st pageState
				if json.Unmarshal([]byte(s), &st) == nil {
					if st.HasResults {
						return nil
					}
					if st.Captcha || strings.Contains(st.URL, "/sorry") {
						return fmt.Errorf("CAPTCHA detected")
					}
				}
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for results")
}

// webEval extracts rich organic (text) results.
const webEval = `
	Array.from(document.querySelectorAll('.tF2Cxc')).map(el => {
		const h3 = el.querySelector('h3');
		const a = el.querySelector('a');
		const s = el.querySelector('.VwiC3b');
		const dom = el.querySelector('.VuuXrf');
		const img = el.querySelector('img');
		return {
			title: h3 ? h3.innerText : '',
			url: a ? a.href : '',
			snippet: s ? s.innerText : '',
			domain: dom ? dom.innerText : '',
			thumbnail: img ? img.src : ''
		};
	})`

// scrapeGoogle collects every result for a query by walking Google's
// start=N pagination until results run out or maxResults is reached.
// Google caps each page (num is often ignored / capped at ~10), so we keep
// paging via start until a page yields no new results.
func (t *Tab) scrapeGoogle(query string, perPage, maxResults int, timeout time.Duration, on func(Progress)) ([]Result, error) {
	if perPage <= 0 {
		perPage = 20
	}
	str := func(m map[string]any, key string) string {
		if s, ok := m[key].(string); ok {
			return s
		}
		return ""
	}

	var results []Result
	seen := map[string]bool{}
	start := 0

	for {
		searchURL := fmt.Sprintf("https://www.google.com/search?q=%s&num=%d&hl=en&start=%d",
			url.QueryEscape(query), perPage, start)
		if on != nil {
			on(Progress{Kind: "page", Message: fmt.Sprintf("Fetching results page (offset %d)…", start), Count: len(results)})
		}
		if err := t.navigateAndWait(searchURL, timeout); err != nil {
			if len(results) > 0 {
				return results, nil // partial results are better than a hard fail
			}
			return nil, err
		}

		rowsRaw, err := t.eval(webEval)
		if err != nil {
			return results, err
		}
		rows, _ := rowsRaw.([]any)

		added := 0
		for _, row := range rows {
			m, ok := row.(map[string]any)
			if !ok {
				continue
			}
			title := str(m, "title")
			link := str(m, "url")
			if title == "" && link == "" {
				continue
			}
			if seen[link] {
				continue
			}
			seen[link] = true
			added++
			results = append(results, Result{
				Query:     query,
				Position:  len(results) + 1,
				Title:     title,
				URL:       link,
				Domain:    domainOf(link, str(m, "domain")),
				Snippet:   str(m, "snippet"),
				ImageURL:  str(m, "image"),
				Thumbnail: str(m, "thumbnail"),
			})
			if maxResults > 0 && len(results) >= maxResults {
				if on != nil {
					on(Progress{Kind: "page", Message: "Reached result cap", Count: len(results)})
				}
				return results, nil
			}
		}

		// Nothing new on this page means we've hit the end of results.
		if added == 0 {
			if on != nil {
				on(Progress{Kind: "page", Message: "No more results", Count: len(results)})
			}
			return results, nil
		}
		start += len(rows) // advance by however many results this page held
	}
}

// domainOf returns a clean domain from a URL, falling back to the inline text.
func domainOf(raw, inline string) string {
	if inline != "" {
		return strings.TrimPrefix(inline, "www.")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}

// ---- Rate limiter (global, so parallel workers stay under the radar) ----

type rateLimiter struct {
	mu       sync.Mutex
	next     time.Time
	interval time.Duration
}

func (r *rateLimiter) wait() {
	if r == nil || r.interval <= 0 {
		return
	}
	for {
		r.mu.Lock()
		now := time.Now()
		if now.After(r.next) {
			r.next = now.Add(r.interval)
			r.mu.Unlock()
			return
		}
		d := r.next.Sub(now)
		r.mu.Unlock()
		time.Sleep(d)
	}
}

// ---- Engine ----
//
// Engine owns one browser with a pool of tabs and runs searches one at a time
// (serialized) so we stay under Google's radar. It's safe for concurrent HTTP
// callers: they queue up.

// Config controls browser setup and per-search behaviour.
type Config struct {
	Browser     string        // browser binary; empty auto-detects
	Display     string        // Xvfb display, e.g. ":99"
	Concurrency int           // number of tabs in the pool
	Interval    time.Duration // min delay between requests
	Timeout     time.Duration // per-page load timeout
	Retries     int           // retries per search on CAPTCHA/timeout
}

// Engine is a long-lived scraper with a shared browser + tab pool.
type Engine struct {
	browser *Browser
	tabs    []*Tab
	rl      *rateLimiter
	cfg     Config
	nextTab int
	mu      sync.Mutex
}

// New starts Xvfb + Chromium, opens the tab pool, and warms each tab.
func New(cfg Config) (*Engine, error) {
	if cfg.Display == "" {
		cfg.Display = ":99"
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 20 * time.Second
	}
	if cfg.Retries < 1 {
		cfg.Retries = 1
	}

	browserPath, err := findBrowser(cfg.Browser)
	if err != nil {
		return nil, err
	}
	cfg.Browser = browserPath

	// Virtual display so Chromium runs non-headless (headless gets CAPTCHA'd).
	// Reuse an already-running Xvfb on this display instead of starting a
	// second one (which would fail because the socket is taken).
	var xvfb *exec.Cmd
	if !displayInUse(cfg.Display) {
		xvfb = exec.Command("Xvfb", cfg.Display, "-screen", "0", "1280x720x24")
		xvfb.Stdout = io.Discard
		xvfb.Stderr = io.Discard
		if err := xvfb.Start(); err != nil {
			// Couldn't start one; maybe the display is already live. Continue
			// anyway and let startBrowser try to use whatever is there.
			xvfb = nil
		} else {
			time.Sleep(time.Second)
		}
	}
	if xvfb != nil {
		defer xvfb.Process.Kill()
	}

	// startBrowser can be flaky on slow/loaded devices; retry a couple of times.
	browser, err := startBrowserWithRetry(cfg.Browser, cfg.Display)
	if err != nil {
		if xvfb != nil {
			xvfb.Process.Kill()
		}
		return nil, err
	}

	e := &Engine{browser: browser, cfg: cfg, rl: &rateLimiter{interval: cfg.Interval}}

	// Warm up each tab to google.com so cookies/SG_SS are established.
	for i := 0; i < cfg.Concurrency; i++ {
		tab, err := warmTab(browser, e.rl)
		if err != nil {
			continue
		}
		e.tabs = append(e.tabs, tab)
	}
	if len(e.tabs) == 0 {
		browser.stop()
		xvfb.Process.Kill()
		return nil, fmt.Errorf("no usable tabs")
	}
	return e, nil
}

// warmTab opens a new tab, applies stealth + UA, and loads google.com once.
func warmTab(b *Browser, rl *rateLimiter) (*Tab, error) {
	tab, err := b.newTab()
	if err != nil {
		return nil, err
	}
	if err := tab.init(); err != nil {
		tab.close()
		return nil, err
	}
	rl.wait()
	tab.navigateAndWait("https://www.google.com", 10*time.Second)
	return tab, nil
}

// recreateTab replaces one broken tab with a freshly warmed one.
func (e *Engine) recreateTab(i int) error {
	if e.tabs[i] != nil {
		e.tabs[i].close()
	}
	t, err := warmTab(e.browser, e.rl)
	if err != nil {
		return err
	}
	e.tabs[i] = t
	return nil
}

// restartBrowser tears down Chromium and rebuilds the whole tab pool. Used
// when the browser process itself died (common under memory pressure).
func (e *Engine) restartBrowser() error {
	e.browser.stop()
	time.Sleep(2 * time.Second)
	b, err := startBrowserWithRetry(e.cfg.Browser, e.cfg.Display)
	if err != nil {
		return err
	}
	e.browser = b
	var tabs []*Tab
	for i := 0; i < e.cfg.Concurrency; i++ {
		t, err := warmTab(b, e.rl)
		if err != nil {
			continue
		}
		tabs = append(tabs, t)
	}
	if len(tabs) == 0 {
		return fmt.Errorf("no usable tabs after restart")
	}
	e.tabs = tabs
	e.nextTab = 0
	return nil
}

// isConnErr reports whether an error indicates a broken tab/browser link, in
// which case a fresh tab (or browser) is likely to fix it.
func isConnErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "use of closed network connection") ||
		strings.Contains(s, "EOF")
}

// Close shuts down all tabs and the browser.
func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range e.tabs {
		t.close()
	}
	if e.browser != nil {
		e.browser.stop()
	}
}

// Search runs a single query (serialized across callers) and returns all
// collected results. perPage is the num=N per page; maxResults caps the total
// (0 = collect until results run out).
func (e *Engine) Search(query string, perPage, maxResults int) ([]Result, error) {
	return e.SearchWithProgress(query, perPage, maxResults, nil)
}

// SearchWithProgress is Search but also reports each step via on (if non-nil)
// so a UI can stream live progress. It reports "info" and "page" steps and an
// "error" per failed attempt; the caller decides the final "done" event.
func (e *Engine) SearchWithProgress(query string, perPage, maxResults int, on func(Progress)) ([]Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if perPage <= 0 {
		perPage = 20
	}
	ti := e.nextTab % len(e.tabs)
	e.nextTab++

	if on != nil {
		on(Progress{Kind: "info", Message: "Starting search for “" + query + "”"})
	}
	var lastErr error
	for attempt := 1; attempt <= e.cfg.Retries; attempt++ {
		if attempt > 1 && on != nil {
			on(Progress{Kind: "info", Message: fmt.Sprintf("Retry %d/%d…", attempt, e.cfg.Retries)})
		}
		e.rl.wait()
		r, err := e.tabs[ti].scrapeGoogle(query, perPage, maxResults, e.cfg.Timeout, on)
		if err == nil {
			return r, nil
		}
		lastErr = err
		if on != nil {
			on(Progress{Kind: "error", Message: err.Error()})
		}
		// Self-heal: a broken tab or a dead browser shouldn't keep failing.
		if isConnErr(err) || e.tabs[ti].isDead() {
			if err := e.recreateTab(ti); err != nil {
				if rerr := e.restartBrowser(); rerr != nil {
					if on != nil {
						on(Progress{Kind: "error", Message: "browser recovery failed: " + rerr.Error()})
					}
					return nil, lastErr
				}
			}
		}
		time.Sleep(time.Duration(attempt*5) * time.Second)
	}
	return nil, lastErr
}

// Status reports engine health for a web UI / health check.
func (e *Engine) Status() (tabs, idle int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.tabs), len(e.tabs)
}
