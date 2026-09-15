// scraper - Google SERP scraper + web UI, one binary.
//
//	go run .                       -> start the web UI (http://127.0.0.1:8080)
//	go run . serve [-addr :8080]   -> same, explicitly
//	go run . search "query one" "query two"  -> CLI scrape
//	go run . search -file queries.txt
//	go run . "query one"           -> CLI scrape (back-compat shorthand)
//
// The scraping engine lives in ./gsearch and is shared by both modes.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"scraper/gsearch"
)

// fs is the active flag set for whichever mode is running.
var fs *flag.FlagSet

func newFS(name string)                           { fs = flag.NewFlagSet(name, flag.ExitOnError) }
func flagStr(n, def, h string) *string            { return fs.String(n, def, h) }
func flagInt(n string, def int, h string) *int    { return fs.Int(n, def, h) }
func flagBool(n string, def bool, h string) *bool { return fs.Bool(n, def, h) }
func flagDur(n string, def time.Duration, h string) *time.Duration {
	return fs.Duration(n, def, h)
}
func parseFlags() { fs.Parse(os.Args[1:]) }

func main() {
	args := os.Args[1:]

	// Decide which mode to run.
	if len(args) == 0 {
		runServe()
		return
	}
	switch args[0] {
	case "serve", "server", "web", "ui":
		os.Args = append([]string{os.Args[0]}, args[1:]...)
		runServe()
	case "search", "scrape", "cli":
		// Explicit subcommand: strip it so the flag parser sees only flags +
		// positional query args (Go's flag package stops at the first
		// non-flag token, so we must not leave "search" in os.Args).
		os.Args = append([]string{os.Args[0]}, args[1:]...)
		runSearch()
	default:
		// Back-compat: `go run . "query"` or `go run . -file queries.txt`.
		os.Args = append([]string{os.Args[0]}, args...)
		runSearch()
	}
}

// runSearch is the CLI scraper mode.
func runSearch() {
	newFS("search")
	concurrency := flagInt("concurrency", 3, "parallel tabs / max concurrent requests")
	num := flagInt("num", 20, "results per page (Google caps this)")
	max := flagInt("max", 100, "max total results per query (0 = unlimited)")
	interval := flagDur("interval", 2*time.Second, "global min delay between requests")
	timeout := flagDur("timeout", 20*time.Second, "per-query page-load timeout")
	retries := flagInt("retries", 3, "retries per query on CAPTCHA/timeout")
	file := flagStr("file", "", "file of queries, one per line")
	display := flagStr("display", ":99", "Xvfb display")
	browser := flagStr("browser", "", "browser binary (default: auto-detect)")
	asJSON := flagBool("json", false, "output JSON")
	outFile := flagStr("out", "", "write JSON to a file (implies -json)")
	parseFlags()

	var queries []string
	queries = append(queries, fs.Args()...)
	if *file != "" {
		if data, err := os.ReadFile(*file); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if line = strings.TrimSpace(line); line != "" {
					queries = append(queries, line)
				}
			}
		} else {
			fmt.Printf("read queries file: %v\n", err)
			return
		}
	}
	if len(queries) == 0 {
		queries = []string{
			"best scraper tools 2026",
			"machine learning tools 2026",
			"golang web scraping 2026",
		}
	}
	if *concurrency < 1 {
		*concurrency = 1
	}
	if *concurrency > len(queries) {
		*concurrency = len(queries)
	}

	fmt.Printf("queries=%d concurrency=%d num=%d max=%d interval=%s\n", len(queries), *concurrency, *num, *max, *interval)

	eng, err := gsearch.New(gsearch.Config{
		Browser:     *browser,
		Display:     *display,
		Concurrency: *concurrency,
		Interval:    *interval,
		Timeout:     *timeout,
		Retries:     *retries,
	})
	if err != nil {
		fmt.Printf("Engine error: %v\n", err)
		return
	}
	defer eng.Close()

	start := time.Now()
	ok := 0
	var all []gsearch.Result
	for _, q := range queries {
		res, err := eng.Search(q, *num, *max)
		if err != nil {
			fmt.Printf("\n[ERROR] %s: %v\n", q, err)
			continue
		}
		ok++
		fmt.Printf("\n=== %s ===\n", q)
		for _, r := range res {
			fmt.Printf("%d. %s\n   %s\n", r.Position, r.Title, r.URL)
			all = append(all, r)
		}
	}
	fmt.Printf("\ncompleted %d/%d queries in %s\n", ok, len(queries), time.Since(start).Round(time.Millisecond))

	// JSON output.
	if *asJSON || *outFile != "" {
		data, err := json.MarshalIndent(all, "", "  ")
		if err != nil {
			fmt.Printf("json encode: %v\n", err)
			return
		}
		if *outFile != "" {
			if werr := os.WriteFile(*outFile, data, 0o644); werr != nil {
				fmt.Printf("write %s: %v\n", *outFile, werr)
				return
			}
			fmt.Printf("wrote JSON  : %s (%d results)\n", *outFile, len(all))
		} else {
			fmt.Println(string(data))
		}
	}
}
