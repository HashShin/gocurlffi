package browser

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"math"
	"strconv"
	"strings"

	"github.com/dop251/goja"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"golang.org/x/net/html"
)

// A 2D canvas. Pages draw to a canvas to render charts, images and text
// client-side, and read the result back with toDataURL. The context implements
// the drawing operations pages actually use, backed by an image.RGBA, and
// toDataURL encodes it as a PNG. drawImage accepts another canvas or an <img>
// whose bytes were fetched, so a page can compose them.

type canvasState struct {
	img       *image.RGBA
	w, h      int
	ctx       *goja.Object
	fill      color.RGBA
	stroke    color.RGBA
	lineW     float64
	alpha     float64
	fontSize  float64
	textAlign string
	path      []pathSeg
}

type pathSeg struct {
	kind            string // move, line, arc, rect
	x, y, x2, y2, r float64
}

var defaultCanvasSize = [2]int{300, 150}

// webGLContext returns a WebGL context for a canvas. A browser always has one -
// even headless Chrome runs ANGLE - so getContext("webgl") answering null is a
// direct tell that the environment is not a browser. Only the surface a
// fingerprinting script reads is modelled: the parameter table, the extension
// list, and no-op entry points for the rest.
func (e *jsEnv) webGLContext(n *html.Node, major int) *goja.Object {
	o := e.vm.NewObject()
	_ = o.Set("canvas", e.wrap(n))
	_ = o.Set("drawingBufferWidth", e.canvasFor(n).w)
	_ = o.Set("drawingBufferHeight", e.canvasFor(n).h)
	// A browser reports the specific context interface, not [object Object].
	tag := "WebGLRenderingContext"
	if major == 2 {
		tag = "WebGL2RenderingContext"
	}
	e.tagObject(o, tag)
	_ = o.DefineDataProperty("constructor", e.namedConstructor(tag, nil),
		goja.FLAG_TRUE, goja.FLAG_FALSE, goja.FLAG_TRUE)

	versionStr := "WebGL 1.0 (OpenGL ES 2.0 Chromium)"
	glslStr := "WebGL GLSL ES 1.0 (OpenGL ES GLSL ES 1.0 Chromium)"
	if major == 2 {
		versionStr = "WebGL 2.0 (OpenGL ES 3.0 Chromium)"
		glslStr = "WebGL GLSL ES 3.00 (OpenGL ES GLSL ES 3.0 Chromium)"
	}

	const (
		cVendor    = 0x1F00
		cRenderer  = 0x1F01
		cVersion   = 0x1F02
		cGLSL      = 0x8B8C
		cMaxView   = 0x0D3A
		cUnmaskedV = 0x9245
		cUnmaskedR = 0x9246
	)

	for name, val := range map[string]int{
		"VENDOR": cVendor, "RENDERER": cRenderer, "VERSION": cVersion,
		"SHADING_LANGUAGE_VERSION": cGLSL, "MAX_VIEWPORT_DIMS": cMaxView,
		"MAX_TEXTURE_SIZE": 0x0D33, "MAX_RENDERBUFFER_SIZE": 0x84E8,
		"MAX_CUBE_MAP_TEXTURE_SIZE": 0x851C, "MAX_VERTEX_ATTRIBS": 0x8869,
		"MAX_VARYING_VECTORS": 0x8DF6, "MAX_FRAGMENT_UNIFORM_VECTORS": 0x8DFD,
		"MAX_TEXTURE_IMAGE_UNITS": 0x8872, "MAX_COMBINED_TEXTURE_IMAGE_UNITS": 0x8B4D,
		"MAX_VERTEX_TEXTURE_IMAGE_UNITS": 0x8B4C, "MAX_VERTEX_UNIFORM_VECTORS": 0x8DFB,
		"NO_ERROR": 0, "DEPTH_TEST": 0x0B71, "TEXTURE_2D": 0x0DE1,
		"COLOR_BUFFER_BIT": 0x4000, "TRIANGLES": 0x0004, "ARRAY_BUFFER": 0x8892,
		"STATIC_DRAW": 0x88E4, "FLOAT": 0x1406, "UNSIGNED_SHORT": 0x1403,
		"FRAGMENT_SHADER": 0x8B30, "VERTEX_SHADER": 0x8B31,
		"COMPILE_STATUS": 0x8B81, "LINK_STATUS": 0x8B82,
	} {
		_ = o.Set(name, val)
	}

	_ = o.Set("getParameter", func(call goja.FunctionCall) goja.Value {
		switch int(call.Argument(0).ToInteger()) {
		case cVendor:
			return e.vm.ToValue("WebKit")
		case cRenderer:
			return e.vm.ToValue("WebKit WebGL")
		case cVersion:
			return e.vm.ToValue(versionStr)
		case cGLSL:
			return e.vm.ToValue(glslStr)
		case cMaxView:
			return e.vm.NewArray(16384, 16384)
		case cUnmaskedV:
			return e.vm.ToValue("Google Inc. (Google)")
		case cUnmaskedR:
			return e.vm.ToValue("ANGLE (Google, Vulkan 1.3.0 (SwiftShader Device (Subzero) (0x0000C0DE)), SwiftShader driver)")
		}
		return e.vm.ToValue(16384)
	})
	_ = o.Set("getExtension", func(call goja.FunctionCall) goja.Value {
		switch argString(call.Argument(0)) {
		case "WEBGL_debug_renderer_info":
			ext := e.vm.NewObject()
			_ = ext.Set("UNMASKED_VENDOR_WEBGL", cUnmaskedV)
			_ = ext.Set("UNMASKED_RENDERER_WEBGL", cUnmaskedR)
			return ext
		case "WEBGL_lose_context":
			ext := e.vm.NewObject()
			_ = ext.Set("loseContext", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
			_ = ext.Set("restoreContext", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
			return ext
		}
		return goja.Null()
	})
	_ = o.Set("getSupportedExtensions", func(goja.FunctionCall) goja.Value {
		return e.vm.NewArray(
			"ANGLE_instanced_arrays", "EXT_blend_minmax", "EXT_color_buffer_half_float",
			"EXT_float_blend", "EXT_frag_depth", "EXT_shader_texture_lod",
			"EXT_texture_compression_bptc", "EXT_texture_compression_rgtc",
			"EXT_texture_filter_anisotropic", "OES_element_index_uint",
			"OES_fbo_render_mipmap", "OES_standard_derivatives", "OES_texture_float",
			"OES_texture_float_linear", "OES_vertex_array_object",
			"WEBGL_color_buffer_float", "WEBGL_compressed_texture_s3tc",
			"WEBGL_debug_renderer_info", "WEBGL_debug_shaders", "WEBGL_depth_texture",
			"WEBGL_draw_buffers", "WEBGL_lose_context", "WEBGL_multi_draw")
	})
	// Every remaining entry point answers, so a probe that calls one does not
	// throw. getError reports NO_ERROR, which is what an idle context returns.
	for _, m := range []string{
		"activeTexture", "attachShader", "bindAttribLocation", "bindBuffer",
		"bindFramebuffer", "bindRenderbuffer", "bindTexture", "blendColor",
		"blendEquation", "blendFunc", "bufferData", "bufferSubData", "clear",
		"clearColor", "clearDepth", "clearStencil", "colorMask", "compileShader",
		"createBuffer", "createFramebuffer", "createProgram", "createRenderbuffer",
		"createShader", "createTexture", "cullFace", "deleteBuffer",
		"deleteFramebuffer", "deleteProgram", "deleteRenderbuffer", "deleteShader",
		"deleteTexture", "depthFunc", "depthMask", "detachShader", "disable",
		"disableVertexAttribArray", "drawArrays", "drawElements", "enable",
		"enableVertexAttribArray", "finish", "flush", "frontFace", "generateMipmap",
		"getAttribLocation", "getProgramInfoLog", "getShaderInfoLog",
		"getShaderSource", "getUniformLocation", "lineWidth", "linkProgram",
		"pixelStorei", "polygonOffset", "readPixels", "renderbufferStorage",
		"scissor", "shaderSource", "stencilFunc", "stencilMask", "stencilOp",
		"texImage2D", "texParameterf", "texParameteri", "uniform1f", "uniform1i",
		"uniform2f", "uniform3f", "uniform4f", "uniformMatrix3fv",
		"uniformMatrix4fv", "useProgram", "validateProgram", "vertexAttribPointer",
		"viewport",
	} {
		_ = o.Set(m, func(goja.FunctionCall) goja.Value { return goja.Null() })
	}
	_ = o.Set("getError", func(goja.FunctionCall) goja.Value { return e.vm.ToValue(0) })
	_ = o.Set("getProgramParameter", func(goja.FunctionCall) goja.Value { return e.vm.ToValue(true) })
	_ = o.Set("getShaderParameter", func(goja.FunctionCall) goja.Value { return e.vm.ToValue(true) })
	return o
}

// installCanvas adds canvas support to the element prototype.
func (e *jsEnv) installCanvas(p *goja.Object) {
	e.method(p, "getContext", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n == nil || n.Data != "canvas" {
			return goja.Null()
		}
		switch call.Argument(0).String() {
		case "2d":
			return e.canvasContext(n)
		case "webgl", "experimental-webgl":
			return e.webGLContext(n, 1)
		case "webgl2":
			return e.webGLContext(n, 2)
		}
		return goja.Null()
	})
	e.accessor(p, "width", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n == nil || n.Data != "canvas" {
			return e.vm.ToValue(0)
		}
		return e.vm.ToValue(e.canvasFor(n).w)
	}, func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n == nil || n.Data != "canvas" {
			return goja.Undefined()
		}
		e.resizeCanvas(n, int(call.Argument(0).ToInteger()), -1)
		return goja.Undefined()
	})
	e.accessor(p, "height", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n == nil || n.Data != "canvas" {
			return e.vm.ToValue(0)
		}
		return e.vm.ToValue(e.canvasFor(n).h)
	}, func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n == nil || n.Data != "canvas" {
			return goja.Undefined()
		}
		e.resizeCanvas(n, -1, int(call.Argument(0).ToInteger()))
		return goja.Undefined()
	})
	e.method(p, "toDataURL", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n == nil || n.Data != "canvas" {
			return e.vm.ToValue("data:,")
		}
		cs := e.canvasFor(n)
		var buf bytes.Buffer
		if err := png.Encode(&buf, cs.img); err != nil {
			return e.vm.ToValue("data:,")
		}
		return e.vm.ToValue("data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()))
	})
}

// canvasFor returns the canvas state for a node, creating it on first use.
func (e *jsEnv) canvasFor(n *html.Node) *canvasState {
	if e.canvases == nil {
		e.canvases = map[*html.Node]*canvasState{}
	}
	if cs, ok := e.canvases[n]; ok {
		return cs
	}
	w, h := defaultCanvasSize[0], defaultCanvasSize[1]
	if v, ok := getAttr(n, "width"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			w = n
		}
	}
	if v, ok := getAttr(n, "height"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			h = n
		}
	}
	cs := &canvasState{
		img: image.NewRGBA(image.Rect(0, 0, w, h)), w: w, h: h,
		fill: color.RGBA{0, 0, 0, 255}, stroke: color.RGBA{0, 0, 0, 255},
		lineW: 1, alpha: 1, fontSize: 10, textAlign: "start",
	}
	e.canvases[n] = cs
	return cs
}

func (e *jsEnv) resizeCanvas(n *html.Node, w, h int) {
	cs := e.canvasFor(n)
	if w < 0 {
		w = cs.w
	}
	if h < 0 {
		h = cs.h
	}
	if w <= 0 {
		w = 1
	}
	if h <= 0 {
		h = 1
	}
	cs.w, cs.h = w, h
	cs.img = image.NewRGBA(image.Rect(0, 0, w, h))
}

// canvasContext builds the 2D context object for a canvas node.
func (e *jsEnv) canvasContext(n *html.Node) goja.Value {
	cs := e.canvasFor(n)
	if cs.ctx != nil {
		return cs.ctx
	}
	o := e.vm.NewObject()

	setColor := func(name string, dst *color.RGBA) {
		e.accessor(o, name, func(call goja.FunctionCall) goja.Value {
			_ = call
			return e.vm.ToValue(cssColorString(*dst))
		}, func(call goja.FunctionCall) goja.Value {
			if c, ok := parseCSSColor(argString(call.Argument(0))); ok {
				*dst = c
			}
			return goja.Undefined()
		})
	}
	setColor("fillStyle", &cs.fill)
	setColor("strokeStyle", &cs.stroke)

	e.accessor(o, "lineWidth", func(goja.FunctionCall) goja.Value { return e.vm.ToValue(cs.lineW) },
		func(call goja.FunctionCall) goja.Value {
			cs.lineW = call.Argument(0).ToFloat()
			return goja.Undefined()
		})
	e.accessor(o, "globalAlpha", func(goja.FunctionCall) goja.Value { return e.vm.ToValue(cs.alpha) },
		func(call goja.FunctionCall) goja.Value {
			cs.alpha = call.Argument(0).ToFloat()
			return goja.Undefined()
		})
	e.accessor(o, "font", func(goja.FunctionCall) goja.Value {
		return e.vm.ToValue(strconv.Itoa(int(cs.fontSize)) + "px sans-serif")
	},
		func(call goja.FunctionCall) goja.Value {
			cs.fontSize = parseFontSize(call.Argument(0).String())
			return goja.Undefined()
		})
	e.accessor(o, "textAlign", func(goja.FunctionCall) goja.Value { return e.vm.ToValue(cs.textAlign) },
		func(call goja.FunctionCall) goja.Value {
			cs.textAlign = argString(call.Argument(0))
			return goja.Undefined()
		})

	e.method(o, "fillRect", func(call goja.FunctionCall) goja.Value {
		cs.fillRect(call.Argument(0).ToFloat(), call.Argument(1).ToFloat(), call.Argument(2).ToFloat(), call.Argument(3).ToFloat(), cs.fill, cs.alpha)
		return goja.Undefined()
	})
	e.method(o, "clearRect", func(call goja.FunctionCall) goja.Value {
		cs.clearRect(call.Argument(0).ToFloat(), call.Argument(1).ToFloat(), call.Argument(2).ToFloat(), call.Argument(3).ToFloat())
		return goja.Undefined()
	})
	e.method(o, "strokeRect", func(call goja.FunctionCall) goja.Value {
		x, y, w, h := call.Argument(0).ToFloat(), call.Argument(1).ToFloat(), call.Argument(2).ToFloat(), call.Argument(3).ToFloat()
		t := cs.lineW
		cs.fillRect(x, y, w, t, cs.stroke, cs.alpha)
		cs.fillRect(x, y+h-t, w, t, cs.stroke, cs.alpha)
		cs.fillRect(x, y, t, h, cs.stroke, cs.alpha)
		cs.fillRect(x+w-t, y, t, h, cs.stroke, cs.alpha)
		return goja.Undefined()
	})
	e.method(o, "fillText", func(call goja.FunctionCall) goja.Value {
		cs.drawText(argString(call.Argument(0)), call.Argument(1).ToFloat(), call.Argument(2).ToFloat(), cs.fill, cs.alpha)
		return goja.Undefined()
	})
	e.method(o, "strokeText", func(call goja.FunctionCall) goja.Value {
		cs.drawText(argString(call.Argument(0)), call.Argument(1).ToFloat(), call.Argument(2).ToFloat(), cs.stroke, cs.alpha)
		return goja.Undefined()
	})
	e.method(o, "measureText", func(call goja.FunctionCall) goja.Value {
		width := float64(font.MeasureString(basicfont.Face7x13, argString(call.Argument(0)))) / 64
		m := e.vm.NewObject()
		_ = m.Set("width", width)
		return m
	})
	e.method(o, "beginPath", func(goja.FunctionCall) goja.Value { cs.path = nil; return goja.Undefined() })
	e.method(o, "closePath", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	e.method(o, "moveTo", func(call goja.FunctionCall) goja.Value {
		cs.path = append(cs.path, pathSeg{kind: "move", x: call.Argument(0).ToFloat(), y: call.Argument(1).ToFloat()})
		return goja.Undefined()
	})
	e.method(o, "lineTo", func(call goja.FunctionCall) goja.Value {
		cs.path = append(cs.path, pathSeg{kind: "line", x: call.Argument(0).ToFloat(), y: call.Argument(1).ToFloat()})
		return goja.Undefined()
	})
	e.method(o, "rect", func(call goja.FunctionCall) goja.Value {
		cs.path = append(cs.path, pathSeg{kind: "rect", x: call.Argument(0).ToFloat(), y: call.Argument(1).ToFloat(), x2: call.Argument(2).ToFloat(), y2: call.Argument(3).ToFloat()})
		return goja.Undefined()
	})
	e.method(o, "arc", func(call goja.FunctionCall) goja.Value {
		cs.path = append(cs.path, pathSeg{kind: "arc", x: call.Argument(0).ToFloat(), y: call.Argument(1).ToFloat(), r: call.Argument(2).ToFloat()})
		return goja.Undefined()
	})
	e.method(o, "fill", func(goja.FunctionCall) goja.Value { cs.fillPath(cs.fill, cs.alpha); return goja.Undefined() })
	e.method(o, "stroke", func(goja.FunctionCall) goja.Value {
		cs.strokePath(cs.stroke, cs.lineW, cs.alpha)
		return goja.Undefined()
	})
	e.method(o, "save", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	e.method(o, "restore", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	for _, name := range []string{"translate", "scale", "rotate", "setTransform", "transform", "clip", "setLineDash", "createLinearGradient", "createRadialGradient"} {
		name := name
		e.method(o, name, func(call goja.FunctionCall) goja.Value {
			if name == "createLinearGradient" || name == "createRadialGradient" {
				g := e.vm.NewObject()
				_ = g.Set("addColorStop", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
				return g
			}
			return goja.Undefined()
		})
	}
	e.method(o, "drawImage", func(call goja.FunctionCall) goja.Value {
		src := e.nodeArg(call.Argument(0))
		if src == nil {
			return goja.Undefined()
		}
		dx, dy := call.Argument(1).ToFloat(), call.Argument(2).ToFloat()
		srcCS, ok := e.canvases[src]
		if !ok {
			return goja.Undefined()
		}
		cs.drawImage(srcCS, dx, dy)
		return goja.Undefined()
	})

	cs.ctx = o
	return o
}

// fillRect blends a rectangle of a solid colour into the canvas.
func (cs *canvasState) fillRect(x, y, w, h float64, c color.RGBA, alpha float64) {
	x0, y0 := int(math.Round(x)), int(math.Round(y))
	x1, y1 := int(math.Round(x+w)), int(math.Round(y+h))
	if x1 < x0 {
		x0, x1 = x1, x0
	}
	if y1 < y0 {
		y0, y1 = y1, y0
	}
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > cs.w {
		x1 = cs.w
	}
	if y1 > cs.h {
		y1 = cs.h
	}
	c = scaleAlpha(c, alpha)
	for py := y0; py < y1; py++ {
		for px := x0; px < x1; px++ {
			cs.blend(px, py, c)
		}
	}
}

// clearRect erases a rectangle to fully transparent.
func (cs *canvasState) clearRect(x, y, w, h float64) {
	x0, y0 := int(math.Round(x)), int(math.Round(y))
	x1, y1 := int(math.Round(x+w)), int(math.Round(y+h))
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > cs.w {
		x1 = cs.w
	}
	if y1 > cs.h {
		y1 = cs.h
	}
	for py := y0; py < y1; py++ {
		for px := x0; px < x1; px++ {
			i := cs.img.PixOffset(px, py)
			cs.img.Pix[i], cs.img.Pix[i+1], cs.img.Pix[i+2], cs.img.Pix[i+3] = 0, 0, 0, 0
		}
	}
}

func (cs *canvasState) blend(x, y int, c color.RGBA) {
	if x < 0 || y < 0 || x >= cs.w || y >= cs.h {
		return
	}
	i := cs.img.PixOffset(x, y)
	if c.A == 255 {
		cs.img.Pix[i] = c.R
		cs.img.Pix[i+1] = c.G
		cs.img.Pix[i+2] = c.B
		cs.img.Pix[i+3] = 255
		return
	}
	if c.A == 0 {
		return
	}
	a := float64(c.A) / 255
	blend := func(dst, src uint8) uint8 {
		return uint8(float64(src)*a + float64(dst)*(1-a) + 0.5)
	}
	cs.img.Pix[i] = blend(cs.img.Pix[i], c.R)
	cs.img.Pix[i+1] = blend(cs.img.Pix[i+1], c.G)
	cs.img.Pix[i+2] = blend(cs.img.Pix[i+2], c.B)
	cs.img.Pix[i+3] = 255
}

// drawText draws a string with the embedded 7x13 font at a baseline.
func (cs *canvasState) drawText(s string, x, y float64, c color.RGBA, alpha float64) {
	if s == "" {
		return
	}
	width := font.MeasureString(basicfont.Face7x13, s)
	if cs.textAlign == "center" {
		x -= float64(width) / 64 / 2
	} else if cs.textAlign == "right" || cs.textAlign == "end" {
		x -= float64(width) / 64
	}
	// basicfont only draws one colour, so render to an alpha mask first.
	mask := image.NewAlpha(image.Rect(0, 0, int(math.Ceil(float64(width)/64))+1, 13))
	d := font.Drawer{Dst: mask, Src: image.NewUniform(color.Alpha{255}), Face: basicfont.Face7x13, Dot: fixed.P(0, 11)}
	d.DrawString(s)
	c = scaleAlpha(c, alpha)
	mx, my := int(math.Round(x)), int(math.Round(y))
	b := mask.Bounds()
	for py := b.Min.Y; py < b.Max.Y; py++ {
		for px := b.Min.X; px < b.Max.X; px++ {
			if mask.AlphaAt(px, py).A == 0 {
				continue
			}
			cs.blend(mx+px, my-11+py, c)
		}
	}
}

func (cs *canvasState) fillPath(c color.RGBA, alpha float64) {
	var cur *pathSeg
	for i := range cs.path {
		seg := cs.path[i]
		switch seg.kind {
		case "rect":
			cs.fillRect(seg.x, seg.y, seg.x2, seg.y2, c, alpha)
		case "arc":
			cs.fillCircle(seg.x, seg.y, seg.r, c, alpha)
		case "move":
			s := seg
			cur = &s
		case "line":
			if cur != nil {
				cs.line(cur.x, cur.y, seg.x, seg.y, c, 1)
			}
			s := seg
			cur = &s
		}
	}
}

func (cs *canvasState) strokePath(c color.RGBA, width, alpha float64) {
	cs.fillPath(c, alpha)
}

func (cs *canvasState) fillCircle(cx, cy, r float64, c color.RGBA, alpha float64) {
	for py := int(cy - r); py <= int(cy+r); py++ {
		for px := int(cx - r); px <= int(cx+r); px++ {
			dx, dy := float64(px)-cx, float64(py)-cy
			if dx*dx+dy*dy <= r*r {
				cs.blend(px, py, scaleAlpha(c, alpha))
			}
		}
	}
}

func (cs *canvasState) line(x0, y0, x1, y1 float64, c color.RGBA, alpha float64) {
	steps := int(math.Max(math.Abs(x1-x0), math.Abs(y1-y0))) + 1
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		cs.blend(int(math.Round(x0+(x1-x0)*t)), int(math.Round(y0+(y1-y0)*t)), scaleAlpha(c, alpha))
	}
}

func (cs *canvasState) drawImage(src *canvasState, dx, dy float64) {
	b := src.img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			i := src.img.PixOffset(x, y)
			c := color.RGBA{src.img.Pix[i], src.img.Pix[i+1], src.img.Pix[i+2], src.img.Pix[i+3]}
			cs.blend(int(dx)+x, int(dy)+y, c)
		}
	}
}

// parseFontSize reads the pixel size out of a CSS font shorthand.
func parseFontSize(s string) float64 {
	for _, part := range strings.Fields(s) {
		if strings.HasSuffix(part, "px") {
			if v, err := strconv.ParseFloat(strings.TrimSuffix(part, "px"), 64); err == nil {
				return v
			}
		}
	}
	return 10
}
