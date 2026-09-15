package browser

import "testing"

// pushState must rewrite the URL in place: a router that pushes a route and
// then reads location.pathname used to see the old path forever.
func TestPushStateUpdatesLocation(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	for _, tc := range []struct{ script, expr, want string }{
		{`history.pushState({}, '', '/dashboard');`, `location.pathname`, "/dashboard"},
		{`history.pushState({}, '', '/a/b?x=1');`, `location.pathname`, "/a/b"},
		{`history.pushState({}, '', '/a/b?x=1');`, `location.search`, "?x=1"},
		{`history.pushState({}, '', '/a/b?x=1#frag');`, `location.hash`, "#frag"},
		{`history.pushState({}, '', '/p');`, `location.href`, "https://example.test/p"},
		{`history.pushState({}, '', '/p');`, `location.origin`, "https://example.test"},
		// Last, because it moves the document to another origin for good.
		{`history.pushState({}, '', 'https://other.test/z');`, `location.hostname`, "other.test"},
	} {
		got := evalThen(t, p, tc.script, tc.expr)
		if got != tc.want {
			t.Errorf("after %s: %s = %q, want %q", tc.script, tc.expr, got, tc.want)
		}
	}
}

func TestPushStatePreservesSearchAndHash(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := evalThen(t, p, `history.pushState({}, '', '/x?q=1#h');`, `location.pathname + location.search + location.hash`)
	if got != "/x?q=1#h" {
		t.Fatalf("location parts = %q", got)
	}
}

func TestReplaceStateDoesNotGrowHistory(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := evalThen(t, p, `
		history.pushState({}, '', '/one');
		history.replaceState({}, '', '/two');
	`, `history.length + ':' + location.pathname`)
	if got != "2:/two" {
		t.Fatalf("history.length and path = %q, want %q", got, "2:/two")
	}
}

func TestHistoryBackRestoresURL(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := evalThen(t, p, `
		history.pushState({}, '', '/first');
		history.pushState({}, '', '/second');
		history.back();
	`, `location.pathname`)
	if got != "/first" {
		t.Fatalf("after back: location.pathname = %q, want %q", got, "/first")
	}
}

func TestHistoryGOReachesEntry(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := evalThen(t, p, `
		history.pushState({}, '', '/a');
		history.pushState({}, '', '/b');
		history.pushState({}, '', '/c');
		history.go(-2);
	`, `location.pathname`)
	if got != "/a" {
		t.Fatalf("after go(-2): location.pathname = %q, want %q", got, "/a")
	}
}

func TestHistoryBackFiresPopstate(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := evalThen(t, p, `
		window.__pops = 0;
		window.addEventListener('popstate', function(){ window.__pops++ });
		history.pushState({}, '', '/one');
		history.back();
	`, `window.__pops`)
	if got != "1" {
		t.Fatalf("popstate fired %q times, want 1", got)
	}
}

// pushState changes the document base, so a relative fetch must resolve against
// the route that was pushed.
func TestPushStateChangesBaseURL(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := evalThen(t, p,
		`history.pushState({}, '', '/deep/route');`,
		`new URL('x.js', location.href).href`)
	if got != "https://example.test/deep/x.js" {
		t.Fatalf("resolved URL = %q, want %q", got, "https://example.test/deep/x.js")
	}
}

// --- structuredClone --------------------------------------------------------

func TestStructuredCloneHandlesMapAndSet(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	for _, tc := range []struct{ expr, want string }{
		{`structuredClone(new Map([['a',1]])).get('a')`, "1"},
		{`structuredClone(new Map([['a',1]])).size`, "1"},
		{`Array.from(structuredClone(new Set([1,2]))).join(',')`, "1,2"},
		{`structuredClone(new Date(0)).getTime()`, "0"},
		{`structuredClone(/ab+/gi).source`, "ab+"},
		{`structuredClone(/ab+/gi).flags`, "gi"},
		{`structuredClone({a:{b:[1,2]}}).a.b[1]`, "2"},
		{`structuredClone([1,2,3]).length`, "3"},
		{`structuredClone('s')`, "s"},
		{`structuredClone(7)`, "7"},
		{`structuredClone(null) === null`, "true"},
		// A nested Map inside an object is the case JSON silently flattened.
		{`structuredClone({m:new Map([['k','v']])}).m.get('k')`, "v"},
		// Typed arrays must survive with their values.
		{`structuredClone(new Uint8Array([1,2,3]))[2]`, "3"},
		{`structuredClone(new Uint8Array([1,2,3])).length`, "3"},
	} {
		got := evalStr(t, p, tc.expr)
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

func TestStructuredCloneIsDeepNotShared(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := evalThen(t, p, `
		var src = {list:[1,2]};
		var copy = structuredClone(src);
		copy.list.push(3);
		window.__r = src.list.length + ':' + copy.list.length;
	`, `window.__r`)
	if got != "2:3" {
		t.Fatalf("clone shares state with its source: %q, want %q", got, "2:3")
	}
}

// JSON.stringify throws on a cycle; structuredClone must not.
func TestStructuredCloneHandlesCycle(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := evalThen(t, p, `
		var a = {name:'a'};
		a.self = a;
		var b = structuredClone(a);
		window.__r = (b !== a) + ':' + (b.self === b) + ':' + b.name;
	`, `window.__r`)
	if got != "true:true:a" {
		t.Fatalf("cyclic clone = %q, want %q", got, "true:true:a")
	}
}
