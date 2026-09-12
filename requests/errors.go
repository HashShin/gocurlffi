package requests

import (
	"errors"
	"fmt"
	"strings"
)

// RequestException is the base error for the requests package. It carries an
// optional associated *Response and a curl-like numeric code for parity with
// curl_cffi.
type RequestException struct {
	Msg      string
	Code     int
	Response *Response
}

func (e *RequestException) Error() string {
	if e == nil {
		return ""
	}
	return e.Msg
}

func (e *RequestException) Unwrap() error { return nil }

func newError(msg string, code int, rsp *Response) *RequestException {
	return &RequestException{Msg: msg, Code: code, Response: rsp}
}

// ConnectionError indicates a connection level failure.
type ConnectionError struct{ *RequestException }

// DNSError indicates the host could not be resolved.
type DNSError struct{ *ConnectionError }

// ProxyError indicates a proxy failure.
type ProxyError struct{ *RequestException }

// SSLError indicates a TLS failure.
type SSLError struct{ *ConnectionError }

// CertificateVerifyError indicates certificate validation failed.
type CertificateVerifyError struct{ *SSLError }

// Timeout indicates the request timed out.
type Timeout struct{ *RequestException }

// TooManyRedirects indicates the redirect limit was exceeded.
type TooManyRedirects struct{ *RequestException }

// InvalidURL indicates a malformed URL.
type InvalidURL struct{ *RequestException }

// InvalidSchema indicates an unsupported URL scheme.
type InvalidSchema struct{ *RequestException }

// InterfaceError indicates the requested outgoing interface could not be used.
type InterfaceError struct{ *RequestException }

// ImpersonateError indicates the requested impersonation target is unknown or
// the TLS configuration failed.
type ImpersonateError struct{ *RequestException }

// SessionClosed indicates the session was already closed.
type SessionClosed struct{ *RequestException }

// HTTPError is returned by Response.raise_for_status for 4xx/5xx.
type HTTPError struct{ *RequestException }

// IncompleteRead indicates the body was truncated.
type IncompleteRead struct{ *HTTPError }

// UnrewindableBodyError indicates the body could not be replayed for a redirect
// or retry.
type UnrewindableBodyError struct{ *RequestException }

// NewRequestException builds the most specific error for a transport failure.
func NewRequestException(msg string, code int, rsp *Response) error {
	err := newError(msg, code, rsp)
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "unsupported protocol") || strings.Contains(lower, "unsupported scheme"):
		return &InvalidSchema{err}
	case strings.Contains(lower, "no such host") || strings.Contains(lower, "server misbehaving"):
		return &DNSError{&ConnectionError{err}}
	case strings.Contains(lower, "timeout") || errors.Is(err, ErrTimeout):
		return &Timeout{err}
	case strings.Contains(lower, "certificate") || strings.Contains(lower, "x509") || strings.Contains(lower, "tls"):
		return &CertificateVerifyError{&SSLError{&ConnectionError{err}}}
	case strings.Contains(lower, "proxy"):
		return &ProxyError{err}
	default:
		return &ConnectionError{err}
	}
}

// ErrTimeout is a sentinel matching any Timeout error.
var ErrTimeout = errors.New("requests: timeout")

// Is lets errors.Is(err, ErrTimeout) succeed for Timeout errors.
func (e *Timeout) Is(target error) bool { return target == ErrTimeout }

// IsTimeout reports whether err is a Timeout.
func IsTimeout(err error) bool {
	var t *Timeout
	return errors.As(err, &t) || errors.Is(err, ErrTimeout)
}

// RaiseForStatus returns an *HTTPError if the response status is not in
// [200, 400), matching requests/curl_cffi.
func (r *Response) RaiseForStatus() error {
	if r.Ok {
		return nil
	}
	msg := fmt.Sprintf("HTTP Error %d: %s", r.StatusCode, r.Reason)
	base := newError(msg, 0, r)
	return &HTTPError{base}
}
