package requests

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	tls "github.com/bogdanfinn/utls"

	"github.com/HashShin/shade/impersonate"
)

// clientKey identifies a cached transport. Timeouts and redirect policy are
// applied per request, so they are not part of the key.
type clientKey struct {
	impersonate string
	proxy       string
	verify      bool
	httpVersion string
	iface       string
	certFile    string
	keyFile     string
}

func keyFor(cfg *config) clientKey {
	return clientKey{
		impersonate: cfg.impersonate,
		proxy:       effectiveProxy(cfg),
		verify:      cfg.verify,
		httpVersion: normalizeHTTPVersion(cfg.httpVersion),
		iface:       cfg.interfaceName,
		certFile:    certFile(cfg),
		keyFile:     certKey(cfg),
	}
}

func certFile(cfg *config) string {
	if cfg.cert == nil {
		return ""
	}
	return cfg.cert.CertFile
}

func certKey(cfg *config) string {
	if cfg.cert == nil {
		return ""
	}
	return cfg.cert.KeyFile
}

func normalizeHTTPVersion(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "auto":
		return ""
	case "v1", "1", "1.0", "1.1", "http/1.0", "http/1.1":
		return "v1"
	case "v2", "2", "2.0", "http/2", "http/2.0", "h2":
		return "v2"
	case "v3", "3", "3.0", "http/3", "http/3.0", "h3":
		return "v3"
	default:
		return ""
	}
}

// effectiveProxy resolves the proxy URL for a config, applying the environment
// when trust_env is set and no explicit proxy was configured.
func effectiveProxy(cfg *config) string {
	if len(cfg.proxies) > 0 {
		if v := cfg.proxies["all"]; v != "" {
			return v
		}
		if v := cfg.proxies["https"]; v != "" {
			return v
		}
		if v := cfg.proxies["http"]; v != "" {
			return v
		}
	}
	if cfg.trustEnv {
		if v := os.Getenv("HTTPS_PROXY"); v != "" {
			return v
		}
		if v := os.Getenv("https_proxy"); v != "" {
			return v
		}
		if v := os.Getenv("ALL_PROXY"); v != "" {
			return v
		}
		if v := os.Getenv("all_proxy"); v != "" {
			return v
		}
	}
	return ""
}

var noopLogger = tls_client.NewNoopLogger()

// isNativeImpersonation reports whether the config requests the non-browser
// (Go) transport. An empty name means no impersonation, which mirrors
// curl_cffi's behaviour of using libcurl's default (non-browser) TLS stack.
func isNativeImpersonation(name string) bool {
	switch strings.ToLower(name) {
	case "", "native", "none", "go":
		return true
	}
	return false
}

// newTransportClient builds a transport for the given config.
func newTransportClient(cfg *config) (httpDoer, error) {
	if isNativeImpersonation(cfg.impersonate) {
		return newNativeClient(
			normalizeHTTPVersion(cfg.httpVersion) == "v1",
			!cfg.verify,
			effectiveProxy(cfg),
			cfg.interfaceName,
		)
	}

	profileName := "chrome_150"
	var preset *impersonate.Preset
	if usesCurlTLS(cfg.impersonate) {
		base := profiles.MappedTLSClients["chrome_150"]
		return newTLSClient(cfg, curlClientProfile(base), true)
	}
	if cfg.impersonate != "" {
		p, err := impersonate.Get(cfg.impersonate)
		if err != nil {
			return nil, &ImpersonateError{newError(err.Error(), 0, nil)}
		}
		preset = p
		profileName = preset.TLSProfile()
		if profileName == "" {
			return nil, &ImpersonateError{newError(
				fmt.Sprintf("impersonate: no TLS profile for %q", cfg.impersonate), 0, nil)}
		}
	}

	base, ok := profiles.MappedTLSClients[profileName]
	if !ok {
		return nil, &ImpersonateError{newError(
			fmt.Sprintf("impersonate: tls profile %q is not available", profileName), 0, nil)}
	}

	// Always build a profile from the preset so that the HTTP/2 settings,
	// pseudo header order and header priority match curl-impersonate exactly.
	base = profileForPreset(preset, base)

	return newTLSClient(cfg, base, false)
}

// newTLSClient builds a tls-client transport around a ClientProfile.
// When disableCompression is set the transport stops adding its own
// Accept-Encoding header, which the curl target needs to stay byte-identical.
func newTLSClient(cfg *config, base profiles.ClientProfile, disableCompression bool) (httpDoer, error) {
	opts := []tls_client.HttpClientOption{
		tls_client.WithClientProfile(base),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithTimeout(0),
	}
	if cfg.debug {
		opts = append(opts, tls_client.WithDebug())
	}
	if !cfg.verify {
		opts = append(opts, tls_client.WithInsecureSkipVerify())
	}

	switch normalizeHTTPVersion(cfg.httpVersion) {
	case "v1":
		opts = append(opts, tls_client.WithForceHttp1())
	case "v3":
		opts = append(opts, tls_client.WithProtocolRacing())
	}

	if p := effectiveProxy(cfg); p != "" {
		if cfg.proxyAuth != nil {
			p = injectProxyAuth(p, cfg.proxyAuth)
		}
		opts = append(opts, tls_client.WithProxyUrl(p))
	}

	if cfg.interfaceName != "" {
		if addr := net.ParseIP(cfg.interfaceName); addr != nil {
			family := "tcp4"
			if addr.To4() == nil {
				family = "tcp6"
			}
			opts = append(opts, tls_client.WithDialer(net.Dialer{
				LocalAddr: &net.TCPAddr{IP: addr},
			}))
			_ = family
		}
	}

	transportOptions := &tls_client.TransportOptions{DisableCompression: disableCompression}
	if cfg.cert != nil {
		cert, err := tls.LoadX509KeyPair(cfg.cert.CertFile, cfg.cert.KeyFile)
		if err != nil {
			return nil, &ConnectionError{newError(
				fmt.Sprintf("load client certificate: %v", err), 0, nil)}
		}
		transportOptions.Certificates = []tls.Certificate{cert}
	}
	if disableCompression || cfg.cert != nil {
		opts = append(opts, tls_client.WithTransportOptions(transportOptions))
	}

	client, err := tls_client.NewHttpClient(noopLogger, opts...)
	if err != nil {
		return nil, NewRequestException(err.Error(), 0, nil)
	}
	return client, nil
}

func injectProxyAuth(proxyURL string, auth *BasicAuth) string {
	if auth == nil || auth.Username == "" {
		return proxyURL
	}
	scheme := ""
	rest := proxyURL
	if i := strings.Index(proxyURL, "://"); i >= 0 {
		scheme = proxyURL[:i+3]
		rest = proxyURL[i+3:]
	}
	if strings.Contains(rest, "@") {
		return proxyURL
	}
	return scheme + auth.Username + ":" + auth.Password + "@" + rest
}

// buildHTTPRequest converts a resolved config + body into an fhttp request with
// header order metadata attached.
func buildHTTPRequest(method, url string, headers *Headers, body []byte, stream io.Reader) (*http.Request, error) {
	var reader io.Reader
	if stream != nil {
		reader = stream
	} else if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return nil, &InvalidURL{newError(err.Error(), 3, nil)}
	}
	m, order := headers.toTransport()
	req.Header = http.Header(m)
	req.Header[http.HeaderOrderKey] = order
	if len(body) > 0 && req.Header.Get("content-length") == "" {
		req.ContentLength = int64(len(body))
	}
	return req, nil
}
