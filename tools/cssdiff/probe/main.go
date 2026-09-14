// Command probe evaluates a JavaScript expression in a real Chromium and
// prints the result. It answers small layout questions ("what does
// grid-column: main-content resolve to?") that geometry dumps cannot.
//
// Usage:
//
//	cd tools/cssdiff
//	go run ./probe -url https://brave.com/ -js "1+1"
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

func main() {
	url := flag.String("url", "about:blank", "url to load")
	js := flag.String("js", "1", "expression to evaluate")
	width := flag.Int("width", 1200, "viewport width")
	ua := flag.String("ua", "", "user agent override")
	nojs := flag.Bool("nojs", false, "disable JavaScript, to see the no-JS layout a page falls back to")
	flag.Parse()

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.WindowSize(*width, 1000),
	)
	if *ua != "" {
		opts = append(opts, chromedp.Flag("user-agent", *ua))
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, 3*time.Minute)
	defer cancelTimeout()

	var out string
	actions := []chromedp.Action{chromedp.Navigate(*url), chromedp.Sleep(3 * time.Second)}
	if *nojs {
		actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
			return emulation.SetScriptExecutionDisabled(true).Do(ctx)
		}))
		actions = append(actions, chromedp.Navigate(*url), chromedp.Sleep(2*time.Second))
	}
	actions = append(actions, chromedp.Evaluate(*js, &out))
	if err := chromedp.Run(ctx, actions...); err != nil {
		fmt.Fprintln(os.Stderr, "chromedp:", err)
		os.Exit(1)
	}
	fmt.Println(out)
}
