package render

import "testing"

// Inline-blocks.
//
// An inline-block is the other atomic inline, and it arrived with replaced
// elements because it needs exactly the same thing from a line box: a place for
// a whole box rather than for a run of words.
//
// Before it was atomic, the engine walked into one as though it were a <span>,
// which flattened its content onto the surrounding line. Everything about the
// box then quietly did nothing — its width, its height, its background, its
// border and its padding — and the page looked as though a stylesheet had been
// ignored rather than as though a box was missing. The tests below are written
// against that failure: each asserts something that is only true if the box
// exists.

// TestInlineBlockIsABoxOnTheLine pins that the box exists at all: it has a
// fragment, at the position the line put it, with the size its declarations
// asked for.
func TestInlineBlockIsABoxOnTheLine(t *testing.T) {
	root := layoutOf(t, 500, `<div id="d">a<span id="s">x</span>b</div>`, noDefaults,
		`#s { display: inline-block; width: 100px; height: 30px }`)
	s := find(t, root, "s")
	px(t, "the inline-block's width", s.BorderRect.W, 100)
	px(t, "the inline-block's height", s.BorderRect.H, 30)
}

// TestInlineBlockTakesWidthFromTheLine pins that it advances the pen: the text
// after it starts past it rather than on top of it.
func TestInlineBlockTakesWidthFromTheLine(t *testing.T) {
	root := layoutOf(t, 500, `<div id="d"><span id="s">x</span>after</div>`, noDefaults,
		`#s { display: inline-block; width: 100px; height: 10px }`)
	d := find(t, root, "d")
	if len(d.Lines) != 1 {
		t.Fatalf("the block has %d lines, want 1", len(d.Lines))
	}
	// The inline-block's own text is inside its own fragment, so the block's
	// line carries only "after" — which is itself the assertion that the
	// content was not flattened.
	if n := len(d.Lines[0].Runs); n != 1 {
		t.Fatalf("the line has %d runs, want 1; the inline-block's content was "+
			"flattened onto its parent's line", n)
	}
	px(t, "the text's offset along the line", d.Lines[0].Runs[0].X, 100)
}

// TestInlineBlockShrinksToFit pins §10.3.9: an auto width is shrink-to-fit, the
// same formula a float uses. A box as wide as its containing block would leave
// no room beside it and the whole point of the display value would be lost.
func TestInlineBlockShrinksToFit(t *testing.T) {
	root := layoutOf(t, 500, `<div id="d"><span id="s"><em id="e">x</em></span></div>`,
		noDefaults, `#s { display: inline-block } #e { display: block; width: 60px }`)
	s := find(t, root, "s")
	px(t, "the inline-block's width", s.BorderRect.W, 60)
}

// TestInlineBlockBaselineIsItsLastLine is §10.8.1's rule for an inline-block,
// and the one that distinguishes it from a replaced element: its baseline is
// the baseline of its *last line box*, so a box of two lines hangs below the
// surrounding text by the depth of its second one rather than sitting on the
// line by its bottom edge.
func TestInlineBlockBaselineIsItsLastLine(t *testing.T) {
	root := layoutOf(t, 500,
		`<div id="d">x<span id="s">a<br>b</span></div>`, noDefaults,
		`#d { font-size: 16px; line-height: 20px } #s { display: inline-block }`)
	d := find(t, root, "d")
	s := find(t, root, "s")
	if len(s.Lines) != 2 {
		t.Fatalf("the inline-block has %d lines, want 2", len(s.Lines))
	}
	line := d.Lines[0]
	// The box is two 20px lines tall, and its own second baseline is at
	// 20 + 15.44 within it. The outer line's baseline is therefore that far
	// down, and the box's top is at zero.
	want := s.Lines[1].Rect.Y.Add(s.Lines[1].Baseline)
	if line.Baseline != want {
		t.Errorf("the line's baseline is %.2fpx; the inline-block's last line "+
			"baseline is at %.2f and is what it should be",
			line.Baseline.Px(), want.Px())
	}
	px(t, "the inline-block's top", s.BorderRect.Y, 0)
}

// TestInlineBlockDescentDeepensTheLine is the half of §10.8's stacking that an
// image can never exercise.
//
// A replaced element is all ascent, so a line holding one is only ever as deep
// below the baseline as the surrounding type wants. An inline-block hangs: its
// baseline is its last line's, so everything under that — another line, a
// padding, a bottom margin — is below the outer baseline and the line box has to
// reach it. An engine that took only the strut's descent would leave the box
// overlapping whatever comes next.
func TestInlineBlockDescentDeepensTheLine(t *testing.T) {
	root := layoutOf(t, 500, `<div id="d">x<span id="s">a</span></div>`, noDefaults,
		`#d { font-size: 16px; line-height: 20px }
		 #s { display: inline-block; margin-bottom: 8px }`)
	d := find(t, root, "d")
	s := find(t, root, "s")
	line := d.Lines[0]

	// How far the inline-block's margin box hangs below the line's baseline.
	below := s.MarginRect().Bottom().Sub(line.Baseline)
	if below <= 0 {
		t.Fatalf("the inline-block does not hang below the baseline (%.2fpx); "+
			"this test is checking nothing", below.Px())
	}
	if got := line.Rect.H.Sub(line.Baseline); got != below {
		t.Errorf("the line reaches %.2fpx below its baseline and the inline-block "+
			"reaches %.2f; the box hangs out of its own line",
			got.Px(), below.Px())
	}
}

// TestInlineBlockWithNoLinesSitsOnItsBottomEdge is the other half of §10.8.1:
// with no in-flow line box there is no baseline to take, so the bottom margin
// edge is used — which is what makes an empty inline-block behave like an
// image.
func TestInlineBlockWithNoLinesSitsOnItsBottomEdge(t *testing.T) {
	root := layoutOf(t, 500, `<div id="d">x<span id="s"></span></div>`, noDefaults,
		`#d { font-size: 16px; line-height: 20px }
		 #s { display: inline-block; width: 50px; height: 40px }`)
	d := find(t, root, "d")
	px(t, "the line's baseline", d.Lines[0].Baseline, 40)
	px(t, "the inline-block's top", find(t, root, "s").BorderRect.Y, 0)
}

// TestInlineBlockPaintsItsBackground pins that the box reaches the display
// list, which is the visible half of being a box.
func TestInlineBlockPaintsItsBackground(t *testing.T) {
	root := layoutOf(t, 500, `<div id="d">x<span id="s">y</span></div>`, noDefaults,
		`#s { display: inline-block; width: 30px; height: 10px; background-color: red }`)
	var found bool
	for _, op := range Paint(root) {
		fill, ok := op.(FillRect)
		if !ok {
			continue
		}
		if fill.Color.R == 255 && fill.Rect.W.Px() == 30 && fill.Rect.H.Px() == 10 {
			found = true
		}
	}
	if !found {
		t.Error("the inline-block painted no background")
	}
}

// TestInlineBlockContainsItsFloats pins that it establishes a formatting
// context: a float inside it does not escape, and its own height reaches the
// bottom of that float. This is what "flow-root" means, and the reason
// inline-block is the idiom it is.
func TestInlineBlockContainsItsFloats(t *testing.T) {
	root := layoutOf(t, 500,
		`<div id="d"><span id="s"><em id="f"></em></span></div>`, noDefaults,
		`#s { display: inline-block; width: 100px }
		 #f { display: block; float: left; width: 20px; height: 50px }`)
	s := find(t, root, "s")
	px(t, "the inline-block's height", s.BorderRect.H, 50)
}
