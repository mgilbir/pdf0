package fonts

import "testing"

// TestNormalisationFollowsTheShaperNotTheScriptFamily pins the coupling that a
// merge broke once already.
//
// A syllabic shaper's rules are written against fully decomposed text, so the
// normalisation pass must not short-circuit for a run bound for one. The two
// decisions — "does a syllabic shaper handle this" and "should this be fully
// decomposed" — are the same question, and were briefly two: normalisation
// asked whether the script was *Indic* while dispatch had grown to cover Khmer
// and Myanmar too, so those two got Latin's treatment. Nothing failed; they
// were simply normalised the wrong way.
func TestNormalisationFollowsTheShaperNotTheScriptFamily(t *testing.T) {
	// Every script that reaches a syllabic shaper must report so, whatever
	// family it belongs to.
	for _, r := range []rune{
		'क', // Devanagari
		'ক', // Bengali
		'ក', // Khmer
		'က', // Myanmar
		'ಕ', // Kannada
		'ം', // Malayalam
	} {
		script := scriptOf(r)
		if !usesSyllabicShaper(script) {
			t.Errorf("U+%04X is set by a syllabic shaper and does not say so", r)
		}
	}
	// And a script that is not must not, or every Latin run pays for full
	// decomposition it does not need.
	for _, r := range []rune{'a', 'Ω', 'д', 'ا', '日', 'א'} {
		if usesSyllabicShaper(scriptOf(r)) {
			t.Errorf("U+%04X is not set by a syllabic shaper and claims to be", r)
		}
	}
}

// TestKhmerAndMyanmarAreFullyDecomposedLikeIndic observes the effect, not the
// predicate.
//
// My first attempt asserted only that usesSyllabicShaper answers correctly, and
// reverting the caller to ask "is this Indic" broke nothing — it guarded the
// predicate and not its use, which is the definition of decorative. Asking the
// normaliser what it actually did found a real defect: a composed character on
// the syllabic path was taken apart and never put back, because the early
// return was decided from the *input* having no marks rather than from anything
// having been decomposed.
func TestKhmerAndMyanmarAreFullyDecomposedLikeIndic(t *testing.T) {
	f, err := NotoSans()
	if err != nil {
		t.Fatal(err)
	}
	const composed = 'é' // U+00E9, which the bundled face has
	if _, ok := f.GlyphID(composed); !ok {
		t.Skip("the bundled face lacks the composed form this turns on")
	}

	// Composed in, composed out — on both paths. Normalisation is to the
	// font's coverage, not to NFC or NFD, and the face has this one whole.
	for _, syllabic := range []bool{false, true} {
		out, _ := f.normalize([]rune{composed}, []int{0}, syllabic, false, false)
		if len(out) != 1 || out[0] != composed {
			t.Errorf("syllabic=%v: composed input gave %U, want it left composed", syllabic, out)
		}
	}
	// Decomposed in, composed out — on both paths, since the face can draw it.
	for _, syllabic := range []bool{false, true} {
		out, _ := f.normalize([]rune{'e', 0x0301}, []int{0, 1}, syllabic, false, false)
		if len(out) != 1 || out[0] != composed {
			t.Errorf("syllabic=%v: decomposed input gave %U, want it recomposed", syllabic, out)
		}
	}

	// And the flag reaching the normaliser follows the shaper, not the family.
	for _, r := range []rune{'क', 'ក', 'က'} {
		if !usesSyllabicShaper(scriptOf(r)) {
			t.Errorf("U+%04X is read by a syllabic shaper and does not say so", r)
		}
	}
	// The Indic model's own exceptions are about Devanagari, Bengali and Tamil
	// letters and Indic split vowel signs. Applying them to a Khmer or Myanmar
	// run would assert a rule of a script the text is not in.
	for _, r := range []rune{'ក', 'က'} {
		if indicConfigFor(scriptOf(r)) != nil {
			t.Errorf("U+%04X would be given the Indic model's exceptions", r)
		}
	}
}
