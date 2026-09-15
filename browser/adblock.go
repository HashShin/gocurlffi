package browser

import (
	"net/url"
	"strings"
)

// A small, built-in ad and tracker blocker, enabled with Options.Adblock. It is
// deliberately conservative: it matches well-known advertising and analytics
// hosts and a few path patterns, so it removes the noise from a page without
// the risk of a broad filter list blocking real content. It runs on the same
// path as interception, after a caller's own Intercept hook, so a hook can
// still override it.

// adblockHosts are hosts (and their subdomains) known to serve ads or trackers.
var adblockHosts = []string{
	"doubleclick.net",
	"googlesyndication.com",
	"googleadservices.com",
	"google-analytics.com",
	"analytics.google.com",
	"googletagmanager.com",
	"googletagservices.com",
	"adservice.google.com",
	"facebook.net",
	"connect.facebook.net",
	"scorecardresearch.com",
	"quantserve.com",
	"outbrain.com",
	"taboola.com",
	"criteo.com",
	"criteo.net",
	"moatads.com",
	"adnxs.com",
	"pubmatic.com",
	"rubiconproject.com",
	"openx.net",
	"casalemedia.com",
	"amazon-adsystem.com",
	"hotjar.com",
	"segment.com",
	"segment.io",
	"mixpanel.com",
	"amplitude.com",
	"branch.io",
	"newrelic.com",
	"bugsnag.com",
}

// adblockPathHints are path fragments that almost always denote an ad or a
// tracker, checked only when the host is not already matched.
var adblockPathHints = []string{
	"/ads/", "/adserver/", "/advert", "/analytics.js", "/gtag/js",
	"/google-analytics", "/pagead/", "/beacon?", "/collect?",
}

// adblockBlocked reports whether the request should be dropped by the built-in
// blocker. It never blocks a document load, only subresources.
func adblockBlocked(rawURL, rtype string) bool {
	if rtype == "document" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range adblockHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	path := strings.ToLower(u.RequestURI())
	for _, p := range adblockPathHints {
		if strings.Contains(path, p) {
			return true
		}
	}
	return false
}
