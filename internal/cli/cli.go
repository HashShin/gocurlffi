// Package cli implements the shade command line: one binary with two
// request paths and the servers that expose the browser to other tools.
//
// The fast path (RunFetch) performs a single HTTP request through the
// impersonating transport and never constructs a JavaScript engine. The browser
// path (RunBrowserGet) loads the same URL in the pure-Go browser and runs the
// page's scripts. RunServe and RunMCP expose the browser over CDP, WebDriver
// BiDi and MCP.
//
// Every subcommand returns an exit status instead of calling os.Exit, so the
// whole surface stays testable and a failure is visible to a shell.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/HashShin/shade/impersonate"
)

// Version is the commit the binary was built from, injected by the Makefile
// with -ldflags. It is printed with --debug on the browser path so that a stale
// build is visible: an older binary that misses a cascade fix produces exactly
// the same symptoms as a bug in the current one.
var Version = "dev"

// Semver is the command's own version, independent of the build commit.
const Semver = "0.1.0"

// renderFlags switch `get` from the HTTP path to the browser path. --js is the
// spelling the command used before the two binaries were merged, and is kept so
// existing scripts keep working.
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

// httpMethod reports whether cmd names an HTTP method, and returns it
// upper-cased. A bare method is accepted as a command so that
// `shade post URL -j '{}'` works without a `get`-style verb.
func httpMethod(cmd string) (string, bool) {
	switch strings.ToUpper(cmd) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE":
		return strings.ToUpper(cmd), true
	}
	return "", false
}

// helpTargets maps a command name to the subcommand whose flags explain it, so
// `shade help post` prints the fast-path flags.
func helpTargets(cmd string) []string {
	switch cmd {
	case "get", "fetch":
		return []string{"get", "--help"}
	case "open", "browse", "render":
		return []string{"open", "--help"}
	case "serve", "mcp":
		return []string{cmd, "--help"}
	}
	if _, ok := httpMethod(cmd); ok {
		return []string{"get", "--help"}
	}
	return nil
}

// Main runs the command line and returns the process exit status.
func Main(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, rootUsage)
		return exitUsage
	}
	cmd, rest := args[0], args[1:]

	switch cmd {
	case "get", "fetch":
		if takeRenderFlag(&rest) {
			if len(rest) == 0 {
				fmt.Fprintln(os.Stderr, "error: missing URL")
				return exitUsage
			}
			return RunBrowserGet(rest)
		}
		return RunFetch("GET", rest)

	case "open", "browse", "render":
		// A render switch here is redundant but not an error.
		takeRenderFlag(&rest)
		if len(rest) == 0 {
			fmt.Fprintln(os.Stderr, "error: missing URL")
			return exitUsage
		}
		return RunBrowserGet(rest)

	case "serve":
		return RunServe(rest)
	case "mcp":
		return RunMCP(rest)

	case "targets", "list":
		for _, t := range impersonate.Targets() {
			fmt.Println(t)
		}
		return exitOK

	case "version", "--version", "-V":
		fmt.Printf("shade %s (%s)\n", Semver, Version)
		return exitOK

	case "help", "--help", "-h":
		if len(rest) > 0 {
			if target := helpTargets(rest[0]); target != nil {
				return Main(target)
			}
		}
		fmt.Print(rootUsage)
		return exitOK
	}

	// A bare HTTP method is a command for the fast path.
	if method, ok := httpMethod(cmd); ok {
		if takeRenderFlag(&rest) {
			fmt.Fprintf(os.Stderr,
				"error: --render is only available for get/open; %s has a request body\n",
				strings.ToLower(cmd))
			return exitUsage
		}
		return RunFetch(method, rest)
	}

	fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
	fmt.Fprint(os.Stderr, rootUsage)
	return exitUsage
}

// parseStatus turns a failed flag parse into an exit status. -h/--help is a
// request for information, not a failure, and the flags themselves have already
// been printed by the flag package.
func parseStatus(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	return exitUsage
}
