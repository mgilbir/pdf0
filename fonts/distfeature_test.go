package fonts

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/fonttest"
)

// Where a font states the space between two glyphs.
//
// 'kern' is the feature everybody knows, and reading only it looks like reading
// all of them until a complex script arrives. The spacing of Indic, and of
// several other scripts, is stated under 'dist' — and a font may declare 'dist'
// and no 'kern' at all for that script. Noto Sans does: its deva and dev2
// scripts select no 'kern' feature, so a reader that asked only for 'kern' built
// a layout with zero pairs in it and set every conjunct at its nominal width.
//
// Both are on for every script rather than for the complex ones alone. That is
// what the OpenType feature registry says and what HarfBuzz does — 'dist' is one
// of its global horizontal features, beside 'kern' and 'curs'.

// TestPairsAreReadFromDistAsWellAsKern is the defect stated as a test: a font
// that says what it says under 'dist' and nothing under 'kern'.
func TestPairsAreReadFromDistAsWellAsKern(t *testing.T) {
	const (
		gA, gB   = 1, 2
		advA     = 500
		tighten  = -80
		fontSize = 1000.0
	)
	build := func(feature string) *Face {
		t.Helper()
		data := fonttest.SFNT(fonttest.SFNTOptions{
			Name: "Dist",
			Glyphs: []fonttest.Glyph{
				{Rune: 'a', Advance: advA, HasShape: true},
				{Rune: 'b', Advance: 600, HasShape: true},
			},
			Extra: map[string][]byte{
				"GPOS": fonttest.GPOSPairsUnder(feature,
					[]fonttest.KernPair{{Left: gA, Right: gB, Adjust: tighten}}),
			},
		})
		f, err := Load(data)
		if err != nil {
			t.Fatalf("loading a face with pairs under %q: %v", feature, err)
		}
		return f
	}

	for _, feature := range []string{"kern", "dist"} {
		f := build(feature)
		glyphs, _ := f.ShapeGlyphs("ab")
		if len(glyphs) != 2 {
			t.Fatalf("%q: shaped to %d glyphs, want 2", feature, len(glyphs))
		}
		if want := float64(advA + tighten); glyphs[0].XAdvance != want {
			t.Errorf("pairs declared under %q: the first glyph advances %v, want %v — "+
				"the adjustment was not read", feature, glyphs[0].XAdvance, want)
		}
		// And what measures the run has to agree with what draws it.
		if got, want := f.MeasureShaped("ab", fontSize), MeasureGlyphs(glyphs, fontSize); got != want {
			t.Errorf("pairs declared under %q: MeasureShaped says %v and the glyphs occupy %v",
				feature, got, want)
		}
		if !f.HasKerning() {
			t.Errorf("pairs declared under %q: HasKerning reports none", feature)
		}
	}
}

// TestBundledFontSpacesDevanagariWhereHarfBuzzDoes is the same fix against a
// real font and an outside authority.
//
// Noto Sans states its Devanagari spacing under 'dist' alone. The advances are
// HarfBuzz's, measured over this face with its own default feature set; the
// first glyph of each of these conjuncts has a nominal advance of 609, and every
// one of them is narrowed by a different amount depending on what follows.
// Reading only 'kern' left all of them at 609.
//
// Verified to fail: with 'dist' dropped from pairFeatures, every case reports
// 609.
func TestBundledFontSpacesDevanagariWhereHarfBuzzDoes(t *testing.T) {
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	const nominal = 609
	for _, tc := range []struct {
		text string
		want float64
		why  string
	}{
		{"क्त", 536, "ka-virama-ta"},
		{"क्क", 545, "ka-virama-ka"},
		{"क्ख", 539, "ka-virama-kha"},
		{"क्च", 574, "ka-virama-ca"},
		{"क्झ", 550, "ka-virama-jha"},
		{"क्‍ष", 550, "ka with an explicit half form, then ssa"},
	} {
		glyphs, missing := f.ShapeGlyphs(tc.text)
		if missing != 0 {
			t.Errorf("%s: %d characters have no glyph", tc.why, missing)
			continue
		}
		if len(glyphs) == 0 {
			t.Errorf("%s: shaped to nothing", tc.why)
			continue
		}
		if glyphs[0].XAdvance != tc.want {
			extra := ""
			if glyphs[0].XAdvance == nominal {
				extra = " — that is the nominal advance, so no pair adjustment was applied at all"
			}
			t.Errorf("%s: the half form advances %v, HarfBuzz gives %v%s",
				tc.why, glyphs[0].XAdvance, tc.want, extra)
		}
	}

	// The Latin side must not have moved: it was already right, and 'kern' and
	// 'dist' name overlapping lookups in this font.
	for _, tc := range []struct {
		text string
		want float64
		why  string
	}{
		{"AV", 599, "A before V, which kerns tight"},
		{"VA", 560, "V before A, which kerns tighter still"},
		{"To", 486, "T before o"},
		{"ab", 561, "a before b, a pair that does not kern"},
	} {
		glyphs, _ := f.ShapeGlyphs(tc.text)
		if len(glyphs) < 1 {
			t.Fatalf("%s: shaped to nothing", tc.why)
		}
		if glyphs[0].XAdvance != tc.want {
			t.Errorf("%s: advances %v, want %v", tc.why, glyphs[0].XAdvance, tc.want)
		}
	}
}
