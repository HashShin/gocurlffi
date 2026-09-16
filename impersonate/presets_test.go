package impersonate

import (
	"strings"
	"testing"
)

// These guard the preset table itself. It used to be generated from a vendored
// copy of curl-impersonate's lib/impersonate.c, and a test compared it against
// a fresh parse so the committed data could not drift. The table is now the
// source of truth, so the equivalent protection is a set of invariants that a
// careless edit breaks loudly.
//
// The values are fingerprints: a dropped cipher or a reordered header is a
// different client, and nothing else in the test suite would notice.

func TestPresetTableShape(t *testing.T) {
	if len(presets) != 39 {
		t.Fatalf("preset table holds %d entries, want 39", len(presets))
	}
	seen := map[string]bool{}
	for _, p := range presets {
		if p.Target == "" {
			t.Errorf("a preset has an empty Target")
			continue
		}
		if seen[p.Target] {
			t.Errorf("%s appears twice in the table", p.Target)
		}
		seen[p.Target] = true

		if p.Alias == "" {
			// One preset, firefox133, has no alias upstream. That is the
			// upstream table's data, not a transcription slip, so an empty
			// alias is allowed as long as the target still resolves.
			if _, err := Get(p.Target); err != nil {
				t.Errorf("%s has no alias and does not resolve by target: %v", p.Target, err)
			}
			continue
		}
		got, err := Get(p.Alias)
		if err != nil {
			t.Errorf("%s: alias %q does not resolve: %v", p.Target, p.Alias, err)
			continue
		}
		if got.Target != p.Target {
			t.Errorf("alias %q resolves to %s, want %s", p.Alias, got.Target, p.Target)
		}
	}
}

// Every header must survive the round trip through its own "Name: Value"
// rendering, because requests rebuilds the wire form from Name and Value.
func TestPresetHeadersAreWellFormed(t *testing.T) {
	for _, p := range presets {
		for i, h := range p.HTTPHeaders {
			if h.Name == "" {
				t.Errorf("%s: header %d has an empty name", p.Target, i)
			}
			if strings.ContainsAny(h.Name, "\r\n:") {
				t.Errorf("%s: header name %q contains a separator", p.Target, h.Name)
			}
			if strings.ContainsAny(h.Value, "\r\n") {
				t.Errorf("%s: header %q value contains a newline", p.Target, h.Name)
			}
		}
	}
}

// The ordering fields are the ones a hand-edit is most likely to break, since
// they are easy to read as unordered sets.
//
// SigHashAlgs is deliberately absent: every Safari preset lists
// rsa_pss_rsae_sha384 twice, which is what that browser sends, so a repeat
// there is correct. The other lists have no repeats anywhere in the table.
func TestPresetOrderIsNotDuplicated(t *testing.T) {
	for _, p := range presets {
		for _, c := range []struct {
			field  string
			values []string
		}{
			{"Ciphers", p.Ciphers},
			{"Curves", p.Curves},
			{"HTTP3SigHashAlgs", p.HTTP3SigHashAlgs},
			{"CertCompression", p.CertCompression},
			{"TLSDelegatedCredentials", p.TLSDelegatedCredentials},
		} {
			seen := map[string]bool{}
			for _, v := range c.values {
				if v == "" {
					t.Errorf("%s: %s holds an empty entry", p.Target, c.field)
					continue
				}
				if seen[v] {
					t.Errorf("%s: %s repeats %q; a cipher or curve may not appear twice",
						p.Target, c.field, v)
				}
				seen[v] = true
			}
		}

		// The extension order is a dash separated list of extension ids, not a
		// slice, so it needs its own check.
		for _, c := range []struct {
			field string
			value string
		}{
			{"TLSExtensionOrder", p.TLSExtensionOrder},
			{"HTTP3TLSExtensionOrder", p.HTTP3TLSExtensionOrder},
		} {
			if c.value == "" {
				continue
			}
			seen := map[string]bool{}
			for _, id := range strings.Split(c.value, "-") {
				if id == "" {
					t.Errorf("%s: %s has an empty id in %q", p.Target, c.field, c.value)
					continue
				}
				if seen[id] {
					t.Errorf("%s: %s repeats extension id %s; the order is exact",
						p.Target, c.field, id)
				}
				seen[id] = true
			}
		}
	}
}

// A few well-known values, pinned so a bad merge or a stray edit to the table
// is visible without running the live fingerprint baseline.
func TestKnownPresetValues(t *testing.T) {
	chrome, err := Get("chrome131")
	if err != nil {
		t.Fatal(err)
	}
	if chrome.HTTPVersion != 2 {
		t.Errorf("chrome131 HTTPVersion = %d, want 2", chrome.HTTPVersion)
	}
	if chrome.Ciphers[0] != "TLS_AES_128_GCM_SHA256" {
		t.Errorf("chrome131 first cipher = %q, want TLS_AES_128_GCM_SHA256", chrome.Ciphers[0])
	}
	if !strings.HasPrefix(chrome.HTTP2Settings, "1:65536") {
		t.Errorf("chrome131 HTTP2Settings = %q, want it to start 1:65536", chrome.HTTP2Settings)
	}

	// The preset UA must match the header the transport actually sends.
	var ua string
	for _, h := range chrome.HTTPHeaders {
		if strings.EqualFold(h.Name, "User-Agent") {
			ua = h.Value
		}
	}
	if !strings.Contains(ua, "Chrome/131") {
		t.Errorf("chrome131 User-Agent = %q, want it to name Chrome/131", ua)
	}
}
