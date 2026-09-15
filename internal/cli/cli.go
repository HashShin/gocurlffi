package cli

import (
	"fmt"
	"os"
	"strings"

	"gocurlffi/impersonate"
)

// Version is the commit the binary was built from, injected by the Makefile
// with -ldflags. It is printed with --debug on the browser path so that a stale
// build is visible: an older binary that misses a cascade fix produces exactly
// the same symptoms as a bug in the current one.
var Version = "dev"

// Semver is the command's own version, independent of the build commit.
const Semver = "0.1.0"

const rootUsage = `gocurlffi - HTTP with browser impersonation, and a pure-Go headless browser

usage:
  gocurlffi get <url> [flags]           fetch with browser TLS/JA3 impersonation
  gocurlffi get <url> --render [flags]  the same URL through the JavaScript browser
  gocurlffi open <url> [flags]          alias for "get --render"
  gocurlffi serve [flags]               CDP + WebDriver BiDi server
  gocurlffi mcp [flags]                 MCP tool server (stdio, or --port for HTTP)
  gocurlffi list                        list impersonation targets
  gocurlffi version                     print the version

methods:
  get, post, put, patch, delete, head, options, trace
  --render applies to get/open only: there is no browser path for a POST body.

The fast path and the browser share one binary and one impersonation target.
"gocurlffi get" costs no JavaScript engine at run time; --render constructs one.

note: -f means --form on the fast path and --format on the browser path, which
is inherited from the two CLIs that were merged. Write --format in full when a
command could be either. Run "gocurlffi open --help" for the browser flags.
`

// renderFlags are the spellings that switch `get` from the HTTP path to the
// browser path.
var renderFlags = map[string]bool{
	"--render": true, "-render": true,
	"--js": true, "-js": true,
}

// takeRenderFlag removes a render switch from args and reports whether one was
// present.
func takeRenderFlag(args *[]string) bool {
	found := false
	out := (*args)[:0:0]
	for _, a := range *args {
		if renderFlags[a] {
			found = true
			continue
		}
		out = append(out, a)
	}
	*args = out
	return found
}

// Main runs the command line and returns the process exit status.
func Main(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, rootUsage)
		return 2
	}
	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "get", "fetch":
		if takeRenderFlag(&rest) {
			if len(rest) == 0 {
				fmt.Fprintln(os.Stderr, "error: missing URL")
				return 2
			}
			RunBrowserGet(rest)
			return 0
		}
		RunFetch(append([]string{strings.ToUpper(cmd)}, rest...))
		return 0
	case "open", "browse", "render":
		if len(rest) == 0 {
			fmt.Fprintln(os.Stderr, "error: missing URL")
			return 2
		}
		RunBrowserGet(rest)
		return 0
	case "serve":
		RunServe(rest)
		return 0
	case "mcp":
		RunMCP(rest)
		return 0
	case "list", "targets":
		for _, t := range impersonate.Targets() {
			fmt.Println(t)
		}
		return 0
	case "version", "--version", "-V":
		fmt.Printf("gocurlffi %s (%s)\n", Semver, Version)
		return 0
	case "help", "--help", "-h":
		fmt.Print(rootUsage)
		return 0
	}

	// A bare HTTP method still works, so `gocurlffi post URL -j '{}'` is
	// unchanged from before the commands were merged.
	switch strings.ToUpper(cmd) {
	case "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE":
		if takeRenderFlag(&rest) {
			fmt.Fprintf(os.Stderr,
				"error: --render is only available for get/open; %s has a request body\n",
				strings.ToLower(cmd))
			return 2
		}
		RunFetch(append([]string{strings.ToUpper(cmd)}, rest...))
		return 0
	}

	fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
	fmt.Fprint(os.Stderr, rootUsage)
	return 2
}
