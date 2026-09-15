package browser

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/png"
)

// PDF output. The renderer produces a picture, so the PDF is that picture on a
// single page: the page is laid out and rendered exactly as Screenshot would,
// then embedded as an image object. This is the same class of output the Zig
// reference produces (a text-only rendering of the page), here a faithful
// rendering of what the renderer drew.

// PDF renders the page and returns a PDF document containing it. The options
// are the same as Screenshot's; Width, Scale and MaxHeight control the layout
// and the image, and NoImages turns off drawing the page's pictures.
func (p *Page) PDF(opts ScreenshotOptions) ([]byte, error) {
	pngBytes, err := p.Screenshot(opts)
	if err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, fmt.Errorf("browser: decode render for pdf: %w", err)
	}
	return imageToPDF(img)
}

// imageToPDF wraps an image in a one-page PDF, embedding the pixels as a
// Flate-compressed DeviceRGB image XObject.
func imageToPDF(img image.Image) ([]byte, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("browser: empty image for pdf")
	}

	// Raw RGB rows, top to bottom, then one zlib stream for the whole thing.
	raw := make([]byte, 0, w*h*3)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			raw = append(raw, byte(r>>8), byte(g>>8), byte(bl>>8))
		}
	}
	var comp bytes.Buffer
	zw := zlib.NewWriter(&comp)
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}

	// Content stream: scale the unit square to the image size and paint it.
	content := fmt.Sprintf("q\n%d 0 0 %d 0 0 cm\n/Im0 Do\nQ\n", w, h)

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")

	offsets := make([]int, 0, 6)
	writeObj := func(n int, body string) {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", n, body)
	}
	writeStreamObj := func(n int, dict string, data []byte) {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n<<%s /Length %d>>\nstream\n", n, dict, len(data))
		out.Write(data)
		out.WriteString("\nendstream\nendobj\n")
	}

	// 1 catalog, 2 pages, 3 page, 4 image, 5 contents. offsets[0] is unused
	// because PDF object numbers start at 1.
	offsets = append(offsets, 0)
	writeObj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	writeObj(3, fmt.Sprintf(
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] "+
			"/Resources << /XObject << /Im0 4 0 R >> >> /Contents 5 0 R >>", w, h))
	writeStreamObj(4, fmt.Sprintf(
		"/Type /XObject /Subtype /Image /Width %d /Height %d "+
			"/ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode", w, h), comp.Bytes())
	writeStreamObj(5, "", []byte(content))

	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n", len(offsets))
	out.WriteString("0000000000 65535 f \n")
	for i := 1; i < len(offsets); i++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return out.Bytes(), nil
}
