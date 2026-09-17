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

	"github.com/HashShin/gocurlffi/impersonate"
)

// Session is a requests-like client with a shared cookie jar and connection
// pool. It is safe for concurrent use.
type Session struct {
	mu      sync.Mutex
	cfg     config
	jar     *Cookies
	clients map[clientKey]httpDoer
	closed  bool
	// browser is the renderer installed by the browser package, created on the
	// first Request that sets Browser and closed with the session.
	browser BrowserRenderer
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
		clients: map[clientKey]httpDoer{},
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

// UserAgent reports the User-Agent this session sends: the impersonated
// browser's, when a preset is selected, and the curl default otherwise. A
// caller that also exposes a user agent to page scripts uses this so that what
// the server is told and what navigator.userAgent reports are the same
// browser, rather than one desktop Chrome to the server and another to the
// page.
func (s *Session) UserAgent() string {
	if s.cfg.impersonate != "" && !isCurlImpersonation(s.cfg.impersonate) {
		if p, err := impersonate.Get(s.cfg.impersonate); err == nil {
			for _, h := range p.HTTPHeaders {
				if strings.EqualFold(h.Name, "User-Agent") {
					return h.Value
				}
			}
		}
	}
	if isCurlImpersonation(s.cfg.impersonate) {
		return curlDefaultUserAgent
	}
	return ""
}

// Browse loads a request in the browser, which is exactly Send on a request
// with Browser set: the same page, the same session's cookies and the same
// fingerprint, spelled so the call site says which path it takes. Use it when
// the two paths are mixed and reading which is which matters.
func (s *Session) Browse(req Request) (*Response, error) {
	req.Browser = true
	return s.Send(req)
}

// sendBrowser loads a request in the browser the browser package installed,
// creating it for this session on first use so pages and plain requests share
// one cookie jar.
func (s *Session) sendBrowser(req Request) (*Response, error) {
	if method := strings.ToUpper(strings.TrimSpace(req.Method)); method != "" && method != "GET" {
		return nil, &InterfaceError{newError(
			"Request.Browser loads a URL: it does not send a "+method, 0, nil)}
	}
	if len(req.Body) > 0 || req.JSON != nil {
		return nil, &InterfaceError{newError(
			"Request.Browser loads a URL: a request body has no browser path", 0, nil)}
	}
	url := req.URL
	if req.Params != nil {
		// The browser loads a URL, so the query has to be on it before the
		// renderer sees the request.
		withParams, err := applyParams(url, toParams(req.Params))
		if err != nil {
			return nil, err
		}
		req.URL = withParams
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, &SessionClosed{newError("session is closed", 0, nil)}
	}
	renderer := s.browser
	if renderer == nil {
		factory := browserRenderer()
		if factory == nil {
			s.mu.Unlock()
			return nil, &InterfaceError{newError(
				"Request.Browser needs the browser package: import github.com/HashShin/gocurlffi/browser", 0, nil)}
		}
		renderer = factory(s)
		s.browser = renderer
	}
	s.mu.Unlock()

	return renderer.Send(req)
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
	s.clients = map[clientKey]httpDoer{}
	if s.browser != nil {
		s.browser.Close()
		s.browser = nil
	}
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

	// Fail before any connection is made. An option this port cannot honour
	// must not be silently dropped: the caller would get a request carrying a
	// different fingerprint than the one they asked for.
	if bad := cfg.unsupportedOptions(); len(bad) > 0 {
		return nil, &UnsupportedOptionError{newError(
			"requests: unsupported option(s): "+strings.Join(bad, "; "), 0, nil)}
	}

	if cfg.browser {
		// The same request as a Request value: the browser path takes a URL,
		// so the options that shape the load and not the body carry over.
		return s.sendBrowser(Request{
			Method:      method,
			URL:         rawURL,
			Headers:     cfg.headers,
			Params:      cfg.params,
			Impersonate: cfg.impersonate,
			Timeout:     cfg.timeout,
			Proxy:       effectiveProxy(&cfg),
		})
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

func (s *Session) getClient(key clientKey, cfg *config) (httpDoer, error) {
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
	if !isNativeImpersonation(cfg.impersonate) && !isCurlImpersonation(cfg.impersonate) {
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

	baseHeaders := buildFinalHeaders(cfg.headers, contentType, preset, cfg.defaultHeaders)
	// Browsers send Accept-Encoding as part of their header set, which keeps
	// it in the fingerprint position; only add the libcurl default when the
	// user did not choose one and no preset supplied it.
	if !cfg.acceptEncodingSet && !isCurlImpersonation(cfg.impersonate) && !baseHeaders.Has("Accept-Encoding") {
		baseHeaders.Set("Accept-Encoding", "gzip, deflate, br")
	}
	if isCurlImpersonation(cfg.impersonate) {
		applyCurlDefaults(baseHeaders)
	}

	authHeader := ""
	if cfg.auth != nil {
		token := base64.StdEncoding.EncodeToString([]byte(cfg.auth.Username + ":" + cfg.auth.Password))
		authHeader = "Basic " + token
	}
	if len(body) > 0 {
		baseHeaders.Set("Content-Length", itoa64(int64(len(body))))
	}

	jar := s.jar
	originalHost := hostOf(finalURL)

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
		// Headers are rebuilt for every hop so that cookies set by a redirect
		// response are sent on the next request, matching libcurl.
		headers := baseHeaders.Clone()
		if authHeader != "" && hostOf(currentURL) == originalHost {
			headers.Set("Authorization", authHeader)
		}
		applyCookieHeader(headers, mergedCookiesFor(jar, cfg.cookies, currentURL), preset)

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
		if err != nil {
			if cancel != nil {
				cancel()
			}
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
				rsp.Body = &cancelReadCloser{
					ReadCloser: decodingReader(httpRsp.Body, rsp.Headers),
					cancel:     cancel,
				}
				rsp.Elapsed = time.Since(start)
				rsp.RedirectCount = len(history)
				rsp.History = history
				finalResp = rsp
				break
			}
			io.Copy(io.Discard, httpRsp.Body)
			httpRsp.Body.Close()
			if cancel != nil {
				cancel()
			}
		} else {
			data, rerr := readBody(httpRsp.Body, rsp.Headers, cfg.contentCallback, cfg.maxRecvSpeed)
			httpRsp.Body.Close()
			if cancel != nil {
				cancel()
			}
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
				baseHeaders.Del("Content-Length")
				baseHeaders.Del("Content-Type")
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
		headers.Del("Cookie")
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

// hostOf returns the lower-cased host of a URL, or "" if it cannot be parsed.
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

// mergedCookiesFor combines the session jar with request-level cookies that
// apply to rawURL.
func mergedCookiesFor(jar, requestCookies *Cookies, rawURL string) *Cookies {
	merged := NewCookies(nil)
	u, err := url.Parse(rawURL)
	if err != nil {
		return merged
	}
	for _, ck := range jar.matching(u) {
		merged.SetCookie(ck)
	}
	for _, ck := range requestCookies.matching(u) {
		merged.SetCookie(ck)
	}
	return merged
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

// Send performs the request described by req. It is the struct-shaped
// counterpart to Request(method, url, opts...): the same request can be built
// as a value, stored, or filled in from configuration, instead of being spread
// across options at the call site.
//
//	req := requests.Request{
//		Method:      "GET",
//		URL:         "https://httpbun.com/get",
//		Headers:     []string{"Accept: application/json"},
//		Impersonate: impersonate.Chrome131,
//	}
//	resp, err := sess.Send(req)
//
// Fields left at their zero value fall back to the session's defaults, so an
// empty Method means GET and an empty Timeout means the session timeout.
func (s *Session) Send(req Request) (*Response, error) {
	if req.Browser {
		return s.sendBrowser(req)
	}
	method := req.Method
	if method == "" {
		method = "GET"
	}

	var opts []Option
	if req.Headers != nil {
		opts = append(opts, WithHeaders(req.Headers))
	}
	if req.Params != nil {
		opts = append(opts, WithParams(req.Params))
	}
	if req.Cookies != nil {
		opts = append(opts, WithCookies(req.Cookies))
	}
	switch {
	case req.JSON != nil:
		opts = append(opts, WithJSON(req.JSON))
	case len(req.Body) > 0:
		opts = append(opts, WithContent(req.Body))
	}
	if req.Impersonate != "" {
		opts = append(opts, WithImpersonate(req.Impersonate))
	}
	if req.Timeout > 0 {
		opts = append(opts, WithTimeout(req.Timeout))
	}
	if req.Proxy != "" {
		opts = append(opts, WithProxy(req.Proxy))
	}
	return s.Request(method, req.URL, opts...)
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

// Send performs req in a throwaway session, so a Request built as a value can
// be sent without a Session of its own:
//
//	rsp, err := Send(Request{
//		Method:      "GET",
//		URL:         "https://httpbun.com/get",
//		Impersonate: Chrome146,
//	})
//
// It reads the whole body before returning, like the other helpers here. Use
// Session.Send instead when cookies and connections should be reused across
// requests.
func Send(req Request) (*Response, error) {
	s := NewSession()
	defer s.Close()
	return s.Send(req)
}

// Browse loads a request in the browser without a session of your own, exactly
// as Send does with Browser set. It needs the browser package imported, which
// is what installs it.
func Browse(req Request) (*Response, error) {
	req.Browser = true
	return Send(req)
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
