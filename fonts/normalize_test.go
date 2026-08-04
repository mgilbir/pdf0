package fonts

import (
	"fmt"
	"testing"

	"github.com/mgilbir/pdf0/internal/fonttest"
)

// Normalisation: the same text, spelled two ways, set the same.
//
// Every fixture here is a face with a *stated* coverage, because coverage is
// what every decision turns on. A face that has "é" and one that does not are
// given the same two strings — the composed spelling and the decomposed one —
// and both must come out drawing the same thing. A reader that composed
// unconditionally would fail the second, one that decomposed unconditionally
// would fail the first, and one that did nothing fails both.

const (
	acute    = 0x0301 // COMBINING ACUTE ACCENT, class 230
	cedilla  = 0x0327 // COMBINING CEDILLA, class 202
	dotBelow = 0x0323 // COMBINING DOT BELOW, class 220
	eAcute   = 0x00E9 // LATIN SMALL LETTER E WITH ACUTE
	eCedilla = 0x0229 // LATIN SMALL LETTER E WITH CEDILLA
	aRing    = 0x00E5 // LATIN SMALL LETTER A WITH RING ABOVE
)

// markFace builds a face covering exactly the given characters, with GDEF
// classifying every combining mark as one — which is what a real face does, and
// what keeps the positioning pass from treating an accent as a letter.
func markFace(t *testing.T, runes ...rune) *Face {
	t.Helper()
	glyphs := make([]fonttest.Glyph, 0, len(runes))
	classes := map[int]int{}
	for i, r := range runes {
		glyphs = append(glyphs, fonttest.Glyph{Rune: r, Advance: 500, HasShape: true})
		if isCombiningMark(r) {
			classes[i+1] = classMark
		}
	}
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name:   "Marks",
		Glyphs: glyphs,
		Extra:  map[string][]byte{"GDEF": fonttest.GDEF(classes)},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

// gidOf is the glyph a face gives one character, for a test that wants to name
// the expected output by character rather than by index.
func gidOf(t *testing.T, f *Face, r rune) int {
	t.Helper()
	gid, ok := f.GlyphID(r)
	if !ok {
		t.Fatalf("the fixture has no glyph for U+%04X", r)
	}
	return gid
}

// TestNormalizeComposesWhatTheFaceHasWhole: a face with the composed character
// draws it, whichever way the text spells it. This is the case the composed
// glyph exists for — it carries the accent placement its designer chose, which
// is better than anything mark attachment can work out.
func TestNormalizeComposesWhatTheFaceHasWhole(t *testing.T) {
	f := markFace(t, 'e', eAcute, acute)
	want := []int{gidOf(t, f, eAcute)}
	for _, s := range []string{string(rune(eAcute)), "e" + string(rune(acute))} {
		wantGIDs(t, shapedGIDs(t, f, s), want, s)
	}
}

// TestNormalizeDecomposesWhatTheFaceHasNot: the other half. A face without the
// composed character draws the letter and the accent, whichever way the text
// spells it — where before it drew .notdef for the composed spelling.
func TestNormalizeDecomposesWhatTheFaceHasNot(t *testing.T) {
	f := markFace(t, 'e', acute)
	want := []int{gidOf(t, f, 'e'), gidOf(t, f, acute)}
	for _, s := range []string{string(rune(eAcute)), "e" + string(rune(acute))} {
		wantGIDs(t, shapedGIDs(t, f, s), want, s)
	}
}

// TestNormalizeKeepsAComposedCharacterItCannotTakeApart: decomposing is only an
// improvement when the face can draw every piece. A face with "é" but no bare
// acute must keep "é" whole — taking it apart would trade one drawn character
// for a drawn letter and a .notdef.
func TestNormalizeKeepsAComposedCharacterItCannotTakeApart(t *testing.T) {
	f := markFace(t, 'e', eAcute)
	wantGIDs(t, shapedGIDs(t, f, string(rune(eAcute))), []int{gidOf(t, f, eAcute)}, "é")

	// And on the Indic path, where nothing is short-circuited: every character is
	// taken apart whether the face has it whole or not, and nothing is composed
	// back — U+0958 is a composition exclusion, so once it comes apart it stays
	// apart. A face with the letter but not the nukta must therefore be left
	// alone. shapedGIDs fails outright if anything comes out as .notdef, which is
	// what the nukta would be.
	const (
		devaQa = 0x0958 // DEVANAGARI LETTER QA, which is KA and a nukta
		devaKa = 0x0915
	)
	f = markFace(t, devaKa, devaQa)
	wantGIDs(t, shapedGIDs(t, f, string(rune(devaQa))), []int{gidOf(t, f, devaQa)}, "Devanagari QA")
}

// TestNormalizeDecomposesInSteps: a character whose decomposition is itself
// decomposable is taken apart as far as the face can follow, and no further.
//
// "ḗ" is "ē" and an acute, and "ē" is "e" and a macron. A face with "ē" stops
// there; a face with only "e" goes the second step too.
func TestNormalizeDecomposesInSteps(t *testing.T) {
	const (
		eMacronAcute = 0x1E17 // LATIN SMALL LETTER E WITH MACRON AND ACUTE
		eMacron      = 0x0113 // LATIN SMALL LETTER E WITH MACRON
		macron       = 0x0304
	)
	f := markFace(t, 'e', eMacron, acute, macron)
	wantGIDs(t, shapedGIDs(t, f, string(rune(eMacronAcute))),
		[]int{gidOf(t, f, eMacron), gidOf(t, f, acute)}, "ḗ with ē in the face")

	f = markFace(t, 'e', acute, macron)
	wantGIDs(t, shapedGIDs(t, f, string(rune(eMacronAcute))),
		[]int{gidOf(t, f, 'e'), gidOf(t, f, macron), gidOf(t, f, acute)}, "ḗ without ē in the face")
}

// TestNormalizeOrdersMarksCanonically is the second of the two gaps this file
// closes, and the one the Indic work measured as most of what was left.
//
// Unicode allows two marks of different combining class in either order and says
// the two spellings mean the same thing. A font's rules are written against one
// of them. Sorting by class is what makes the other one match.
func TestNormalizeOrdersMarksCanonically(t *testing.T) {
	f := markFace(t, 'e', cedilla, acute, dotBelow)
	// Cedilla is class 202, dot below 220, acute 230, so canonical order is the
	// order they are listed here whatever order they are written in.
	want := []int{gidOf(t, f, 'e'), gidOf(t, f, cedilla), gidOf(t, f, dotBelow), gidOf(t, f, acute)}
	for _, order := range [][]rune{
		{cedilla, dotBelow, acute},
		{acute, dotBelow, cedilla},
		{dotBelow, acute, cedilla},
		{acute, cedilla, dotBelow},
	} {
		s := "e" + string(order)
		wantGIDs(t, shapedGIDs(t, f, s), want, fmt.Sprintf("e with marks written %04X", order))
	}
}

// TestNormalizeKeepsMarksOfOneClassInTheOrderWritten: two marks of the same
// class are not canonically equivalent in the other order — they stack, and
// which is nearer the letter is what the text says. So the sort is stable.
func TestNormalizeKeepsMarksOfOneClassInTheOrderWritten(t *testing.T) {
	const grave = 0x0300 // also class 230
	f := markFace(t, 'e', acute, grave)
	for _, order := range [][]rune{{acute, grave}, {grave, acute}} {
		s := "e" + string(order)
		want := []int{gidOf(t, f, 'e'), gidOf(t, f, order[0]), gidOf(t, f, order[1])}
		wantGIDs(t, shapedGIDs(t, f, s), want, s)
	}
}

// TestNormalizeComposesOnlyThroughLowerMarks: a mark is composed onto its
// starter only when nothing drawn further in stands between them. "e", a dot
// below and an acute must not compose to "é" with the dot left dangling —
// the acute would jump past a mark it is written outside of.
func TestNormalizeComposesOnlyThroughLowerMarks(t *testing.T) {
	f := markFace(t, 'e', eAcute, acute, dotBelow)
	// Dot below is class 220 and the acute 230, so the acute is *not* blocked:
	// the mark between it and the starter is drawn closer in.
	wantGIDs(t, shapedGIDs(t, f, "e"+string([]rune{dotBelow, acute})),
		[]int{gidOf(t, f, eAcute), gidOf(t, f, dotBelow)}, "e, dot below, acute")

	// With a mark of the *same* class in between there is nothing to compose
	// through, and the acute stays where it is.
	const grave = 0x0300
	f = markFace(t, 'e', eAcute, acute, grave)
	wantGIDs(t, shapedGIDs(t, f, "e"+string([]rune{grave, acute})),
		[]int{gidOf(t, f, 'e'), gidOf(t, f, grave), gidOf(t, f, acute)}, "e, grave, acute")
}

// TestNormalizeKeepsClusters: a Glyph.Cluster is the byte offset of the first
// character its glyph came from, and every step here has to keep that true or
// selection and hit-testing break.
//
// The texts are written with escapes rather than as literal accented characters,
// because the whole point of each case is *which* of the two spellings it is and
// how many bytes each character takes.
func TestNormalizeKeepsClusters(t *testing.T) {
	for _, tc := range []struct {
		why   string
		face  []rune
		text  string
		want  []int
		count int
	}{
		{
			why:  "a decomposition gives both pieces the character they came from",
			face: []rune{'a', 'e', acute},
			// "a" at 0, the composed \u00E9 at 1 and two bytes long, "b" at 3.
			// Both halves it comes apart into must point at 1.
			text: "a\u00e9b", want: []int{0, 1, 1, 3}, count: 4,
		},
		{
			why:  "a composition takes the earliest of what went into it",
			face: []rune{'a', 'e', eAcute, acute},
			// "a" at 0, "e" at 1, the acute at 2 and two bytes long, "b" at 4.
			// The \u00E9 they compose to must point at the "e".
			text: "ae\u0301b", want: []int{0, 1, 4}, count: 3,
		},
		{
			why:  "a composition reaches over a mark, and takes that with it",
			face: []rune{'a', 'e', eAcute, acute, dotBelow},
			// "e" at 1, the dot below at 2, the acute at 4, "b" at 6. The acute
			// composes onto the "e" over the dot, so the dot is no longer the
			// glyph the text's order says it is: the span it is in takes the
			// earliest offset in it.
			text: "ae\u0323\u0301b", want: []int{0, 1, 1, 6}, count: 4,
		},
		{
			why:  "a mark that moves past another takes the span down with it",
			face: []rune{'e', acute, cedilla},
			// The acute is written first at offset 1 and drawn second; the
			// cedilla is written second at offset 3 and drawn first. Both end up
			// at 1, the earliest character of the span they now cover.
			text: "e\u0301\u0327", want: []int{0, 1, 1}, count: 3,
		},
	} {
		// Every fixture above ends in a letter the face must also have.
		f := markFace(t, append(append([]rune(nil), tc.face...), 'b')...)
		glyphs, missing := f.ShapeGlyphs(tc.text)
		if missing != 0 {
			t.Errorf("%s: %d characters have no glyph", tc.why, missing)
			continue
		}
		if len(glyphs) != tc.count {
			t.Errorf("%s: %q gave %d glyphs, want %d", tc.why, tc.text, len(glyphs), tc.count)
			continue
		}
		got := make([]int, len(glyphs))
		for i, g := range glyphs {
			got[i] = g.Cluster
		}
		if !sameInts(got, tc.want) {
			t.Errorf("%s: %q gave clusters %v, want %v", tc.why, tc.text, got, tc.want)
		}
		// And the properties the contract rests on, whatever the offsets are: a
		// cluster names a character of the text, and they do not go backwards.
		for i, c := range got {
			if c < 0 || c >= len(tc.text) {
				t.Errorf("%s: %q gave a cluster %d outside the text", tc.why, tc.text, c)
				break
			}
			if i > 0 && c < got[i-1] {
				t.Errorf("%s: %q gave clusters that go backwards at %d", tc.why, tc.text, i)
				break
			}
		}
	}
}

// TestNormalizeShortcutAgreesWithTheAlgorithm is the only thing that makes the
// shortcut in normalize safe to have.
//
// A run that cannot be changed is returned untouched without any of the three
// rounds running, because the pipeline asks this of every string it sets and
// almost every answer is no. A shortcut that disagreed with the algorithm would
// be a silent wrong answer on exactly the text nobody thinks to check, so every
// case it claims is checked against the algorithm itself.
func TestNormalizeShortcutAgreesWithTheAlgorithm(t *testing.T) {
	f := markFace(t, 'a', 'b', 'e', ' ', eAcute, acute, cedilla, dotBelow, aRing)
	for _, s := range []string{
		"", "a", "abe", "a b e", "é", "å", "éå", "é a",
	} {
		runes, offsets := bidiRunCharacters(s, false)
		if f.needsNormalizing(runes, true) {
			t.Errorf("%q was not taken by the shortcut; the case proves nothing", s)
			continue
		}
		gotRunes, gotOffsets := runes, offsets
		wantRunes, wantOffsets := f.normalizeAlways(runes, offsets, false)
		if !sameRunes(gotRunes, wantRunes) || !sameInts(gotOffsets, wantOffsets) {
			t.Errorf("%q: the shortcut gives %04X/%v and the algorithm %04X/%v",
				s, gotRunes, gotOffsets, wantRunes, wantOffsets)
		}
	}

	// And the other half: text the algorithm would change must not be taken by
	// the shortcut.
	for _, tc := range []struct {
		s     string
		short bool
	}{
		{"e" + string(rune(acute)), true},    // a mark: a cluster to order
		{string(rune(eCedilla)), true},       // composed, and the face has not got it
		{string([]rune{'a', 0x1E17}), true},  // likewise, two steps deep
		{string(rune(eAcute)), false},        // the Indic path takes it apart anyway
		{"a" + string(rune(0x0FC6)), true},   // a mark from a script this does not shape
		{string([]rune{0xAC00, 'a'}), false}, // a Hangul syllable, on the Indic path
	} {
		runes, _ := bidiRunCharacters(tc.s, false)
		if !f.needsNormalizing(runes, tc.short) {
			t.Errorf("%q was taken by the shortcut with shortest=%v, and it must not be", tc.s, tc.short)
		}
	}
}

// normalizeAlways is normalize with the shortcut taken out, for the test above.
func (f *Face) normalizeAlways(runes []rune, offsets []int, indic bool) ([]rune, []int) {
	n := normalizer{
		f: f, indic: indic, shortest: !indic,
		out: make([]rune, 0, len(runes)+4),
		off: make([]int, 0, len(runes)+4),
	}
	if n.decomposeRound(runes, offsets) {
		return n.out, n.off
	}
	n.reorderRound()
	return n.composeRound()
}

func sameRunes(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestNormalizeBoundsALongRunOfMarks: a font is untrusted input and so is the
// text. The mark sort is quadratic, so a run longer than the cap is left in the
// order it was written rather than sorted — and nothing hangs or panics.
func TestNormalizeBoundsALongRunOfMarks(t *testing.T) {
	if maxCombiningMarks > 1024 {
		t.Fatalf("maxCombiningMarks is %d, which is not a bound on anything", maxCombiningMarks)
	}
	f := markFace(t, 'e', acute, cedilla)
	long := []rune{'e'}
	for i := 0; i < maxCombiningMarks+4; i++ {
		long = append(long, acute, cedilla)
	}
	glyphs, _ := f.ShapeGlyphs(string(long))
	if len(glyphs) != len(long) {
		t.Fatalf("a run of %d marks gave %d glyphs, want %d", len(long)-1, len(glyphs), len(long))
	}
	// Written acute-then-cedilla throughout and longer than the cap, so it must
	// come back in exactly that order.
	if glyphs[1].GID != gidOf(t, f, acute) || glyphs[2].GID != gidOf(t, f, cedilla) {
		t.Errorf("a run past the cap was reordered; it must be left as written")
	}
	// A short run of the same marks is sorted, which is what makes the case
	// above about the cap and not about the sort being broken.
	short, _ := f.ShapeGlyphs("e" + string([]rune{acute, cedilla}))
	if short[1].GID != gidOf(t, f, cedilla) {
		t.Errorf("a run inside the cap was not sorted")
	}
}

// TestCanonicalHangul: Hangul's decomposition and composition are arithmetic
// rather than table entries, so they are checked against the standard's own
// worked values.
func TestCanonicalHangul(t *testing.T) {
	for _, tc := range []struct{ s, a, b rune }{
		{0xAC00, 0x1100, 0x1161}, // 가 = leading g + vowel a, no trailing
		{0xAC01, 0xAC00, 0x11A8}, // 각 = 가 and a trailing g, not three jamo
		{0xD7A3, 0xD788, 0x11C2}, // the last syllable, likewise
		{0xB4A4, 0x1103, 0x1171}, // 뒤, which has no trailing consonant
	} {
		a, b, ok := canonicalDecompose(tc.s)
		if !ok || a != tc.a || b != tc.b {
			t.Errorf("U+%04X came apart into U+%04X U+%04X (%v), want U+%04X U+%04X", tc.s, a, b, ok, tc.a, tc.b)
		}
		if got, ok := canonicalCompose(tc.a, tc.b); !ok || got != tc.s {
			t.Errorf("U+%04X and U+%04X composed to U+%04X (%v), want U+%04X", tc.a, tc.b, got, ok, tc.s)
		}
	}
	// U+11A7 is the filler that means "no trailing consonant" and composes with
	// nothing: composing it would turn 가 into itself and consume a character.
	if got, ok := canonicalCompose(0xAC00, 0x11A7); ok {
		t.Errorf("the trailing-jamo filler composed to U+%04X, and it must not", got)
	}
	// A syllable that already has a trailing consonant takes no second one.
	if got, ok := canonicalCompose(0xAC01, 0x11A8); ok {
		t.Errorf("a syllable with a trailing consonant took another, giving U+%04X", got)
	}
}

// TestCanonicalTablesAreSorted: every lookup here binary-searches, so a table
// out of order would silently answer "no" for characters that are in it.
func TestCanonicalTablesAreSorted(t *testing.T) {
	for i := 1; i < len(charClasses); i++ {
		if charClasses[i].lo <= charClasses[i-1].hi {
			t.Fatalf("charClasses is out of order at %d: U+%04X after U+%04X", i, charClasses[i].lo, charClasses[i-1].hi)
		}
	}
	for i := 1; i < len(canonicalDecompositions); i++ {
		if canonicalDecompositions[i].r <= canonicalDecompositions[i-1].r {
			t.Fatalf("canonicalDecompositions is out of order at %d", i)
		}
	}
	for i := 1; i < len(canonicalCompositions); i++ {
		a, b := canonicalCompositions[i-1], canonicalCompositions[i]
		if a.a > b.a || (a.a == b.a && a.b >= b.b) {
			t.Fatalf("canonicalCompositions is out of order at %d", i)
		}
	}
	// And that a search actually finds *every* entry that is in them. Sampling
	// would not do: a comparator that ignores the second half of a composition
	// pair still finds the first entry of each group, so only walking the whole
	// table shows it up.
	for _, d := range canonicalDecompositions {
		if a, b, ok := canonicalDecompose(d.r); !ok || a != d.a || b != d.b {
			t.Fatalf("U+%04X is in the table but came apart into U+%04X U+%04X (%v)", d.r, a, b, ok)
		}
	}
	for _, c := range canonicalCompositions {
		if got, ok := canonicalCompose(c.a, c.b); !ok || got != c.ab {
			t.Fatalf("U+%04X and U+%04X are in the table but composed to U+%04X (%v)", c.a, c.b, got, ok)
		}
	}
	for _, c := range charClasses {
		for _, r := range []rune{c.lo, (c.lo + c.hi) / 2, c.hi} {
			if ccc, mark := charClassOf(r); ccc != c.ccc || mark != c.mark {
				t.Fatalf("U+%04X is in a range with class %d/%v but read as %d/%v", r, c.ccc, c.mark, ccc, mark)
			}
		}
	}
}

// TestCanonicalCompositionExcludesWhatUnicodeExcludes: the composition table is
// derived rather than read, so the exclusions it applies are worth naming. Each
// of these has a canonical decomposition that must not be put back together.
func TestCanonicalCompositionExcludesWhatUnicodeExcludes(t *testing.T) {
	for _, tc := range []struct {
		a, b rune
		why  string
	}{
		{0x0915, 0x093C, "a script-specific exclusion: Devanagari QA"},
		{0x09AF, 0x09BC, "another: Bengali YYA"},
		{0x0A32, 0x0A3C, "and a Gurmukhi one"},
		{0x0308, 0x0301, "a non-starter decomposition: the first part is itself a mark"},
	} {
		if got, ok := canonicalCompose(tc.a, tc.b); ok {
			t.Errorf("%s: U+%04X and U+%04X composed to U+%04X, and must not", tc.why, tc.a, tc.b, got)
		}
	}
	// A singleton decomposition has no pair to compose from at all.
	if a, b, ok := canonicalDecompose(0x212B); !ok || b != 0 || a != aRing-0x20 {
		t.Errorf("U+212B ANGSTROM SIGN came apart into U+%04X U+%04X (%v), want the single U+%04X", a, b, ok, aRing-0x20)
	}
}

// TestNormalizeLeavesTheIndicModelWhatItStatesDifferently: two places where the
// Indic shaping model disagrees with Unicode's own tables, and where following
// Unicode would set the text wrong.
func TestNormalizeLeavesTheIndicModelWhatItStatesDifferently(t *testing.T) {
	indic := normalizer{indic: true}
	plain := normalizer{}

	// Four letters that Unicode says are a letter and a nukta, and that the
	// languages writing them treat as letters with conjuncts of their own.
	for _, r := range []rune{0x0931, 0x09DC, 0x09DD, 0x0B94} {
		if _, _, ok := indic.decompose(r); ok {
			t.Errorf("U+%04X came apart in an Indic run, and must not", r)
		}
		if _, _, ok := plain.decompose(r); !ok {
			t.Errorf("U+%04X did not come apart outside one; the case proves nothing", r)
		}
	}

	// A vowel sign drawn as several marks is left to indic.go, which splits it
	// after the check for sequences nobody writes — see decompose.
	const malayalamOO = 0x0D4B
	if _, _, ok := indic.decompose(malayalamOO); ok {
		t.Errorf("a split vowel sign came apart in an Indic run, and must not")
	}
	if _, _, ok := plain.decompose(malayalamOO); !ok {
		t.Errorf("a split vowel sign did not come apart outside one; the case proves nothing")
	}

	// Its parts must not be put back together either: they go on different sides
	// of the letter, and the model cannot place the sign whole.
	if got, ok := indic.compose(0x0D46, 0x0D3E); ok {
		t.Errorf("two marks composed to U+%04X in an Indic run, and must not", got)
	}

	// And the one exclusion an Indic font wants back: Unicode will not compose
	// Bengali YYA, and every Bengali font draws it as one letter.
	if got, ok := indic.compose(0x09AF, 0x09BC); !ok || got != 0x09DF {
		t.Errorf("Bengali YA and a nukta composed to U+%04X (%v), want U+09DF", got, ok)
	}
	if got, ok := plain.compose(0x09AF, 0x09BC); ok {
		t.Errorf("outside an Indic run they composed to U+%04X; the case proves nothing", got)
	}
}

// TestReorderClassesNameClassesTheDataHas: the reordering table is written
// against particular combining classes, and a class no character has is a rule
// that can never fire. cmd/gencanonical makes this check when it regenerates;
// this makes it against the tables as they are committed, which is what ships.
func TestReorderClassesNameClassesTheDataHas(t *testing.T) {
	present := map[uint8]bool{}
	for _, c := range charClasses {
		present[c.ccc] = true
	}
	for ccc, to := range reorderClasses {
		if to == 0 {
			continue
		}
		if !present[uint8(ccc)] {
			t.Errorf("combining class %d is reordered to %d, and no character has it", ccc, to)
		}
	}
}

// BenchmarkNormalizeASCII measures the cost on text that needs none of this,
// which is the cost the shortcut exists to keep small: the pipeline asks the
// question of every string it sets.
func BenchmarkNormalizeASCII(b *testing.B) {
	f, err := NotoSans()
	if err != nil {
		b.Fatal(err)
	}
	const text = "The quick brown fox jumps over the lazy dog, and then some more."
	runes, offsets := bidiRunCharacters(text, false)
	b.ReportAllocs()
	for b.Loop() {
		f.normalize(runes, offsets, false, false)
	}
}

// BenchmarkNormalizePrecomposed is French, whose accents this face has as single
// characters. It takes the shortcut too — that is the point of the second half of
// the test above — and the extra cost over ASCII is the table lookups those
// characters cost.
func BenchmarkNormalizePrecomposed(b *testing.B) {
	f, err := NotoSans()
	if err != nil {
		b.Fatal(err)
	}
	const text = "Le vif renard brun saute par-dessus le chien paresseux, à côté d'où l'été."
	runes, offsets := bidiRunCharacters(text, false)
	if !normalizeIsShortcut(b, f, runes) {
		b.Fatal("this text was meant to take the shortcut")
	}
	b.ReportAllocs()
	for b.Loop() {
		f.normalize(runes, offsets, false, false)
	}
}

// BenchmarkNormalizeDecomposed is the same text written the other way, with each
// accent as its own character. It is what the full three rounds cost.
func BenchmarkNormalizeDecomposed(b *testing.B) {
	f, err := NotoSans()
	if err != nil {
		b.Fatal(err)
	}
	text := "Le vif renard brun saute par-dessus le chien paresseux, a" +
		string(rune(0x0300)) + " co" + string(rune(0x0302)) + "te" +
		string(rune(0x0301)) + " d'ou" + string(rune(0x0300)) + " l'e" +
		string(rune(0x0301)) + "te" + string(rune(0x0301)) + "."
	runes, offsets := bidiRunCharacters(text, false)
	if normalizeIsShortcut(b, f, runes) {
		b.Fatal("this text was meant to need the full algorithm")
	}
	b.ReportAllocs()
	for b.Loop() {
		f.normalize(runes, offsets, false, false)
	}
}

func normalizeIsShortcut(b *testing.B, f *Face, runes []rune) bool {
	b.Helper()
	return !f.needsNormalizing(runes, true)
}
