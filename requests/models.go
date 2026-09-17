package requests

import (
	"encoding/json"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Request describes an HTTP request as a value. It is what Response.Request
// reports for a request that was sent, and what Session.Send accepts to send
// one, so a request can be built, stored and passed around rather than being a
// list of options at the call site.
//
// Only Method and URL are required; every other field falls back to the
// session's setting when it is left at its zero value.
type Request struct {
	// Method is the HTTP method. Empty means GET.
	Method string
	// URL is the target. A URL without a scheme defaults to https.
	URL string

	// Headers accepts anything the WithHeaders option accepts, including
	// []string{"Name: Value"} and map[string]string.
	Headers HeaderTypes
	// Params accepts anything the WithParams option accepts.
	Params any
	// Cookies accepts anything the WithCookies option accepts.
	Cookies CookieTypes

	// Body is the raw request body. JSON wins when both are set.
	Body []byte
	// JSON is marshalled as the request body and sets application/json.
	JSON any

	// Browser loads the URL in the full browser instead of the fast client:
	// scripts run, the page settles, and the response body is the document as
	// it ended up. It needs the browser package linked in, which importing the
	// module root does; a program that imports requests alone is told so.
	//
	// Only a GET of a URL has a browser path, and it is slower by orders of
	// magnitude, so leave it off unless the page needs scripts.
	Browser bool

	// Impersonate selects the fingerprint target, for example
	// impersonate.Chrome131. Empty uses the session default.
	Impersonate string

	// Timeout overrides the session timeout when it is non-zero.
	Timeout time.Duration
	// Proxy overrides the session proxy when it is non-empty.
	Proxy string
}

var charsetRE = regexp.MustCompile(`charset=([\w-]+)`)

// Response holds everything the server sent back, mirroring
// curl_cffi.requests.Response.
type Response struct {
	URL             string
	Content         []byte
	StatusCode      int
	Reason          string
	Ok              bool
	Headers         *Headers
	Cookies         *Cookies
	Elapsed         time.Duration
	DefaultEncoding string
	RedirectCount   int
	RedirectURL     string
	HTTPVersion     int
	PrimaryIP       string
	PrimaryPort     int
	LocalIP         string
	LocalPort       int
	History         []*Response
	Request         *Request
	DownloadSize    int64
	UploadSize      int64
	HeaderSize      int64
	RequestSize     int64

	// Body is set when the request used Stream=true. It must be closed by the
	// caller (or via Close).
	Body io.ReadCloser

	text   *string
	enc    string
	encSet bool
}

// NewResponse returns an empty response.
func NewResponse() *Response {
	return &Response{
		StatusCode:      200,
		Reason:          "OK",
		Ok:              true,
		Headers:         &Headers{},
		Cookies:         NewCookies(nil),
		DefaultEncoding: "utf-8",
	}
}

// CharsetEncoding returns the charset from the Content-Type header, if any.
func (r *Response) CharsetEncoding() string {
	ct := r.Headers.Get("Content-Type")
	if ct == "" {
		return ""
	}
	m := charsetRE.FindStringSubmatch(ct)
	if m == nil {
		return ""
	}
	return m[1]
}

// Encoding returns the charset used to decode Text.
func (r *Response) Encoding() string {
	if r.encSet {
		return r.enc
	}
	if cs := r.CharsetEncoding(); cs != "" {
		r.enc = cs
		r.encSet = true
		return r.enc
	}
	if r.DefaultEncoding != "" {
		r.enc = r.DefaultEncoding
	} else {
		r.enc = "utf-8"
	}
	r.encSet = true
	return r.enc
}

// SetEncoding overrides the decode charset. It must be called before Text.
func (r *Response) SetEncoding(enc string) error {
	if r.text != nil {
		return &RequestException{Msg: "cannot set encoding after text has been accessed"}
	}
	r.enc = enc
	r.encSet = true
	return nil
}

// Text decodes Content using Encoding, replacing invalid bytes.
func (r *Response) Text() string {
	if r.text == nil {
		var s string
		if len(r.Content) == 0 {
			s = ""
		} else {
			s = decode(r.Content, r.Encoding())
		}
		r.text = &s
	}
	return *r.text
}

func decode(content []byte, enc string) string {
	// The common cases are handled without an external dependency.
	switch strings.ToLower(strings.ReplaceAll(enc, "_", "-")) {
	case "utf-8", "utf8", "ascii", "us-ascii", "":
		if utf8.Valid(content) {
			return string(content)
		}
		return strings.ToValidUTF8(string(content), "\uFFFD")
	case "iso-8859-1", "latin-1", "latin1", "cp1252", "windows-1252":
		return latin1ToUTF8(content)
	}
	// Fall back to UTF-8, mirroring curl_cffi's lenient behaviour.
	if utf8.Valid(content) {
		return string(content)
	}
	return strings.ToValidUTF8(string(content), "\uFFFD")
}

func latin1ToUTF8(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b) * 2)
	for _, c := range b {
		if c < 0x80 {
			sb.WriteByte(c)
		} else {
			sb.WriteRune(rune(c))
		}
	}
	return sb.String()
}

// JSON unmarshals the body into v using encoding/json.
func (r *Response) JSON(v any) error {
	return json.Unmarshal(r.Content, v)
}

// IsRedirect reports whether this is a well-formed redirect response.
func (r *Response) IsRedirect() bool {
	return r.Headers.Has("location") && isRedirectStatus(r.StatusCode)
}

func isRedirectStatus(code int) bool {
	switch code {
	case 301, 302, 303, 307, 308:
		return true
	}
	return false
}

// Close closes a streaming body, if any.
func (r *Response) Close() error {
	if r.Body != nil {
		err := r.Body.Close()
		r.Body = nil
		return err
	}
	return nil
}

// ReadAll consumes and returns the rest of a streaming body.
func (r *Response) ReadAll() ([]byte, error) {
	if r.Body == nil {
		return r.Content, nil
	}
	defer r.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return b, err
	}
	r.Content = b
	return b, nil
}

// IterContent yields the response body in chunks. In non-stream mode it yields
// the buffered content once. In stream mode it reads from the network.
func (r *Response) IterContent() (func() ([]byte, error, bool), error) {
	if r.Body != nil {
		return func() ([]byte, error, bool) {
			buf := make([]byte, 32*1024)
			n, err := r.Body.Read(buf)
			if n > 0 {
				return buf[:n], nil, false
			}
			if err == io.EOF {
				r.Body.Close()
				r.Body = nil
				return nil, nil, true
			}
			if err != nil {
				return nil, err, true
			}
			return nil, nil, false
		}, nil
	}
	done := false
	return func() ([]byte, error, bool) {
		if done {
			return nil, nil, true
		}
		done = true
		return r.Content, nil, true
	}, nil
}

// IterLines iterates over the response body line by line.
func (r *Response) IterLines(delimiter string) ([]string, error) {
	data, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	var lines []string
	s := string(data)
	if delimiter != "" {
		lines = strings.Split(s, delimiter)
	} else {
		lines = strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	}
	return lines, nil
}

// ContentType returns the Content-Type header value.
func (r *Response) ContentType() string { return r.Headers.Get("Content-Type") }

// HasHeader reports whether a response header exists.
func (r *Response) HasHeader(name string) bool { return r.Headers.Has(name) }

// Len returns the response body length in bytes.
func (r *Response) Len() int {
	if r.Body != nil {
		if b, err := r.ReadAll(); err == nil {
			return len(b)
		}
	}
	return len(r.Content)
}

// String implements fmt.Stringer.
func (r *Response) String() string {
	return "<Response [" + itoa(r.StatusCode) + "]>"
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
