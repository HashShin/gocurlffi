package requests

import (
	"crypto/tls"
	"io"
	"net"
	nhttp "net/http"
	"net/url"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
)

// httpDoer is the subset of the transport client used by the request pipeline.
// It is satisfied by both the impersonating transport (tls-client) and the
// native Go transport.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
	CloseIdleConnections()
}

// nativeClient performs requests with Go's own TLS and HTTP stacks. It is used
// when no browser is impersonated. Some sites (for example Akamai-protected
// endpoints that challenge unsolicited browser fingerprints) accept a generic
// client but reject a browser-like fingerprint that lacks their sensor cookie;
// this is also what curl_cffi does when no impersonation is configured.
type nativeClient struct {
	client *nhttp.Client
}

func newNativeClient(forceHTTP1 bool, insecure bool, proxyURL string, localAddr string) (*nativeClient, error) {
	tr := &nhttp.Transport{
		Proxy:                 nhttp.ProxyFromEnvironment,
		ForceAttemptHTTP2:     !forceHTTP1,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	if forceHTTP1 {
		tr.TLSNextProto = map[string]func(string, *tls.Conn) nhttp.RoundTripper{}
	}
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	if localAddr != "" {
		if ip := net.ParseIP(localAddr); ip != nil {
			tr.DialContext = (&net.Dialer{LocalAddr: &net.TCPAddr{IP: ip}}).DialContext
		}
	}
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, err
		}
		tr.Proxy = nhttp.ProxyURL(u)
	}
	return &nativeClient{client: &nhttp.Client{
		Transport: tr,
		// Redirects are followed by the request pipeline so that history and
		// per-hop cookies behave like the impersonating transport.
		CheckRedirect: func(*nhttp.Request, []*nhttp.Request) error {
			return nhttp.ErrUseLastResponse
		},
	}}, nil
}

func (c *nativeClient) CloseIdleConnections() {
	if tr, ok := c.client.Transport.(*nhttp.Transport); ok {
		tr.CloseIdleConnections()
	}
}

// Do adapts an fhttp request to net/http, runs it, and adapts the response back.
func (c *nativeClient) Do(req *http.Request) (*http.Response, error) {
	outReq, err := nhttp.NewRequestWithContext(req.Context(), req.Method, req.URL.String(), req.Body)
	if err != nil {
		return nil, err
	}
	if req.ContentLength >= 0 {
		outReq.ContentLength = req.ContentLength
	}
	for k, vals := range req.Header {
		if strings.HasPrefix(k, "Header-Order") || strings.HasPrefix(k, "PHeader-Order") {
			continue
		}
		for _, v := range vals {
			outReq.Header.Add(k, v)
		}
	}
	if req.Host != "" {
		outReq.Host = req.Host
	}
	nresp, err := c.client.Do(outReq)
	if err != nil {
		return nil, err
	}
	hdr := http.Header{}
	for k, vals := range nresp.Header {
		hdr[k] = append([]string(nil), vals...)
	}
	return &http.Response{
		Status:        nresp.Status,
		StatusCode:    nresp.StatusCode,
		ProtoMajor:    nresp.ProtoMajor,
		ProtoMinor:    nresp.ProtoMinor,
		Header:        hdr,
		Body:          io.NopCloser(nresp.Body),
		ContentLength: nresp.ContentLength,
		Request:       req,
	}, nil
}
