package browser

import (
	"strings"
	"testing"
)

// A font-family list falls through to whatever the machine can draw. Keeping
// only the first family meant a page asking for "Georgia, 'Times New Roman',
// Times, serif" was drawn in the embedded font even where the system had a
// serif face, and the text measured and wrapped nothing like the browser's.
func TestCSSFamilyList(t *testing.T) {
	got := cssFamilyList(` Georgia, "Times New Roman", 'Liberation Serif' ,serif `)
	want := []string{"Georgia", "Times New Roman", "Liberation Serif", "serif"}
	if len(got) != len(want) {
		t.Fatalf("families = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("families = %q, want %q", got, want)
		}
	}
	if first := cssFirstFamily(`"Liberation Sans", Arial, sans-serif`); first != "Liberation Sans" {
		t.Errorf("first family = %q, want Liberation Sans", first)
	}
}

// "sans-serif" has to resolve to the face a browser on the same machine would
// use. Chromium on Linux asks for Arial, which fontconfig answers with
// Liberation Sans; putting DejaVu Sans first drew every line about 13% wider,
// so text wrapped a word early all over the page.
func TestGenericFamiliesStartWithTheBrowsersFace(t *testing.T) {
	if got := familyCandidates("sans-serif"); got[0] != "Liberation Sans" {
		t.Errorf("sans-serif starts at %q, want Liberation Sans", got[0])
	}
	if got := familyCandidates("serif"); got[0] != "Liberation Serif" {
		t.Errorf("serif starts at %q, want Liberation Serif", got[0])
	}
	// A real family name is not a generic: it stands for itself alone.
	got := familyCandidates("Helvetica")
	if len(got) != 1 || got[0] != "Helvetica" {
		t.Errorf("familyCandidates(Helvetica) = %q, want just Helvetica", got)
	}
	if got := familyCandidates("  "); got != nil {
		t.Errorf("an empty family resolved to %q", got)
	}
}

func TestFontStyleOf(t *testing.T) {
	cases := map[string]fontStyle{
		"Book":         styleRegular,
		"Regular":      styleRegular,
		"":             styleRegular,
		"Bold":         styleBold,
		"SemiBold":     styleBold,
		"Black":        styleBold,
		"Italic":       styleItalic,
		"Oblique":      styleItalic,
		"Bold Italic":  styleBoldItalic,
		"Bold Oblique": styleBoldItalic,
	}
	for sub, want := range cases {
		if got := fontStyleOf(sub); got != want {
			t.Errorf("fontStyleOf(%q) = %d, want %d", sub, got, want)
		}
	}
	// An italic run may be drawn upright, but an upright run must never be
	// drawn in an italic face: italic text that leans is a smaller lie than
	// upright text that does.
	for _, style := range styleFallbacks(styleRegular) {
		if style == styleItalic || style == styleBoldItalic {
			t.Errorf("an upright run fell back to %d", style)
		}
	}
}

// The index reads the machine's families, and the face it hands back is a real
// one. On a machine with no fonts at all it finds nothing, which is what the
// embedded fonts are for.
func TestSystemFontIndex(t *testing.T) {
	families := systemFamilies()
	if len(families) == 0 {
		t.Skip("no system fonts on this machine")
	}
	for _, name := range families {
		if name != strings.ToLower(name) {
			t.Errorf("family %q is not lower-cased", name)
		}
	}
	face := systemFace([]string{"sans-serif"}, 400, false)
	if face == nil {
		t.Fatal("no sans-serif face resolved on a machine that has fonts")
	}
	if face.UnitsPerEm() == 0 || face.NumGlyphs() == 0 {
		t.Error("the resolved face has no glyphs")
	}
	if up := systemFace([]string{"Liberation Sans Oblique"}, 400, false); up != nil {
		// A family nobody has must resolve to nothing, so the caller can fall
		// back to the embedded fonts rather than draw a wrong face.
		t.Skip("this machine has a family called Liberation Sans Oblique")
	}
}
