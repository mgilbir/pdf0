package fonts

import (
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/fonttest"
)

// Contextual substitution, all six forms.
//
// The fixture is the same throughout: glyphs a, b, c, d at indices 1-4, an
// alternate of b at 5 and an alternate of c at 6, and a combining mark at 7.
// Lookup 0 turns b into its alternate and lookup 1 turns c into its; every rule
// below names one of them. That separation is what is under test — the rule
// carries an index, not a substitution — so a test that saw the alternate
// appear saw the indirection work.

const (
	gidA     = 1
	gidB     = 2
	gidC     = 3
	gidD     = 4
	gidBalt  = 5
	gidCalt  = 6
	gidMark  = 7
	advBalt  = 300
	acuteRne = 0x0301
)

// substB and substC are the acting lookups every rule in this file invokes.
func substB() fonttest.Lookup {
	return fonttest.Lookup{Type: 1, Subtables: [][]byte{fonttest.SingleSubst([]int{gidB}, []int{gidBalt})}}
}

func substC() fonttest.Lookup {
	return fonttest.Lookup{Type: 1, Subtables: [][]byte{fonttest.SingleSubst([]int{gidC}, []int{gidCalt})}}
}

// contextFace builds a face carrying the given lookups, with 'calt' naming the
// last of them — the rule under test.
func contextFace(t *testing.T, lookups []fonttest.Lookup, gdef map[int]int) *Face {
	t.Helper()
	extra := map[string][]byte{
		"GSUB": fonttest.GSUBLookups(lookups, map[string][]int{"calt": {len(lookups) - 1}}),
	}
	if gdef != nil {
		extra["GDEF"] = fonttest.GDEF(gdef)
	}
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
		Extra: extra,
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

func shapedGIDs(t *testing.T, f *Face, s string) []int {
	t.Helper()
	glyphs, missing := f.ShapeGlyphs(s)
	if missing != 0 {
		t.Fatalf("shaping %q: %d runes have no glyph", s, missing)
	}
	out := make([]int, len(glyphs))
	for i, g := range glyphs {
		out[i] = g.GID
	}
	return out
}

func wantGIDs(t *testing.T, got, want []int, s string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("shaping %q gave %d glyphs %v, want %d %v", s, len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("shaping %q: position %d is glyph %d, want %d (all: %v)", s, i, got[i], want[i], got)
		}
	}
}

// TestChainedContextFormat3 is the form a modern font reaches for most, and the
// one 'calt' is nearly always written in: three lists of coverage tables saying
// what must precede, what is replaced, and what must follow.
func TestChainedContextFormat3(t *testing.T) {
	rule := fonttest.ChainedContext3(
		[][]int{{gidA}}, // preceded by a
		[][]int{{gidB}}, // replace b
		[][]int{{gidC}}, // followed by c
		[]fonttest.SeqLookup{{At: 0, Lookup: 0}},
	)
	f := contextFace(t, []fonttest.Lookup{substB(), {Type: 6, Subtables: [][]byte{rule}}}, nil)

	wantGIDs(t, shapedGIDs(t, f, "abc"), []int{gidA, gidBalt, gidC}, "abc")
	// The context is the whole of the rule: change either side and nothing fires.
	wantGIDs(t, shapedGIDs(t, f, "abd"), []int{gidA, gidB, gidD}, "abd")
	wantGIDs(t, shapedGIDs(t, f, "dbc"), []int{gidD, gidB, gidC}, "dbc")
	// A match at the start of the run has no backtrack to match against.
	wantGIDs(t, shapedGIDs(t, f, "bc"), []int{gidB, gidC}, "bc")
}

// TestSubstitutedGlyphTakesItsOwnAdvance pins the arithmetic that makes a
// substitution visible rather than merely present: the alternate is narrower
// than what it replaced, and a shaper that swapped the glyph and kept the old
// advance would leave a gap the width of the difference.
func TestSubstitutedGlyphTakesItsOwnAdvance(t *testing.T) {
	rule := fonttest.ChainedContext3(
		[][]int{{gidA}}, [][]int{{gidB}}, [][]int{{gidC}},
		[]fonttest.SeqLookup{{At: 0, Lookup: 0}},
	)
	f := contextFace(t, []fonttest.Lookup{substB(), {Type: 6, Subtables: [][]byte{rule}}}, nil)
	glyphs, _ := f.ShapeGlyphs("abc")
	if glyphs[1].XAdvance != advBalt {
		t.Errorf("the alternate advances %v, want %d", glyphs[1].XAdvance, advBalt)
	}
}

// TestSequenceContextFormat1 matches by glyph and applies two lookups from one
// rule, which is how a font states "in this pair, change both".
func TestSequenceContextFormat1(t *testing.T) {
	rule := fonttest.SequenceContext1(map[int][]fonttest.ContextRule{
		gidB: {{
			Input:   []int{gidB, gidC},
			Lookups: []fonttest.SeqLookup{{At: 0, Lookup: 0}, {At: 1, Lookup: 1}},
		}},
	})
	f := contextFace(t, []fonttest.Lookup{substB(), substC(), {Type: 5, Subtables: [][]byte{rule}}}, nil)

	wantGIDs(t, shapedGIDs(t, f, "bc"), []int{gidBalt, gidCalt}, "bc")
	wantGIDs(t, shapedGIDs(t, f, "bd"), []int{gidB, gidD}, "bd")
}

// TestSequenceContextFormat2 matches by class, which is how a font states one
// rule for a group of glyphs rather than one rule per glyph.
func TestSequenceContextFormat2(t *testing.T) {
	classes := map[int]int{gidB: 1, gidC: 2, gidD: 2}
	rule := fonttest.SequenceContext2(
		[]int{gidB},
		classes,
		map[int][]fonttest.ContextRule{
			1: {{Input: []int{1, 2}, Lookups: []fonttest.SeqLookup{{At: 1, Lookup: 0}}}},
		},
	)
	// Lookup 0 here turns c into its alternate, so a match at position 1 shows.
	f := contextFace(t, []fonttest.Lookup{substC(), {Type: 5, Subtables: [][]byte{rule}}}, nil)

	wantGIDs(t, shapedGIDs(t, f, "bc"), []int{gidB, gidCalt}, "bc")
	// d is in the same class as c, so the rule matches — and finds nothing to do,
	// because the lookup it names does not cover d. That is the class model:
	// the rule selects a position, the lookup decides what happens there.
	wantGIDs(t, shapedGIDs(t, f, "bd"), []int{gidB, gidD}, "bd")
	// a is in no class the rule names.
	wantGIDs(t, shapedGIDs(t, f, "ac"), []int{gidA, gidC}, "ac")
}

// TestSequenceContextFormat3 matches by a coverage table per position, with no
// rule sets at all.
func TestSequenceContextFormat3(t *testing.T) {
	rule := fonttest.SequenceContext3(
		[][]int{{gidB}, {gidC, gidD}},
		[]fonttest.SeqLookup{{At: 0, Lookup: 0}},
	)
	f := contextFace(t, []fonttest.Lookup{substB(), {Type: 5, Subtables: [][]byte{rule}}}, nil)

	wantGIDs(t, shapedGIDs(t, f, "bc"), []int{gidBalt, gidC}, "bc")
	wantGIDs(t, shapedGIDs(t, f, "bd"), []int{gidBalt, gidD}, "bd")
	wantGIDs(t, shapedGIDs(t, f, "ba"), []int{gidB, gidA}, "ba")
}

// TestChainedContextFormat1 is the glyph-based chained form.
func TestChainedContextFormat1(t *testing.T) {
	rule := fonttest.ChainedContext1(map[int][]fonttest.ChainRule{
		gidB: {{
			Backtrack: []int{gidA},
			Input:     []int{gidB},
			Lookahead: []int{gidC},
			Lookups:   []fonttest.SeqLookup{{At: 0, Lookup: 0}},
		}},
	})
	f := contextFace(t, []fonttest.Lookup{substB(), {Type: 6, Subtables: [][]byte{rule}}}, nil)

	wantGIDs(t, shapedGIDs(t, f, "abc"), []int{gidA, gidBalt, gidC}, "abc")
	wantGIDs(t, shapedGIDs(t, f, "dbc"), []int{gidD, gidB, gidC}, "dbc")
	wantGIDs(t, shapedGIDs(t, f, "abd"), []int{gidA, gidB, gidD}, "abd")
}

// TestChainedContextFormat2 is the class-based chained form, whose three class
// definitions let a glyph belong to a different group in each role.
func TestChainedContextFormat2(t *testing.T) {
	rule := fonttest.ChainedContext2(
		[]int{gidB},
		map[int]int{gidA: 1},          // backtrack classes
		map[int]int{gidB: 1},          // input classes
		map[int]int{gidC: 1, gidD: 2}, // lookahead classes
		map[int][]fonttest.ChainRule{
			1: {{
				Backtrack: []int{1},
				Input:     []int{1},
				Lookahead: []int{1},
				Lookups:   []fonttest.SeqLookup{{At: 0, Lookup: 0}},
			}},
		},
	)
	f := contextFace(t, []fonttest.Lookup{substB(), {Type: 6, Subtables: [][]byte{rule}}}, nil)

	wantGIDs(t, shapedGIDs(t, f, "abc"), []int{gidA, gidBalt, gidC}, "abc")
	// d is lookahead class 2, so the rule — which wants class 1 — does not fire.
	wantGIDs(t, shapedGIDs(t, f, "abd"), []int{gidA, gidB, gidD}, "abd")
}

// TestContextSkipsTheGlyphsItsFlagIgnores is the correctness point that lookup
// flags exist for, and the one a reader sees. A contextual rule about a letter
// and the letter after it must still hold when an accent is written between
// them; a font declares that by setting IgnoreMarks, and honouring the rule
// without the flag breaks every accented word.
func TestContextSkipsTheGlyphsItsFlagIgnores(t *testing.T) {
	rule := fonttest.ChainedContext3(
		[][]int{{gidA}}, [][]int{{gidB}}, [][]int{{gidC}},
		[]fonttest.SeqLookup{{At: 0, Lookup: 0}},
	)
	gdef := map[int]int{gidA: classBase, gidB: classBase, gidC: classBase, gidMark: classMark}
	withFlag := contextFace(t, []fonttest.Lookup{
		substB(),
		{Type: 6, Flag: flagIgnoreMarks, Subtables: [][]byte{rule}},
	}, gdef)

	// The mark sits between the b and the c the rule looks ahead to.
	text := "ab́c"
	wantGIDs(t, shapedGIDs(t, withFlag, text), []int{gidA, gidBalt, gidMark, gidC}, text)

	// Without the flag the same font must not fire: the glyph after b is the
	// mark, and the rule asks for c.
	withoutFlag := contextFace(t, []fonttest.Lookup{
		substB(),
		{Type: 6, Subtables: [][]byte{rule}},
	}, gdef)
	wantGIDs(t, shapedGIDs(t, withoutFlag, text), []int{gidA, gidB, gidMark, gidC}, text)
}

// TestBacktrackIsMatchedNearestFirst pins the one ordering in these tables that
// runs against the grain of every other list in the format.
//
// A backtrack sequence is stored from the match outwards: the first entry is the
// glyph immediately before, the second the one before that. Reading it as it
// reads on the page inverts the condition, and a font whose rule fires on "da"
// would instead fire on "ad" — a rule that still matches something, which is why
// this needs more than one entry to catch.
func TestBacktrackIsMatchedNearestFirst(t *testing.T) {
	rule := fonttest.ChainedContext3(
		[][]int{{gidA}, {gidD}}, // immediately before: a; before that: d
		[][]int{{gidB}},
		[][]int{{gidC}, {gidD}}, // immediately after: c; after that: d
		[]fonttest.SeqLookup{{At: 0, Lookup: 0}},
	)
	f := contextFace(t, []fonttest.Lookup{substB(), {Type: 6, Subtables: [][]byte{rule}}}, nil)

	wantGIDs(t, shapedGIDs(t, f, "dabcd"), []int{gidD, gidA, gidBalt, gidC, gidD}, "dabcd")
	// Both sequences reversed: the same glyphs, in the order a rule read
	// outwards-in would want.
	wantGIDs(t, shapedGIDs(t, f, "adbdc"), []int{gidA, gidD, gidB, gidD, gidC}, "adbdc")
}

// TestRecordPositionCountsMatchesNotGlyphs pins what a rule's position index
// means. A record saying "apply lookup 0 at position 1" means the second thing
// the rule *matched*, which is not the second glyph in the buffer once the
// lookup has skipped a mark. Taking it as a buffer offset applies the lookup to
// the mark, and the substitution silently does not happen.
func TestRecordPositionCountsMatchesNotGlyphs(t *testing.T) {
	rule := fonttest.SequenceContext3(
		[][]int{{gidB}, {gidC}},
		[]fonttest.SeqLookup{{At: 1, Lookup: 0}},
	)
	gdef := map[int]int{gidB: classBase, gidC: classBase, gidMark: classMark}
	f := contextFace(t, []fonttest.Lookup{
		substC(),
		{Type: 5, Flag: flagIgnoreMarks, Subtables: [][]byte{rule}},
	}, gdef)

	// The mark sits between the two matched glyphs, so the match is at buffer
	// positions 0 and 2 while the rule calls them 0 and 1.
	text := "b́c"
	wantGIDs(t, shapedGIDs(t, f, text), []int{gidB, gidMark, gidCalt}, text)
}

// TestContextualRuleMayInvokeALigature pins that the lookup a rule names is not
// restricted to a single substitution: invoking a ligature from a context is how
// a font states "these letters join, but only here".
func TestContextualRuleMayInvokeALigature(t *testing.T) {
	lig := fonttest.LigatureSubst([]fonttest.Ligature{
		{Components: []int{gidB, gidC}, Glyph: gidCalt},
	})
	rule := fonttest.ChainedContext3(
		[][]int{{gidA}}, [][]int{{gidB}}, [][]int{{gidC}},
		[]fonttest.SeqLookup{{At: 0, Lookup: 0}},
	)
	f := contextFace(t, []fonttest.Lookup{
		{Type: 4, Subtables: [][]byte{lig}},
		{Type: 6, Subtables: [][]byte{rule}},
	}, nil)

	// b and c become one glyph, but only after an a.
	wantGIDs(t, shapedGIDs(t, f, "abc"), []int{gidA, gidCalt}, "abc")
	wantGIDs(t, shapedGIDs(t, f, "dbc"), []int{gidD, gidB, gidC}, "dbc")
}

// TestMultipleSubstitutionDecomposesAGlyph is what 'ccmp' is usually written
// with: one glyph becomes several, so that the rules after it have the pieces
// they are written against.
func TestMultipleSubstitutionDecomposesAGlyph(t *testing.T) {
	f := contextFace(t, []fonttest.Lookup{{
		Type:      2,
		Subtables: [][]byte{fonttest.MultipleSubst([]int{gidB}, [][]int{{gidBalt, gidCalt}})},
	}}, nil)

	wantGIDs(t, shapedGIDs(t, f, "abc"), []int{gidA, gidBalt, gidCalt, gidC}, "abc")

	// Both pieces stand for the one character, so both carry its cluster —
	// which is what maps a click on either back to the same place in the text.
	//
	// The text begins with a two-byte character deliberately. A cluster is a
	// byte offset, and in ASCII that is also the glyph's index in the buffer, so
	// a fixture without one cannot tell the two apart — and confusing them is
	// the mistake worth catching, since it makes every cluster in a non-ASCII
	// run point at the wrong character.
	const text = "́bc" // U+0301 occupies bytes 0-1, so 'b' begins at byte 2
	glyphs, _ := f.ShapeGlyphs(text)
	if len(glyphs) != 4 {
		t.Fatalf("got %d glyphs, want 4", len(glyphs))
	}
	for i := 1; i <= 2; i++ {
		if glyphs[i].Cluster != 2 {
			t.Errorf("piece %d has cluster %d, want 2 — the byte offset of the character it came from",
				i-1, glyphs[i].Cluster)
		}
	}
	if glyphs[3].Cluster != 3 {
		t.Errorf("the glyph after the decomposition has cluster %d, want 3", glyphs[3].Cluster)
	}
	// Each piece advances by its own width, not by what it replaced.
	if glyphs[1].XAdvance != advBalt {
		t.Errorf("the first piece advances %v, want %d", glyphs[1].XAdvance, advBalt)
	}
}

// TestAlternateSubstitutionTakesTheFontsFirstChoice pins the default. A lookup
// reached through a feature that is simply on has no one to ask which alternate
// is wanted, and the font lists its own preference first.
func TestAlternateSubstitutionTakesTheFontsFirstChoice(t *testing.T) {
	f := contextFace(t, []fonttest.Lookup{{
		Type:      3,
		Subtables: [][]byte{fonttest.AlternateSubst([]int{gidB}, [][]int{{gidBalt, gidCalt}})},
	}}, nil)
	wantGIDs(t, shapedGIDs(t, f, "b"), []int{gidBalt}, "b")
}

// TestPositionsFollowAGrowingBuffer is the bookkeeping a rule with more than one
// lookup depends on.
//
// The rule matches three glyphs and names two lookups. The first decomposes the
// glyph at position 0, which pushes everything after it along by one; the second
// is aimed at position 2, and would land on the wrong glyph if the positions
// were the ones remembered when the rule matched.
func TestPositionsFollowAGrowingBuffer(t *testing.T) {
	rule := fonttest.SequenceContext1(map[int][]fonttest.ContextRule{
		gidB: {{
			Input:   []int{gidB, gidC, gidD},
			Lookups: []fonttest.SeqLookup{{At: 0, Lookup: 0}, {At: 2, Lookup: 1}},
		}},
	})
	f := contextFace(t, []fonttest.Lookup{
		{Type: 2, Subtables: [][]byte{fonttest.MultipleSubst([]int{gidB}, [][]int{{gidBalt, gidB}})}},
		{Type: 1, Subtables: [][]byte{fonttest.SingleSubst([]int{gidD}, []int{gidA})}},
		{Type: 5, Subtables: [][]byte{rule}},
	}, nil)

	// b becomes two glyphs, and the d — now at position 3, not 2 — becomes a.
	wantGIDs(t, shapedGIDs(t, f, "bcd"), []int{gidBalt, gidB, gidC, gidA}, "bcd")
}

// TestPositionsFollowAShrinkingBuffer is the same in the other direction, and
// the more dangerous one: a stale index here points past the end of the buffer.
func TestPositionsFollowAShrinkingBuffer(t *testing.T) {
	rule := fonttest.SequenceContext1(map[int][]fonttest.ContextRule{
		gidB: {{
			Input:   []int{gidB, gidC, gidD},
			Lookups: []fonttest.SeqLookup{{At: 0, Lookup: 0}, {At: 2, Lookup: 1}},
		}},
	})
	f := contextFace(t, []fonttest.Lookup{
		{Type: 4, Subtables: [][]byte{fonttest.LigatureSubst([]fonttest.Ligature{
			{Components: []int{gidB, gidC}, Glyph: gidBalt},
		})}},
		{Type: 1, Subtables: [][]byte{fonttest.SingleSubst([]int{gidD}, []int{gidA})}},
		{Type: 5, Subtables: [][]byte{rule}},
	}, nil)

	// b and c ligate, so the d moves from position 2 to position 1.
	wantGIDs(t, shapedGIDs(t, f, "bcd"), []int{gidBalt, gidA}, "bcd")
}

// TestDecompositionLengthIsBounded pins that one glyph cannot be made to become
// an unbounded number of them. A font is untrusted input, and a sequence long
// enough to matter is malformed — a real decomposition is two or three glyphs.
func TestDecompositionLengthIsBounded(t *testing.T) {
	huge := make([]int, maxSubstitutionLength+1)
	for i := range huge {
		huge[i] = gidBalt
	}
	f := contextFace(t, []fonttest.Lookup{{
		Type:      2,
		Subtables: [][]byte{fonttest.MultipleSubst([]int{gidB}, [][]int{huge})},
	}}, nil)
	wantGIDs(t, shapedGIDs(t, f, "b"), []int{gidB}, "b")

	// One glyph under the bound is applied, so the bound is a bound and not a
	// refusal to decompose at all.
	ok := make([]int, maxSubstitutionLength)
	for i := range ok {
		ok[i] = gidBalt
	}
	g := contextFace(t, []fonttest.Lookup{{
		Type:      2,
		Subtables: [][]byte{fonttest.MultipleSubst([]int{gidB}, [][]int{ok})},
	}}, nil)
	if got := shapedGIDs(t, g, "b"); len(got) != maxSubstitutionLength {
		t.Errorf("a sequence at the bound produced %d glyphs, want %d", len(got), maxSubstitutionLength)
	}
}

// TestContextualRecursionIsBounded is the safety property. A font may describe a
// lookup that invokes itself, and nothing in the format forbids it; the depth
// bound is what turns that from a hang into a rule that stops applying.
func TestContextualRecursionIsBounded(t *testing.T) {
	// Lookup 0 is a context whose rule invokes lookup 0.
	selfRef := fonttest.SequenceContext1(map[int][]fonttest.ContextRule{
		gidB: {{Input: []int{gidB}, Lookups: []fonttest.SeqLookup{{At: 0, Lookup: 0}}}},
	})
	f := contextFace(t, []fonttest.Lookup{{Type: 5, Subtables: [][]byte{selfRef}}}, nil)

	done := make(chan []int, 1)
	go func() {
		glyphs, _ := f.ShapeGlyphs("bbb")
		out := make([]int, len(glyphs))
		for i, g := range glyphs {
			out[i] = g.GID
		}
		done <- out
	}()
	select {
	case got := <-done:
		wantGIDs(t, got, []int{gidB, gidB, gidB}, "bbb")
	case <-time.After(10 * time.Second):
		t.Fatal("shaping a self-referential lookup did not finish")
	}
}

// TestContextualRuleNamingAMissingLookupIsIgnored pins that a malformed font —
// one whose rule points past the end of the lookup list — shapes plainly rather
// than reaching outside it.
func TestContextualRuleNamingAMissingLookupIsIgnored(t *testing.T) {
	rule := fonttest.SequenceContext1(map[int][]fonttest.ContextRule{
		gidB: {{Input: []int{gidB}, Lookups: []fonttest.SeqLookup{{At: 0, Lookup: 99}}}},
	})
	f := contextFace(t, []fonttest.Lookup{substB(), {Type: 5, Subtables: [][]byte{rule}}}, nil)
	wantGIDs(t, shapedGIDs(t, f, "b"), []int{gidB}, "b")
}

// TestContextualRuleWithAnOutOfRangePositionIsIgnored is the other half: a
// record naming a matched position the rule does not have.
func TestContextualRuleWithAnOutOfRangePositionIsIgnored(t *testing.T) {
	rule := fonttest.SequenceContext1(map[int][]fonttest.ContextRule{
		gidB: {{Input: []int{gidB}, Lookups: []fonttest.SeqLookup{{At: 7, Lookup: 0}}}},
	})
	f := contextFace(t, []fonttest.Lookup{substB(), {Type: 5, Subtables: [][]byte{rule}}}, nil)
	wantGIDs(t, shapedGIDs(t, f, "b"), []int{gidB}, "b")
}

// TestEveryExtensionSubtableIsUnwrapped pins a lookup with more than one
// extension subtable.
//
// Unwrapping is what replaces the lookup's type with the real one, so deciding
// whether to unwrap by reading that type back inside the loop unwraps the first
// subtable and then treats the rest as though they were already the real thing.
// The bytes it reads then are the extension header itself, which parses as a
// coverage table pointing nowhere: no crash, no substitution, and a font whose
// later subtables silently do nothing.
func TestEveryExtensionSubtableIsUnwrapped(t *testing.T) {
	f := contextFace(t, []fonttest.Lookup{{
		Type: 7, // extension substitution
		Subtables: [][]byte{
			fonttest.ExtensionSubst(1, fonttest.SingleSubst([]int{gidB}, []int{gidBalt})),
			fonttest.ExtensionSubst(1, fonttest.SingleSubst([]int{gidC}, []int{gidCalt})),
		},
	}}, nil)
	wantGIDs(t, shapedGIDs(t, f, "bc"), []int{gidBalt, gidCalt}, "bc")
}

// TestUndeclaredFeatureDoesNothing pins that the machinery is driven by what the
// font declares. The same rule under a tag no shaper turns on by default must
// leave the text alone.
func TestUndeclaredFeatureDoesNothing(t *testing.T) {
	rule := fonttest.ChainedContext3(
		[][]int{{gidA}}, [][]int{{gidB}}, [][]int{{gidC}},
		[]fonttest.SeqLookup{{At: 0, Lookup: 0}},
	)
	lookups := []fonttest.Lookup{substB(), {Type: 6, Subtables: [][]byte{rule}}}
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "Context",
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: 500, HasShape: true},
			{Rune: 'b', Advance: 500, HasShape: true},
			{Rune: 'c', Advance: 500, HasShape: true},
		},
		Extra: map[string][]byte{
			// 'salt' is stylistic alternates: on only when asked for.
			"GSUB": fonttest.GSUBLookups(lookups, map[string][]int{"salt": {1}}),
		},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	wantGIDs(t, shapedGIDs(t, f, "abc"), []int{gidA, gidB, gidC}, "abc")
}
