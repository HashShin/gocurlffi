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

// A request that fails must not report success. This regressed once: Main
// discarded the status returned by the command functions and always returned 0,
// so `gocurlffi get <bad-host>` exited 0 with the error on stderr and a script
// could not tell that it had failed. Port 1 on the loopback address refuses the
// connection immediately, so this needs no network.
func TestMainPropagatesRequestFailure(t *testing.T) {
	if code := Main([]string{"get", "http://127.0.0.1:1/"}); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

// A malformed command line is a usage error (2), which is distinct from a
// request that ran and failed (1).
func TestMainUsageErrorsAreDistinct(t *testing.T) {
	for _, args := range [][]string{
		{"get", "https://x.test/", "--no-such-flag"},
		{"get", "https://x.test/", "-j", "{not json"},
		{"nonsense-command"},
	} {
		if code := Main(args); code != 2 {
			t.Errorf("Main(%q) = %d, want 2", args, code)
		}
	}
}

// -h/--help is a request for information, not a failure.
func TestMainHelpIsNotAnError(t *testing.T) {
	for _, args := range [][]string{
		{"help"}, {"--help"}, {"-h"},
		{"help", "get"}, {"help", "post"}, {"help", "open"}, {"help", "serve"},
		{"get", "--help"},
	} {
		if code := Main(args); code != 0 {
			t.Errorf("Main(%q) = %d, want 0", args, code)
		}
	}
}

// -f means --format on the browser path. It deliberately does not mean --form
// there, so the fast path rejects it rather than guessing: one letter with two
// meanings across one binary is worse than a longer spelling.
func TestFormatShortFlagBelongsToTheBrowserPath(t *testing.T) {
	fs := newFlagSet("get", "usage", "synopsis")
	f := &fetchFlags{}
	f.register(fs)
	if err := parse(fs, []string{"https://x.test/", "-f", "text"}); err == nil {
		t.Fatal("-f was accepted on the fast path, where it has no meaning")
	}

	fs = newFlagSet("open", "usage", "synopsis")
	g := &browserGetFlags{}
	g.register(fs)
	if err := parse(fs, []string{"https://x.test/", "-f", "text"}); err != nil {
		t.Fatalf("-f was rejected on the browser path: %v", err)
	}
	if g.format != "text" {
		t.Fatalf("format = %q, want text", g.format)
	}
}
