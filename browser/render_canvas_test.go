package browser

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"strings"
	"testing"
)

func canvasEval(t *testing.T, p *Page, script string) string {
	t.Helper()
	v, err := p.Eval(script)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	return v.String()
}

func decodeDataURL(t *testing.T, dataURL string) []byte {
	t.Helper()
	i := strings.Index(dataURL, "base64,")
	if i < 0 {
		t.Fatalf("not a base64 data URL: %q", dataURL)
	}
	b, err := base64.StdEncoding.DecodeString(dataURL[i+len("base64,"):])
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	return b
}

func TestCanvasFillRectToDataURL(t *testing.T) {
	p := flexPage(t, `<canvas id="c" width="4" height="4"></canvas>`)
	url := canvasEval(t, p, `(function(){
		var c = document.getElementById('c');
		var ctx = c.getContext('2d');
		ctx.fillStyle = '#ff0000';
		ctx.fillRect(0, 0, 4, 4);
		return c.toDataURL();
	})()`)
	img, err := png.Decode(bytes.NewReader(decodeDataURL(t, url)))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	r, g, b, _ := img.At(2, 2).RGBA()
	if r>>8 != 255 || g>>8 != 0 || b>>8 != 0 {
		t.Fatalf("pixel = %d,%d,%d, want red", r>>8, g>>8, b>>8)
	}
}

func TestCanvasSizeFromProperties(t *testing.T) {
	p := flexPage(t, `<canvas id="c"></canvas>`)
	got := canvasEval(t, p, `(function(){
		var c = document.getElementById('c');
		c.width = 8; c.height = 6;
		return c.width + 'x' + c.height;
	})()`)
	if got != "8x6" {
		t.Fatalf("size = %q, want 8x6", got)
	}
}

func TestCanvasClearRect(t *testing.T) {
	p := flexPage(t, `<canvas id="c" width="4" height="4"></canvas>`)
	url := canvasEval(t, p, `(function(){
		var c = document.getElementById('c');
		var ctx = c.getContext('2d');
		ctx.fillStyle = '#0000ff';
		ctx.fillRect(0, 0, 4, 4);
		ctx.clearRect(0, 0, 2, 4);
		return c.toDataURL();
	})()`)
	img, _ := png.Decode(bytes.NewReader(decodeDataURL(t, url)))
	r, g, b, a := img.At(0, 0).RGBA()
	if a != 0 {
		t.Fatalf("cleared pixel alpha = %d, want 0 (r,g,b=%d,%d,%d)", a>>8, r>>8, g>>8, b>>8)
	}
	r, g, b, _ = img.At(3, 0).RGBA()
	if b>>8 != 255 || r>>8 != 0 {
		t.Fatalf("uncleared pixel = %d,%d,%d, want blue", r>>8, g>>8, b>>8)
	}
}

func TestCanvasFillText(t *testing.T) {
	p := flexPage(t, `<canvas id="c" width="60" height="20"></canvas>`)
	url := canvasEval(t, p, `(function(){
		var c = document.getElementById('c');
		var ctx = c.getContext('2d');
		ctx.fillStyle = '#000';
		ctx.fillText('Hi', 2, 15);
		return c.toDataURL();
	})()`)
	img, _ := png.Decode(bytes.NewReader(decodeDataURL(t, url)))
	ink := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if r, _, _, a := img.At(x, y).RGBA(); a > 0 && r < 0xffff {
				ink++
			}
		}
	}
	if ink == 0 {
		t.Fatal("fillText drew nothing")
	}
}

func TestCanvasGetContextNonCanvas(t *testing.T) {
	p := flexPage(t, `<div id="d"></div>`)
	v, err := p.Eval(`document.getElementById('d').getContext('2d')`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v.Export() != nil {
		t.Fatalf("getContext on a div = %v, want null", v.Export())
	}
}

func TestCanvasDrawImage(t *testing.T) {
	p := flexPage(t, `<canvas id="a" width="2" height="2"></canvas><canvas id="b" width="4" height="4"></canvas>`)
	url := canvasEval(t, p, `(function(){
		var a = document.getElementById('a'), b = document.getElementById('b');
		var ca = a.getContext('2d'), cb = b.getContext('2d');
		ca.fillStyle = '#00ff00'; ca.fillRect(0,0,2,2);
		cb.drawImage(a, 1, 1);
		return b.toDataURL();
	})()`)
	img, _ := png.Decode(bytes.NewReader(decodeDataURL(t, url)))
	r, g, b, _ := img.At(1, 1).RGBA()
	if g>>8 != 255 || r>>8 != 0 {
		t.Fatalf("drawn pixel = %d,%d,%d, want green", r>>8, g>>8, b>>8)
	}
}
