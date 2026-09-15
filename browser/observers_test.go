package browser

import "testing"

// MutationObserver callbacks are delivered at the microtask checkpoint, so a
// test observes, mutates, and reads the result from the next Eval -- exactly the
// shape a framework uses.
func TestMutationObserverChildListFires(t *testing.T) {
	p := flexPage(t, `<html><body><div id="root"></div></body></html>`)
	got := evalThen(t, p, `
		window.__n = 0; window.__added = 0; window.__type = '';
		var mo = new MutationObserver(function(records){
			window.__n += records.length;
			window.__type = records[0].type;
			window.__added += records[0].addedNodes.length;
		});
		mo.observe(document.getElementById('root'), {childList:true});
		document.getElementById('root').appendChild(document.createElement('span'));
	`, `window.__n + ':' + window.__type + ':' + window.__added`)
	if got != "1:childList:1" {
		t.Fatalf("mutation callback got %q, want %q", got, "1:childList:1")
	}
}

func TestMutationObserverRemovedNodes(t *testing.T) {
	p := flexPage(t, `<html><body><div id="root"><b id="gone">x</b></div></body></html>`)
	got := evalThen(t, p, `
		window.__removed = -1;
		var mo = new MutationObserver(function(records){ window.__removed = records[0].removedNodes.length });
		mo.observe(document.getElementById('root'), {childList:true});
		document.getElementById('root').removeChild(document.getElementById('gone'));
	`, `window.__removed`)
	if got != "1" {
		t.Fatalf("removedNodes = %q, want 1", got)
	}
}

func TestMutationObserverAttributes(t *testing.T) {
	p := flexPage(t, `<html><body><div id="d" class="old"></div></body></html>`)
	got := evalThen(t, p, `
		window.__r = '';
		var mo = new MutationObserver(function(records){
			var r = records[0];
			window.__r = r.type + ':' + r.attributeName + ':' + r.oldValue;
		});
		mo.observe(document.getElementById('d'), {attributes:true, attributeOldValue:true});
		document.getElementById('d').setAttribute('class', 'new');
	`, `window.__r`)
	if got != "attributes:class:old" {
		t.Fatalf("attribute record = %q, want %q", got, "attributes:class:old")
	}
}

func TestMutationObserverSubtree(t *testing.T) {
	p := flexPage(t, `<html><body><div id="root"><div id="inner"></div></div></body></html>`)
	got := evalThen(t, p, `
		window.__n = 0;
		var mo = new MutationObserver(function(records){ window.__n += records.length });
		mo.observe(document.getElementById('root'), {childList:true, subtree:true});
		document.getElementById('inner').appendChild(document.createElement('i'));
	`, `window.__n`)
	if got != "1" {
		t.Fatalf("subtree observation fired %q times, want 1", got)
	}
	// Without subtree the same mutation must not be reported. This observer
	// keeps its own counter, because the subtree one above is still attached.
	got = evalThen(t, p, `
		window.__n2 = 0;
		var mo2 = new MutationObserver(function(records){ window.__n2 += records.length });
		mo2.observe(document.getElementById('root'), {childList:true});
		document.getElementById('inner').appendChild(document.createElement('i'));
	`, `window.__n2`)
	if got != "0" {
		t.Fatalf("non-subtree observation fired %q times, want 0", got)
	}
}

func TestMutationObserverDisconnect(t *testing.T) {
	p := flexPage(t, `<html><body><div id="root"></div></body></html>`)
	got := evalThen(t, p, `
		window.__n = 0;
		var mo = new MutationObserver(function(records){ window.__n += records.length });
		mo.observe(document.getElementById('root'), {childList:true});
		mo.disconnect();
		document.getElementById('root').appendChild(document.createElement('span'));
	`, `window.__n`)
	if got != "0" {
		t.Fatalf("after disconnect the callback fired %q times, want 0", got)
	}
}

func TestMutationObserverTakeRecords(t *testing.T) {
	p := flexPage(t, `<html><body><div id="root"></div></body></html>`)
	got := evalThen(t, p, `
		window.__taken = 0; window.__after = 0;
		var mo = new MutationObserver(function(records){ window.__after = records.length });
		mo.observe(document.getElementById('root'), {childList:true});
		document.getElementById('root').appendChild(document.createElement('span'));
		window.__taken = mo.takeRecords().length;
	`, `window.__taken + ':' + window.__after`)
	// takeRecords drains the queue, so the callback must not run afterwards.
	if got != "1:0" {
		t.Fatalf("takeRecords gave %q, want %q", got, "1:0")
	}
}

// A framework-shaped pattern: the observer is installed and then the content it
// needs is produced from the callback.
func TestMutationObserverDrivesContent(t *testing.T) {
	p := flexPage(t, `<html><body><div id="mount"></div></body></html>`)
	got := evalThen(t, p, `
		var mount = document.getElementById('mount');
		var mo = new MutationObserver(function(){
			if (document.getElementById('late')) return;
			var d = document.createElement('p');
			d.id = 'late';
			d.textContent = 'rendered';
			mount.appendChild(d);
		});
		mo.observe(mount, {childList:true});
		mount.appendChild(document.createElement('span'));
	`, `document.getElementById('late') ? document.getElementById('late').textContent : 'missing'`)
	if got != "rendered" {
		t.Fatalf("mutation-driven render gave %q, want %q", got, "rendered")
	}
}

// --- IntersectionObserver ---------------------------------------------------

// An observer registered after load used to never fire; now observe() schedules
// the initial notification.
func TestIntersectionObserverFiresAfterLoad(t *testing.T) {
	p := flexPage(t, `<html><body><div id="lazy"></div></body></html>`)
	got := evalThen(t, p, `
		window.__hit = 0;
		var io = new IntersectionObserver(function(entries){
			if (entries[0].isIntersecting) window.__hit++;
		});
		io.observe(document.getElementById('lazy'));
	`, `window.__hit`)
	if got != "1" {
		t.Fatalf("intersection callback fired %q times, want 1", got)
	}
}

func TestIntersectionObserverReportsRealRect(t *testing.T) {
	p := flexPage(t, `<html><body><div id="box" style="width:120px;height:40px"></div></body></html>`)
	got := evalThen(t, p, `
		window.__w = -1;
		var io = new IntersectionObserver(function(entries){
			window.__w = entries[0].boundingClientRect.width;
		});
		io.observe(document.getElementById('box'));
	`, `window.__w`)
	if got != "120" {
		t.Fatalf("boundingClientRect.width = %q, want 120 (a zeroed rect means "+
			"the entry is not reading layout)", got)
	}
}

func TestIntersectionObserverDisconnect(t *testing.T) {
	p := flexPage(t, `<html><body><div id="a"></div></body></html>`)
	got := evalThen(t, p, `
		window.__hit = 0;
		var io = new IntersectionObserver(function(){ window.__hit++ });
		io.observe(document.getElementById('a'));
		io.disconnect();
	`, `window.__hit`)
	if got != "0" {
		t.Fatalf("after disconnect the callback fired %q times, want 0", got)
	}
}

func TestResizeObserverReportsContentRect(t *testing.T) {
	p := flexPage(t, `<html><body><div id="box" style="width:200px;height:50px"></div></body></html>`)
	got := evalThen(t, p, `
		window.__w = -1;
		var ro = new ResizeObserver(function(entries){
			window.__w = entries[0].contentRect.width;
		});
		ro.observe(document.getElementById('box'));
	`, `window.__w`)
	if got != "200" {
		t.Fatalf("contentRect.width = %q, want 200", got)
	}
}
