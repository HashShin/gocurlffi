package browser

// The renderer carries the Go fonts embedded, so it draws the same way on a
// machine with no fonts at all: a scratch container, a phone with no font
// packages, Termux. Where the system does have fonts, they are preferred: a
// page asking for "sans-serif" is then drawn in the face the browser beside it
// would use, so its text measures and wraps the same way, which is most of what
// "it does not look like the browser" turns out to mean.
//
// Nothing is downloaded and nothing is parsed until a page asks for a family
// the page itself does not provide: the index holds file names, and a face is
// read and parsed the first time it is drawn with.

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

// systemFontDirs are searched in order; the first family found wins.
var systemFontDirs = []string{
	"/usr/share/fonts",
	"/usr/local/share/fonts",
	"/usr/share/X11/fonts",
	"/system/fonts",          // Android
	"/system/font",           // Android, older
	"/Library/Fonts",         // macOS
	"/System/Library/Fonts",  // macOS
	"/Network/Library/Fonts", // macOS
	`C:\Windows\Fonts`,       // Windows
	"~/.fonts",
	"~/.local/share/fonts",
}

// genericFamilies is what a CSS generic family stands for, in preference
// order. A browser's own default font settings come first - on Linux they ask
// for Arial and Times New Roman, which fontconfig answers with the
// metric-compatible Liberation family when it is installed, and with the DejaVu
// family when it is not - because drawing "sans-serif" in a face the browser
// beside it would not use is most of why text wraps in the wrong place.
var genericFamilies = map[string][]string{
	"sans-serif": {"Liberation Sans", "Arial", "Helvetica", "Arimo", "DejaVu Sans", "Noto Sans", "Roboto", "Segoe UI", "Verdana", "Tahoma"},
	"serif":      {"Liberation Serif", "Times New Roman", "Times", "Tinos", "DejaVu Serif", "Noto Serif", "Georgia"},
	"monospace":  {"DejaVu Sans Mono", "Liberation Mono", "Courier New", "Noto Sans Mono", "Consolas", "Menlo", "Monaco"},
	"cursive":    {"Comic Sans MS", "DejaVu Sans"},
	"fantasy":    {"Impact", "DejaVu Sans"},
	"system-ui":  {"Liberation Sans", "Arial", "DejaVu Sans", "Noto Sans", "Roboto"},
}

// fontStyle is the four faces a family is asked for.
type fontStyle int

const (
	styleRegular fontStyle = iota
	styleBold
	styleItalic
	styleBoldItalic
)

// fontRef is one face on disk: a file, and for a TrueType collection the index
// of the font inside it.
type fontRef struct {
	path  string
	index int
}

// systemFontIndex maps a lower-cased family name to the faces it has. subs
// holds each face's own subfamily name, which is what decides between two
// upright faces of the same family.
type systemFontIndex struct {
	once   sync.Once
	faces  map[string][4]fontRef
	subs   map[string][4]string
	parsed map[fontRef]*opentype.Font
}

var systemFonts = &systemFontIndex{}

// systemFace returns the system face for a rune's family list, weight and
// slope, or nil when the machine has nothing that fits, in which case the
// caller falls back to the embedded fonts.
func systemFace(families []string, weight int, italic bool) *opentype.Font {
	style := styleRegular
	switch {
	case weight >= 600 && italic:
		style = styleBoldItalic
	case weight >= 600:
		style = styleBold
	case italic:
		style = styleItalic
	}
	for _, family := range families {
		for _, name := range familyCandidates(family) {
			if f := systemFonts.face(name, style); f != nil {
				return f
			}
		}
	}
	return nil
}

// familyCandidates expands one CSS family: a generic name stands for several
// real families, and a real name stands for itself.
func familyCandidates(family string) []string {
	name := strings.TrimSpace(family)
	if name == "" {
		return nil
	}
	if generic, ok := genericFamilies[strings.ToLower(name)]; ok {
		return generic
	}
	return []string{name}
}

// face returns the face for a family and style, preferring the exact style and
// then the nearest one: an italic run may be drawn upright, an upright run may
// not be drawn in an italic face, and a bold run may thin out but a regular one
// must not thicken.
func (s *systemFontIndex) face(family string, style fontStyle) *opentype.Font {
	if family == "" {
		return nil
	}
	s.once.Do(s.scan)
	refs, ok := s.faces[strings.ToLower(strings.TrimSpace(family))]
	if !ok {
		return nil
	}
	for _, candidate := range styleFallbacks(style) {
		if refs[candidate].path != "" {
			return s.load(refs[candidate])
		}
	}
	return nil
}

func styleFallbacks(style fontStyle) []fontStyle {
	switch style {
	case styleBold:
		return []fontStyle{styleBold, styleBoldItalic, styleRegular, styleItalic}
	case styleItalic:
		return []fontStyle{styleItalic, styleRegular}
	case styleBoldItalic:
		return []fontStyle{styleBoldItalic, styleBold, styleItalic, styleRegular}
	}
	return []fontStyle{styleRegular}
}

// load parses a face once and keeps it: a page that draws two hundred runs of
// the same family should read its file once.
func (s *systemFontIndex) load(ref fontRef) *opentype.Font {
	if f, ok := s.parsed[ref]; ok {
		return f
	}
	// A miss is remembered as nil so a broken file is not re-read per run.
	s.parsed[ref] = nil
	data, err := os.ReadFile(ref.path)
	if err != nil {
		return nil
	}
	var f *opentype.Font
	if ref.index > 0 {
		coll, err := opentype.ParseCollection(data)
		if err != nil {
			return nil
		}
		f, err = coll.Font(ref.index)
		if err != nil {
			return nil
		}
	} else {
		f, err = opentype.Parse(data)
		if err != nil {
			return nil
		}
	}
	s.parsed[ref] = f
	return f
}

// scan walks the font directories and records which families are on the
// machine. Only the name table of each file is read; the glyphs wait until the
// face is drawn with.
func (s *systemFontIndex) scan() {
	s.faces = map[string][4]fontRef{}
	s.subs = map[string][4]string{}
	s.parsed = map[fontRef]*opentype.Font{}
	for _, dir := range systemFontDirs {
		dir = expandHome(dir)
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			switch strings.ToLower(filepath.Ext(path)) {
			case ".ttf", ".otf":
				s.indexFile(path)
			case ".ttc", ".otc":
				s.indexCollection(path)
			}
			return nil
		})
	}
}

func (s *systemFontIndex) indexFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	f, err := opentype.Parse(data)
	if err != nil {
		return
	}
	s.record(path, 0, f)
}

func (s *systemFontIndex) indexCollection(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	coll, err := opentype.ParseCollection(data)
	if err != nil {
		return
	}
	for i := 0; i < coll.NumFonts(); i++ {
		f, err := coll.Font(i)
		if err != nil {
			continue
		}
		s.record(path, i, f)
	}
}

// record puts one parsed font into the index under its family name. A family
// with more than one upright face - "DejaVu Sans" ships Book and ExtraLight -
// keeps the plainest: a Subfamily of Book, Regular or Roman wins over a named
// variant, whatever order the files were found in.
func (s *systemFontIndex) record(path string, index int, f *opentype.Font) {
	var buf sfnt.Buffer
	family, err := f.Name(&buf, sfnt.NameIDFamily)
	if err != nil {
		return
	}
	family = strings.TrimSpace(family)
	if family == "" || strings.HasPrefix(family, ".") {
		return
	}
	sub, _ := f.Name(&buf, sfnt.NameIDSubfamily)
	style := fontStyleOf(sub)
	key := strings.ToLower(family)
	refs, subs := s.faces[key], s.subs[key]
	if refs[style].path != "" && !(style == styleRegular && plainSubfamily(sub) && !plainSubfamily(subs[style])) {
		return
	}
	refs[style] = fontRef{path: path, index: index}
	subs[style] = sub
	s.faces[key], s.subs[key] = refs, subs
}

// fontStyleOf classifies a subfamily name into the face a run asks for.
func fontStyleOf(sub string) fontStyle {
	lower := strings.ToLower(sub)
	bold := strings.Contains(lower, "bold") || strings.Contains(lower, "black") ||
		strings.Contains(lower, "heavy") || strings.Contains(lower, "semibold")
	italic := strings.Contains(lower, "italic") || strings.Contains(lower, "oblique")
	switch {
	case bold && italic:
		return styleBoldItalic
	case bold:
		return styleBold
	case italic:
		return styleItalic
	}
	return styleRegular
}

// plainSubfamily reports whether a face is the family's ordinary upright one,
// rather than a weight or width variant that shares its family name.
func plainSubfamily(sub string) bool {
	switch strings.ToLower(strings.TrimSpace(sub)) {
	case "", "book", "regular", "normal", "roman":
		return true
	}
	return false
}

// systemFamilies lists the families the machine has, for diagnostics.
func systemFamilies() []string {
	systemFonts.once.Do(systemFonts.scan)
	out := make([]string, 0, len(systemFonts.faces))
	for name := range systemFonts.faces {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func expandHome(dir string) string {
	if !strings.HasPrefix(dir, "~") {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return dir
	}
	return filepath.Join(home, strings.TrimPrefix(dir, "~"))
}
