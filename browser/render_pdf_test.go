package browser

import (
	"bytes"
	"testing"
)

func TestPDFIsAValidPDF(t *testing.T) {
	p := flexPage(t, `<html><body><h1>Title</h1><p>Some body text that should render.</p></body></html>`)
	pdf, err := p.PDF(ScreenshotOptions{Width: 400, NoImages: true})
	if err != nil {
		t.Fatalf("PDF: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-1.")) {
		t.Fatalf("pdf does not start with a PDF header: %q", pdf[:min(8, len(pdf))])
	}
	if !bytes.HasSuffix(bytes.TrimSpace(pdf), []byte("%%EOF")) {
		t.Fatalf("pdf does not end with EOF")
	}
	for _, want := range []string{"/Type /Catalog", "/Subtype /Image", "/FlateDecode", "startxref"} {
		if !bytes.Contains(pdf, []byte(want)) {
			t.Errorf("pdf is missing %q", want)
		}
	}
}

func TestPDFHasXref(t *testing.T) {
	p := flexPage(t, `<div style="background:#000;width:100px;height:40px"></div>`)
	pdf, err := p.PDF(ScreenshotOptions{Width: 320, NoImages: true})
	if err != nil {
		t.Fatalf("PDF: %v", err)
	}
	if !bytes.Contains(pdf, []byte("xref")) {
		t.Fatal("pdf has no xref table")
	}
}

func TestPDFScale(t *testing.T) {
	p := flexPage(t, `<p>hi</p>`)
	one, err := p.PDF(ScreenshotOptions{Width: 320, Scale: 1, NoImages: true})
	if err != nil {
		t.Fatalf("PDF: %v", err)
	}
	two, err := p.PDF(ScreenshotOptions{Width: 320, Scale: 2, NoImages: true})
	if err != nil {
		t.Fatalf("PDF: %v", err)
	}
	if len(two) <= len(one) {
		t.Fatalf("2x pdf (%d bytes) not larger than 1x (%d bytes)", len(two), len(one))
	}
}
