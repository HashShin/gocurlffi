package main

import (
	"flag"
	"strings"
	"testing"
)

// boolFlagNames reads the value-less options from the flag set, so a boolean
// option never has to be declared twice. If a new one were missed, reorderFlags
// would bind the argument after it: "get URL --sheets -f text" tried to fetch
// the host "text".
func TestBoolFlagNames(t *testing.T) {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	fs.String("f", "html", "format")
	fs.Duration("timeout", 0, "timeout")
	fs.Bool("debug", false, "debug")
	fs.Bool("sheets", false, "sheets")
	names := boolFlagNames(fs)
	for _, want := range []string{"-debug", "--debug", "-sheets", "--sheets"} {
		if !names[want] {
			t.Errorf("boolFlagNames missed %q", want)
		}
	}
	for _, notBool := range []string{"-f", "--f", "-timeout", "--timeout"} {
		if names[notBool] {
			t.Errorf("boolFlagNames marked the value-taking flag %q as boolean", notBool)
		}
	}
}

func TestReorderFlags(t *testing.T) {
	boolFlags := map[string]bool{"-debug": true, "--debug": true, "-sheets": true, "--sheets": true}
	cases := []struct {
		in   []string
		want []string
	}{
		// A value-taking flag after the URL still binds its value.
		{[]string{"https://x/", "-f", "text"}, []string{"-f", "text", "https://x/"}},
		// A boolean flag never consumes the next argument.
		{[]string{"https://x/", "--sheets", "-f", "text"}, []string{"--sheets", "-f", "text", "https://x/"}},
		{[]string{"https://x/", "--debug"}, []string{"--debug", "https://x/"}},
		{[]string{"-i", "chrome", "https://x/", "--no-js"}, []string{"-i", "chrome", "--no-js", "https://x/"}},
		// --flag=value carries its own value.
		{[]string{"https://x/", "--width=900"}, []string{"--width=900", "https://x/"}},
		{[]string{"-H", "A: b", "https://x/"}, []string{"-H", "A: b", "https://x/"}},
	}
	for _, c := range cases {
		got := reorderFlags(c.in, boolFlags)
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("reorderFlags(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
