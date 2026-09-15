package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestWarnJSGate(t *testing.T) {
	var buf bytes.Buffer
	warnJSGate(&buf, "https://www.google.com/search?q=x",
		[]byte(`<meta content="0;url=/httpservice/retry/enablejs?sei=abc" http-equiv="refresh">`))
	if !strings.Contains(buf.String(), "JavaScript") {
		t.Fatalf("expected a JavaScript gate warning, got %q", buf.String())
	}

	buf.Reset()
	warnJSGate(&buf, "https://example.com", []byte("<html><body>hello</body></html>"))
	if buf.Len() != 0 {
		t.Fatalf("expected no warning for normal content, got %q", buf.String())
	}
}
