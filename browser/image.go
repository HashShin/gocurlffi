package browser

import (
	"bytes"
	"image"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	// Image formats a page can use. The standard three plus webp, which is what
	// most modern sites actually serve.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"

	"golang.org/x/net/html"
)

// Images are drawn into a screenshot when the page's own bytes can be fetched
// and decoded. They are optional and bounded: a screenshot of an image-heavy
// page should not turn into hundreds of requests.

const (
	// defaultMaxImages bounds how many images one screenshot will fetch.
	defaultMaxImages = 48
	// defaultMaxImageBytes bounds the total bytes kept for drawing.
	defaultMaxImageBytes = 16 << 20
	// maxImageBytesPerImage rejects a single oversized file.
	maxImageBytesPerImage = 4 << 20
	// maxImageHeight caps how tall a drawn image may be, so one enormous
	// picture cannot dominate the render.
	maxImageHeight = 900.0
)

// pageImage is a fetched image, decoded only when it is drawn.
type pageImage struct {
	src  string
	data []byte
	size image.Point // natural size, from the header
}

// imageError marks a source that must not be retried.
type imageError struct{}

// imageFor walks an <img> (or a <picture>'s <img>) and returns the best source
// the page offers: the largest srcset candidate, else src. An empty string means
// there is nothing to fetch.
func imageSource(img *html.Node) string {
	if srcset, ok := getAttr(img, "srcset"); ok {
		if best := bestSrcsetCandidate(srcset); best != "" {
			return best
		}
	}
	src, _ := getAttr(img, "src")
	return strings.TrimSpace(src)
}

// bestSrcsetCandidate picks the highest-resolution candidate from a srcset.
// Descriptors are "2x" or "640w"; the largest wins, because a screenshot is
// drawn at a known width and a bigger source looks better.
func bestSrcsetCandidate(srcset string) string {
	best, bestScore := "", -1.0
	for _, part := range strings.Split(srcset, ",") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			continue
		}
		url := fields[0]
		if url == "" || strings.HasPrefix(url, "data:") {
			continue
		}
		score := 1.0
		if len(fields) > 1 {
			d := fields[1]
			if v, err := strconv.ParseFloat(strings.TrimSuffix(d, "x"), 64); err == nil && strings.HasSuffix(d, "x") {
				score = v
			} else if v, err := strconv.ParseFloat(strings.TrimSuffix(d, "w"), 64); err == nil && strings.HasSuffix(d, "w") {
				score = v / 100
			}
		}
		if score > bestScore {
			best, bestScore = url, score
		}
	}
	return best
}

// prefetchImages fetches the images the document references, in parallel and
// within budget, so that drawing does not wait on the network image by image.
// It is a no-op unless the caller asked for images.
func (p *Page) prefetchImages(root *html.Node, maxImages, maxBytes int) {
	if root == nil || maxImages <= 0 {
		return
	}
	var srcs []string
	seen := map[string]bool{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode {
				switch c.Data {
				case "img":
					if _, ok := getAttr(c, "src"); ok || hasAttr(c, "srcset") {
						if src := imageSource(c); src != "" {
							abs := resolveURL(p.baseURL(), src)
							if !seen[abs] {
								seen[abs] = true
								srcs = append(srcs, abs)
							}
						}
					}
				case "script", "style", "template":
					continue
				}
			}
			walk(c)
		}
	}
	walk(root)
	if len(srcs) > maxImages {
		srcs = srcs[:maxImages]
	}
	if len(srcs) == 0 {
		return
	}

	p.imageMu.Lock()
	if p.images == nil {
		p.images = map[string]*pageImage{}
	}
	p.imageMu.Unlock()

	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	var budget int64
	var failed, dropped int32
	var budgetMu sync.Mutex
	for _, src := range srcs {
		wg.Add(1)
		go func(src string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			img := p.fetchImage(src)
			if img == nil {
				// A 404, an image format we cannot decode (SVG, AVIF) or a
				// failed request: the render falls back to the alt text.
				atomic.AddInt32(&failed, 1)
				return
			}
			budgetMu.Lock()
			defer budgetMu.Unlock()
			budget += int64(len(img.data))
			if budget > int64(maxBytes) {
				// Over budget: drop the bytes but keep the fact that this
				// source was seen, so it is not fetched again.
				atomic.AddInt32(&dropped, 1)
				p.imageMu.Lock()
				p.images[src] = &pageImage{src: src}
				p.imageMu.Unlock()
				return
			}
			p.imageMu.Lock()
			p.images[src] = img
			p.imageMu.Unlock()
		}(src)
	}
	wg.Wait()
	p.debugf("images: %d referenced, %d drawn, %d unusable (svg/avif/404), %d over budget",
		len(srcs), p.imageCount(), atomic.LoadInt32(&failed), atomic.LoadInt32(&dropped))
}

func (p *Page) imageCount() int {
	p.imageMu.Lock()
	defer p.imageMu.Unlock()
	n := 0
	for _, img := range p.images {
		if img != nil && len(img.data) > 0 {
			n++
		}
	}
	return n
}

// fetchImage downloads and header-decodes one image. The body is decoded only
// when the image is drawn, so a page of images costs one decode each at most.
func (p *Page) fetchImage(src string) *pageImage {
	if p.browser == nil || !strings.HasPrefix(src, "http") {
		return nil
	}
	resp, err := p.browser.get(src, nil)
	if err != nil || resp == nil || resp.StatusCode >= 400 {
		return nil
	}
	if len(resp.Content) == 0 || len(resp.Content) > maxImageBytesPerImage {
		return nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(resp.Content))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil
	}
	return &pageImage{src: src, data: resp.Content, size: image.Point{cfg.Width, cfg.Height}}
}

// image returns a prefetched image, if there is one.
func (p *Page) image(src string) *pageImage {
	if src == "" {
		return nil
	}
	p.imageMu.Lock()
	defer p.imageMu.Unlock()
	img := p.images[src]
	if img == nil || len(img.data) == 0 {
		return nil
	}
	return img
}
