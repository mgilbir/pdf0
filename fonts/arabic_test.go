package fonts

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/fonttest"
)

// Arabic joining, on a font that declares the four positional forms.
//
// The fixture uses three real Arabic letters whose joining behaviour differs,
// which is what makes the test meaningful: beh joins on both sides, alef joins
// only on its right, and a vowel sign is transparent and must not break a join.
const (
	beh   = 0x0628 // ARABIC LETTER BEH, dual-joining
	alef  = 0x0627 // ARABIC LETTER ALEF, right-joining
	fatha = 0x064E // ARABIC FATHA, a transparent mark
	space = 0x0020 // non-joining
)

// TestJoiningTypesAreUnicodes pins the generated table against the characters
// whose behaviour the rest of this rests on.
func TestJoiningTypesAreUnicodes(t *testing.T) {
	cases := map[rune]joiningType{
		beh:    joinD,
		alef:   joinR,
		fatha:  joinT,
		space:  joinU,
		'A':    joinU,
		0x0640: joinC, // ARABIC TATWEEL, join-causing
		0x200D: joinC, // ZERO WIDTH JOINER
		0x200C: joinU, // ZERO WIDTH NON-JOINER
	}
	for r, want := range cases {
		if got := joiningTypeOf(r); got != want {
			t.Errorf("joiningTypeOf(U+%04X) = %d, want %d", r, got, want)
		}
	}
	// A combining mark Unicode does not list is transparent by category, so a
	// vowel from another script does not break an Arabic join either.
	if got := joiningTypeOf(0x0301); got != joinT {
		t.Errorf("a combining acute is type %d, want transparent", got)
	}
}

// TestJoinFormsFollowTheNeighbours is the algorithm. A dual-joining letter
// takes all four shapes according to what is on each side; a right-joining one
// takes only two, because it cannot join forwards — which is why a word
// containing alef breaks in the middle, and why getting this wrong is visible.
func TestJoinFormsFollowTheNeighbours(t *testing.T) {
	cases := []struct {
		name  string
		runes []rune
		want  []string
	}{
		{"one letter alone", []rune{beh}, []string{featIsolated}},
		{"three dual-joining", []rune{beh, beh, beh},
			[]string{featInitial, featMedial, featFinal}},
		{"alef cannot join forwards", []rune{beh, alef, beh},
			// The alef joins back to the beh before it and not on to the one
			// after, so the third letter starts a new join rather than
			// continuing one.
			[]string{featInitial, featFinal, featIsolated}},
		{"a space breaks the word", []rune{beh, space, beh},
			[]string{featIsolated, "", featIsolated}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := joinForms(tc.runes)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d forms, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("position %d: form %q, want %q (all: %v)", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

// TestTransparentMarksDoNotBreakAJoin is the case a naive implementation gets
// wrong. A vowel sign written between two letters is skipped when deciding what
// each joins to; treating it as an ordinary character would break every
// vocalised word into isolated letters.
func TestTransparentMarksDoNotBreakAJoin(t *testing.T) {
	forms := joinForms([]rune{beh, fatha, beh})
	if forms[0] != featInitial {
		t.Errorf("the first letter is %q, want %q: the mark must not break the join", forms[0], featInitial)
	}
	if forms[1] != "" {
		t.Errorf("the mark took the form %q; it has none of its own", forms[1])
	}
	if forms[2] != featFinal {
		t.Errorf("the last letter is %q, want %q", forms[2], featFinal)
	}
}

// arabicFace declares the four positional forms for one dual-joining letter.
// Glyph indices: beh=1, and its initial, medial, final and isolated shapes 2-5.
func arabicFace(t *testing.T) *Face {
	t.Helper()
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "Arabic",
		Glyphs: []fonttest.Glyph{
			{Rune: beh, Advance: 500, HasShape: true},    // 1, the isolated letter
			{Rune: 0xE000, Advance: 300, HasShape: true}, // 2, stands for beh.init
			{Rune: 0xE001, Advance: 250, HasShape: true}, // 3, beh.medi
			{Rune: 0xE002, Advance: 400, HasShape: true}, // 4, beh.fina
			{Rune: alef, Advance: 350, HasShape: true},   // 5
		},
		Extra: map[string][]byte{
			"GSUB": joiningGSUB(),
		},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

// joiningGSUB declares beh's three joined shapes under the features that name
// them. The fixture builder writes one feature per table, so the three are
// concatenated into a font by declaring them in separate GSUB tables is not
// possible — instead one table carries all three lookups, which is what a real
// font does.
func joiningGSUB() []byte {
	return fonttest.GSUBForms(map[string][2][]int{
		"init": {{1}, {2}},
		"medi": {{1}, {3}},
		"fina": {{1}, {4}},
	})
}

// TestArabicWordTakesItsJoinedForms is the capability end to end: three letters
// become initial, medial and final shapes, which is what makes the word legible
// rather than a row of disconnected letters.
//
// The glyphs come back in the order they are drawn, and Arabic is drawn right to
// left, so the *final* shape is emitted first and the initial one last. That is
// not a detail of this test: emitting them the other way round draws the word
// backwards, which is what this package did before it had a bidirectional pass.
func TestArabicWordTakesItsJoinedForms(t *testing.T) {
	f := arabicFace(t)
	if !f.HasJoiningForms() {
		t.Fatal("the fixture's positional forms were not read")
	}
	glyphs, missing := f.ShapeGlyphs(string([]rune{beh, beh, beh}))
	if missing != 0 {
		t.Fatalf("%d runes missing", missing)
	}
	want := []int{4, 3, 2} // final, medial, initial: leftmost drawn first
	if len(glyphs) != len(want) {
		t.Fatalf("got %d glyphs, want %d", len(glyphs), len(want))
	}
	for i, g := range glyphs {
		if g.GID != want[i] {
			t.Errorf("position %d: glyph %d, want %d", i, g.GID, want[i])
		}
	}
	// The clusters run backwards through the string, which is how a caller maps
	// a glyph on the page back to the character it was written as. Beh is two
	// bytes in UTF-8, so the offsets are 4, 2, 0.
	for i, g := range glyphs {
		if want := 2 * (len(glyphs) - 1 - i); g.Cluster != want {
			t.Errorf("position %d came from byte %d, want %d", i, g.Cluster, want)
		}
	}
	// The advances follow the shapes, not the isolated letter: a joined form is
	// narrower, and a shaper that substituted the glyph without the advance
	// would leave gaps.
	if glyphs[1].XAdvance != 250 {
		t.Errorf("the medial form's advance is %v, want 250", glyphs[1].XAdvance)
	}
}

// joiningFaceFor is arabicFace with the positional forms declared under a
// chosen script rather than under the default one.
func joiningFaceFor(t *testing.T, script string) *Face {
	t.Helper()
	forms := map[string][2][]int{
		"init": {{1}, {2}},
		"medi": {{1}, {3}},
		"fina": {{1}, {4}},
	}
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "ArabicOneScript",
		Glyphs: []fonttest.Glyph{
			{Rune: beh, Advance: 500, HasShape: true},
			{Rune: 0xE000, Advance: 300, HasShape: true},
			{Rune: 0xE001, Advance: 250, HasShape: true},
			{Rune: 0xE002, Advance: 400, HasShape: true},
			{Rune: alef, Advance: 350, HasShape: true},
		},
		Extra: map[string][]byte{
			// The features are indexed in tag order: fina, init, medi. The
			// default script is declared and selects nothing, which is what
			// makes the font's claim narrow: these forms are for one script,
			// and any other run is to be set plainly.
			"GSUB": fonttest.GSUBFormsIn(forms, map[string]fonttest.Script{
				script: {Required: fonttest.NoFeature, Features: []int{0, 1, 2}},
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

// TestJoiningFormsBelongToTheScriptThatDeclaresThem pins that the positional
// shapes are a claim about one script, not about the font.
//
// A face may carry 'init', 'medi' and 'fina' for a cursive script it covers and
// mean them for that script alone. Applied to a run in another, they substitute
// glyphs the font never meant to appear there — and the substitution is by
// glyph index, so it is not stopped by the letters being different.
func TestJoiningFormsBelongToTheScriptThatDeclaresThem(t *testing.T) {
	word := string([]rune{beh, beh, beh})

	declared := joiningFaceFor(t, "arab")
	got := shapedGIDs(t, declared, word)
	// Final, medial, initial: the shapes a joined word takes, in the order they
	// are drawn.
	want := []int{4, 3, 2}
	wantGIDs(t, got, want, word)

	// The same font declaring the same forms for Latin instead: an Arabic word
	// takes none of them, and stays a row of isolated letters.
	elsewhere := joiningFaceFor(t, "latn")
	if !elsewhere.HasJoiningForms() {
		t.Fatal("the fixture is wrong: the font does carry positional forms, whatever script they are for")
	}
	got = shapedGIDs(t, elsewhere, word)
	wantGIDs(t, got, []int{1, 1, 1}, word)
}

// TestUnjoinedScriptIsUntouched pins that a font with no positional forms is
// left exactly alone, which is every font for a script that does not join.
func TestUnjoinedScriptIsUntouched(t *testing.T) {
	f := loadTestFace(t, alphabet()...)
	if f.HasJoiningForms() {
		t.Fatal("a Latin fixture reported positional forms")
	}
	glyphs, _ := f.ShapeGlyphs("abc")
	for i, g := range glyphs {
		want, _ := f.GlyphIDForTest(rune('a' + i))
		if g.GID != want {
			t.Errorf("glyph %d was substituted: %d, want %d", i, g.GID, want)
		}
	}
}
