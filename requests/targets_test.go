package requests

import (
	"errors"
	"strings"
	"testing"

	"github.com/HashShin/gocurlffi/impersonate"
)

// The re-exported constants are a convenience, so the risk they carry is that
// the list here drifts from the preset table. These tests close that.
func TestReExportedTargetsMatchImpersonate(t *testing.T) {
	want := impersonate.Targets()
	if len(allTargets) != len(want) {
		t.Fatalf("requests/targets.go re-exports %d targets, impersonate has %d; "+
			"a target was added or removed without updating the other",
			len(allTargets), len(want))
	}

	have := map[string]bool{}
	for _, n := range allTargets {
		have[n] = true
	}
	for _, n := range want {
		if !have[n] {
			t.Errorf("target %q is not re-exported in requests/targets.go", n)
		}
	}
}

// Every re-exported name must be a real target, so a typo here fails rather
// than reaching a caller.
func TestReExportedTargetsResolve(t *testing.T) {
	for _, n := range allTargets {
		if _, err := impersonate.Get(n); err != nil {
			t.Errorf("requests re-exports %q, which does not resolve: %v", n, err)
		}
	}
}

// Spot-check that the alias really is the same constant, not a second copy of
// the value that could diverge.
func TestReExportedTargetIsTheSameConstant(t *testing.T) {
	for _, c := range []struct {
		inRequests    string
		inImpersonate string
	}{
		{Chrome146, impersonate.Chrome146},
		{Chrome131, impersonate.Chrome131},
		{Safari260, impersonate.Safari260},
		{Firefox147, impersonate.Firefox147},
		{Custom, impersonate.Custom},
	} {
		if c.inRequests != c.inImpersonate {
			t.Errorf("requests and impersonate disagree: %q vs %q",
				c.inRequests, c.inImpersonate)
		}
	}
}

// The point of the re-export: one import, and the target really is applied.
func TestSendWithReExportedTarget(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	s := NewSession()
	defer s.Close()

	rsp, err := s.Send(Request{URL: srv.URL + "/get", Impersonate: Chrome131})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var out struct {
		Headers map[string][]string `json:"headers"`
	}
	if err := rsp.JSON(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var ua string
	if v := out.Headers["User-Agent"]; len(v) > 0 {
		ua = v[0]
	}
	if !strings.Contains(ua, "Chrome/131") {
		t.Errorf("User-Agent = %q, want the Chrome 131 preset", ua)
	}
}

// A misspelt string is the failure mode the constants exist to prevent: it is
// only caught when the request is made.
func TestUnknownImpersonationTargetIsAnError(t *testing.T) {
	s := NewSession()
	defer s.Close()

	_, err := s.Send(Request{URL: "https://example.test/", Impersonate: "chrome999"})
	if err == nil {
		t.Fatal("an unknown target should fail the request")
	}
	var imp *ImpersonateError
	if !errors.As(err, &imp) {
		t.Errorf("err = %T (%v), want *ImpersonateError", err, err)
	}
}
