package render

import (
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// Where an atomic inline sits in Appendix E's painting order.
//
// §E.2 divides the marks of one stacking context into layers, and the two that
// matter here are:
//
//	4. ...the in-flow, non-inline-level, non-positioned descendants... background
//	   and border
//	7. ...for each line box of that element... if the box is an inline-block or
//	   inline-table, paint it atomically as if it created a new stacking context
//
// The words "non-inline-level" in step 4 are the whole of it. An inline-block's
// background is not a block background: it travels with the line it sits on, and
// so is painted *after* the background of every ordinary block in the same
// context — including blocks that come later in the document and overlap it.
//
// An engine painting in tree order gets this backwards for exactly the documents
// that test it, and gets it right for every document that does not overlap, so
// the fault is invisible until something is drawn on top of something else.

// paintOrderOf returns the index of the first fill of a colour, or -1.
func indexOfFill(ops []Op, c style.RGBA) int {
	for i, op := range ops {
		if f, ok := op.(FillRect); ok && sameColour(f.Color, c) && !f.Rect.Empty() {
			return i
		}
	}
	return -1
}

// TestAnInlineBlockPaintsOverALaterBlockBackground is §E.2 steps 4 and 7.
func TestAnInlineBlockPaintsOverALaterBlockBackground(t *testing.T) {
	// Two siblings the same size, the second pulled up over the first by a
	// negative margin so that they cover exactly the same rectangle. The first
	// holds a green inline-block and the second has a red background, and the
	// only thing that decides which is visible is the order the two are painted
	// in.
	root := layoutOf(t, 400,
		`<div id="one"><span id="s"></span></div><div id="two"></div>`,
		noDefaults+`
		  #one { width: 100px; height: 20px }
		  #s { display: inline-block; width: 100px; height: 20px;
		       background: green; vertical-align: top }
		  #two { width: 100px; height: 20px; margin-top: -20px; background: red }`)

	green := style.RGBA{G: 128, A: 1}
	red := style.RGBA{R: 255, A: 1}
	ops := Paint(root)
	g, r := indexOfFill(ops, green), indexOfFill(ops, red)
	if g < 0 || r < 0 {
		t.Fatalf("expected both fills; green at %d, red at %d", g, r)
	}
	if g < r {
		t.Errorf("the inline-block's background was painted at %d, before the "+
			"later block's at %d; §E.2 step 4 is over the non-inline-level "+
			"descendants, so an inline-block's background belongs with its line "+
			"in step 7 and goes on top", g, r)
	}
	// The two really do overlap, so the order above is the only thing that can
	// decide what is seen. Without this the test would pass on two rectangles
	// that never meet.
	s, two := find(t, root, "s"), find(t, root, "two")
	if s.BorderRect != two.BorderRect {
		t.Fatalf("the two boxes must cover the same rectangle for the order to "+
			"decide anything: %v against %v", s.BorderRect, two.BorderRect)
	}
}

// TestAnOrdinaryBlockStillPaintsInStepFour is the other side, and it is what
// stops the rule above from being "paint every background last".
//
// A nested *block* is still step 4, so a later sibling's background covers it.
// If the change that moved inline-blocks had moved every child, this would have
// flipped too and nothing would have said so.
func TestAnOrdinaryBlockStillPaintsInStepFour(t *testing.T) {
	root := layoutOf(t, 400,
		`<div id="one"><div id="s"></div></div><div id="two"></div>`,
		noDefaults+`
		  #one { width: 100px; height: 20px }
		  #s { width: 100px; height: 20px; background: green }
		  #two { width: 100px; height: 20px; margin-top: -20px; background: red }`)

	ops := Paint(root)
	g := indexOfFill(ops, style.RGBA{G: 128, A: 1})
	r := indexOfFill(ops, style.RGBA{R: 255, A: 1})
	if g < 0 || r < 0 {
		t.Fatalf("expected both fills; green at %d, red at %d", g, r)
	}
	if g > r {
		t.Errorf("a nested block's background was painted at %d, after the later "+
			"sibling's at %d; both are §E.2 step 4 and go in tree order", g, r)
	}
}

// TestAnInlineBlockIsPaintedAsAUnit is the "atomically" in §E.2 step 7: an
// inline-block's own background and the text inside it are one group, so
// nothing from the enclosing context can be drawn between them.
func TestAnInlineBlockIsPaintedAsAUnit(t *testing.T) {
	// A nested block inside the inline-block. Painting the inline-block as a
	// unit puts its own background, then its child's, next to each other; the
	// old tree-order walk put the outer div's own content between them only
	// because there was none. What this asserts is that the child travels with
	// its parent rather than joining the enclosing context's block layer, which
	// is what "as if it created a new stacking context" means.
	root := layoutOf(t, 400,
		`<div id="one"><span id="s"><div id="inner"></div></span></div>`+
			`<div id="two"></div>`,
		noDefaults+`
		  #one { width: 100px; height: 20px }
		  #s { display: inline-block; width: 100px; height: 20px;
		       background: green; vertical-align: top }
		  #inner { width: 100px; height: 20px; background: blue }
		  #two { width: 100px; height: 20px; margin-top: -20px; background: red }`)

	ops := Paint(root)
	g := indexOfFill(ops, style.RGBA{G: 128, A: 1})
	b := indexOfFill(ops, style.RGBA{B: 255, A: 1})
	r := indexOfFill(ops, style.RGBA{R: 255, A: 1})
	if g < 0 || b < 0 || r < 0 {
		t.Fatalf("expected three fills; green %d, blue %d, red %d", g, b, r)
	}
	if !(r < g && g < b) {
		t.Errorf("the group is out of order: red %d, green %d, blue %d — the "+
			"later block's background comes first, then the inline-block and "+
			"everything inside it, together", r, g, b)
	}
}
