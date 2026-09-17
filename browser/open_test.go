package browser

import (
	"testing"

	"github.com/HashShin/gocurlffi/requests"
)

// Open takes what Browse takes and answers with the page object instead of the
// rendered bytes, so the same Request literal can be read and driven.
func TestOpenTakesWhatBrowseTakes(t *testing.T) {
	srv := htmlServer(t, map[string]string{
		"/": `<h1 id="t">before</h1>
<script>document.getElementById('t').textContent = 'after'</script>`,
	})

	req := requests.Request{URL: srv.URL + "/", Impersonate: requests.Chrome131}
	for _, tc := range []struct {
		name string
		in   requests.RequestTypes
		opts []requests.Option
	}{
		{"a Request", req, nil},
		{"a *Request", &req, nil},
		{"a URL on its own", srv.URL + "/", nil},
		{"options instead of fields", srv.URL + "/", []requests.Option{requests.WithImpersonate(requests.Chrome131)}},
	} {
		p, err := Open(tc.in, tc.opts...)
		if err != nil {
			t.Fatalf("%s: Open: %v", tc.name, err)
		}
		if got := p.TextOf(p.Query("#t")); got != "after" {
			t.Errorf("%s: rendered text = %q, want the script's value", tc.name, got)
		}
		if p.Title() == "" && p.Query("h1") == nil {
			t.Errorf("%s: page has no document", tc.name)
		}
		p.Close()
	}
}

// Only a GET of a URL has a browser path, which is the same limit Browse has,
// so Open refuses what it cannot send rather than silently sending a GET.
func TestOpenRejectsWhatTheBrowserCannotSend(t *testing.T) {
	srv := htmlServer(t, map[string]string{"/": `<p>ok</p>`})

	if _, err := Open(requests.Request{URL: srv.URL + "/", Method: "POST"}); err == nil {
		t.Error("Open accepted a POST")
	}
	if _, err := Open(requests.Request{URL: srv.URL + "/", JSON: map[string]any{"a": 1}}); err == nil {
		t.Error("Open accepted a JSON body")
	}
	if _, err := Open(42); err == nil {
		t.Error("Open accepted an int")
	}
}
