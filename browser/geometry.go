package browser

import "golang.org/x/net/html"

// Rect is an element's border box in CSS pixels, measured from the page's
// top-left, the same rectangle getBoundingClientRect reports.
type Rect struct {
	X, Y, Width, Height float64
}

// Right is the box's right edge.
func (r Rect) Right() float64 { return r.X + r.Width }

// Bottom is the box's bottom edge.
func (r Rect) Bottom() float64 { return r.Y + r.Height }

// layoutGeom is the element geometry of one layout, keyed by element and by
// box id, valid until the DOM or the viewport width changes.
type layoutGeom struct {
	stamp uint64
	width int
	rects map[int]Rect
	nodes map[*html.Node]int
}

// ensureGeom returns the current element geometry, laying the page out with
// geometry recording on when the cache is stale. The layout is shared: a script
// that measures a thousand elements pays for one layout, not a thousand.
func (p *Page) ensureGeom() *layoutGeom {
	if p.doc == nil {
		return nil
	}
	w := int(p.viewportWidth())
	if p.geomCache != nil && p.geomCache.stamp == domMutations.Load() && p.geomCache.width == w {
		return p.geomCache
	}
	prev := p.geomOn
	p.geomOn = true
	p.geomLayouts++
	_, _, err := p.layOut(ScreenshotOptions{Width: w, NoImages: true, MaxHeight: 1 << 20})
	p.geomOn = prev
	if err != nil {
		return nil
	}
	return p.geomCache
}

// ElementRect returns the element's border box in CSS pixels. An element the
// layout did not place (a text node, or one inside display:none) returns the
// zero rectangle, as a browser's getBoundingClientRect returns for a detached
// node's parent chain.
func (p *Page) ElementRect(n *html.Node) Rect {
	if n == nil {
		return Rect{}
	}
	g := p.ensureGeom()
	if g == nil {
		return Rect{}
	}
	id, ok := g.nodes[n]
	if !ok {
		return Rect{}
	}
	return g.rects[id]
}

// ElementFromPoint returns the deepest element whose box contains the point,
// which is what document.elementFromPoint and a CDP hit test need. It reads the
// same cached layout as ElementRect.
func (p *Page) ElementFromPoint(x, y float64) *html.Node {
	g := p.ensureGeom()
	if g == nil {
		return nil
	}
	var best *html.Node
	bestDepth := -1
	for n, id := range g.nodes {
		r, ok := g.rects[id]
		if !ok {
			continue
		}
		if x < r.X || x > r.Right() || y < r.Y || y > r.Bottom() {
			continue
		}
		// The deepest element containing the point is the topmost one, which
		// is what hit testing returns.
		d := 0
		for p := n.Parent; p != nil; p = p.Parent {
			d++
		}
		if d > bestDepth {
			best = n
			bestDepth = d
		}
	}
	return best
}
