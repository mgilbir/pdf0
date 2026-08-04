package fonts

import (
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/fonttest"
)

// Contextual positioning: GPOS types 7 and 8.
//
// A rule says "where this sequence occurs, apply positioning lookup number six
// at position two". Until now this package read no such rule, so a font stating
// its spacing that way was set with the spacing left out — and it is not a rare
// way to state it: Noto Sans Khmer has sixty-six contextual positioning
// subtables and fifteen lookups reachable only through one.
//
// # How this is verified
//
// The fixtures below, and one cross-check outside them. No font in
// testdata/harfbuzz exercises this: removing every contextual positioning
// subtable from all three leaves all 9540 corpus answers unchanged, so the
// corpus cannot tell a working implementation from a missing one.
//
// So it was checked against HarfBuzz directly, on a copy of Noto Sans Khmer
// edited to make one of its rules match: with a rule applying a +5000 placement,
// HarfBuzz and this package both move the glyph by 5000, and without the rule
// neither does. That is what says the subtable is read the way HarfBuzz reads
// it. The fixtures here then pin the behaviour that a checked-in test can pin:
// that the context is required, and that each of the three formats matches.

const (
	cpA, cpB, cpC, cpD = 1, 2, 3, 4
	cpAdvance          = 500
	cpNudge            = 120
)

// contextPosFace builds a face whose 'kern' feature is the given positioning
// lookups. Lookup 0 is always the nudge a rule reaches for: it shifts glyph B
// and is named by no feature, so nothing applies it except a rule.
func contextPosFace(t *testing.T, lookups []fonttest.Lookup, named []int) *Face {
	t.Helper()
	all := append([]fonttest.Lookup{
		{Type: 1, Subtables: [][]byte{fonttest.SinglePosSubtable(cpB, cpNudge, 0, 0)}},
	}, lookups...)
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "ContextPos",
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: cpAdvance, HasShape: true},
			{Rune: 'b', Advance: cpAdvance, HasShape: true},
			{Rune: 'c', Advance: cpAdvance, HasShape: true},
			{Rune: 'd', Advance: cpAdvance, HasShape: true},
		},
		Extra: map[string][]byte{
			"GPOS": fonttest.GPOSLookups(all, map[string][]int{"kern": named}),
		},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

// offsetsOf shapes a string and returns each glyph's horizontal offset, which
// is what the rules below move.
func offsetsOf(t *testing.T, f *Face, s string) []float64 {
	t.Helper()
	glyphs, missing := f.ShapeGlyphs(s)
	if missing != 0 {
		t.Fatalf("shaping %q: %d characters have no glyph", s, missing)
	}
	out := make([]float64, len(glyphs))
	for i, g := range glyphs {
		out[i] = g.XOffset
	}
	return out
}

func sameOffsets(a, b []float64) bool {
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

// TestAPositioningLookupNamedByARuleIsApplied is the defect stated as a test,
// in each of the three chained formats.
//
// Each rule says: where "abc" occurs, nudge the b. The nudge lookup is named by
// no feature, so nothing but the rule can reach it — which is the case a font
// uses this for and the case that did nothing at all before.
func TestAPositioningLookupNamedByARuleIsApplied(t *testing.T) {
	nudge := []fonttest.SeqLookup{{At: 0, Lookup: 0}}
	for _, tc := range []struct {
		sub  []byte
		why  string
		kind int
	}{
		{
			fonttest.ChainedContext3([][]int{{cpA}}, [][]int{{cpB}}, [][]int{{cpC}}, nudge),
			"format 3: a coverage for each position", 8,
		},
		{
			fonttest.ChainedContext1(map[int][]fonttest.ChainRule{
				cpB: {{Backtrack: []int{cpA}, Input: []int{cpB}, Lookahead: []int{cpC}, Lookups: nudge}},
			}),
			"format 1: rules by glyph", 8,
		},
		{
			fonttest.ChainedContext2(
				[]int{cpB},
				map[int]int{cpA: 1}, map[int]int{cpB: 1}, map[int]int{cpC: 1},
				map[int][]fonttest.ChainRule{
					1: {{Backtrack: []int{1}, Input: []int{1}, Lookahead: []int{1}, Lookups: nudge}},
				}),
			"format 2: rules by class", 8,
		},
	} {
		f := contextPosFace(t, []fonttest.Lookup{
			{Type: tc.kind, Subtables: [][]byte{tc.sub}},
		}, []int{1})

		// In context: the b moves.
		if got, want := offsetsOf(t, f, "abc"), []float64{0, cpNudge, 0}; !sameOffsets(got, want) {
			t.Errorf("%s: \"abc\" gave offsets %v, want %v — the rule names a lookup "+
				"no feature does, so nothing else could have applied it", tc.why, got, want)
		}
		// Out of context: it does not.
		for _, s := range []string{"abd", "dbc", "bc", "ab", "b"} {
			got := offsetsOf(t, f, s)
			zero := make([]float64, len(got))
			if !sameOffsets(got, zero) {
				t.Errorf("%s: %q gave offsets %v, want all zero — the context does not match",
					tc.why, s, got)
			}
		}
	}
}

// TestAnUnchainedPositioningRuleIsApplied covers type 7, which states a
// sequence with no before and after.
func TestAnUnchainedPositioningRuleIsApplied(t *testing.T) {
	f := contextPosFace(t, []fonttest.Lookup{{
		Type: 7,
		Subtables: [][]byte{fonttest.SequenceContext3(
			[][]int{{cpA}, {cpB}},
			[]fonttest.SeqLookup{{At: 1, Lookup: 0}},
		)},
	}}, []int{1})

	if got, want := offsetsOf(t, f, "ab"), []float64{0, cpNudge}; !sameOffsets(got, want) {
		t.Errorf("\"ab\" gave offsets %v, want %v", got, want)
	}
	if got := offsetsOf(t, f, "db"); !sameOffsets(got, []float64{0, 0}) {
		t.Errorf("\"db\" gave offsets %v, want all zero", got)
	}
}

// TestAPositioningRuleCanNameAPairLookup pins that what a rule reaches is a
// lookup of any positioning kind, not only the single adjustment that is
// easiest to test with.
func TestAPositioningRuleCanNameAPairLookup(t *testing.T) {
	const tighten = -70
	all := []fonttest.Lookup{
		// Lookup 0: the nudge, unused here but kept so the indices below match
		// contextPosFace's layout.
		{Type: 1, Subtables: [][]byte{fonttest.SinglePosSubtable(cpB, cpNudge, 0, 0)}},
		// Lookup 1: a pair adjustment, named by no feature.
		{Type: 2, Subtables: [][]byte{fonttest.PairPosSubtable(
			[]fonttest.KernPair{{Left: cpB, Right: cpC, Adjust: tighten}})}},
		// Lookup 2: the rule that reaches it.
		{Type: 8, Subtables: [][]byte{fonttest.ChainedContext3(
			[][]int{{cpA}}, [][]int{{cpB}, {cpC}}, nil,
			[]fonttest.SeqLookup{{At: 0, Lookup: 1}},
		)}},
	}
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "ContextPair",
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: cpAdvance, HasShape: true},
			{Rune: 'b', Advance: cpAdvance, HasShape: true},
			{Rune: 'c', Advance: cpAdvance, HasShape: true},
			{Rune: 'd', Advance: cpAdvance, HasShape: true},
		},
		Extra: map[string][]byte{
			"GPOS": fonttest.GPOSLookups(all, map[string][]int{"kern": {2}}),
		},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}

	glyphs, _ := f.ShapeGlyphs("abc")
	if len(glyphs) != 3 {
		t.Fatalf("shaped to %d glyphs, want 3", len(glyphs))
	}
	if want := float64(cpAdvance + tighten); glyphs[1].XAdvance != want {
		t.Errorf("the b advances %v, want %v — the rule names a pair lookup and "+
			"no feature does", glyphs[1].XAdvance, want)
	}
	// Without the a, the rule does not match and the pair is not adjusted.
	glyphs, _ = f.ShapeGlyphs("dbc")
	if glyphs[1].XAdvance != cpAdvance {
		t.Errorf("out of context the b advances %v, want %v", glyphs[1].XAdvance, float64(cpAdvance))
	}
}

// TestAPositioningRuleCannotRecurseForever pins the bound. A font can describe
// a cycle — a rule naming a lookup that names the rule — and nothing in the
// format forbids it, so the depth is what stops it. A shaper that read one and
// did not stop would hang on a document it was handed.
func TestAPositioningRuleCannotRecurseForever(t *testing.T) {
	all := []fonttest.Lookup{
		{Type: 1, Subtables: [][]byte{fonttest.SinglePosSubtable(cpB, cpNudge, 0, 0)}},
		// Lookup 1 names lookup 2, and lookup 2 names lookup 1.
		{Type: 8, Subtables: [][]byte{fonttest.ChainedContext3(
			nil, [][]int{{cpB}}, nil, []fonttest.SeqLookup{{At: 0, Lookup: 2}})}},
		{Type: 8, Subtables: [][]byte{fonttest.ChainedContext3(
			nil, [][]int{{cpB}}, nil, []fonttest.SeqLookup{{At: 0, Lookup: 1}})}},
	}
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "ContextCycle",
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: cpAdvance, HasShape: true},
			{Rune: 'b', Advance: cpAdvance, HasShape: true},
			{Rune: 'c', Advance: cpAdvance, HasShape: true},
			{Rune: 'd', Advance: cpAdvance, HasShape: true},
		},
		Extra: map[string][]byte{
			"GPOS": fonttest.GPOSLookups(all, map[string][]int{"kern": {1}}),
		},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	done := make(chan []float64, 1)
	go func() { done <- offsetsOf(t, f, "abc") }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("shaping did not finish: a cycle of positioning rules was followed to the end")
	}
}

// What a rule may name, and what happens when it names something twice.
//
// Both of these were guesses in the source until they were measured against
// HarfBuzz on a font built for the purpose, and both guesses were wrong. They
// are asserted here because a caveat nobody has tested is worth less than no
// caveat: it stops the next reader from checking.

// TestAPositioningRuleCanNameAMarkAttachment is the defect the first guess hid.
//
// The source said reaching a mark attachment from a rule "would place a mark
// twice", so types 3 to 6 were skipped. HarfBuzz places it once and correctly;
// this package placed it not at all, leaving the mark on the baseline at the
// pen. A lookup a rule names is usually named by no feature, so a rule is the
// only way to its anchors.
//
// The values are the font's own: the base anchors at x=400 y=700, the mark at
// x=100 y=200, and the base is 500 wide — so the mark sits at (400-100)-500 =
// -200 and 700-200 = 500. HarfBuzz agrees.
func TestAPositioningRuleCanNameAMarkAttachment(t *testing.T) {
	const (
		mkBase, mkMark = 1, 3
		advance        = 500
	)
	markSub := fonttest.MarkAttachSubtable(
		[]fonttest.MarkAttachment{{Glyph: mkMark, Class: 0, Anchor: fonttest.Anchor{X: 100, Y: 200}}},
		[]fonttest.BaseAttachment{{Glyph: mkBase, Anchors: map[int]fonttest.Anchor{0: {X: 400, Y: 700}}}},
	)
	f := markRuleFace(t, []fonttest.Lookup{
		// Named by no feature: only the rule below can reach it.
		{Type: 4, Subtables: [][]byte{markSub}},
		{Type: 8, Subtables: [][]byte{fonttest.ChainedContext3(
			[][]int{{mkBase}}, [][]int{{mkMark}}, nil,
			[]fonttest.SeqLookup{{At: 0, Lookup: 0}})}},
	}, []int{1})

	glyphs, _ := f.ShapeGlyphs("á")
	if len(glyphs) != 2 {
		t.Fatalf("shaped to %d glyphs, want 2", len(glyphs))
	}
	if glyphs[1].XOffset != -200 || glyphs[1].YOffset != 500 {
		t.Errorf("the mark is at (%v, %v), want (-200, 500) — a rule is the only way "+
			"to a lookup no feature names", glyphs[1].XOffset, glyphs[1].YOffset)
	}
}

// TestALookupNamedTwiceIsAppliedTwice is the second guess, which was that a
// lookup both named by a feature and reached from a rule would be applied twice
// and that this would be wrong.
//
// It is applied twice, and that is what HarfBuzz does: measured on this very
// font, both move the glyph by 200 where one application moves it by 100. The
// format says a feature's lookups run over the text and a rule's run where it
// matches, and nothing says the two may not be the same lookup.
//
// It is harmless for the attachments because those *set* an offset rather than
// adding to one, which is why the test above and this one can both be true.
func TestALookupNamedTwiceIsAppliedTwice(t *testing.T) {
	const (
		mkA, mkB = 1, 2
		nudge    = 100
	)
	f := markRuleFace(t, []fonttest.Lookup{
		{Type: 1, Subtables: [][]byte{fonttest.SinglePosSubtable(mkB, nudge, 0, 0)}},
		{Type: 8, Subtables: [][]byte{fonttest.ChainedContext3(
			[][]int{{mkA}}, [][]int{{mkB}}, nil,
			[]fonttest.SeqLookup{{At: 0, Lookup: 0}})}},
	}, []int{0, 1}) // the feature names the nudge *and* the rule

	glyphs, _ := f.ShapeGlyphs("ab")
	if len(glyphs) != 2 {
		t.Fatalf("shaped to %d glyphs, want 2", len(glyphs))
	}
	if glyphs[1].XOffset != 2*nudge {
		t.Errorf("the b moved by %v; the feature applies the nudge and the rule "+
			"applies it again, so HarfBuzz and this both move it by %v",
			glyphs[1].XOffset, float64(2*nudge))
	}
}

// markRuleFace builds a face with the given positioning lookups, the given ones
// named by 'kern', and a GDEF that calls glyph 3 a mark.
func markRuleFace(t *testing.T, lookups []fonttest.Lookup, named []int) *Face {
	t.Helper()
	f, err := Load(fonttest.SFNT(fonttest.SFNTOptions{
		Name: "MarkRule",
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: 500, HasShape: true},
			{Rune: 'b', Advance: 500, HasShape: true},
			{Rune: 0x0301, Advance: 0, HasShape: true},
		},
		Extra: map[string][]byte{
			"GPOS": fonttest.GPOSLookups(lookups, map[string][]int{"kern": named}),
			"GDEF": fonttest.GDEF(map[int]int{1: classBase, 2: classBase, 3: classMark}),
		},
	}))
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}
