package fonts

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/fonttest"
)

// The pre-base-reordering Ra: a consonant written after the base and drawn
// before it.
//
// It is separate from the rest of the Indic tests because it is the one thing
// in the model that no bundled face can show. Which consonant reorders this way
// is the *font's* to say, through its 'pref' feature, and the bundled Noto Sans
// declares none — Devanagari has no pre-base-reordering Ra at all. So the
// fixtures here declare one, over Devanagari characters, and what they assert
// was checked against HarfBuzz shaping the same synthetic font.

// devaPref declares the pre-base form of a Ra bound to the base by a virama.
// The rule is written virama-first, which is how the model states it.
func devaPref() devaFeature {
	return devaLigatures("pref", fonttest.Ligature{
		Components: []int{gidVirama, gidDRa}, Glyph: gidRakar,
	})
}

// devaPrefBlocked declares 'pref' over a pair that is not this font's virama
// and Ra: a face that has pre-base forms, but not for the consonant in the
// text. It is what tells "the font has no such form" from "the font declined to
// make it here".
func devaPrefBlocked() devaFeature {
	return devaLigatures("pref", fonttest.Ligature{
		Components: []int{gidVirama, gidDTa}, Glyph: gidRakar,
	})
}

// devaPrefDeclined declares 'pref' as two lookups: one that replaces the Ra
// with another form of it, and one that would ligate the virama and the
// *original* Ra into the pre-base form. Asked about the pair, the font answers
// yes — the ligature covers it — and then, run over the text, the first lookup
// changes the Ra out from under the second and no pre-base form is made.
//
// It is the fixture for a font that declares the form generally and blocks it
// in context, which the specification says to expect and which no
// non-contextual rule can show.
func devaPrefDeclined() devaFeature {
	return devaFeature{tag: "pref", build: func(base int) ([]fonttest.Lookup, []int) {
		return []fonttest.Lookup{
			{Type: 1, Subtables: [][]byte{fonttest.SingleSubst(
				[]int{gidDRa}, []int{gidRaAlt})}},
			{Type: 4, Subtables: [][]byte{fonttest.LigatureSubst([]fonttest.Ligature{
				{Components: []int{gidVirama, gidDRa}, Glyph: gidRakar},
			})}},
		}, []int{base, base + 1}
	}}
}

// TestAPreBaseReorderingRaIsDrawnBeforeTheBase. क्र is Ka, virama and Ra, and a
// font that declares the pre-base form draws that Ra *before* the Ka — the one
// consonant in the model that is drawn earlier than it is written.
func TestAPreBaseReorderingRaIsDrawnBeforeTheBase(t *testing.T) {
	f := devaFace(t, devaPref())
	s := str(devKa, devVirama, devRa)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidRakar, gidDKa}, s)
}

// TestAPreBaseReorderingRaMovesOnlyIfTheFontMadeIt is the half of the rule that
// costs something if it is missed. A font may declare the pre-base form for a
// consonant and then decline to make it — the pair is marked before the feature
// runs and only the result says whether it fired — and a Ra the font left as an
// ordinary letter is an ordinary letter. Moving it would draw a plain consonant
// before the base, which is not a thing the script writes.
func TestAPreBaseReorderingRaMovesOnlyIfTheFontMadeIt(t *testing.T) {
	// The font's 'pref' covers a different pair, so nothing is made and nothing
	// moves: the three characters are drawn in the order they were written.
	f := devaFace(t, devaPrefBlocked())
	s := str(devKa, devVirama, devRa)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidDKa, gidVirama, gidDRa}, s)

	// A font with no 'pref' at all is the same story with a different cause,
	// and is what every Devanagari font in fact is.
	f = devaFace(t)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidDKa, gidVirama, gidDRa}, s)

	// The case the rule is actually written for, and the only one that
	// distinguishes it: the font's 'pref' *does* cover this pair — the pair is
	// marked, and the feature is applied to it — and yet no pre-base form comes
	// out. What the font made is an ordinary letter and stays where it was.
	f = devaFace(t, devaPrefDeclined())
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidDKa, gidVirama, gidRaAlt}, s)
}

// TestAPreBaseReorderingRaStopsAtTheLastVirama pins where it stops. It goes to
// the same place a pre-base vowel sign goes: back past the consonants before
// the base, as far as the last virama the font left standing, and no further.
//
// The two fixtures differ in one feature and the answer differs with it, which
// is what makes this a claim about the virama rather than about the position.
func TestAPreBaseReorderingRaStopsAtTheLastVirama(t *testing.T) {
	// With no half forms the virama between the Ta and the Ka survives, and the
	// Ra stops after it — inside the conjunct rather than in front of it.
	f := devaFace(t, devaPref())
	s := str(devTa, devVirama, devKa, devVirama, devRa)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidDTa, gidVirama, gidRakar, gidDKa}, s)

	// With half forms the virama is swallowed into the half form, so there is
	// none left to stop at and the Ra goes to the front of the syllable.
	f = devaFace(t, devaPref(), devaHalf())
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidRakar, gidTaHalf, gidDKa}, s)
}

// TestOnlyOnePreBaseReorderingRaPerSyllable: a syllable has one base to be drawn
// before, so only the first such pair in it is one.
func TestOnlyOnePreBaseReorderingRaPerSyllable(t *testing.T) {
	f := devaFace(t, devaPref())
	s := str(devKa, devVirama, devRa, devVirama, devRa)
	wantGIDs(t, shapedGIDs(t, f, s),
		[]int{gidRakar, gidDKa, gidVirama, gidDRa}, s)
}
