package browser

import (
	"testing"

	"github.com/HashShin/gocurlffi/impersonate"
)

// The re-exported constants exist so a browser-only caller needs one import.
// They must cover the same targets impersonate has, or the list here has fallen
// behind the preset table.
func TestReExportedTargetsMatchImpersonate(t *testing.T) {
	want := impersonate.Targets()
	if len(targetConstants) != len(want) {
		t.Fatalf("browser re-exports %d targets, impersonate has %d; "+
			"a target was added or removed without updating the other",
			len(targetConstants), len(want))
	}
	have := map[string]bool{}
	for _, n := range targetConstants {
		have[n] = true
	}
	for _, n := range want {
		if !have[n] {
			t.Errorf("target %q is not re-exported in browser/targets.go", n)
		}
	}
}

// The aliases must be the same constants, not copies that could diverge.
func TestReExportedTargetsAreTheSameValues(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{Chrome131, impersonate.Chrome131},
		{Chrome146, impersonate.Chrome146},
		{Safari260, impersonate.Safari260},
		{Firefox147, impersonate.Firefox147},
		{DefaultChrome, impersonate.DefaultChrome},
		{Custom, impersonate.Custom},
	} {
		if c.got != c.want {
			t.Errorf("browser and impersonate disagree: %q vs %q", c.got, c.want)
		}
	}
}

// Every re-exported constant, target or family default, must name something the
// impersonate package can resolve, so a typo here fails rather than reaching a
// caller.
func TestEveryReExportedConstantResolves(t *testing.T) {
	if len(allTargets) != len(targetConstants)+len(defaultConstants) {
		t.Fatalf("allTargets is %d, want %d", len(allTargets), len(targetConstants)+len(defaultConstants))
	}
	for _, n := range allTargets {
		if _, err := impersonate.Get(n); err != nil {
			t.Errorf("browser re-exports %q, which does not resolve: %v", n, err)
		}
	}
}
