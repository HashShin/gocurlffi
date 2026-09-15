package browser

import "testing"

func TestClickDispatchesEvent(t *testing.T) {
	p := flexPage(t, `<button id="b" style="width:80px;height:30px;background:#000">go</button>
	<script>window.hit=0;document.getElementById('b').addEventListener('click',function(){window.hit++})</script>`)
	if err := p.Click("#b"); err != nil {
		t.Fatalf("Click: %v", err)
	}
	v, err := p.Eval(`window.hit`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.ToInteger(); got != 1 {
		t.Fatalf("click handler hit %d times, want 1", got)
	}
}

func TestClickMissingSelector(t *testing.T) {
	p := flexPage(t, `<div></div>`)
	if err := p.Click("#nope"); err == nil {
		t.Fatal("Click on a missing selector should error")
	}
}

func TestTypeUpdatesValue(t *testing.T) {
	p := flexPage(t, `<input id="q">
	<script>window.typed='';document.getElementById('q').addEventListener('input',function(e){window.typed=this.value})</script>`)
	if err := p.Type("#q", "hello"); err != nil {
		t.Fatalf("Type: %v", err)
	}
	v, err := p.Eval(`document.getElementById('q').value`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.String(); got != "hello" {
		t.Fatalf("input value = %q, want hello", got)
	}
	tv, err := p.Eval(`window.typed`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := tv.String(); got != "hello" {
		t.Fatalf("input handler saw %q, want hello", got)
	}
}

func TestFillFiresChange(t *testing.T) {
	p := flexPage(t, `<input id="q"><script>window.changed=0;document.getElementById('q').addEventListener('change',function(){window.changed++})</script>`)
	if err := p.Fill("#q", "x"); err != nil {
		t.Fatalf("Fill: %v", err)
	}
	v, _ := p.Eval(`document.getElementById('q').value`)
	if v.String() != "x" {
		t.Fatalf("value = %q, want x", v.String())
	}
	c, _ := p.Eval(`window.changed`)
	if c.ToInteger() != 1 {
		t.Fatalf("change fired %d times, want 1", c.ToInteger())
	}
}

func TestSelectOption(t *testing.T) {
	p := flexPage(t, `<select id="s"><option value="a">A</option><option value="b">B</option></select>`)
	if err := p.Select("#s", "b"); err != nil {
		t.Fatalf("Select: %v", err)
	}
	v, _ := p.Eval(`document.querySelector('#s option[selected]').value`)
	if v.String() != "b" {
		t.Fatalf("selected option = %q, want b", v.String())
	}
}

func TestCheckToggles(t *testing.T) {
	p := flexPage(t, `<input id="c" type="checkbox">`)
	if err := p.Check("#c", true); err != nil {
		t.Fatalf("Check: %v", err)
	}
	v, _ := p.Eval(`document.getElementById('c').checked`)
	if !v.ToBoolean() {
		t.Fatal("checkbox not checked")
	}
}

func TestClickNodeUsesCenterFromGeometry(t *testing.T) {
	p := flexPage(t, `<div id="box" style="width:100px;height:50px;background:#000">
		<script>window.pt=null;document.getElementById('box').addEventListener('click',function(e){window.pt=[e.clientX,e.clientY]})</script></div>`)
	if err := p.Click("#box"); err != nil {
		t.Fatalf("Click: %v", err)
	}
	v, _ := p.Eval(`JSON.stringify(window.pt)`)
	if v.String() == "null" {
		t.Fatalf("no click coordinates recorded")
	}
}
