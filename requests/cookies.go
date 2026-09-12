package requests

import (
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Cookie is a single HTTP cookie with the attributes curl_cffi tracks.
type Cookie struct {
	Name     string
	Value    string
	Domain   string
	Path     string
	Secure   bool
	HTTPOnly bool
	Expires  time.Time
}

// Cookies is an ordered cookie collection. It mirrors curl_cffi's Cookies
// mapping (name -> attributes) closely enough for session handling.
type Cookies struct {
	mu    sync.RWMutex
	items []Cookie
}

// CookieTypes is the set of accepted cookie inputs.
type CookieTypes interface{}

// NewCookies builds a Cookies value from a map or an existing collection.
func NewCookies(c CookieTypes) *Cookies {
	out := &Cookies{}
	out.Update(c)
	return out
}

// Update merges cookies into the collection, replacing by name.
func (c *Cookies) Update(other CookieTypes) {
	if other == nil {
		return
	}
	switch v := other.(type) {
	case *Cookies:
		if v == nil {
			return
		}
		// Snapshot under the source's lock, then apply one by one so the two
		// jars are never locked at the same time.
		for _, ck := range v.Items() {
			c.SetCookie(ck)
		}
	case Cookies:
		for _, ck := range v.items {
			c.SetCookie(ck)
		}
	case map[string]string:
		names := make([]string, 0, len(v))
		for k := range v {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			c.Set(k, v[k])
		}
	case []*Cookie:
		for _, ck := range v {
			if ck != nil {
				c.SetCookie(*ck)
			}
		}
	default:
		panic("requests: unsupported cookie type")
	}
}

// Set sets a cookie by name, dropping any domain/path attributes.
func (c *Cookies) Set(name, value string) {
	c.SetCookie(Cookie{Name: name, Value: value})
}

// SetCookie inserts or replaces a cookie, matching on name+domain+path.
func (c *Cookies) SetCookie(ck Cookie) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ck.Path == "" {
		ck.Path = "/"
	}
	for i, it := range c.items {
		if it.Name == ck.Name && it.Domain == ck.Domain && it.Path == ck.Path {
			c.items[i] = ck
			return
		}
	}
	c.items = append(c.items, ck)
}

// Get returns the raw value of a cookie by name.
func (c *Cookies) Get(name string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, it := range c.items {
		if it.Name == name {
			return it.Value, true
		}
	}
	return "", false
}

// GetCookie returns the full cookie by name.
func (c *Cookies) GetCookie(name string) (*Cookie, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for i := range c.items {
		if c.items[i].Name == name {
			ck := c.items[i]
			return &ck, true
		}
	}
	return nil, false
}

// Del removes a cookie by name.
func (c *Cookies) Del(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	dst := c.items[:0]
	for _, it := range c.items {
		if it.Name != name {
			dst = append(dst, it)
		}
	}
	c.items = dst
}

// Items returns a copy of all cookies in insertion order.
func (c *Cookies) Items() []Cookie {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Cookie(nil), c.items...)
}

// Len returns the cookie count.
func (c *Cookies) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// Map returns name -> value for every cookie.
func (c *Cookies) Map() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]string, len(c.items))
	for _, it := range c.items {
		out[it.Name] = it.Value
	}
	return out
}

// matching returns the cookies that should be sent to u.
func (c *Cookies) matching(u *url.URL) []Cookie {
	c.mu.RLock()
	defer c.mu.RUnlock()
	host := u.Hostname()
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	secure := u.Scheme == "https"
	now := time.Now()
	var out []Cookie
	for _, it := range c.items {
		if !it.Expires.IsZero() && it.Expires.Before(now) {
			continue
		}
		if it.Secure && !secure {
			continue
		}
		if it.Domain != "" && !domainMatch(host, it.Domain) {
			continue
		}
		if it.Path != "" && !pathMatch(path, it.Path) {
			continue
		}
		out = append(out, it)
	}
	return out
}

func domainMatch(host, domain string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	domain = strings.ToLower(strings.TrimPrefix(domain, "."))
	if host == domain {
		return true
	}
	return strings.HasSuffix(host, "."+domain)
}

func pathMatch(reqPath, cookiePath string) bool {
	if reqPath == cookiePath {
		return true
	}
	if strings.HasPrefix(reqPath, cookiePath) {
		if strings.HasSuffix(cookiePath, "/") {
			return true
		}
		return reqPath[len(cookiePath)] == '/'
	}
	return false
}

// String renders the cookies as a "a=1; b=2" header value.
func (c *Cookies) String() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	parts := make([]string, 0, len(c.items))
	for _, it := range c.items {
		parts = append(parts, it.Name+"="+it.Value)
	}
	return strings.Join(parts, "; ")
}
