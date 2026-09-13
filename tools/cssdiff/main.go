// Command cssdiff compares the Go browser's cascaded styles with a real
// Chromium, element by element, using the same page in both.
//
// It exists because "the CSS did not load" is not something to argue about: it
// is a set of computed values to compare. Chromium is driven over CDP with
// getComputedStyle; the Go browser answers with its own getComputedStyle
// through `gobrowser get --eval`. Every fidelity fix in browser/style.go was
// found and verified with this tool.
//
// This is a separate module on purpose: it is a development tool, and its only
// dependency (chromedp) must not become a dependency of the browser package,
// which stays pure Go with no cgo.
//
// Usage:
//
//	cd tools/cssdiff
//	go mod download          # once, needs network
//	go run . <url>
//
// It prints the URL to open in Chromium, then the comparison once both dumps
// exist. Flags:
//
//	-chromium BIN   Chromium binary (default chromium-browser)
//	-go BIN         gobrowser binary to use (default: built into a temp dir)
//	-wait DUR       how long Chromium waits after load (default 6s)
//	-impersonate T  gobrowser TLS fingerprint (default custom)
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// dumpJS reads the same seven properties from every element, in document
// order, on both sides.
const dumpJS = `(function(){
  var out=[];
  var els=document.querySelectorAll('*');
  function pathOf(el){
    var p=[];
    while(el && el.nodeType===1){
      var n=0, s=el;
      while(s){ s=s.previousElementSibling; if(s) n++; }
      p.unshift(n);
      el=el.parentElement;
    }
    return p.join('/');
  }
  for(var i=0;i<els.length;i++){
    var el=els[i];
    var cs=getComputedStyle(el);
    out.push({
      i:i,
      path:pathOf(el),
      tag:el.tagName.toLowerCase(),
      id:el.id||'',
      cls:el.getAttribute('class')||'',
      text:(el.textContent||'').trim().slice(0,40),
      display:cs.getPropertyValue('display'),
      color:cs.getPropertyValue('color'),
      bg:cs.getPropertyValue('background-color'),
      fs:cs.getPropertyValue('font-size'),
      fw:cs.getPropertyValue('font-weight'),
      fst:cs.getPropertyValue('font-style'),
      ta:cs.getPropertyValue('text-align')
    });
  }
  return JSON.stringify(out);
})()`

type element struct {
	Index   int    `json:"i"`
	Path    string `json:"path"`
	Tag     string `json:"tag"`
	ID      string `json:"id"`
	Class   string `json:"cls"`
	Text    string `json:"text"`
	Display string `json:"display"`
	Color   string `json:"color"`
	BG      string `json:"bg"`
	FS      string `json:"fs"`
	FW      string `json:"fw"`
	FST     string `json:"fst"`
	TA      string `json:"ta"`
}

func main() {
	var (
		chromiumBin = flag.String("chromium", "", "Chromium binary")
		goBin       = flag.String("go", "", "gobrowser binary (default: go run ../..)")
		wait        = flag.Duration("wait", 6*time.Second, "wait after load in Chromium")
		impersonate = flag.String("impersonate", "custom", "gobrowser impersonation target")
		keep        = flag.Bool("keep", false, "keep the two JSON dumps")
	)
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: cssdiff [flags] <url>")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	url := flag.Arg(0)

	bin := *chromiumBin
	if bin == "" {
		for _, c := range []string{"chromium-browser", "chromium", "google-chrome", "chrome"} {
			if p, err := exec.LookPath(c); err == nil {
				bin = p
				break
			}
		}
	}
	if bin == "" {
		fatal("no Chromium found; pass -chromium")
	}

	dir, err := os.MkdirTemp("", "cssdiff")
	if err != nil {
		fatal("temp dir: %v", err)
	}
	if !*keep {
		defer os.RemoveAll(dir)
	}

	fmt.Fprintln(os.Stderr, "chromium:", bin)
	chrome, err := dumpChromium(bin, url, *wait)
	if err != nil {
		fatal("chromium: %v", err)
	}
	ours, err := dumpGoBrowser(*goBin, *impersonate, url, filepath.Join(dir, "ours.json"))
	if err != nil {
		fatal("gobrowser: %v", err)
	}
	if *keep {
		writeJSON(filepath.Join(dir, "chromium.json"), chrome)
		fmt.Fprintln(os.Stderr, "dumps kept in", dir)
	}
	report(url, chrome, ours)
}

func dumpChromium(bin, url string, wait time.Duration) ([]element, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(bin),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.WindowSize(1280, 900),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, 4*time.Minute)
	defer cancelTimeout()

	var raw string
	if err := chromedp.Run(ctx, chromedp.Navigate(url), chromedp.Sleep(wait),
		chromedp.Evaluate(dumpJS, &raw)); err != nil {
		return nil, err
	}
	var out []element
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// dumpGoBrowser runs the Go browser with the same script. It builds it from
// the repository root first, since this tool is a separate module and cannot
// `go run` a package outside itself.
func dumpGoBrowser(bin, impersonate, url, outPath string) ([]element, error) {
	if bin == "" {
		root, err := repoRoot()
		if err != nil {
			return nil, err
		}
		bin = filepath.Join(os.TempDir(), "cssdiff-gobrowser")
		build := exec.Command("go", "build", "-o", bin, "./cmd/gobrowser")
		build.Dir = root
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			return nil, fmt.Errorf("build gobrowser: %w", err)
		}
	}
	cmd := exec.Command(bin, "get", url, "-i", impersonate, "--eval", dumpJS, "-o", outPath)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(outPath)
	if err != nil {
		return nil, err
	}
	var out []element
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse gobrowser output: %w", err)
	}
	return out, nil
}

// repoRoot walks up from the working directory to the gocurlffi module.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(b), "module gocurlffi") {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("gocurlffi module not found above %s", dir)
		}
		dir = parent
	}
}

func writeJSON(path string, v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
	}
}

// --- comparison ---

var fields = []struct {
	name string
	norm func(string) string
	get  func(element) string
}{
	{"font-size", normSize, func(e element) string { return e.FS }},
	{"color", normColor, func(e element) string { return e.Color }},
	{"background", normColor, func(e element) string { return e.BG }},
	{"font-weight", normWeight, func(e element) string { return e.FW }},
	{"font-style", normWord, func(e element) string { return e.FST }},
	{"display", normWord, func(e element) string { return e.Display }},
	{"text-align", normAlign, func(e element) string { return e.TA }},
}

func report(url string, chrome, ours []element) {
	fmt.Printf("url: %s\n", url)
	fmt.Printf("elements: chromium %d, gobrowser %d\n", len(chrome), len(ours))

	cg, og := map[string][]element{}, map[string][]element{}
	for _, e := range chrome {
		cg[key(e)] = append(cg[key(e)], e)
	}
	for _, e := range ours {
		og[key(e)] = append(og[key(e)], e)
	}
	var common []string
	for k := range cg {
		if _, ok := og[k]; ok {
			common = append(common, k)
		}
	}
	total := 0
	for _, k := range common {
		a, b := cg[k], og[k]
		if len(a) > len(b) {
			a = a[:len(b)]
		}
		total += len(a)
	}
	fmt.Printf("comparable elements: %d (chromium-only keys %d, gobrowser-only keys %d)\n",
		total, len(cg)-len(common), len(og)-len(common))

	bad := 0
	for _, f := range fields {
		mismatch := 0
		shown := 0
		for _, k := range common {
			a, b := cg[k], og[k]
			if len(a) > len(b) {
				a = a[:len(b)]
			}
			for i := range a {
				if f.norm(f.get(a[i])) == f.norm(f.get(b[i])) {
					continue
				}
				mismatch++
				if shown < 5 {
					shown++
					fmt.Printf("  %s: %s#%s.%s chromium %q gobrowser %q %q\n",
						f.name, a[i].Tag, a[i].ID, truncate(a[i].Class, 24),
						f.norm(f.get(a[i])), f.norm(f.get(b[i])), truncate(a[i].Text, 24))
				}
			}
		}
		pct := 0.0
		if total > 0 {
			pct = 100 * float64(mismatch) / float64(total)
		}
		if mismatch > 0 {
			bad++
		}
		fmt.Printf("  %-12s %5d/%d differ (%.1f%%)\n", f.name, mismatch, total, pct)
	}
	if bad == 0 {
		fmt.Println("all compared properties agree")
	}
}

// key identifies an element by its position in the tree, falling back to
// tag/id/class for older dumps. Position matters: a page has hundreds of
// elements with the same tag and no class, and pairing those by their computed
// values instead would report differences that do not exist.
func key(e element) string {
	if e.Path != "" {
		return e.Tag + "@" + e.Path
	}
	return e.Tag + "\x00" + e.ID + "\x00" + e.Class
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n]
	}
	return s
}

func normWord(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	return s
}

func normSize(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "px")
	if f, err := parseFloat(s); err == nil {
		return fmt.Sprintf("%.1f", f)
	}
	return s
}

func normColor(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "none"
	}
	nums := numbers(s)
	switch len(nums) {
	case 3:
		return fmt.Sprintf("rgb(%d,%d,%d)", int(nums[0]), int(nums[1]), int(nums[2]))
	case 4:
		if nums[3] == 0 {
			return "transparent"
		}
		return fmt.Sprintf("rgb(%d,%d,%d)", int(nums[0]), int(nums[1]), int(nums[2]))
	}
	return s
}

func normWeight(s string) string {
	switch strings.TrimSpace(s) {
	case "normal", "400":
		return "400"
	case "bold", "700":
		return "700"
	}
	return strings.TrimSpace(s)
}

func normAlign(s string) string {
	s = strings.TrimSpace(s)
	switch s {
	case "start", "-webkit-left":
		return "left"
	case "end", "-webkit-right":
		return "right"
	case "-webkit-center":
		return "center"
	}
	return s
}

func numbers(s string) []float64 {
	var out []float64
	cur := strings.Builder{}
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		f, err := parseFloat(cur.String())
		if err == nil {
			out = append(out, f)
		}
		cur.Reset()
	}
	for _, r := range s {
		if r >= '0' && r <= '9' || r == '.' {
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%g", &f)
	return f, err
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "cssdiff: "+format+"\n", args...)
	os.Exit(1)
}
