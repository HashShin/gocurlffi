package requests

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// An option this port accepts for curl_cffi compatibility but cannot honour
// must fail the request rather than be dropped. Silently ignoring a
// fingerprint option sends a request that carries a different fingerprint than
// the caller asked for, which is the failure mode these tests exist to prevent.
func TestUnsupportedOptionsFailLoudly(t *testing.T) {
	cases := []struct {
		name string
		opt  Option
	}{
		{"WithJA3", WithJA3("771,4865-4866,0-11-10,29-23,0")},
		{"WithAkamai", WithAkamai("3:100;4:65536|1048510465|0|m,s,a,p")},
		{"WithExtraFP", WithExtraFP(&ExtraFingerprints{TLSMinVersion: "1.2"})},
		{"WithQuote", WithQuote(false)},
		{"WithMaxRecvSpeed", WithMaxRecvSpeed(4096)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSession()
			defer s.Close()

			_, err := s.Get("https://example.test/", tc.opt)
			var unsupported *UnsupportedOptionError
			if !errors.As(err, &unsupported) {
				t.Fatalf("err = %v, want *UnsupportedOptionError", err)
			}
			if !strings.Contains(err.Error(), tc.name) {
				t.Errorf("error does not name the option it refused: %v", err)
			}
		})
	}
}

// The check runs before the request is built, so nothing is sent. Pointing the
// session at a live server and asserting it was never reached is what proves
// the failure happens up front rather than after a connection.
func TestUnsupportedOptionSendsNothing(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	s := NewSession()
	defer s.Close()

	if _, err := s.Get(srv.URL, WithJA3("771,4865,0,0")); err == nil {
		t.Fatal("expected the request to fail")
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("the server received %d request(s); the option check must run first", n)
	}
}

// WithDefaultHeaders(false) keeps the caller's headers and drops the
// impersonated browser's, which is what curl_cffi does for
// default_headers=False. It is the one previously-ignored option that is
// implemented rather than refused.
func TestDefaultHeadersFalseDropsThePresetHeaderSet(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	headersFor := func(opts ...Option) map[string]any {
		t.Helper()
		s := NewSession(opts...)
		defer s.Close()
		rsp, err := s.Get(srv.URL + "/get")
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		var out map[string]any
		if err := rsp.JSON(&out); err != nil {
			t.Fatalf("decoding the echo failed: %v", err)
		}
		h, _ := out["headers"].(map[string]any)
		return h
	}

	withDefaults := headersFor(WithImpersonate("chrome131"))
	ua, _ := withDefaults["User-Agent"].([]any)
	if len(ua) == 0 || !strings.Contains(ua[0].(string), "Chrome/131") {
		t.Fatalf("the preset User-Agent was not sent by default: %v", withDefaults["User-Agent"])
	}
	if withDefaults["Sec-Ch-Ua"] == nil {
		t.Fatalf("the preset sec-ch-ua was not sent by default")
	}

	without := headersFor(
		WithImpersonate("chrome131"),
		WithDefaultHeaders(false),
		WithHeader("X-Mine", "kept"),
	)
	if ua, ok := without["User-Agent"].([]any); ok && len(ua) > 0 {
		if s, _ := ua[0].(string); strings.Contains(s, "Chrome/131") {
			t.Errorf("default_headers=False still sent the preset User-Agent: %q", s)
		}
	}
	if without["Sec-Ch-Ua"] != nil {
		t.Errorf("default_headers=False still sent sec-ch-ua: %v", without["Sec-Ch-Ua"])
	}
	if without["X-Mine"] == nil {
		t.Error("default_headers=False dropped the caller's own header")
	}

	// A fingerprint preset is still selected: only its header set is skipped.
	s := NewSession(WithImpersonate("chrome131"), WithDefaultHeaders(false))
	defer s.Close()
	if ua := s.UserAgent(); !strings.Contains(ua, "Chrome/131") {
		t.Errorf("UserAgent() = %q, want the impersonated browser's", ua)
	}
}
