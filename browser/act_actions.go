package browser

import (
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

// The Page action methods drive the page the way a user would: they resolve a
// selector, dispatch the events a browser would fire, and update the control's
// value. They are the shared layer the CLI, the CDP Input domain and the BiDi
// input module all call, and they use the geometry from geometry.go so a click
// lands on the element's centre.

func (p *Page) resolve(sel string) (*html.Node, error) {
	n := p.Query(sel)
	if n == nil {
		return nil, fmt.Errorf("browser: no element matches %q", sel)
	}
	return n, nil
}

// Click dispatches a full mouse press and release on the first element matching
// the selector, then a click, as a browser does.
func (p *Page) Click(sel string) error {
	n, err := p.resolve(sel)
	if err != nil {
		return err
	}
	return p.ClickNode(n)
}

// ClickNode clicks an element node directly.
func (p *Page) ClickNode(n *html.Node) error {
	if n == nil {
		return fmt.Errorf("browser: click on nil element")
	}
	if p.env == nil {
		return nil
	}
	r := p.ElementRect(n)
	cx, cy := r.X+r.Width/2, r.Y+r.Height/2
	for _, typ := range []string{"pointerdown", "mousedown", "pointerup", "mouseup", "click"} {
		ev := p.env.newEvent(typ)
		_ = ev.Set("clientX", cx)
		_ = ev.Set("clientY", cy)
		_ = ev.Set("pageX", cx)
		_ = ev.Set("pageY", cy)
		_ = ev.Set("button", 0)
		p.env.dispatchNode(n, typ, ev)
	}
	p.focused = n
	return nil
}

// ClickPoint clicks whatever element is at the page coordinates.
func (p *Page) ClickPoint(x, y float64) error {
	n := p.ElementFromPoint(x, y)
	if n == nil {
		return fmt.Errorf("browser: nothing at (%g, %g)", x, y)
	}
	return p.ClickNode(n)
}

// Focus dispatches the focus event on the element and records it as active.
func (p *Page) Focus(sel string) error {
	n, err := p.resolve(sel)
	if err != nil {
		return err
	}
	if p.env == nil {
		return nil
	}
	p.focused = n
	p.env.dispatchNode(n, "focus", p.env.newEvent("focus"))
	return nil
}

// Type focuses the element and types text into it, firing the key and input
// events a user's keystrokes would.
func (p *Page) Type(sel, text string) error {
	n, err := p.resolve(sel)
	if err != nil {
		return err
	}
	if p.env == nil {
		return nil
	}
	p.focused = n
	cur := nodeValue(n)
	for _, r := range text {
		key := string(r)
		kd := p.env.newEvent("keydown")
		_ = kd.Set("key", key)
		p.env.dispatchNode(n, "keydown", kd)

		cur += key
		setNodeValue(n, cur)

		in := p.env.newEvent("input")
		_ = in.Set("data", key)
		p.env.dispatchNode(n, "input", in)

		ku := p.env.newEvent("keyup")
		_ = ku.Set("key", key)
		p.env.dispatchNode(n, "keyup", ku)
	}
	return nil
}

// Fill sets a control's value in one step and fires input and change, the way a
// paste or a programmatic fill does.
func (p *Page) Fill(sel, value string) error {
	n, err := p.resolve(sel)
	if err != nil {
		return err
	}
	if p.env == nil {
		return nil
	}
	setNodeValue(n, value)
	p.env.dispatchNode(n, "input", p.env.newEvent("input"))
	p.env.dispatchNode(n, "change", p.env.newEvent("change"))
	return nil
}

// Select chooses an <option> by value (or by label when no value matches) and
// fires change.
func (p *Page) Select(sel, value string) error {
	n, err := p.resolve(sel)
	if err != nil {
		return err
	}
	if p.env == nil {
		return nil
	}
	var match *html.Node
	for _, opt := range descendants(n) {
		if opt.Type != html.ElementNode || opt.Data != "option" {
			continue
		}
		v, _ := getAttr(opt, "value")
		if v == "" {
			v = strings.TrimSpace(textContent(opt))
		}
		if v == value {
			match = opt
			break
		}
	}
	if match == nil {
		return fmt.Errorf("browser: no option %q in %q", value, sel)
	}
	for _, opt := range descendants(n) {
		if opt.Type == html.ElementNode && opt.Data == "option" {
			removeAttr(opt, "selected")
		}
	}
	setAttr(match, "selected", "")
	p.env.dispatchNode(n, "change", p.env.newEvent("change"))
	return nil
}

// Check sets a checkbox or radio's checked state and fires change.
func (p *Page) Check(sel string, on bool) error {
	n, err := p.resolve(sel)
	if err != nil {
		return err
	}
	if p.env == nil {
		return nil
	}
	if on {
		setAttr(n, "checked", "")
	} else {
		removeAttr(n, "checked")
	}
	p.env.dispatchNode(n, "change", p.env.newEvent("change"))
	return nil
}

// Press dispatches a key press on the focused element, or the body when
// nothing is focused.
func (p *Page) Press(key string) error {
	if p.env == nil {
		return nil
	}
	n := p.focused
	if n == nil {
		n = p.doc
	}
	for _, typ := range []string{"keydown", "keyup"} {
		ev := p.env.newEvent(typ)
		_ = ev.Set("key", key)
		p.env.dispatchNode(n, typ, ev)
	}
	return nil
}

// Scroll dispatches a scroll event on the element; a full-page render has no
// scrolling viewport, so this only informs the page.
func (p *Page) Scroll(sel string) error {
	n, err := p.resolve(sel)
	if err != nil {
		return err
	}
	if p.env == nil {
		return nil
	}
	p.env.dispatchNode(n, "scroll", p.env.newEvent("scroll"))
	return nil
}

// nodeValue reads a control's current value, matching the DOM's value property.
func nodeValue(n *html.Node) string {
	if n == nil || n.Type != html.ElementNode {
		return ""
	}
	if n.Data == "textarea" {
		return textContent(n)
	}
	v, _ := getAttr(n, "value")
	return v
}

// setNodeValue writes a control's value the way its DOM property setter does.
func setNodeValue(n *html.Node, value string) {
	if n == nil || n.Type != html.ElementNode {
		return
	}
	if n.Data == "textarea" {
		setTextContent(n, value)
		return
	}
	setAttr(n, "value", value)
}
