package server

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"

	"gocurlffi/browser"
)

// An MCP (Model Context Protocol) server over JSON-RPC 2.0, so an AI agent can
// drive the browser through a small set of tools: navigate, read the page,
// evaluate JavaScript, screenshot, interact and read structured data. It runs
// over stdio by default, and over HTTP when given a port, matching the two
// transports MCP clients use.

// defaultSessionID is the session stdio uses: an MCP client that spawns the
// server owns the whole process, so it does not need to name a session.
const defaultSessionID = "default"

// MCP is a tool server. It holds one browsing session per client: each has its
// own Browser (cookies and memory) and its own current page, so two agents
// sharing one HTTP server no longer clobber each other's page.
type MCP struct {
	// browser is the template every session is forked from.
	browser *browser.Browser

	mu       sync.Mutex
	sessions map[string]*mcpSession
	order    []string
	seq      int
}

// mcpSession is one client's browsing session.
type mcpSession struct {
	id      string
	browser *browser.Browser

	mu   sync.Mutex
	page *browser.Page
}

// NewMCP creates an MCP server for a browser.
func NewMCP(b *browser.Browser) *MCP {
	return &MCP{browser: b, sessions: map[string]*mcpSession{}}
}

// Session returns the named session, creating it if it does not exist yet.
func (s *MCP) Session(id string) *mcpSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		id = defaultSessionID
	}
	if sess, ok := s.sessions[id]; ok {
		return sess
	}
	sess := &mcpSession{id: id, browser: s.browser.Fork()}
	s.sessions[id] = sess
	s.order = append(s.order, id)
	return sess
}

// CloseSession drops a session and closes its browser. It reports whether the
// session existed.
func (s *MCP) CloseSession(id string) bool {
	s.mu.Lock()
	sess, ok := s.sessions[id]
	if ok {
		delete(s.sessions, id)
		for i, o := range s.order {
			if o == id {
				s.order = append(s.order[:i], s.order[i+1:]...)
				break
			}
		}
	}
	s.mu.Unlock()
	if ok && sess.browser != nil {
		sess.browser.Close()
	}
	return ok
}

// SessionIDs lists the live sessions, oldest first.
func (s *MCP) SessionIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}

// nextID mints a session id that no live session uses.
func (s *MCP) nextID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		s.seq++
		id := "s" + strconv.Itoa(s.seq)
		if _, taken := s.sessions[id]; !taken {
			return id
		}
	}
}

// currentPage returns the session's current page, if it has one.
func (sess *mcpSession) currentPage() *browser.Page {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return sess.page
}

func (sess *mcpSession) setPage(p *browser.Page) {
	sess.mu.Lock()
	sess.page = p
	sess.mu.Unlock()
}

// page returns the session's page or reports that none is loaded, so every tool
// answers the same way.
func (sess *mcpSession) pageOrNil() (*browser.Page, bool) {
	if p := sess.currentPage(); p != nil {
		return p, true
	}
	return nil, false
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ServeStdio reads JSON-RPC messages from r (one per line) and writes replies to
// w, which is the stdio transport an MCP client spawns.
func (s *MCP) ServeStdio(r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	enc := json.NewEncoder(w)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytesTrimSpace(line)) == 0 {
			continue
		}
		if resp := s.HandleSession(s.Session(defaultSessionID), line); resp != nil {
			if err := enc.Encode(resp); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

// ServeHTTP serves the streamable-HTTP transport: a JSON-RPC request POSTed to
// /mcp and answered with a single JSON response.
//
// Mcp-Session-Id selects the browsing session. A request without one is given a
// fresh session, whose id comes back in the response header; sending that id on
// later requests stays on the same page, cookies and memory. Two clients that
// send the same id deliberately share one page. DELETE closes a session.
func (s *MCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("Mcp-Session-Id")

	if r.Method == http.MethodDelete {
		if id == "" {
			http.Error(w, "Mcp-Session-Id is required", http.StatusBadRequest)
			return
		}
		if !s.CloseSession(id) {
			http.Error(w, "unknown session", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	sess := s.Session(id)
	if id == "" {
		// Mint an id so the client can stay on this session.
		sess = s.Session(s.nextID())
	}
	resp := s.HandleSession(sess, body)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Mcp-Session-Id", sess.id)
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// Handle processes one message on the default session, which is what the stdio
// transport wants: the client owns the process, so it never names a session.
func (s *MCP) Handle(data []byte) *rpcResponse {
	return s.HandleSession(s.Session(defaultSessionID), data)
}

// HandleSession processes one message on the given session and returns the
// reply, or nil for a notification (which has no id).
func (s *MCP) HandleSession(sess *mcpSession, data []byte) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return &rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}}
	}
	if len(req.ID) == 0 {
		// A notification: nothing to answer.
		return nil
	}
	result, rerr := s.dispatch(sess, req.Method, req.Params)
	resp := &rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if rerr != nil {
		resp.Error = rerr
	} else {
		resp.Result = result
	}
	return resp
}

func (s *MCP) dispatch(sess *mcpSession, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "gobrowser", "version": version},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools()}, nil
	case "tools/call":
		return s.callTool(sess, params)
	}
	return nil, &rpcError{Code: -32601, Message: "method not found: " + method}
}

func (s *MCP) callTool(sess *mcpSession, params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "bad params"}
	}
	var args struct {
		URL        string `json:"url"`
		Expression string `json:"expression"`
		Selector   string `json:"selector"`
		Text       string `json:"text"`
		Format     string `json:"format"`
		Width      int    `json:"width"`
		Session    string `json:"session"`
	}
	_ = json.Unmarshal(p.Arguments, &args)

	text := func(s string) any {
		return map[string]any{"content": []map[string]any{{"type": "text", "text": s}}}
	}
	fail := func(err error) any {
		return map[string]any{
			"isError": true,
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
		}
	}

	switch p.Name {
	case "session_new":
		return text(s.Session(s.nextID()).id), nil

	case "session_list":
		b, _ := json.Marshal(s.SessionIDs())
		return text(string(b)), nil

	case "session_close":
		if args.Session == "" {
			return text("error: session is required"), nil
		}
		if !s.CloseSession(args.Session) {
			return text("error: unknown session " + args.Session), nil
		}
		return text("closed " + args.Session), nil

	case "navigate":
		if args.URL == "" {
			return text("error: url is required"), nil
		}
		p, err := sess.browser.Open(args.URL)
		if err != nil {
			return fail(err), nil
		}
		sess.setPage(p)
		return text(p.Text()), nil

	case "get_content":
		pg, ok := sess.pageOrNil()
		if !ok {
			return text("error: no page loaded; call navigate first"), nil
		}
		switch args.Format {
		case "markdown", "md":
			return text(pg.Markdown()), nil
		case "html":
			return text(pg.HTML()), nil
		case "links":
			var out []map[string]string
			for _, l := range pg.Links() {
				out = append(out, map[string]string{"href": l.Href, "text": l.Text})
			}
			b, _ := json.Marshal(out)
			return text(string(b)), nil
		default:
			return text(pg.Text()), nil
		}

	case "evaluate":
		pg, ok := sess.pageOrNil()
		if !ok {
			return text("error: no page loaded; call navigate first"), nil
		}
		v, err := pg.Eval(args.Expression)
		if err != nil {
			return fail(err), nil
		}
		if v == nil {
			return text("null"), nil
		}
		return text(fmt.Sprintf("%v", v.Export())), nil

	case "screenshot":
		pg, ok := sess.pageOrNil()
		if !ok {
			return text("error: no page loaded; call navigate first"), nil
		}
		width := args.Width
		if width <= 0 {
			width = 1280
		}
		png, err := pg.Screenshot(browser.ScreenshotOptions{Width: width})
		if err != nil {
			return fail(err), nil
		}
		return map[string]any{
			"content": []map[string]any{{
				"type": "image", "mimeType": "image/png",
				"data": base64.StdEncoding.EncodeToString(png),
			}},
		}, nil

	case "click":
		pg, ok := sess.pageOrNil()
		if !ok {
			return text("error: no page loaded; call navigate first"), nil
		}
		if err := pg.Click(args.Selector); err != nil {
			return fail(err), nil
		}
		return text("clicked " + args.Selector), nil

	case "type":
		pg, ok := sess.pageOrNil()
		if !ok {
			return text("error: no page loaded; call navigate first"), nil
		}
		if err := pg.Type(args.Selector, args.Text); err != nil {
			return fail(err), nil
		}
		return text("typed into " + args.Selector), nil

	case "structured_data":
		pg, ok := sess.pageOrNil()
		if !ok {
			return text("error: no page loaded; call navigate first"), nil
		}
		b, _ := json.Marshal(pg.StructuredData())
		return text(string(b)), nil
	}
	return nil, &rpcError{Code: -32602, Message: "unknown tool: " + p.Name}
}

// mcpTools describes the tools to an MCP client.
func mcpTools() []map[string]any {
	tool := func(name, desc string, props map[string]any, required ...string) map[string]any {
		schema := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			schema["required"] = required
		}
		return map[string]any{"name": name, "description": desc, "inputSchema": schema}
	}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	return []map[string]any{
		tool("navigate", "Load a URL and return its rendered text.", map[string]any{"url": str("absolute URL")}, "url"),
		tool("get_content", "Return the current page as text, markdown, html or links.", map[string]any{
			"format": map[string]any{"type": "string", "enum": []string{"text", "markdown", "html", "links"}},
		}),
		tool("evaluate", "Evaluate a JavaScript expression in the page and return its value.", map[string]any{"expression": str("JavaScript expression")}, "expression"),
		tool("screenshot", "Render the current page to a PNG image.", map[string]any{"width": map[string]any{"type": "integer"}}),
		tool("click", "Click the first element matching a CSS selector.", map[string]any{"selector": str("CSS selector")}, "selector"),
		tool("type", "Type text into the element matching a CSS selector.", map[string]any{
			"selector": str("CSS selector"), "text": str("text to type"),
		}, "selector", "text"),
		tool("structured_data", "Return the page's JSON-LD structured data.", map[string]any{}),
		tool("session_new", "Start a browsing session with its own page, cookies and memory, and return its id.", map[string]any{}),
		tool("session_list", "List the live browsing sessions, oldest first.", map[string]any{}),
		tool("session_close", "Close a browsing session by id.", map[string]any{
			"session": str("session id, as returned by session_new"),
		}, "session"),
	}
}

func bytesTrimSpace(b []byte) []byte {
	start := 0
	for start < len(b) && (b[start] == ' ' || b[start] == '\t' || b[start] == '\r' || b[start] == '\n') {
		start++
	}
	end := len(b)
	for end > start && (b[end-1] == ' ' || b[end-1] == '\t' || b[end-1] == '\r' || b[end-1] == '\n') {
		end--
	}
	return b[start:end]
}
