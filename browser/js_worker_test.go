package browser

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func workerServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/worker.js":
			w.Header().Set("Content-Type", "application/javascript")
			io.WriteString(w, `self.onmessage = function(e){ postMessage(e.data * 2); };`)
		case "/adder.js":
			w.Header().Set("Content-Type", "application/javascript")
			io.WriteString(w, `importScripts('/lib.js'); self.onmessage = function(e){ postMessage(add(e.data, 1)); };`)
		case "/lib.js":
			w.Header().Set("Content-Type", "application/javascript")
			io.WriteString(w, `function add(a, b){ return a + b; }`)
		case "/listener.js":
			w.Header().Set("Content-Type", "application/javascript")
			io.WriteString(w, `addEventListener('message', function(e){ postMessage('hi ' + e.data); });`)
		case "/":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<p>page</p>`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestWorkerPostMessage(t *testing.T) {
	srv := workerServer(t)
	p := flexPage(t, `<script>
	window.out = 'pending';
	var w = new Worker('`+srv.URL+`/worker.js');
	w.onmessage = function(e){ window.out = e.data; w.terminate(); };
	w.postMessage(21);
	</script>`)
	v, err := p.Eval(`window.out`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.ToInteger(); got != 42 {
		t.Fatalf("worker reply = %d, want 42", got)
	}
}

func TestWorkerImportScripts(t *testing.T) {
	srv := workerServer(t)
	p := flexPage(t, `<script>
	window.out = 'pending';
	var w = new Worker('`+srv.URL+`/adder.js');
	w.onmessage = function(e){ window.out = e.data; w.terminate(); };
	w.postMessage(41);
	</script>`)
	v, _ := p.Eval(`window.out`)
	if got := v.ToInteger(); got != 42 {
		t.Fatalf("importScripts worker reply = %d, want 42", got)
	}
}

func TestWorkerAddEventListener(t *testing.T) {
	srv := workerServer(t)
	p := flexPage(t, `<script>
	window.out = 'pending';
	var w = new Worker('`+srv.URL+`/listener.js');
	w.addEventListener('message', function(e){ window.out = e.data; w.terminate(); });
	w.postMessage('there');
	</script>`)
	v, _ := p.Eval(`window.out`)
	if got := v.String(); got != "hi there" {
		t.Fatalf("reply = %q, want hi there", got)
	}
}

func TestWorkerErrorOnMissingScript(t *testing.T) {
	srv := workerServer(t)
	p := flexPage(t, `<script>
	window.err = '';
	var w = new Worker('`+srv.URL+`/nope.js');
	w.onerror = function(e){ window.err = e.message || 'error'; };
	</script>`)
	v, _ := p.Eval(`window.err`)
	if !strings.Contains(v.String(), "failed") {
		t.Fatalf("error = %q, want a load failure", v.String())
	}
}
