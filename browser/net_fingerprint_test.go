package browser

import "testing"

const (
	testChromeUA  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	testAndroidUA = "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36"
	testFirefoxUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:147.0) Gecko/20100101 Firefox/147.0"
)

func fingerprintPage(t *testing.T, ua string) *Page {
	t.Helper()
	b := New(Options{UserAgent: ua})
	t.Cleanup(b.Close)
	p := b.NewPage("https://example.test/")
	if err := p.SetContent(`<html><body></body></html>`, "https://example.test/"); err != nil {
		t.Fatalf("SetContent: %v", err)
	}
	return p
}

func checkAll(t *testing.T, p *Page, cases []struct{ expr, want string }) {
	t.Helper()
	for _, tc := range cases {
		if got := stealthEval(t, p, tc.expr); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// window.chrome is a Chrome-only namespace. Its absence under a Chrome UA is one
// of the oldest automation tells, and the usual stealth scripts install a stub
// for exactly that reason.
func TestWindowChromeMatchesUserAgent(t *testing.T) {
	chrome := fingerprintPage(t, testChromeUA)
	checkAll(t, chrome, []struct{ expr, want string }{
		{`typeof window.chrome`, "object"},
		{`typeof window.chrome.runtime`, "object"},
		{`typeof window.chrome.csi`, "function"},
		{`typeof window.chrome.loadTimes`, "function"},
		{`window.chrome.app.isInstalled`, "false"},
	})
	// ...but Safari does not have it, so a Firefox UA must not get one either.
	firefox := fingerprintPage(t, testFirefoxUA)
	if got := stealthEval(t, firefox, `typeof window.chrome`); got != "undefined" {
		t.Errorf("firefox UA: typeof window.chrome = %q, want undefined", got)
	}
}

// An empty vendor beside a Chrome UA is a mismatch visible without any other
// probe.
func TestNavigatorVendorMatchesUserAgent(t *testing.T) {
	checkAll(t, fingerprintPage(t, testChromeUA), []struct{ expr, want string }{
		{`navigator.vendor`, "Google Inc."},
		{`navigator.deviceMemory`, "8"},
	})
	if got := stealthEval(t, fingerprintPage(t, testFirefoxUA), `navigator.vendor`); got != "" {
		t.Errorf("firefox vendor = %q, want empty", got)
	}
}

func TestNavigatorPluginsLookReal(t *testing.T) {
	p := fingerprintPage(t, testChromeUA)
	checkAll(t, p, []struct{ expr, want string }{
		{`navigator.plugins.length`, "5"},
		{`navigator.plugins[0].name`, "PDF Viewer"},
		{`navigator.plugins[4].name`, "WebKit built-in PDF"},
		{`navigator.plugins.item(1).name`, "Chrome PDF Viewer"},
		{`navigator.plugins.namedItem("PDF Viewer").name`, "PDF Viewer"},
		{`navigator.plugins.namedItem("nope") === null`, "true"},
		{`navigator.plugins[0].length`, "2"},
		{`navigator.plugins[0].item(0).type`, "application/pdf"},
		{`navigator.mimeTypes.length`, "2"},
		{`navigator.mimeTypes[0].type`, "application/pdf"},
		{`navigator.mimeTypes.namedItem("text/pdf").suffixes`, "pdf"},
	})
}

// The client-hint brand list must agree with the UA string, because comparing
// the two is a standard consistency check.
func TestUserAgentDataBrandsMatchUserAgent(t *testing.T) {
	checkAll(t, fingerprintPage(t, testChromeUA), []struct{ expr, want string }{
		{`navigator.userAgentData.brands.length`, "3"},
		{`navigator.userAgentData.brands[1].brand`, "Chromium"},
		{`navigator.userAgentData.brands[1].version`, "131"},
		{`navigator.userAgentData.brands[2].brand`, "Google Chrome"},
		{`navigator.userAgentData.brands[2].version`, "131"},
		{`navigator.userAgentData.mobile`, "false"},
		{`navigator.userAgentData.platform`, "macOS"},
		{`String(navigator.userAgentData.brands[0].version)`, greaseFor131},
	})
	// A Firefox UA has no client hints at all.
	checkAll(t, fingerprintPage(t, testFirefoxUA), []struct{ expr, want string }{
		{`typeof navigator.userAgentData`, "undefined"},
	})
}

const greaseFor131 = "24" // 131 - 107

func TestGetHighEntropyValuesHonoursHints(t *testing.T) {
	p := fingerprintPage(t, testChromeUA)
	// The promise resolves on the job queue, which goja drains when the script
	// returns, so the value is readable from a second evaluation.
	if got := stealthEval(t, p, `typeof navigator.userAgentData.getHighEntropyValues(["uaFullVersion"]).then`); got != "function" {
		t.Fatalf("getHighEntropyValues did not return a promise")
	}
	stealthEval(t, p, `window.__hev = null;
		navigator.userAgentData.getHighEntropyValues(["uaFullVersion","platformVersion","architecture"])
			.then(function (v) { window.__hev = v; });`)
	checkAll(t, p, []struct{ expr, want string }{
		{`window.__hev.uaFullVersion`, "131.0.0.0"},
		{`window.__hev.platformVersion`, "15.7.0"},
		{`window.__hev.architecture`, "x86"},
		// Hints that were not asked for must not come back.
		{`Object.keys(window.__hev).length`, "3"},
	})
}

// Google's interstitial reads performance.timing directly.
func TestPerformanceTimingPresent(t *testing.T) {
	p := fingerprintPage(t, testChromeUA)
	checkAll(t, p, []struct{ expr, want string }{
		{`typeof performance.timing`, "object"},
		{`typeof performance.timing.navigationStart`, "number"},
		{`typeof performance.timing.responseStart`, "number"},
		{`performance.timing.navigationStart > 0`, "true"},
		{`typeof performance.navigation`, "object"},
		{`performance.getEntriesByType("navigation").length`, "1"},
		{`performance.getEntriesByType("navigation")[0].entryType`, "navigation"},
		{`performance.getEntriesByType("resource").length`, "0"},
		// now() is measured from timeOrigin, so it is small and non-negative.
		{`performance.now() >= 0`, "true"},
	})
}

// goja ships no Intl, and a ReferenceError on it aborts whatever script touched
// it. The time zone is read by nearly every fingerprinting library.
func TestIntlSurface(t *testing.T) {
	p := fingerprintPage(t, testChromeUA)
	checkAll(t, p, []struct{ expr, want string }{
		{`typeof Intl`, "object"},
		{`Intl.DateTimeFormat().resolvedOptions().timeZone`, "UTC"},
		{`Intl.DateTimeFormat().resolvedOptions().locale`, "en-US"},
		{`typeof Intl.DateTimeFormat().format(new Date(0))`, "string"},
		{`Intl.NumberFormat().format(42)`, "42"},
		{`Intl.Collator().compare("a","b")`, "-1"},
		{`Intl.getCanonicalLocales(["en-US"]).length`, "1"},
	})
}

// An Android UA reporting no touch points contradicts itself.
func TestMaxTouchPointsMatchesUserAgent(t *testing.T) {
	if got := stealthEval(t, fingerprintPage(t, testAndroidUA), `navigator.maxTouchPoints > 0`); got != "true" {
		t.Errorf("android maxTouchPoints > 0 = %s, want true", got)
	}
	if got := stealthEval(t, fingerprintPage(t, testChromeUA), `navigator.maxTouchPoints`); got != "0" {
		t.Errorf("desktop maxTouchPoints = %s, want 0", got)
	}
}

func TestGreaseMajorTracksChromeVersion(t *testing.T) {
	for _, tc := range []struct{ ua, want string }{
		{"Mozilla/5.0 Chrome/131.0.0.0 Safari/537.36", "24"},
		{"Mozilla/5.0 Chrome/107.0.0.0 Safari/537.36", "0"},
		{"Mozilla/5.0 Firefox/147.0", "0"},
	} {
		if got := greaseMajor(tc.ua); got != tc.want {
			t.Errorf("greaseMajor(%q) = %s, want %s", tc.ua, got, tc.want)
		}
	}
}
