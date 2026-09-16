// Command gocurlffi is a curl_cffi-style HTTP client with browser TLS/HTTP
// fingerprint impersonation, and a pure-Go headless browser in the same binary.
//
// Usage:
//
//	gocurlffi get https://tls.browserleaks.com/json --impersonate chrome
//	gocurlffi get https://example.com --render --format text
//	gocurlffi open https://example.com --eval 'document.title'
//	gocurlffi serve --port 9222
//	gocurlffi mcp --port 9223
//	gocurlffi list
package main

import (
	"os"

	"github.com/HashShin/gocurlffi/internal/cli"
)

// version is the build commit, injected with -ldflags -X main.version=...
var version = "dev"

func main() {
	cli.Version = version
	os.Exit(cli.Main(os.Args[1:]))
}
