package browser

import "testing"

// navigator.permissions, connection and pdfViewerEnabled are on every Chrome,
// and the usual stealth scripts patch permissions.query, so it is read.
func TestNavigatorExtras(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	checkAll(t, p, []struct{ expr, want string }{
		{`typeof navigator.permissions`, "object"},
		{`Object.prototype.toString.call(navigator.permissions)`, "[object Permissions]"},
		{`typeof navigator.permissions.query`, "function"},
		{`navigator.pdfViewerEnabled`, "true"},
		{`String(navigator.doNotTrack)`, "null"},
		{`typeof navigator.connection`, "object"},
		{`Object.prototype.toString.call(navigator.connection)`, "[object NetworkInformation]"},
		{`navigator.connection.effectiveType`, "4g"},
		{`navigator.connection.saveData`, "false"},
	})
}

func TestPermissionsQueryResolves(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	stealthEval(t, p, `window.__perm = null;
		navigator.permissions.query({name: "geolocation"}).then(function (s) { window.__perm = s; });`)
	checkAll(t, p, []struct{ expr, want string }{
		{`window.__perm.state`, "prompt"},
		{`window.__perm.onchange === null`, "true"},
	})
}

// Chrome always has an AudioContext, and an audio fingerprint is built from it.
func TestAudioContextExistsAndChainRuns(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	checkAll(t, p, []struct{ expr, want string }{
		{`typeof AudioContext`, "function"},
		{`typeof webkitAudioContext`, "function"},
		{`Object.prototype.toString.call(new AudioContext())`, "[object AudioContext]"},
		{`new AudioContext().sampleRate`, "44100"},
		{`new AudioContext().state`, "running"},
		{`new AudioContext().constructor.name`, "AudioContext"},
		{`new AudioContext().createAnalyser().frequencyBinCount`, "1024"},
		{`(function () {
			var c = new AudioContext(), o = c.createOscillator(), g = c.createGain(), a = c.createAnalyser();
			o.connect(g); g.connect(a); a.connect(c.destination); o.start(0);
			var d = new Float32Array(a.frequencyBinCount);
			a.getFloatFrequencyData(d);
			return d.length;
		})()`, "1024"},
	})
}
