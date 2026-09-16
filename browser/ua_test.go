package browser

import "testing"

// The user-agent sheet is what a browser applies before any author CSS, and
// these are the parts of it a page can depend on without saying so.
//
// Every value here was read from Chromium's getComputedStyle for the same
// markup. The replaced elements were previously all inline-block; the form
// controls inherited the page's font size instead of the UA sheet's.
func TestUserAgentDefaultsMatchChromium(t *testing.T) {
	p := flexPage(t, `<html><body>
		<iframe id="iframe"></iframe>
		<svg id="svg"></svg>
		<canvas id="canvas"></canvas>
		<video id="video"></video>
		<object id="object"></object>
		<embed id="embed">
		<audio id="audio"></audio>
		<audio id="audioctl" controls></audio>
		<input id="input">
		<textarea id="textarea"></textarea>
		<select id="select"></select>
		<button id="button"></button>
	</body></html>`)

	checkAll(t, p, []struct{ expr, want string }{
		// A replaced element's initial display is inline, not inline-block.
		{`getComputedStyle(document.getElementById("iframe")).display`, "inline"},
		{`getComputedStyle(document.getElementById("svg")).display`, "inline"},
		{`getComputedStyle(document.getElementById("canvas")).display`, "inline"},
		{`getComputedStyle(document.getElementById("video")).display`, "inline"},
		{`getComputedStyle(document.getElementById("object")).display`, "inline"},
		{`getComputedStyle(document.getElementById("embed")).display`, "inline"},
		// "audio:not([controls]) { display: none }".
		{`getComputedStyle(document.getElementById("audio")).display`, "none"},
		{`getComputedStyle(document.getElementById("audioctl")).display`, "inline"},
		// Form controls are inline-block, unlike the replaced elements.
		{`getComputedStyle(document.getElementById("input")).display`, "inline-block"},
		{`getComputedStyle(document.getElementById("textarea")).display`, "inline-block"},
		{`getComputedStyle(document.getElementById("select")).display`, "inline-block"},
		{`getComputedStyle(document.getElementById("button")).display`, "inline-block"},
		// "font: 400 13.3333px Arial": a control does not inherit the page size.
		{`getComputedStyle(document.getElementById("input")).fontSize`, "13.3333px"},
		{`getComputedStyle(document.getElementById("textarea")).fontSize`, "13.3333px"},
		{`getComputedStyle(document.getElementById("select")).fontSize`, "13.3333px"},
		{`getComputedStyle(document.getElementById("button")).fontSize`, "13.3333px"},
	})
}

// An author rule still wins over the UA sheet, so the defaults above must not
// be unconditional.
func TestAuthorOverridesUserAgentDefaults(t *testing.T) {
	p := flexPage(t, `<html><head><style>
		iframe { display: block }
		input { display: block; font-size: 20px }
	</style></head><body>
		<iframe id="iframe"></iframe><input id="input">
	</body></html>`)

	checkAll(t, p, []struct{ expr, want string }{
		{`getComputedStyle(document.getElementById("iframe")).display`, "block"},
		{`getComputedStyle(document.getElementById("input")).display`, "block"},
		{`getComputedStyle(document.getElementById("input")).fontSize`, "20px"},
	})
}
