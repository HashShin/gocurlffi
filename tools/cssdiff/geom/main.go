// Command geom prints the geometry (getBoundingClientRect) of the large boxes
// on a page, read from a real Chromium over CDP.
//
// It exists to answer "why does the Go browser's screenshot look different from
// the site": the cascade can be verified with cssdiff, but layout cannot be
// compared from computed styles, since they say nothing about position. This
// gives the target geometry - how tall the hero is, how many columns a row has,
// where each section starts - so the renderer can be checked against it.
//
// Usage:
//
//	cd tools/cssdiff
//	go mod download
//	go run ./geom -min 80 -max 40 https://brave.com/
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/chromedp/chromedp"
)

const geomJS = `(function(){
  var out=[];
  var els=document.querySelectorAll('body *');
  for (var i=0;i<els.length;i++){
    var el=els[i];
    var r=el.getBoundingClientRect();
    if (r.width < 1 || r.height < 1) continue;
    var cs=getComputedStyle(el);
    out.push({
      tag: el.tagName.toLowerCase(),
      cls: (el.getAttribute('class')||'').slice(0,60),
      id: el.id||'',
      x: Math.round(r.x), y: Math.round(r.y + window.scrollY),
      w: Math.round(r.width), h: Math.round(r.height),
      display: cs.display,
      flexDir: cs.flexDirection,
      gridCols: cs.gridTemplateColumns,
      text: (el.textContent||'').trim().slice(0,40)
    });
  }
  out.push({tag:'#document', cls:'', id:'', x:0, y:0, w:window.innerWidth,
    h: document.documentElement.scrollHeight, display:'', flexDir:'', gridCols:'', text:'page height'});
  return JSON.stringify(out);
})()`

type rect struct {
	Tag      string `json:"tag"`
	Class    string `json:"cls"`
	ID       string `json:"id"`
	X        int    `json:"x"`
	Y        int    `json:"y"`
	W        int    `json:"w"`
	H        int    `json:"h"`
	Display  string `json:"display"`
	FlexDir  string `json:"flexDir"`
	GridCols string `json:"gridCols"`
	Text     string `json:"text"`
}

func main() {
	width := flag.Int("width", 900, "viewport width")
	minSize := flag.Int("min", 80, "only report boxes at least this many px wide and tall")
	limit := flag.Int("max", 60, "maximum rows to print")
	url := flag.String("url", "", "url to inspect")
	flag.Parse()
	if *url == "" {
		flag.Usage()
		os.Exit(2)
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.WindowSize(*width, 1000),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, 3*time.Minute)
	defer cancelTimeout()

	var raw string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(*url),
		chromedp.Sleep(5*time.Second),
		chromedp.Evaluate(geomJS, &raw),
	); err != nil {
		fmt.Fprintln(os.Stderr, "chromedp:", err)
		os.Exit(1)
	}
	var rects []rect
	if err := json.Unmarshal([]byte(raw), &rects); err != nil {
		fmt.Fprintln(os.Stderr, "parse:", err)
		os.Exit(1)
	}
	shown := 0
	for _, r := range rects {
		if r.Tag == "#document" {
			fmt.Printf("%-8s %-40s page height %dpx\n", "#document", "", r.H)
			continue
		}
		if r.W < *minSize || r.H < *minSize {
			continue
		}
		if shown++; shown > *limit {
			continue
		}
		layout := r.Display
		if r.FlexDir != "" && r.FlexDir != "row" && r.Display == "flex" {
			layout += "/" + r.FlexDir
		}
		if r.GridCols != "" && r.GridCols != "none" {
			layout = "grid[" + r.GridCols + "]"
		}
		fmt.Printf("y=%-6d %-7s %-8s %-34s %4dx%-5d %-18s %s\n",
			r.Y, r.Tag, layout, trunc(r.Class, 34), r.W, r.H, trunc(r.Text, 18), r.ID)
	}
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
