package impersonate

import (
	"testing"
)

func TestGetResolvesAliases(t *testing.T) {
	cases := map[string]string{
		"chrome":         DefaultChrome,
		"firefox":        DefaultFirefox,
		"safari":         DefaultSafari,
		"safari_ios":     DefaultSafariIOS,
		"tor":            DefaultTor,
		"chrome_android": DefaultChromeAndroid,
		"safari15_3":     "safari153",
		"safari18_4_ios": "safari184_ios",
	}
	for alias, want := range cases {
		p, err := Get(alias)
		if err != nil {
			t.Errorf("Get(%q): %v", alias, err)
			continue
		}
		if p.Target != want {
			t.Errorf("Get(%q) = %s, want %s", alias, p.Target, want)
		}
	}
}

func TestGetUnknown(t *testing.T) {
	if _, err := Get("nope"); err == nil {
		t.Fatal("expected error for unknown browser")
	}
}

func TestTargetsSortedAndComplete(t *testing.T) {
	targets := Targets()
	if len(targets) != 40 {
		t.Fatalf("targets = %d, want 40 (39 presets + custom)", len(targets))
	}
	for i := 1; i < len(targets); i++ {
		if targets[i-1] >= targets[i] {
			t.Fatalf("targets not sorted: %s >= %s", targets[i-1], targets[i])
		}
	}
}

func TestPresetHeadersPresent(t *testing.T) {
	for _, name := range Targets() {
		p, _ := Get(name)
		if len(p.HTTPHeaders) == 0 {
			t.Errorf("%s: no HTTP headers", name)
		}
		if p.Browser() == "" {
			t.Errorf("%s: unknown browser family", name)
		}
	}
}

func TestChromePresetDetails(t *testing.T) {
	p, _ := Get("chrome131")
	if p.TLSGrease != true {
		t.Error("chrome should use GREASE")
	}
	if p.SplitCookies != true {
		t.Error("chrome should split cookies")
	}
	if len(p.PseudoHeaderOrder()) != 4 {
		t.Errorf("pseudo order = %v", p.PseudoHeaderOrder())
	}
	if p.TLSProfile() == "" {
		t.Error("chrome131 should map to a TLS profile")
	}
}
