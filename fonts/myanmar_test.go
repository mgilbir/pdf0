package fonts

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/fonttest"
)

// Myanmar reordering, on a font whose Myanmar features the test states.
//
// The bundled face has no Myanmar, so every fixture here is synthetic: a font
// declaring, under 'mym2', the rules a real Myanmar font declares, over the real
// characters of the script. The claims were measured against HarfBuzz over the
// notofonts Myanmar corpus and every pair of characters Noto Sans Myanmar
// covers; what is here is the part of that a reader of the repository can run.
const (
	myKa     = 0x1000 // MYANMAR LETTER KA
	myKha    = 0x1001 // MYANMAR LETTER KHA
	myNga    = 0x1004 // MYANMAR LETTER NGA, the letter a kinzi is made from
	myAA     = 0x102C // MYANMAR VOWEL SIGN AA, drawn after
	myU      = 0x102F // MYANMAR VOWEL SIGN U, drawn below
	myE      = 0x1031 // MYANMAR VOWEL SIGN E, stored after its letter, drawn before
	myAnus   = 0x1036 // MYANMAR SIGN ANUSVARA
	myDot    = 0x1037 // MYANMAR SIGN DOT BELOW
	myVirama = 0x1039 // MYANMAR SIGN VIRAMA, an invisible stacker
	myAsat   = 0x103A // MYANMAR SIGN ASAT
	myMedY   = 0x103B // MYANMAR CONSONANT SIGN MEDIAL YA
	myMedR   = 0x103C // MYANMAR CONSONANT SIGN MEDIAL RA, drawn before the base
	myShanE  = 0x1084 // MYANMAR VOWEL SIGN SHAN E, also drawn before
)

// The fixture's glyph indices, in the order myanmarGlyphs lists them.
const (
	gidMyKa = 1 + iota
	gidMyKha
	gidMyNga
	gidMyAA
	gidMyU
	gidMyE
	gidMyAnus
	gidMyDot
	gidMyVirama
	gidMyAsat
	gidMyMedY
	gidMyMedR
	gidMyShanE
	gidMyKinzi  // what 'rphf' makes of Nga, asat and virama
	gidMySubKha // what 'blwf' makes of a virama and a Kha
	gidMySpace
	gidMyDotted
	gidMyZWNJ
	gidMyZWJ
)

func myanmarGlyphs() []fonttest.Glyph {
	runes := []rune{
		myKa, myKha, myNga, myAA, myU, myE, myAnus, myDot, myVirama, myAsat,
		myMedY, myMedR, myShanE,
		0xE000, 0xE001,
		' ', 0x25CC, 0x200C, 0x200D,
	}
	out := make([]fonttest.Glyph, len(runes))
	for i, r := range runes {
		out[i] = fonttest.Glyph{Rune: r, Advance: 300 + 10*i, HasShape: true}
	}
	return out
}

// myanmarFace builds a font declaring the given features under 'mym2' and
// nothing under the default script.
func myanmarFace(t *testing.T, features ...devaFeature) *Face {
	t.Helper()
	sorted := append([]devaFeature(nil), features...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].tag < sorted[j-1].tag; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	lookups := make([]fonttest.Lookup, 0, len(sorted))
	list := make([]fonttest.Feature, 0, len(sorted))
	selected := make([]int, 0, len(sorted))
	for _, f := range sorted {
		added, named := f.build(len(lookups))
		lookups = append(lookups, added...)
		selected = append(selected, len(list))
		list = append(list, fonttest.Feature{Tag: f.tag, Lookups: named})
	}
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name:   "Myanmar",
		Glyphs: myanmarGlyphs(),
		Extra: map[string][]byte{
			"GSUB": fonttest.GSUBTable(lookups, list, map[string]fonttest.Script{
				"mym2": {Required: fonttest.NoFeature, Features: selected},
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

func myanmarRphf() devaFeature {
	return devaLigatures("rphf", fonttest.Ligature{
		Components: []int{gidMyNga, gidMyAsat, gidMyVirama}, Glyph: gidMyKinzi,
	})
}

func myanmarBlwf() devaFeature {
	return devaLigatures("blwf", fonttest.Ligature{
		Components: []int{gidMyVirama, gidMyKha}, Glyph: gidMySubKha,
	})
}

// myanmarBlwfAcross declares 'blwf' over a pair of letters that a syllable
// boundary falls between: the rule can only fire if the feature was allowed to
// look past the syllable it was applied to.
func myanmarBlwfAcross() devaFeature {
	return devaLigatures("blwf", fonttest.Ligature{
		Components: []int{gidMyKha, gidMyKa}, Glyph: gidMySubKha,
	})
}

// TestMyanmarCategoriesAreTheScriptsOwn pins the characters whose category the
// grammar and the reordering are written in terms of. Myanmar names five medial
// consonants and an asat that Unicode's Indic categories have no equivalent
// for, and two signs it groups with neither the vowels nor the marks.
func TestMyanmarCategoriesAreTheScriptsOwn(t *testing.T) {
	for _, tc := range []struct {
		r    rune
		want indicCat
		name string
	}{
		{0x1004, catRa, "nga, the letter a kinzi is made from"},
		{0x101B, catRa, "ra"},
		{0x1000, catConsonant, "ka"},
		{0x1021, catVowel, "the independent a"},
		{0x1039, catStacker, "the virama"},
		{0x103A, catAsat, "the asat"},
		{0x103B, catMedialY, "medial ya"},
		{0x103C, catMedialR, "medial ra, drawn before the base"},
		{0x103D, catMedialW, "medial wa"},
		{0x103E, catMedialH, "medial ha"},
		{0x1060, catMedialL, "the Mon medial la"},
		{0x1031, catVPre, "the e-sign, drawn before its letter"},
		{0x1084, catVPre, "the Shan e-sign, also drawn before it"},
		{0x102C, catVPst, "the aa-sign, drawn after it"},
		{0x102F, catVBlw, "the u-sign, drawn below it"},
		{0x102D, catVAbv, "the i-sign, drawn above it"},
		// Unicode calls the first a vowel sign and the second a bindu; the
		// reordering counts both as the one thing that may stand between the
		// below-base signs and what follows them.
		{0x1032, catAnusvara, "the ai-sign"},
		{0x1036, catAnusvara, "the anusvara"},
		{0x1037, catNukta, "the dot below"},
		{0x1063, catPTone, "a Pwo tone mark"},
		{0xFE00, catVS, "a variation selector"},
		{0x1040, catPlaceholder, "the digit zero"},
	} {
		if got := myanmarCategory(tc.r); got != tc.want {
			t.Errorf("U+%04X (%s) is category %d, want %d", tc.r, tc.name, got, tc.want)
		}
	}
	for _, r := range []rune{'A', ' ', 0x104C, 0x109F} {
		if got := myanmarCategory(r); got != catOther {
			t.Errorf("U+%04X is category %d, want none", r, got)
		}
	}
}

// TestMyanmarIsShapedByItsOwnModel pins the dispatch, in both directions.
func TestMyanmarIsShapedByItsOwnModel(t *testing.T) {
	myanmar := runScript(str(myKa))
	if !isMyanmarScript(myanmar) {
		t.Errorf("Myanmar is not recognised as Myanmar")
	}
	if indicConfigFor(myanmar) != nil {
		t.Errorf("Myanmar is reordered by the Indic model, which is not its own")
	}
	if isKhmerScript(myanmar) {
		t.Errorf("Myanmar is recognised as Khmer")
	}
}

// TestMyanmarSyllablesAreCut. The interesting case is the last: a kinzi opens
// with a letter that is also an ordinary consonant, so the grammar's two
// alternatives are not disjoint and the cut has to take the longer of them
// rather than the first that matches.
func TestMyanmarSyllablesAreCut(t *testing.T) {
	for _, tc := range []struct {
		text  []rune
		want  []int
		kinds []myanmarSyllableKind
		name  string
	}{
		{[]rune{myKa, myKha}, []int{0, 1},
			[]myanmarSyllableKind{myanmarConsonant, myanmarConsonant},
			"two letters with nothing binding them are two syllables"},
		{[]rune{myKa, myVirama, myKha}, []int{0},
			[]myanmarSyllableKind{myanmarConsonant},
			"a virama binds the letter after it into one syllable"},
		{[]rune{myKa, myMedY, myE, myAA}, []int{0},
			[]myanmarSyllableKind{myanmarConsonant},
			"a letter takes its medials and its vowel signs"},
		{[]rune{myNga, myAsat, myVirama, myKa}, []int{0},
			[]myanmarSyllableKind{myanmarConsonant},
			"a kinzi and the letter it belongs to are one syllable"},
		{[]rune{myE}, []int{0},
			[]myanmarSyllableKind{myanmarBroken},
			"a sign with no letter is a broken cluster"},
		{[]rune{0x200D}, []int{0},
			[]myanmarSyllableKind{myanmarNonMyanmar},
			"a lone join control is named as a cluster of its own, not a broken one"},
		{[]rune{myNga, myAsat, myVirama}, []int{0},
			[]myanmarSyllableKind{myanmarBroken},
			"a kinzi with no letter after it is a broken cluster of all three, " +
				"not a consonant syllable of the first two"},
	} {
		cats := make([]indicCat, len(tc.text))
		for i, r := range tc.text {
			cats[i] = myanmarCategory(r)
		}
		syls := myanmarSyllables(cats)
		if len(syls) != len(tc.want) {
			t.Errorf("%s: %d syllables, want %d (%v)", tc.name, len(syls), len(tc.want), syls)
			continue
		}
		for i, s := range syls {
			if s.start != tc.want[i] || s.kind != tc.kinds[i] {
				t.Errorf("%s: syllable %d is %v, want start %d kind %d",
					tc.name, i, s, tc.want[i], tc.kinds[i])
			}
		}
	}
}

// TestMyanmarSyllablesCoverTheInput is the property every caller of the cut
// relies on: it makes progress and loses nothing, whatever it is handed.
func TestMyanmarSyllablesCoverTheInput(t *testing.T) {
	pool := []indicCat{
		catConsonant, catRa, catVowel, catStacker, catAsat, catMedialY, catMedialR,
		catMedialW, catMedialH, catMedialL, catVPre, catVAbv, catVBlw, catVPst,
		catAnusvara, catNukta, catPTone, catVS, catSM, catZWJ, catZWNJ,
		catPlaceholder, catDottedCircle, catOther,
	}
	seed := uint32(7)
	next := func() indicCat {
		seed = seed*1664525 + 1013904223
		return pool[int(seed>>16)%len(pool)]
	}
	for n := 0; n < 4000; n++ {
		cats := make([]indicCat, 1+n%9)
		for i := range cats {
			cats[i] = next()
		}
		at := 0
		for _, s := range myanmarSyllables(cats) {
			if s.start != at || s.end <= s.start || s.end > len(cats) {
				t.Fatalf("%v: syllable %v does not continue from %d", cats, s, at)
			}
			at = s.end
		}
		if at != len(cats) {
			t.Fatalf("%v: syllables cover %d of %d characters", cats, at, len(cats))
		}
	}
}

// TestMyanmarLongSyllableIsCut pins the bound.
func TestMyanmarLongSyllableIsCut(t *testing.T) {
	cats := make([]indicCat, 0, 400)
	cats = append(cats, catConsonant)
	for i := 0; i < 200; i++ {
		cats = append(cats, catStacker, catConsonant)
	}
	for _, s := range myanmarSyllables(cats) {
		if s.end-s.start > maxIndicSyllable {
			t.Fatalf("syllable %v is %d characters, want at most %d",
				s, s.end-s.start, maxIndicSyllable)
		}
	}
}

// TestAMyanmarKinziIsDrawnAfterTheLetterItBelongsTo. ကင်္ is written Nga, asat,
// virama, Ka and drawn as the Ka with a mark over it — the three characters that
// open the syllable are drawn last.
func TestAMyanmarKinziIsDrawnAfterTheLetterItBelongsTo(t *testing.T) {
	f := myanmarFace(t, myanmarRphf())
	s := str(myNga, myAsat, myVirama, myKa)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidMyKa, gidMyKinzi}, s)

	// A font with no kinzi form still draws the three characters after the
	// letter: where they go is the model's answer, and what they look like is
	// the font's.
	f = myanmarFace(t)
	wantGIDs(t, shapedGIDs(t, f, s),
		[]int{gidMyKa, gidMyNga, gidMyAsat, gidMyVirama}, s)

	// The same three characters written after a letter rather than before it
	// are not that letter's kinzi: they open a syllable of their own, and since
	// it has no letter they are shown against a placeholder. That is what makes
	// the rule above a claim about where they stand rather than about which
	// characters they are.
	f = myanmarFace(t, myanmarRphf())
	s = str(myKa, myNga, myAsat, myVirama)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidMyKa, gidMyDotted, gidMyKinzi}, s)

	// Written before a second letter they are that letter's kinzi.
	s = str(myKa, myNga, myAsat, myVirama, myKa)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidMyKa, gidMyKa, gidMyKinzi}, s)
}

// TestAMyanmarMedialRaIsDrawnBeforeItsLetter. The medial Ra wraps the letter it
// follows and is drawn before it — and a pre-base vowel sign is drawn outside
// that again, which is why the two are separate positions rather than one.
func TestAMyanmarMedialRaIsDrawnBeforeItsLetter(t *testing.T) {
	f := myanmarFace(t)

	s := str(myKa, myMedR)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidMyMedR, gidMyKa}, s)

	s = str(myKa, myMedR, myE)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidMyE, gidMyMedR, gidMyKa}, s)

	// A medial that is not a Ra stays where it was written.
	s = str(myKa, myMedY)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidMyKa, gidMyMedY}, s)
}

// TestAMyanmarSignAfterABelowBaseSignIsDrawnBeforeIt is the walk that is
// Myanmar's own. An anusvara written after a below-base vowel sign is drawn
// *before* it; the same anusvara written with no below-base sign in front of it
// stays where it was. Nothing in the character says so — only what has been seen
// earlier in the syllable does.
func TestAMyanmarSignAfterABelowBaseSignIsDrawnBeforeIt(t *testing.T) {
	f := myanmarFace(t)

	s := str(myKa, myU, myAnus)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidMyKa, gidMyAnus, gidMyU}, s)

	// The same anusvara after a medial rather than a below-base sign stays where
	// it was written. Nothing about the anusvara differs between the two.
	s = str(myKa, myMedY, myAnus)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidMyKa, gidMyMedY, gidMyAnus}, s)

	// And what follows the anusvara is drawn after the below-base sign, not
	// before it: the state moves on once a mark that is not an anusvara is seen.
	s = str(myKa, myU, myAnus, myAA)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidMyKa, gidMyAnus, gidMyU, gidMyAA}, s)
}

// TestTwoMyanmarPreBaseSignsAreDrawnOutsideIn: a syllable carrying two signs
// that are both drawn before the letter draws the second of them outermost, so
// the pair comes out in the order opposite to the one it was written in.
func TestTwoMyanmarPreBaseSignsAreDrawnOutsideIn(t *testing.T) {
	f := myanmarFace(t)
	s := str(myE, myShanE)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidMyShanE, gidMyE, gidMyDotted}, s)
}

// TestAMyanmarSyllableWithNoLetterGetsAPlaceholder.
func TestAMyanmarSyllableWithNoLetterGetsAPlaceholder(t *testing.T) {
	f := myanmarFace(t)
	s := str(myU)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidMyDotted, gidMyU}, s)

	// A kinzi with no letter after it is shown against one too, and the kinzi
	// is then not a kinzi: the placeholder stands where its letter would have,
	// so the three characters are drawn after it as they were written.
	s = str(myNga, myAsat, myVirama)
	wantGIDs(t, shapedGIDs(t, f, s),
		[]int{gidMyDotted, gidMyNga, gidMyAsat, gidMyVirama}, s)
}

// TestAMyanmarFeatureDoesNotReachTheNextSyllable: the basic features are applied
// one syllable at a time, and neither the glyphs a rule may consume nor the
// glyphs it may see as context reach past the syllable it was applied to.
func TestAMyanmarFeatureDoesNotReachTheNextSyllable(t *testing.T) {
	f := myanmarFace(t, myanmarBlwf())

	s := str(myKa, myVirama, myKha)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidMyKa, gidMySubKha}, s)

	// The virama ends one syllable and the Kha opens the next, so the pair the
	// rule names never stands within one syllable.
	// The virama has no letter after it, so it ends its syllable rather than
	// binding: the Kha the rule names is two syllables further on, and the sign
	// between them has none of its own.
	s = str(myKa, myVirama, myU, myKha)
	wantGIDs(t, shapedGIDs(t, f, s),
		[]int{gidMyKa, gidMyVirama, gidMyDotted, gidMyU, gidMyKha}, s)

	// The other half of the same claim, and the one a bound on the *consumed*
	// glyphs alone would not make: the rule starts inside the syllable it was
	// applied to and would run past its end.
	f = myanmarFace(t, myanmarBlwfAcross())
	s = str(myKa, myVirama, myKha, myKa)
	wantGIDs(t, shapedGIDs(t, f, s),
		[]int{gidMyKa, gidMyVirama, gidMyKha, gidMyKa}, s)
}
