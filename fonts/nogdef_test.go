package fonts

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/fonttest"
)

// A font that classifies nothing.
//
// GDEF is where a font says which of its glyphs are marks, and it is what makes
// a lookup flag mean anything: IgnoreMarks can only ignore marks it can
// recognise. Plenty of fonts declare no GDEF glyph class table at all, and for
// those every flag used to match nothing — so IgnoreMarks ignored nothing, a
// lookup written to step over an accent stepped over nothing, and every rule
// that depends on the flag quietly did not apply.
//
// What is visible: "A" and "V" kern and "Ä" and "V" do not; a ligature written
// across an accent never forms; two stacked accents do not stack.
//
// The character is the authority when the font is silent — it is where the
// font's own classification came from — so a glyph carries what the character it
// came from says it is, and that answers the flag when GDEF does not.

// noGDEFFace builds a face with a ligature lookup that ignores marks, and no
// GDEF at all.
func noGDEFFace(t *testing.T, flags int, ligs []fonttest.Ligature) *Face {
	t.Helper()
	return contextFace(t, []fonttest.Lookup{{
		Type: 4, Flag: flags, Subtables: [][]byte{fonttest.LigatureSubst(ligs)},
	}}, nil)
}

// TestALookupIgnoresMarksWithoutGDEF is the defect stated as a test.
func TestALookupIgnoresMarksWithoutGDEF(t *testing.T) {
	ligs := []fonttest.Ligature{{Components: []int{gidA, gidB}, Glyph: gidBalt}}

	// The same font twice: one that classifies its glyphs and one that does
	// not. They have to shape the same, because the classification a font would
	// have written is the one the characters already imply.
	for _, tc := range []struct {
		face *Face
		why  string
	}{
		{ligatureFace(t, flagIgnoreMarks, ligs, markGDEF()), "with GDEF"},
		{noGDEFFace(t, flagIgnoreMarks, ligs), "without GDEF"},
	} {
		got := shapedGIDs(t, tc.face, "áb")
		want := []int{gidBalt, gidMark}
		if !sameGIDs(got, want) {
			t.Errorf("%s: a + acute + b shaped to %v, want %v\n"+
				"The lookup ignores marks, so it matches a and b across the accent — "+
				"and a font that declares no GDEF still has marks in it.",
				tc.why, got, want)
		}
	}
}

// TestKerningStepsOverAnAccentWithoutGDEF is the same rule where a reader
// notices it soonest. A kerning lookup almost always declares that it ignores
// marks, because the pair it means to adjust is two letters; without the flag
// working, an accent between them breaks the pair.
func TestKerningStepsOverAnAccentWithoutGDEF(t *testing.T) {
	const (
		kern    = -60
		advance = 500
	)
	build := func(gdef map[int]int) *Face {
		t.Helper()
		extra := map[string][]byte{
			"GPOS": fonttest.GPOSWithFlag(
				[]fonttest.KernPair{{Left: gidA, Right: gidB, Adjust: kern}}, flagIgnoreMarks),
		}
		if gdef != nil {
			extra["GDEF"] = fonttest.GDEF(gdef)
		}
		data := fonttest.SFNT(fonttest.SFNTOptions{
			Name: "NoGDEFKern",
			Glyphs: []fonttest.Glyph{
				{Rune: 'a', Advance: advance, HasShape: true},
				{Rune: 'b', Advance: advance, HasShape: true},
				{Rune: 'c', Advance: advance, HasShape: true},
				{Rune: 'd', Advance: advance, HasShape: true},
				{Rune: 'X', Advance: advance, HasShape: true},
				{Rune: 'Y', Advance: advance, HasShape: true},
				{Rune: acuteRne, Advance: 0, HasShape: true},
			},
			Extra: extra,
		})
		f, err := Load(data)
		if err != nil {
			t.Fatalf("loading: %v", err)
		}
		return f
	}

	for _, tc := range []struct {
		face *Face
		why  string
	}{
		{build(markGDEF()), "with GDEF"},
		{build(nil), "without GDEF"},
	} {
		plain, _ := tc.face.ShapeGlyphs("ab")
		if len(plain) != 2 || plain[0].XAdvance != advance+kern {
			t.Fatalf("%s: the control failed — \"ab\" gave %d glyphs with first advance %v",
				tc.why, len(plain), plain[0].XAdvance)
		}
		accented, _ := tc.face.ShapeGlyphs("áb")
		if len(accented) != 3 {
			t.Errorf("%s: a + acute + b shaped to %d glyphs, want 3", tc.why, len(accented))
			continue
		}
		if accented[0].XAdvance != advance+kern {
			t.Errorf("%s: \"a\" advances %v before an accented b and %v before a plain one; "+
				"the lookup ignores marks, so the accent must not break the pair",
				tc.why, accented[0].XAdvance, plain[0].XAdvance)
		}
	}
}

// TestWhatAFontSaysBeatsWhatTheCharacterImplies pins the precedence.
//
// The character is a fallback, not a second opinion. A font that classified its
// glyphs has said what they are, including by leaving one out — a face that
// deliberately does not call its accent a mark must be obeyed, or a rule its
// author wrote against that decision stops working.
func TestWhatAFontSaysBeatsWhatTheCharacterImplies(t *testing.T) {
	ligs := []fonttest.Ligature{{Components: []int{gidA, gidB}, Glyph: gidBalt}}
	// A GDEF that classifies the letters and calls the accent a base.
	f := ligatureFace(t, flagIgnoreMarks, ligs, map[int]int{
		gidA: classBase, gidB: classBase, gidMark: classBase,
	})
	got := shapedGIDs(t, f, "áb")
	want := []int{gidA, gidMark, gidB}
	if !sameGIDs(got, want) {
		t.Errorf("a + acute + b shaped to %v, want %v — this font says the accent is not "+
			"a mark, so a lookup that ignores marks must not step over it", got, want)
	}
}

// TestALigatureOfLettersIsClassifiedAsOne pins what a substitution's product is
// for a font that classifies nothing.
//
// Several letters drawn as one glyph is a ligature, and a lookup that ignores
// ligatures has to be able to tell. A letter drawn together with its own marks
// is still that letter — nothing was joined that could be called a ligature —
// which is the same distinction that decides whether a mark inside it gets a
// component to belong to.
func TestALigatureOfLettersIsClassifiedAsOne(t *testing.T) {
	// Two lookups: one makes the ligature, the next ignores ligatures and would
	// otherwise turn the product into something else.
	// Both lookups have to run, so the feature names both — contextFace names
	// only the last.
	face := contextFaceWithFeature(t, []fonttest.Lookup{
		{Type: 4, Subtables: [][]byte{fonttest.LigatureSubst(
			[]fonttest.Ligature{{Components: []int{gidA, gidB}, Glyph: gidBalt}})}},
		{Type: 4, Flag: flagIgnoreLigatures, Subtables: [][]byte{fonttest.LigatureSubst(
			[]fonttest.Ligature{{Components: []int{gidBalt, gidC}, Glyph: gidCalt}})}},
	}, "calt", []int{0, 1})

	// "ab" becomes the ligature. The second lookup ignores ligatures, so it must
	// not then join that product with the c.
	got := shapedGIDs(t, face, "abc")
	want := []int{gidBalt, gidC}
	if !sameGIDs(got, want) {
		t.Errorf("abc shaped to %v, want %v — the product of the first lookup is a "+
			"ligature, and the second ignores ligatures", got, want)
	}
}

// contextFaceWithFeature is contextFace with the feature naming whichever
// lookups the caller chooses, rather than only the last.
func contextFaceWithFeature(t *testing.T, lookups []fonttest.Lookup, tag string, indices []int) *Face {
	t.Helper()
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "Context",
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: 500, HasShape: true},
			{Rune: 'b', Advance: 500, HasShape: true},
			{Rune: 'c', Advance: 500, HasShape: true},
			{Rune: 'd', Advance: 500, HasShape: true},
			{Rune: 'X', Advance: advBalt, HasShape: true},
			{Rune: 'Y', Advance: 200, HasShape: true},
			{Rune: acuteRne, Advance: 0, HasShape: true},
		},
		Extra: map[string][]byte{
			"GSUB": fonttest.GSUBLookups(lookups, map[string][]int{tag: indices}),
		},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}
