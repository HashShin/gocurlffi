package impersonate

import "testing"

// TestTargetConstantsResolve checks the named constants in both directions, so
// neither a typo nor a missing name can hide.
func TestTargetConstantsResolve(t *testing.T) {
	// Every constant must name a real preset.
	for _, name := range namedTargets {
		if _, err := Get(name); err != nil {
			t.Errorf("target constant %q does not resolve: %v", name, err)
		}
	}

	// And every preset must have a constant, so adding one to the table without
	// naming it fails here rather than leaving callers to spell a raw string.
	have := map[string]bool{}
	for _, name := range namedTargets {
		have[name] = true
	}
	for _, name := range Targets() {
		if !have[name] {
			t.Errorf("target %q has no constant; add one to targets.go", name)
		}
	}
}

// The alias means a constant is usable anywhere a string is, including the
// Impersonate field of a Request. This is the compile-time proof of that.
func TestTargetConstantIsAString(t *testing.T) {
	var s string = Chrome131
	if s != "chrome131" {
		t.Fatalf("Chrome131 = %q, want chrome131", s)
	}
	if Chrome146 != "chrome146" {
		t.Fatalf("Chrome146 = %q, want chrome146", Chrome146)
	}
}
