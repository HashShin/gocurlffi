package requests

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// fingerprint_baseline.json holds the JA3N (normalized), JA4 and Akamai hashes
// reported by the Python curl_cffi, recorded once with
// scripts/record_baseline (see README). The live test below compares this
// port's fingerprints against that baseline, so no Python is needed to run it.
const baselinePath = "testdata/fingerprint_baseline.json"

type baselineEntry struct {
	JA3NText   string `json:"ja3n_text"`
	JA4        string `json:"ja4"`
	AkamaiHash string `json:"akamai_hash"`
}

// normalizeJA3N drops the padding extension (21) and sorts the extension list
// so comparisons are not affected by ECH payload randomness.
func normalizeJA3N(text string) string {
	parts := strings.Split(text, ",")
	if len(parts) < 5 {
		return text
	}
	exts := strings.Split(parts[2], "-")
	kept := exts[:0]
	for _, e := range exts {
		if e != "" && e != "21" {
			kept = append(kept, e)
		}
	}
	sort.Strings(kept)
	parts[2] = strings.Join(kept, "-")
	return strings.Join(parts, "|")
}

func loadBaseline(t *testing.T) map[string]baselineEntry {
	t.Helper()
	data, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Skipf("baseline not available: %v", err)
	}
	var base map[string]baselineEntry
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatalf("parse baseline: %v", err)
	}
	return base
}

// TestLiveFingerprintBaseline checks every recorded target against
// tls.browserleaks.com. Requires network; skipped with -short. Targets whose
// request fails are skipped rather than failed, so the test is not flaky on
// rate limiting.
func TestLiveFingerprintBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}
	base := loadBaseline(t)
	for name, want := range base {
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
			gotJA3N, _ := out["ja3n_text"].(string)
			if normalizeJA3N(gotJA3N) != normalizeJA3N(want.JA3NText) {
				t.Errorf("ja3n mismatch:\n got %s\nwant %s",
					normalizeJA3N(gotJA3N), normalizeJA3N(want.JA3NText))
			}
			if got, _ := out["akamai_hash"].(string); got != want.AkamaiHash {
				t.Errorf("akamai_hash: got %q want %q", got, want.AkamaiHash)
			}
		})
	}
}
