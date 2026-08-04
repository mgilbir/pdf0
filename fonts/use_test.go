package fonts

import "testing"

// The Universal Shaping Engine's categories.
//
// The table is derived rather than tabulated — cmd/genuse computes it from five
// properties Unicode publishes plus the engine's own corrections — so what is
// worth testing is not that the generator ran, but that the answers it produces
// are the ones a shaper needs. These are checked against what the scripts
// actually do: a pre-base vowel has to come out Pre or it will not be moved,
// and a virama has to come out H or the syllable will not stack.

// TestUseCategoriesAreWhatTheScriptsNeed spot-checks the characters the engine
// turns on, across several of the scripts it covers.
func TestUseCategoriesAreWhatTheScriptsNeed(t *testing.T) {
	for _, tc := range []struct {
		r    rune
		cat  useCategory
		pos  usePosition
		what string
	}{
		// Javanese, which is what the corpus in testdata/harfbuzz measures.
		{0xA98F, useB, usePosNone, "JAVANESE LETTER KA"},
		{0xA9C0, useH, usePosNone, "JAVANESE PANGKON, which stacks the next letter"},
		{0xA9BA, useV, usePosPre, "JAVANESE VOWEL SIGN TALING, written after and drawn before"},
		{0xA9BB, useV, usePosPre, "JAVANESE VOWEL SIGN DIRGA MURE"},
		{0xA9B4, useV, usePosPst, "JAVANESE VOWEL SIGN TARUNG, which Unicode places Right"},
		{0xA980, useVM, usePosAbv, "JAVANESE SIGN PANYANGGA"},
		{0xA983, useVM, usePosPst, "JAVANESE SIGN WIGNYAN"},

		// Balinese, Buginese, Tai Tham, Chakma: four more the engine covers and
		// nothing here has ever shaped.
		{0x1B05, useB, usePosNone, "BALINESE LETTER AKARA"},
		{0x1B44, useH, usePosNone, "BALINESE ADEG ADEG"},
		{0x1A00, useB, usePosNone, "BUGINESE LETTER KA"},
		{0x1A60, useSk, usePosNone, "TAI THAM SIGN SAKOT"},
		{0x11103, useB, usePosNone, "CHAKMA LETTER KAA"},
		{0x11133, useIS, usePosNone, "CHAKMA VIRAMA, an invisible stacker"},
		// Unicode calls this a Pure_Killer, which would make it a vowel; the
		// engine overrides it to a gemination mark, which makes it a modifier.
		{0x11134, useCM, usePosAbv, "CHAKMA MAAYYAA"},

		// Sinhala's al-lakuna, which is the one character that is both a halant
		// and a vowel modifier and so has a category of its own.
		{0x0DCA, useHVM, usePosNone, "SINHALA AL-LAKUNA"},

		// Ordinary text is of no category at all, which is what makes the
		// shortcut in useCategoryOf worth having.
		{'a', useO, usePosNone, "a Latin letter"},
		{' ', useO, usePosNone, "a space"},
		// A digit is Indic_Syllabic_Category Number, and the engine's Base rule
		// takes Number — so a digit is a base a mark could hang off, which is
		// what a script that writes numbers with marks needs.
		{'1', useB, usePosNone, "a digit"},
		{0x0301, useO, usePosNone, "a Latin combining acute"},
	} {
		cat, pos := useCategoryOf(tc.r)
		if cat != tc.cat || pos != tc.pos {
			t.Errorf("U+%04X %s: category %d position %d, want %d and %d",
				tc.r, tc.what, cat, pos, tc.cat, tc.pos)
		}
	}
}

// TestTheEngineTakesItsOwnViewWhereUnicodeDiffers pins the two entries that are
// not derivable from anything, and that a shaper cannot do without.
//
// Unicode gives U+A9BE PENGKAL the position Bottom_And_Right and U+A9BF CAKRA
// the position Right, because that is where the marks are written. The engine
// overrides both — see testdata/ms-use/NOTICE.md — because that is not where
// they are *drawn*, and a shaper reading Unicode's values would put each on the
// wrong side of the letter.
//
// Verified to fail: with the override files left out of cmd/genuse's inputs,
// PENGKAL comes out Blw and CAKRA comes out Pst.
func TestTheEngineTakesItsOwnViewWhereUnicodeDiffers(t *testing.T) {
	for _, tc := range []struct {
		r        rune
		cat      useCategory
		pos      usePosition
		what     string
		unicodes string
	}{
		{0xA9BE, useM, usePosPst, "JAVANESE CONSONANT SIGN PENGKAL", "Bottom_And_Right, which would be Blw"},
		{0xA9BF, useM, usePosBlw, "JAVANESE CONSONANT SIGN CAKRA", "Right, which would be Pst"},
	} {
		cat, pos := useCategoryOf(tc.r)
		if cat != tc.cat || pos != tc.pos {
			t.Errorf("U+%04X %s: category %d position %d, want %d and %d.\n"+
				"Unicode says %s; the engine overrides it and the override file must be read.",
				tc.r, tc.what, cat, pos, tc.cat, tc.pos, tc.unicodes)
		}
	}
}

// TestTheUseTableIsSearchable guards the shape the lookup depends on: sorted,
// non-overlapping ranges, none of them empty.
func TestTheUseTableIsSearchable(t *testing.T) {
	if len(useRanges) == 0 {
		t.Fatal("the table is empty")
	}
	for i := range useRanges {
		r := useRanges[i]
		if r.lo > r.hi {
			t.Errorf("range %d is %04X..%04X, which is empty", i, r.lo, r.hi)
		}
		if i > 0 && r.lo <= useRanges[i-1].hi {
			t.Errorf("range %d (%04X..%04X) overlaps the one before it (%04X..%04X)",
				i, r.lo, r.hi, useRanges[i-1].lo, useRanges[i-1].hi)
		}
	}
	// And the binary search agrees with a walk, for every boundary in the
	// table and the characters either side of it.
	for _, r := range useRanges {
		for _, probe := range []rune{r.lo - 1, r.lo, r.hi, r.hi + 1} {
			if probe < 0 {
				continue
			}
			cat, pos := useCategoryOf(probe)
			wantCat, wantPos := useO, usePosNone
			for _, k := range useRanges {
				if probe >= k.lo && probe <= k.hi {
					wantCat, wantPos = k.cat, k.pos
					break
				}
			}
			if cat != wantCat || pos != wantPos {
				t.Fatalf("U+%04X: the search says %d/%d and the walk says %d/%d",
					probe, cat, pos, wantCat, wantPos)
			}
		}
	}
}
