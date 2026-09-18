package browser

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/HashShin/shade/requests"
)

// CORS enforcement for fetch and XHR, enabled by Options.CORS. The browser is a
// crawling browser, so documents and subresources are not CORS-checked; only
// script-issued requests are, which is where the web platform applies it.

// simpleHeaders are the request headers a simple request may carry.
var simpleHeaders = map[string]bool{
	"accept": true, "accept-language": true,
	"content-language": true, "content-type": true,
}

// sameOrigin reports whether two absolute URLs share a scheme, host and port.
func sameOrigin(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return ua.Scheme == ub.Scheme && ua.Host == ub.Host
}

// corsRequest applies CORS to a script-issued request: it adds Origin, runs a
// preflight when the request is not simple, and validates the response. It is a
// no-op unless Options.CORS is set and the request is cross-origin.
func (e *jsEnv) corsRequest(method, rawURL string, headers map[string]string, pageOrigin string) (map[string]string, error) {
	if !e.page.browser.opts.CORS || pageOrigin == "" || sameOrigin(pageOrigin, rawURL) {
		return headers, nil
	}
	out := map[string]string{}
	for k, v := range headers {
		out[k] = v
	}
	out["Origin"] = pageOrigin

	if err := e.corsPreflight(method, rawURL, out, pageOrigin); err != nil {
		return nil, err
	}
	return out, nil
}

// corsPreflight sends an OPTIONS request when the request is not simple and
// verifies the preflight response, as a browser does.
func (e *jsEnv) corsPreflight(method, rawURL string, headers map[string]string, origin string) error {
	if isSimpleRequest(method, headers) {
		return nil
	}
	ph := map[string]string{
		"Origin":                        origin,
		"Access-Control-Request-Method": method,
	}
	var names []string
	for k := range headers {
		lk := strings.ToLower(k)
		if lk == "origin" || simpleHeaders[lk] {
			continue
		}
		names = append(names, k)
	}
	if len(names) > 0 {
		sort.Strings(names)
		ph["Access-Control-Request-Headers"] = strings.Join(names, ", ")
	}
	resp, err := e.page.browser.request("OPTIONS", rawURL, ph, nil, "fetch")
	if err != nil {
		return err
	}
	if !corsAllowOrigin(resp, origin) {
		return fmt.Errorf("CORS preflight for %s failed: no Access-Control-Allow-Origin", rawURL)
	}
	if allowed := resp.Headers.Get("Access-Control-Allow-Methods"); allowed != "" &&
		!strings.Contains(allowed, "*") && !headerListContains(allowed, method) {
		return fmt.Errorf("CORS preflight for %s failed: method %s not allowed", rawURL, method)
	}
	if len(names) > 0 {
		if allowed := resp.Headers.Get("Access-Control-Allow-Headers"); allowed != "" {
			for _, n := range names {
				if !headerListContains(allowed, n) && !strings.Contains(allowed, "*") {
					return fmt.Errorf("CORS preflight for %s failed: header %s not allowed", rawURL, n)
				}
			}
		}
	}
	return nil
}

// corsCheckResponse rejects a cross-origin response without a matching
// Access-Control-Allow-Origin.
func (e *jsEnv) corsCheckResponse(resp *requests.Response, pageOrigin, rawURL string) error {
	if !e.page.browser.opts.CORS || pageOrigin == "" || sameOrigin(pageOrigin, rawURL) {
		return nil
	}
	if !corsAllowOrigin(resp, pageOrigin) {
		return fmt.Errorf("fetch of %s blocked by CORS policy: no Access-Control-Allow-Origin", rawURL)
	}
	return nil
}

// corsAllowOrigin reports whether the response permits the requesting origin.
func corsAllowOrigin(resp *requests.Response, origin string) bool {
	if resp == nil || resp.Headers == nil {
		return false
	}
	acao := strings.TrimSpace(resp.Headers.Get("Access-Control-Allow-Origin"))
	return acao == "*" || acao == origin
}

// isSimpleRequest reports whether a request may skip the preflight.
func isSimpleRequest(method string, headers map[string]string) bool {
	switch method {
	case "GET", "HEAD", "POST":
	default:
		return false
	}
	for k := range headers {
		lk := strings.ToLower(k)
		if lk == "origin" || simpleHeaders[lk] {
			continue
		}
		return false
	}
	return true
}

// headerListContains reports whether a comma-separated header value contains a
// token, case-insensitively.
func headerListContains(list, token string) bool {
	for _, part := range strings.Split(list, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}
