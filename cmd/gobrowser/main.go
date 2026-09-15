// Command gobrowser is a small headless-browser CLI built on the pure-Go
// browser package. It fetches a page with real browser TLS/HTTP fingerprints,
// runs its JavaScript, and prints the rendered result.
package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"gocurlffi/browser"
	"gocurlffi/impersonate"
)

// version is the commit the binary was built from, set by the Makefile with
// -ldflags. It is printed with --debug so that a stale build is visible: an
// older binary that misses a cascade fix produces the same symptoms as a bug.
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "get", "fetch", "open":
		runGet(args)
	case "list", "targets":
		for _, t := range impersonate.Targets() {
			fmt.Println(t)
		}
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `gobrowser - pure-Go headless browser

usage:
  gobrowser get <url> [flags]
  gobrowser list

flags:
  -i, --impersonate NAME   TLS/HTTP fingerprint target (chrome, chrome131, custom, ...)
  -o, --output FILE        write output to FILE instead of stdout
  -f, --format FORMAT      html (default), markdown, text, links
      --eval JS            evaluate JS after load and print the result
      --wait SELECTOR      wait for a selector before extracting
      --wait-timeout DUR   timeout for --wait (default 10s)
      --timeout DUR        per-request timeout (default 30s)
      --load-timeout DUR   script-loading budget per page (default 30s)
      --timer-budget DUR   wait for pending timers after load (default 2s)
      --screenshot FILE    render the page to a PNG (text layout, no images)
      --width N            screenshot layout width (default 1280)
      --scale F            screenshot scale factor (default 1)
      --max-height N       screenshot height cap (default 20000)
      --no-images          do not draw the page's pictures (faster, text only)
      --no-js              disable JavaScript execution
      --console            print page console output to stderr
      --status             print HTTP status to stderr
      --sheets             report the page's own stylesheets on stderr (keeps going)
      --xpath EXPR         evaluate an XPath expression and print matched text
      --proxy URL          route requests through a proxy
      --obey-robots        honour robots.txt
      --block GLOB         block requests matching GLOB (repeatable)
      --click SELECTOR     click the matching element (repeatable)
      --type SEL=TEXT      type text into a control (repeatable)
      --fill SEL=VALUE     set a control's value (repeatable)
      --select SEL=VALUE   choose an option (repeatable)
      --debug              log page-load phases to stderr
  -H, --header "K: V"      extra header to send (repeatable)
`)
}

func runGet(args []string) {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	var (
		impersonate  = fs.String("i", "", "impersonate target")
		impersonateL = fs.String("impersonate", "", "impersonate target")
		output       = fs.String("o", "", "output file")
		outputL      = fs.String("output", "", "output file")
		format       = fs.String("f", "html", "output format")
		formatL      = fs.String("format", "html", "output format")
		eval         = fs.String("eval", "", "JS to evaluate after load")
		wait         = fs.String("wait", "", "selector to wait for")
		waitTimeout  = fs.Duration("wait-timeout", 10*time.Second, "selector wait timeout")
		timeout      = fs.Duration("timeout", 30*time.Second, "request timeout")
		loadTimeout  = fs.Duration("load-timeout", 30*time.Second, "script-loading budget")
		timerBudget  = fs.Duration("timer-budget", 2*time.Second, "wait for pending timers after load")
		screenshot   = fs.String("screenshot", "", "render the page to a PNG file")
		width        = fs.Int("width", 1280, "screenshot layout width in px")
		scale        = fs.Float64("scale", 1, "screenshot scale factor")
		maxHeight    = fs.Int("max-height", 20000, "screenshot height cap in px")
		noJS         = fs.Bool("no-js", false, "disable JavaScript")
		showConsole  = fs.Bool("console", false, "print console output")
		showStatus   = fs.Bool("status", false, "print HTTP status")
		listSheets   = fs.Bool("sheets", false, "report the page's stylesheets")
		noImages     = fs.Bool("no-images", false, "do not draw the page's pictures")
		debug        = fs.Bool("debug", false, "log page-load phases to stderr")
		proxy        = fs.String("proxy", "", "route requests through a proxy URL")
		obeyRobots   = fs.Bool("obey-robots", false, "honour robots.txt")
		xpath        = fs.String("xpath", "", "evaluate an XPath expression and print matched text")
	)
	var headers headerList
	fs.Var(&headers, "H", "extra header \"K: V\" (repeatable)")
	var blocks stringList
	fs.Var(&blocks, "block", "block requests matching a glob (repeatable)")
	var clicks stringList
	fs.Var(&clicks, "click", "click the element matching a selector (repeatable)")
	var types stringList
	fs.Var(&types, "type", "type text into a control as \"selector=text\" (repeatable)")
	var fills stringList
	fs.Var(&fills, "fill", "set a control's value as \"selector=value\" (repeatable)")
	var selects stringList
	fs.Var(&selects, "select", "choose an option as \"selector=value\" (repeatable)")
	_ = fs.Parse(reorderFlags(args, boolFlagNames(fs)))

	target := fs.Arg(0)
	if target == "" {
		fmt.Fprintln(os.Stderr, "error: url required")
		os.Exit(2)
	}
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "https://" + target
	}
	if *impersonate == "" {
		*impersonate = *impersonateL
	}
	if *output == "" {
		*output = *outputL
	}
	if *format == "html" && *formatL != "html" {
		*format = *formatL
	}

	if *debug {
		fmt.Fprintf(os.Stderr, "gobrowser %s\n", version)
	}
	runScripts := !*noJS
	opts := browser.Options{
		Impersonate: *impersonate,
		Timeout:     *timeout,
		LoadTimeout: *loadTimeout,
		TimerBudget: *timerBudget,
		RunScripts:  &runScripts,
		Debug:       *debug,
		Proxy:       *proxy,
		ObeyRobots:  *obeyRobots,
	}
	if len(blocks) > 0 {
		patterns := append([]string(nil), blocks...)
		opts.Intercept = func(r *browser.Request) *browser.Response {
			for _, pat := range patterns {
				if ok, _ := path.Match(pat, r.URL); ok {
					return browser.Block()
				}
				if u, err := url.Parse(r.URL); err == nil {
					if ok, _ := path.Match(pat, u.Path); ok {
						return browser.Block()
					}
				}
			}
			return nil
		}
	}
	if len(headers) > 0 {
		opts.Headers = map[string]string{}
		for _, h := range headers {
			k, v, ok := strings.Cut(h, ":")
			if !ok {
				fmt.Fprintf(os.Stderr, "error: bad header %q, want \"K: V\"\n", h)
				os.Exit(2)
			}
			opts.Headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	if *showConsole {
		opts.Console = func(level, message string) {
			fmt.Fprintf(os.Stderr, "[console.%s] %s\n", level, message)
		}
	}

	b := browser.New(opts)
	defer b.Close()

	p, err := b.Open(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if *showStatus && p.Response() != nil {
		fmt.Fprintf(os.Stderr, "status: %d %s\n", p.Response().StatusCode, p.Response().Reason)
	}
	if *wait != "" {
		if p.WaitForSelector(*wait, *waitTimeout) == nil {
			fmt.Fprintf(os.Stderr, "warning: selector %q not found within %s\n", *wait, *waitTimeout)
		}
	}

	// Drive the page before extracting: click, type, fill and select run in
	// the order their flags appear, so a flow can be scripted from the CLI.
	for _, sel := range clicks {
		if err := p.Click(sel); err != nil {
			fmt.Fprintf(os.Stderr, "warning: click %s: %v\n", sel, err)
		}
	}
	for _, kv := range types {
		sel, text, ok := strings.Cut(kv, "=")
		if !ok {
			fmt.Fprintf(os.Stderr, "error: --type wants \"selector=text\", got %q\n", kv)
			os.Exit(2)
		}
		if err := p.Type(sel, text); err != nil {
			fmt.Fprintf(os.Stderr, "warning: type %s: %v\n", sel, err)
		}
	}
	for _, kv := range fills {
		sel, value, ok := strings.Cut(kv, "=")
		if !ok {
			fmt.Fprintf(os.Stderr, "error: --fill wants \"selector=value\", got %q\n", kv)
			os.Exit(2)
		}
		if err := p.Fill(sel, value); err != nil {
			fmt.Fprintf(os.Stderr, "warning: fill %s: %v\n", sel, err)
		}
	}
	for _, kv := range selects {
		sel, value, ok := strings.Cut(kv, "=")
		if !ok {
			fmt.Fprintf(os.Stderr, "error: --select wants \"selector=value\", got %q\n", kv)
			os.Exit(2)
		}
		if err := p.Select(sel, value); err != nil {
			fmt.Fprintf(os.Stderr, "warning: select %s: %v\n", sel, err)
		}
	}

	// --sheets is diagnostic, so it reports on stderr and does not stop the
	// rest of the command: "get URL --screenshot page.png --sheets" writes the
	// PNG and lists the stylesheets it was rendered with.
	if *listSheets {
		sheets := p.StyleSheetsAt(float64(*width))
		if len(sheets) == 0 {
			fmt.Fprintln(os.Stderr, "this page declares no stylesheets")
		} else {
			// Rule counts depend on the width, since @media is evaluated while
			// parsing; say which width these were counted at.
			fmt.Fprintf(os.Stderr, "stylesheets (rule counts at width %g):\n", sheets[0].Width)
		}
		for _, s := range sheets {
			url := s.Href
			if url == "" {
				url = "inline <style>"
			}
			if s.Err != "" {
				fmt.Fprintf(os.Stderr, "NOT APPLIED  %-60s %s\n", url, s.Err)
				continue
			}
			fmt.Fprintf(os.Stderr, "applied      %-60s %d rules, %d font faces, %d bytes\n", url, s.Rules, s.Fonts, s.Bytes)
		}
	}

	if *screenshot != "" {
		png, err := p.Screenshot(browser.ScreenshotOptions{
			Width:     *width,
			Scale:     *scale,
			MaxHeight: *maxHeight,
			NoImages:  *noImages,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "screenshot error: %v\n", err)
			os.Exit(1)
		}
		if *output == "" {
			*output = *screenshot
		}
		if err := os.WriteFile(*output, png, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error writing %s: %v\n", *output, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "wrote %d bytes to %s\n", len(png), *output)
		if *debug {
			// The layout this render used, to compare against a browser's
			// getBoundingClientRect (tools/cssdiff/geom).
			if outline, err := p.RenderOutline(browser.ScreenshotOptions{
				Width: *width, Scale: *scale, MaxHeight: *maxHeight, NoImages: *noImages,
			}, 5000); err == nil {
				for _, ln := range outline {
					fmt.Fprintln(os.Stderr, "[layout] "+ln)
				}
			}
		}
		return
	}

	var out string
	if *eval != "" {
		v, err := p.Eval(*eval)
		if err != nil {
			fmt.Fprintf(os.Stderr, "eval error: %v\n", err)
			os.Exit(1)
		}
		if v != nil && !isUndefined(v.Export()) {
			out = fmt.Sprintf("%v", v.Export())
		}
	} else if *xpath != "" {
		var sb strings.Builder
		for _, n := range p.QueryXPath(*xpath) {
			sb.WriteString(p.TextOf(n))
			sb.WriteByte('\n')
		}
		out = sb.String()
	} else {
		switch *format {
		case "markdown", "md":
			out = p.Markdown()
		case "text", "txt":
			out = p.Text()
		case "links":
			var sb strings.Builder
			for _, l := range p.Links() {
				sb.WriteString(l.Href)
				if l.Text != "" {
					sb.WriteString("\t" + l.Text)
				}
				sb.WriteByte('\n')
			}
			out = sb.String()
		default:
			out = p.HTML()
		}
	}

	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	if *output != "" {
		if err := os.WriteFile(*output, []byte(out), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error writing %s: %v\n", *output, err)
			os.Exit(1)
		}
		return
	}
	fmt.Print(out)
}

func isUndefined(v any) bool { return v == nil }

// headerList collects repeatable -H "K: V" flags.
type headerList []string

// stringList is a repeatable string flag; the value is used verbatim.
type stringList = headerList

func (h *headerList) String() string { return strings.Join(*h, ", ") }

func (h *headerList) Set(v string) error {
	*h = append(*h, v)
	return nil
}

// boolFlagNames reports the flags that take no value, read from the flag set
// itself so a new boolean option never has to be registered twice. Without it,
// reorderFlags would treat the argument after a boolean flag as its value:
// "get URL --sheets -f text" tried to fetch the host "text".
func boolFlagNames(fs *flag.FlagSet) map[string]bool {
	names := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) {
		switch f.Value.String() {
		case "true", "false":
			names["-"+f.Name] = true
			names["--"+f.Name] = true
		}
	})
	return names
}

// reorderFlags moves options ahead of positional arguments so the standard
// flag package (which stops at the first non-flag) sees them, allowing
// "gobrowser get URL -f text". boolFlags holds the options that take no value.
func reorderFlags(args []string, boolFlags map[string]bool) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && !boolFlags[a] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}
