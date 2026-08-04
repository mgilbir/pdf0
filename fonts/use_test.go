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

// The cluster grammar.
//
// What the grammar answers that a scan could not is *what kind* of cluster a
// stretch of characters is, and two things hang off that: a cluster with no base
// is broken and gets a dotted circle, and a character the model calls Other
// begins a cluster rather than sitting between two.
//
// These assert the kinds directly rather than through a font, so that a failure
// says which production went wrong instead of which glyph moved.

// useClusterOf is the grammar run over a string.
func useClustersOf(s string) []useCluster {
	runes := []rune(s)
	info := make([]useInfo, len(runes))
	for i, r := range runes {
		info[i].cat, info[i].pos = useCategoryOf(r)
	}
	return useClusters(info, runes)
}

func (k useClusterKind) String() string {
	switch k {
	case useNonCluster:
		return "non-cluster"
	case useViramaTerminatedCluster:
		return "virama-terminated"
	case useSakotTerminatedCluster:
		return "sakot-terminated"
	case useStandardCluster:
		return "standard"
	case useNumberJoinerTerminatedCluster:
		return "number-joiner-terminated"
	case useNumeralCluster:
		return "numeral"
	case useSymbolCluster:
		return "symbol"
	case useBrokenCluster:
		return "broken"
	}
	return "?"
}

// TestTheUseGrammarSaysWhatEachClusterIs is the grammar, stated as a test.
func TestTheUseGrammarSaysWhatEachClusterIs(t *testing.T) {
	const (
		balA       = 0x1B05 // BALINESE LETTER AKARA, a base
		balAdeg    = 0x1B44 // BALINESE ADEG ADEG, the virama
		balRaRepa  = 0x1B3F // BALINESE VOWEL SIGN RA REPA
		balUlu     = 0x1B36 // BALINESE VOWEL SIGN ULU, above
		balPam     = 0x1B60 // BALINESE PAMENENG, which the engine calls Other
		javKa      = 0xA98F // JAVANESE LETTER KA
		javTaling  = 0xA9BA // JAVANESE VOWEL SIGN TALING, drawn before the letter
		javPangkon = 0xA9C0
	)
	for _, tc := range []struct {
		in    []rune
		kinds []useClusterKind
		spans [][2]int
		why   string
	}{
		{
			[]rune{balA}, []useClusterKind{useStandardCluster}, [][2]int{{0, 1}},
			"a letter on its own",
		},
		{
			[]rune{balA, balUlu}, []useClusterKind{useStandardCluster}, [][2]int{{0, 2}},
			"a letter and a vowel above it",
		},
		{
			[]rune{balUlu}, []useClusterKind{useBrokenCluster}, [][2]int{{0, 1}},
			"a vowel with no letter: broken, and what a dotted circle is for",
		},
		{
			[]rune{balA, balAdeg}, []useClusterKind{useStandardCluster}, [][2]int{{0, 2}},
			"a letter and a bare virama, which is standard and not virama-terminated: " +
				"that kind is for the invisible stacker and the reordering killer",
		},
		{
			[]rune{balA, balAdeg, balA}, []useClusterKind{useStandardCluster}, [][2]int{{0, 3}},
			"a virama joining two letters into one cluster",
		},
		// The case the fuzzer found, and the reason a scan could not answer it.
		// Pameneng is Other, which is not a gap between clusters: it begins one
		// and takes the vowel written after it onto itself.
		{
			[]rune{balA, balPam, balRaRepa},
			[]useClusterKind{useStandardCluster, useSymbolCluster},
			[][2]int{{0, 1}, {1, 3}},
			"a symbol between a letter and a vowel: the vowel belongs to the symbol",
		},
		{
			[]rune{balPam}, []useClusterKind{useSymbolCluster}, [][2]int{{0, 1}},
			"a symbol on its own",
		},
		{
			[]rune{javKa, javTaling}, []useClusterKind{useStandardCluster}, [][2]int{{0, 2}},
			"a letter and a vowel written after it and drawn before it",
		},
		{
			[]rune{javKa, javPangkon, javKa, javTaling},
			[]useClusterKind{useStandardCluster}, [][2]int{{0, 4}},
			"two letters stacked, carrying a pre-base vowel",
		},
		{
			[]rune{'A'}, []useClusterKind{useSymbolCluster}, [][2]int{{0, 1}},
			"a Latin letter, which the engine has no category for",
		},
	} {
		got := useClustersOf(string(tc.in))
		if len(got) != len(tc.kinds) {
			t.Errorf("%s:\n  %s\n  became %d clusters, want %d (%v)",
				tc.why, describeCodepoints(tc.in), len(got), len(tc.kinds), got)
			continue
		}
		for i, cl := range got {
			if cl.kind != tc.kinds[i] || cl.start != tc.spans[i][0] || cl.end != tc.spans[i][1] {
				t.Errorf("%s:\n  %s\n  cluster %d is %v over [%d,%d), want %v over [%d,%d)",
					tc.why, describeCodepoints(tc.in), i, cl.kind, cl.start, cl.end,
					tc.kinds[i], tc.spans[i][0], tc.spans[i][1])
			}
		}
	}
}

// TestABrokenUseClusterGetsADottedCircle is the other half: that the kind is
// acted on, and that the placeholder reaches the buffer.
//
// It shapes with the Balinese face the corpus uses, because the question is
// whether a glyph appears — which needs a font that has one.
func TestABrokenUseClusterGetsADottedCircle(t *testing.T) {
	f := harfbuzzFace(t, "fonts/NotoSansBalinese.ttf",
		mustHarfBuzzHeader(t, "balinese.expected.txt"))
	circle, ok := f.GlyphID(dottedCircle)
	if !ok {
		t.Fatal("the face has no U+25CC, so this cannot prove anything")
	}
	count := func(s string) (n int, total int) {
		glyphs, _ := f.ShapeGlyphs(s)
		for _, g := range glyphs {
			if g.GID == circle {
				n++
			}
		}
		return n, len(glyphs)
	}
	for _, tc := range []struct {
		in   string
		want int
		why  string
	}{
		{"\u1B36", 1, "a vowel sign with no letter"},
		{"\u1B44", 1, "a virama with no letter"},
		{"\u1B05\u1B36", 0, "the same vowel on a letter"},
		{"\u1B05", 0, "a letter on its own"},
		{"\u1B36\u1B36", 1, "two vowel signs, which are one broken cluster and get one"},
		{"\u25CC\u1B36", 1, "a dotted circle written by hand is a base, and is not doubled"},
	} {
		if n, total := count(tc.in); n != tc.want {
			t.Errorf("%s: %s shaped to %d glyphs with %d dotted circles, want %d",
				tc.why, describeCodepoints([]rune(tc.in)), total, n, tc.want)
		}
	}
}

// mustHarfBuzzHeader reads just the header of an expectations file, so that a
// test needing the font can check it is the one the corpus was built against.
func mustHarfBuzzHeader(t *testing.T, name string) map[string]string {
	t.Helper()
	_, _, header := readHarfBuzzGolden(t, "balinese.txt", name)
	return header
}
