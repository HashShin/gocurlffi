package browser

import (
	"strconv"
	"strings"

	"golang.org/x/image/font/opentype"
)

// Web fonts: the page's own @font-face rules, used in preference to the
// embedded Go fonts when a run's computed family matches one. Only a screenshot
// resolves and loads them, so loading a page pays nothing for this.
//
// The decoder is golang.org/x/image/font/opentype, which reads raw sfnt (TTF
// and OTF). WOFF and WOFF2 files are recorded but not decoded: a face that is
// only available in those formats is reported and skipped rather than silently
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
	if fontFormatRank(face.format, face.src) > 1 {
		// WOFF and WOFF2 need a decompressor this renderer does not have, so
		// the file is not even fetched: it would only be discarded.
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
	resp, err := p.browser.get(url, nil)
	if err != nil {
		p.failFont(url, "fetch: "+err.Error())
		return nil
	}
	if resp.StatusCode >= 400 {
		p.failFont(url, "HTTP "+strconv.Itoa(resp.StatusCode))
		return nil
	}
	f, err := opentype.Parse(resp.Content)
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
