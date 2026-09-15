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

// installCanvas adds canvas support to the element prototype.
func (e *jsEnv) installCanvas(p *goja.Object) {
	e.method(p, "getContext", func(call goja.FunctionCall) goja.Value {
		n := e.thisNode(call)
		if n == nil || n.Data != "canvas" {
			return goja.Null()
		}
		if call.Argument(0).String() != "2d" {
			return goja.Null()
		}
		return e.canvasContext(n)
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
