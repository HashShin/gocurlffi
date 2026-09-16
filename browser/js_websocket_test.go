package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

func echoWSServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			reply := append([]byte("echo:"), data...)
			if err := conn.Write(context.Background(), websocket.MessageText, reply); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestWebSocketEcho(t *testing.T) {
	wsURL := echoWSServer(t)
	p := flexPage(t, `<script>
	window.msg = 'pending';
	var ws = new WebSocket('`+wsURL+`');
	ws.onopen = function(){ ws.send('ping'); };
	ws.onmessage = function(e){ window.msg = e.data; ws.close(); };
	</script>`)
	v, err := p.Eval(`window.msg`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.String(); got != "echo:ping" {
		t.Fatalf("message = %q, want echo:ping", got)
	}
}

func TestWebSocketReadyState(t *testing.T) {
	wsURL := echoWSServer(t)
	p := flexPage(t, `<script>
	window.state = -1;
	var ws = new WebSocket('`+wsURL+`');
	ws.onopen = function(){ window.state = ws.readyState; ws.close(); };
	</script>`)
	v, _ := p.Eval(`window.state`)
	if v.ToInteger() != 1 {
		t.Fatalf("readyState = %d, want 1 (OPEN)", v.ToInteger())
	}
}

func TestWebSocketEventListener(t *testing.T) {
	wsURL := echoWSServer(t)
	p := flexPage(t, `<script>
	window.seen = '';
	var ws = new WebSocket('`+wsURL+`');
	ws.addEventListener('open', function(){ ws.send('hi there'); });
	ws.addEventListener('message', function(e){ window.seen = e.data; ws.close(); });
	</script>`)
	v, _ := p.Eval(`window.seen`)
	if got := v.String(); got != "echo:hi there" {
		t.Fatalf("seen = %q, want echo:hi there", got)
	}
}

func TestWebSocketConnectError(t *testing.T) {
	p := flexPage(t, `<script>
	window.errored = false;
	var ws = new WebSocket('ws://127.0.0.1:1/nope');
	ws.onerror = function(){ window.errored = true; };
	</script>`)
	v, _ := p.Eval(`window.errored`)
	if !v.ToBoolean() {
		t.Fatal("a failed connection should fire an error event")
	}
}

// A close the peer initiates is reported by the page's own goroutine, not by
// the socket reader. The reader used to schedule the event itself, which raced
// the timer queue the page was draining at the same time; see the -race target
// in the Makefile. The observable behaviour is what this pins.
func TestWebSocketPeerCloseFiresClose(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		// Close from the server side as soon as the handshake completes, so
		// the page never asks for the close itself.
		_ = conn.Close(websocket.StatusNormalClosure, "bye")
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	p := flexPage(t, `<script>
	window.closed = false;
	window.code = -1;
	var ws = new WebSocket('`+wsURL+`');
	ws.onclose = function(e){ window.closed = true; window.code = ws.readyState; };
	</script>`)

	v, err := p.Eval(`window.closed`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if !v.ToBoolean() {
		t.Fatal("a peer-initiated close did not fire the close event")
	}
	if state, _ := p.Eval(`window.code`); state.ToInteger() != 3 {
		t.Fatalf("readyState inside onclose = %d, want 3 (CLOSED)", state.ToInteger())
	}
}

// A binary frame must not arrive as a string: binaryType decides whether it is
// an ArrayBuffer or a Blob, and both branches used to deliver a string.
func TestWebSocketBinaryFrameType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		// Wait for the page's first message so binaryType is set already.
		if _, _, err := conn.Read(context.Background()); err != nil {
			return
		}
		_ = conn.Write(context.Background(), websocket.MessageBinary, []byte{1, 2, 3})
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	for _, tc := range []struct {
		binaryType string
		want       string
	}{
		{"arraybuffer", "arraybuffer"},
		{"blob", "blob"},
	} {
		t.Run(tc.binaryType, func(t *testing.T) {
			p := flexPage(t, `<script>
			window.kind = 'pending';
			var ws = new WebSocket('`+wsURL+`');
			ws.binaryType = '`+tc.binaryType+`';
			ws.onopen = function(){ ws.send('go'); };
			ws.onmessage = function(e){
				if (e.data instanceof ArrayBuffer) { window.kind = 'arraybuffer'; }
				else if (e.data instanceof Blob) { window.kind = 'blob'; }
				else { window.kind = 'other:' + (typeof e.data); }
				ws.close();
			};
			</script>`)

			v, err := p.Eval(`window.kind`)
			if err != nil {
				t.Fatalf("Eval: %v", err)
			}
			if got := v.String(); got != tc.want {
				t.Fatalf("binary frame with binaryType=%q arrived as %s, want %s",
					tc.binaryType, got, tc.want)
			}
		})
	}
}
