package fonts

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/fonttest"
)

// The zero-width joiner and non-joiner: obeyed, invisible to the rules that are
// not about them, and never drawn.
//
// The fixtures are the Devanagari ones (indic_test.go), because Devanagari is
// where these two characters are actually written — but two of the claims below
// are about every script, and are made against a Latin run for that reason.

// devaPresAfter declares 'pres' as a chained rule: the aa-sign, *preceded by* a
// Ka, becomes something else. The rule says nothing about what may stand between
// the two, which is the point — a join control there must not hide the Ka from
// it.
func devaPresAfter() devaFeature {
	return devaFeature{tag: "pres", build: func(base int) ([]fonttest.Lookup, []int) {
		return []fonttest.Lookup{
			{Type: 6, Subtables: [][]byte{fonttest.ChainedContext3(
				[][]int{{gidDKa}},     // preceded by a Ka
				[][]int{{gidAAMatra}}, // this aa-sign
				nil,
				[]fonttest.SeqLookup{{At: 0, Lookup: base + 1}},
			)}},
			{Type: 1, Subtables: [][]byte{fonttest.SingleSubst([]int{gidAAMatra}, []int{gidKaWithI})}},
		}, []int{base}
	}}
}

// devaPresBefore is the same claim in the other direction: a Ka *followed by* an
// aa-sign becomes something else.
func devaPresBefore() devaFeature {
	return devaFeature{tag: "pres", build: func(base int) ([]fonttest.Lookup, []int) {
		return []fonttest.Lookup{
			{Type: 6, Subtables: [][]byte{fonttest.ChainedContext3(
				nil,
				[][]int{{gidDKa}},     // this Ka
				[][]int{{gidAAMatra}}, // followed by an aa-sign
				[]fonttest.SeqLookup{{At: 0, Lookup: base + 1}},
			)}},
			{Type: 1, Subtables: [][]byte{fonttest.SingleSubst([]int{gidDKa}, []int{gidKTa})}},
		}, []int{base}
	}}
}

// TestJoinerIsNotDrawn is the plainest half of it. A joiner has no shape; a
// reader that drew one would show a blank where the writer asked for a joining
// form, and a PDF carrying one would carry a code for nothing.
func TestJoinerIsNotDrawn(t *testing.T) {
	f := devaFace(t, devaHalf())
	for _, tc := range []struct {
		name string
		s    string
		want []int
	}{
		// A joiner after a virama asks for the half form and gets it; a
		// non-joiner asks for the letters to stand apart. Either way the joiner
		// itself is gone.
		{"a joiner after a virama", str(devTa, devVirama, zeroWidthJoiner, devKa),
			[]int{gidTaHalf, gidDKa}},
		{"a non-joiner after a virama", str(devTa, devVirama, zeroWidthNonJoiner, devKa),
			[]int{gidDTa, gidVirama, gidDKa}},
		{"a joiner between two syllables", str(devKa, zeroWidthJoiner, devKa),
			[]int{gidDKa, gidDKa}},
		{"a joiner on its own", str(zeroWidthJoiner), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := shapedGIDs(t, f, tc.s)
			for _, g := range got {
				if g == gidZWJ || g == gidZWNJ {
					t.Fatalf("shaping %q gave %v, which still holds the join control's own glyph", tc.s, got)
				}
			}
			wantGIDs(t, got, tc.want, tc.s)
		})
	}
}

// TestJoinerIsNotDrawnOutsideAnIndicRun pins that this is not an Indic rule. The
// same two characters are written in Arabic and its neighbours, where they force
// or forbid a cursive join, and they are just as invisible there.
func TestJoinerIsNotDrawnOutsideAnIndicRun(t *testing.T) {
	f := devaFace(t)
	s := str('A', zeroWidthJoiner, 'A', zeroWidthNonJoiner, 'A')
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidLatinA, gidLatinA, gidLatinA}, s)
}

// arabicJoinerFace is arabicFace with a glyph for the zero-width joiner, so
// that a test can tell a shaper that removes it from one that draws it.
// Glyph indices: beh=1, its initial, medial and final shapes 2-4, alef=5,
// the joiner 6.
func arabicJoinerFace(t *testing.T) *Face {
	t.Helper()
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "Arabic",
		Glyphs: []fonttest.Glyph{
			{Rune: beh, Advance: 500, HasShape: true},
			{Rune: 0xE000, Advance: 300, HasShape: true}, // beh.init
			{Rune: 0xE001, Advance: 250, HasShape: true}, // beh.medi
			{Rune: 0xE002, Advance: 400, HasShape: true}, // beh.fina
			{Rune: alef, Advance: 350, HasShape: true},
			{Rune: zeroWidthJoiner, Advance: 0, HasShape: false},
		},
		Extra: map[string][]byte{"GSUB": joiningGSUB()},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

// TestAJoinerStillJoinsACursiveScript is the other script these two characters
// are actually written in, and the reason removing them cannot simply happen
// before shaping. A joiner after a letter that stands alone asks for the letter
// to be drawn as though a word continued after it — which is how a single Arabic
// letter is shown in a table of forms — and the joiner itself is not drawn.
func TestAJoinerStillJoinsACursiveScript(t *testing.T) {
	f := arabicJoinerFace(t)
	const behInitial, joinerGlyph = 2, 6

	s := str(beh)
	wantGIDs(t, shapedGIDs(t, f, s), []int{1}, s) // isolated: no joined form

	s = str(beh, zeroWidthJoiner)
	got := shapedGIDs(t, f, s)
	for _, g := range got {
		if g == joinerGlyph {
			t.Fatalf("shaping %q gave %v, which draws the joiner", s, got)
		}
	}
	wantGIDs(t, got, []int{behInitial}, s)
}

// TestAJoinerDoesNotHideALetterFromARule is the half that is not obvious, and
// the one that was wrong: a font's rule about what stands either side of a glyph
// is a claim about *letters*, and a joiner is not a letter. A rule that could
// not see past one would pick a plainer form than its author meant — which is
// exactly what a joiner is written to avoid.
func TestAJoinerDoesNotHideALetterFromARule(t *testing.T) {
	// Ka, joiner, aa-sign shapes to the three in that order, so the joiner
	// stands between the two glyphs each rule below is about.
	plain := devaFace(t)
	s := str(devKa, zeroWidthJoiner, devAAMatra)
	wantGIDs(t, shapedGIDs(t, plain, s), []int{gidDKa, gidAAMatra}, s)

	t.Run("as backtrack", func(t *testing.T) {
		f := devaFace(t, devaPresAfter())
		// Without the joiner, to show the rule fires at all.
		bare := str(devKa, devAAMatra)
		wantGIDs(t, shapedGIDs(t, f, bare), []int{gidDKa, gidKaWithI}, bare)
		wantGIDs(t, shapedGIDs(t, f, s), []int{gidDKa, gidKaWithI}, s)
	})

	t.Run("as lookahead", func(t *testing.T) {
		f := devaFace(t, devaPresBefore())
		bare := str(devKa, devAAMatra)
		wantGIDs(t, shapedGIDs(t, f, bare), []int{gidKTa, gidAAMatra}, bare)
		wantGIDs(t, shapedGIDs(t, f, s), []int{gidKTa, gidAAMatra}, s)
	})
}

// TestANonJoinerIsSeenByTheRulesWrittenAboutIt is the asymmetry, which is the
// specification's rather than an accident: a *non*-joiner is written to stop the
// letters either side of it being treated as neighbours, so the features that
// make joined forms must see it standing there. A shaper that stepped over it
// everywhere would do exactly what the writer asked it not to.
func TestANonJoinerIsSeenByTheRulesWrittenAboutIt(t *testing.T) {
	f := devaFace(t, devaPresAfter())
	bare := str(devKa, devAAMatra)
	wantGIDs(t, shapedGIDs(t, f, bare), []int{gidDKa, gidKaWithI}, bare)

	s := str(devKa, zeroWidthNonJoiner, devAAMatra)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidDKa, gidAAMatra}, s)
}

// TestAJoinerForbidsAConjunct is the joiner doing its actual job, and the reason
// the Indic features must not step over one while matching what they replace.
// क्त is one compound letterform; क्‍त — the same three characters with a joiner
// after the virama — asks for the half form and the letter, drawn apart.
func TestAJoinerForbidsAConjunct(t *testing.T) {
	f := devaFace(t, devaCjct())

	s := str(devKa, devVirama, devTa)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidKTa}, s)

	s = str(devKa, devVirama, zeroWidthJoiner, devTa)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidDKa, gidVirama, gidDTa}, s)

	// A non-joiner says the same thing about the conjunct.
	s = str(devKa, devVirama, zeroWidthNonJoiner, devTa)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidDKa, gidVirama, gidDTa}, s)
}

// TestANonJoinerForbidsAHalfForm pins the other of the two effects. The half
// form is what a virama between two consonants normally produces; a non-joiner
// after the virama asks for the letters to stand apart instead.
func TestANonJoinerForbidsAHalfForm(t *testing.T) {
	f := devaFace(t, devaHalf())

	s := str(devTa, devVirama, devKa)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidTaHalf, gidDKa}, s)

	s = str(devTa, devVirama, zeroWidthNonJoiner, devKa)
	wantGIDs(t, shapedGIDs(t, f, s), []int{gidDTa, gidVirama, gidDKa}, s)
}

// sharedJoinerGlyphFace is the fixture with one thing changed: its cmap sends
// the zero-width joiner to the same glyph as the space.
//
// That is not a contrivance. A face has no reason to draw a joiner and every
// reason not to carry a glyph for it, so mapping it onto the blank one is what
// faces commonly do — and it is the case that tells a shaper which removes
// joiners by *position* from one that removes them by glyph index.
func sharedJoinerGlyphFace(t *testing.T) *Face {
	t.Helper()
	glyphs := devaGlyphs()
	segs := make([][3]int, 0, len(glyphs)+1)
	for i, g := range glyphs {
		gid := i + 1
		if g.Rune == zeroWidthJoiner {
			gid = gidSpace
		}
		c := int(g.Rune)
		segs = append(segs, [3]int{c, c, (gid - c) & 0xFFFF})
	}
	sortSegments(segs)
	segs = append(segs, [3]int{0xFFFF, 0xFFFF, 1})

	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name:   "Devanagari",
		Glyphs: glyphs,
		Extra: map[string][]byte{
			"cmap": fonttest.SFNTCmapTable(fonttest.CmapFormat4(segs)),
		},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if g, _ := f.GlyphID(zeroWidthJoiner); g != gidSpace {
		t.Fatalf("the fixture maps the joiner to glyph %d, not to the space glyph %d — it tests nothing", g, gidSpace)
	}
	return f
}

func sortSegments(segs [][3]int) {
	for i := 1; i < len(segs); i++ {
		for j := i; j > 0 && segs[j][0] < segs[j-1][0]; j-- {
			segs[j], segs[j-1] = segs[j-1], segs[j]
		}
	}
}

// TestJoinerIsNotFoundByItsGlyph is the guard on how the joiners are found. In a
// face that draws a joiner as a blank, removing every glyph with that index
// would remove the spaces of the text along with the joiners — a word gone from
// the page, from a pass meant to remove nothing visible at all.
func TestJoinerIsNotFoundByItsGlyph(t *testing.T) {
	f := sharedJoinerGlyphFace(t)
	for _, s := range []string{
		str(devKa, ' ', zeroWidthJoiner, ' ', devKa), // the general path is Indic here
		str('A', ' ', zeroWidthJoiner, ' ', 'A'),     // and Latin here
	} {
		got := shapedGIDs(t, f, s)
		spaces := 0
		for _, g := range got {
			if g == gidSpace {
				spaces++
			}
		}
		if spaces != 2 {
			t.Errorf("shaping %q gave %v, holding %d spaces; the two written spaces should both survive",
				s, got, spaces)
		}
		if len(got) != 4 {
			t.Errorf("shaping %q gave %d glyphs %v, want 4: the joiner should be gone and nothing else",
				s, len(got), got)
		}
	}
}
