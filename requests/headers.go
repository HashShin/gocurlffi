package requests

import (
	"fmt"
	"sort"
	"strings"
)

// Headers is an ordered, case-insensitive HTTP header collection. It mirrors
// the behaviour of curl_cffi/httpx Headers: insertion order is preserved,
// lookups are case-insensitive and repeated keys can be retrieved with
// GetList.
//
// It is a slice of "Name: Value" lines, so it can be written directly as a
// composite literal, which is how headers appear in HTTP and on a command line:
//
//	requests.Headers{
//		"Accept: application/json",
//		"X-Custom: value",
//	}
//
// A value's leading and trailing spaces are not significant and are dropped
// when the line is read back, which matches how an HTTP parser treats them.
type Headers []string

// HeaderTypes is the set of accepted header inputs: *Headers, Headers,
// []HeaderPair, map[string]string, or map[string][]string. Headers itself is a
// []string of "Name: Value" lines, so a plain []string is accepted too. A map
// loses ordering, so prefer Headers or []HeaderPair when order matters, as it
// usually does for impersonation. Anything else panics, so a caller finds out
// immediately rather than sending a request with headers silently missing.
type HeaderTypes interface{}

// HeaderPair is a single header line.
type HeaderPair struct {
	Name  string
	Value string
}

// NewHeaders builds ordered Headers from any supported input.
func NewHeaders(h HeaderTypes) *Headers {
	out := &Headers{}
	out.Update(h)
	return out
}

// Update merges another header set, replacing any existing keys in place.
func (h *Headers) Update(other HeaderTypes) {
	if other == nil {
		return
	}
	switch v := other.(type) {
	case *Headers:
		if v == nil {
			return
		}
		h.mergeLines(*v)
	case Headers:
		h.mergeLines(v)
	case []string:
		// Distinct from Headers in a type switch, but the same content, so a
		// bare []string works as well.
		h.mergeLines(v)
	case []HeaderPair:
		for _, it := range v {
			h.Set(it.Name, it.Value)
		}
	case map[string]string:
		// Deterministic order for maps.
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			h.Set(k, v[k])
		}
	case map[string][]string:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			for _, val := range v[k] {
				h.Set(k, val)
			}
		}
	default:
		panic(fmt.Sprintf("requests: unsupported header type %T", other))
	}
}

// mergeLines copies already-rendered "Name: Value" lines, replacing any key
// they collide with so the incoming order wins for those names.
func (h *Headers) mergeLines(lines []string) {
	for _, line := range lines {
		name, value := splitHeaderLine(line)
		h.Set(name, value)
	}
}

// splitHeaderLine separates a "Name: Value" line. A line with no colon is a
// name with an empty value, which is valid HTTP.
func splitHeaderLine(line string) (string, string) {
	name, value, ok := strings.Cut(line, ":")
	if !ok {
		return strings.TrimSpace(line), ""
	}
	return strings.TrimSpace(name), strings.TrimSpace(value)
}

// line renders one stored header.
func headerLine(name, value string) string { return name + ": " + value }

func (h *Headers) index(name string) int {
	for i, line := range *h {
		got, _ := splitHeaderLine(line)
		if strings.EqualFold(got, name) {
			return i
		}
	}
	return -1
}

// Set replaces any existing value for name, keeping its position, or appends.
// The stored line takes the spelling of name that is passed in.
func (h *Headers) Set(name, value string) {
	if i := h.index(name); i >= 0 {
		(*h)[i] = headerLine(name, value)
		return
	}
	*h = append(*h, headerLine(name, value))
}

// Add appends a value, keeping any existing values for the same key.
func (h *Headers) Add(name, value string) {
	*h = append(*h, headerLine(name, value))
}

// Del removes all values for name.
func (h *Headers) Del(name string) {
	dst := (*h)[:0]
	for _, line := range *h {
		got, _ := splitHeaderLine(line)
		if !strings.EqualFold(got, name) {
			dst = append(dst, line)
		}
	}
	*h = dst
}

// Get returns the values for name concatenated with ", " (RFC 7230 style).
func (h *Headers) Get(name string) string {
	return strings.Join(h.GetList(name), ", ")
}

// GetList returns all values for name, in order.
func (h *Headers) GetList(name string) []string {
	var out []string
	for _, line := range *h {
		got, value := splitHeaderLine(line)
		if strings.EqualFold(got, name) {
			out = append(out, value)
		}
	}
	return out
}

// Has reports whether name is present.
func (h *Headers) Has(name string) bool {
	return h.index(name) >= 0
}

// Len returns the number of header lines.
func (h *Headers) Len() int { return len(*h) }

// MultiItems returns every header line in order, without concatenation.
func (h *Headers) MultiItems() []HeaderPair {
	out := make([]HeaderPair, 0, len(*h))
	for _, line := range *h {
		name, value := splitHeaderLine(line)
		out = append(out, HeaderPair{name, value})
	}
	return out
}

// Items returns unique keys mapped to their concatenated values.
func (h *Headers) Items() map[string]string {
	out := map[string]string{}
	for _, line := range *h {
		name, value := splitHeaderLine(line)
		if existing, ok := out[name]; ok {
			out[name] = existing + ", " + value
		} else {
			out[name] = value
		}
	}
	return out
}

// Keys returns the unique header names in first-seen order.
func (h *Headers) Keys() []string {
	seen := map[string]bool{}
	var out []string
	for _, line := range *h {
		name, _ := splitHeaderLine(line)
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// Clone returns a deep copy.
func (h *Headers) Clone() *Headers {
	out := make(Headers, len(*h))
	copy(out, *h)
	return &out
}

func (h *Headers) String() string {
	return "Headers{" + strings.Join(*h, ", ") + "}"
}

// toTransport converts to the map form used by the fhttp transport, preserving
// order through the transport's magic order key. Header names are lower-cased
// because that is what HTTP/2 (and the fhttp transport) requires.
func (h *Headers) toTransport() (map[string][]string, []string) {
	m := make(map[string][]string, len(*h)+2)
	order := make([]string, 0, len(*h))
	for _, line := range *h {
		name, value := splitHeaderLine(line)
		key := strings.ToLower(name)
		if _, ok := m[key]; !ok {
			order = append(order, key)
		}
		m[key] = append(m[key], value)
	}
	return m, order
}
