package fonts

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/fonttest"
)

// The scripts other than Devanagari.
//
// They share the model and differ in data, so what is tested here is the data:
// that each script's own answer is used and not another's. The bundled face has
// Devanagari alone, so every fixture below is synthetic — a font whose Indic
// features the test states, over the real characters of the script.
//
// The claims are checked against HarfBuzz elsewhere, over a corpus of every pair
// of characters each Noto face covers and 60000 random four-character sequences
// per script. What is here is what a reader of the repository can run.

// TestEachScriptHasItsOwnData is the plainest guard on the table: nine scripts,
// each with the virama its own font's rules are written against, and no two
// sharing one.
func TestEachScriptHasItsOwnData(t *testing.T) {
	want := map[string]rune{
		"dev2": 0x094D, "bng2": 0x09CD, "gur2": 0x0A4D, "gjr2": 0x0ACD,
		"ory2": 0x0B4D, "tml2": 0x0BCD, "tel2": 0x0C4D, "knd2": 0x0CCD,
		"mlm2": 0x0D4D,
	}
	if len(indicConfigs) != len(want) {
		t.Errorf("the table holds %d scripts, want %d", len(indicConfigs), len(want))
	}
	for tag, virama := range want {
		cfg, ok := indicConfigs[tag]
		if !ok {
			t.Errorf("%q is not in the table", tag)
			continue
		}
		if cfg.virama != virama {
			t.Errorf("%q names virama U+%04X, want U+%04X", tag, cfg.virama, virama)
		}
	}
}

// TestEachScriptHasItsOwnRa pins the one letter per script that can become a
// reph. Its Unicode category is plain Consonant, exactly like every other
// letter's, so the only thing that distinguishes it is being named — and naming
// the wrong one turns an ordinary letter into a stroke over the syllable.
func TestEachScriptHasItsOwnRa(t *testing.T) {
	ras := []rune{0x0930, 0x09B0, 0x09F0, 0x0A30, 0x0AB0, 0x0B30, 0x0BB0, 0x0C30, 0x0CB0, 0x0D30}
	for _, r := range ras {
		if cat, _ := indicProperties(r); cat != catRa {
			t.Errorf("U+%04X is category %d, not the Ra of its script", r, cat)
		}
		// Its neighbour in the block is an ordinary consonant, which is what
		// makes the naming a claim rather than a range.
		if cat, _ := indicProperties(r + 1); cat == catRa {
			t.Errorf("U+%04X is also taken for an Ra", r+1)
		}
	}
}

// TestAVowelSignIsPlacedWhereItsScriptPlacesIt is the data that no test of
// Devanagari alone could catch. Unicode says which side of the letter a sign is
// written on; how far out from the base it is *drawn* is each script's own, and
// they disagree — so the same side means a different place in the drawing order
// in each of them, and a sign placed by another script's rule ends up on the
// wrong side of a conjunct.
func TestAVowelSignIsPlacedWhereItsScriptPlacesIt(t *testing.T) {
	for _, tc := range []struct {
		r    rune
		want indicPos
		name string
	}{
		{0x093E, posAfterSub, "the Devanagari aa-sign, drawn after the below-base forms"},
		{0x09BE, posAfterPost, "the Bengali aa-sign, drawn after everything"},
		{0x0BBE, posAfterPost, "the Tamil aa-sign, drawn after everything"},
		{0x0C3E, posBeforeSub, "the Telugu aa-sign, drawn before the below-base forms"},
		{0x0CBE, posBeforeSub, "the Kannada aa-sign, drawn before them"},
		{0x0D3E, posAfterPost, "the Malayalam aa-sign, drawn after everything"},
		{0x0B3E, posAfterPost, "the Oriya aa-sign, drawn after everything"},
		{0x0A3E, posAfterPost, "the Gurmukhi aa-sign, drawn after everything"},
		{0x0ABE, posAfterPost, "the Gujarati aa-sign, drawn after everything"},

		// A sign written to the left of the letter is drawn before the base in
		// every one of them, which is the one rule they all share.
		{0x093F, posPreM, "the Devanagari i-sign"},
		{0x09BF, posPreM, "the Bengali i-sign"},
		{0x0BC6, posPreM, "the Tamil e-sign"},
		{0x0D46, posPreM, "the Malayalam e-sign"},

		// Tamil's i-sign is *not* one of them: it is written to the right, and
		// is drawn where a right-side Tamil sign is drawn.
		{0x0BBF, posAfterPost, "the Tamil i-sign, which is not a left-side sign"},
	} {
		_, pos := indicProperties(tc.r)
		if pos != tc.want {
			t.Errorf("U+%04X (%s) is placed at %d, want %d", tc.r, tc.name, pos, tc.want)
		}
	}
}

// TestASplitVowelSignIsTakenApart is the model's second step, and the one that
// cannot be skipped: the parts of such a sign go to *different* places, one
// before the letter and one after, so there is no single place the sign itself
// could be given.
func TestASplitVowelSignIsTakenApart(t *testing.T) {
	for _, tc := range []struct {
		r     rune
		parts []rune
		name  string
	}{
		{0x0BCA, []rune{0x0BC6, 0x0BBE}, "the Tamil o-sign"},
		{0x09CB, []rune{0x09C7, 0x09BE}, "the Bengali o-sign"},
		{0x0CCB, []rune{0x0CC6, 0x0CC2, 0x0CD5}, "the Kannada oo-sign, which is three marks"},
	} {
		parts, ok := indicSplitMatraOf(tc.r)
		if !ok {
			t.Errorf("U+%04X (%s) is not known to be a split sign", tc.r, tc.name)
			continue
		}
		if len(parts) != len(tc.parts) {
			t.Errorf("U+%04X (%s) is %d marks, want %d", tc.r, tc.name, len(parts), len(tc.parts))
			continue
		}
		for i := range parts {
			if parts[i] != tc.parts[i] {
				t.Errorf("U+%04X (%s) part %d is U+%04X, want U+%04X",
					tc.r, tc.name, i, parts[i], tc.parts[i])
			}
		}
	}
	// An ordinary sign is not one, and neither is a letter that happens to be
	// written with a dot — taking one of those apart would replace a letter.
	for _, r := range []rune{0x093E, 0x0BBE, 0x0929, 0x0958, 0x09DC} {
		if _, ok := indicSplitMatraOf(r); ok {
			t.Errorf("U+%04X is taken for a split sign", r)
		}
	}
}

// tamilFace builds a Tamil font over the characters the test names.
//
// Tamil is the script chosen for the end-to-end fixture because its o-sign is a
// split one: written as a single character U+0BCA and drawn as a mark before the
// letter and another after it. Nothing in Devanagari behaves that way, so
// nothing in Devanagari's tests could have caught it being kept whole.
const (
	tamKa       = 0x0B95 // TAMIL LETTER KA
	tamVirama   = 0x0BCD // TAMIL SIGN VIRAMA
	tamOSign    = 0x0BCA // TAMIL VOWEL SIGN O, written as one, drawn as two
	tamESign    = 0x0BC6 // its first part, drawn before the letter
	tamAASign   = 0x0BBE // its second part, drawn after
	tamAnusvara = 0x0B82
)

const (
	gidTamKa = 1 + iota
	gidTamVirama
	gidTamOSign
	gidTamESign
	gidTamAASign
	gidTamAnusvara
	gidTamDotted
)

func tamilFace(t *testing.T, withOSignGlyphParts bool) *Face {
	t.Helper()
	runes := []rune{tamKa, tamVirama, tamOSign, tamESign, tamAASign, tamAnusvara, 0x25CC}
	if !withOSignGlyphParts {
		// A face that draws the sign whole and has no glyph for its halves.
		runes = []rune{tamKa, tamVirama, tamOSign, 0xE000, 0xE001, tamAnusvara, 0x25CC}
	}
	glyphs := make([]fonttest.Glyph, len(runes))
	for i, r := range runes {
		glyphs[i] = fonttest.Glyph{Rune: r, Advance: 400 + 10*i, HasShape: true}
	}
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name:   "Tamil",
		Glyphs: glyphs,
		Extra: map[string][]byte{
			"GSUB": fonttest.GSUBTable(nil, nil, map[string]fonttest.Script{
				"tml2": {Required: fonttest.NoFeature},
				"DFLT": {Required: fonttest.NoFeature},
			}),
		},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

// TestATamilSplitSignIsDrawnOnBothSidesOfItsLetter is the whole of it in one
// line of text. கொ is two characters — ka and the o-sign — and three glyphs, one
// of them drawn *before* the ka. A shaper that kept the sign whole would draw
// both halves after the letter, which every reader of Tamil sees at once.
func TestATamilSplitSignIsDrawnOnBothSidesOfItsLetter(t *testing.T) {
	f := tamilFace(t, true)

	s := str(tamKa, tamOSign)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidTamESign, gidTamKa, gidTamAASign}, s)

	// A face that has no glyph for the halves keeps the sign whole: losing half
	// of a sign is worse than drawing it where the model would rather it were
	// not.
	f = tamilFace(t, false)
	s = str(tamKa, tamOSign)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidTamKa, gidTamOSign}, s)
}

// TestATamilSyllableWithNoLetterGetsAPlaceholder pins that the rest of the model
// reaches the other scripts too, not only Devanagari.
func TestATamilSyllableWithNoLetterGetsAPlaceholder(t *testing.T) {
	f := tamilFace(t, true)
	s := str(tamOSign)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidTamESign, gidTamDotted, gidTamAASign}, s)
}
