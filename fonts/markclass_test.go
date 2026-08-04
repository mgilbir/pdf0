package fonts

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/fonttest"
)

// A mark class is a number local to the subtable that declares it.
//
// This is the thing about mark attachment that is easy to read past. A
// mark-to-base subtable says "class 0 attaches here" for each base it covers,
// and the class numbering is that subtable's own: another subtable's class 0 is
// a different class, about different marks, attaching somewhere else entirely.
// Noto Sans has eighteen mark-to-base subtables and four of them cover the
// letter b, each with a class 0 and each with a different anchor for it.
//
// Read into one table keyed by glyph and class, they collide. The anchor kept
// is whichever subtable was read last — in Noto Sans, one written for four
// particular marks — and every mark in the font is then placed by it, because
// the mark's own anchor came from the subtable that covers the *mark* while the
// base's came from the subtable that happened to be read last.
//
// Measured against HarfBuzz over 877 strings, that put the marks in the wrong
// place in 294 of them: every accented Latin word and every Devanagari cluster
// in the sample.

const (
	mkA     = 1
	mkB     = 2
	mkAcute = 3
	mkGrave = 4

	mkAdvA = 500
	mkAdvB = 600
)

// twoLookupMarkFace builds a face with two mark-to-base lookups that both cover
// both letters and both call their class 0, but cover different marks and put
// them in different places. That is the shape a real font has, reduced to the
// smallest thing that can tell a correct reader from a flattened one.
func twoLookupMarkFace(t *testing.T) *Face {
	t.Helper()
	first := fonttest.MarkAttachSubtable(
		[]fonttest.MarkAttachment{{Glyph: mkAcute, Class: 0, Anchor: fonttest.Anchor{X: 100, Y: 500}}},
		[]fonttest.BaseAttachment{
			{Glyph: mkA, Anchors: map[int]fonttest.Anchor{0: {X: 300, Y: 700}}},
			{Glyph: mkB, Anchors: map[int]fonttest.Anchor{0: {X: 350, Y: 720}}},
		})
	second := fonttest.MarkAttachSubtable(
		[]fonttest.MarkAttachment{{Glyph: mkGrave, Class: 0, Anchor: fonttest.Anchor{X: 200, Y: 400}}},
		[]fonttest.BaseAttachment{
			{Glyph: mkA, Anchors: map[int]fonttest.Anchor{0: {X: 900, Y: 100}}},
			{Glyph: mkB, Anchors: map[int]fonttest.Anchor{0: {X: 950, Y: 120}}},
		})

	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "MarkClasses",
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: mkAdvA, HasShape: true},
			{Rune: 'b', Advance: mkAdvB, HasShape: true},
			{Rune: 0x0301, Advance: 0, HasShape: true},
			{Rune: 0x0300, Advance: 0, HasShape: true},
		},
		Extra: map[string][]byte{
			"GPOS": fonttest.GPOSLookups([]fonttest.Lookup{
				{Type: 4, Subtables: [][]byte{first}},
				{Type: 4, Subtables: [][]byte{second}},
			}, map[string][]int{"mark": {0, 1}}),
			"GDEF": fonttest.GDEF(map[int]int{
				mkA: classBase, mkB: classBase, mkAcute: classMark, mkGrave: classMark,
			}),
		},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

// TestEachMarkIsPlacedByItsOwnSubtable is the defect stated as a test.
func TestEachMarkIsPlacedByItsOwnSubtable(t *testing.T) {
	f := twoLookupMarkFace(t)

	for _, tc := range []struct {
		text    string
		base    int
		baseAdv float64
		// The anchors the subtable covering this mark declares.
		baseX, baseY, markX, markY float64
		why                        string
	}{
		{"á", mkA, mkAdvA, 300, 700, 100, 500, "acute over a, from the first lookup"},
		{"b́", mkB, mkAdvB, 350, 720, 100, 500, "acute over b, from the first lookup"},
		{"à", mkA, mkAdvA, 900, 100, 200, 400, "grave over a, from the second lookup"},
		{"b̀", mkB, mkAdvB, 950, 120, 200, 400, "grave over b, from the second lookup"},
	} {
		glyphs, missing := f.ShapeGlyphs(tc.text)
		if missing != 0 {
			t.Errorf("%s: %d runes have no glyph", tc.why, missing)
			continue
		}
		if len(glyphs) != 2 {
			t.Errorf("%s: shaped to %d glyphs, want 2", tc.why, len(glyphs))
			continue
		}
		// The pen has passed the base by the time the mark is drawn, so the
		// base's advance comes off the offset.
		wantX := (tc.baseX - tc.markX) - tc.baseAdv
		wantY := tc.baseY - tc.markY
		if glyphs[1].XOffset != wantX || glyphs[1].YOffset != wantY {
			t.Errorf("%s: placed at (%v, %v), want (%v, %v)\n"+
				"Both lookups call their class 0, and both cover this base. The anchor "+
				"has to come from the subtable that covers this *mark*.",
				tc.why, glyphs[1].XOffset, glyphs[1].YOffset, wantX, wantY)
		}
	}
}

// TestAMarkNoSubtableCoversIsLeftAlone pins the negative case. A subtable that
// covers the base but not the mark says nothing about this pair, and must not be
// used for it — which is exactly the coincidence the flattened reader turned
// into an anchor.
func TestAMarkNoSubtableCoversIsLeftAlone(t *testing.T) {
	// One lookup, covering the acute only, with 'a' and 'b' as bases.
	sub := fonttest.MarkAttachSubtable(
		[]fonttest.MarkAttachment{{Glyph: mkAcute, Class: 0, Anchor: fonttest.Anchor{X: 100, Y: 500}}},
		[]fonttest.BaseAttachment{{Glyph: mkA, Anchors: map[int]fonttest.Anchor{0: {X: 300, Y: 700}}}},
	)
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "OneMark",
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: mkAdvA, HasShape: true},
			{Rune: 'b', Advance: mkAdvB, HasShape: true},
			{Rune: 0x0301, Advance: 0, HasShape: true},
			{Rune: 0x0300, Advance: 0, HasShape: true},
		},
		Extra: map[string][]byte{
			"GPOS": fonttest.GPOSLookups(
				[]fonttest.Lookup{{Type: 4, Subtables: [][]byte{sub}}},
				map[string][]int{"mark": {0}}),
			"GDEF": fonttest.GDEF(map[int]int{
				mkA: classBase, mkB: classBase, mkAcute: classMark, mkGrave: classMark,
			}),
		},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}

	// The grave is in no mark coverage at all: nothing places it.
	glyphs, _ := f.ShapeGlyphs("à")
	if len(glyphs) != 2 {
		t.Fatalf("shaped to %d glyphs, want 2", len(glyphs))
	}
	if glyphs[1].XOffset != 0 || glyphs[1].YOffset != 0 {
		t.Errorf("a mark no subtable covers was placed at (%v, %v); the font says nothing about it",
			glyphs[1].XOffset, glyphs[1].YOffset)
	}

	// The acute over 'b' — a base the subtable does not cover — likewise.
	glyphs, _ = f.ShapeGlyphs("b́")
	if len(glyphs) != 2 {
		t.Fatalf("shaped to %d glyphs, want 2", len(glyphs))
	}
	if glyphs[1].XOffset != 0 || glyphs[1].YOffset != 0 {
		t.Errorf("a mark over a base the subtable does not cover was placed at (%v, %v)",
			glyphs[1].XOffset, glyphs[1].YOffset)
	}
}

// TestBundledFontPlacesMarksWhereHarfBuzzDoes is the same fix checked against a
// real font and an outside authority.
//
// The values are HarfBuzz's, measured over the bundled face with its own default
// feature set. They are ground truth from a different implementation, which is
// what makes them worth pinning: a fixture built from this package's own reading
// of a table could agree with a misreading of it.
//
// Verified to fail: with the mark subtables merged into one table keyed by glyph
// and class, every one of these is wrong — 'ö' + acute goes to (242, 0) instead
// of (-30, 194).
func TestBundledFontPlacesMarksWhereHarfBuzzDoes(t *testing.T) {
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	for _, tc := range []struct {
		text   string
		at     int
		dx, dy float64
		why    string
	}{
		{"ö́", 1, -30, 194, "acute above the diaeresis of a composed o"},
		{"áb́c", 2, -5, 224, "acute over b, mid-word"},
		{"ọ̀", 1, -6, 0, "dot below o"},
		{"ọ̀", 2, 61, 0, "grave after a dot below"},
		{"र्क", 1, -221, 0, "Devanagari reph over ka"},
	} {
		glyphs, missing := f.ShapeGlyphs(tc.text)
		if missing != 0 {
			t.Errorf("%s: %d runes have no glyph", tc.why, missing)
			continue
		}
		if tc.at >= len(glyphs) {
			t.Errorf("%s: shaped to %d glyphs, no index %d", tc.why, len(glyphs), tc.at)
			continue
		}
		g := glyphs[tc.at]
		if g.XOffset != tc.dx || g.YOffset != tc.dy {
			t.Errorf("%s: glyph %d placed at (%v, %v); HarfBuzz places it at (%v, %v)",
				tc.why, tc.at, g.XOffset, g.YOffset, tc.dx, tc.dy)
		}
	}
}

// A mark written under a ligature attaches to the part of it the mark belongs
// to.
//
// "ffi" is one glyph. A dot written under the first f and a dot written under
// the second are the same mark attaching to the same glyph, and they belong in
// quite different places — so the font does not give the ligature one anchor,
// it gives it one per component, and the shaper has to know which component the
// mark came from. That is GPOS type 5, and it is the reason forming a ligature
// records which of its parts each kept glyph stood between.
//
// Without it a mark inside a ligature is left where it fell, at the pen — which
// after a 946-unit ligature is most of an em to the right of where it belongs.

// TestAMarkInsideALigatureAttachesToItsOwnComponent is the defect stated as a
// test, against the bundled face.
//
// The offsets are HarfBuzz's. The font gives the ffi ligature component anchors
// at x = 132, 476 and 819, the dot below has its own at x = -298, and the
// ligature is 946 wide: 132 + 298 - 946 = -516 for a dot under the first f, and
// 476 + 298 - 946 = -172 for one under the second. Two of the same glyph pair,
// half an em apart, decided by nothing but which part of the text the mark came
// from.
func TestAMarkInsideALigatureAttachesToItsOwnComponent(t *testing.T) {
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	for _, tc := range []struct {
		text   string
		dx, dy float64
		why    string
	}{
		{"f̣fi", -516, 0, "a dot below the first f of ffi"},
		{"ff̣i", -172, 0, "a dot below the second f of ffi"},
		{"f́fi", -422, 229, "an acute over the first f of ffi"},
		{"ff́i", -78, 229, "an acute over the second f of ffi"},
		{"f̣fl", -516, 0, "a dot below the first f of ffl"},
		{"ff̣l", -172, 0, "a dot below the second f of ffl"},
	} {
		glyphs, missing := f.ShapeGlyphs(tc.text)
		if missing != 0 {
			t.Errorf("%s: %d characters have no glyph", tc.why, missing)
			continue
		}
		if len(glyphs) != 2 {
			t.Errorf("%s: shaped to %d glyphs, want 2 — the ligature and the mark it kept",
				tc.why, len(glyphs))
			continue
		}
		if glyphs[1].XOffset != tc.dx || glyphs[1].YOffset != tc.dy {
			extra := ""
			if glyphs[1].XOffset == 0 && glyphs[1].YOffset == 0 {
				extra = " — it was not attached at all"
			}
			t.Errorf("%s: placed at (%v, %v), HarfBuzz places it at (%v, %v)%s",
				tc.why, glyphs[1].XOffset, glyphs[1].YOffset, tc.dx, tc.dy, extra)
		}
	}
}

// TestAMarkAfterALigatureGoesOnItsLastComponent pins the case the component is
// not known for.
//
// A mark written after "ffi" is not inside the ligature — it came after all
// three letters — so there is no component it belongs to, and OpenType says
// such a mark attaches to the last one. It is the only sensible answer: a mark
// written after "ffi" belongs to the i.
func TestAMarkAfterALigatureGoesOnItsLastComponent(t *testing.T) {
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	// Component 2 of the ffi ligature anchors at x = 819; the dot below anchors
	// at -298; the ligature is 946 wide.
	const wantX = 819 + 298 - 946
	glyphs, _ := f.ShapeGlyphs("ffị")
	if len(glyphs) != 2 {
		t.Fatalf("shaped to %d glyphs, want 2", len(glyphs))
	}
	if glyphs[1].XOffset != wantX {
		t.Errorf("a dot written after ffi is placed at %v, want %v — the last component",
			glyphs[1].XOffset, float64(wantX))
	}
}
