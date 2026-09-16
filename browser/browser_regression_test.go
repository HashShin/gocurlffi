package browser

import (
	"strings"
	"testing"
	"time"
)

// Regression: setTimeout(cb) with no delay used to slice past the argument
// list and panic, crashing the whole process.
func TestSetTimeoutArgumentHandling(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	err := p.SetContent(`<html><body><div id="o"></div>
<script>
  setTimeout(function () { document.getElementById('o').setAttribute('data-a', '1'); });
  setTimeout(function () { document.getElementById('o').setAttribute('data-b', '2'); }, 1);
  setTimeout(function (x, y) {
    document.getElementById('o').setAttribute('data-c', x + y);
  }, 1, 'p', 'q');
</script></body></html>`, "https://example.test/")
	if err != nil {
		t.Fatalf("SetContent: %v", err)
	}
	o := p.Query("#o")
	for _, attr := range []string{"data-a", "data-b", "data-c"} {
		if Attr(o, attr) == "" {
			t.Fatalf("timer did not set %s; html=%s", attr, p.HTML())
		}
	}
	if got := Attr(o, "data-c"); got != "pq" {
		t.Fatalf("timer args = %q, want pq", got)
	}
}

// A panic raised inside a native binding while a script runs must surface as
// a logged error, not a process crash.
func TestScriptPanicIsRecovered(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	if err := p.SetContent(`<html><body>
<script>setTimeout(function(){});</script>
<script>document.body.setAttribute('data-ok','1');</script>
</body></html>`, "https://example.test/"); err != nil {
		t.Fatalf("SetContent: %v", err)
	}
	if Attr(p.Query("body"), "data-ok") != "1" {
		t.Fatalf("script after the offending one did not run")
	}
}

func TestWebGlobals(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://a.test/dir/page")
	_ = p.SetContent(`<html><body><div id="o"></div></body></html>`, "https://a.test/dir/page")

	cases := []struct{ expr, want string }{
		{`new URL('/x?y=1', 'https://a.test/b/').href`, "https://a.test/x?y=1"},
		{`new URL('https://a.test/p?q=2#h').searchParams.get('q')`, "2"},
		{`new URLSearchParams('a=1&b=2').get('b')`, "2"},
		{`typeof TextEncoder !== 'undefined' && new TextEncoder().encode('ab').length`, "2"},
		{`new TextDecoder().decode([104,105])`, "hi"},
		{`typeof crypto.randomUUID()`, "string"},
		{`JSON.stringify(structuredClone({a:[1,2]}))`, `{"a":[1,2]}`},
		{`(function(){var c=new AbortController();c.abort();return c.signal.aborted;})()`, "true"},
		{`typeof performance.now()`, "number"},
		{`typeof new Event('x').type`, "string"},
		{`typeof new MutationObserver(function(){}).observe`, "function"},
	}
	for _, c := range cases {
		v, err := p.Eval(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got := v.String(); got != c.want {
			t.Fatalf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestDocumentCurrentScript(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body><div id="o"></div>
<script>document.body.setAttribute('data-during', document.currentScript ? 'yes' : 'no');</script>
</body></html>`, "https://example.test/")
	if got := Attr(p.Query("body"), "data-during"); got != "yes" {
		t.Fatalf("document.currentScript during execution = %q, want yes", got)
	}
	v, _ := p.Eval("document.currentScript === null")
	if v == nil || v.String() != "true" {
		t.Fatalf("document.currentScript after load should be null, got %v", v)
	}
}

// Some bundlers ship classic scripts with top-level await. Those are not valid
// classic scripts, but they can be run wrapped in an async function.
func TestTopLevelAwaitFallback(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><body>
<script>
  var x = await Promise.resolve(41);
  window.__tla = x + 1;
</script></body></html>`, "https://example.test/")
	v, err := p.Eval("window.__tla")
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if v == nil || v.String() != "42" {
		t.Fatalf("top-level await result = %v, want 42", v)
	}
}

func TestMarkdownSkipsScripts(t *testing.T) {
	b := newTestBrowser(t)
	p := b.NewPage("https://example.test/")
	_ = p.SetContent(`<html><head><script>var leak = "SHOULD_NOT_APPEAR";</script></head>
<body><p>visible</p></body></html>`, "https://example.test/")
	md := p.Markdown()
	if strings.Contains(md, "SHOULD_NOT_APPEAR") {
		t.Fatalf("markdown leaked script source: %s", md)
	}
}

func TestInterruptBusyLoop(t *testing.T) {
	ms := 200 * time.Millisecond
	b := New(Options{JavaScriptTimeout: ms, LoadTimeout: 5 * time.Second})
	defer b.Close()
	p := b.NewPage("https://example.test/")
	start := time.Now()
	_ = p.SetContent(`<html><body><script>while(true){}</script><script>document.title='after';</script></body></html>`, "https://example.test/")
	t.Logf("elapsed=%s console=%v", time.Since(start), p.Console())
	v, _ := p.Eval("document.title")
	t.Logf("title=%v", v)
}

// Runaway recursion through Function.prototype.apply used to spin forever
// because goja's call stack was unbounded. It must now fail fast.
//
// This test has two timing dependencies that both scale with the machine, and
// it used to carry neither, so it failed under the race detector and on a
// contended host for reasons unrelated to what it checks:
//
//   - the recursion itself has to hit the interpreter's limit, which is CPU
//     bound (about 21s here, and roughly double that under -race);
//   - the load budget has to outlast that, because the assertion is that the
//     script *after* the runaway one still runs. A load whose budget is spent
//     skips the remaining scripts by design, so a 30s budget meant the second
//     script was skipped and the attribute was never set.
func TestRunawayRecursionIsBounded(t *testing.T) {
	b := New(Options{LoadTimeout: recursionBudget + 30*time.Second})
	t.Cleanup(b.Close)

	p := b.NewPage("https://example.test/")
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = p.SetContent(`<html><body><div id="o"></div>
<script>
  function f() { return f.apply(null, arguments); }
  f();
</script>
<script>document.getElementById('o').setAttribute('data-after', '1');</script>
</body></html>`, "https://example.test/")
	}()
	select {
	case <-done:
	case <-time.After(recursionBudget):
		t.Fatal("runaway recursion did not terminate")
	}
	if Attr(p.Query("#o"), "data-after") != "1" {
		t.Fatalf("later script did not run after recursion error; console=%v", p.Console())
	}
}
