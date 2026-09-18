// Command shade is a curl_cffi-style HTTP client with browser TLS/HTTP
// fingerprint impersonation, and a pure-Go headless browser in the same binary.
//
// Usage:
//
//	shade get https://tls.browserleaks.com/json --impersonate chrome
//	shade get https://example.com --render --format text
//	shade open https://example.com --eval 'document.title'
//	shade serve --port 9222
//	shade mcp --port 9223
//	shade list
package main

import (
	"os"

	"github.com/HashShin/shade/internal/cli"
)

// version is the build commit, injected with -ldflags -X main.version=...
var version = "dev"

func main() {
	cli.Version = version
	os.Exit(cli.Main(os.Args[1:]))
}
