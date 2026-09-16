package browser

import (
	"testing"
	"time"
)

// A page that renders on a later turn needs the clock moved, not just slept on.
func TestWaitForTimeRunsTimer(t *testing.T) {
	p := flexPage(t, `<html><body><div id="root"></div><script>
		setTimeout(function(){
			var d = document.createElement('span');
			d.id = 'late';
			d.textContent = 'appeared';
			document.getElementById('root').appendChild(d);
		}, 120);
	</script></body></html>`)
	// The default timer budget is 2s, so a plain load usually catches this; make
	// the wait explicit anyway and assert the outcome.
	p.WaitForTime(400 * time.Millisecond)
	got := evalStr(t, p, `document.getElementById('late') ? document.getElementById('late').textContent : 'missing'`)
	if got != "appeared" {
		t.Fatalf("after WaitForTime = %q, want %q", got, "appeared")
	}
}

func TestWaitForScriptResolvesOnCondition(t *testing.T) {
	p := flexPage(t, `<html><body><script>
		window.__ready = false;
		setTimeout(function(){ window.__ready = true }, 100);
	</script></body></html>`)
	if err := p.WaitForScript(`window.__ready`, 3*time.Second); err != nil {
		t.Fatalf("WaitForScript: %v", err)
	}
}

func TestWaitForScriptTimesOut(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	err := p.WaitForScript(`false`, 150*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForScript returned nil for a condition that never becomes true")
	}
}

// A broken expression never becomes true, so it must fail fast rather than
// retrying until the timeout.
func TestWaitForScriptReportsSyntaxError(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	start := time.Now()
	err := p.WaitForScript(`this is not javascript`, 5*time.Second)
	if err == nil {
		t.Fatal("WaitForScript returned nil for a syntax error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("WaitForScript retried a syntax error for %s instead of returning it", elapsed)
	}
}

func TestWaitForNetworkIdleReturns(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	done := make(chan struct{})
	go func() {
		p.WaitForNetworkIdle(50*time.Millisecond, 2*time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("WaitForNetworkIdle did not return")
	}
}

// --wait-until domcontentloaded stops before the load event.
func TestWaitUntilDOMContentLoaded(t *testing.T) {
	b := New(Options{WaitUntil: "domcontentloaded"})
	defer b.Close()
	p := b.NewPage("https://example.test/")
	err := p.SetContent(`<html><body><script>
		window.__dcl = 0; window.__load = 0; window.__timer = 0;
		addEventListener('DOMContentLoaded', function(){
			window.__dcl = 1;
			// A handler that renders at DOMContentLoaded still runs, because the
			// event is dispatched synchronously.
			document.title = 'rendered-at-dcl';
		});
		addEventListener('load', function(){ window.__load = 1 });
		setTimeout(function(){ window.__timer = 1 }, 30);
	</script></body></html>`, "https://example.test/")
	if err != nil {
		t.Fatal(err)
	}
	if got := evalStr(t, p, "window.__dcl"); got != "1" {
		t.Errorf("DOMContentLoaded fired = %q, want 1", got)
	}
	if got := evalStr(t, p, "document.title"); got != "rendered-at-dcl" {
		t.Errorf("a DOMContentLoaded handler did not run: title = %q", got)
	}
	if got := evalStr(t, p, "window.__load"); got != "0" {
		t.Errorf("load fired = %q under wait-until=domcontentloaded, want 0", got)
	}
	// The timer budget is what this mode skips, so a pending timer must not have
	// run. Without that, the mode would cost as much as a full load.
	if got := evalStr(t, p, "window.__timer"); got != "0" {
		t.Errorf("a timer ran under wait-until=domcontentloaded: __timer = %q, want 0", got)
	}
}

func TestWaitUntilDefaultRunsLoad(t *testing.T) {
	p := flexPage(t, `<html><body><script>
		window.__load = 0;
		addEventListener('load', function(){ window.__load = 1 });
	</script></body></html>`)
	if got := evalStr(t, p, "window.__load"); got != "1" {
		t.Fatalf("load fired = %q with the default wait-until, want 1", got)
	}
}

// An unknown mode means the default rather than failing or hanging.
func TestWaitUntilUnknownFallsBack(t *testing.T) {
	b := New(Options{WaitUntil: "nonsense"})
	defer b.Close()
	p := b.NewPage("https://example.test/")
	if err := p.SetContent(`<html><body><script>
		window.__load = 0;
		addEventListener('load', function(){ window.__load = 1 });
	</script></body></html>`, "https://example.test/"); err != nil {
		t.Fatal(err)
	}
	if got := evalStr(t, p, "window.__load"); got != "1" {
		t.Fatalf("unknown wait-until ran the load = %q, want 1", got)
	}
}
