package browser

import "gocurlffi/requests"

// Request is offered to an Intercept hook before it is sent. Its fields are a
// copy, so a hook may modify the returned request through the pointer it
// receives but cannot change the page's own state.
type Request struct {
	URL          string
	Method       string
	ResourceType string // document, script, stylesheet, image, font, fetch, xhr, other
	Headers      map[string]string
}

// Response is what an Intercept hook returns: nil to continue to the network,
// Block to drop the request, or Fulfill to answer without the network.
type Response struct {
	Status  int
	Headers map[string]string
	Body    []byte
	blocked bool
}

// Block drops a request. The caller sees a synthetic 403 with no body, and the
// load continues rather than failing.
func Block() *Response {
	return &Response{Status: 403, blocked: true}
}

// Fulfill answers a request from the caller instead of the network.
func Fulfill(status int, contentType string, body []byte) *Response {
	h := map[string]string{}
	if contentType != "" {
		h["Content-Type"] = contentType
	}
	return &Response{Status: status, Headers: h, Body: body}
}

// toRequests turns a hook response into the transport response the callers
// expect.
func (r *Response) toRequests(u string) *requests.Response {
	resp := requests.NewResponse()
	resp.URL = u
	if r.Status != 0 {
		resp.StatusCode = r.Status
	}
	resp.Ok = resp.StatusCode >= 200 && resp.StatusCode < 300
	resp.Content = r.Body
	for k, v := range r.Headers {
		resp.Headers.Set(k, v)
	}
	return resp
}

// intercept offers the request to the hook, returning a response when the hook
// handled it and nil to continue. The nil check is the only cost on the fast
// path.
func (b *Browser) intercept(method, rawURL string, headers map[string]string, rtype string) *Response {
	if b.opts.Intercept == nil {
		return nil
	}
	return b.opts.Intercept(&Request{
		URL:          rawURL,
		Method:       method,
		ResourceType: rtype,
		Headers:      headers,
	})
}
