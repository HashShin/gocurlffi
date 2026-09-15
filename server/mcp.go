package server

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"gocurlffi/browser"
)

// An MCP (Model Context Protocol) server over JSON-RPC 2.0, so an AI agent can
// drive the browser through a small set of tools: navigate, read the page,
// evaluate JavaScript, screenshot, interact and read structured data. It runs
// over stdio by default, and over HTTP when given a port, matching the two
// transports MCP clients use.

// MCP is a tool server bound to one browser and its current page.
type MCP struct {
	browser *browser.Browser

	mu   sync.Mutex
	page *browser.Page
}

// NewMCP creates an MCP server for a browser.
func NewMCP(b *browser.Browser) *MCP { return &MCP{browser: b} }

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
		if resp := s.Handle(line); resp != nil {
			if err := enc.Encode(resp); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

// ServeHTTP serves the streamable-HTTP transport: a JSON-RPC request POSTed to
// /mcp and answered with a single JSON response.
func (s *MCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	resp := s.Handle(body)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Mcp-Session-Id", "gobrowser")
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// Handle processes one message and returns the reply, or nil for a
// notification (which has no id).
func (s *MCP) Handle(data []byte) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return &rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}}
	}
	if len(req.ID) == 0 {
		// A notification: nothing to answer.
		return nil
	}
	result, rerr := s.dispatch(req.Method, req.Params)
	resp := &rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if rerr != nil {
		resp.Error = rerr
	} else {
		resp.Result = result
	}
	return resp
}

func (s *MCP) dispatch(method string, params json.RawMessage) (any, *rpcError) {
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
		return s.callTool(params)
	}
	return nil, &rpcError{Code: -32601, Message: "method not found: " + method}
}

func (s *MCP) callTool(params json.RawMessage) (any, *rpcError) {
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
	case "navigate":
		if args.URL == "" {
			return text("error: url is required"), nil
		}
		p, err := s.browser.Open(args.URL)
		if err != nil {
			return fail(err), nil
		}
		s.mu.Lock()
		s.page = p
		s.mu.Unlock()
		return text(p.Text()), nil

	case "get_content":
		pg := s.current()
		if pg == nil {
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
		pg := s.current()
		if pg == nil {
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
		pg := s.current()
		if pg == nil {
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
		pg := s.current()
		if pg == nil {
			return text("error: no page loaded; call navigate first"), nil
		}
		if err := pg.Click(args.Selector); err != nil {
			return fail(err), nil
		}
		return text("clicked " + args.Selector), nil

	case "type":
		pg := s.current()
		if pg == nil {
			return text("error: no page loaded; call navigate first"), nil
		}
		if err := pg.Type(args.Selector, args.Text); err != nil {
			return fail(err), nil
		}
		return text("typed into " + args.Selector), nil

	case "structured_data":
		pg := s.current()
		if pg == nil {
			return text("error: no page loaded; call navigate first"), nil
		}
		b, _ := json.Marshal(pg.StructuredData())
		return text(string(b)), nil
	}
	return nil, &rpcError{Code: -32602, Message: "unknown tool: " + p.Name}
}

func (s *MCP) current() *browser.Page {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.page
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
