package requests

import (
	"net/url"
	"testing"

	"gocurlffi/impersonate"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestAllPresetsBuildSpec(t *testing.T) {
	for _, name := range impersonate.Targets() {
		p, err := impersonate.Get(name)
		if err != nil {
			t.Fatalf("Get(%s): %v", name, err)
		}
		order := presetOrder(p)
		spec, err := buildSpecForPreset(p, order)
		if err != nil {
			t.Errorf("%s: buildSpecForPreset: %v", name, err)
			continue
		}
		if len(spec.CipherSuites) == 0 {
			t.Errorf("%s: no cipher suites", name)
		}
		if len(spec.Extensions) == 0 {
			t.Errorf("%s: no extensions", name)
		}
		wantCiphers := len(p.Ciphers)
		if p.TLSGrease {
			wantCiphers++
		}
		if len(spec.CipherSuites) != wantCiphers {
			t.Errorf("%s: ciphers = %d, want %d (all names should map)",
				name, len(spec.CipherSuites), wantCiphers)
		}
	}
}

func TestCipherNameCoverage(t *testing.T) {
	for _, name := range impersonate.Targets() {
		p, _ := impersonate.Get(name)
		for _, c := range p.Ciphers {
			if _, ok := cipherByName[c]; !ok {
				t.Errorf("%s: unmapped cipher %q", name, c)
			}
		}
		for _, g := range p.Curves {
			if _, ok := curveByName[g]; !ok {
				t.Errorf("%s: unmapped curve %q", name, g)
			}
		}
		for _, s := range p.SigHashAlgs {
			if _, ok := sigByName[s]; !ok {
				t.Errorf("%s: unmapped signature algorithm %q", name, s)
			}
		}
	}
}

func TestParseHTTP2Settings(t *testing.T) {
	settings, order := parseHTTP2Settings("1:65536;2:0;4:6291456;6:262144")
	if len(order) != 4 {
		t.Fatalf("order = %v", order)
	}
	if settings[order[0]] != 65536 || settings[order[1]] != 0 {
		t.Errorf("settings = %v", settings)
	}
	if order[0] != 1 || order[3] != 6 {
		t.Errorf("order ids = %v", order)
	}
}

func TestParseHTTP3SettingsSkipsGrease(t *testing.T) {
	settings, order := parseHTTP3Settings("1:65536;6:262144;7:100;51:1;GREASE")
	if len(settings) != 4 || len(order) != 4 {
		t.Fatalf("settings=%v order=%v", settings, order)
	}
	if _, ok := settings[65536]; ok {
		t.Error("GREASE must not be turned into a setting")
	}
}

func TestPresetPseudoOrder(t *testing.T) {
	p, _ := impersonate.Get("chrome")
	got := p.PseudoHeaderOrder()
	want := []string{":method", ":authority", ":scheme", ":path"}
	if len(got) != len(want) {
		t.Fatalf("pseudo order = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pseudo order = %v", got)
		}
	}
}
