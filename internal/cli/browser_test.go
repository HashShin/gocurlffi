package cli

import (
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

// The driving flags keep the order they were given: a flow that logs in fills
// before it clicks, so the click has something to submit.
func TestDriveFlagsKeepOrder(t *testing.T) {
	g := &browserGetFlags{}
	fs := newFlagSet("get --render", "shade get <url> --render", "test")
	g.register(fs)

	if err := fs.Parse([]string{"--click", "#a", "--fill", "#b=1", "--click", "#c", "--type", "#d=x"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []driveStep{
		{kind: "click", value: "#a"},
		{kind: "fill", value: "#b=1"},
		{kind: "click", value: "#c"},
		{kind: "type", value: "#d=x"},
	}
	if len(g.actions.steps) != len(want) {
		t.Fatalf("steps = %v, want %v", g.actions.steps, want)
	}
	for i, step := range g.actions.steps {
		if step != want[i] {
			t.Fatalf("step %d = %v, want %v", i, step, want[i])
		}
	}
}

// End to end through the command: fill, click the submit button, wait for what
// the click brought about. The wait comes after the driving, or it waits for a
// page that has not been submitted yet.
func TestBrowserGetDrivesInFlagOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = r.ParseForm()
		if r.FormValue("user") == "" {
			_, _ = w.Write([]byte(`<form action="/" method="post">
				<input id="u" name="user">
				<button id="s" type="submit">Login</button>
			</form>`))
			return
		}
		_, _ = w.Write([]byte(`<h1 id="done">hello ` + r.FormValue("user") + `</h1>`))
	}))
	defer srv.Close()

	out, errOut := captureOutput(t, func() int {
		return RunBrowserGet([]string{srv.URL + "/",
			"--fill", "#u=me",
			"--click", "#s",
			"--wait", "#done",
			"--wait-timeout", "300ms",
			"--format", "text",
		})
	})
	if !strings.Contains(out, "hello me") {
		t.Fatalf("output = %q, want the submitted page", out)
	}
	// The wait must find what the click brought about. Waiting before the
	// driving, as this command once did, leaves this warning behind.
	if strings.Contains(errOut, "not found") {
		t.Fatalf("the wait ran before the driving: %q", errOut)
	}
}

// captureOutput runs the command with both streams redirected, so a test can
// read what it printed and what it warned about.
func captureOutput(t *testing.T, run func() int) (string, string) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW

	code := run()

	_ = outW.Close()
	_ = errW.Close()
	os.Stdout, os.Stderr = oldOut, oldErr

	out, _ := io.ReadAll(outR)
	errOut, _ := io.ReadAll(errR)
	_ = outR.Close()
	_ = errR.Close()
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr %q)", code, exitOK, errOut)
	}
	return string(out), string(errOut)
}
