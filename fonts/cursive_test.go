package fonts

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/fonttest"
)

// Cursive attachment: making the connecting strokes of joined letters meet.
//
// The fixture is three glyphs at indices 1-3, each 500 units wide in a
// 1000-unit em — so a font unit is a shaped unit and every number below can be
// read directly off the anchors. The anchors are deliberately at different
// heights, because a joint whose two ends are level tests only half of this:
// the horizontal pull-together happens either way, and the vertical lift is
// where the direction flag decides which glyph moves.
const (
	curA, curB, curC = 1, 2, 3

	aExitX, aExitY = 400, 100
	bEntryX        = 50
	bEntryY        = 40
	bExitX, bExitY = 450, 200
	cEntryX        = 60
	cEntryY        = 30

	curAdvance = 500
)

func cursiveFace(t *testing.T, flag int, anchors []fonttest.CursiveAnchor) *Face {
	t.Helper()
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "Cursive",
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: curAdvance, HasShape: true},
			{Rune: 'b', Advance: curAdvance, HasShape: true},
			{Rune: 'c', Advance: curAdvance, HasShape: true},
		},
		Extra: map[string][]byte{"GPOS": fonttest.GPOSCursive(anchors, flag)},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

// joinedAnchors is a chain: a leaves, b arrives and leaves, c arrives.
func joinedAnchors() []fonttest.CursiveAnchor {
	return []fonttest.CursiveAnchor{
		{Glyph: curA, HasExit: true, Exit: fonttest.Anchor{X: aExitX, Y: aExitY}},
		{
			Glyph:    curB,
			HasEntry: true, Entry: fonttest.Anchor{X: bEntryX, Y: bEntryY},
			HasExit: true, Exit: fonttest.Anchor{X: bExitX, Y: bExitY},
		},
		{Glyph: curC, HasEntry: true, Entry: fonttest.Anchor{X: cEntryX, Y: cEntryY}},
	}
}

// TestCursiveJoinsExitToEntry is the horizontal half: each letter advances
// exactly to its exit point, and the next is pulled back so its entry lands
// there. The word ends up narrower than the sum of its letters, which is what
// makes joined text look joined rather than spaced.
func TestCursiveJoinsExitToEntry(t *testing.T) {
	f := cursiveFace(t, 0, joinedAnchors())
	glyphs, missing := f.ShapeGlyphs("abc")
	if missing != 0 {
		t.Fatalf("%d runes have no glyph", missing)
	}

	// a stops at its exit point rather than at its nominal advance.
	if glyphs[0].XAdvance != aExitX {
		t.Errorf("a advances %v, want %d — it should stop at its exit point", glyphs[0].XAdvance, aExitX)
	}
	// b is pulled back by its entry point, and then itself cut to its exit.
	if glyphs[1].XOffset != -bEntryX {
		t.Errorf("b is displaced %v, want %d", glyphs[1].XOffset, -bEntryX)
	}
	if want := float64(bExitX - bEntryX); glyphs[1].XAdvance != want {
		t.Errorf("b advances %v, want %v", glyphs[1].XAdvance, want)
	}
	if glyphs[2].XOffset != -cEntryX {
		t.Errorf("c is displaced %v, want %d", glyphs[2].XOffset, -cEntryX)
	}
	if want := float64(curAdvance - cEntryX); glyphs[2].XAdvance != want {
		t.Errorf("c advances %v, want %v", glyphs[2].XAdvance, want)
	}

	// The whole word is tighter than three unjoined letters.
	joined := MeasureGlyphs(glyphs, 1000)
	if loose := float64(3 * curAdvance); joined >= loose {
		t.Errorf("the joined word measures %v, no narrower than %v unjoined", joined, loose)
	}
}

// TestCursiveLiftsEachGlyphOntoTheStrokeItJoins is the vertical half. A joining
// stroke leaves one letter at a different height from where it enters the next,
// so a glyph has to be lifted to meet it — and the lift accumulates along the
// run, because each letter is measured against the one it joins rather than
// against the baseline.
func TestCursiveLiftsEachGlyphOntoTheStrokeItJoins(t *testing.T) {
	f := cursiveFace(t, 0, joinedAnchors())
	glyphs, _ := f.ShapeGlyphs("abc")

	// With no RightToLeft flag the first glyph is the anchored one.
	if glyphs[0].YOffset != 0 {
		t.Errorf("a sits at %v, want 0: it is the anchored end", glyphs[0].YOffset)
	}
	if want := float64(aExitY - bEntryY); glyphs[1].YOffset != want {
		t.Errorf("b sits at %v, want %v", glyphs[1].YOffset, want)
	}
	if want := float64((aExitY - bEntryY) + (bExitY - cEntryY)); glyphs[2].YOffset != want {
		t.Errorf("c sits at %v, want %v — the lifts accumulate along the run", glyphs[2].YOffset, want)
	}
}

// TestCursiveRightToLeftAnchorsTheLastGlyph pins the one thing the RightToLeft
// lookup flag means.
//
// Every joint is the same either way; what changes is which end of the chain
// stays on the baseline. An Arabic font sets the flag, and a shaper that ignores
// it produces a word whose letters join correctly to each other and which sits
// as a whole off the line — a fault that is invisible in a single word and
// obvious in a paragraph.
func TestCursiveRightToLeftAnchorsTheLastGlyph(t *testing.T) {
	f := cursiveFace(t, flagRightToLeft, joinedAnchors())
	glyphs, _ := f.ShapeGlyphs("abc")

	if glyphs[2].YOffset != 0 {
		t.Errorf("c sits at %v, want 0: with the flag set it is the anchored end", glyphs[2].YOffset)
	}
	if want := float64(-(bExitY - cEntryY)); glyphs[1].YOffset != want {
		t.Errorf("b sits at %v, want %v", glyphs[1].YOffset, want)
	}
	if want := float64(-((bExitY - cEntryY) + (aExitY - bEntryY))); glyphs[0].YOffset != want {
		t.Errorf("a sits at %v, want %v", glyphs[0].YOffset, want)
	}

	// The horizontal join is unaffected by the flag.
	if glyphs[0].XAdvance != aExitX {
		t.Errorf("a advances %v, want %d: the flag is vertical only", glyphs[0].XAdvance, aExitX)
	}
}

// TestCursiveNeedsBothEndsOfAJoint pins that a joint requires an exit on the
// left and an entry on the right. A letter that ends a word has no exit, and
// nothing must be pulled onto it.
func TestCursiveNeedsBothEndsOfAJoint(t *testing.T) {
	// b keeps its entry but loses its exit, so a-b joins and b-c does not.
	anchors := []fonttest.CursiveAnchor{
		{Glyph: curA, HasExit: true, Exit: fonttest.Anchor{X: aExitX, Y: aExitY}},
		{Glyph: curB, HasEntry: true, Entry: fonttest.Anchor{X: bEntryX, Y: bEntryY}},
		{Glyph: curC, HasEntry: true, Entry: fonttest.Anchor{X: cEntryX, Y: cEntryY}},
	}
	f := cursiveFace(t, 0, anchors)
	glyphs, _ := f.ShapeGlyphs("abc")

	if glyphs[0].XAdvance != aExitX {
		t.Errorf("a advances %v, want %d", glyphs[0].XAdvance, aExitX)
	}
	// b was pulled back onto a, so the pen must travel that much less to reach
	// b's right edge — but b is not cut to an exit point, because it has none.
	if want := float64(curAdvance - bEntryX); glyphs[1].XAdvance != want {
		t.Errorf("b advances %v, want %v: pulled back, but not cut to an exit", glyphs[1].XAdvance, want)
	}
	if glyphs[2].XOffset != 0 || glyphs[2].YOffset != 0 {
		t.Errorf("c was moved to (%v,%v); nothing joins into it", glyphs[2].XOffset, glyphs[2].YOffset)
	}
	if want := float64(curAdvance); glyphs[2].XAdvance != want {
		t.Errorf("c advances %v, want %v", glyphs[2].XAdvance, want)
	}
}

// TestCursiveCarriesTheMarksOnALiftedBase is where the two positioning passes
// meet.
//
// A mark is placed at an offset from the letter it belongs to. Cursive
// attachment then lifts that letter onto the joining stroke — and the mark has
// to go with it. Placing the mark relative to the baseline instead leaves every
// accent in a joined word hanging at the height its letter *would* have been at,
// which is exactly wrong by the amount of the lift and grows along the word.
func TestCursiveCarriesTheMarksOnALiftedBase(t *testing.T) {
	const (
		markGID           = 3
		baseAnchorX       = 200
		baseAnchorY       = 500
		markAcuteRune     = 0x0301
		expectedBaseLift  = aExitY - bEntryY // b is lifted onto a's exit stroke
		expectedMarkShift = baseAnchorX - bEntryX - (curAdvance - bEntryX)
	)
	gpos := fonttest.GPOSLookups([]fonttest.Lookup{
		{Type: 3, Subtables: [][]byte{fonttest.CursivePosSubtable([]fonttest.CursiveAnchor{
			{Glyph: curA, HasExit: true, Exit: fonttest.Anchor{X: aExitX, Y: aExitY}},
			{Glyph: curB, HasEntry: true, Entry: fonttest.Anchor{X: bEntryX, Y: bEntryY}},
		})}},
		{Type: 4, Subtables: [][]byte{fonttest.MarkAttachSubtable(
			[]fonttest.MarkAttachment{{Glyph: markGID, Class: 0, Anchor: fonttest.Anchor{}}},
			[]fonttest.BaseAttachment{{Glyph: curB, Anchors: map[int]fonttest.Anchor{
				0: {X: baseAnchorX, Y: baseAnchorY},
			}}},
		)}},
	}, map[string][]int{"curs": {0}, "mark": {1}})

	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "CursiveMark",
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: curAdvance, HasShape: true},
			{Rune: 'b', Advance: curAdvance, HasShape: true},
			{Rune: markAcuteRune, Advance: 0, HasShape: true},
		},
		Extra: map[string][]byte{
			"GPOS": gpos,
			"GDEF": fonttest.GDEF(map[int]int{curA: classBase, curB: classBase, markGID: classMark}),
		},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}

	glyphs, _ := f.ShapeGlyphs("ab́")
	if len(glyphs) != 3 {
		t.Fatalf("got %d glyphs, want 3", len(glyphs))
	}
	if want := float64(expectedBaseLift); glyphs[1].YOffset != want {
		t.Fatalf("the base sits at %v, want %v — the fixture's join did not happen", glyphs[1].YOffset, want)
	}
	if want := float64(expectedBaseLift + baseAnchorY); glyphs[2].YOffset != want {
		t.Errorf("the mark sits at %v, want %v: it must ride the base's lift, not the baseline",
			glyphs[2].YOffset, want)
	}
	// The same for the horizontal displacement the join gave the base.
	if want := float64(expectedMarkShift); glyphs[2].XOffset != want {
		t.Errorf("the mark is displaced %v, want %v", glyphs[2].XOffset, want)
	}
}

// TestFontWithoutCursiveAnchorsIsUntouched pins that this costs nothing for the
// scripts that do not join, which is most of them.
func TestFontWithoutCursiveAnchorsIsUntouched(t *testing.T) {
	f := cursiveFace(t, 0, []fonttest.CursiveAnchor{
		// Anchors that describe no joint: an entry with nothing before it.
		{Glyph: curA, HasEntry: true, Entry: fonttest.Anchor{X: 10, Y: 10}},
	})
	glyphs, _ := f.ShapeGlyphs("abc")
	for i, g := range glyphs {
		if g.XAdvance != curAdvance || g.XOffset != 0 || g.YOffset != 0 {
			t.Errorf("glyph %d moved: advance %v offset (%v,%v)", i, g.XAdvance, g.XOffset, g.YOffset)
		}
	}
}
