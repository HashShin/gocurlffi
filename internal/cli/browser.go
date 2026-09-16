package cli

// The browser subcommands: get --render / open, serve and mcp. They are built
// on the pure-Go browser package, which fetches with real browser TLS/HTTP
// fingerprints, runs the page's JavaScript and prints the rendered result.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"gocurlffi/browser"
	"gocurlffi/server"
)

// browserFlags are the page-loading options every browser subcommand shares, so
// serve, mcp and get --render cannot drift apart. Register one of these on a
// flag set rather than declaring the options again.
type browserFlags struct {
	impersonate string
	proxy       string
	noJS        bool
	obeyRobots  bool
	adblock     bool
	debug       bool
}

func (b *browserFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&b.impersonate, "i", "", "TLS/HTTP fingerprint target (default: native)")
	fs.StringVar(&b.impersonate, "impersonate", "", "alias for -i")
	fs.StringVar(&b.proxy, "proxy", "", "route requests through this proxy URL")
	fs.BoolVar(&b.noJS, "no-js", false, "do not run the page's JavaScript")
	fs.BoolVar(&b.obeyRobots, "obey-robots", false, "honour robots.txt")
	fs.BoolVar(&b.adblock, "adblock", false, "block ads and trackers")
	fs.BoolVar(&b.debug, "debug", false, "log page-load phases to stderr")
}

// apply fills the load options the browser path always shares. Timeouts are set
// by the caller, which owns the flags for them.
func (b *browserFlags) apply(opts *browser.Options) {
	runScripts := !b.noJS
	opts.Impersonate = b.impersonate
	opts.Proxy = b.proxy
	opts.RunScripts = &runScripts
	opts.ObeyRobots = b.obeyRobots
	opts.Adblock = b.adblock
	opts.Debug = b.debug
}

// RunServe starts the CDP server so Puppeteer, Playwright or chromedp can drive
// the browser over ws://host:port. Chrome DevTools Protocol and WebDriver BiDi
// are always served on the same port.
func RunServe(args []string) int {
	fs := newFlagSet("serve", "gocurlffi serve [flags]",
		"gocurlffi serve - drive the browser over CDP and WebDriver BiDi")
	var (
		host = fs.String("host", "127.0.0.1", "interface to bind")
		port = fs.Int("port", 9222, "port to listen on")
	)
	bf := &browserFlags{}
	bf.register(fs)

	if err := parse(fs, args); err != nil {
		return checkParse(err)
	}

	opts := browser.Options{}
	bf.apply(&opts)
	b := browser.New(opts)
	defer b.Close()

	server.SetVersion(Version)
	srv := server.New(server.Config{Browser: b})
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	fmt.Fprintf(os.Stderr, "CDP server listening on ws://%s:%d\n", *host, *port)
	if err := srv.ListenAndServe(ctx, *host, *port); err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "serve error: %v\n", err)
		return exitError
	}
	return exitOK
}

// RunMCP starts the MCP tool server, over stdio by default or HTTP with --port.
func RunMCP(args []string) int {
	fs := newFlagSet("mcp", "gocurlffi mcp [flags]",
		"gocurlffi mcp - expose the browser as Model Context Protocol tools")
	var (
		host = fs.String("host", "127.0.0.1", "interface to bind for the HTTP transport")
		port = fs.Int("port", 0, "serve over HTTP on this port (0 = stdio)")
	)
	bf := &browserFlags{}
	bf.register(fs)

	if err := parse(fs, args); err != nil {
		return checkParse(err)
	}

	opts := browser.Options{}
	bf.apply(&opts)
	b := browser.New(opts)
	defer b.Close()

	server.SetVersion(Version)
	s := server.NewMCP(b)

	if *port == 0 {
		if err := s.ServeStdio(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "mcp: %v\n", err)
			return exitError
		}
		return exitOK
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.ServeHTTP)
	mux.HandleFunc("/mcp/", s.ServeHTTP)
	srv := &http.Server{Addr: fmt.Sprintf("%s:%d", *host, *port), Handler: mux}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() { <-ctx.Done(); _ = srv.Close() }()

	fmt.Fprintf(os.Stderr, "MCP server listening on http://%s:%d/mcp\n", *host, *port)
	if err := srv.ListenAndServe(); err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "mcp error: %v\n", err)
		return exitError
	}
	return exitOK
}

// browserGetFlags is the extra command line for a one-shot page load. The
// browserFlags above are embedded so the load options are shared, and the rest
// cover extraction, screenshots and page interaction.
type browserGetFlags struct {
	browserFlags

	output      string
	format      string
	eval        string
	xpath       string
	wait        string
	waitTimeout time.Duration
	waitUntil   string
	waitMs      time.Duration
	waitScript  string
	timeout     time.Duration
	loadTimeout time.Duration
	timerBudget time.Duration
	screenshot  string
	pdfOut      string
	width       int
	scale       float64
	maxHeight   int
	showConsole bool
	showStatus  bool
	listSheets  bool
	noImages    bool

	headers stringList
	blocks  stringList
	clicks  stringList
	types   stringList
	fills   stringList
	selects stringList
}

// RunBrowserGet loads one URL in the browser and prints the result.
func RunBrowserGet(args []string) int {
	fs := newFlagSet("get --render", "gocurlffi get <url> --render [flags]",
		"gocurlffi open - load a URL in the pure-Go browser and extract from it")
	g := &browserGetFlags{}
	g.register(fs)

	if err := parse(fs, args); err != nil {
		return checkParse(err)
	}

	target := fs.Arg(0)
	if target == "" {
		fmt.Fprintln(os.Stderr, "error: missing URL")
		fs.Usage()
		return exitUsage
	}
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "https://" + target
	}

	opts := browser.Options{
		Timeout:     g.timeout,
		LoadTimeout: g.loadTimeout,
		TimerBudget: g.timerBudget,
		WaitUntil:   g.waitUntil,
	}
	g.apply(&opts)
	if rc := g.finishOptions(&opts); rc != exitOK {
		return rc
	}

	b := browser.New(opts)
	defer b.Close()

	p, err := b.Open(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitError
	}
	if g.showStatus && p.Response() != nil {
		fmt.Fprintf(os.Stderr, "status: %d %s\n", p.Response().StatusCode, p.Response().Reason)
	}

	// --wait-until networkidle0 drains what the load left pending. It is a
	// separate step rather than a load mode because the load already ran.
	if strings.EqualFold(g.waitUntil, "networkidle0") || strings.EqualFold(g.waitUntil, "networkidle") {
		p.WaitForNetworkIdle(500*time.Millisecond, g.timeout)
	}
	if rc := g.waitFor(p); rc != exitOK {
		return rc
	}
	if rc := g.drive(p); rc != exitOK {
		return rc
	}
	if g.listSheets {
		reportStyleSheets(p, float64(g.width))
	}
	if done, rc := g.writeArtifacts(p); done {
		return rc
	}
	return g.extract(p)
}

func (g *browserGetFlags) register(fs *flag.FlagSet) {
	g.browserFlags.register(fs)

	fs.StringVar(&g.output, "o", "", "write the output to this file")
	fs.StringVar(&g.output, "output", "", "alias for -o")
	fs.StringVar(&g.format, "f", "html", "output format: html, markdown, text, links or structured")
	fs.StringVar(&g.format, "format", "html", "alias for -f")
	fs.StringVar(&g.eval, "eval", "", "evaluate this JavaScript after load and print the result")
	fs.StringVar(&g.xpath, "xpath", "", "evaluate this XPath expression and print the matched text")
	fs.StringVar(&g.wait, "wait", "", "wait for this selector before extracting")
	fs.DurationVar(&g.waitTimeout, "wait-timeout", 10*time.Second, "timeout for --wait and --wait-script")
	fs.StringVar(&g.waitUntil, "wait-until", "load", "how far the load goes: load, domcontentloaded or networkidle0")
	fs.DurationVar(&g.waitMs, "wait-ms", 0, "keep running the page's timers for this long after load")
	fs.StringVar(&g.waitScript, "wait-script", "", "evaluate this JavaScript repeatedly until it is truthy")
	fs.DurationVar(&g.timeout, "timeout", 30*time.Second, "per-request timeout")
	fs.DurationVar(&g.loadTimeout, "load-timeout", 30*time.Second, "script-loading budget per page")
	fs.DurationVar(&g.timerBudget, "timer-budget", 2*time.Second, "wait for pending timers after load")
	fs.StringVar(&g.screenshot, "screenshot", "", "render the page to a PNG at this path")
	fs.StringVar(&g.pdfOut, "pdf", "", "render the page to a PDF at this path")
	fs.IntVar(&g.width, "width", 1280, "screenshot layout width in pixels")
	fs.Float64Var(&g.scale, "scale", 1, "screenshot scale factor")
	fs.IntVar(&g.maxHeight, "max-height", 20000, "screenshot height cap in pixels")
	fs.BoolVar(&g.noImages, "no-images", false, "do not draw the page's images (faster, text only)")
	fs.BoolVar(&g.showConsole, "console", false, "print page console output to stderr")
	fs.BoolVar(&g.showStatus, "status", false, "print the HTTP status to stderr")
	fs.BoolVar(&g.listSheets, "sheets", false, "report the page's own stylesheets on stderr")
	fs.Var(&g.headers, "H", "extra request header as \"Name: Value\" (repeatable)")
	fs.Var(&g.headers, "header", "alias for -H")
	fs.Var(&g.blocks, "block", "block requests matching this glob (repeatable)")
	fs.Var(&g.clicks, "click", "click the matching element (repeatable)")
	fs.Var(&g.types, "type", "type text into a control as \"selector=text\" (repeatable)")
	fs.Var(&g.fills, "fill", "set a control's value as \"selector=value\" (repeatable)")
	fs.Var(&g.selects, "select", "choose an option as \"selector=value\" (repeatable)")
}

// finishOptions adds the options that come from repeatable flags or need
// validation. It returns a non-zero status when an argument is malformed.
func (g *browserGetFlags) finishOptions(opts *browser.Options) int {
	if len(g.blocks) > 0 {
		patterns := append([]string(nil), g.blocks...)
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
	if len(g.headers) > 0 {
		opts.Headers = make(map[string]string, len(g.headers))
		for _, h := range g.headers {
			k, v, ok := strings.Cut(h, ":")
			if !ok {
				fmt.Fprintf(os.Stderr, "error: bad header %q, want \"K: V\"\n", h)
				return exitUsage
			}
			opts.Headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	if g.showConsole {
		opts.Console = func(level, message string) {
			fmt.Fprintf(os.Stderr, "[console.%s] %s\n", level, message)
		}
	}
	return exitOK
}

// waitFor runs the waits in the order they are most useful: let timers settle,
// then poll for the page's own readiness signal, then for a specific element.
func (g *browserGetFlags) waitFor(p *browser.Page) int {
	if g.waitMs > 0 {
		p.WaitForTime(g.waitMs)
	}
	if g.waitScript != "" {
		if err := p.WaitForScript(g.waitScript, g.waitTimeout); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		}
	}
	if g.wait != "" {
		if p.WaitForSelector(g.wait, g.waitTimeout) == nil {
			fmt.Fprintf(os.Stderr, "warning: selector %q not found within %s\n", g.wait, g.waitTimeout)
		}
	}
	return exitOK
}

// drive performs the scripted interactions, in the order the flags appear, so a
// flow can be scripted from the command line.
func (g *browserGetFlags) drive(p *browser.Page) int {
	for _, sel := range g.clicks {
		if err := p.Click(sel); err != nil {
			fmt.Fprintf(os.Stderr, "warning: click %s: %v\n", sel, err)
		}
	}
	sets := []struct {
		flag  string
		items stringList
		set   func(sel, value string) error
	}{
		{"type", g.types, p.Type},
		{"fill", g.fills, p.Fill},
		{"select", g.selects, p.Select},
	}
	for _, s := range sets {
		for _, kv := range s.items {
			sel, value, ok := strings.Cut(kv, "=")
			if !ok {
				fmt.Fprintf(os.Stderr, "error: --%s wants \"selector=value\", got %q\n", s.flag, kv)
				return exitUsage
			}
			if err := s.set(sel, value); err != nil {
				fmt.Fprintf(os.Stderr, "warning: %s %s: %v\n", s.flag, sel, err)
			}
		}
	}
	return exitOK
}

// writeArtifacts handles --pdf and --screenshot. Each ends the command because
// the file is the output, so done reports that the caller should stop. Without
// one of those flags it writes nothing and leaves the exit status alone.
func (g *browserGetFlags) writeArtifacts(p *browser.Page) (done bool, code int) {
	shots := browser.ScreenshotOptions{
		Width: g.width, Scale: g.scale, MaxHeight: g.maxHeight, NoImages: g.noImages,
	}
	if g.pdfOut != "" {
		data, err := p.PDF(shots)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pdf error: %v\n", err)
			return true, exitError
		}
		if err := os.WriteFile(g.pdfOut, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error writing %s: %v\n", g.pdfOut, err)
			return true, exitError
		}
		fmt.Fprintf(os.Stderr, "wrote %d bytes to %s\n", len(data), g.pdfOut)
		return true, exitOK
	}
	if g.screenshot == "" {
		return false, exitOK
	}
	png, err := p.Screenshot(shots)
	if err != nil {
		fmt.Fprintf(os.Stderr, "screenshot error: %v\n", err)
		return true, exitError
	}
	if g.output == "" {
		g.output = g.screenshot
	}
	if err := os.WriteFile(g.output, png, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", g.output, err)
		return true, exitError
	}
	fmt.Fprintf(os.Stderr, "wrote %d bytes to %s\n", len(png), g.output)
	if g.debug {
		// The layout this render used, to compare against a browser's
		// getBoundingClientRect (tools/cssdiff/geom).
		if outline, err := p.RenderOutline(shots, 5000); err == nil {
			for _, ln := range outline {
				fmt.Fprintln(os.Stderr, "[layout] "+ln)
			}
		}
	}
	return true, exitOK
}

// extract prints the selected representation of the page, or writes it to
// --output. --eval and --xpath take precedence over --format.
func (g *browserGetFlags) extract(p *browser.Page) int {
	var out string
	switch {
	case g.eval != "":
		v, err := p.Eval(g.eval)
		if err != nil {
			fmt.Fprintf(os.Stderr, "eval error: %v\n", err)
			return exitError
		}
		if v != nil && v.Export() != nil {
			out = fmt.Sprintf("%v", v.Export())
		}
	case g.xpath != "":
		var sb strings.Builder
		for _, n := range p.QueryXPath(g.xpath) {
			sb.WriteString(p.TextOf(n))
			sb.WriteByte('\n')
		}
		out = sb.String()
	default:
		switch g.format {
		case "markdown", "md":
			out = p.Markdown()
		case "text", "txt":
			out = p.Text()
		case "structured", "jsonld", "ld+json":
			b, _ := json.MarshalIndent(p.StructuredData(), "", "  ")
			out = string(b)
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
	if g.output != "" {
		if err := os.WriteFile(g.output, []byte(out), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error writing %s: %v\n", g.output, err)
			return exitError
		}
		return exitOK
	}
	fmt.Print(out)
	return exitOK
}

// reportStyleSheets is diagnostic, so it writes to stderr and does not stop the
// rest of the command: "get URL --screenshot page.png --sheets" writes the PNG
// and lists the stylesheets it was rendered with.
func reportStyleSheets(p *browser.Page, width float64) {
	sheets := p.StyleSheetsAt(width)
	if len(sheets) == 0 {
		fmt.Fprintln(os.Stderr, "this page declares no stylesheets")
		return
	}
	// Rule counts depend on the width, since @media is evaluated while
	// parsing; say which width these were counted at.
	fmt.Fprintf(os.Stderr, "stylesheets (rule counts at width %g):\n", sheets[0].Width)
	for _, s := range sheets {
		url := s.Href
		if url == "" {
			url = "inline <style>"
		}
		if s.Err != "" {
			fmt.Fprintf(os.Stderr, "NOT APPLIED  %-60s %s\n", url, s.Err)
			continue
		}
		fmt.Fprintf(os.Stderr, "applied      %-60s %d rules, %d font faces, %d bytes\n",
			url, s.Rules, s.Fonts, s.Bytes)
	}
}
