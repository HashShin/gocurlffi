package requests

import (
	"strings"
	"testing"
)

func TestHeadersOrderAndCase(t *testing.T) {
	h := NewHeaders([]HeaderPair{{"Sec-Ch-Ua", "x"}, {"User-Agent", "y"}, {"sec-ch-ua", "z"}})
	items := h.MultiItems()
	if len(items) != 2 {
		t.Fatalf("expected 2 unique keys, got %d", len(items))
	}
	// Set replaces in place, preserving the original position (the case comes
	// from the most recent assignment, matching httpx/curl_cffi).
	if items[0].Name != "sec-ch-ua" || items[0].Value != "z" {
		t.Errorf("item0 = %+v", items[0])
	}
	if items[1].Name != "User-Agent" {
		t.Errorf("item1 = %+v", items[1])
	}
	if got := h.Get("SEC-CH-UA"); got != "z" {
		t.Errorf("case-insensitive get = %q", got)
	}
}

func TestHeadersMultiValues(t *testing.T) {
	h := NewHeaders(nil)
	h.Add("Cookie", "a=1")
	h.Add("Cookie", "b=2")
	if got := h.Get("cookie"); got != "a=1, b=2" {
		t.Errorf("Get = %q", got)
	}
	if got := h.GetList("Cookie"); len(got) != 2 {
		t.Errorf("GetList = %v", got)
	}
	h.Del("cOoKiE")
	if h.Has("cookie") {
		t.Error("cookie should be deleted")
	}
}

func TestHeadersTransportOrder(t *testing.T) {
	h := NewHeaders([]HeaderPair{{"B", "2"}, {"A", "1"}})
	m, order := h.toTransport()
	if strings.Join(order, ",") != "b,a" {
		t.Errorf("order = %v", order)
	}
	if m["a"][0] != "1" || m["b"][0] != "2" {
		t.Errorf("map = %v", m)
	}
}

func TestCookiesMatching(t *testing.T) {
	c := NewCookies(nil)
	c.SetCookie(Cookie{Name: "a", Value: "1", Domain: "example.com", Path: "/"})
	c.SetCookie(Cookie{Name: "b", Value: "2", Domain: "sub.example.com", Path: "/x"})
	c.SetCookie(Cookie{Name: "secure", Value: "3", Domain: "example.com", Path: "/", Secure: true})

	u := mustParseURL(t, "https://www.example.com/")
	got := map[string]string{}
	for _, ck := range c.matching(u) {
		got[ck.Name] = ck.Value
	}
	if got["a"] != "1" {
		t.Errorf("a not matched: %v", got)
	}
	if _, ok := got["b"]; ok {
		t.Errorf("b should not match subdomain path: %v", got)
	}
	if got["secure"] != "3" {
		t.Errorf("secure cookie should match https: %v", got)
	}

	plain := mustParseURL(t, "http://example.com/")
	for _, ck := range c.matching(plain) {
		if ck.Name == "secure" {
			t.Error("secure cookie should not be sent over http")
		}
	}
}
