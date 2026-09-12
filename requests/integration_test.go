package requests

import (
	"testing"
	"time"
)

// browserleaks mirrors the fingerprint curl_cffi reports, which is what this
// port aims to reproduce. These expectations were captured from the installed
// curl_cffi (see scripts/compare_fingerprints.py).
var liveFingerprints = map[string]struct {
	ja4        string
	ja3nHash   string
	akamaiHash string
}{
	"chrome131":  {"t13d1516h2_8daaf6152771_02713d6af862", "dee19b855b658c6aa0f575eda2525e19", "52d84b11737d980aef856699f885ca86"},
	"firefox135": {"t13d1717h2_5b57614c22b0_3cbfd9057e0d", "e4147a4860c1f347354f0a84d8787c02", "6ea73faa8fc5aac76bded7bd238f6433"},
	"safari180":  {"t13d2014h2_a09f3c656075_e42f34c56612", "44f7ed5185d22c92b96da72dbe68d307", "d4a2dcbfde511b5040ed5a5190a8d78b"},
	"tor145":     {"t13d1513h2_8daaf6152771_748f4c70de1c", "7b0f620d5ed159195cfe1b7e75b25ef3", "6ea73faa8fc5aac76bded7bd238f6433"},
}

func TestLiveFingerprints(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}
	for name, want := range liveFingerprints {
		name, want := name, want
		t.Run(name, func(t *testing.T) {
			s := NewSession(WithImpersonate(name), WithTimeout(30*time.Second))
			defer s.Close()
			rsp, err := s.Get("https://tls.browserleaks.com/json")
			if err != nil {
				t.Skipf("network unavailable: %v", err)
			}
			var out map[string]any
			if err := rsp.JSON(&out); err != nil {
				t.Fatalf("decode: %v", err)
			}
			checks := map[string]string{
				"ja4":         want.ja4,
				"ja3n_hash":   want.ja3nHash,
				"akamai_hash": want.akamaiHash,
			}
			for key, expected := range checks {
				if got, _ := out[key].(string); got != expected {
					t.Errorf("%s: got %q want %q", key, got, expected)
				}
			}
		})
	}
}
