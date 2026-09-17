package browser

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/dop251/goja"
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
// the selector, then a click, as a browser does, and follows the click's own
// meaning: a link navigates and a submit control submits its form.
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
	r := p.ElementRect(n)
	cx, cy := r.X+r.Width/2, r.Y+r.Height/2
	var click *goja.Object
	if p.env != nil {
		for _, typ := range []string{"pointerdown", "mousedown", "pointerup", "mouseup", "click"} {
			ev := p.env.newEvent(typ)
			_ = ev.Set("clientX", cx)
			_ = ev.Set("clientY", cy)
			_ = ev.Set("pageX", cx)
			_ = ev.Set("pageY", cy)
			_ = ev.Set("button", 0)
			p.env.dispatchNode(n, typ, ev)
			if typ == "click" {
				click = ev
			}
		}
	}
	p.focused = n
	if p.env != nil && p.env.defaultPrevented(click) {
		return nil
	}
	if err := p.defaultAction(n); err != nil {
		return err
	}
	// A handler may have asked for a navigation of its own, with location.assign
	// rather than by the click. Follow it now that the page has settled.
	return p.followPendingNav()
}

// ClickPoint clicks whatever element is at the page coordinates.
func (p *Page) ClickPoint(x, y float64) error {
	n := p.ElementFromPoint(x, y)
	if n == nil {
		return fmt.Errorf("browser: nothing at (%g, %g)", x, y)
	}
	return p.ClickNode(n)
}

// defaultAction is what a click means once the page's handlers have had their
// say: a link navigates, a submit control submits its form, and anything else
// is the page's own business. A handler that calls preventDefault stops this,
// as it does in a browser.
//
// A click a script makes (element.click()) is queued instead of performed,
// because loading a new document from inside the script that asked would pull
// the document out from under it.
func (p *Page) defaultAction(n *html.Node) error {
	if form, submitter := p.submitOwner(n); form != nil {
		if p.inScript {
			// Carrying a POST body past the end of the script phase would need
			// a queue of its own; a page that submits from script usually posts
			// with fetch instead.
			p.log("warn", "form submit from a script is not carried out")
			return nil
		}
		return p.submitForm(form, submitter)
	}
	href, ok := p.linkTarget(n)
	if !ok {
		return nil
	}
	if p.inScript {
		p.requestNavigation(href)
		return nil
	}
	return p.navigate(href, nil, nil)
}

// linkTarget reports the URL a click on n follows: the nearest enclosing anchor
// with an href. Anchors a browser would not navigate - a download, or a target
// that needs a second window - are reported as no link.
func (p *Page) linkTarget(n *html.Node) (string, bool) {
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.Type != html.ElementNode {
			continue
		}
		switch strings.ToLower(cur.Data) {
		case "a", "area":
		default:
			continue
		}
		href := attrOf(cur, "href")
		if href == "" || hasAttr(cur, "download") {
			return "", false
		}
		if t := attrOf(cur, "target"); t != "" && t != "_self" && t != "_top" && t != "_parent" {
			p.log("warn", "link target "+t+" needs a second window; staying on "+p.URL)
			return "", false
		}
		return resolveURL(p.baseURL(), href), true
	}
	return "", false
}

// submitOwner reports the form a click submits: the control, when it is a
// submit button, and the form it belongs to, by nesting or by the form
// attribute. A disabled control submits nothing.
func (p *Page) submitOwner(n *html.Node) (*html.Node, *html.Node) {
	if n == nil || n.Type != html.ElementNode || hasAttr(n, "disabled") {
		return nil, nil
	}
	tag := strings.ToLower(n.Data)
	typ := strings.ToLower(attrOf(n, "type"))
	isSubmit := tag == "button" && (typ == "" || typ == "submit") || tag == "input" && (typ == "submit" || typ == "image")
	if !isSubmit {
		return nil, nil
	}
	if id := attrOf(n, "form"); id != "" {
		if f := p.GetElementByID(id); f != nil && strings.EqualFold(f.Data, "form") {
			return f, n
		}
		return nil, nil
	}
	for cur := n.Parent; cur != nil; cur = cur.Parent {
		if cur.Type == html.ElementNode && strings.EqualFold(cur.Data, "form") {
			return cur, n
		}
	}
	return nil, nil
}

// submitForm performs a form's default action: submit fires on the form, the
// successful controls are collected, and the form's method and action fetch the
// next document. A POST carries the entries as the body; a GET appends them to
// the action's query.
func (p *Page) submitForm(form, submitter *html.Node) error {
	if p.env != nil {
		ev := p.env.newEvent("submit")
		p.env.dispatchNode(form, "submit", ev)
		if p.env.defaultPrevented(ev) {
			return nil
		}
	}
	target := resolveURL(p.baseURL(), attrOf(form, "action"))
	fd := &formData{}
	collectFormEntries(form, fd)
	addSubmitter(fd, submitter)
	// collectFormEntries skips file inputs, so this is always the urlencoded
	// body, which is also the query a GET sends.
	body, contentType := fd.encode()
	switch strings.ToUpper(strings.TrimSpace(attrOf(form, "method"))) {
	case "DIALOG":
		return nil
	case "POST":
		return p.navigate(target, body, map[string]string{"Content-Type": contentType})
	default:
		return p.navigate(withQuery(target, string(body)), nil, nil)
	}
}

// addSubmitter adds the button that was clicked to the entries, which a browser
// includes in the submission.
func addSubmitter(fd *formData, n *html.Node) {
	if n == nil {
		return
	}
	name := attrOf(n, "name")
	if name == "" {
		return
	}
	value := attrOf(n, "value")
	if value == "" && strings.EqualFold(n.Data, "button") {
		value = strings.TrimSpace(textContent(n))
	}
	fd.entries = append(fd.entries, formEntry{name: name, value: value})
}

// withQuery puts a form's serialized entries in a URL's query, dropping the
// fragment the way a form submission does.
func withQuery(rawURL, query string) string {
	if query == "" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL + "?" + query
	}
	if u.RawQuery == "" {
		u.RawQuery = query
	} else {
		u.RawQuery += "&" + query
	}
	u.Fragment = ""
	return u.String()
}

// navigate loads the document a default action resolved to, and follows any
// navigation that document asks for in turn. A non-nil body makes it a POST.
func (p *Page) navigate(rawURL string, body []byte, headers map[string]string) error {
	if body != nil {
		if err := p.loadForm(rawURL, body, headers); err != nil {
			return err
		}
	} else if err := p.load(rawURL, headers); err != nil {
		return err
	}
	return p.followPendingNav()
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
