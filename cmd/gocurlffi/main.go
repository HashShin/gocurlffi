// Command gocurlffi is a curl_cffi-style CLI: fetch URLs with browser TLS and
// HTTP fingerprint impersonation.
//
// Usage:
//
//	gocurlffi get https://tls.browserleaks.com/json --impersonate chrome
//	gocurlffi post https://httpbin.org/post --json '{"a":1}'
//	gocurlffi list
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"gocurlffi/impersonate"
	"gocurlffi/requests"
)

const usage = `gocurlffi - HTTP client with browser impersonation (Go port of curl_cffi)

Usage:
  gocurlffi <method> <url> [flags]
  gocurlffi list            list impersonation targets

Methods:
  get, post, put, patch, delete, head, options, trace

Flags:
  -i, --impersonate NAME   browser to impersonate, or "native"/"curl" (default: native)
  -H, --header "K: V"      add a request header (repeatable)
  -P, --param "k=v"        add a query parameter (repeatable)
  -d, --data BODY          request body (string, @file, or key=value pairs)
  -j, --json JSON          JSON request body
  -f, --form               send --data as form fields
      --cookie "k=v"       add a cookie (repeatable)
      --auth user:pass     HTTP basic auth
      --proxy URL          proxy URL
  -t, --timeout SECONDS    request timeout (default 30)
      --follow             follow redirects (default true)
      --max-redirects N    maximum redirects (default 30)
      --verify             verify TLS certificates (default true)
      --http-version V     v1, v2 or v3
  -o, --output FILE        write the response body to FILE
      --headers            print response headers only
      --body               print the response body only (default)
  -v, --verbose            print response status, headers and body
      --version            print version
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Print(usage)
		os.Exit(1)
	}

	signal.Ignore(os.Interrupt, syscall.SIGPIPE)

	switch args[0] {
	case "list", "targets":
		for _, t := range impersonate.Targets() {
			fmt.Println(t)
		}
		return
	case "version", "--version", "-V":
		fmt.Println("gocurlffi", version)
		return
	case "help", "--help", "-h":
		fmt.Print(usage)
		return
	}

	method := strings.ToUpper(args[0])
	rest := args[1:]
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "error: missing URL")
		os.Exit(2)
	}
	rawURL := rest[0]
	opts, m, outPath, err := parseFlags(rest[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}

	sess := requests.NewSession(opts...)
	defer sess.Close()

	rsp, err := sess.Request(method, rawURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	if err := output(rsp, os.Stdout, m, outPath); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

const version = "0.1.0"

type mode int

const (
	modeBody mode = iota
	modeHeaders
	modeVerbose
)

func parseFlags(args []string) ([]requests.Option, mode, string, error) {
	var opts []requests.Option
	headers := requests.NewHeaders(nil)
	var params requests.Params
	cookies := requests.NewCookies(nil)

	var bodyArg string
	formMode := false
	var jsonArg string
	var outputFile string
	m := modeBody

	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("flag %s requires a value", a)
			}
			i++
			return args[i], nil
		}
		switch a {
		case "-i", "--impersonate":
			v, err := next()
			if err != nil {
				return nil, modeBody, "", err
			}
			opts = append(opts, requests.WithImpersonate(v))
		case "-H", "--header":
			v, err := next()
			if err != nil {
				return nil, modeBody, "", err
			}
			name, val, ok := splitHeader(v)
			if !ok {
				return nil, modeBody, "", fmt.Errorf("invalid header %q, want 'Name: Value'", v)
			}
			headers.Set(name, val)
		case "-P", "--param":
			v, err := next()
			if err != nil {
				return nil, modeBody, "", err
			}
			k, val, _ := strings.Cut(v, "=")
			params = append(params, requests.Param{Key: k, Value: val})
		case "-d", "--data":
			v, err := next()
			if err != nil {
				return nil, modeBody, "", err
			}
			bodyArg = v
		case "-j", "--json":
			v, err := next()
			if err != nil {
				return nil, modeBody, "", err
			}
			jsonArg = v
			_ = jsonArg
		case "-f", "--form":
			formMode = true
		case "--cookie":
			v, err := next()
			if err != nil {
				return nil, modeBody, "", err
			}
			k, val, _ := strings.Cut(v, "=")
			cookies.Set(k, val)
		case "--auth":
			v, err := next()
			if err != nil {
				return nil, modeBody, "", err
			}
			user, pass, _ := strings.Cut(v, ":")
			opts = append(opts, requests.WithAuth(user, pass))
		case "--proxy":
			v, err := next()
			if err != nil {
				return nil, modeBody, "", err
			}
			opts = append(opts, requests.WithProxy(v))
		case "-t", "--timeout":
			v, err := next()
			if err != nil {
				return nil, modeBody, "", err
			}
			var secs float64
			if _, err := fmt.Sscanf(v, "%f", &secs); err != nil {
				return nil, modeBody, "", fmt.Errorf("invalid timeout %q", v)
			}
			opts = append(opts, requests.WithTimeoutSeconds(secs))
		case "--follow":
			opts = append(opts, requests.WithAllowRedirects(true))
		case "--no-follow":
			opts = append(opts, requests.WithAllowRedirects(false))
		case "--max-redirects":
			v, err := next()
			if err != nil {
				return nil, modeBody, "", err
			}
			var n int
			if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
				return nil, modeBody, "", fmt.Errorf("invalid max-redirects %q", v)
			}
			opts = append(opts, requests.WithMaxRedirects(n))
		case "--verify":
			opts = append(opts, requests.WithVerify(true))
		case "--no-verify":
			opts = append(opts, requests.WithVerify(false))
		case "--http-version":
			v, err := next()
			if err != nil {
				return nil, modeBody, "", err
			}
			opts = append(opts, requests.WithHTTPVersion(v))
		case "-o", "--output":
			v, err := next()
			if err != nil {
				return nil, modeBody, "", err
			}
			outputFile = v
		case "--headers":
			m = modeHeaders
		case "--body":
			m = modeBody
		case "-v", "--verbose":
			m = modeVerbose
		case "--stream":
			opts = append(opts, requests.WithStream(true))
		default:
			if strings.HasPrefix(a, "-") {
				return nil, modeBody, "", fmt.Errorf("unknown flag %q", a)
			}
		}
	}

	if headers.Len() > 0 {
		opts = append(opts, requests.WithHeaders(headers))
	}
	if len(params) > 0 {
		opts = append(opts, requests.WithParams(params))
	}
	if cookies.Len() > 0 {
		opts = append(opts, requests.WithCookies(cookies))
	}
	if jsonArg != "" {
		var v any
		if err := json.Unmarshal([]byte(jsonArg), &v); err != nil {
			return nil, modeBody, "", fmt.Errorf("invalid json: %w", err)
		}
		opts = append(opts, requests.WithJSON(v))
	} else if bodyArg != "" {
		if strings.HasPrefix(bodyArg, "@") {
			data, err := os.ReadFile(bodyArg[1:])
			if err != nil {
				return nil, modeBody, "", err
			}
			opts = append(opts, requests.WithData(data))
		} else if formMode || strings.Contains(bodyArg, "=") {
			vals := requests.Params{}
			for _, pair := range strings.Split(bodyArg, "&") {
				k, v, _ := strings.Cut(pair, "=")
				vals = append(vals, requests.Param{Key: k, Value: v})
			}
			opts = append(opts, requests.WithData(vals))
		} else {
			opts = append(opts, requests.WithData(bodyArg))
		}
	}
	return opts, m, outputFile, nil
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

func output(rsp *requests.Response, w io.Writer, m mode, outPath string) error {
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

	switch m {
	case modeHeaders, modeVerbose:
		fmt.Fprintf(w, "HTTP/%d %d %s\n", max(rsp.HTTPVersion, 1), rsp.StatusCode, rsp.Reason)
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
	_, err = w.Write(data)
	if err == nil && m == modeVerbose && len(data) > 0 && data[len(data)-1] != '\n' {
		fmt.Fprintln(w)
	}
	return err
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
