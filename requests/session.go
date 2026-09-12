// Package requests is a Go port of the curl_cffi.requests API. It provides a
// requests-like Session with browser TLS/HTTP fingerprint impersonation.
package requests

import (
	"context"
	"encoding/base64"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	tls_client "github.com/bogdanfinn/tls-client"

	"gocurlffi/impersonate"
)

// Session is a requests-like client with a shared cookie jar and connection
// pool. It is safe for concurrent use.
type Session struct {
	mu      sync.Mutex
	cfg     config
	jar     *Cookies
	clients map[clientKey]tls_client.HttpClient
	closed  bool
}

// NewSession creates a Session. Options set here become defaults for every
// request and can be overridden per request.
func NewSession(opts ...Option) *Session {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	s := &Session{
		cfg:     cfg,
		jar:     NewCookies(nil),
		clients: map[clientKey]tls_client.HttpClient{},
	}
	// Cookies passed to the session constructor go into the jar.
	s.jar.Update(cfg.cookies)
	cfg.cookies = NewCookies(nil)
	s.cfg = cfg
	s.cfg.verify = resolveVerify(cfg)
	return s
}

func resolveVerify(cfg config) bool {
	if !cfg.verify {
		return false
	}
	if cfg.cert != nil {
		return true
	}
	return true
}

// Close releases idle connections held by the session.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	for _, c := range s.clients {
		c.CloseIdleConnections()
	}
	s.clients = map[clientKey]tls_client.HttpClient{}
}

// Cookies returns the live session cookie jar.
func (s *Session) Cookies() *Cookies { return s.jar }

// Headers returns the session default headers. Mutating it affects subsequent
// requests.
func (s *Session) Headers() *Headers { return s.cfg.headers }

// SetCookies replaces the session cookie jar.
func (s *Session) SetCookies(c CookieTypes) {
	s.jar = NewCookies(c)
}

// Request sends an HTTP request using the session defaults plus opts.
func (s *Session) Request(method, rawURL string, opts ...Option) (*Response, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, &SessionClosed{newError("session is closed, cannot send request", 0, nil)}
	}
	cfg := s.cfg
	// copy pointer fields so per-request mutation does not leak into the session
	cfgHeaders := s.cfg.headers.Clone()
	cfg.headers = cfgHeaders
	cfg.cookies = NewCookies(nil)
	cfg.params = append(Params(nil), s.cfg.params...)
	s.mu.Unlock()

	for _, o := range opts {
		o(&cfg)
	}

	var lastErr error
	attempts := cfg.retry + 1
	if attempts < 1 {
		attempts = 1
	}
	for i := 0; i < attempts; i++ {
		rsp, err := s.requestOnce(strings.ToUpper(method), rawURL, &cfg)
		if err == nil {
			return rsp, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (s *Session) getClient(key clientKey, cfg *config) (tls_client.HttpClient, error) {
	s.mu.Lock()
	if c, ok := s.clients[key]; ok {
		s.mu.Unlock()
		return c, nil
	}
	s.mu.Unlock()
	c, err := newTransportClient(cfg)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.clients[key] = c
	s.mu.Unlock()
	return c, nil
}

func (s *Session) requestOnce(method, rawURL string, cfg *config) (*Response, error) {
	var preset *impersonate.Preset
	if cfg.impersonate != "" {
		p, err := impersonate.Get(cfg.impersonate)
		if err != nil {
			return nil, &ImpersonateError{newError(err.Error(), 0, nil)}
		}
		preset = p
	}

	body, stream, contentType, err := buildBody(cfg.content, cfg.data, cfg.jsonBody)
	if err != nil {
		return nil, err
	}

	finalURL, err := resolveURL(cfg.baseURL, rawURL, cfg.params)
	if err != nil {
		return nil, err
	}

	acceptEncoding := ""
	if preset != nil {
		for _, h := range preset.HTTPHeaders {
			if strings.EqualFold(h.Name, "Accept-Encoding") {
				acceptEncoding = h.Value
			}
		}
	}
	headers := buildFinalHeaders(cfg.headers, contentType, acceptEncoding, preset)

	if cfg.auth != nil {
		token := base64.StdEncoding.EncodeToString([]byte(cfg.auth.Username + ":" + cfg.auth.Password))
		headers.Set("Authorization", "Basic "+token)
	}
	if len(body) > 0 {
		headers.Set("Content-Length", itoa64(int64(len(body))))
	}

	jar := s.jar
	merged := NewCookies(nil)
	u, _ := url.Parse(finalURL)
	if u != nil {
		for _, ck := range jar.matching(u) {
			merged.SetCookie(ck)
		}
		for _, ck := range cfg.cookies.matching(u) {
			merged.SetCookie(ck)
		}
	}
	applyCookieHeader(headers, merged, preset)

	client, err := s.getClient(keyFor(cfg), cfg)
	if err != nil {
		return nil, err
	}

	history := []*Response{}
	currentURL := finalURL
	currentMethod := method
	var currentBody []byte = body
	var currentStream io.Reader = stream

	start := time.Now()
	var finalResp *Response

	for redirects := 0; ; redirects++ {
		var reader io.Reader
		if currentStream != nil {
			reader = currentStream
		}
		req, err := buildHTTPRequest(currentMethod, currentURL, headers, currentBody, reader)
		if err != nil {
			return nil, err
		}
		ctx := context.Background()
		var cancel context.CancelFunc
		if cfg.timeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, cfg.timeout)
		}
		req = req.WithContext(ctx)

		httpRsp, err := client.Do(req)
		if cancel != nil {
			cancel()
		}
		if err != nil {
			if isTimeoutErr(err) {
				return nil, &Timeout{newError(err.Error(), 28, nil)}
			}
			return nil, NewRequestException(err.Error(), 0, nil)
		}

		rsp := NewResponse()
		rsp.URL = currentURL
		rsp.StatusCode = httpRsp.StatusCode
		rsp.Reason = strings.TrimPrefix(httpRsp.Status, itoa(httpRsp.StatusCode)+" ")
		rsp.Ok = httpRsp.StatusCode >= 200 && httpRsp.StatusCode < 400
		rsp.Headers = parseHeadersFromTransport(httpRsp.Header)
		rsp.HTTPVersion = httpRsp.ProtoMajor
		rsp.DefaultEncoding = cfg.defaultEncoding
		rsp.Request = &Request{URL: currentURL, Method: currentMethod, Headers: headers, Body: currentBody}

		// record cookies from this hop
		if !(cfg.discardCookies || s.cfg.discardCookies) {
			if u, perr := url.Parse(currentURL); perr == nil {
				parseSetCookies(rsp.Headers, jar, u)
			}
		}
		rsp.Cookies = NewCookies(nil)
		if u, perr := url.Parse(currentURL); perr == nil {
			for _, ck := range jar.matching(u) {
				rsp.Cookies.SetCookie(ck)
			}
		}

		if cfg.stream && cfg.contentCallback == nil {
			// leave the body for the caller; only follow redirects when there
			// is no body consumed yet.
			if !(cfg.allowRedirects && rsp.IsRedirect() && httpRsp.Header.Get("location") != "") {
				rsp.Body = decodingReader(httpRsp.Body, rsp.Headers)
				rsp.Elapsed = time.Since(start)
				rsp.RedirectCount = len(history)
				rsp.History = history
				finalResp = rsp
				break
			}
			io.Copy(io.Discard, httpRsp.Body)
			httpRsp.Body.Close()
		} else {
			data, rerr := readBody(httpRsp.Body, rsp.Headers, cfg.contentCallback, cfg.maxRecvSpeed)
			httpRsp.Body.Close()
			if rerr != nil {
				return nil, NewRequestException(rerr.Error(), 0, nil)
			}
			rsp.Content = data
			rsp.DownloadSize = int64(len(data))
		}

		if cfg.allowRedirects && rsp.IsRedirect() {
			loc := httpRsp.Header.Get("location")
			if loc == "" {
				rsp.Elapsed = time.Since(start)
				rsp.RedirectCount = len(history)
				rsp.History = history
				finalResp = rsp
				break
			}
			if cfg.maxRedirects >= 0 && len(history) >= cfg.maxRedirects {
				return nil, &TooManyRedirects{newError("maximum redirects exceeded", 47, rsp)}
			}
			next, nerr := resolveLocation(currentURL, loc)
			if nerr != nil {
				return nil, nerr
			}
			// history entry has no body in curl_cffi
			hist := *rsp
			hist.Content = nil
			hist.Body = nil
			history = append(history, &hist)

			if rsp.StatusCode == 303 || ((rsp.StatusCode == 301 || rsp.StatusCode == 302) && currentMethod != "GET" && currentMethod != "HEAD") {
				currentMethod = "GET"
				currentBody = nil
				currentStream = nil
				headers.Del("Content-Length")
				headers.Del("Content-Type")
			}
			currentURL = next
			continue
		}

		rsp.Elapsed = time.Since(start)
		rsp.RedirectCount = len(history)
		rsp.RedirectURL = ""
		rsp.History = history
		finalResp = rsp
		break
	}

	if cfg.raiseForStatus {
		if err := finalResp.RaiseForStatus(); err != nil {
			return nil, err
		}
	}
	return finalResp, nil
}

func applyCookieHeader(headers *Headers, cookies *Cookies, preset *impersonate.Preset) {
	if cookies.Len() == 0 {
		return
	}
	headers.Del("Cookie")
	if preset != nil && preset.SplitCookies {
		for _, ck := range cookies.Items() {
			headers.Add("Cookie", ck.Name+"="+ck.Value)
		}
		return
	}
	headers.Set("Cookie", cookies.String())
}

func resolveLocation(base, loc string) (string, error) {
	if strings.HasPrefix(loc, "http://") || strings.HasPrefix(loc, "https://") {
		return loc, nil
	}
	bu, err := url.Parse(base)
	if err != nil {
		return "", &InvalidURL{newError(err.Error(), 3, nil)}
	}
	lu, err := url.Parse(loc)
	if err != nil {
		return "", &InvalidURL{newError(err.Error(), 3, nil)}
	}
	return bu.ResolveReference(lu).String(), nil
}

func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	if err == context.DeadlineExceeded || err == context.Canceled {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "timeout") ||
		strings.Contains(strings.ToLower(err.Error()), "deadline exceeded")
}

// ---------------------------------------------------------------------------
// convenience methods and package level helpers
// ---------------------------------------------------------------------------

// Get sends a GET request.
func (s *Session) Get(rawURL string, opts ...Option) (*Response, error) {
	return s.Request("GET", rawURL, opts...)
}

// Post sends a POST request.
func (s *Session) Post(rawURL string, opts ...Option) (*Response, error) {
	return s.Request("POST", rawURL, opts...)
}

// Put sends a PUT request.
func (s *Session) Put(rawURL string, opts ...Option) (*Response, error) {
	return s.Request("PUT", rawURL, opts...)
}

// Patch sends a PATCH request.
func (s *Session) Patch(rawURL string, opts ...Option) (*Response, error) {
	return s.Request("PATCH", rawURL, opts...)
}

// Delete sends a DELETE request.
func (s *Session) Delete(rawURL string, opts ...Option) (*Response, error) {
	return s.Request("DELETE", rawURL, opts...)
}

// Head sends a HEAD request.
func (s *Session) Head(rawURL string, opts ...Option) (*Response, error) {
	return s.Request("HEAD", rawURL, opts...)
}

// Options sends an OPTIONS request.
func (s *Session) Options(rawURL string, opts ...Option) (*Response, error) {
	return s.Request("OPTIONS", rawURL, opts...)
}

// Trace sends a TRACE request.
func (s *Session) Trace(rawURL string, opts ...Option) (*Response, error) {
	return s.Request("TRACE", rawURL, opts...)
}

// oneShot performs a request in a fresh session, mirroring the module level
// helpers in curl_cffi.requests.
func oneShot(method, rawURL string, opts ...Option) (*Response, error) {
	s := NewSession()
	defer s.Close()
	return s.Request(method, rawURL, opts...)
}

// Do sends a request in a throwaway session, mirroring curl_cffi's
// module-level requests.request helper.
func Do(method, rawURL string, opts ...Option) (*Response, error) {
	return oneShot(method, rawURL, opts...)
}

// Get sends a GET request in a throwaway session.
func Get(rawURL string, opts ...Option) (*Response, error) { return oneShot("GET", rawURL, opts...) }

// Post sends a POST request in a throwaway session.
func Post(rawURL string, opts ...Option) (*Response, error) { return oneShot("POST", rawURL, opts...) }

// Put sends a PUT request in a throwaway session.
func Put(rawURL string, opts ...Option) (*Response, error) { return oneShot("PUT", rawURL, opts...) }

// Patch sends a PATCH request in a throwaway session.
func Patch(rawURL string, opts ...Option) (*Response, error) {
	return oneShot("PATCH", rawURL, opts...)
}

// Delete sends a DELETE request in a throwaway session.
func Delete(rawURL string, opts ...Option) (*Response, error) {
	return oneShot("DELETE", rawURL, opts...)
}

// Head sends a HEAD request in a throwaway session.
func Head(rawURL string, opts ...Option) (*Response, error) { return oneShot("HEAD", rawURL, opts...) }

// Options sends an OPTIONS request in a throwaway session.
func Options(rawURL string, opts ...Option) (*Response, error) {
	return oneShot("OPTIONS", rawURL, opts...)
}

// Trace sends a TRACE request in a throwaway session.
func Trace(rawURL string, opts ...Option) (*Response, error) {
	return oneShot("TRACE", rawURL, opts...)
}
