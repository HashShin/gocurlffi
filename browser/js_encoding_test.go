package browser

import "testing"

// btoa/atob operate on binary strings: one code unit is one byte. Encoding the
// Go string as UTF-8 instead turns every code unit above 0x7F into two bytes,
// which silently corrupts any base64 a page builds out of raw bytes - the
// binary blobs inside a challenge token among them.
func TestBtoaTreatsInputAsBinaryString(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	checkAll(t, p, []struct{ expr, want string }{
		{`btoa(String.fromCharCode(0xC3))`, "ww=="},
		{`btoa(String.fromCharCode(0xFF))`, "/w=="},
		{`btoa(String.fromCharCode(0xC3,0xB3))`, "w7M="},
		{`btoa("hello")`, "aGVsbG8="},
		{`btoa("")`, ""},
		// atob hands back a binary string: one code unit per decoded byte.
		{`atob("ww==").charCodeAt(0)`, "195"},
		{`atob("w7M=").charCodeAt(1)`, "179"},
		{`atob(btoa(String.fromCharCode(0xC3,0xB3))).charCodeAt(0)`, "195"},
		{`atob("aGVsbG8=")`, "hello"},
	})
}

func TestBtoaRejectsCodeUnitsAboveOneByte(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := stealthEval(t, p, `(function () {
		try { btoa(String.fromCharCode(0x100)) } catch (e) { return e.name }
		return "no throw";
	})()`)
	if got != "TypeError" {
		t.Errorf("btoa above the Latin-1 range threw %q, want TypeError", got)
	}
}

// TextEncoder must hand back a real Uint8Array: instanceof, the toString tag,
// .byteLength and ArrayBuffer.isView are all readable from a page, and an
// array-like object answers "not a typed array" to every one of them.
func TestTextEncoderReturnsUint8Array(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	checkAll(t, p, []struct{ expr, want string }{
		{`Object.prototype.toString.call(new TextEncoder().encode("a"))`, "[object Uint8Array]"},
		{`new TextEncoder().encode("a") instanceof Uint8Array`, "true"},
		{`ArrayBuffer.isView(new TextEncoder().encode("a"))`, "true"},
		{`new TextEncoder().encode("\u00f3")[0]`, "195"},
		{`new TextEncoder().encode("\u00f3")[1]`, "179"},
		{`new TextEncoder().encode("\u00f3").byteLength`, "2"},
		{`new TextEncoder().encode("hello").length`, "5"},
		{`new TextEncoder().encoding`, "utf-8"},
	})
}

// The decoder's label selects the encoding; ignoring it turns every non-UTF-8
// byte into U+FFFD.
func TestTextDecoderHonoursEncodingLabel(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	checkAll(t, p, []struct{ expr, want string }{
		{`new TextDecoder().encoding`, "utf-8"},
		{`new TextDecoder().decode(new TextEncoder().encode("\u00f3"))`, "\u00f3"},
		{`new TextDecoder("latin1").encoding`, "windows-1252"},
		{`new TextDecoder("latin1").decode(new Uint8Array([0xF3]))`, "\u00f3"},
		{`new TextDecoder("windows-1252").decode(new Uint8Array([0x41]))`, "A"},
		// An unknown label falls back to UTF-8, as the Encoding Standard says.
		{`new TextDecoder("not-a-real-encoding").encoding`, "utf-8"},
	})
}
