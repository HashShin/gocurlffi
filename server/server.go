// Package server implements a Chrome DevTools Protocol (CDP) server on top of
// the pure-Go browser, so Puppeteer, Playwright and chromedp can drive it over
// a WebSocket the way they drive Chrome. It serves the discovery endpoints
// (/json/version, /json/list) and a browser-level and per-page WebSocket.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/coder/websocket"

	"gocurlffi/browser"
)

// Config configures a CDP server.
type Config struct {
	Browser *browser.Browser
	Host    string
	Port    int
}

// Server is a CDP endpoint over a browser.
type Server struct {
	browser *browser.Browser

	mu      sync.Mutex
	targets map[string]*target
	order   []string
}

// target is one page exposed as a CDP target.
type target struct {
	id   string
	page *browser.Page

	mu       sync.Mutex // serializes commands against one page
	sessions map[string]*session
	refs     *refTable
	domNodes map[int]*nodeRef
	domSeq   int
	url      string
	title    string
	viewport int
	// loaderID identifies the current document. It changes on every
	// navigation, which is how a client detects a new document.
	loaderID string
	// contextSeq numbers the execution contexts announced to a client (the
	// main world and any isolated world it asks for).
	contextSeq int
	// initScripts are the Page.addScriptToEvaluateOnNewDocument sources bound
	// to this target, in installation order, so a client can revoke one by the
	// identifier it was handed back.
	initScripts []initScript
	// uaOverride is the Network.setUserAgentOverride value, applied to the page
	// and to the requests it issues.
	uaOverride string
}

// initScript is one script installed with Page.addScriptToEvaluateOnNewDocument.
type initScript struct {
	id     string
	source string
}

// sources returns the installed init scripts in installation order.
func (t *target) initSources() []string {
	out := make([]string, 0, len(t.initScripts))
	for _, s := range t.initScripts {
		out = append(out, s.source)
	}
	return out
}

type session struct {
	id     string
	target *target
}

// New creates a CDP server for a browser.
func New(cfg Config) *Server {
	return &Server{
		browser: cfg.Browser,
		targets: map[string]*target{},
	}
}

// NewTarget creates a page target, loading it if url is non-empty.
func (s *Server) NewTarget(url string) *target {
	t := &target{
		id:       randomID(),
		sessions: map[string]*session{},
		refs:     newRefTable(),
		domNodes: map[int]*nodeRef{},
	}
	if url == "" {
		url = "about:blank"
	}
	t.page = s.browser.NewPage(url)
	if url != "about:blank" {
		_ = t.page.Load(url)
	}
	t.url = t.page.URL
	s.mu.Lock()
	s.targets[t.id] = t
	s.order = append(s.order, t.id)
	s.mu.Unlock()
	return t
}

func (s *Server) getTarget(id string) *target {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.targets[id]
}

func (s *Server) targetList() []*target {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*target, 0, len(s.order))
	for _, id := range s.order {
		if t := s.targets[id]; t != nil {
			out = append(out, t)
		}
	}
	return out
}

// ListenAndServe starts the HTTP server and blocks.
func (s *Server) ListenAndServe(ctx context.Context, host string, port int) error {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, itoa(port)))
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: s.Handler()}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	return srv.Serve(ln)
}

// Handler returns the HTTP handler, exposing the discovery endpoints and the
// WebSocket upgrade paths.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"Browser":              "gobrowser/" + strings.TrimSpace(version),
			"Protocol-Version":     "1.3",
			"User-Agent":           browser.UserAgent(),
			"V8-Version":           "goja",
			"WebKit-Version":       "",
			"webSocketDebuggerUrl": "ws://" + r.Host + "/devtools/browser/" + s.browserID(),
		})
	})
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		list := []map[string]any{}
		for _, t := range s.targetList() {
			list = append(list, t.info(r.Host))
		}
		writeJSON(w, list)
	})
	mux.HandleFunc("/json/new", func(w http.ResponseWriter, r *http.Request) {
		u := r.URL.Query().Get("url")
		t := s.NewTarget(u)
		writeJSON(w, t.info(r.Host))
	})
	mux.HandleFunc("/devtools/browser/", func(w http.ResponseWriter, r *http.Request) {
		s.serveWS(w, r, nil)
	})
	mux.HandleFunc("/devtools/page/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/devtools/page/")
		t := s.getTarget(id)
		if t == nil {
			http.NotFound(w, r)
			return
		}
		s.serveWS(w, r, t)
	})
	// WebDriver BiDi shares the port, at /session and /session/{id}.
	mux.HandleFunc("/session", s.serveBiDi)
	mux.HandleFunc("/session/", s.serveBiDi)
	// The root path is the browser-level WebSocket, which is what a client
	// connects to when it is given "ws://host:port" with no path (Puppeteer's
	// browserWSEndpoint style).
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		s.serveWS(w, r, nil)
	})
	return mux
}

// version is overridden by the CLI with the build commit.
var version = "dev"

// SetVersion reports the build commit through Browser.getVersion.
func SetVersion(v string) { version = v }

// UserAgent is exposed so the server package can report the browser's UA.
func (s *Server) browserID() string { return "gobrowser" }

func (t *target) info(host string) map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	return map[string]any{
		"id":                   t.id,
		"type":                 "page",
		"title":                t.title,
		"url":                  t.url,
		"description":          "",
		"webSocketDebuggerUrl": "ws://" + host + "/devtools/page/" + t.id,
		"devtoolsFrontendUrl":  "",
	}
}

const wsBufSize = 1 << 20

func (s *Server) serveWS(w http.ResponseWriter, r *http.Request, fixed *target) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	c := &conn{server: s, ws: ws, sessions: map[string]*target{}}
	if fixed != nil {
		c.page = fixed
		c.sessions[""] = fixed
	}
	c.run(r.Context())
}

// conn is one WebSocket connection. A browser-level connection carries
// sessionId-tagged commands; a page connection has page set.
type conn struct {
	server   *Server
	ws       *websocket.Conn
	page     *target
	mu       sync.Mutex
	sessions map[string]*target
	mode     string // "browser" or "page"
	// autoAttach, when set by Target.setAutoAttach, makes a newly created
	// target attach to this connection automatically, which is how Puppeteer
	// discovers pages.
	autoAttach bool
	// pending holds event emitters to run after the reply is written.
	pending []func()
}

func (c *conn) run(ctx context.Context) {
	defer c.ws.Close(websocket.StatusNormalClosure, "")
	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			return
		}
		c.handle(data)
	}
}

func (c *conn) send(v any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = c.ws.Write(context.Background(), websocket.MessageText, b)
}

// sendEvent delivers a protocol event, tagged with a session id when present.
func (c *conn) sendEvent(sessionID, method string, params any) {
	if debugCDP {
		fmt.Fprintf(os.Stderr, "CDP -> %s sid=%q\n", method, sessionID)
	}
	msg := map[string]any{"method": method, "params": params}
	if sessionID != "" {
		msg["sessionId"] = sessionID
	}
	c.send(msg)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func randomID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
