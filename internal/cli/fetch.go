package cli

// The fast path: fetch URLs with browser TLS and HTTP fingerprint
// impersonation, without a JavaScript engine. This is the curl_cffi-shaped
// command, and it is what `gocurlffi get` runs unless --render is given.

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/HashShin/gocurlffi/requests"
)

// output modes. body is the default; the other two are selected by --headers
// and -v, which are mutually exclusive with the last one winning.
type mode int

const (
	modeBody mode = iota
	modeHeaders
	modeVerbose
)

// fetchFlags is the parsed fast-path command line. The URL stays a positional
// argument; everything else is collected here.
type fetchFlags struct {
	impersonate  string
	headers      stringList
	params       stringList
	cookies      stringList
	data         string
	jsonBody     string
	form         bool
	auth         string
	proxy        string
	timeout      float64
	follow       bool
	maxRedirects int
	verify       bool
	httpVersion  string
	method       string
	output       string
	stream       bool
	headersOnly  bool
	verbose      bool
}

// RunFetch is the fast path: one request through the impersonating transport,
// with no JavaScript engine involved. method is the upper-case HTTP verb.
func RunFetch(method string, args []string) int {
	fs := newFlagSet("get", "gocurlffi "+strings.ToLower(method)+" <url> [flags]",
		"gocurlffi get - HTTP client with browser impersonation (Go port of curl_cffi)")
	f := &fetchFlags{}
	f.register(fs)

	if err := parse(fs, args); err != nil {
		return parseStatus(err)
	}
	rawURL := fs.Arg(0)
	if rawURL == "" {
		fmt.Fprintln(os.Stderr, "error: missing URL")
		fs.Usage()
		return exitUsage
	}
	if f.method != "" {
		method = strings.ToUpper(f.method)
	}

	signal.Ignore(os.Interrupt, syscall.SIGPIPE)

	opts, err := f.options()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitUsage
	}

	sess := requests.NewSession(opts...)
	defer sess.Close()

	rsp, err := sess.Request(method, rawURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitError
	}

	m := modeBody
	switch {
	case f.verbose:
		m = modeVerbose
	case f.headersOnly:
		m = modeHeaders
	}
	if err := fetchOutput(rsp, os.Stdout, m, f.output); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitError
	}
	return exitOK
}

// register declares the fast-path flags. -f is deliberately not a short form of
// --form: it means --format on the browser path, and one letter with two
// meanings across one binary is worse than a longer spelling here.
func (f *fetchFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.impersonate, "i", "", "browser to impersonate, or native/curl/custom (default: native)")
	fs.StringVar(&f.impersonate, "impersonate", "", "alias for -i")
	fs.Var(&f.headers, "H", "add a request header as \"Name: Value\" (repeatable)")
	fs.Var(&f.headers, "header", "alias for -H")
	fs.Var(&f.params, "P", "add a query parameter as \"key=value\" (repeatable)")
	fs.Var(&f.params, "param", "alias for -P")
	fs.Var(&f.cookies, "cookie", "add a cookie as \"key=value\" (repeatable)")
	fs.StringVar(&f.data, "d", "", "request body: a string, @file, or key=value pairs")
	fs.StringVar(&f.data, "data", "", "alias for -d")
	fs.StringVar(&f.jsonBody, "j", "", "JSON request body")
	fs.StringVar(&f.jsonBody, "json", "", "alias for -j")
	fs.BoolVar(&f.form, "form", false, "send --data as form fields even without key=value pairs")
	fs.StringVar(&f.auth, "auth", "", "HTTP basic auth as user:pass")
	fs.StringVar(&f.proxy, "proxy", "", "proxy URL")
	fs.Float64Var(&f.timeout, "t", 30, "request timeout in seconds")
	fs.Float64Var(&f.timeout, "timeout", 30, "alias for -t")
	fs.BoolVar(&f.follow, "follow", true, "follow redirects (use -follow=false to stop)")
	fs.IntVar(&f.maxRedirects, "max-redirects", 30, "maximum number of redirects")
	fs.BoolVar(&f.verify, "verify", true, "verify TLS certificates (use -verify=false to skip)")
	fs.StringVar(&f.httpVersion, "http-version", "", "force an HTTP version: v1, v2 or v3")
	fs.StringVar(&f.method, "X", "", "HTTP method, overriding the command (get, post, ...)")
	fs.StringVar(&f.method, "method", "", "alias for -X")
	fs.BoolVar(&f.stream, "stream", false, "stream the body instead of buffering it")
	fs.StringVar(&f.output, "o", "", "write the response body to this file")
	fs.StringVar(&f.output, "output", "", "alias for -o")
	fs.BoolVar(&f.headersOnly, "headers", false, "print the response headers only")
	fs.BoolVar(&f.verbose, "v", false, "print the status, headers and body")
	fs.BoolVar(&f.verbose, "verbose", false, "alias for -v")
}

// options turns the parsed flags into requests options, reporting a bad
// combination before any network call is made.
func (f *fetchFlags) options() ([]requests.Option, error) {
	var opts []requests.Option

	if f.impersonate != "" {
		opts = append(opts, requests.WithImpersonate(f.impersonate))
	}
	if len(f.headers) > 0 {
		headers := requests.NewHeaders(nil)
		for _, h := range f.headers {
			name, value, ok := splitHeader(h)
			if !ok {
				return nil, fmt.Errorf("invalid header %q, want 'Name: Value'", h)
			}
			headers.Set(name, value)
		}
		opts = append(opts, requests.WithHeaders(headers))
	}
	if len(f.params) > 0 {
		var params requests.Params
		for _, p := range f.params {
			k, v, _ := strings.Cut(p, "=")
			params = append(params, requests.Param{Key: k, Value: v})
		}
		opts = append(opts, requests.WithParams(params))
	}
	if len(f.cookies) > 0 {
		cookies := requests.NewCookies(nil)
		for _, c := range f.cookies {
			k, v, _ := strings.Cut(c, "=")
			cookies.Set(k, v)
		}
		opts = append(opts, requests.WithCookies(cookies))
	}
	if f.auth != "" {
		user, pass, _ := strings.Cut(f.auth, ":")
		opts = append(opts, requests.WithAuth(user, pass))
	}
	if f.proxy != "" {
		opts = append(opts, requests.WithProxy(f.proxy))
	}
	if f.timeout > 0 {
		opts = append(opts, requests.WithTimeoutSeconds(f.timeout))
	}
	opts = append(opts,
		requests.WithAllowRedirects(f.follow),
		requests.WithMaxRedirects(f.maxRedirects),
		requests.WithVerify(f.verify),
	)
	if f.httpVersion != "" {
		opts = append(opts, requests.WithHTTPVersion(f.httpVersion))
	}
	if f.stream {
		opts = append(opts, requests.WithStream(true))
	}

	body, err := f.body()
	if err != nil {
		return nil, err
	}
	if body != nil {
		opts = append(opts, body)
	}
	return opts, nil
}

// body resolves -j/--json and -d/--data into one request body option. JSON
// wins when both are given, matching curl_cffi's `json=` argument.
func (f *fetchFlags) body() (requests.Option, error) {
	if f.jsonBody != "" {
		var v any
		if err := json.Unmarshal([]byte(f.jsonBody), &v); err != nil {
			return nil, fmt.Errorf("invalid json: %w", err)
		}
		return requests.WithJSON(v), nil
	}
	if f.data == "" {
		return nil, nil
	}
	if strings.HasPrefix(f.data, "@") {
		data, err := os.ReadFile(f.data[1:])
		if err != nil {
			return nil, err
		}
		return requests.WithData(data), nil
	}
	// "a=1&b=2" is form-encoded; --form forces the same for a single bare
	// value that contains no "=" yet is not meant to be a raw string body.
	if f.form || strings.Contains(f.data, "=") {
		var vals requests.Params
		for _, pair := range strings.Split(f.data, "&") {
			k, v, _ := strings.Cut(pair, "=")
			vals = append(vals, requests.Param{Key: k, Value: v})
		}
		return requests.WithData(vals), nil
	}
	return requests.WithData(f.data), nil
}

func splitHeader(s string) (string, string, bool) {
	k, v, ok := strings.Cut(s, ":")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(k), strings.TrimSpace(v), true
}

// jsGateMarkers match pages that are JavaScript-only interstitials rather than
// real content. Google, for example, returns "Turn on JavaScript to keep
// searching" for /search to every client that does not execute scripts, no
// matter the TLS/JA3/HTTP2 fingerprint (plain curl gets the same page).
var jsGateMarkers = []struct {
	marker string
	note   string
}{
	{"/httpservice/retry/enablejs", "Google requires JavaScript for /search: it serves a JS-only \"Turn on JavaScript to keep searching\" page to every non-browser client (curl gets the same page)."},
	{"Turn on JavaScript to keep searching", "Google requires JavaScript for /search: the no-JS HTML results were removed, so this is a JavaScript gate, not real results."},
}

// warnJSGate writes a diagnostic to stderr when body is a known JS-only
// interstitial, so callers are not misled into treating it as content.
func warnJSGate(w io.Writer, url string, body []byte) {
	for _, g := range jsGateMarkers {
		if bytes.Contains(body, []byte(g.marker)) {
			fmt.Fprintf(w, "\nwarning: %s\n  (final URL: %s, %d bytes of JavaScript-only interstitial)\n", g.note, url, len(body))
			return
		}
	}
}

func fetchOutput(rsp *requests.Response, w io.Writer, m mode, outPath string) error {
	if outPath != "" {
		data, err := rsp.ReadAll()
		if err != nil {
			return err
		}
		if err := os.WriteFile(outPath, data, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "saved %d bytes to %s\n", len(data), outPath)
		warnJSGate(os.Stderr, rsp.URL, data)
		return nil
	}

	if m == modeHeaders || m == modeVerbose {
		version := rsp.HTTPVersion
		if version < 1 {
			version = 1
		}
		fmt.Fprintf(w, "HTTP/%d %d %s\n", version, rsp.StatusCode, rsp.Reason)
		for _, h := range rsp.Headers.MultiItems() {
			fmt.Fprintf(w, "%s: %s\n", h.Name, h.Value)
		}
		if m == modeVerbose {
			fmt.Fprintln(w)
		}
	}
	if m == modeHeaders {
		return nil
	}
	data, err := rsp.ReadAll()
	if err != nil {
		return err
	}
	warnJSGate(os.Stderr, rsp.URL, data)
	if _, err := w.Write(data); err != nil {
		return err
	}
	if m == modeVerbose && len(data) > 0 && data[len(data)-1] != '\n' {
		fmt.Fprintln(w)
	}
	return nil
}
