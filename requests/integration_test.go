package requests

import (
	"sort"
	"strings"
	"testing"
	"time"
)

// browserleaks mirrors the fingerprints curl_cffi reports, which is what this
// port aims to reproduce. The expected values were captured from the installed
// curl_cffi (see scripts/compare_fingerprints.py).
//
// JA4 is deliberately not asserted: presets that send GREASE ECH randomise the
// ECH payload length, so the padding extension (and therefore the JA4
// extension count) varies per connection in both curl_cffi and this port.
// JA3N is compared with padding removed for the same reason.
var liveFingerprints = map[string]struct {
	ja3n       string
	akamaiHash string
}{
	"chrome131": {
		"771|4865-4866-4867-49195-49199-49196-49200-52393-52392-49171-49172-156-157-47-53|0-10-11-13-16-17513-18-23-27-35-43-45-5-51-65037-65281|4588-29-23-24|0",
		"52d84b11737d980aef856699f885ca86",
	},
	"firefox135": {
		"771|4865-4867-4866-49195-49199-52393-52392-49196-49200-49162-49161-49171-49172-156-157-47-53|0-10-11-13-16-18-23-27-28-34-35-43-45-5-51-65037-65281|4588-29-23-24-25-256-257|0",
		"6ea73faa8fc5aac76bded7bd238f6433",
	},
	"safari180": {
		"771|4865-4866-4867-49196-49195-52393-49200-49199-52392-49162-49161-49172-49171-157-156-53-47-49160-49170-10|0-10-11-13-16-18-23-27-43-45-5-51-65281|29-23-24-25|0",
		"d4a2dcbfde511b5040ed5a5190a8d78b",
	},
	"tor145": {
		"771|4865-4867-4866-49195-49199-52393-52392-49196-49200-49171-49172-156-157-47-53|0-10-11-13-16-23-28-34-43-5-51-65037-65281|29-23-24-25-256-257|0",
		"6ea73faa8fc5aac76bded7bd238f6433",
	},
}

// normalizeJA3N drops the padding extension (21) so comparisons are not
// affected by ECH payload randomness.
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
			gotJA3N, _ := out["ja3n_text"].(string)
			if got := normalizeJA3N(gotJA3N); got != want.ja3n {
				t.Errorf("ja3n:\n got %s\nwant %s", got, want.ja3n)
			}
			if got, _ := out["akamai_hash"].(string); got != want.akamaiHash {
				t.Errorf("akamai_hash: got %q want %q", got, want.akamaiHash)
			}
		})
	}
}
