package render

import (
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// CSS 2.1 §10.8.1's vertical-align, on a run of text.
//
// The property was read for atomic inlines only — a replaced element or an
// inline-block — so a <sup> was set at the smaller size the user-agent
// stylesheet gives it and on the same baseline as the words around it, and a
// "vertical-align: 96px" on a <span> moved nothing at all. §10.8.1 names
// "inline-level boxes" and distinguishes the two nowhere; the only real
// difference is where each says how far it reaches above and below its own
// baseline.
//
// # Why the numbers here are whole
//
// Ahem states an ascent of 0.8em and a descent of 0.2em with no line gap, so
// "line-height: normal" is exactly one em and every figure below is a whole
// number of pixels rather than a tolerance. It is the same face and the same
// reasoning as leading_test.go next door, which these tests extend.
//
// # What is asserted
//
// Two things per case, and they have to agree or the page is wrong in a way one
// of them alone would not catch: the *line box*, which vertical-align changes
// because a raised box reaches further above the baseline than it did, and the
// *run's own shift*, which is how far its glyphs sit below the line's baseline.
// A raise that grew the line without moving the text, or moved the text without
// growing the line, would pass one and fail the other.

// vaLine lays a document out against Ahem and returns its first line box.
func vaLine(t *testing.T, set FontSet, htmlSrc, cssSrc string) LineFragment {
	t.Helper()
	line, _ := lineOfBlock(t, set, htmlSrc, noDefaults+cssSrc, "d")
	return line
}

// shiftOf is the baseline shift of the run reading want.
func shiftOf(t *testing.T, line LineFragment, want string) float64 {
	t.Helper()
	return runAt(t, line.Runs, want).Shift.Px()
}

// baselineOfRun is where a run's own baseline sits from the top of its line box:
// the line's baseline plus whatever vertical-align moved the run by.
func baselineOfRun(t *testing.T, line LineFragment, want string) float64 {
	t.Helper()
	return line.Baseline.Add(runAt(t, line.Runs, want).Shift).Px()
}

// TestVerticalAlignLengthRaisesTheRunAndGrowsTheLine is §10.8.1's <length>:
// "raise the box by this distance", with baseline alignment otherwise.
func TestVerticalAlignLengthRaisesTheRunAndGrowsTheLine(t *testing.T) {
	set := loadAhem(t)
	// 20px Ahem at line-height 20: the half-leading is zero, so the strut and
	// every run reach 16 above the baseline and 4 below.
	//
	// The span is raised 30, so it reaches 16 + 30 = 46 above the line's baseline
	// and 4 - 30 = -26 below it. §10.8.1's maximum per side takes 46 from the
	// span and 4 from the strut: the line is 50 tall with its baseline at 46.
	line := vaLine(t, set, `<div id="d">a<span id="s">x</span></div>`, `
		#d { font-family: Ahem; font-size: 20px; line-height: 20px }
		#s { vertical-align: 30px }`)

	if got := line.Baseline.Px(); got != 46 {
		t.Errorf("the baseline is at %vpx, want 46", got)
	}
	if got := line.Rect.H.Px(); got != 50 {
		t.Errorf("the line box is %vpx tall, want 50", got)
	}
	if got := shiftOf(t, line, "x"); got != -30 {
		t.Errorf("the raised run sits %vpx below the line's baseline, want -30", got)
	}
	if got := shiftOf(t, line, "a"); got != 0 {
		t.Errorf("the run beside it sits %vpx below the line's baseline, want 0 — "+
			"vertical-align is not inherited and moves only the box it is on", got)
	}
}

// TestVerticalAlignPercentageIsOfTheBoxsOwnLineHeight pins which line-height the
// percentage is of.
//
// §10.8.1 says "a percentage of the 'line-height' value" and qualifies it no
// further, and an unqualified percentage in CSS is of the element's own value of
// the property named. The block's is the other candidate and it is a different
// number here on purpose.
func TestVerticalAlignPercentageIsOfTheBoxsOwnLineHeight(t *testing.T) {
	set := loadAhem(t)
	// The span's own line-height is 50, so 50% is a raise of 25. The block's is
	// 20, which would give 10.
	//
	// The span's box is 20px Ahem in a 50px line-height: half-leading (50-20)/2
	// is 15, so it reaches 15 + 16 = 31 above its baseline and 15 + 4 = 19 below.
	// Raised 25 it reaches 56 above the line's baseline and -6 below, so the line
	// is 56 + 4 = 60 tall with its baseline at 56.
	line := vaLine(t, set, `<div id="d">a<span id="s">x</span></div>`, `
		#d { font-family: Ahem; font-size: 20px; line-height: 20px }
		#s { line-height: 50px; vertical-align: 50% }`)

	if got := shiftOf(t, line, "x"); got != -25 {
		t.Errorf("the run is raised %vpx, want 25 — half of its own 50px "+
			"line-height, not of the block's 20px", -got)
	}
	if got := line.Baseline.Px(); got != 56 {
		t.Errorf("the baseline is at %vpx, want 56", got)
	}
	if got := line.Rect.H.Px(); got != 60 {
		t.Errorf("the line box is %vpx tall, want 60", got)
	}
}

// TestSubAndSuperMoveTheirText is the pair the user-agent stylesheet puts on
// <sub> and <sup>, and the pair whose absence was visible in every document with
// a footnote mark in it.
//
// §10.8.1 leaves the distance to the engine. A fifth of the font size down and a
// third up is what browsers use, and 50px is chosen so that both are exact in
// layout units: 0.33 x 50 is 16.5px, which is 1056 sixty-fourths.
func TestSubAndSuperMoveTheirText(t *testing.T) {
	set := loadAhem(t)
	line := vaLine(t, set,
		`<div id="d">a<span id="u">x</span><span id="v">y</span></div>`, `
		#d { font-family: Ahem; font-size: 50px; line-height: 50px }
		#u { vertical-align: super }
		#v { vertical-align: sub }`)

	if got := shiftOf(t, line, "x"); got != -16.5 {
		t.Errorf("the superscript sits %vpx below the baseline, want -16.5", got)
	}
	if got := shiftOf(t, line, "y"); got != 10 {
		t.Errorf("the subscript sits %vpx below the baseline, want 10", got)
	}
	// The line grows on both sides, which is what makes a paragraph of
	// superscripts space itself out rather than overprint the line above.
	//
	// 50px Ahem: every box reaches 40 above its own baseline and 10 below. The
	// superscript reaches 56.5 above and -6.5 below, the subscript 30 above and
	// 20 below, and the strut 40 and 10. The maxima are 56.5 and 20.
	if got := line.Baseline.Px(); got != 56.5 {
		t.Errorf("the baseline is at %vpx, want 56.5", got)
	}
	if got := line.Rect.H.Px(); got != 76.5 {
		t.Errorf("the line box is %vpx tall, want 76.5", got)
	}
}

// TestVerticalAlignTextBottomIsMeasuredAgainstTheContentArea is one of the two
// keywords that name the *parent's* content area rather than the line box, which
// is the distinction the two halves of §10.8.1 turn on.
func TestVerticalAlignTextBottomIsMeasuredAgainstTheContentArea(t *testing.T) {
	set := loadAhem(t)
	// 40px Ahem at line-height 130 — the shape of css/CSS2/linebox's
	// vertical-align-117a, which is what found this gap.
	//
	// The half-leading is (130-40)/2 = 45, so every box reaches 45 + 32 = 77
	// above its baseline and 45 + 8 = 53 below, and is 130 tall.
	//
	// "text-bottom" puts the bottom of the span's box at the bottom of the
	// parent's *content area*, which is the font's own descent — 8 below the
	// baseline, not the 53 the half-leading adds. So the span reaches 130 - 8 =
	// 122 above the line's baseline and 8 below, and the line is 122 + 53 = 175
	// tall: the strut still wants its own 53.
	line := vaLine(t, set, `<div id="d">a<span id="s">x</span></div>`, `
		#d { font-family: Ahem; font-size: 40px; line-height: 130px }
		#s { vertical-align: text-bottom }`)

	if got := line.Baseline.Px(); got != 122 {
		t.Errorf("the baseline is at %vpx, want 122", got)
	}
	if got := line.Rect.H.Px(); got != 175 {
		t.Errorf("the line box is %vpx tall, want 175", got)
	}
	// The span's own baseline is 122 - 45 = 77 from the top of the line, so it
	// is raised 45 — exactly the half-leading that "text-bottom" declines to
	// count. Aligning against the line box instead of the content area would
	// have raised it by nothing at all.
	if got := shiftOf(t, line, "x"); got != -45 {
		t.Errorf("the aligned run sits %vpx below the line's baseline, want -45", got)
	}
}

// TestVerticalAlignTopMovesTheWholeAlignedSubtree is §10.8.1's aligned subtree,
// and it is the case that separates the rule from a per-box reading of it.
//
//	The aligned subtree of an inline element contains that element and the
//	aligned subtrees of all children inline elements whose computed
//	vertical-align value is not top or bottom.
//
// So "top" moves a box *and everything baseline-aligned inside it*, keeping
// their relative positions, and puts the top of the whole group at the top of
// the line. Applying it to each run on its own pulls the smaller text up out of
// the words it belongs with — which is what css/CSS2/linebox's
// anonymous-inline-inherit-001 asserts must not happen, and what it caught.
func TestVerticalAlignTopMovesTheWholeAlignedSubtree(t *testing.T) {
	set := loadAhem(t)
	// A "top" span holding a 40px word and a 10px one. The subtree is the whole
	// of the line's content, so its top is the line's top already and nothing
	// may move: the two words stay on one baseline, 32 from the top of a 40px
	// line — which is exactly where they are with no vertical-align at all.
	const src = `<div id="d"><span id="s"><span id="b">A</span>x</span></div>`
	const base = `
		#d { font-family: Ahem; font-size: 10px; line-height: 10px }
		#b { font-size: 40px; line-height: 40px }`

	line := vaLine(t, set, src, base+`#s { vertical-align: top }`)
	control := vaLine(t, set, src, base)

	if got := line.Rect.H.Px(); got != 40 {
		t.Errorf("the line box is %vpx tall, want 40", got)
	}
	for _, word := range []string{"A", "x"} {
		got := baselineOfRun(t, line, word)
		if got != 32 {
			t.Errorf("%q sits on a baseline %vpx from the top of the line, want 32",
				word, got)
		}
		if want := baselineOfRun(t, control, word); got != want {
			t.Errorf("%q is at %vpx with \"vertical-align: top\" and %vpx without it; "+
				"the subtree already reaches the top of the line, so aligning it "+
				"there must move nothing", word, got, want)
		}
	}
}

// TestVerticalAlignTopAndBottomReachTheLineBox is the same rule where it does
// move something, so that the test above cannot be satisfied by ignoring the
// keywords altogether.
func TestVerticalAlignTopAndBottomReachTheLineBox(t *testing.T) {
	set := loadAhem(t)
	// A 40px word fixes the line: it reaches 32 above the baseline and 8 below,
	// against the 10px strut's 8 and 2, so the line is 40 tall with its baseline
	// at 32.
	//
	// The 10px span beside it is 10 tall and reaches 8 above its own baseline.
	// Aligned to the top, its own baseline is 8 from the top of the line, so it
	// sits 8 - 32 = 24 *above* the line's. Aligned to the bottom, its box ends at
	// 40 and its baseline is 40 - 2 = 38, which is 6 below the line's.
	const base = `
		#d { font-family: Ahem; font-size: 10px; line-height: 10px }
		#b { font-size: 40px; line-height: 40px }`

	for _, tc := range []struct {
		align string
		shift float64
	}{
		{"top", -24},
		{"bottom", 6},
	} {
		line := vaLine(t, set,
			`<div id="d"><span id="b">A</span><span id="s">x</span></div>`,
			base+`#s { vertical-align: `+tc.align+` }`)

		if got := line.Rect.H.Px(); got != 40 {
			t.Errorf("%s: the line box is %vpx tall, want 40 — the small span fits "+
				"inside it and may not change it", tc.align, got)
		}
		if got := line.Baseline.Px(); got != 32 {
			t.Errorf("%s: the baseline is at %vpx, want 32", tc.align, got)
		}
		if got := shiftOf(t, line, "x"); got != tc.shift {
			t.Errorf("%s: the aligned run sits %vpx below the line's baseline, want %v",
				tc.align, got, tc.shift)
		}
	}
}

// TestVerticalAlignAccumulatesAndKeywordsReplace is how §10.8.1's values compose,
// which they must because the property aligns each box against its *parent*.
func TestVerticalAlignAccumulatesAndKeywordsReplace(t *testing.T) {
	set := loadAhem(t)
	// Two raises nest and add: 10 and then 5 is 15 off the block's baseline.
	line := vaLine(t, set,
		`<div id="d">a<span id="s"><span id="t">x</span></span></div>`, `
		#d { font-family: Ahem; font-size: 20px; line-height: 20px }
		#s { vertical-align: 10px }
		#t { vertical-align: 5px }`)
	if got := shiftOf(t, line, "x"); got != -15 {
		t.Errorf("two nested raises put the run %vpx below the baseline, want -15 — "+
			"each box is aligned against its parent, so the displacements add", got)
	}

	// A keyword is a position rather than a displacement, so it replaces the
	// raise it is inside instead of adding to it. "text-bottom" on the inner
	// span puts the bottom of its box at the parent's font descent whatever the
	// outer span asked for.
	//
	// 20px Ahem at line-height 20 has no half-leading, so the box's bottom is
	// already the font's descent and "text-bottom" moves it nowhere: a run that
	// had inherited the outer 10px raise would be 10px out.
	line = vaLine(t, set,
		`<div id="d">a<span id="s"><span id="t">x</span></span></div>`, `
		#d { font-family: Ahem; font-size: 20px; line-height: 20px }
		#s { vertical-align: 10px }
		#t { vertical-align: text-bottom }`)
	if got := shiftOf(t, line, "x"); got != 0 {
		t.Errorf("a \"text-bottom\" inside a 10px raise put the run %vpx below the "+
			"baseline, want 0 — a keyword names a position and does not inherit a "+
			"displacement", got)
	}
}

// TestAnInlineBoxsInkMovesWithItsText is the agreement between layout and
// painting, on the axis vertical-align works in.
//
// §10.6.1 gives a non-replaced inline box a content area the height of its font,
// and that rectangle is where its background and its border are drawn. If
// vertical-align moves the words and leaves the rectangle, a highlighted <sup>
// is a stripe across the line with its letters somewhere above it.
func TestAnInlineBoxsInkMovesWithItsText(t *testing.T) {
	set := loadAhem(t)
	line := vaLine(t, set, `<div id="d">a<span id="s">x</span></div>`, `
		#d { font-family: Ahem; font-size: 20px; line-height: 20px }
		#s { vertical-align: 30px; background: blue }`)

	if len(line.Boxes) != 1 {
		t.Fatalf("the line has %d inline box fragments, want 1", len(line.Boxes))
	}
	// The line's baseline is at 46 and the span's own is 30 above it, at 16. Its
	// content area is the font's: 16 above its baseline and 4 below, so the
	// rectangle runs from 0 to 20 within the line box.
	//
	// The fragment has been absolutised, so the line's own top is what puts the
	// two in the same space.
	top := line.Boxes[0].BorderRect.Y.Sub(line.Rect.Y).Px()
	if top != 0 {
		t.Errorf("the span's background starts %vpx from the top of the line, want 0 "+
			"— its content area is 16 above its own baseline, which the 30px raise "+
			"put at 16", top)
	}
	if got := line.Boxes[0].BorderRect.H.Px(); got != 20 {
		t.Errorf("the span's background is %vpx tall, want 20 — §10.6.1's content "+
			"area is the font's ascent and descent", got)
	}
}

// TestADecorationIsNotMovedByADescendantsAlignment is §16.3.1, which is the one
// place the run's own shift is the wrong number to draw at.
//
//	Text decorations on inline boxes are drawn across the entire element,
//	going across any descendant elements without paying any attention to
//	their presence.
//
// So a paragraph that underlines its text rules one straight line under a raised
// <sup> and the words either side of it, and three spans at three different
// vertical-aligns under one overlining div are crossed by one line and not by
// three stepped ones. css/CSS2/text's text-decoration-va-length-001 asserts
// exactly that, and it is what caught this.
func TestADecorationIsNotMovedByADescendantsAlignment(t *testing.T) {
	set := loadAhem(t)
	const css = `
		#d { font-family: Ahem; font-size: 20px; line-height: 20px;
		     text-decoration: underline }
		#s { vertical-align: 30px }`
	line := vaLine(t, set, `<div id="d">a<span id="s">x</span></div>`, css)

	for _, word := range []string{"a", "x"} {
		run := runAt(t, line.Runs, word)
		if len(run.Decorations) != 1 {
			t.Fatalf("the run %q carries %d decorations, want 1", word, len(run.Decorations))
		}
		if got := run.Decorations[0].shift.Px(); got != 0 {
			t.Errorf("the underline across %q is drawn %vpx off the line's baseline, "+
				"want 0 — the div declared it and the div was not moved", word, got)
		}
	}

	// The other direction, so that this cannot be satisfied by never moving a
	// decoration: one declared *by* the aligned box moves with it, because that
	// box is the one §16.3.1 draws it across.
	line = vaLine(t, set, `<div id="d">a<span id="s">x</span></div>`, `
		#d { font-family: Ahem; font-size: 20px; line-height: 20px }
		#s { vertical-align: 30px; text-decoration: underline }`)
	run := runAt(t, line.Runs, "x")
	if len(run.Decorations) != 1 {
		t.Fatalf("the raised run carries %d decorations, want 1", len(run.Decorations))
	}
	if got := run.Decorations[0].shift.Px(); got != -30 {
		t.Errorf("the underline the raised span declared is drawn %vpx off the "+
			"line's baseline, want -30 — it belongs to the box that moved", got)
	}
	if got := runAt(t, line.Runs, "a").Decorations; len(got) != 0 {
		t.Errorf("the text beside the span carries %d decorations, want none", len(got))
	}
}

// TestPlainTextCarriesNoShift is the other direction of the whole change, and
// the reason it is safe for every document that never names the property: a run
// with no vertical-align above it is placed on the line's own baseline, and
// nothing about the line moved.
func TestPlainTextCarriesNoShift(t *testing.T) {
	set := loadAhem(t)
	line := vaLine(t, set, `<div id="d">abc <span id="s">def</span></div>`, `
		#d { font-family: Ahem; font-size: 20px; line-height: 20px }`)
	if got := line.Rect.H.Px(); got != 20 {
		t.Errorf("a plain line of 20px Ahem is %vpx tall, want 20", got)
	}
	if got := line.Baseline.Px(); got != 16 {
		t.Errorf("its baseline is at %vpx, want 16", got)
	}
	for _, r := range line.Runs {
		if r.Shift != style.Unit(0) {
			t.Errorf("the run %q carries a shift of %v, want 0", r.Text, r.Shift)
		}
	}
}
