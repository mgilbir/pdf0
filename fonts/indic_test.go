package fonts

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/fonttest"
)

// Devanagari reordering, on a font whose Indic features the test states.
//
// The characters are real ones whose behaviour differs in the ways the
// reordering turns on: Ka is an ordinary consonant, Ra is the one that becomes
// a reph, the i-sign is stored after its consonant and drawn before it, the
// aa-sign is stored and drawn after it, and the virama is what binds consonants
// into a conjunct.
const (
	devKa       = 0x0915 // DEVANAGARI LETTER KA
	devTa       = 0x0924 // DEVANAGARI LETTER TA
	devRa       = 0x0930 // DEVANAGARI LETTER RA, the one that becomes a reph
	devVirama   = 0x094D // DEVANAGARI SIGN VIRAMA
	devIMatra   = 0x093F // DEVANAGARI VOWEL SIGN I, stored after, drawn before
	devAAMatra  = 0x093E // DEVANAGARI VOWEL SIGN AA, stored and drawn after
	devAnusvara = 0x0902 // DEVANAGARI SIGN ANUSVARA, a syllable modifier
	devA        = 0x0905 // DEVANAGARI LETTER A, an independent vowel
	devNukta    = 0x093C // DEVANAGARI SIGN NUKTA
)

// The fixture's glyph indices. Glyph 0 is .notdef and the glyphs below follow
// in the order devaGlyphs lists them.
const (
	gidDKa = 1 + iota
	gidDTa
	gidDRa
	gidVirama
	gidIMatra
	gidAAMatra
	gidAnusvara
	gidDevA
	gidNukta
	gidReph      // what 'rphf' makes of Ra + virama
	gidTaHalf    // what 'half' makes of Ta + virama
	gidKTa       // what 'cjct' makes of Ka + virama + Ta
	gidIMatraIni // what 'init' makes of a word-initial i-sign
	gidKaWithI   // what 'pres' makes of a reordered i-sign and its Ka
	gidKaKa      // a ligature of two Ka, which no real font declares
	gidRakar     // what 'blwf' makes of a virama and Ra: a stroke under the base
	gidSpace
	gidDanda
	gidLatinA
	gidZWNJ
	gidZWJ
	gidDotted
)

func devaGlyphs() []fonttest.Glyph {
	runes := []rune{
		devKa, devTa, devRa, devVirama, devIMatra, devAAMatra, devAnusvara, devA, devNukta,
		0xE000, 0xE001, 0xE002, 0xE003, 0xE004, 0xE005, 0xE006,
		' ', 0x0964, 'A', 0x200C, 0x200D, 0x25CC,
	}
	out := make([]fonttest.Glyph, len(runes))
	for i, r := range runes {
		// Distinct advances, so that a substitution that failed to carry the
		// new glyph's width across is visible as well as a wrong glyph.
		out[i] = fonttest.Glyph{Rune: r, Advance: 300 + 10*i, HasShape: true}
	}
	return out
}

// devaFeature is one feature a fixture declares.
//
// build makes the lookups the feature adds and says which of them the feature
// itself names. It is told the index the first of them will take in the font's
// lookup list, because a contextual rule names the lookup it applies by that
// index and so cannot be written without it.
type devaFeature struct {
	tag   string
	build func(base int) (lookups []fonttest.Lookup, named []int)
}

// devaLigatures is the common case: one lookup, a set of ligatures.
func devaLigatures(tag string, ligs ...fonttest.Ligature) devaFeature {
	return devaFeature{tag: tag, build: func(base int) ([]fonttest.Lookup, []int) {
		return []fonttest.Lookup{
			{Type: 4, Subtables: [][]byte{fonttest.LigatureSubst(ligs)}},
		}, []int{base}
	}}
}

// devaSingle is the other common case: one lookup, one glyph for another.
func devaSingle(tag string, from, to []int) devaFeature {
	return devaFeature{tag: tag, build: func(base int) ([]fonttest.Lookup, []int) {
		return []fonttest.Lookup{
			{Type: 1, Subtables: [][]byte{fonttest.SingleSubst(from, to)}},
		}, []int{base}
	}}
}

// devaFace builds a font declaring the given features under 'dev2', and nothing
// under the default script.
//
// Declaring them under 'dev2' alone is the point: these are claims about
// Devanagari, and a font that made them generally would be applying reph
// formation to Latin. The features are sorted by tag, which is the order a
// FeatureList is written in.
func devaFace(t *testing.T, features ...devaFeature) *Face {
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
		Name:   "Devanagari",
		Glyphs: devaGlyphs(),
		Extra: map[string][]byte{
			"GSUB": fonttest.GSUBTable(lookups, list, map[string]fonttest.Script{
				"dev2": {Required: fonttest.NoFeature, Features: selected},
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

// Features a fixture can ask for, so that each test declares only what it is
// about and no rule fires behind another one's back.
func devaRphf() devaFeature {
	return devaLigatures("rphf", fonttest.Ligature{
		Components: []int{gidDRa, gidVirama}, Glyph: gidReph,
	})
}

// devaRphfElsewhere declares 'rphf' over a sequence that is not this font's Ra
// and virama — a face that has reph formation but not for the Ra in the text.
func devaRphfElsewhere() devaFeature {
	return devaLigatures("rphf", fonttest.Ligature{
		Components: []int{gidDTa, gidVirama}, Glyph: gidTaHalf,
	})
}

func devaHalf() devaFeature {
	return devaLigatures("half", fonttest.Ligature{
		Components: []int{gidDTa, gidVirama}, Glyph: gidTaHalf,
	})
}

func devaCjct() devaFeature {
	return devaLigatures("cjct", fonttest.Ligature{
		Components: []int{gidDKa, gidVirama, gidDTa}, Glyph: gidKTa,
	})
}

func devaInit() devaFeature {
	return devaSingle("init", []int{gidIMatra}, []int{gidIMatraIni})
}

func devaPres() devaFeature {
	return devaLigatures("pres", fonttest.Ligature{
		Components: []int{gidIMatra, gidDKa}, Glyph: gidKaWithI,
	})
}

// devaBlwf declares the below-base form of Ra: the stroke a Devanagari font
// draws under the base for a Ra bound to it by a virama. The rule is written
// virama-first, which is how a second-generation font writes it.
func devaBlwf() devaFeature {
	return devaLigatures("blwf", fonttest.Ligature{
		Components: []int{gidVirama, gidDRa}, Glyph: gidRakar,
	})
}

func devaAkhn() devaFeature {
	return devaLigatures("akhn", fonttest.Ligature{
		Components: []int{gidDKa, gidDKa}, Glyph: gidKaKa,
	})
}

// devaAkhnAfterAnything declares 'akhn' as a chained rule: a Ka preceded by a
// Ka or a virama becomes something else. The rule is about what comes *before*
// the glyph it changes, which is what makes it the test for how far back a
// lookup may look — and the two alternatives let one fixture show the rule
// firing inside a syllable and declining to fire across two.
func devaAkhnAfterAnything() devaFeature {
	return devaFeature{tag: "akhn", build: func(base int) ([]fonttest.Lookup, []int) {
		return []fonttest.Lookup{
			{Type: 6, Subtables: [][]byte{fonttest.ChainedContext3(
				[][]int{{gidDKa, gidVirama}}, // preceded by a Ka or a virama
				[][]int{{gidDKa}},            // this Ka
				nil,
				[]fonttest.SeqLookup{{At: 0, Lookup: base + 1}},
			)}},
			{Type: 1, Subtables: [][]byte{fonttest.SingleSubst([]int{gidDKa}, []int{gidKaKa})}},
		}, []int{base}
	}}
}

func str(runes ...rune) string { return string(runes) }

// TestIndicCategoriesAreUnicodes pins the generated table against the
// characters the whole of the reordering rests on. Get one of these wrong and
// the syllable is cut in the wrong place, which is worse than any single glyph
// being wrong.
func TestIndicCategoriesAreUnicodes(t *testing.T) {
	cases := []struct {
		r    rune
		cat  indicCat
		pos  indicPos
		name string
	}{
		{devKa, catConsonant, posBaseC, "ka"},
		{devRa, catRa, posBaseC, "ra, which is the one that becomes a reph"},
		{devVirama, catHalant, posBelowC, "the virama"},
		{devIMatra, catMatra, posPreM, "the i-sign, which is drawn before its consonant"},
		{devAAMatra, catMatra, posAfterSub, "the aa-sign, which is drawn after it"},
		{devAnusvara, catSM, posSMVD, "the anusvara"},
		{devA, catVowel, posBaseC, "an independent vowel"},
		{devNukta, catNukta, posBelowC, "the nukta"},
		{0x200D, catZWJ, posEnd, "a zero width joiner"},
		{0x200C, catZWNJ, posEnd, "a zero width non-joiner"},
		{0x25CC, catDottedCircle, posBaseC, "a dotted circle"},
		{0x093D, catSymbol, posEnd, "the avagraha"},
		{'A', catOther, posEnd, "a Latin letter, which is in no Indic category"},
		{0x0964, catOther, posEnd, "the danda, which is punctuation and not part of a syllable"},
	}
	for _, tc := range cases {
		cat, pos := indicProperties(tc.r)
		if cat != tc.cat || pos != tc.pos {
			t.Errorf("U+%04X (%s) is category %d at position %d, want %d at %d",
				tc.r, tc.name, cat, pos, tc.cat, tc.pos)
		}
	}
}

// TestOnlyDevanagariIsReordered pins the stated scope. The other Indic scripts
// select their second-generation tags and take their fonts' features, and are
// deliberately not reordered — claiming otherwise in the code while shipping a
// half-model would be worse than the gap.
func TestOnlyDevanagariIsReordered(t *testing.T) {
	reordered := map[string]bool{}
	for s := uint16(0); int(s) < len(scriptOpenTypeTags); s++ {
		if reordersIndic(s) {
			for _, tag := range scriptTags(s) {
				reordered[tag] = true
			}
		}
	}
	if !reordered["dev2"] {
		t.Error("Devanagari is not reordered, which is the one script this package does reorder")
	}
	for _, tag := range []string{"bng2", "gjr2", "gur2", "knd2", "mlm2", "ory2", "tml2", "tel2", "mym2", "latn", "arab"} {
		if reordered[tag] {
			t.Errorf("%q is reordered, but this package covers Devanagari alone", tag)
		}
	}
	if reordersIndic(scriptOf('A')) {
		t.Error("a Latin run would be reordered as Indic")
	}
	if reordersIndic(scriptOf(0x0995)) {
		t.Error("a Bengali run would be reordered as Devanagari, whose rules are not Bengali's")
	}
}

// TestSyllablesAreCut is the boundary, which everything else depends on: a
// virama binds the consonant after it into the same syllable, and a consonant
// with nothing binding it starts a new one.
func TestSyllablesAreCut(t *testing.T) {
	cats := func(runes ...rune) []indicCat {
		out := make([]indicCat, len(runes))
		for i, r := range runes {
			out[i], _ = indicProperties(r)
		}
		return out
	}
	cases := []struct {
		name  string
		runes []rune
		want  [][2]int
		kinds []indicSyllableKind
	}{
		{
			"a consonant and its matra are one syllable",
			[]rune{devKa, devIMatra},
			[][2]int{{0, 2}}, []indicSyllableKind{sylConsonant},
		},
		{
			"two of them are two",
			[]rune{devKa, devIMatra, devKa, devIMatra},
			[][2]int{{0, 2}, {2, 4}}, []indicSyllableKind{sylConsonant, sylConsonant},
		},
		{
			"a virama binds the consonant after it",
			[]rune{devKa, devVirama, devTa, devIMatra},
			[][2]int{{0, 4}}, []indicSyllableKind{sylConsonant},
		},
		{
			"and does so however many times it is repeated",
			[]rune{devKa, devVirama, devTa, devVirama, devKa},
			[][2]int{{0, 5}}, []indicSyllableKind{sylConsonant},
		},
		{
			"a modifier ends the syllable and a consonant after it starts a new one",
			[]rune{devKa, devAnusvara, devKa},
			[][2]int{{0, 2}, {2, 3}}, []indicSyllableKind{sylConsonant, sylConsonant},
		},
		{
			"an independent vowel is a syllable of its own kind",
			[]rune{devA, devKa},
			[][2]int{{0, 1}, {1, 2}}, []indicSyllableKind{sylVowel, sylConsonant},
		},
		{
			"a matra with nothing to depend on is a broken cluster",
			[]rune{devIMatra, devKa},
			[][2]int{{0, 1}, {1, 2}}, []indicSyllableKind{sylBroken, sylConsonant},
		},
		{
			"a character in no Indic category is left out of every syllable",
			[]rune{'A', devKa},
			[][2]int{{0, 1}, {1, 2}}, []indicSyllableKind{sylNonIndic, sylConsonant},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := indicSyllables(cats(tc.runes...))
			if len(got) != len(tc.want) {
				t.Fatalf("got %d syllables %v, want %d %v", len(got), got, len(tc.want), tc.want)
			}
			for i, s := range got {
				if s.start != tc.want[i][0] || s.end != tc.want[i][1] {
					t.Errorf("syllable %d is [%d,%d), want [%d,%d)", i, s.start, s.end, tc.want[i][0], tc.want[i][1])
				}
				if s.kind != tc.kinds[i] {
					t.Errorf("syllable %d is kind %d, want %d", i, s.kind, tc.kinds[i])
				}
			}
		})
	}
}

// TestSyllablesCoverTheInput is the property every caller of the cut relies on:
// it makes progress and loses nothing, whatever it is handed.
func TestSyllablesCoverTheInput(t *testing.T) {
	all := []indicCat{
		catOther, catConsonant, catRa, catVowel, catMatra, catNukta, catHalant,
		catStacker, catZWJ, catZWNJ, catSM, catVD, catPlaceholder,
		catDottedCircle, catSymbol, catRepha, catCM, catCS, catRS,
	}
	// Every sequence of three categories, which is enough to reach every
	// alternative of the grammar and every place one can fail to consume.
	seq := make([]indicCat, 3)
	for _, a := range all {
		for _, b := range all {
			for _, c := range all {
				seq[0], seq[1], seq[2] = a, b, c
				at := 0
				for _, s := range indicSyllables(seq) {
					if s.start != at || s.end <= s.start {
						t.Fatalf("%v: syllable [%d,%d) does not continue from %d", seq, s.start, s.end, at)
					}
					at = s.end
				}
				if at != len(seq) {
					t.Fatalf("%v: the syllables stop at %d, short of %d", seq, at, len(seq))
				}
			}
		}
	}
}

// TestLongSyllableIsCut pins the bound. Text is untrusted input, the sort is
// quadratic, and the grammar puts no limit on how long a consonant-and-virama
// chain may be.
func TestLongSyllableIsCut(t *testing.T) {
	var cats []indicCat
	for i := 0; i < 200; i++ {
		cats = append(cats, catConsonant, catHalant)
	}
	cats = append(cats, catConsonant)
	for _, s := range indicSyllables(cats) {
		if s.end-s.start > maxIndicSyllable {
			t.Fatalf("syllable [%d,%d) is %d characters, over the %d cap",
				s.start, s.end, s.end-s.start, maxIndicSyllable)
		}
	}
}

// TestPreBaseMatraIsDrawnBeforeItsConsonant is the plainest of the three
// reorderings and the one a reader notices first: कि is written ka then i-sign
// and drawn i-sign then ka.
func TestPreBaseMatraIsDrawnBeforeItsConsonant(t *testing.T) {
	f := devaFace(t)
	s := str(devKa, devIMatra)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidIMatra, gidDKa}, s)

	// The aa-sign is stored and drawn after its consonant, and must not move:
	// a reordering that moved every matra would be as wrong as one that moved
	// none, and only a test naming both catches it.
	s = str(devKa, devAAMatra)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidDKa, gidAAMatra}, s)
}

// TestRephIsDrawnAtTheEndOfItsSyllable is the second: र् at the front of a
// syllable is one stroke drawn over the end of it.
func TestRephIsDrawnAtTheEndOfItsSyllable(t *testing.T) {
	f := devaFace(t, devaRphf())
	s := str(devRa, devVirama, devKa)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidDKa, gidReph}, s)

	// The advance follows the glyph the font substituted, not the Ra it was
	// made from.
	glyphs, _ := f.ShapeGlyphs(s)
	if want := f.advanceGID(gidReph); glyphs[1].XAdvance != want {
		t.Errorf("the reph advances %v, want %v", glyphs[1].XAdvance, want)
	}
}

// TestRephAndPreBaseMatraTogether is the case that needs both reorderings and
// needs them in the right order: र्कि is drawn i-sign, ka, reph — the matra
// first, the reph last, and the consonant they both belong to in between.
func TestRephAndPreBaseMatraTogether(t *testing.T) {
	f := devaFace(t, devaRphf())
	s := str(devRa, devVirama, devKa, devIMatra)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidIMatra, gidDKa, gidReph}, s)
}

// TestLoneRaIsNotAReph is what stops the rule running away with itself. र् with
// nothing after it has no syllable to sit above, so the Ra stays a letter.
func TestLoneRaIsNotAReph(t *testing.T) {
	f := devaFace(t, devaRphf())
	s := str(devRa, devVirama)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidDRa, gidVirama}, s)
}

// TestRephNeedsTheFontToHaveOne pins that the question is asked of the font.
// A face that declares no 'rphf' draws the Ra and its virama as they stand;
// assuming a reph and reordering for it would move a letter that is still
// there.
func TestRephNeedsTheFontToHaveOne(t *testing.T) {
	s := str(devRa, devVirama, devKa)
	plain := []int{gidDRa, gidVirama, gidDKa}

	// A face with no 'rphf' at all.
	wantGIDs(t, shapedGIDs(t, devaFace(t, devaHalf()), s), plain, s)

	// And the case that needs the font to be *asked* rather than assumed: a
	// face that declares 'rphf' but not over this Ra. Taking the feature's
	// presence for a reph would drop the Ra out of the base search and move a
	// virama the font left standing.
	wantGIDs(t, shapedGIDs(t, devaFace(t, devaRphfElsewhere()), s), plain, s)
}

// TestRephNeedsSomethingToSitAbove is the other half of that guard. र् followed
// by nothing but a syllable modifier has no consonant under it, so the Ra is an
// ordinary letter — a reph over an empty syllable would be a stroke floating
// above a mark.
func TestRephNeedsSomethingToSitAbove(t *testing.T) {
	f := devaFace(t, devaRphf())
	s := str(devRa, devVirama, devAnusvara)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidDRa, gidVirama, gidAnusvara}, s)
}

// TestOnlyTheOpeningRaBecomesAReph is the teeth of the mask's *extent*, as
// distinct from its presence. र्क्र्क is one syllable holding two Ra-plus-virama
// sequences; only the first is a reph, and it is drawn after the first virama
// still standing — the second Ra is an ordinary letter of the conjunct.
func TestOnlyTheOpeningRaBecomesAReph(t *testing.T) {
	f := devaFace(t, devaRphf())
	s := str(devRa, devVirama, devKa, devVirama, devRa, devVirama, devKa)
	wantGIDs(t, shapedGIDs(t, f, s),
		[]int{gidDKa, gidVirama, gidReph, gidDRa, gidVirama, gidDKa}, s)
}

// TestRephIsOnlyMadeAtTheStartOfASyllable is the teeth of the feature masks.
// 'rphf' is a claim about the *opening* Ra of a syllable. A shaper that applied
// it wherever it matched would turn the Ra in क्र्क into a reph — a stroke over
// a syllable it does not belong to, and a letter gone from the middle of a word.
func TestRephIsOnlyMadeAtTheStartOfASyllable(t *testing.T) {
	f := devaFace(t, devaRphf())
	s := str(devKa, devVirama, devRa, devVirama, devKa)
	wantGIDs(t, shapedGIDs(t, f, s),
		[]int{gidDKa, gidVirama, gidDRa, gidVirama, gidDKa}, s)
}

// TestHalfFormIsMadeFromTheConsonantsBeforeTheBase is the third reordering's
// companion: the consonants a virama binds to the base take their half forms,
// and the base does not.
func TestHalfFormIsMadeFromTheConsonantsBeforeTheBase(t *testing.T) {
	f := devaFace(t, devaHalf())
	s := str(devTa, devVirama, devKa)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidTaHalf, gidDKa}, s)

	// The same two characters with nothing after them are not a half form:
	// there is no base for the Ta to be half of, so Ta is the base itself.
	s = str(devTa, devVirama)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidDTa, gidVirama}, s)
}

// TestABelowBaseConsonantCannotBeTheBase is what the base search cannot decide
// from the characters, and has to ask the font.
//
// In त्र the Ra is an ordinary consonant by every Unicode property it has, and
// the last one in the syllable — so taking the last consonant would make it the
// base and turn the Ta into a half form. A Devanagari font draws it as a stroke
// under the Ta, which it says by declaring a below-base form for it, and that
// makes the Ta the base.
func TestABelowBaseConsonantCannotBeTheBase(t *testing.T) {
	f := devaFace(t, devaBlwf(), devaHalf())
	s := str(devTa, devVirama, devRa)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidDTa, gidRakar}, s)

	// A font with no below-base form for its Ra says the opposite, and gets the
	// opposite: the Ra is the base and the Ta takes its half form.
	f = devaFace(t, devaHalf())
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidTaHalf, gidDRa}, s)
}

// TestRephIsDrawnUnderASyllableModifier pins where the reph stops. An anusvara
// is drawn over the syllable and the reph belongs under it, not after it — and
// a font commonly has one glyph for the two together, which it can only make if
// they are in that order.
func TestRephIsDrawnUnderASyllableModifier(t *testing.T) {
	f := devaFace(t, devaRphf())
	s := str(devRa, devVirama, devKa, devAnusvara)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidDKa, gidReph, gidAnusvara}, s)
}

// TestConjunctIsMadeFromTheWholeSyllable pins that a feature the model applies
// to all of a syllable is applied to all of it: 'cjct' joins the base to what
// precedes it, which a mask restricted to the pre-base consonants would cut in
// half.
func TestConjunctIsMadeFromTheWholeSyllable(t *testing.T) {
	f := devaFace(t, devaCjct())
	s := str(devKa, devVirama, devTa)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidKTa}, s)
}

// TestPresentationFeatureSeesTheReorderedGlyphs is what the whole ordering is
// for. 'pres' is declared over the i-sign followed by its consonant — an order
// the characters are never in. It can only fire if the reordering happened
// first, and it fires after the basic features, which is where the model puts
// it.
func TestPresentationFeatureSeesTheReorderedGlyphs(t *testing.T) {
	f := devaFace(t, devaPres())
	s := str(devKa, devIMatra)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidKaWithI}, s)
}

// TestInitAppliesToAWordInitialMatra pins the one masked feature applied after
// the reordering rather than before it, and the condition on it: the i-sign
// opening a word is drawn differently from the same sign inside one.
func TestInitAppliesToAWordInitialMatra(t *testing.T) {
	f := devaFace(t, devaInit())

	s := str(devKa, devIMatra)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidIMatraIni, gidDKa}, s)

	// The second syllable of a word is not a word start, so its matra keeps its
	// ordinary form.
	s = str(devKa, devKa, devIMatra)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidDKa, gidIMatra, gidDKa}, s)

	// After a space it is a word start again.
	s = str(devKa, ' ', devKa, devIMatra)
	got := shapedGIDs(t, f, s)
	if len(got) != 4 || got[2] != gidIMatraIni {
		t.Errorf("shaping %q gave %v; the matra after the space should take its initial form", s, got)
	}
}

// TestBasicFeaturesDoNotCrossASyllable is the teeth of the syllable bound. The
// fixture declares a ligature of two Ka, which no real font would; two Ka in a
// row are two syllables, and a basic feature applied to one of them must not
// reach into the other.
func TestBasicFeaturesDoNotCrossASyllable(t *testing.T) {
	f := devaFace(t, devaAkhn())
	s := str(devKa, devKa)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidDKa, gidDKa}, s)

	// The same in the other direction. A chained rule asking what *precedes*
	// the glyph it changes must not find its answer in the syllable before —
	// backtrack is bounded by the same wall as matching.
	f = devaFace(t, devaAkhnAfterAnything())
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidDKa, gidDKa}, s)

	// The fixture is not vacuous: within one syllable the same rule fires. A
	// virama makes the two Ka one syllable, and the second Ka then has
	// something the rule accepts in front of it.
	s = str(devKa, devVirama, devKa)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidDKa, gidVirama, gidKaKa}, s)
}

// TestSyllableTakesOneCluster pins what a caller can do with the result. Once
// the glyphs are in drawing order they no longer line up with the characters,
// so the syllable is the smallest piece that can honestly be mapped back to a
// position in the text — and the clusters must still not go backwards.
func TestSyllableTakesOneCluster(t *testing.T) {
	f := devaFace(t, devaRphf())
	s := str(devRa, devVirama, devKa, devIMatra) + str(devKa, devAAMatra)
	glyphs, _ := f.ShapeGlyphs(s)
	if len(glyphs) != 5 {
		t.Fatalf("got %d glyphs, want 5", len(glyphs))
	}
	first := len(str(devRa, devVirama, devKa, devIMatra))
	want := []int{0, 0, 0, first, first}
	for i, g := range glyphs {
		if g.Cluster != want[i] {
			t.Errorf("glyph %d has cluster %d, want %d (all: %v)", i, g.Cluster, want[i], clusters(glyphs))
		}
	}
}

func clusters(glyphs []Glyph) []int {
	out := make([]int, len(glyphs))
	for i, g := range glyphs {
		out[i] = g.Cluster
	}
	return out
}

// TestNonIndicCharactersAreLeftAlone pins that the pass does not disturb what
// it is not for. A Latin word set with a Devanagari font is still a Latin word.
func TestNonIndicCharactersAreLeftAlone(t *testing.T) {
	f := devaFace(t, devaRphf())
	// The danda is punctuation: it is in no syllable, is not reordered, and
	// does not stop the syllables on either side of it from being.
	s := str(devKa, devIMatra, 0x0964, devKa, devIMatra)
	wantGIDs(t, shapedGIDs(t, f, s),
		[]int{gidIMatra, gidDKa, gidDanda, gidIMatra, gidDKa}, s)
}

// TestIndicRunSurvivesRubbish is the untrusted-input case: characters in orders
// no language produces must shape into something rather than panic or hang.
func TestIndicRunSurvivesRubbish(t *testing.T) {
	f := devaFace(t, devaRphf(), devaHalf(), devaCjct(), devaInit(), devaPres(), devaAkhn())
	parts := []rune{devKa, devTa, devRa, devVirama, devIMatra, devAAMatra,
		devAnusvara, devA, devNukta, 0x200C, 0x200D, 0x25CC, ' ', 'A'}
	for _, a := range parts {
		for _, b := range parts {
			for _, c := range parts {
				var sb strings.Builder
				sb.WriteRune(a)
				sb.WriteRune(b)
				sb.WriteRune(c)
				glyphs, _ := f.ShapeGlyphs(sb.String())
				last := -1
				for _, g := range glyphs {
					if g.Cluster < last {
						t.Fatalf("shaping %q gave clusters %v, which go backwards",
							sb.String(), clusters(glyphs))
					}
					last = g.Cluster
				}
			}
		}
	}
}

// The reordering against the face this module ships, which is the claim that
// matters: not that a synthetic fixture behaves as its own test says, but that a
// real Devanagari word set in a real Devanagari font comes out in the order a
// reader reads it.
//
// The assertions are written against glyphs the face itself names — the Ka it
// maps U+0915 to — rather than against glyph numbers, so they say what they mean
// and survive the font being rebuilt.

// devaNoto is the bundled face, with a plain statement of what it must be for
// any of this to test anything.
func devaNoto(t *testing.T) *Face {
	t.Helper()
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading the bundled face: %v", err)
	}
	if !f.HasScript("dev2") && !f.HasScript("deva") {
		t.Fatal("the bundled face declares no Devanagari rules, so nothing below tests the reordering")
	}
	return f
}

func devaGlyph(t *testing.T, f *Face, r rune) int {
	t.Helper()
	g, ok := f.GlyphID(r)
	if !ok {
		t.Fatalf("the bundled face has no glyph for U+%04X", r)
	}
	return g
}

// TestBundledFaceDrawsAMatraBeforeItsConsonant is the whole capability in one
// line of text. कि is written ka then the i-sign; a reader sees the i-sign
// first. A shaper that did not reorder would put the consonant first, which is
// the one thing this cannot be mistaken for.
func TestBundledFaceDrawsAMatraBeforeItsConsonant(t *testing.T) {
	f := devaNoto(t)
	ka := devaGlyph(t, f, devKa)

	s := str(devKa, devIMatra)
	glyphs, missing := f.ShapeGlyphs(s)
	if missing != 0 {
		t.Fatalf("shaping %q: %d characters have no glyph", s, missing)
	}
	if len(glyphs) != 2 {
		t.Fatalf("shaping %q gave %d glyphs %v, want 2", s, len(glyphs), gids(glyphs))
	}
	if glyphs[1].GID != ka {
		t.Errorf("shaping %q gave %v; the consonant should be the *second* glyph, %d",
			s, gids(glyphs), ka)
	}
	if glyphs[0].GID == ka {
		t.Errorf("shaping %q gave %v; the consonant was drawn first, so nothing was reordered",
			s, gids(glyphs))
	}
}

// TestBundledFaceDrawsARephAtTheEnd is the second reordering against the real
// face. र्क is written ra, virama, ka; it is drawn as ka with a stroke over it,
// and the stroke comes last.
func TestBundledFaceDrawsARephAtTheEnd(t *testing.T) {
	f := devaNoto(t)
	ka := devaGlyph(t, f, devKa)
	ra := devaGlyph(t, f, devRa)
	virama := devaGlyph(t, f, devVirama)

	s := str(devRa, devVirama, devKa)
	glyphs, _ := f.ShapeGlyphs(s)
	if len(glyphs) != 2 {
		t.Fatalf("shaping %q gave %d glyphs %v, want 2: the Ra and its virama are one reph",
			s, len(glyphs), gids(glyphs))
	}
	if glyphs[0].GID != ka {
		t.Errorf("shaping %q gave %v; the consonant should come first, as glyph %d",
			s, gids(glyphs), ka)
	}
	for i, g := range glyphs {
		if g.GID == ra || g.GID == virama {
			t.Errorf("shaping %q gave %v; glyph %d is still the plain Ra or virama, so no reph was made",
				s, gids(glyphs), i)
		}
	}
}

// TestBundledFaceDrawsARephUnderAnAnusvara is the placement rule that a
// synthetic fixture can only assert and a real font can prove. र्कं has both a
// reph and an anusvara over the same syllable; Noto Sans has a single glyph for
// the two together, and can only reach it if the reph was put *under* the
// anusvara rather than after it.
func TestBundledFaceDrawsARephUnderAnAnusvara(t *testing.T) {
	f := devaNoto(t)
	ka := devaGlyph(t, f, devKa)
	anusvara := devaGlyph(t, f, devAnusvara)

	s := str(devRa, devVirama, devKa, devAnusvara)
	glyphs, _ := f.ShapeGlyphs(s)
	if len(glyphs) != 2 {
		t.Fatalf("shaping %q gave %d glyphs %v, want 2: the reph and the anusvara are one glyph",
			s, len(glyphs), gids(glyphs))
	}
	if glyphs[0].GID != ka {
		t.Errorf("shaping %q gave %v; the consonant should come first, as glyph %d",
			s, gids(glyphs), ka)
	}
	if glyphs[1].GID == anusvara {
		t.Errorf("shaping %q gave %v; the anusvara stands alone, so the reph was drawn after it rather than under it",
			s, gids(glyphs))
	}
}

// TestBundledFaceMakesAConjunct is the third reordering. क्त is three characters
// and त्र is three; each is drawn as one compound letterform, which the font can
// only make when its pieces are where its rules expect them.
func TestBundledFaceMakesAConjunct(t *testing.T) {
	f := devaNoto(t)
	virama := devaGlyph(t, f, devVirama)

	for _, tc := range []struct {
		s    string
		want int
	}{
		{str(devKa, devVirama, devTa), 2}, // a half Ka and a Ta
		{str(devTa, devVirama, devRa), 1}, // a Ta with the Ra drawn under it
	} {
		glyphs, _ := f.ShapeGlyphs(tc.s)
		if len(glyphs) != tc.want {
			t.Errorf("shaping %q gave %d glyphs %v, want %d", tc.s, len(glyphs), gids(glyphs), tc.want)
		}
		for _, g := range glyphs {
			if g.GID == virama {
				t.Errorf("shaping %q gave %v; the virama is still drawn, so no conjunct was made",
					tc.s, gids(glyphs))
			}
		}
	}
}

// TestBundledFaceSetsAWord is the whole pipeline on ordinary text: a word with a
// pre-base matra, a conjunct and a matra that does not move. The assertion is
// that its clusters still walk forwards and cover the word, which is what a
// caller mapping a glyph back to a position in the text depends on and what
// reordering is most likely to break.
func TestBundledFaceSetsAWord(t *testing.T) {
	f := devaNoto(t)
	const word = "\u0939\u093F\u0928\u094D\u0926\u0940" // हिन्दी

	glyphs, missing := f.ShapeGlyphs(word)
	if missing != 0 {
		t.Fatalf("shaping %q: %d characters have no glyph", word, missing)
	}
	if len(glyphs) == 0 {
		t.Fatalf("shaping %q gave nothing", word)
	}
	last := -1
	for i, g := range glyphs {
		if g.Cluster < last {
			t.Fatalf("shaping %q gave clusters %v, which go backwards at %d", word, clusters(glyphs), i)
		}
		if g.Cluster < 0 || g.Cluster >= len(word) {
			t.Errorf("glyph %d has cluster %d, outside the %d bytes of the word", i, g.Cluster, len(word))
		}
		last = g.Cluster
	}
	if glyphs[0].Cluster != 0 {
		t.Errorf("the first glyph has cluster %d, want 0", glyphs[0].Cluster)
	}
}

func gids(glyphs []Glyph) []int {
	out := make([]int, len(glyphs))
	for i, g := range glyphs {
		out[i] = g.GID
	}
	return out
}
