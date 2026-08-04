package fonts

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/fonttest"
)

// Khmer reordering, on a font whose Khmer features the test states.
//
// The bundled face has Latin, Greek, Cyrillic and Devanagari and no Khmer at
// all, so every fixture here is synthetic: a font declaring, under 'khmr', the
// rules a real Khmer font declares, over the real characters of the script.
// What the fixtures assert is what was measured against HarfBuzz over the
// notofonts Khmer corpus and every pair of characters Noto Sans Khmer covers;
// what is here is the part of that a reader of the repository can run.
const (
	khKa      = 0x1780 // KHMER LETTER KA
	khKha     = 0x1781 // KHMER LETTER KHA
	khRo      = 0x179A // KHMER LETTER RO, the one drawn before the base
	khCoeng   = 0x17D2 // KHMER SIGN COENG, which subscripts the letter after it
	khE       = 0x17C1 // KHMER VOWEL SIGN E, stored after its letter, drawn before
	khAA      = 0x17B6 // KHMER VOWEL SIGN AA, stored and drawn after
	khOO      = 0x17C4 // KHMER VOWEL SIGN OO, written as one mark and drawn as two
	khU       = 0x17BB // KHMER VOWEL SIGN U, drawn below
	khMuu     = 0x17C9 // KHMER SIGN MUUSIKATOAN, a register shifter
	khNikahit = 0x17C6 // KHMER SIGN NIKAHIT
	khReahmuk = 0x17C7 // KHMER SIGN REAHMUK
)

// The fixture's glyph indices, in the order khmerGlyphs lists them.
const (
	gidKhKa = 1 + iota
	gidKhKha
	gidKhRo
	gidKhCoeng
	gidKhE
	gidKhAA
	gidKhOO
	gidKhU
	gidKhMuu
	gidKhNikahit
	gidKhReahmuk
	gidKhSubRo   // what 'pref' makes of a coeng and a Ro
	gidKhSubKa   // what 'blwf' makes of a coeng and a Ka
	gidKhSubKha  // what 'blwf' makes of a coeng and a Kha
	gidKhRoLow   // what 'cfar' makes of a subscript that follows a subscript Ro
	gidKhKaWithE // what 'pres' makes of a reordered e-sign and its Ka
	gidKhSpace
	gidKhDotted
	gidKhZWNJ
	gidKhZWJ
)

func khmerGlyphs() []fonttest.Glyph {
	runes := []rune{
		khKa, khKha, khRo, khCoeng, khE, khAA, khOO, khU, khMuu, khNikahit, khReahmuk,
		0xE000, 0xE001, 0xE002, 0xE003, 0xE004,
		' ', 0x25CC, 0x200C, 0x200D,
	}
	out := make([]fonttest.Glyph, len(runes))
	for i, r := range runes {
		// Distinct advances, so that a substitution that failed to carry the
		// new glyph's width across is visible as well as a wrong glyph.
		out[i] = fonttest.Glyph{Rune: r, Advance: 300 + 10*i, HasShape: true}
	}
	return out
}

// khmerFace builds a font declaring the given features under 'khmr' and nothing
// under the default script.
//
// Declaring them under 'khmr' alone is the point: these are claims about Khmer,
// and a font that made them generally would be applying the coeng rules to
// Latin.
func khmerFace(t *testing.T, features ...devaFeature) *Face {
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
		Name:   "Khmer",
		Glyphs: khmerGlyphs(),
		Extra: map[string][]byte{
			"GSUB": fonttest.GSUBTable(lookups, list, map[string]fonttest.Script{
				"khmr": {Required: fonttest.NoFeature, Features: selected},
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

// The features a fixture can ask for, so that each test declares only what it
// is about and no rule fires behind another one's back.
func khmerPref() devaFeature {
	return devaLigatures("pref", fonttest.Ligature{
		Components: []int{gidKhCoeng, gidKhRo}, Glyph: gidKhSubRo,
	})
}

func khmerBlwf() devaFeature {
	return devaLigatures("blwf",
		fonttest.Ligature{Components: []int{gidKhCoeng, gidKhKa}, Glyph: gidKhSubKa},
		fonttest.Ligature{Components: []int{gidKhCoeng, gidKhKha}, Glyph: gidKhSubKha},
	)
}

func khmerCfar() devaFeature {
	return devaSingle("cfar", []int{gidKhSubKha}, []int{gidKhRoLow})
}

// khmerBlwfAcross declares 'blwf' over a pair of letters that a syllable
// boundary falls between: the rule can only fire if the feature was allowed to
// look past the syllable it was applied to.
func khmerBlwfAcross() devaFeature {
	return devaLigatures("blwf", fonttest.Ligature{
		Components: []int{gidKhKha, gidKhKa}, Glyph: gidKhSubKa,
	})
}

func khmerPres() devaFeature {
	return devaLigatures("pres", fonttest.Ligature{
		Components: []int{gidKhE, gidKhKa}, Glyph: gidKhKaWithE,
	})
}

// TestKhmerCategoriesAreTheScriptsOwn pins the characters whose Khmer category
// is not the one their Unicode category implies. Each is a statement the
// grammar is written in terms of, and getting one wrong cuts the syllable in
// the wrong place — which is worse than any single glyph being wrong.
func TestKhmerCategoriesAreTheScriptsOwn(t *testing.T) {
	for _, tc := range []struct {
		r    rune
		want indicCat
		name string
	}{
		// Unicode calls these a register shifter and a succeeding repha; the
		// script's own rules group all three as marks that may stand between a
		// letter and its subscripts.
		{0x17C9, catRobatic, "muusikatoan"},
		{0x17CA, catRobatic, "triisap"},
		{0x17CC, catRobatic, "robat"},
		// Unicode calls this a bindu; the rules group it with the marks that may
		// stand before a vowel sign.
		{0x17C6, catXgroup, "nikahit"},
		{0x17D1, catXgroup, "viriam"},
		// Unicode calls the first a visarga and the second a vowel sign; the
		// rules group both with the marks that may stand only at the end.
		{0x17C7, catYgroup, "reahmuk"},
		{0x17C8, catYgroup, "yuukaleapintu"},
		{0x17DD, catYgroup, "atthacan"},
		// The rest follow Unicode, and are here because the grammar names them.
		{0x179A, catRa, "ro"},
		{0x1780, catConsonant, "ka"},
		{0x17A3, catVowel, "independent aq"},
		{0x17D2, catStacker, "coeng"},
		{0x17C1, catVPre, "the e-sign, drawn before its letter"},
		{0x17B6, catVPst, "the aa-sign, drawn after it"},
		{0x17BB, catVBlw, "the u-sign, drawn below it"},
		{0x17B7, catVAbv, "the i-sign, drawn above it"},
		{0x17E0, catPlaceholder, "the digit zero"},
	} {
		if got := khmerCategory(tc.r); got != tc.want {
			t.Errorf("U+%04X (%s) is category %d, want %d", tc.r, tc.name, got, tc.want)
		}
	}
	// A character of no Khmer category at all is not part of a syllable.
	for _, r := range []rune{'A', ' ', 0x17B4, 0x17D4, 0x19E0} {
		if got := khmerCategory(r); got != catOther {
			t.Errorf("U+%04X is category %d, want none", r, got)
		}
	}
}

// TestKhmerIsShapedByItsOwnModel pins the dispatch, in both directions. Khmer
// does not share the Indic model, and setting it by those rules would be worse
// than setting it in storage order.
func TestKhmerIsShapedByItsOwnModel(t *testing.T) {
	khmer := runScript(str(khKa))
	if !isKhmerScript(khmer) {
		t.Errorf("Khmer is not recognised as Khmer")
	}
	if indicConfigFor(khmer) != nil {
		t.Errorf("Khmer is reordered by the Indic model, which is not its own")
	}
	if isMyanmarScript(khmer) {
		t.Errorf("Khmer is recognised as Myanmar")
	}
	// A script of neither model is shaped by neither.
	for _, s := range []string{"A", "م", "ก"} {
		script := runScript(s)
		if isKhmerScript(script) {
			t.Errorf("%q is recognised as Khmer", s)
		}
	}
}

// TestKhmerSyllablesAreCut is the boundary everything else depends on: a coeng
// binds the letter after it into the same syllable, and a letter with nothing
// binding it starts a new one.
func TestKhmerSyllablesAreCut(t *testing.T) {
	for _, tc := range []struct {
		text  []rune
		want  []int // the start of each syllable
		kinds []khmerSyllableKind
		name  string
	}{
		{[]rune{khKa, khKha}, []int{0, 1},
			[]khmerSyllableKind{khmerConsonant, khmerConsonant},
			"two letters with nothing binding them are two syllables"},
		{[]rune{khKa, khCoeng, khKha}, []int{0},
			[]khmerSyllableKind{khmerConsonant},
			"a coeng binds the letter after it into one syllable"},
		{[]rune{khKa, khCoeng, khKha, khCoeng, khRo}, []int{0},
			[]khmerSyllableKind{khmerConsonant},
			"and does so however often it is repeated"},
		{[]rune{khKa, khCoeng, khKha, khKa}, []int{0, 3},
			[]khmerSyllableKind{khmerConsonant, khmerConsonant},
			"the letter after the bound one starts a new syllable"},
		{[]rune{khKa, khE, khAA}, []int{0},
			[]khmerSyllableKind{khmerConsonant},
			"a letter takes its vowel signs"},
		{[]rune{khE}, []int{0},
			[]khmerSyllableKind{khmerBroken},
			"a sign with no letter is a broken cluster"},
		{[]rune{khKa, khCoeng}, []int{0},
			[]khmerSyllableKind{khmerConsonant},
			"a coeng with no letter after it ends the syllable rather than binding"},
		{[]rune{'A', khKa}, []int{0, 1},
			[]khmerSyllableKind{khmerNonKhmer, khmerConsonant},
			"a character of no Khmer category is its own cluster"},
	} {
		cats := make([]indicCat, len(tc.text))
		for i, r := range tc.text {
			cats[i] = khmerCategory(r)
		}
		syls := khmerSyllables(cats)
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

// TestKhmerSyllablesCoverTheInput is the property every caller of the cut
// relies on: it makes progress and loses nothing, whatever it is handed.
func TestKhmerSyllablesCoverTheInput(t *testing.T) {
	pool := []indicCat{
		catConsonant, catRa, catVowel, catStacker, catVPre, catVBlw, catVAbv,
		catVPst, catRobatic, catXgroup, catYgroup, catZWJ, catZWNJ,
		catPlaceholder, catDottedCircle, catOther,
	}
	seed := uint32(1)
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
		for _, s := range khmerSyllables(cats) {
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

// TestKhmerLongSyllableIsCut pins the bound. Text is untrusted input and the
// grammar puts no limit on how long a coeng-and-letter chain may be.
func TestKhmerLongSyllableIsCut(t *testing.T) {
	cats := make([]indicCat, 0, 400)
	cats = append(cats, catConsonant)
	for i := 0; i < 200; i++ {
		cats = append(cats, catStacker, catConsonant)
	}
	for _, s := range khmerSyllables(cats) {
		if s.end-s.start > maxIndicSyllable {
			t.Fatalf("syllable %v is %d characters, want at most %d",
				s, s.end-s.start, maxIndicSyllable)
		}
	}
}

// TestAKhmerPreBaseVowelIsDrawnBeforeItsLetter is the plainest of the three
// things that move: កេ is two characters — ka and the e-sign — and two glyphs
// in the opposite order.
func TestAKhmerPreBaseVowelIsDrawnBeforeItsLetter(t *testing.T) {
	f := khmerFace(t)
	s := str(khKa, khE)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidKhE, gidKhKa}, s)

	// A sign drawn after the letter stays where it was written, which is what
	// makes the move above a claim about that sign rather than about all of
	// them.
	s = str(khKa, khAA)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidKhKa, gidKhAA}, s)
}

// TestAKhmerSubscriptRoIsDrawnBeforeTheBase is the rule that makes Khmer its own
// shaper: every other subscript is drawn under the base, and this one is drawn
// before it — the coeng and the Ro move to the front of the syllable together
// and are asked for the pre-base form.
func TestAKhmerSubscriptRoIsDrawnBeforeTheBase(t *testing.T) {
	f := khmerFace(t, khmerPref(), khmerBlwf())

	s := str(khKa, khCoeng, khRo)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidKhSubRo, gidKhKa}, s)

	// A subscript that is not a Ro stays under the base and takes 'blwf'
	// instead, which is what tells the two apart.
	s = str(khKa, khCoeng, khKha)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidKhKa, gidKhSubKha}, s)
}

// TestAKhmerSubscriptRoMarksWhatFollowedIt pins 'cfar', which exists in no other
// script. Once the coeng and Ro have moved to the front, the syllable no longer
// says which subscript stood after them — so they mark it as they go, and the
// font draws that subscript lower to clear the Ro's tail.
//
// The two sequences below are the same three letters in the same order bar one
// transposition, and without 'cfar' a font could not tell them apart.
func TestAKhmerSubscriptRoMarksWhatFollowedIt(t *testing.T) {
	f := khmerFace(t, khmerPref(), khmerBlwf(), khmerCfar())

	s := str(khKa, khCoeng, khRo, khCoeng, khKha)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidKhSubRo, gidKhKa, gidKhRoLow}, s)

	// The same letters with the Ro subscripted second. Its coeng and Ro move to
	// the front just the same — that is not what 'cfar' distinguishes — but
	// nothing stood after them, so nothing is marked and the second subscript
	// keeps its ordinary below-base form.
	s = str(khKa, khCoeng, khKha, khCoeng, khRo)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidKhSubRo, gidKhKa, gidKhSubKha}, s)
}

// TestAKhmerSplitVowelIsDrawnOnBothSidesOfItsLetter is the model's second step.
// កោ is two characters — ka and the oo-sign — and three glyphs, one of them
// drawn before the ka. Unicode gives the sign no decomposition, so a shaper that
// did not know it was two marks would draw both halves after the letter.
func TestAKhmerSplitVowelIsDrawnOnBothSidesOfItsLetter(t *testing.T) {
	f := khmerFace(t)
	s := str(khKa, khOO)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidKhE, gidKhKa, gidKhOO}, s)
}

// TestAKhmerSyllableWithNoLetterGetsAPlaceholder: a mark written with nothing to
// attach to floats over nothing, where a reader cannot tell it from a mark on
// the letter before. The dotted circle is the placeholder every reader of the
// script knows — and the pre-base sign is still drawn before it, because that is
// where the sign goes.
func TestAKhmerSyllableWithNoLetterGetsAPlaceholder(t *testing.T) {
	f := khmerFace(t)
	s := str(khE)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidKhE, gidKhDotted}, s)

	s = str(khCoeng)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidKhDotted, gidKhCoeng}, s)
}

// TestAKhmerPresentationFeatureSeesTheReorderedGlyphs pins the order of the two
// halves of the pass: 'pres' is written about a pre-base sign standing before
// the letter it belongs to, which is an arrangement that exists only after the
// reordering has run.
func TestAKhmerPresentationFeatureSeesTheReorderedGlyphs(t *testing.T) {
	f := khmerFace(t, khmerPres())
	s := str(khKa, khE)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidKhKaWithE}, s)
}

// TestAKhmerFeatureDoesNotReachTheNextSyllable: the basic features are applied
// one syllable at a time, and neither the glyphs a rule may consume nor the
// glyphs it may see as context reach past the syllable it was applied to. A
// rule that reached further would join letters the font never meant to see
// together — every Khmer word is several syllables, and they are written with
// nothing between them.
func TestAKhmerFeatureDoesNotReachTheNextSyllable(t *testing.T) {
	f := khmerFace(t, khmerBlwf())
	// The Ka ends one syllable and the coeng opens the next, so the pair the
	// 'blwf' rule names never stands within one syllable.
	s := str(khKa, khCoeng, khKa, khCoeng)
	got := shapedGIDs(t, f, s)
	if len(got) != 3 || got[0] != gidKhKa || got[1] != gidKhSubKa || got[2] != gidKhCoeng {
		t.Errorf("%q shaped to %v, want the first pair joined and the trailing coeng left alone",
			s, got)
	}

	// The other half of the same claim, and the one a bound on the *consumed*
	// glyphs alone would not make: the rule starts inside the syllable it was
	// applied to and would run past its end. ក្ខ is one syllable and the ក after
	// it is another, so the Kha and the Ka the rule names are never both within
	// one, and nothing may join them.
	f = khmerFace(t, khmerBlwfAcross())
	s = str(khKa, khCoeng, khKha, khKa)
	wantGIDs(t, shapedGIDs(t, f, s),
		[]int{gidKhKa, gidKhCoeng, gidKhKha, gidKhKa}, s)
}

// TestKhmerJoinControlsAreNotDrawn: a joiner has said everything it is for once
// the reordering and the features have run, and what is left is a character with
// no shape.
func TestKhmerJoinControlsAreNotDrawn(t *testing.T) {
	f := khmerFace(t)
	s := str(khKa, 0x200D, khKha)
	for _, g := range shapedGIDs(t, f, s) {
		if g == 0 {
			t.Errorf("%q shaped to a run holding .notdef", s)
		}
	}
	if got := len(shapedGIDs(t, f, s)); got != 2 {
		t.Errorf("%q shaped to %d glyphs, want the two letters alone", s, got)
	}
}
