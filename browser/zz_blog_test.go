package browser

import (
	"fmt"
	"testing"
	"time"
)

func TestZZBlog(t *testing.T) {
	b := New(Options{Impersonate: "chrome", LoadTimeout: 30 * time.Second})
	defer b.Close()
	p, err := b.Open("https://go.dev/blog/go1.24")
	if err != nil {
		t.Fatal(err)
	}
	doc, _, err := p.layOut(ScreenshotOptions{Width: 1200, MaxHeight: 60000})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("height", doc.height)
	for _, l := range doc.lines {
		var txt string
		for _, r := range l.runs {
			txt += r.text
		}
		if len(txt) > 3 {
			if len(txt) > 45 {
				txt = txt[:45]
			}
			fmt.Printf("y=%7.0f x=%6.0f h=%4.0f %q\n", l.y, l.runs[0].x, l.height, txt)
		}
	}
}
