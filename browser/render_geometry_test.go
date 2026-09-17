package browser

import "testing"

func TestElementRectFromLayout(t *testing.T) {
	p := flexPage(t, `<div id="a" style="width:200px;height:50px;background:#000"></div>`)
	r := p.ElementRect(p.GetElementByID("a"))
	if r.Width != 200 {
		t.Fatalf("width = %g, want 200", r.Width)
	}
	if r.Height < 49 || r.Height > 51 {
		t.Fatalf("height = %g, want about 50", r.Height)
	}
}

func TestGetBoundingClientRect(t *testing.T) {
	p := flexPage(t, `<div id="a" style="width:120px;height:40px;background:#000"></div>`)
	v, err := p.Eval(`document.getElementById('a').getBoundingClientRect().width`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.ToFloat(); got != 120 {
		t.Fatalf("getBoundingClientRect().width = %v, want 120", got)
	}
}

func TestOffsetWidthAnswers(t *testing.T) {
	p := flexPage(t, `<div id="a" style="width:90px;height:30px;background:#000"></div>`)
	v, err := p.Eval(`document.getElementById('a').offsetWidth`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.ToInteger(); got != 90 {
		t.Fatalf("offsetWidth = %d, want 90", got)
	}
}

// An unplaced element reports the zero rectangle rather than an error.
func TestElementRectUnknownIsZero(t *testing.T) {
	p := flexPage(t, `<div id="a" style="width:10px;height:10px"></div>`)
	r := p.ElementRect(p.Query("nope"))
	if r != (Rect{}) {
		t.Fatalf("rect = %+v, want zero", r)
	}
}

// The layout is computed once and reused: measuring many elements must not
// re-lay-out the page each time.
func TestLayoutIsCached(t *testing.T) {
	p := flexPage(t, `<div style="background:#000;height:20px"></div><div style="background:#000;height:20px"></div>`)
	p.geomLayouts = 0
	p.ElementRect(p.doc)
	p.ElementRect(p.doc)
	p.ElementRect(p.doc)
	if p.geomLayouts != 1 {
		t.Fatalf("geometry performed %d layouts, want 1", p.geomLayouts)
	}
}

// A DOM mutation invalidates the cached layout, so a measured box follows the
// change.
func TestGeometryFollowsMutation(t *testing.T) {
	p := flexPage(t, `<div id="a" style="width:100px;height:20px;background:#000"></div>`)
	before := p.ElementRect(p.GetElementByID("a"))
	if before.Width != 100 {
		t.Fatalf("width = %g, want 100", before.Width)
	}
	_, err := p.Eval(`document.getElementById('a').style.width = '250px'`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	after := p.ElementRect(p.GetElementByID("a"))
	if after.Width != 250 {
		t.Fatalf("width after mutation = %g, want 250", after.Width)
	}
}

// A parent's rectangle covers its children, and the child's rectangle is its
// own. A container that produces no block of its own hands its padding to the
// first block inside it, so the two shared a rectangle until the padding each
// block carries was attributed to the element that put it there. The outer sits
// inside the body's default 8px margin: Chromium reports it as 8,8,1264,50 and
// the inner as 18,18,1244,30 for this page.
func TestParentRectSpansChildren(t *testing.T) {
	p := flexPage(t, `<div id="outer" style="background:#f00;padding:10px">
		<p id="inner" style="background:#00f;height:30px;margin:0">x</p>
	</div>`)
	outer := p.ElementRect(p.GetElementByID("outer"))
	inner := p.ElementRect(p.GetElementByID("inner"))
	if outer.Height != 50 {
		t.Fatalf("outer height = %g, want 50 (30 + 2*10 padding)", outer.Height)
	}
	if inner.X != 18 || inner.Y != 18 || inner.Width != 1244 || inner.Height != 30 {
		t.Errorf("inner rect = %g,%g %gx%g, want 18,18 1244x30",
			inner.X, inner.Y, inner.Width, inner.Height)
	}
	if outer.X > inner.X || outer.Right() < inner.Right() {
		t.Fatalf("outer %+v does not cover inner %+v horizontally", outer, inner)
	}
	if outer.Y > inner.Y {
		t.Fatalf("outer %+v does not cover inner %+v vertically", outer, inner)
	}
}

func TestElementFromPoint(t *testing.T) {
	p := flexPage(t, `<div id="box" style="width:100px;height:100px;background:#000"></div>`)
	got := p.ElementFromPoint(50, 50)
	if got == nil || got.Data != "div" {
		t.Fatalf("elementFromPoint returned %v", got)
	}
}

func TestDocumentElementFromPoint(t *testing.T) {
	p := flexPage(t, `<div id="box" style="width:100px;height:100px;background:#000"></div>`)
	v, err := p.Eval(`document.elementFromPoint(50, 50).id`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.String(); got != "box" {
		t.Fatalf("element id = %q, want box", got)
	}
}
