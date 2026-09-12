package requests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"gocurlffi/impersonate"
)

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// buildBody resolves content/data/json into a byte body plus an optional
// streaming reader and the Content-Type that should be inserted.
func buildBody(content any, data any, jsonBody any) (body []byte, stream io.Reader, contentType string, err error) {
	if content != nil {
		switch v := content.(type) {
		case string:
			return []byte(v), nil, "", nil
		case []byte:
			return v, nil, "", nil
		case io.Reader:
			return nil, v, "", nil
		default:
			return nil, nil, "", &RequestException{Msg: fmt.Sprintf("unsupported content type %T", content)}
		}
	}
	if data != nil {
		switch v := data.(type) {
		case string:
			return []byte(v), nil, "", nil
		case []byte:
			return v, nil, "", nil
		case io.Reader:
			return nil, v, "", nil
		case url.Values:
			return []byte(v.Encode()), nil, "application/x-www-form-urlencoded", nil
		case Params:
			return []byte(v.Encode()), nil, "application/x-www-form-urlencoded", nil
		case map[string]string:
			return []byte(encodeForm(toParams(v))), nil, "application/x-www-form-urlencoded", nil
		case map[string][]string:
			return []byte(encodeForm(toParams(v))), nil, "application/x-www-form-urlencoded", nil
		default:
			return nil, nil, "", &RequestException{Msg: fmt.Sprintf("unsupported data type %T", data)}
		}
	}
	if jsonBody != nil {
		buf, err := marshalJSON(jsonBody)
		if err != nil {
			return nil, nil, "", err
		}
		return buf, nil, "application/json", nil
	}
	return nil, nil, "", nil
}

func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, &RequestException{Msg: "json encode: " + err.Error()}
	}
	b := buf.Bytes()
	// encoding/json appends a newline; curl_cffi does not.
	return bytes.TrimRight(b, "\n"), nil
}

// Encode renders Params as a query string.
func (p Params) Encode() string {
	var sb strings.Builder
	for i, kv := range p {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(url.QueryEscape(kv.Key))
		sb.WriteByte('=')
		sb.WriteString(url.QueryEscape(kv.Value))
	}
	return sb.String()
}

func encodeForm(p Params) string { return p.Encode() }

// applyParams appends params to a URL.
func applyParams(rawURL string, params Params) (string, error) {
	if len(params) == 0 {
		return rawURL, nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", &InvalidURL{newError(err.Error(), 3, nil)}
	}
	q := u.Query()
	for _, kv := range params {
		q.Add(kv.Key, kv.Value)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// resolveURL builds the final URL from base, relative url and params.
func resolveURL(baseURL, rawURL string, params Params) (string, error) {
	final := rawURL
	if baseURL != "" {
		bu, err := url.Parse(baseURL)
		if err != nil {
			return "", &InvalidURL{newError(err.Error(), 3, nil)}
		}
		ru, err := url.Parse(rawURL)
		if err != nil {
			return "", &InvalidURL{newError(err.Error(), 3, nil)}
		}
		final = bu.ResolveReference(ru).String()
	}
	if !strings.Contains(final, "://") {
		return "", &InvalidURL{newError("invalid or missing URL scheme: "+rawURL, 3, nil)}
	}
	return applyParams(final, params)
}

// buildFinalHeaders produces the ordered header set for a request, merging the
// session/request headers with the impersonated browser defaults. User headers
// always win, matching curl_cffi's default_headers behaviour.
func buildFinalHeaders(user *Headers, contentType, acceptEncoding string, preset *impersonate.Preset) *Headers {
	out := &Headers{}
	seen := map[string]bool{}
	for _, it := range user.MultiItems() {
		out.Set(it.Name, it.Value)
		seen[strings.ToLower(it.Name)] = true
	}
	if acceptEncoding != "" && !seen["accept-encoding"] {
		out.Set("Accept-Encoding", acceptEncoding)
		seen["accept-encoding"] = true
	}
	if contentType != "" && !seen["content-type"] {
		out.Set("Content-Type", contentType)
		seen["content-type"] = true
	}
	if preset != nil {
		for _, h := range preset.HTTPHeaders {
			lower := strings.ToLower(h.Name)
			if seen[lower] {
				continue
			}
			if lower == "host" && h.Value == "" {
				continue
			}
			seen[lower] = true
			out.Set(h.Name, h.Value)
		}
	}
	// Content-Length is set from the body, not the fingerprint.
	out.Del("Content-Length")
	return out
}

// parseSetCookies parses Set-Cookie values and records any new session cookie.
// parseSetCookies parses Set-Cookie values and records any new session cookie.
func parseSetCookies(rspHeaders *Headers, jar *Cookies, reqURL *url.URL) {
	for _, line := range rspHeaders.GetList("set-cookie") {
		ck, ok := parseSetCookie(line, reqURL)
		if !ok {
			continue
		}
		if ck.Value == "" {
			jar.Del(ck.Name)
			continue
		}
		jar.SetCookie(ck)
	}
}

// parseSetCookie parses one Set-Cookie header line.
func parseSetCookie(line string, reqURL *url.URL) (Cookie, bool) {
	hc, err := http.ParseSetCookie(line)
	if err != nil || hc == nil {
		return Cookie{}, false
	}
	ck := Cookie{
		Name:     hc.Name,
		Value:    hc.Value,
		Domain:   hc.Domain,
		Path:     hc.Path,
		Secure:   hc.Secure,
		HTTPOnly: hc.HttpOnly,
	}
	if hc.Domain != "" && reqURL != nil {
		ck.Domain = hc.Domain
	}
	if ck.Path == "" {
		ck.Path = "/"
	}
	if !hc.Expires.IsZero() {
		ck.Expires = hc.Expires
	}
	return ck, true
}

func parseHeadersFromTransport(h map[string][]string) *Headers {
	out := &Headers{}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "Header-Order:" || k == "PHeader-Order:" || strings.HasPrefix(k, "Header-Order") || strings.HasPrefix(k, "PHeader-Order") {
			continue
		}
		for _, v := range h[k] {
			out.Add(k, v)
		}
	}
	return out
}

func itoa64(i int64) string { return strconv.FormatInt(i, 10) }
