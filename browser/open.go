package browser

import (
	"fmt"
	"strings"

	"github.com/HashShin/gocurlffi/requests"
)

// Open loads a request's URL in a browser of its own and returns the page
// object, so the same Request literal Browse takes can be read and driven:
// Browse answers with the rendered bytes, Open with the page they came from.
//
// The page owns the browser, so Page.Close releases both. Only a GET of a URL
// has a browser path, which is the same limit Browse has.
func Open(req requests.RequestTypes, opts ...requests.Option) (*Page, error) {
	r, err := requests.ToRequest(req)
	if err != nil {
		return nil, err
	}
	if m := strings.ToUpper(strings.TrimSpace(r.Method)); m != "" && m != "GET" {
		return nil, fmt.Errorf("browser: Open takes a GET of a URL, not %s", m)
	}
	if len(r.Body) > 0 || r.JSON != nil {
		return nil, fmt.Errorf("browser: Open takes a GET of a URL, which has no body")
	}

	o := Options{}
	if r.Impersonate != "" {
		o.Impersonate = r.Impersonate
	}
	if r.Proxy != "" {
		o.Proxy = r.Proxy
	}
	if r.Timeout > 0 {
		o.Timeout = r.Timeout
	}
	if r.Headers != nil {
		o.Headers = requests.NewHeaders(r.Headers).Items()
	}
	// Options come after the request's own fields, as they do on Send and
	// Browse, so a later option wins over the field it names.
	o.LoadOptions = opts

	b := New(o)
	p, err := b.Open(r.URL)
	if err != nil {
		b.Close()
		return nil, err
	}
	return p, nil
}
