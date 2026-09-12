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
type Headers struct {
	items []headerItem
}

type headerItem struct {
	name  string
	value string
}

// HeaderTypes is the set of accepted header inputs. A plain map loses ordering,
// so prefer *Headers or []HeaderPair when order matters (it usually does for
// impersonation).
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
		for _, it := range v.items {
			h.Set(it.name, it.value)
		}
	case Headers:
		for _, it := range v.items {
			h.Set(it.name, it.value)
		}
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

func (h *Headers) index(name string) int {
	lower := strings.ToLower(name)
	for i, it := range h.items {
		if strings.ToLower(it.name) == lower {
			return i
		}
	}
	return -1
}

// Set replaces any existing value for name (retaining its position) or appends.
func (h *Headers) Set(name, value string) {
	if i := h.index(name); i >= 0 {
		h.items[i] = headerItem{name, value}
		return
	}
	h.items = append(h.items, headerItem{name, value})
}

// Add appends a value, keeping any existing values for the same key.
func (h *Headers) Add(name, value string) {
	h.items = append(h.items, headerItem{name, value})
}

// Del removes all values for name.
func (h *Headers) Del(name string) {
	lower := strings.ToLower(name)
	dst := h.items[:0]
	for _, it := range h.items {
		if strings.ToLower(it.name) != lower {
			dst = append(dst, it)
		}
	}
	h.items = dst
}

// Get returns the values for name concatenated with ", " (RFC 7230 style).
func (h *Headers) Get(name string) string {
	vals := h.GetList(name)
	return strings.Join(vals, ", ")
}

// GetList returns all values for name, in order.
func (h *Headers) GetList(name string) []string {
	lower := strings.ToLower(name)
	var out []string
	for _, it := range h.items {
		if strings.ToLower(it.name) == lower {
			out = append(out, it.value)
		}
	}
	return out
}

// Has reports whether name is present.
func (h *Headers) Has(name string) bool {
	return h.index(name) >= 0
}

// Len returns the number of header lines.
func (h *Headers) Len() int { return len(h.items) }

// MultiItems returns every header line in order, without concatenation.
func (h *Headers) MultiItems() []HeaderPair {
	out := make([]HeaderPair, 0, len(h.items))
	for _, it := range h.items {
		out = append(out, HeaderPair{it.name, it.value})
	}
	return out
}

// Items returns unique keys mapped to their concatenated values.
func (h *Headers) Items() map[string]string {
	out := map[string]string{}
	for _, it := range h.items {
		if existing, ok := out[it.name]; ok {
			out[it.name] = existing + ", " + it.value
		} else {
			out[it.name] = it.value
		}
	}
	return out
}

// Keys returns the unique header names in first-seen order.
func (h *Headers) Keys() []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range h.items {
		if !seen[it.name] {
			seen[it.name] = true
			out = append(out, it.name)
		}
	}
	return out
}

// Clone returns a deep copy.
func (h *Headers) Clone() *Headers {
	out := &Headers{items: make([]headerItem, len(h.items))}
	copy(out.items, h.items)
	return out
}

func (h *Headers) String() string {
	parts := make([]string, 0, len(h.items))
	for _, it := range h.items {
		parts = append(parts, it.name+": "+it.value)
	}
	return "Headers{" + strings.Join(parts, ", ") + "}"
}

// ToHTTP converts to the map form used by the fhttp transport, preserving order
// through the transport's magic order key. Header names are lower-cased because
// that is what HTTP/2 (and the fhttp transport) requires.
func (h *Headers) toTransport() (map[string][]string, []string) {
	m := make(map[string][]string, len(h.items)+2)
	order := make([]string, 0, len(h.items))
	for _, it := range h.items {
		key := strings.ToLower(it.name)
		if _, ok := m[key]; !ok {
			order = append(order, key)
		}
		m[key] = append(m[key], it.value)
	}
	return m, order
}
