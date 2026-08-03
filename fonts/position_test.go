package fonts

import (
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/internal/fonttest"
)

// accentFace has a base letter and a combining acute, with the font stating
// where the accent attaches: the base's anchor is high and centred, the mark's
// is at its own top-centre. Glyph indices: a=1, acute=2, b=3.
func accentFace(t *testing.T, kind int) *Face {
	t.Helper()
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "Accents",
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: 500, HasShape: true},
			{Rune: 0x0301, Advance: 300, HasShape: true}, // combining acute; a real advance, so that zeroing it is observable
			{Rune: 'b', Advance: 500, HasShape: true},
		},
		Extra: map[string][]byte{
			"GDEF": fonttest.GDEF(map[int]int{1: 1, 2: 3, 3: 1}), // base, mark, base
			"GPOS": fonttest.GPOSMarkToBase(kind,
				[]fonttest.MarkAttachment{{Glyph: 2, Class: 0, Anchor: fonttest.Anchor{X: 100, Y: 0}}},
				[]fonttest.BaseAttachment{{Glyph: 1, Anchors: map[int]fonttest.Anchor{0: {X: 250, Y: 700}}}},
			),
		},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

// TestMarkAttachesToItsBase is the whole point of positioning into glyphs. The
// font says the accent's anchor meets the base's; without applying that the
// accent is drawn at its nominal advance, which for a zero-width mark is the
// origin — so every accent in a run piles up in the same place.
func TestMarkAttachesToItsBase(t *testing.T) {
	f := accentFace(t, 4)
	glyphs, missing := f.ShapeGlyphs("a\u0301") // a + combining acute, not the precomposed á
	if missing != 0 {
		t.Fatalf("%d runes missing", missing)
	}
	if len(glyphs) != 2 {
		t.Fatalf("got %d glyphs, want 2 (the letter and its accent)", len(glyphs))
	}
	base, mark := glyphs[0], glyphs[1]
	if base.GID != 1 || mark.GID != 2 {
		t.Fatalf("glyphs are %d and %d, want 1 and 2", base.GID, mark.GID)
	}
	// The base is 500 wide and its anchor is at x=250; the mark's own anchor is
	// at x=100. So the mark is placed 250-100 = 150 from the base's origin,
	// which is 500-150 = 350 back from where the pen now stands.
	if want := 250.0 - 100 - 500; mark.XOffset != want {
		t.Errorf("mark XOffset = %v, want %v", mark.XOffset, want)
	}
	if want := 700.0 - 0; mark.YOffset != want {
		t.Errorf("mark YOffset = %v, want %v", mark.YOffset, want)
	}
	// A mark does not move the pen, whatever advance the font gave it: the next
	// letter follows the base. This font gives the acute an advance of 300,
	// which a shaper that passed it through would add to every accented word.
	if mark.XAdvance != 0 {
		t.Errorf("mark XAdvance = %v, want 0 (the font declares 300)", mark.XAdvance)
	}
}

// TestMarkAttachmentSurvivesFollowingText pins that the pen is where it should
// be afterwards. A mark that moved the pen would push everything after it along
// by the width of an accent.
func TestMarkAttachmentSurvivesFollowingText(t *testing.T) {
	f := accentFace(t, 4)
	glyphs, _ := f.ShapeGlyphs("a\u0301b")
	if len(glyphs) != 3 {
		t.Fatalf("got %d glyphs, want 3", len(glyphs))
	}
	if got, want := MeasureGlyphs(glyphs, 1000), 1000.0; got != want {
		t.Errorf("the run measures %v, want %v: two 500-wide letters and a mark that takes no room "+
			"(the font gives the mark an advance of 300, which attachment discards)", got, want)
	}
}

// TestStackedMarksAttachToEachOther pins mark-to-mark. Two accents on one
// letter stack, and the second attaches to the first rather than to the base —
// otherwise they are drawn on top of one another.
func TestStackedMarksAttachToEachOther(t *testing.T) {
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "Stacked",
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: 500, HasShape: true}, // 1
			{Rune: '́', Advance: 0, HasShape: true},   // 2, acute
			{Rune: '̈', Advance: 0, HasShape: true},   // 3, diaeresis
		},
		Extra: map[string][]byte{
			"GDEF": fonttest.GDEF(map[int]int{1: 1, 2: 3, 3: 3}),
			"GPOS": fonttest.GPOSMarkToBase(6,
				[]fonttest.MarkAttachment{{Glyph: 3, Class: 0, Anchor: fonttest.Anchor{X: 0, Y: 0}}},
				// The acute receives another mark 200 units above itself.
				[]fonttest.BaseAttachment{{Glyph: 2, Anchors: map[int]fonttest.Anchor{0: {X: 0, Y: 200}}}},
			),
		},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	glyphs, _ := f.ShapeGlyphs("á̈")
	if len(glyphs) != 3 {
		t.Fatalf("got %d glyphs, want 3", len(glyphs))
	}
	if glyphs[2].YOffset != 200 {
		t.Errorf("the second mark is at y=%v, want 200: it attaches to the first mark, not the base",
			glyphs[2].YOffset)
	}
}

// TestSinglePositioningNudgesAGlyph pins GPOS type 1: an adjustment applied
// wherever a glyph appears, which is how a font corrects a glyph that sits
// wrongly in its own box.
func TestSinglePositioningNudgesAGlyph(t *testing.T) {
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "Nudge",
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: 500, HasShape: true},
			{Rune: 'b', Advance: 500, HasShape: true},
		},
		Extra: map[string][]byte{"GPOS": fonttest.GPOSSingle(2, 30, -20, 40)},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	glyphs, _ := f.ShapeGlyphs("ab")
	if len(glyphs) != 2 {
		t.Fatalf("got %d glyphs", len(glyphs))
	}
	if glyphs[0].XOffset != 0 || glyphs[0].YOffset != 0 {
		t.Errorf("the unadjusted glyph moved: %+v", glyphs[0])
	}
	if glyphs[1].XOffset != 30 || glyphs[1].YOffset != -20 {
		t.Errorf("adjusted glyph offset = (%v, %v), want (30, -20)", glyphs[1].XOffset, glyphs[1].YOffset)
	}
	if glyphs[1].XAdvance != 540 {
		t.Errorf("adjusted glyph advance = %v, want 540", glyphs[1].XAdvance)
	}
}

// TestDrawEmitsTheOffsetsItWasGiven pins the arithmetic that turns positions
// into operators: an offset before the glyph, the rest after, and a rise for
// the vertical part — which is the only way a text object lifts a glyph off the
// baseline without disturbing the pen.
func TestDrawEmitsTheOffsetsItWasGiven(t *testing.T) {
	f := accentFace(t, 4)
	glyphs, missing := f.ShapeGlyphs("a\u0301")
	if missing != 0 {
		t.Fatalf("%d runes missing", missing)
	}

	var b content.Builder
	b.BeginText().SetFont("F1", 10)
	f.Draw(&b, glyphs, 10)
	b.EndText()
	out, err := b.Bytes()
	if err != nil {
		t.Fatalf("drawing: %v", err)
	}
	stream := string(out)
	// The mark's vertical offset of 700/1000 em at 10pt is a rise of 7.
	if !contains(stream, "7 Ts") {
		t.Errorf("no rise of 7 was emitted:\n%s", stream)
	}
	// And it is put back afterwards, or everything shown next would be raised.
	if !contains(stream, "0 Ts") {
		t.Errorf("the rise was not reset:\n%s", stream)
	}
	// The horizontal offset must be emitted too. The mark sits 350 back from
	// where the pen stands after the base, and TJ subtracts — so the number in
	// the stream is +350. Without it the accent is drawn after the letter
	// rather than over it.
	if !contains(stream, "350") {
		t.Errorf("the horizontal offset was not emitted:\n%s", stream)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// TestUnmarkedTextIsUnchangedByPositioning pins that the new machinery costs
// nothing where there is nothing to do: a run with no marks and no adjustments
// shapes to exactly the glyphs and advances the font gives.
func TestUnmarkedTextIsUnchangedByPositioning(t *testing.T) {
	f := loadTestFace(t, alphabet()...)
	glyphs, _ := f.ShapeGlyphs("abc")
	for i, g := range glyphs {
		if g.XOffset != 0 || g.YOffset != 0 {
			t.Errorf("glyph %d was displaced: %+v", i, g)
		}
		want, _ := f.Advance(rune('a' + i))
		if g.XAdvance != want {
			t.Errorf("glyph %d advance = %v, want %v", i, g.XAdvance, want)
		}
	}
}
