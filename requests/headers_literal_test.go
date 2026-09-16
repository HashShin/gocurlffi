package requests

import (
	"slices"
	"strings"
	"testing"
)

// Headers is a slice of "Name: Value" lines so it can be written as a literal,
// which is the form headers take in HTTP and on a command line. These tests pin
// the behaviour that representation has to keep: order, case-insensitive
// lookup, replace-in-place, and values that themselves contain a colon.
func TestHeadersLiteralForm(t *testing.T) {
	h := Headers{
		"Accept: application/json",
		"X-Custom: value",
	}

	if got := h.Get("accept"); got != "application/json" {
		t.Errorf("case-insensitive Get = %q, want application/json", got)
	}
	if got := h.Get("X-Custom"); got != "value" {
		t.Errorf("Get = %q, want value", got)
	}
	if h.Len() != 2 {
		t.Fatalf("Len = %d, want 2", h.Len())
	}

	// Order is part of a fingerprint, so it has to survive.
	names := h.Keys()
	if len(names) != 2 || names[0] != "Accept" || names[1] != "X-Custom" {
		t.Errorf("Keys = %v, want [Accept X-Custom]", names)
	}
}

// Set on an existing key replaces it where it is rather than appending, which
// is what keeps a header in its fingerprint position. The stored line takes the
// spelling passed to Set, and the lookup that finds it is case-insensitive.
func TestHeadersLiteralReplaceInPlace(t *testing.T) {
	h := Headers{"A: 1", "B: 2", "C: 3"}
	h.Set("b", "changed")

	if got := h.Get("B"); got != "changed" {
		t.Errorf("Get(B) = %q, want changed", got)
	}
	if h.Len() != 3 {
		t.Fatalf("Len = %d; Set must replace, not append", h.Len())
	}
	if want := (Headers{"A: 1", "b: changed", "C: 3"}); !slices.Equal(h, want) {
		t.Errorf("lines = %v, want %v", h, want)
	}
}

// A value may contain a colon; only the first one separates name from value.
func TestHeadersValueMayContainColon(t *testing.T) {
	h := Headers{"Location: https://example.com:8443/x", "Time: 12:30"}
	if got := h.Get("Location"); got != "https://example.com:8443/x" {
		t.Errorf("Location = %q", got)
	}
	if got := h.Get("Time"); got != "12:30" {
		t.Errorf("Time = %q", got)
	}
}

// A line with no colon is a name with an empty value, which is valid HTTP.
func TestHeadersLineWithoutColon(t *testing.T) {
	h := Headers{"X-Empty", "Y: "}
	if got := h.Get("X-Empty"); got != "" {
		t.Errorf("X-Empty = %q, want empty", got)
	}
	if !h.Has("X-Empty") {
		t.Error("a name with an empty value is still present")
	}
	if !h.Has("Y") {
		t.Error("Y should be present")
	}
}

// A bare []string, a Headers value and a *Headers must all be accepted, since
// Headers is a []string underneath but a distinct type in a type switch.
func TestHeadersAcceptsEveryForm(t *testing.T) {
	want := "application/json"
	for name, in := range map[string]HeaderTypes{
		"Headers value": Headers{"Accept: " + want},
		"[]string":      []string{"Accept: " + want},
		"*Headers":      NewHeaders(Headers{"Accept: " + want}),
		"[]HeaderPair":  []HeaderPair{{"Accept", want}},
		"map":           map[string]string{"Accept": want},
		"map of slices": map[string][]string{"Accept": {want}},
	} {
		got := NewHeaders(in).Get("Accept")
		if got != want {
			t.Errorf("%s: Get(Accept) = %q, want %q", name, got, want)
		}
	}
}

// The transport form lower-cases names, because HTTP/2 requires it, and keeps
// the first-seen order.
func TestHeadersToTransportKeepsOrder(t *testing.T) {
	h := Headers{"Sec-Ch-Ua: x", "User-Agent: y", "Accept: z"}
	m, order := h.toTransport()

	if strings.Join(order, ",") != "sec-ch-ua,user-agent,accept" {
		t.Errorf("order = %v", order)
	}
	if len(m["user-agent"]) != 1 || m["user-agent"][0] != "y" {
		t.Errorf("user-agent = %v", m["user-agent"])
	}
}

// A repeated name is kept as multiple values, which RFC 7230 allows and
// Set-Cookie needs.
func TestHeadersRepeatedValues(t *testing.T) {
	h := Headers{}
	h.Add("Set-Cookie", "a=1")
	h.Add("Set-Cookie", "b=2")

	if got := h.GetList("set-cookie"); len(got) != 2 {
		t.Fatalf("GetList = %v, want two values", got)
	}
	if got := h.Get("Set-Cookie"); got != "a=1, b=2" {
		t.Errorf("Get = %q, want the values joined", got)
	}
}
