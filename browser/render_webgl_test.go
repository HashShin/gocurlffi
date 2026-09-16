package browser

import "testing"

// getContext("webgl") answering null is a direct tell: a browser always has a
// WebGL context, even headless Chrome via ANGLE.
func TestWebGLContextExists(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	checkAll(t, p, []struct{ expr, want string }{
		{`document.createElement("canvas").getContext("webgl") !== null`, "true"},
		{`document.createElement("canvas").getContext("experimental-webgl") !== null`, "true"},
		{`document.createElement("canvas").getContext("webgl2") !== null`, "true"},
		{`Object.prototype.toString.call(document.createElement("canvas").getContext("webgl"))`, "[object WebGLRenderingContext]"},
		{`Object.prototype.toString.call(document.createElement("canvas").getContext("webgl2"))`, "[object WebGL2RenderingContext]"},
		{`typeof WebGLRenderingContext`, "function"},
		{`typeof WebGL2RenderingContext`, "function"},
		{`document.createElement("canvas").getContext("webgl").constructor.name`, "WebGLRenderingContext"},
	})
}

func TestWebGLParametersLookReal(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	checkAll(t, p, []struct{ expr, want string }{
		{`(function () { var g = document.createElement("canvas").getContext("webgl"); return g.getParameter(g.VENDOR); })()`, "WebKit"},
		{`(function () { var g = document.createElement("canvas").getContext("webgl"); return g.getParameter(g.RENDERER); })()`, "WebKit WebGL"},
		{`(function () { var g = document.createElement("canvas").getContext("webgl"); return g.getParameter(g.VERSION); })()`, "WebGL 1.0 (OpenGL ES 2.0 Chromium)"},
		{`(function () { var g = document.createElement("canvas").getContext("webgl"); return g.getParameter(g.SHADING_LANGUAGE_VERSION); })()`, "WebGL GLSL ES 1.0 (OpenGL ES GLSL ES 1.0 Chromium)"},
		{`(function () { var g = document.createElement("canvas").getContext("webgl"); return g.getSupportedExtensions().length > 10; })()`, "true"},
		{`(function () { var g = document.createElement("canvas").getContext("webgl"); var e = g.getExtension("WEBGL_debug_renderer_info"); return g.getParameter(e.UNMASKED_RENDERER_WEBGL).indexOf("ANGLE") === 0; })()`, "true"},
		{`(function () { var g = document.createElement("canvas").getContext("webgl"); return g.getExtension("nope") === null; })()`, "true"},
		{`(function () { var g = document.createElement("canvas").getContext("webgl"); return g.getError(); })()`, "0"},
		// An entry point must answer rather than throw.
		{`(function () { var g = document.createElement("canvas").getContext("webgl"); g.viewport(0,0,1,1); g.clear(g.COLOR_BUFFER_BIT); return "ok"; })()`, "ok"},
	})
}
