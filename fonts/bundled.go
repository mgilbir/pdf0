package fonts

import (
	_ "embed"
	"fmt"
	"sync"
)

// The bundled face.
//
// Every other way of getting a Face needs a font file from somewhere, and for a
// document that must conform there is no way round that: PDF/A requires every
// font a document shows to be embedded, precisely so the file renders the same
// in fifty years. Naming one of the fourteen faces a reader is required to have
// embeds nothing, and is therefore exactly what a conforming document may not
// do.
//
// So a module that can write a conforming document with text on it has to be
// able to produce a font, and this is it. Noto Sans, as Google Fonts ships it,
// under the SIL Open Font License — the licence, the copyright notice and the
// provenance are in the notosans directory beside this file, and travel with it
// as that licence requires.
//
// # Why the variable font, and why that is not a problem
//
// Google Fonts ships Noto Sans as a variable font, and it is the *only* build
// that carries Devanagari: the narrower per-script upstream this originally
// took has Latin, Greek and Cyrillic alone. A variable font embedded whole
// would be wrong — a PDF reader is not asked to instance one — but nothing is
// embedded whole. Subsetting keeps the static tables and drops fvar, gvar,
// avar, HVAR, MVAR and STAT, and in a variable font the glyf outlines *are*
// the default instance. So what reaches a document is an ordinary static font
// at the default weight, which for this face is Regular: the advances are
// identical to the separately-published static Regular, glyph for glyph.
//
// A document made with it carries no licence obligation of its own. OFL 1.1 is
// explicit that the requirement to stay under the licence "does not apply to
// any document created using the Font Software".

//go:embed notosans/NotoSans-Variable.ttf
var notoSansRegular []byte

//go:embed notosans/OFL.txt
var notoSansLicense string

// NotoSans returns the bundled face, embedded as a composite font.
//
// This is the one to reach for. A composite font addresses glyphs by index, so
// it can show everything the face covers — Latin, Greek and Cyrillic — and it
// is what shaping needs: ligatures, kerning and contextual substitution are all
// statements about glyph indices, and a simple font cannot name them.
//
// Each call returns a new face. A face records the glyphs it was asked to set,
// which is what subsetting is computed from, so sharing one between documents
// would put each document's glyphs into the other's font.
func NotoSans() (*Face, error) {
	notoOnce.Do(func() { notoPrototype, notoErr = Load(notoSansRegular) })
	if notoErr != nil {
		return nil, fmt.Errorf("fonts: the bundled Noto Sans could not be read: %w", notoErr)
	}
	return notoPrototype.forDocument(), nil
}

// The parsed prototype, read once and never handed out.
//
// Reading this face cost 16 ms and 9.6 MB on every call, and a program that
// writes documents calls it once per document. Nine tenths of that is reading
// the layout tables — a face this size states some sixty thousand kern pairs —
// and none of it depends on the document: the parsed program, the tables and
// the rules read out of them are the same every time.
//
// What is *not* the same is the set of glyphs the document asked for, which is
// what subsetting is computed from. So the parse is shared and the record of
// use is not; see Face.forDocument.
var (
	notoOnce      sync.Once
	notoPrototype *Face
	notoErr       error
)

// NotoSansSimple returns the bundled face embedded as a simple font: one byte
// per character, WinAnsiEncoding.
//
// It makes a smaller file and a simpler one, and it costs the two things a
// simple font cannot do — anything outside WinAnsi's 224 characters of Latin,
// and any shaping at all, because a one-byte code names nothing in the layout
// tables. Use it for plain Latin text where size matters; use NotoSans
// otherwise.
func NotoSansSimple() (*Face, error) {
	f, err := LoadSimple(notoSansRegular)
	if err != nil {
		return nil, fmt.Errorf("fonts: the bundled Noto Sans could not be read: %w", err)
	}
	return f, nil
}

// NotoSansLicense is the text of the SIL Open Font License 1.1 as it is
// distributed with the bundled font, including the copyright line.
//
// It is exposed because the licence requires it to travel with the font, and a
// program that embeds the font in something it ships may need to reproduce it —
// in an about box, a credits file, a --licenses flag. Reading it off disk is not
// an option for a single binary, so it is compiled in.
func NotoSansLicense() string { return notoSansLicense }
