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
