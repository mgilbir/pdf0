package render

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mgilbir/pdf0/fonts"
)

// The Ahem font, which a quarter of the suite is written against.
//
// Ahem is a test font, and the reason it exists is that ordinary faces make a
// layout test unwritable: an assertion about where a line breaks or how tall a
// box is has to be expressed in the metrics of whatever face happened to be
// installed. Ahem removes the variable. Every glyph is a solid square exactly
// one em wide, the ascent is 0.8em and the descent 0.2em, so "font: 20px Ahem"
// with four characters is a 80x20 block and the test can assert that number.
//
// Measured over the suite, 260 failures are in documents that name it. That is
// larger than any remaining feature cluster, and none of it is a layout fault:
// the engine has the fourteen standard PDF faces and nothing else, so a document
// asking for Ahem gets a fallback whose metrics are not square, every measurement
// it makes is wrong, and it is wrong by a different amount in the test than in
// the reference — which is exactly the asymmetry a reftest is built to detect.
//
// Supplying the font is not a concession to make tests pass. The suite ships it,
// the documents reference it by name, and a renderer that cannot be given a font
// file is missing something an HTML-to-PDF engine needs for any real document.
// What is test-only here is the *wiring*: the engine's FontSet is an interface
// precisely so a caller can answer for families it has, and this is a caller
// doing that.
//
// It is loaded from the checkout rather than vendored: the file belongs to the
// suite, it is fetched with it, and a copy in this repository would be a second
// thing to keep in step.

// wptFonts is a FontSet that answers for Ahem and defers to the standard
// faces for everything else.
type wptFonts struct {
	ahem     *fonts.Face
	standard FontSet
}

func (w wptFonts) Face(family string, bold, italic bool) (*fonts.Face, bool) {
	if w.ahem != nil && strings.EqualFold(strings.TrimSpace(family), "ahem") {
		// Ahem has one face. Asking it for bold or italic gets the same
		// squares, which is what the font is for — a test that sets bold Ahem
		// is testing that the box moved, not that the glyphs changed shape.
		return w.ahem, true
	}
	return w.standard.Face(family, bold, italic)
}

var (
	wptFontsOnce sync.Once
	wptFontSet   FontSet
)

// wptFontSet is built once: parsing a font program per document, twice per
// reftest, over four thousand of them, would cost more than the whole run.
func fontSetForWPT() FontSet {
	wptFontsOnce.Do(func() {
		standard := StandardFonts()
		wptFontSet = standard

		dir := os.Getenv(wptEnv)
		if dir == "" {
			return
		}
		data, err := os.ReadFile(filepath.Join(dir, "fonts", "Ahem.ttf"))
		if err != nil {
			// An older checkout without the fonts directory. The suite still
			// runs; the documents that want Ahem fall back as they did before,
			// and the ratchet is what says whether that has cost anything.
			return
		}
		face, err := fonts.Load(data)
		if err != nil {
			return
		}
		wptFontSet = wptFonts{ahem: face, standard: standard}
	})
	return wptFontSet
}
