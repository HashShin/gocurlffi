package browser

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func robotsServer(t *testing.T, robots string, hits *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(robots))
			return
		}
		if hits != nil {
			atomic.AddInt32(hits, 1)
		}
		switch r.URL.Path {
		case "/private":
			_, _ = w.Write([]byte(`<p>secret</p>`))
		default:
			_, _ = w.Write([]byte(`<p>public</p>`))
		}
	}))
}

func TestRobotsDisallowsWhenEnabled(t *testing.T) {
	srv := robotsServer(t, "User-agent: *\nDisallow: /private\n", nil)
	defer srv.Close()

	b := New(Options{ObeyRobots: true})
	defer b.Close()
	off := false
	b.opts.RunScripts = &off

	p, err := b.Open(srv.URL + "/private")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := strings.TrimSpace(p.Text()); strings.Contains(got, "secret") {
		t.Fatalf("robots.txt was ignored: rendered %q", got)
	}
	if p.Response() == nil || p.Response().StatusCode != 403 {
		t.Fatalf("status = %v, want a synthetic 403", p.Response())
	}
}

func TestRobotsAllowsEverythingElse(t *testing.T) {
	srv := robotsServer(t, "User-agent: *\nDisallow: /private\n", nil)
	defer srv.Close()

	b := New(Options{ObeyRobots: true})
	defer b.Close()
	off := false
	b.opts.RunScripts = &off

	p, err := b.Open(srv.URL + "/public")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := strings.TrimSpace(p.Text()); !strings.Contains(got, "public") {
		t.Fatalf("allowed page rendered %q", got)
	}
}

func TestRobotsIgnoredByDefault(t *testing.T) {
	srv := robotsServer(t, "User-agent: *\nDisallow: /\n", nil)
	defer srv.Close()

	b := New(Options{})
	defer b.Close()
	off := false
	b.opts.RunScripts = &off

	p, err := b.Open(srv.URL + "/private")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := strings.TrimSpace(p.Text()); !strings.Contains(got, "secret") {
		t.Fatalf("with ObeyRobots off the page should load, got %q", got)
	}
}

func TestRobotsSubpathsHonorLongestMatch(t *testing.T) {
	srv := robotsServer(t, "User-agent: *\nDisallow: /private\nAllow: /private/open\n", nil)
	defer srv.Close()

	b := New(Options{ObeyRobots: true})
	defer b.Close()
	off := false
	b.opts.RunScripts = &off

	p, err := b.Open(srv.URL + "/private/open")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if p.Response() != nil && p.Response().StatusCode == 403 {
		t.Fatal("/private/open is explicitly allowed but was blocked")
	}
}

func TestRobotsUserAgentGroup(t *testing.T) {
	body := "User-agent: goodbot\nDisallow: /x\n\nUser-agent: *\nDisallow: /\n"
	srv := robotsServer(t, body, nil)
	defer srv.Close()

	b := New(Options{ObeyRobots: true, UserAgent: "goodbot/1.0"})
	defer b.Close()
	off := false
	b.opts.RunScripts = &off

	p, err := b.Open(srv.URL + "/y")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if p.Response() != nil && p.Response().StatusCode == 403 {
		t.Fatal("the specific user-agent group should allow /y")
	}
}
