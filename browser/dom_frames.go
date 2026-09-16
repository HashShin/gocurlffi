package browser

import (
	"strings"

	"github.com/dop251/goja"
	"golang.org/x/net/html"
)

// maxFrameDepth bounds how deep nested iframes load, so a document that embeds
// itself cannot recurse forever.
const maxFrameDepth = 4

// Frames returns the child pages of this document, in document order.
func (p *Page) Frames() []*Page { return p.frames }

// FrameFor returns the child page an <iframe> element loaded, or nil.
func (p *Page) FrameFor(el *html.Node) *Page {
	if p.frameFor == nil {
		return nil
	}
	return p.frameFor[el]
}

// loadFrames fetches and parses the document's iframes, each as a child Page
// with its own document and JavaScript environment. It runs once per document
// load, after the top document's scripts, so a frame an element creates is
// found too. A frame that cannot be fetched is skipped, not fatal.
func (p *Page) loadFrames() {
	p.frames = nil
	p.frameFor = map[*html.Node]*Page{}
	if p.doc == nil || p.frameDepth >= maxFrameDepth {
		return
	}
	for _, el := range getElementsByTagName(p.doc, "iframe") {
		if p.outOfBudget() {
			p.log("warn", "frame load budget exceeded; skipped remaining frames")
			return
		}
		src := attrOf(el, "src")
		srcdoc := attrOf(el, "srcdoc")
		var child *Page
		switch {
		case srcdoc != "":
			child = newPage(p.browser, p.baseURL())
			child.parent = p
			child.frameDepth = p.frameDepth + 1
			if err := child.SetContent(srcdoc, p.baseURL()); err != nil {
				p.debugf("frame srcdoc failed: %v", err)
				continue
			}
		case src != "" && !strings.HasPrefix(src, "javascript:") && src != "about:blank":
			abs := resolveURL(p.baseURL(), src)
			child = newPage(p.browser, abs)
			child.parent = p
			child.frameDepth = p.frameDepth + 1
			if err := child.load(abs, nil); err != nil {
				p.debugf("frame load failed %s: %v", abs, err)
				continue
			}
		default:
			// An iframe with no src (or about:blank) still has an empty
			// browsing context, as a browser gives it.
			child = newPage(p.browser, "about:blank")
			child.parent = p
			child.frameDepth = p.frameDepth + 1
			if err := child.SetContent(`<!doctype html><html><head></head><body></body></html>`, "about:blank"); err != nil {
				continue
			}
		}
		p.frames = append(p.frames, child)
		p.frameFor[el] = child
	}
}

// frameWindow builds a window object for a frame that a script can reach
// through iframe.contentWindow. The frame runs in its own JavaScript runtime,
// so only its document's nodes can be shared; the rest is a thin window shape.
func (e *jsEnv) frameWindow(child *Page) goja.Value {
	if child == nil {
		return goja.Null()
	}
	o := e.vm.NewObject()
	_ = o.Set("document", e.wrap(child.doc))
	loc := e.vm.NewObject()
	_ = loc.Set("href", child.URL)
	_ = loc.Set("toString", func(goja.FunctionCall) goja.Value { return e.vm.ToValue(child.URL) })
	_ = o.Set("location", loc)
	_ = o.Set("frameElement", goja.Null())
	return o
}
