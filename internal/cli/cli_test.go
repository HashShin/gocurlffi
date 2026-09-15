package cli

import (
	"testing"
)

// The render switch has to be stripped before the fast-path flag parser sees it,
// whatever position it appears in.
func TestTakeRenderFlag(t *testing.T) {
	for _, tc := range []struct {
		in    []string
		want  []string
		found bool
	}{
		{[]string{"https://x.test/"}, []string{"https://x.test/"}, false},
		{[]string{"https://x.test/", "--render"}, []string{"https://x.test/"}, true},
		{[]string{"--render", "https://x.test/"}, []string{"https://x.test/"}, true},
		{[]string{"https://x.test/", "--render", "-f", "text"}, []string{"https://x.test/", "-f", "text"}, true},
		{[]string{"-js", "https://x.test/"}, []string{"https://x.test/"}, true},
	} {
		got := append([]string(nil), tc.in...)
		found := takeRenderFlag(&got)
		if found != tc.found {
			t.Errorf("takeRenderFlag(%v) found = %v, want %v", tc.in, found, tc.found)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("takeRenderFlag(%v) = %v, want %v", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("takeRenderFlag(%v) = %v, want %v", tc.in, got, tc.want)
			}
		}
	}
}

func TestMainRejectsUnknownCommand(t *testing.T) {
	if code := Main([]string{"definitely-not-a-command"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestMainRequiresAnArgument(t *testing.T) {
	if code := Main(nil); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

// There is no browser path for a request body, so this must fail before any
// network call rather than silently ignoring the flag.
func TestMainRejectsRenderOnPost(t *testing.T) {
	if code := Main([]string{"post", "https://x.test/", "--render"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestMainRenderWithoutURL(t *testing.T) {
	if code := Main([]string{"get", "--render"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if code := Main([]string{"open"}); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestMainVersion(t *testing.T) {
	if code := Main([]string{"version"}); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}
