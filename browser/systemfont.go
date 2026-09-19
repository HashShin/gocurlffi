package browser

// systemFace resolves a run's font-family list against the fonts the machine
// has. Those lookups are not carried yet, so this is the documented fallback:
// nil, which draws the run with the embedded Go fonts. It exists because
// Page.fontFor calls it after the page's own @font-face faces, and the browser
// must still build and draw every page that names a family it cannot load.
func systemFace(families []string, weight int, italic bool) *webFont {
	return nil
}
