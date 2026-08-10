package render

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/mgilbir/pdf0/fonts"
)

// The font library the suite harness lends the engine, and the one thing it
// deliberately does not contain.
//
// It does not contain Ahem. A quarter of the suite is written in that face, and
// every document that uses it asks for it the way a document asks for anything —
// `<link rel="stylesheet" href="/fonts/ahem.css">`, whose whole content is an
// @font-face pointing at /fonts/Ahem.ttf. The engine loads that itself now, from
// the document, through the same resolver an <img> goes through. An earlier
// version of this file handed the face over by filename because the document's
// own request for it could not be honoured; honouring it is more faithful than
// the shortcut was, and the shortcut is gone.
//
// What is left here is a real font library: the pan-Unicode Noto faces, which
// are what a *caller* supplies. They are not a shortcut for anything the
// documents ask for — no suite document names Noto — they are coverage for the
// scripts the fourteen standard PDF faces do not have, which is exactly the job
// FallbackFontSet exists for.
//
// They are loaded from the checkout rather than vendored: pdf0 ships no font
// bytes at all. See the note beside NOTO_DIR in the Makefile.

// suiteFonts is a FontSet that falls back to Noto for the scripts the standard
// faces do not have, and defers to those for everything else.
type suiteFonts struct {
	fallback []*fonts.Face
	standard FontSet
}

// FaceFor implements FallbackFontSet: the first Noto face that can set the whole
// of the text, or nothing.
//
// Weight and style are ignored. Only the regular faces are fetched, and a
// document whose Hebrew is meant to be bold is far better served by upright
// Hebrew than by the space the standard faces would put there — the substitution
// is reported either way, so nothing is being hidden.
func (w suiteFonts) FaceFor(text string, bold, italic bool) (*fonts.Face, bool) {
	for _, f := range w.fallback {
		if _, missing := f.Shape(text); missing == 0 {
			return f, true
		}
	}
	return nil, false
}

func (w suiteFonts) Face(family string, bold, italic bool) (*fonts.Face, bool) {
	return w.standard.Face(family, bold, italic)
}

var (
	wptFontsOnce sync.Once
	wptFontSet   FontSet
)

// fontSetForWPT is built once: the Noto faces are megabytes apiece and parsing
// them per document, twice per reftest, over five thousand of them, would cost
// more than the whole run.
//
// Sharing one face between documents is safe *here* and would not be in a
// caller that produced PDFs, because a face records the glyphs it was asked to
// show and that record decides what each document embeds. This harness compares
// display lists and writes no PDF.
func fontSetForWPT() FontSet {
	wptFontsOnce.Do(func() {
		wptFontSet = suiteFonts{standard: StandardFonts(), fallback: notoFaces()}
	})
	return wptFontSet
}

// notoEnv names the directory `make noto-fonts` fetches into.
const notoEnv = "NOTO_FONTS"

// notoFaces loads the fallback faces, or none.
//
// Absent, everything still runs: the documents that need them report a missing
// glyph exactly as they did before, and the ratchet is what says whether that
// has cost anything. The order is coverage-first — the pan-Unicode face, then
// the two that add a script it does not have — so the common case is answered
// by the first one asked.
func notoFaces() []*fonts.Face {
	dir := os.Getenv(notoEnv)
	if dir == "" {
		return nil
	}
	var out []*fonts.Face
	for _, name := range []string{
		// Broadest first, so the common case is answered by the first face
		// asked. The rest each add a script the one before it does not have.
		"NotoSans-Regular.ttf",
		"NotoSansHebrew-Regular.ttf",
		"NotoSansArabic-Regular.ttf",
		"NotoSansDevanagari-Regular.ttf",
		"NotoSerifTibetan-Regular.ttf",
		"NotoSansArmenian-Regular.ttf",
		"NotoSansGeorgian-Regular.ttf",
		"NotoSansJP-VF.ttf",
	} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if face, err := fonts.Load(data); err == nil {
			registerBlockFont(face, data)
			out = append(out, face)
		}
	}
	return out
}

// registerBlockFont records which of a face's glyphs are filled rectangles, for
// the comparison in blockglyph_test.go.
//
// A face with none — which is every ordinary text face — is not registered, and
// its runs are compared as text as they always were. The work is done once per
// face, while the font set is built, because it reads every outline in the font.
func registerBlockFont(face *fonts.Face, data []byte) {
	if bf, err := newBlockFont(data); err == nil {
		blockFonts[face] = bf
	}
}
