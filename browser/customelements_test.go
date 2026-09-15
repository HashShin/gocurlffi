package browser

import (
	"strings"
	"testing"
)

// Defining a component upgrades the element already written in the HTML.
func TestCustomElementUpgradesParsedElement(t *testing.T) {
	p := flexPage(t, `<html><body><my-el id="ce"></my-el></body></html>`)
	got := evalThen(t, p, `
		window.__log = [];
		customElements.define('my-el', class extends HTMLElement {
			constructor(){ super(); window.__log.push('ctor') }
			connectedCallback(){ window.__log.push('connected') }
		});
	`, `window.__log.join(',')`)
	if got != "ctor,connected" {
		t.Fatalf("lifecycle = %q, want %q", got, "ctor,connected")
	}
}

func TestCustomElementUpgradesCreatedElement(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := evalThen(t, p, `
		window.__log = [];
		customElements.define('new-el', class extends HTMLElement {
			constructor(){ super(); window.__log.push('ctor') }
			connectedCallback(){ window.__log.push('connected') }
		});
		var e = document.createElement('new-el');
		window.__log.push('created');
		document.body.appendChild(e);
	`, `window.__log.join(',')`)
	// The constructor runs at createElement; connectedCallback only on insertion.
	if got != "ctor,created,connected" {
		t.Fatalf("lifecycle = %q, want %q", got, "ctor,created,connected")
	}
}

// The object the page gets back must be the upgraded instance, with the
// definition's prototype methods reachable.
func TestCustomElementIdentityAndMethods(t *testing.T) {
	p := flexPage(t, `<html><body><my-el id="ce"></my-el></body></html>`)
	got := evalThen(t, p, `
		customElements.define('my-el', class extends HTMLElement {
			constructor(){ super(); this.count = 7 }
			greet(){ return 'hello ' + this.count }
		});
	`, `document.getElementById('ce').greet()`)
	if got != "hello 7" {
		t.Fatalf("prototype method = %q, want %q", got, "hello 7")
	}
	// An instance created after define shares the prototype.
	if got := evalStr(t, p, `document.createElement('my-el') instanceof customElements.get('my-el')`); got != "true" {
		t.Fatalf("instanceof = %q, want true", got)
	}
}

// An element the page already held keeps its identity through the upgrade.
func TestCustomElementKeepsHeldReference(t *testing.T) {
	p := flexPage(t, `<html><body><late-el id="ce"></late-el></body></html>`)
	got := evalThen(t, p, `
		var held = document.getElementById('ce');
		customElements.define('late-el', class extends HTMLElement {
			constructor(){ super(); this.mark = 'upgraded' }
		});
		window.__same = (held === document.getElementById('ce'));
		window.__mark = held.mark;
	`, `window.__same + ':' + window.__mark`)
	if got != "true:upgraded" {
		t.Fatalf("held reference after upgrade = %q, want %q", got, "true:upgraded")
	}
}

func TestCustomElementObservedAttributes(t *testing.T) {
	p := flexPage(t, `<html><body><attr-el id="ce" data-x="first"></attr-el></body></html>`)
	got := evalThen(t, p, `
		window.__log = [];
		customElements.define('attr-el', class extends HTMLElement {
			static get observedAttributes(){ return ['data-x'] }
			attributeChangedCallback(name, oldV, newV){
				window.__log.push(name + ':' + oldV + '->' + newV);
			}
		});
		document.getElementById('ce').setAttribute('data-x', 'second');
	`, `window.__log.join('|')`)
	// The attribute present at parse time is reported first, then the change.
	if got != "data-x:->first|data-x:first->second" {
		t.Fatalf("attributeChangedCallback = %q", got)
	}
	// An unobserved attribute must not fire.
	got = evalThen(t, p, `
		window.__log = [];
		document.getElementById('ce').setAttribute('data-y', 'nope');
	`, `window.__log.join('|')`)
	if got != "" {
		t.Fatalf("unobserved attribute fired %q", got)
	}
}

func TestCustomElementDisconnected(t *testing.T) {
	p := flexPage(t, `<html><body><gone-el id="ce"></gone-el></body></html>`)
	got := evalThen(t, p, `
		window.__log = [];
		customElements.define('gone-el', class extends HTMLElement {
			constructor(){ super(); window.__log.push('ctor') }
			connectedCallback(){ window.__log.push('connected') }
			disconnectedCallback(){ window.__log.push('disconnected') }
		});
		document.getElementById('ce').remove();
	`, `window.__log.join(',')`)
	if got != "ctor,connected,disconnected" {
		t.Fatalf("lifecycle = %q, want %q", got, "ctor,connected,disconnected")
	}
}

func TestCustomElementGetAndWhenDefined(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := evalThen(t, p, `
		window.__when = 'pending';
		customElements.whenDefined('later-el').then(function(c){ window.__when = 'resolved:' + (typeof c) });
		window.__before = String(customElements.get('later-el'));
		customElements.define('later-el', class extends HTMLElement {});
	`, `window.__before + '|' + window.__when`)
	if got != "undefined|resolved:function" {
		t.Fatalf("whenDefined = %q, want %q", got, "undefined|resolved:function")
	}
}

func TestCustomElementDefineTwiceThrows(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := evalStr(t, p, `
		customElements.define('dup-el', class extends HTMLElement {});
		try { customElements.define('dup-el', class extends HTMLElement {}); 'no error' }
		catch (e) { 'threw' }
	`)
	if got != "threw" {
		t.Fatalf("second define = %q, want %q", got, "threw")
	}
}

// A component that builds its shadow tree in the constructor is the shape real
// web components use, and it exercises the node-symbol fallback.
func TestCustomElementConstructorAttachesShadow(t *testing.T) {
	p := flexPage(t, `<html><body><card-el id="card"></card-el></body></html>`)
	got := evalThen(t, p, `
		customElements.define('card-el', class extends HTMLElement {
			constructor(){
				super();
				var root = this.attachShadow({mode:'open'});
				root.innerHTML = '<h2>card body</h2>';
			}
		});
	`, `document.getElementById('card').shadowRoot.querySelector('h2').textContent`)
	if got != "card body" {
		t.Fatalf("shadow built in the constructor = %q, want %q", got, "card body")
	}
}

// The end-to-end value: a component's rendered content reaches extraction.
func TestCustomElementContentAppearsInText(t *testing.T) {
	p := flexPage(t, `<html><body><card-el id="card"></card-el></body></html>`)
	if _, err := p.Eval(`
		customElements.define('card-el', class extends HTMLElement {
			connectedCallback(){
				var root = this.attachShadow({mode:'open'});
				root.innerHTML = '<p>component rendered</p>';
			}
		});
	`); err != nil {
		t.Fatal(err)
	}
	if got := p.Text(); !strings.Contains(got, "component rendered") {
		t.Fatalf("Page.Text() = %q, want the component's content", got)
	}
}

func TestCustomElementUpgradeDetachedTree(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := evalThen(t, p, `
		window.__log = [];
		customElements.define('det-el', class extends HTMLElement {
			constructor(){ super(); window.__log.push('ctor') }
			connectedCallback(){ window.__log.push('connected') }
		});
		var box = document.createElement('div');
		box.innerHTML = '<det-el></det-el>';
		window.__log.push('built');
		customElements.upgrade(box);
	`, `window.__log.join(',')`)
	if got != "built,ctor" {
		t.Fatalf("upgrade() on a detached tree = %q, want %q", got, "built,ctor")
	}
}
