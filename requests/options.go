package requests

import (
	"net/url"
	"time"
)

// Params is an ordered query parameter list. Use Param to preserve duplicates.
type Params []Param

// Param is one query string key/value pair.
type Param struct {
	Key   string
	Value string
}

// BasicAuth holds HTTP basic-auth credentials.
type BasicAuth struct {
	Username string
	Password string
}

// ClientCert holds a client certificate and key file path.
type ClientCert struct {
	CertFile string
	KeyFile  string
}

// ExtraFingerprints carries the per-request fingerprint overrides that
// curl_cffi exposes through extra_fp. Only the fields understood by this port
// are represented.
type ExtraFingerprints struct {
	TLSMinVersion        string
	TLSGrease            *bool
	TLSPermuteExtensions *bool
	TLSCertCompression   string
	TLSRecordSizeLimit   int
	HTTP2StreamWeight    *int
	HTTP2StreamExclusive *int
	HTTP2NoPriority      *bool
	HeaderOrder          string
	SplitCookies         *bool
}

// config is the fully resolved request configuration used internally.
type config struct {
	headers  *Headers
	cookies  *Cookies
	auth     *BasicAuth
	baseURL  string
	params   Params
	verify   bool
	timeout  time.Duration
	trustEnv bool

	allowRedirects bool
	maxRedirects   int
	retry          int

	impersonate     string
	ja3             string
	akamai          string
	extraFP         *ExtraFingerprints
	defaultHeaders  bool
	defaultEncoding string
	httpVersion     string

	interfaceName string
	cert          *ClientCert
	proxies       map[string]string
	proxyAuth     *BasicAuth

	discardCookies    bool
	raiseForStatus    bool
	dohURL            string
	debug             bool
	acceptEncodingSet bool

	// per-request only
	data            any
	content         any
	jsonBody        any
	stream          bool
	contentCallback func([]byte) error
	maxRecvSpeed    int
	quote           any
}

func defaultConfig() config {
	return config{
		headers:         &Headers{},
		cookies:         NewCookies(nil),
		verify:          true,
		timeout:         30 * time.Second,
		trustEnv:        true,
		allowRedirects:  true,
		maxRedirects:    30,
		defaultHeaders:  true,
		defaultEncoding: "utf-8",
	}
}

// Option configures a Session or a single request.
type Option func(*config)

// WithParams sets the query string. Accepts Params, url.Values, map[string]string
// or map[string][]string.
func WithParams(p any) Option {
	return func(c *config) { c.params = toParams(p) }
}

func toParams(p any) Params {
	switch v := p.(type) {
	case nil:
		return nil
	case Params:
		return v
	case []Param:
		return Params(v)
	case url.Values:
		var out Params
		for _, k := range sortedKeys(v) {
			for _, val := range v[k] {
				out = append(out, Param{k, val})
			}
		}
		return out
	case map[string]string:
		var out Params
		for _, k := range sortedMapKeys(v) {
			out = append(out, Param{k, v[k]})
		}
		return out
	case map[string][]string:
		var out Params
		for _, k := range sortedKeys(v) {
			for _, val := range v[k] {
				out = append(out, Param{k, val})
			}
		}
		return out
	default:
		panic("requests: unsupported params type")
	}
}

// WithData sets the request body. Accepts string, []byte, io.Reader,
// map[string]string, url.Values or Params (the latter are form-encoded).
func WithData(v any) Option { return func(c *config) { c.data = v } }

// WithContent sets the raw request body, allowing streaming io.Reader bodies.
func WithContent(v any) Option { return func(c *config) { c.content = v } }

// WithJSON sets a JSON request body and the application/json content type.
func WithJSON(v any) Option { return func(c *config) { c.jsonBody = v } }

// WithHeaders sets request headers.
func WithHeaders(h HeaderTypes) Option { return func(c *config) { c.headers.Update(h) } }

// WithHeader sets a single request header.
func WithHeader(name, value string) Option {
	return func(c *config) { c.headers.Set(name, value) }
}

// WithCookies sets request cookies.
func WithCookies(cookieTypes CookieTypes) Option {
	return func(c *config) { c.cookies.Update(cookieTypes) }
}

// WithAuth sets HTTP basic auth credentials.
func WithAuth(username, password string) Option {
	return func(c *config) { c.auth = &BasicAuth{username, password} }
}

// WithTimeout sets the total request timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

// WithTimeoutSeconds sets the total request timeout in seconds.
func WithTimeoutSeconds(s float64) Option {
	return func(c *config) { c.timeout = time.Duration(s * float64(time.Second)) }
}

// WithAllowRedirects controls whether redirects are followed.
func WithAllowRedirects(v bool) Option {
	return func(c *config) { c.allowRedirects = v }
}

// WithMaxRedirects sets the maximum number of redirects (use -1 for unlimited).
func WithMaxRedirects(n int) Option { return func(c *config) { c.maxRedirects = n } }

// WithProxy sets a single proxy URL used for all schemes.
func WithProxy(proxyURL string) Option {
	return func(c *config) { c.proxies = map[string]string{"all": proxyURL} }
}

// WithProxies sets per-scheme or per-host proxy URLs, e.g.
// {"http": "http://...", "https": "http://...", "all": "..."}.
func WithProxies(p map[string]string) Option {
	return func(c *config) {
		cp := make(map[string]string, len(p))
		for k, v := range p {
			cp[k] = v
		}
		c.proxies = cp
	}
}

// WithProxyAuth sets proxy credentials.
func WithProxyAuth(username, password string) Option {
	return func(c *config) { c.proxyAuth = &BasicAuth{username, password} }
}

// WithVerify controls TLS certificate verification.
func WithVerify(v bool) Option { return func(c *config) { c.verify = v } }

// WithReferer sets the Referer header.
func WithReferer(r string) Option {
	return func(c *config) { c.headers.Set("Referer", r) }
}

// WithAcceptEncoding sets the Accept-Encoding header. Pass an empty string to
// remove it and let the transport handle decompression.
func WithAcceptEncoding(v string) Option {
	return func(c *config) {
		c.acceptEncodingSet = true
		if v == "" {
			c.headers.Del("Accept-Encoding")
			return
		}
		c.headers.Set("Accept-Encoding", v)
	}
}

// WithImpersonate selects a browser fingerprint preset to mimic.
func WithImpersonate(name string) Option {
	return func(c *config) { c.impersonate = name }
}

// WithJA3 sets a custom JA3 fingerprint string (advanced).
func WithJA3(s string) Option { return func(c *config) { c.ja3 = s } }

// WithAkamai sets a custom Akamai HTTP/2 fingerprint string (advanced).
func WithAkamai(s string) Option { return func(c *config) { c.akamai = s } }

// WithExtraFP sets extra fingerprint overrides.
func WithExtraFP(fp *ExtraFingerprints) Option {
	return func(c *config) { c.extraFP = fp }
}

// WithDefaultHeaders controls whether the impersonated browser's default headers
// are applied. User supplied headers always win.
func WithDefaultHeaders(v bool) Option {
	return func(c *config) { c.defaultHeaders = v }
}

// WithDefaultEncoding sets the charset used to decode responses that lack an
// explicit charset.
func WithDefaultEncoding(enc string) Option {
	return func(c *config) { c.defaultEncoding = enc }
}

// WithHTTPVersion selects the HTTP version: "v1", "v2", "v3".
func WithHTTPVersion(v string) Option {
	return func(c *config) { c.httpVersion = v }
}

// WithInterface binds the outgoing connection to a network interface or local IP.
func WithInterface(name string) Option {
	return func(c *config) { c.interfaceName = name }
}

// WithCert sets a client certificate and key file for mutual TLS.
func WithCert(certFile, keyFile string) Option {
	return func(c *config) { c.cert = &ClientCert{certFile, keyFile} }
}

// WithStream keeps the response body unread so it can be consumed incrementally.
func WithStream(v bool) Option { return func(c *config) { c.stream = v } }

// WithContentCallback is invoked for every body chunk as it arrives.
func WithContentCallback(fn func([]byte) error) Option {
	return func(c *config) { c.contentCallback = fn }
}

// WithDiscardCookies prevents server cookies from updating the session.
func WithDiscardCookies(v bool) Option {
	return func(c *config) { c.discardCookies = v }
}

// WithRaiseForStatus makes the request return an error for 4xx/5xx responses.
func WithRaiseForStatus(v bool) Option {
	return func(c *config) { c.raiseForStatus = v }
}

// WithRetry sets the number of retries for failed requests.
func WithRetry(n int) Option { return func(c *config) { c.retry = n } }

// WithBaseURL sets a base URL used to resolve relative request URLs.
func WithBaseURL(u string) Option { return func(c *config) { c.baseURL = u } }

// WithTrustEnv controls whether http_proxy/https_proxy environment variables are
// honoured when no explicit proxy is configured.
func WithTrustEnv(v bool) Option { return func(c *config) { c.trustEnv = v } }

// WithDebug enables verbose transport logging.
func WithDebug(v bool) Option { return func(c *config) { c.debug = v } }

// WithMaxRecvSpeed limits the download rate in bytes per second.
func WithMaxRecvSpeed(n int) Option { return func(c *config) { c.maxRecvSpeed = n } }

// WithQuote controls URL percent-encoding behaviour (parity with curl_cffi's
// quote parameter). Pass false to disable automatic encoding.
func WithQuote(v any) Option { return func(c *config) { c.quote = v } }
