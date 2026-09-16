package browser

import (
	"bytes"
	"compress/zlib"
	"errors"
	"io"
	"strconv"
	"strings"

	"golang.org/x/image/font/opentype"
)

// Web fonts: the page's own @font-face rules, used in preference to the
// embedded Go fonts when a run's computed family matches one. Only a screenshot
// resolves and loads them, so loading a page pays nothing for this.
//
// The decoder is golang.org/x/image/font/opentype, which reads raw sfnt (TTF
// and OTF). A WOFF file is unpacked into sfnt first, which needs nothing but
// zlib: brave.com serves its whole typeface set as WOFF, and without this every
// run on the page is drawn in the embedded face instead. WOFF2 is recorded but
// not decoded - it needs Brotli and its own table transforms - so a face that
// is only available that way is reported and skipped rather than silently
// substituted.

// addFontFaces records the page's @font-face rules, keeping the first for each
// (family, weight, slope). Document order decides, which is what a browser does
// with duplicate descriptors.
func (p *Page) addFontFaces(faces []fontFace) {
	base := ""
	if p.doc != nil {
		base = p.baseURL()
	}
	for _, f := range faces {
		if base != "" {
			f.src = resolveURL(base, f.src)
		}
		if p.hasFontFace(f) {
			continue
		}
		p.fontFaces = append(p.fontFaces, f)
	}
}

func (p *Page) hasFontFace(f fontFace) bool {
	for _, ex := range p.fontFaces {
		if strings.EqualFold(ex.family, f.family) && ex.weight == f.weight && ex.italic == f.italic {
			return true
		}
	}
	return false
}

// pageFont returns the page font that best covers a computed family, weight and
// slope, or nil to fall back to the embedded Go fonts. It never fails loudly:
// a font that cannot be fetched or decoded is reported once under --debug and
// the run is drawn with the built-in face.
func (p *Page) pageFont(family string, weight int, italic bool) *webFont {
	if family == "" || len(p.fontFaces) == 0 {
		return nil
	}
	best, bestScore := -1, 1<<30
	for i, f := range p.fontFaces {
		if !strings.EqualFold(f.family, family) {
			continue
		}
		score := 0
		if f.italic != italic {
			// Italic text may fall back to an upright face; an upright run
			// must not be drawn in an italic one.
			if !italic || f.italic {
				continue
			}
			score += 1000
		}
		score += absInt(f.weight - weight)
		if score < bestScore {
			bestScore, best = score, i
		}
	}
	if best < 0 {
		return nil
	}
	face := p.fontFaces[best]
	if fontFormatRank(face.format, face.src) > 2 {
		// WOFF2 needs Brotli and its own table transforms, so the file is not
		// even fetched: it would only be discarded.
		p.failFont(face.src, "format "+face.format+" is not decodable")
		return nil
	}
	return p.loadFontFile(face.src)
}

// loadFontFile fetches and parses one font file, caching both outcomes by URL.
func (p *Page) loadFontFile(url string) *webFont {
	if f, ok := p.fontLoaded[url]; ok {
		return f
	}
	if p.fontFailed[url] {
		return nil
	}
	resp, err := p.browser.fetch(url, nil, "font")
	if err != nil {
		p.failFont(url, "fetch: "+err.Error())
		return nil
	}
	if resp.StatusCode >= 400 {
		p.failFont(url, "HTTP "+strconv.Itoa(resp.StatusCode))
		return nil
	}
	raw := resp.Content
	if isWOFF(raw) {
		packed, err := unpackWOFF(raw)
		if err != nil {
			p.failFont(url, "woff: "+err.Error())
			return nil
		}
		raw = packed
	}
	f, err := opentype.Parse(raw)
	if err != nil {
		p.failFont(url, "decode: "+err.Error())
		return nil
	}
	if p.fontLoaded == nil {
		p.fontLoaded = map[string]*webFont{}
	}
	p.fontLoaded[url] = f
	p.debugf("webfont: %d bytes from %s", len(resp.Content), url)
	return f
}

func (p *Page) failFont(url, reason string) {
	if p.fontFailed == nil {
		p.fontFailed = map[string]bool{}
	}
	if p.fontFailed[url] {
		return
	}
	p.fontFailed[url] = true
	p.debugf("webfont FAILED (%s): %s", reason, url)
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// --- WOFF ---

// A WOFF file is an sfnt whose tables are zlib-compressed and stored in one
// block, with a directory that says where each one is. Unpacking it is the
// whole format: rebuild the sfnt header and directory, decompress each table,
// and pad every table to a four-byte boundary.

// woffSignature is the first four bytes of a WOFF file, "wOFF".
const woffSignature = "wOFF"

// woffHeaderLen is the size of the WOFF header, and woffEntryLen the size of
// one table directory entry.
const (
	woffHeaderLen = 44
	woffEntryLen  = 20
)

// isWOFF reports whether data begins with the WOFF signature.
func isWOFF(data []byte) bool {
	return len(data) >= 4 && string(data[:4]) == woffSignature
}

// unpackWOFF converts a WOFF file into the sfnt (TTF/OTF) it wraps.
func unpackWOFF(data []byte) ([]byte, error) {
	if len(data) < woffHeaderLen {
		return nil, errors.New("shorter than a WOFF header")
	}
	flavor := be32(data[4:8])
	numTables := int(be16(data[12:14]))
	if numTables == 0 {
		return nil, errors.New("no tables")
	}
	if len(data) < woffHeaderLen+numTables*woffEntryLen {
		return nil, errors.New("truncated table directory")
	}

	type table struct {
		tag      [4]byte
		checksum uint32
		body     []byte
	}
	tables := make([]table, 0, numTables)
	total := 12 + 16*numTables
	for i := 0; i < numTables; i++ {
		off := woffHeaderLen + i*woffEntryLen
		e := data[off : off+woffEntryLen]
		var tag [4]byte
		copy(tag[:], e[0:4])
		start := int(be32(e[4:8]))
		compLen := int(be32(e[8:12]))
		origLen := int(be32(e[12:16]))
		checksum := be32(e[16:20])
		if start < woffHeaderLen || start+compLen > len(data) || compLen < 0 || origLen < 0 {
			return nil, errors.New("table " + string(tag[:]) + " is out of range")
		}
		body := data[start : start+compLen]
		if compLen != origLen {
			zr, err := zlib.NewReader(bytes.NewReader(body))
			if err != nil {
				return nil, errors.New("table " + string(tag[:]) + ": " + err.Error())
			}
			plain, err := io.ReadAll(zr)
			zr.Close()
			if err != nil {
				return nil, errors.New("table " + string(tag[:]) + ": " + err.Error())
			}
			if len(plain) != origLen {
				return nil, errors.New("table " + string(tag[:]) + " unpacked to the wrong size")
			}
			body = plain
		}
		tables = append(tables, table{tag: tag, checksum: checksum, body: body})
		total += (len(body) + 3) &^ 3
	}

	out := make([]byte, 12, total)
	be32Put(out[0:4], flavor)
	be16Put(out[4:6], uint16(numTables))
	// The search fields are derived, not stored: an sfnt reader uses them to
	// binary-search the directory, and a browser computes them the same way.
	entrySelector := 0
	for 1<<(entrySelector+1) <= numTables {
		entrySelector++
	}
	searchRange := 16 * (1 << entrySelector)
	be16Put(out[6:8], uint16(searchRange))
	be16Put(out[8:10], uint16(entrySelector))
	be16Put(out[10:12], uint16(numTables*16-searchRange))

	offset := 12 + 16*numTables
	dir := make([]byte, 0, 16*numTables)
	for _, t := range tables {
		var e [16]byte
		copy(e[0:4], t.tag[:])
		be32Put(e[4:8], t.checksum)
		be32Put(e[8:12], uint32(offset))
		// The length in the directory is the table's own, without the padding
		// that follows it in the file.
		be32Put(e[12:16], uint32(len(t.body)))
		dir = append(dir, e[:]...)
		offset += (len(t.body) + 3) &^ 3
	}
	out = append(out, dir...)
	for _, t := range tables {
		out = append(out, t.body...)
		for pad := (4 - len(t.body)%4) % 4; pad > 0; pad-- {
			out = append(out, 0)
		}
	}
	return out, nil
}

func be16(b []byte) uint16 { return uint16(b[0])<<8 | uint16(b[1]) }

func be32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func be16Put(b []byte, v uint16) {
	b[0], b[1] = byte(v>>8), byte(v)
}

func be32Put(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
}
