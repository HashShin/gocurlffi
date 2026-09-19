package requests

import (
	"bufio"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// One tls-client cannot serve both schemes. It caches a transport under
// host:port for an http URL exactly as it does for an https one, so an http
// request to a host poisons the https request that follows it in the same
// session: the https hop is handed the cached http transport, whose dial goes
// through the function that reports success by returning the sentinel
// "protocol negotiated". That string then surfaces as the request error.
//
// Seen on https://apis.google.com, whose 301 points at
// http://developers.google.com/ and so walks both schemes in one redirect
// chain.
func TestHTTPSAfterHTTPToTheSameHost(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	httpURL := "http://" + strings.TrimPrefix(srv.URL, "https://")

	s := NewSession()
	defer s.Close()

	// The plaintext request cannot succeed against a TLS listener. That is
	// not the point: it is enough that it caches a transport for host:port,
	// which is what the https request then trips over.
	_, _ = s.Get(httpURL,
		WithImpersonate("chrome"), WithVerify(false), WithTimeout(3*time.Second))

	rsp, err := s.Get(srv.URL,
		WithImpersonate("chrome"), WithVerify(false), WithTimeout(10*time.Second))
	if err != nil {
		t.Fatalf("https after http to the same host:port: %v", err)
	}
	if got := string(rsp.Content); got != "ok" {
		t.Errorf("body = %q, want ok", got)
	}
}

// The same collision inside one Send. The chain starts on one https origin and
// redirects to a plain-http URL on another host, which redirects back to https
// on that same host. The http hop is what caches a transport with no protocol
// kind recorded for host:port, and the https hop that follows is what trips
// over it - which is the shape of https://apis.google.com, whose 301 Location
// is plain http.
func TestCrossSchemeRedirectInOneSend(t *testing.T) {
	dualAddr := dualSchemeServer(t)

	// The first hop is https on a host of its own, so the http hop below is the
	// first request to dualAddr: that is the entry the https hop then inherits.
	front := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+dualAddr+"/step", http.StatusMovedPermanently)
	}))
	defer front.Close()

	s := NewSession()
	defer s.Close()

	rsp, err := s.Get(front.URL+"/",
		WithImpersonate("chrome"), WithVerify(false),
		WithAllowRedirects(true), WithTimeout(10*time.Second))
	if err != nil {
		t.Fatalf("cross-scheme redirect: %v", err)
	}
	if got := string(rsp.Content); got != "ok" {
		t.Errorf("body = %q, want ok", got)
	}
	if rsp.RedirectCount != 2 {
		t.Errorf("redirects = %d, want 2", rsp.RedirectCount)
	}
}

// dualSchemeServer serves plaintext HTTP and TLS on the same port, so an http
// URL and an https URL can name the same host:port - which is exactly what a
// redirect between the schemes produces, and what one shared transport cache
// cannot survive. The handler routes by scheme: http/step redirects to
// https/ok, and https/ok answers "ok".
func dualSchemeServer(t *testing.T) (addr string) {
	t.Helper()

	// A throwaway TLS server lends its self-signed certificate.
	certSrv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	tlsConfig := certSrv.TLS.Clone()
	certSrv.Close()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		switch {
		case scheme == "http" && r.URL.Path == "/step":
			http.Redirect(w, r, "https://"+r.Host+"/ok", http.StatusMovedPermanently)
		case scheme == "https" && r.URL.Path == "/ok":
			_, _ = io.WriteString(w, "ok")
		default:
			http.Error(w, "unexpected "+scheme+r.URL.Path, http.StatusNotFound)
		}
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr = ln.Addr().String()

	plainCh := make(chan net.Conn)
	tlsCh := make(chan net.Conn)
	stop := make(chan struct{})

	// One accept loop hands each connection to the server that speaks its
	// protocol: a TLS ClientHello starts with the handshake record byte 0x16.
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				br := bufio.NewReader(c)
				first, err := br.Peek(1)
				if err != nil {
					c.Close()
					return
				}
				ch := plainCh
				if first[0] == 0x16 {
					ch = tlsCh
				}
				select {
				case ch <- &peekedConn{Conn: c, r: br}:
				case <-stop:
					c.Close()
				}
			}(c)
		}
	}()

	plain := &http.Server{Handler: handler}
	secure := &http.Server{Handler: handler}
	go func() { _ = plain.Serve(&chanListener{ch: plainCh, addr: ln.Addr()}) }()
	go func() { _ = secure.Serve(tls.NewListener(&chanListener{ch: tlsCh, addr: ln.Addr()}, tlsConfig)) }()

	t.Cleanup(func() {
		close(stop)
		ln.Close()
	})
	return addr
}

// chanListener feeds a server connections another loop accepted.
type chanListener struct {
	ch   chan net.Conn
	addr net.Addr
}

func (l *chanListener) Accept() (net.Conn, error) {
	c, ok := <-l.ch
	if !ok {
		return nil, net.ErrClosed
	}
	return c, nil
}
func (l *chanListener) Close() error   { return nil }
func (l *chanListener) Addr() net.Addr { return l.addr }

// peekedConn is a connection whose first bytes have already been read to
// decide which server owns it.
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(b []byte) (int, error) { return c.r.Read(b) }
