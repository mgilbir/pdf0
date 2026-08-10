package render

import (
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/style"
)

// Floats and clear.
//
// Every number below is derived from CSS 2.1's own arithmetic and stated in the
// comment that asserts it, never recorded from a run. The documents are written
// so that only the rule under test can produce the answer: a float test whose
// expected position is also where an ordinary block would have gone proves
// nothing, so each one is arranged to differ from the in-flow answer.
//
// Text widths come from Helvetica, whose advances the PDF specification fixes,
// so an exact assertion about a line's contents is possible. At 100px, "a" is
// 55.6px and a space 27.8px.

// relY and relX give a fragment's position within an ancestor's content box.
//
// Layout is absolutised before it is returned, so a raw BorderRect carries the
// page margin and every enclosing box's offset with it. Asserting against those
// would make each expected number a sum of things the test is not about.
func relY(t *testing.T, child, parent *Fragment) style.Unit {
	t.Helper()
	return child.BorderRect.Y.Sub(parent.ContentRect().Y)
}

func relX(t *testing.T, child, parent *Fragment) style.Unit {
	t.Helper()
	return child.BorderRect.X.Sub(parent.ContentRect().X)
}

// helvetica is the face the width arithmetic below is against.
func helvetica(t *testing.T) *fonts.Face {
	t.Helper()
	face, err := fonts.Standard("Helvetica")
	if err != nil {
		t.Fatalf("loading Helvetica: %v", err)
	}
	return face
}

func measured(t *testing.T, text string, size float64) float64 {
	t.Helper()
	return helvetica(t).Measure(text, size)
}

// TestFloatIsOutOfTheNormalFlow pins the first half of §9.5: the float is taken
// out of the flow, so the block after it starts where the float started and is
// as wide as though the float were not there.
//
// The two assertions are the ones that fail in opposite directions. If the float
// were left in the flow the following block would be pushed to y=60; if it were
// removed from the tree entirely the float itself would have no fragment.
func TestFloatIsOutOfTheNormalFlow(t *testing.T) {
	css := noDefaults + `
	#f { float: left; width: 100px; height: 60px }
	#b { height: 40px }`
	root := layoutOf(t, 1000,
		`<section id="w"><div id="f"></div><div id="b"></div></section>`, css)

	w, f, b := find(t, root, "w"), find(t, root, "f"), find(t, root, "b")

	px(t, "the float's top", relY(t, f, w), 0)
	px(t, "the float's left", relX(t, f, w), 0)
	px(t, "the float's width", f.BorderRect.W, 100)

	// The block that follows starts at the top, not below the float, and takes
	// the full width of the containing block. Only a box out of the flow gives
	// both of those at once.
	px(t, "the following block's top", relY(t, b, w), 0)
	px(t, "the following block's left", relX(t, b, w), 0)
	px(t, "the following block's width", b.BorderRect.W, 1000)

	// And an ordinary block does not grow to hold its floats, so the wrapper is
	// as tall as its in-flow content and no taller. This is the half of §10.6.7
	// that says what does *not* happen.
	px(t, "the wrapper's height", w.BorderRect.H, 40)
}

// TestFloatGoesToTheEdgeItNames pins §9.5.1 rules 1 and 2: a left float's outer
// left edge meets the containing block's left content edge, and a right float's
// outer right edge meets the right one.
//
// The margins are there so that the assertion is about the *outer* edge. A float
// with a 15px left margin sits with its border box at 15, not at 0, and an
// implementation that positioned the border box instead would pass without them.
func TestFloatGoesToTheEdgeItNames(t *testing.T) {
	css := noDefaults + `
	#l { float: left; width: 100px; height: 50px; margin-left: 15px }
	#r { float: right; width: 80px; height: 50px; margin-right: 25px }`
	root := layoutOf(t, 1000,
		`<section id="w"><div id="l"></div><div id="r"></div></section>`, css)

	w := find(t, root, "w")
	px(t, "the left float's border box", relX(t, find(t, root, "l"), w), 15)
	// 1000 - 25 of margin - 80 of width.
	px(t, "the right float's border box", relX(t, find(t, root, "r"), w), 1000-25-80)
}

// TestFloatsSitBesideEachOtherUntilThereIsNoRoom pins §9.5.1 rules 3 and 7 — as
// far up as possible, then as far to the side as possible — and rule 8, which
// sends a float that will not fit below the ones in its way.
//
// The containing block is 250px and each float is 100px, so two fit on the first
// band and the third does not: 250 - 200 = 50, and 50 < 100.
func TestFloatsSitBesideEachOtherUntilThereIsNoRoom(t *testing.T) {
	css := noDefaults + `
	#w { width: 250px }
	div div { float: left; width: 100px; height: 50px }`
	root := layoutOf(t, 1000,
		`<div id="w"><div id="a"></div><div id="b"></div><div id="c"></div></div>`, css)

	w := find(t, root, "w")
	for _, tc := range []struct {
		id   string
		x, y float64
	}{
		{"a", 0, 0},
		{"b", 100, 0},
		// No room left on the first band, so it drops to the bottom of the
		// floats above it and starts again at the left edge.
		{"c", 0, 50},
	} {
		f := find(t, root, tc.id)
		px(t, "#"+tc.id+"'s left", relX(t, f, w), tc.x)
		px(t, "#"+tc.id+"'s top", relY(t, f, w), tc.y)
	}
}

// TestFloatsDoNotReorder pins §9.5.1 rule 5: a float may not start higher than
// any float declared before it.
//
// Without the rule the third float here would slide up into the gap beside the
// first, which puts the boxes on the page in a different order from the markup
// while looking perfectly tidy — the failure mode that makes rule 5 worth having
// rather than an implementation detail.
//
// #b clears #a, so it starts at y=50. At y=0 the band right of #a is 100 wide
// and #c is 50 wide, so #c would fit there. Rule 5 forbids it, and the first
// position at or below #b's top with room for #c is (100, 50).
func TestFloatsDoNotReorder(t *testing.T) {
	css := noDefaults + `
	#w { width: 200px }
	div div { float: left }
	#a { width: 100px; height: 50px }
	#b { width: 100px; height: 50px; clear: left }
	#c { width: 50px; height: 20px }`
	root := layoutOf(t, 1000,
		`<div id="w"><div id="a"></div><div id="b"></div><div id="c"></div></div>`, css)

	w := find(t, root, "w")
	px(t, "#b's top after clearing #a", relY(t, find(t, root, "b"), w), 50)
	c := find(t, root, "c")
	px(t, "#c's top", relY(t, c, w), 50)
	px(t, "#c's left", relX(t, c, w), 100)
}

// TestFloatDoesNotCollapseMarginsWithAnything pins the two halves of §8.3.1 that
// concern a float, which pull in opposite directions and are easy to get half
// right.
//
// Inwards: a float establishes a formatting context, so its first child's top
// margin stays inside it rather than escaping through its top edge. The float is
// therefore 20 + 10 = 30 tall and its child sits 20 below its own top.
//
// Outwards: the float is not in the flow, so the margin between the blocks
// either side of it collapses through it as though it were not there. #a is 10
// tall with a 20px bottom margin and #b has a 30px top margin, so #b sits at
// 10 + max(20, 30) = 40 — exactly where it would be with no float present.
//
// The float itself goes at the flow position with the pending margin applied but
// not consumed: 10 + 20 = 30.
func TestFloatDoesNotCollapseMarginsWithAnything(t *testing.T) {
	css := noDefaults + `
	#a { height: 10px; margin-bottom: 20px }
	#f { float: left; width: 50px }
	#f p { margin-top: 20px; height: 10px }
	#b { height: 10px; margin-top: 30px }`
	root := layoutOf(t, 1000,
		`<section id="w"><div id="a"></div><div id="f"><p id="in"></p></div>`+
			`<div id="b"></div></section>`, css)

	w, f := find(t, root, "w"), find(t, root, "f")
	px(t, "the float's height", f.BorderRect.H, 30)
	px(t, "the float child's top inside it", relY(t, find(t, root, "in"), f), 20)
	px(t, "the float's own top", relY(t, f, w), 30)
	px(t, "the block after the float", relY(t, find(t, root, "b"), w), 40)
}

// TestLineBoxesShortenBesideAFloat is the half of §9.5 that makes floats worth
// having, and the half an engine can quietly not do.
//
// The containing block is 200px and the float is 100x50 with a 25px line height,
// so the first two lines span y 0 to 25 and 25 to 50 and both overlap the float;
// the third spans 50 to 75, which begins at the float's bottom edge and is
// therefore clear of it. §9.5 shortens a line to the band over the line box's
// own extent — see bandOver, and floatextent_test.go for the case where that
// differs from the band at its top edge — so the first two are 100px wide
// starting at x=100 and the rest are the full 200.
//
// The text follows from that and is asserted as well as the geometry, because
// the geometry alone would be satisfied by a line box that was narrowed and then
// filled as though it were not. At 20px Helvetica "aaaa" is 44.48px and a space
// is 5.56px: two words need 94.52px and fit in 100; three need 144.56 and do
// not. Four words need 194.6px and fit in 200; five need 244.64 and do not.
func TestLineBoxesShortenBesideAFloat(t *testing.T) {
	css := noDefaults + `
	#w { width: 200px }
	#f { float: left; width: 100px; height: 50px }
	#p { font-family: Helvetica; font-size: 20px; line-height: 25px }`
	root := layoutOf(t, 1000,
		`<div id="w"><div id="f"></div><p id="p">aaaa aaaa aaaa aaaa aaaa aaaa aaaa aaaa aaaa aaaa</p></div>`,
		css)

	if got, want := measured(t, "aaaa", 20), 44.48; !nearly(got, want) {
		t.Fatalf("Helvetica measures \"aaaa\" at 20px as %.4f, want %.4f — the "+
			"arithmetic in this test is derived from that number", got, want)
	}

	lines := linesOf(t, root, "p")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4: %v", len(lines), lineTexts(lines))
	}
	for i, want := range []struct{ x, w, y float64 }{
		{100, 100, 0},
		{100, 100, 25},
		{0, 200, 50},
		{0, 200, 75},
	} {
		px(t, "line "+string(rune('0'+i))+"'s left", lines[i].Rect.X, want.x)
		px(t, "line "+string(rune('0'+i))+"'s width", lines[i].Rect.W, want.w)
		px(t, "line "+string(rune('0'+i))+"'s top", lines[i].Rect.Y, want.y)
	}

	want := []string{"aaaa aaaa", "aaaa aaaa", "aaaa aaaa aaaa aaaa", "aaaa aaaa"}
	if got := lineTexts(lines); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("the lines read %v, want %v", got, want)
	}
}

// TestALineWithNoRoomIsPushedDown pins the rule §9.5 gives for a band too narrow
// to hold anything: the line box moves down rather than being clipped to
// nothing.
//
// A 150px left float and a 40px right float in a 200px block leave a band from
// 150 to 160 — ten pixels, and the narrowest word here is 44.48px. Both floats
// end at y=40, so the first line that can hold anything starts there and is the
// full width.
//
// Without the rule the text would be squeezed into a 10px line, reported as an
// unbreakable overflow, and drawn on top of both floats: a page with the words
// in it that a reader cannot read.
func TestALineWithNoRoomIsPushedDown(t *testing.T) {
	css := noDefaults + `
	#w { width: 200px }
	#l { float: left; width: 150px; height: 40px }
	#r { float: right; width: 40px; height: 40px }
	#p { font-family: Helvetica; font-size: 20px; line-height: 25px }`
	root := layoutOf(t, 1000,
		`<div id="w"><div id="l"></div><div id="r"></div><p id="p">aaaa aaaa</p></div>`,
		css)

	lines := linesOf(t, root, "p")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %v", len(lines), lineTexts(lines))
	}
	px(t, "the pushed line's top", lines[0].Rect.Y, 40)
	px(t, "the pushed line's left", lines[0].Rect.X, 0)
	px(t, "the pushed line's width", lines[0].Rect.W, 200)
}

// TestFloatInsideAParagraphFlowsWithItsLines pins that a float written among
// inline content is placed against the line it appears on rather than hoisted
// out of the paragraph.
//
// It also pins the box-tree half of the rule: a float among inline content must
// not trigger an anonymous block box, because that would put the float and the
// text it is meant to flow around into two different formatting contexts. If it
// did, the paragraph would have no lines at all and the assertion below would
// find none.
func TestFloatInsideAParagraphFlowsWithItsLines(t *testing.T) {
	css := noDefaults + `
	#p { width: 200px; font-family: Helvetica; font-size: 20px; line-height: 25px }
	#f { float: left; width: 100px; height: 50px }`
	root := layoutOf(t, 1000,
		`<p id="p"><span id="f"></span>aaaa aaaa aaaa aaaa</p>`, css)

	p := find(t, root, "p")
	f := find(t, root, "f")
	px(t, "the float's top", relY(t, f, p), 0)
	px(t, "the float's left", relX(t, f, p), 0)
	// §9.7: float blockifies an inline box, so the span has a real width and
	// height rather than being measured as text.
	px(t, "the blockified span's width", f.BorderRect.W, 100)

	lines := p.Lines
	if len(lines) < 2 {
		t.Fatalf("got %d lines, want at least 2: %v", len(lines), lineTexts(lines))
	}
	px(t, "the first line's left", lines[0].Rect.X, 100)
	px(t, "the first line's width", lines[0].Rect.W, 100)
}

// TestClearMovesPastTheFloatsItNames pins §9.5.2. Each case names the float
// bottom the box must get past, and the ones that name none must not move.
func TestClearMovesPastTheFloatsItNames(t *testing.T) {
	cases := []struct {
		clear string
		wantY float64
	}{
		// The left float ends at 60 and the right one at 90.
		{"none", 0},
		{"left", 60},
		{"right", 90},
		{"both", 90},
	}
	for _, tc := range cases {
		css := noDefaults + `
		#w { width: 400px }
		#l { float: left; width: 100px; height: 60px }
		#r { float: right; width: 100px; height: 90px }
		#c { height: 10px; clear: ` + tc.clear + ` }`
		root := layoutOf(t, 1000,
			`<div id="w"><div id="l"></div><div id="r"></div><div id="c"></div></div>`, css)

		w := find(t, root, "w")
		px(t, "clear:"+tc.clear, relY(t, find(t, root, "c"), w), tc.wantY)
	}
}

// TestClearanceIsADifferenceNotAPosition pins the half of §9.5.2 that an
// implementation reaching for the float bottom directly would get backwards.
//
// Clearance is the amount needed to get *past* the floats, and it is zero for a
// box that was already past them. Here #a is 200px tall, so the box after it
// starts at 200 — well below the float's bottom of 60 — and "clear: left" must
// leave it exactly where it was rather than pulling it up to 60.
func TestClearanceIsADifferenceNotAPosition(t *testing.T) {
	css := noDefaults + `
	#w { width: 400px }
	#f { float: left; width: 100px; height: 60px }
	#a { height: 200px }
	#c { height: 10px; clear: left }`
	root := layoutOf(t, 1000,
		`<div id="w"><div id="f"></div><div id="a"></div><div id="c"></div></div>`, css)

	w := find(t, root, "w")
	px(t, "a box already below the float", relY(t, find(t, root, "c"), w), 200)
}

// TestClearanceIsMeasuredAfterMarginsCollapse pins the order the two rules apply
// in: §9.5.2 computes clearance against the position the box would have had,
// which is the position its collapsed margins put it at.
//
// #a is 10 tall with a 20px bottom margin and #c has a 30px top margin, so
// without any float #c would sit at 10 + max(20, 30) = 40. The float ends at 25,
// which is above that, so there is no clearance and #c stays at 40. A float
// ending at 100 would give clearance of 60 and put it at 100.
func TestClearanceIsMeasuredAfterMarginsCollapse(t *testing.T) {
	for _, tc := range []struct{ floatHeight, wantY float64 }{
		{25, 40},
		{100, 100},
	} {
		css := noDefaults + `
		#a { height: 10px; margin-bottom: 20px }
		#f { float: left; width: 50px; height: ` + trim(tc.floatHeight) + `px }
		#c { height: 10px; margin-top: 30px; clear: left }`
		root := layoutOf(t, 1000,
			`<section id="w"><div id="f"></div><div id="a"></div><div id="c"></div></section>`, css)

		w := find(t, root, "w")
		px(t, "clearance against a float ending at "+trim(tc.floatHeight),
			relY(t, find(t, root, "c"), w), tc.wantY)
	}
}

// TestClearanceSeparatesTheTopMarginFromTheParents pins the entry CSS 2.1 §8.3.1
// puts in its list of what stops two margins being adjoining: "no line boxes, no
// clearance, no padding and no border separate them".
//
// The wrapper has no border and no padding, so an ordinary first child's top
// margin escapes through its top edge and moves the wrapper rather than the
// child. A cleared child's does not, and the difference is visible in two
// places at once: where the wrapper starts, and where the child ends up.
//
// The arithmetic. One float, 100 tall, and one child with a 50px top margin. The
// only difference between the two cases is the child's clear property.
//
//   - With "clear: none" nothing separates the two margins, so the 50 escapes:
//     the wrapper's border top is at 50 and the child's is at 50 too, flush with
//     it and overlapping the float, which is what an ordinary block does.
//   - With "clear: left" the clearance separates them, so the 50 is trapped
//     inside: the wrapper stays at 0 and the child lands at 100, the float's
//     bottom edge.
//
// Getting this wrong is not a small error, and the shape it takes is the reason
// it is worth a test of its own: the escaped margin moves the wrapper *up the
// page* while the child stays pinned to the float, so the two separate by
// exactly the margin. The suite's clear-on-parent-with-margins uses a
// "margin-top: -1000px" grandchild and the wrapper ends up eight hundred pixels
// above the top of the document.
func TestClearanceSeparatesTheTopMarginFromTheParents(t *testing.T) {
	for _, tc := range []struct {
		what, clear           string
		wantWrapperY, wantCsY float64
	}{
		{"clear: none", "none", 50, 50},
		{"clear: left", "left", 0, 100},
	} {
		css := noDefaults + `
		#f { float: left; width: 50px; height: 100px }
		#c { clear: ` + tc.clear + `; margin-top: 50px; height: 20px }`
		root := layoutOf(t, 1000,
			`<section id="w"><div id="f"></div><div id="c"></div></section>`, css)

		w, c := find(t, root, "w"), find(t, root, "c")
		px(t, tc.what+": the wrapper's top", w.BorderRect.Y, tc.wantWrapperY)
		px(t, tc.what+": the cleared box's top", c.BorderRect.Y, tc.wantCsY)
	}
}

// TestABoxThatMakesItsOwnContextDoesNotOverlapAFloat pins the second rule of
// §9.5, the one an engine can omit without the page looking broken: "the border
// box of a table, a block-level replaced element, or an element in the normal
// flow that establishes a new block formatting context ... must not overlap the
// margin box of any floats in the same block formatting context".
//
// Three answers are needed for the rule to have been implemented rather than
// approximated, and each is the wrong answer for the other two:
//
//   - An ordinary block *does* overlap the float. Only its line boxes are
//     shortened, and an engine that moved every block would put a gap beside
//     every float where the specification puts running text. This is the case
//     that would break if the rule were applied too widely, and it is the
//     common one.
//   - A context root with an auto width is narrowed to the band and put beside
//     the float: the specification's "they may even make the border box of said
//     element narrower". 300 minus the float's 200 is 100.
//   - A context root with a declared width that will not fit is dropped below
//     the float instead, keeping its width: "implementations should clear the
//     said element by placing it below any preceding floats". 150 does not fit
//     in 100, so it goes to y=100 at the full 300-wide band.
func TestABoxThatMakesItsOwnContextDoesNotOverlapAFloat(t *testing.T) {
	for _, tc := range []struct {
		what, extra            string
		wantX, wantY, wantWide float64
	}{
		{"an ordinary block", "", 0, 0, 300},
		{"an auto-width context root", "overflow-x: hidden; overflow-y: hidden", 200, 0, 100},
		{"a context root too wide for the band",
			"overflow-x: hidden; overflow-y: hidden; width: 150px", 0, 100, 150},
	} {
		css := noDefaults + `
		#w { width: 300px }
		#f { float: left; width: 200px; height: 100px }
		#n { height: 20px; ` + tc.extra + ` }`
		root := layoutOf(t, 1000,
			`<div id="w"><div id="f"></div><div id="n"></div></div>`, css)

		w, n := find(t, root, "w"), find(t, root, "n")
		px(t, tc.what+"'s left", relX(t, n, w), tc.wantX)
		px(t, tc.what+"'s top", relY(t, n, w), tc.wantY)
		px(t, tc.what+"'s width", n.BorderRect.W, tc.wantWide)
	}
}

// TestABlockLevelImageDoesNotOverlapAFloat is §9.5's third named kind, and it is
// tested separately because it is the one that reaches the rule by a different
// route: a block-level replaced element establishes no formatting context at
// all, so the clause that admits it is its own.
//
// The picture is 40 × 20 and is given "width: 150px", which with the ratio kept
// makes it 150 × 75. That does not fit in the 100px band beside the 200px float,
// so it goes below the float, at x=0 and y=100, exactly as the declared-width
// context root above does. An engine that admitted only formatting-context roots
// would leave it at the top, over the float.
func TestABlockLevelImageDoesNotOverlapAFloat(t *testing.T) {
	css := noDefaults + `
	#w { width: 300px }
	#f { float: left; width: 200px; height: 100px }
	#i { display: block; width: 150px }`
	root := replacedLayout(t, 1000,
		`<div id="w"><div id="f"></div><img id="i" src="wide.png" alt=""></div>`, css)

	w, i := find(t, root, "w"), find(t, root, "i")
	px(t, "the image's width", i.BorderRect.W, 150)
	px(t, "the image's left", relX(t, i, w), 0)
	px(t, "the image's top", relY(t, i, w), 100)
}

// TestAContextRootBesideARightFloatKeepsItsLeftEdge is the mirror, and it is
// here because the two are not the same assertion: a left float moves the box
// sideways *and* narrows it, a right float only narrows it. Each catches a
// defect the other does not, which was checked by planting both — measuring the
// width from the containing block's far edge rather than from the band's is
// invisible beside a left float and halves the box beside a right one, and
// narrowing without shifting is invisible here and leaves the box under the
// float there.
func TestAContextRootBesideARightFloatKeepsItsLeftEdge(t *testing.T) {
	css := noDefaults + `
	#w { width: 300px }
	#f { float: right; width: 200px; height: 100px }
	#n { height: 20px; overflow-x: hidden; overflow-y: hidden }`
	root := layoutOf(t, 1000,
		`<div id="w"><div id="f"></div><div id="n"></div></div>`, css)

	w, n := find(t, root, "w"), find(t, root, "n")
	px(t, "the context root's left", relX(t, n, w), 0)
	px(t, "the context root's top", relY(t, n, w), 0)
	px(t, "the context root's width", n.BorderRect.W, 100)
}

// TestFormattingContextRootContainsItsFloats pins §10.6.7, and the plain block
// beside it pins that the rule is specific to a formatting-context root.
//
// The difference between the two is the entire reason "overflow: hidden" is the
// idiom for containing a float, so a test that only checked the containing half
// would pass on an engine that made every block do it — which would be wrong in
// the far commoner direction.
func TestFormattingContextRootContainsItsFloats(t *testing.T) {
	for _, tc := range []struct {
		what, extra string
		wantHeight  float64
	}{
		{"a plain block", "", 20},
		{"overflow: hidden", "overflow-x: hidden; overflow-y: hidden", 60},
		{"display: flow-root", "display: flow-root", 60},
	} {
		css := noDefaults + `
		#w { ` + tc.extra + ` }
		#f { float: left; width: 100px; height: 60px }
		#b { height: 20px }`
		root := layoutOf(t, 1000,
			`<section id="outer"><div id="w"><div id="f"></div><div id="b"></div></div></section>`, css)

		px(t, "the height of "+tc.what, find(t, root, "w").BorderRect.H, tc.wantHeight)
	}
}

// TestAFloatContainsItsOwnFloats pins that a float is a formatting context root
// too, which is easy to leave out because nothing about a float says "overflow".
//
// The outer float has no height of its own and holds a 40px float, so §10.6.7
// makes it 40 tall. If floats escaped it, it would be zero.
func TestAFloatContainsItsOwnFloats(t *testing.T) {
	css := noDefaults + `
	#outer { float: left; width: 200px }
	#inner { float: left; width: 50px; height: 40px }`
	root := layoutOf(t, 1000, `<div id="outer"><div id="inner"></div></div>`, css)

	px(t, "the outer float's height", find(t, root, "outer").BorderRect.H, 40)
}

// TestFloatWidthShrinksToFit pins §10.3.5, which is the difference between a
// float and a block and is what leaves room beside one.
//
// The formula is min(max(preferred minimum, available), preferred). At 100px
// Helvetica "aa" is 111.2px and "aa aa" is 250.2px, so the preferred minimum is
// 111.2 and the preferred is 250.2. The three cases put the available width
// above, between and below them, which is the only way to tell the formula apart
// from either of the two numbers on its own.
func TestFloatWidthShrinksToFit(t *testing.T) {
	if got, want := measured(t, "aa", 100), 111.2; !nearly(got, want) {
		t.Fatalf("Helvetica measures \"aa\" at 100px as %.4f, want %.4f", got, want)
	}
	if got, want := measured(t, "aa aa", 100), 250.2; !nearly(got, want) {
		t.Fatalf("Helvetica measures \"aa aa\" at 100px as %.4f, want %.4f", got, want)
	}

	for _, tc := range []struct{ available, want float64 }{
		{1000, 250.2}, // room to spare: the preferred width
		{150, 150},    // between the two: the available width
		{50, 111.2},   // less than the minimum: the minimum, and it overflows
	} {
		css := noDefaults + `
		#w { width: ` + trim(tc.available) + `px }
		#f { float: left; font-family: Helvetica; font-size: 100px }`
		root := layoutOf(t, 2000, `<div id="w"><div id="f">aa aa</div></div>`, css)

		px(t, "shrink-to-fit in "+trim(tc.available)+"px",
			find(t, root, "f").BorderRect.W, tc.want)
	}

	// A declared width wins outright: shrink-to-fit is what "auto" means, not a
	// cap on what the author asked for.
	root := layoutOf(t, 2000, `<div id="w"><div id="f">aa aa</div></div>`,
		noDefaults+`#w { width: 1000px }
		#f { float: left; width: 400px; font-family: Helvetica; font-size: 100px }`)
	px(t, "a declared float width", find(t, root, "f").BorderRect.W, 400)
}

// TestFloatPositionSurvivesAMarginItCannotSeeYet is about the mechanism rather
// than about CSS, and it is here because the mechanism is where this
// implementation could be silently wrong.
//
// A box's collapsed top margin is not known until its subtree has been walked,
// because a descendant's margin can escape through its top edge — but the floats
// inside that subtree have to be placed *during* the walk, against a position
// that is therefore predicted. This document makes the prediction wrong on
// purpose and checks the correction.
//
// #w has a top border, so nothing escapes it. #n has no margin of its own, so
// the prediction for it is 0. Its grandchild #m has a 40px top margin which
// escapes through #n's open top edge, so #n really sits at 40 — and the float
// inside #m, which was placed while #n was being laid out, belongs at 40 rather
// than at 0. Nothing in that subtree ever read the float geometry, so the cheap
// repair applies: the float is translated. #c then clears to 40 + 30 = 70.
func TestFloatPositionSurvivesAMarginItCannotSeeYet(t *testing.T) {
	css := noDefaults + `
	#w { border-top-style: solid; border-top-width: 5px }
	#m { margin-top: 40px; height: 30px }
	#f { float: left; width: 50px; height: 30px }
	#c { height: 10px; clear: left }`
	root := layoutOf(t, 1000,
		`<div id="w"><div id="n"><div id="m"><div id="f"></div></div></div>`+
			`<div id="c"></div></div>`, css)

	w := find(t, root, "w")
	px(t, "the block whose margin escaped", relY(t, find(t, root, "n"), w), 40)

	f := find(t, root, "f")
	px(t, "the float's top", relY(t, f, w), 40)
	px(t, "the float's left", relX(t, f, w), 0)

	// The clearing box has to get past the corrected bottom, not the predicted
	// one. A translation that never happened would put it at 30.
	px(t, "the clearing box", relY(t, find(t, root, "c"), w), 70)
}

// relayoutSource is a document where the prediction above is wrong *and* the
// subtree read the float geometry, so translating it cannot repair it.
//
// #w has a top border, so nothing escapes it, and it holds a float from 0 to 50.
// #n has no margin of its own, so it is predicted at 0 — where its text would
// run beside that float in a 100px band. Its child #m has a 60px top margin
// which escapes through #n's open top edge, so #n really sits at 60, below the
// float, where the same text has the full 200px.
//
// The two answers differ in the line's width and in how many lines there are,
// and no amount of moving the finished subtree turns one into the other. That is
// the whole difference between the two repairs.
const relayoutSource = `<div id="w"><div id="f"></div><div id="n"><div id="m">` +
	`aaaa aaaa aaaa aaaa</div></div></div>`

const relayoutCSS = `
	#w { border-top-style: solid; border-top-width: 5px; width: 200px }
	#f { float: left; width: 100px; height: 50px }
	#m { margin-top: 60px; font-family: Helvetica; font-size: 20px; line-height: 25px }`

// TestSubtreeThatReadFloatsIsLaidOutAgain pins the expensive repair.
//
// At 20px Helvetica four "aaaa" and three spaces are 4×44.48 + 3×5.56 = 194.6px,
// which fits on one 200px line and does not fit on one 100px line.
func TestSubtreeThatReadFloatsIsLaidOutAgain(t *testing.T) {
	root := layoutOf(t, 1000, relayoutSource, noDefaults+relayoutCSS)

	w := find(t, root, "w")
	px(t, "the block whose margin escaped", relY(t, find(t, root, "n"), w), 60)

	lines := linesOf(t, root, "m")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %v — the text was broken against the "+
			"band beside the float rather than the one below it",
			len(lines), lineTexts(lines))
	}
	px(t, "the line's left", lines[0].Rect.X, 0)
	px(t, "the line's width", lines[0].Rect.W, 200)
}

// TestRelayoutBudgetIsSeenToFire pins the bound on the correction above.
//
// A cap that has only ever been observed not to trip is one nobody knows works.
// Lowering it to zero forces the cheap repair on a document that needed the
// expensive one, and the render has to say so rather than quietly produce
// geometry it knows is stale.
func TestRelayoutBudgetIsSeenToFire(t *testing.T) {
	src, css := relayoutSource, noDefaults+relayoutCSS

	built := Build(Input{HTML: src, CSS: []Stylesheet{{Source: css}}})
	if built.Root == nil {
		t.Fatal("the document produced no boxes")
	}

	saved := maxRelayouts
	maxRelayouts = 0
	defer func() { maxRelayouts = saved }()

	rec := NewRecorder(nil)
	w, _ := style.FromPx(1000)
	h, _ := style.FromPx(10000)
	if frag := Layout(built.Root, Size{W: w, H: h}, nil, rec); frag == nil {
		t.Fatal("layout produced no fragment")
	}
	fired[RuleLimit] = true
	if rec.Count(RuleLimit) == 0 {
		t.Error("the relayout budget was exhausted and the render did not say so")
	}
}

// TestFloatAndClearAreImplementedProperties pins that the two declarations are
// applied rather than reported as unsupported.
//
// §6.3 makes an unapplied declaration a finding, so a property missing from the
// registry would be *reported* rather than silently dropped — but it would also
// keep every document using floats out of the clean-pass count for ever, and the
// registry is the only place that distinguishes the two.
func TestFloatAndClearAreImplementedProperties(t *testing.T) {
	got := Build(Input{HTML: `<div id="a"></div>`,
		CSS: []Stylesheet{{Source: "#a { float: left; clear: both }"}}})
	for _, f := range got.Findings {
		if f.Property == "float" || f.Property == "clear" {
			t.Errorf("%q is reported as not implemented: %v", f.Property, f)
		}
	}
}

// TestFloatDoesNotSplitTheInlineItIsIn pins the box-tree rule that §9.2.1.1 is
// *not* about a float.
//
// An inline box holding a block-level box is split around it: the <em> becomes
// two inline boxes with the block between them, both keeping the em's style, so
// a border on it is drawn twice. A float is block-level after §9.7 blockifies
// it, and an engine that applied the splitting rule to it would break every
// paragraph containing a floated image into three boxes.
//
// The assertion is on the box tree rather than on the geometry, and that is not
// a shortcut — it is the only place the rule shows. Inline layout flattens the
// pieces back into one sequence of runs, so a split <em> lays out identically
// today; it becomes visible the moment an inline box's own border or background
// is painted. A test written against the lines would have claimed coverage it
// did not have, which was measured rather than assumed: the split was planted
// and every geometric assertion in this file still passed.
//
// The second case is the one that makes both halves of the rule load-bearing.
// With a real block inside the <em> as well, the split *does* happen — and the
// float has to stay inside the piece it was written in rather than being lifted
// out alongside the block.
func TestFloatDoesNotSplitTheInlineItIsIn(t *testing.T) {
	css := `#f { float: left; width: 40px; height: 10px }`

	for _, tc := range []struct {
		what, src string
		wantEms   int
	}{
		{"a float alone in an inline",
			`<p><em>one <span id="f"></span>two</em></p>`, 1},
		// A block-level box in the flow does split it, into the piece before and
		// the piece after — and the float belongs to whichever piece it was
		// written in rather than becoming a third sibling.
		{"a float beside a real block in an inline",
			`<p><em>one <span id="f"></span>two<div>block</div>three</em></p>`, 2},
	} {
		got := Build(Input{HTML: tc.what + tc.src, CSS: []Stylesheet{{Source: noDefaults + css}}})
		if got.Root == nil {
			t.Fatalf("%s: the document produced no boxes", tc.what)
		}
		if n := boxesFor(got.Root, "em"); n != tc.wantEms {
			t.Errorf("%s: the <em> produced %d boxes, want %d", tc.what, n, tc.wantEms)
		}
	}

	// And the words are still one line with a space between them, which is what
	// the reader sees when the rule is right.
	root := layoutOf(t, 1000, `<p id="p"><em>one <span id="f"></span>two</em></p>`,
		noDefaults+css+`
		#p { width: 1000px; font-family: Helvetica; font-size: 20px }`)
	lines := linesOf(t, root, "p")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %v", len(lines), lineTexts(lines))
	}
	if got := lineTexts(lines)[0]; got != "one two" {
		t.Errorf("the line reads %q, want %q", got, "one two")
	}
}

// boxesFor counts the boxes an element generated, which is what the
// inline-splitting rule is about.
func boxesFor(b *Box, name string) int {
	n := 0
	if b.Element != nil && strings.EqualFold(b.Element.Name, name) && !b.IsText() {
		n++
	}
	for _, c := range b.Children {
		n += boxesFor(c, name)
	}
	return n
}

// TestFloatContextDoesNotEscapeItsRoot pins that a float inside a formatting
// context root is invisible to the lines outside it.
//
// The float is 500px wide inside a wrapper with overflow hidden, and the
// paragraph after the wrapper is a full 200px line. If the contexts were shared,
// the paragraph's first line would have been shortened by a float that is not in
// its formatting context at all.
func TestFloatContextDoesNotEscapeItsRoot(t *testing.T) {
	css := noDefaults + `
	#outer { width: 200px }
	#w { overflow-x: hidden; overflow-y: hidden }
	#f { float: left; width: 150px; height: 100px }
	#p { font-family: Helvetica; font-size: 20px; line-height: 25px }`
	root := layoutOf(t, 1000,
		`<div id="outer"><div id="w"><div id="f"></div></div><p id="p">aaaa</p></div>`, css)

	// §10.6.7 again: the wrapper is as tall as the float.
	px(t, "the wrapper's height", find(t, root, "w").BorderRect.H, 100)

	lines := linesOf(t, root, "p")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %v", len(lines), lineTexts(lines))
	}
	px(t, "the line outside the context", lines[0].Rect.X, 0)
	px(t, "the line's width", lines[0].Rect.W, 200)
	// And it starts below the wrapper rather than beside the float.
	px(t, "the paragraph's top", relY(t, find(t, root, "p"), find(t, root, "outer")), 100)
}

// TestFloatBandsAreQueriedAtTheLineTop pins the shape of the query, which is a
// point rather than a range.
//
// The float starts 30px down and the line height is 25px, so the first line box
// spans 0 to 25 and the second 25 to 50. The float overlaps the second and not
// the first; a query over the line's whole height would also shorten the first,
// which is not what §9.5 says and not what a browser does.
func TestFloatBandsAreQueriedAtTheLineTop(t *testing.T) {
	css := noDefaults + `
	#w { width: 200px }
	#spacer { height: 30px }
	#f { float: left; width: 100px; height: 100px }
	#p { font-family: Helvetica; font-size: 20px; line-height: 25px }`
	root := layoutOf(t, 1000,
		`<div id="w"><div id="spacer"></div><div id="f"></div>`+
			`<p id="p">aaaa aaaa aaaa aaaa</p></div>`, css)

	// The paragraph starts at the top of the flow, because the float is out of
	// it and the spacer is 30 tall.
	lines := linesOf(t, root, "p")
	if len(lines) < 2 {
		t.Fatalf("got %d lines, want at least 2: %v", len(lines), lineTexts(lines))
	}
	// The paragraph's own content box starts 30 below the containing block, so
	// its first line is at formatting-context y=30 — where the float begins.
	px(t, "the first line's left", lines[0].Rect.X, 100)
	px(t, "the second line's left", lines[1].Rect.X, 100)
}

// TestABlockHoldingOnlyAFloatCollapsesThrough pins §9.7's blockification, by way
// of the one thing in this engine that can tell an inline-level box from a
// block-level one once the float machinery has taken over.
//
// §8.3.1 lets a box's own two margins collapse through it when it has no in-flow
// content, and a float is not in-flow content — so #mid, whose only child is a
// floated <span>, collapses through and the margins either side of it meet:
// 10 + max(20, 30) = 40.
//
// The <span> is deliberate. Without §9.7 turning it into a block-level box, #mid
// would be an inline formatting context that produced no lines, which counts as
// having been laid out and stops the collapse — putting #b at 10 + 20 + 30 = 60.
// Every other consequence of blockification is masked, because the rest of this
// engine keys off the float property directly.
func TestABlockHoldingOnlyAFloatCollapsesThrough(t *testing.T) {
	css := noDefaults + `
	#a { height: 10px; margin-bottom: 20px }
	#f { float: left; width: 50px; height: 15px }
	#b { height: 10px; margin-top: 30px }`
	root := layoutOf(t, 1000,
		`<section id="w"><div id="a"></div><div id="mid"><span id="f"></span></div>`+
			`<div id="b"></div></section>`, css)

	w := find(t, root, "w")
	px(t, "the block after one holding only a float", relY(t, find(t, root, "b"), w), 40)
	// And the float is still placed, at the flow position the pending margin
	// puts it at: 10 + 20.
	px(t, "the float's top", relY(t, find(t, root, "f"), w), 30)
}

// TestAWideFloatMetPartWayAlongALineGoesBelowIt pins §9.5.1 rule 9 as browsers
// apply it: a float written after some words on a line sits beside them when
// there is room, and starts the next line down when there is not.
//
// The line is 200px. "aaaa" is 44.48px and a space 5.56px, so 50.04px is used
// when the float is reached and 149.96px is left — less than the float's 180px.
// The float therefore drops by one line height, to 25.
func TestAWideFloatMetPartWayAlongALineGoesBelowIt(t *testing.T) {
	css := noDefaults + `
	#p { width: 200px; font-family: Helvetica; font-size: 20px; line-height: 25px }
	#wide { float: left; width: 180px; height: 30px }
	#narrow { float: left; width: 100px; height: 30px }`

	root := layoutOf(t, 1000,
		`<p id="p">aaaa <span id="wide"></span>aaaa aaaa</p>`, css)
	px(t, "a float too wide for the rest of its line",
		relY(t, find(t, root, "wide"), find(t, root, "p")), 25)

	// A float that does fit stays on the line it was written on, which is what
	// makes the case above a rule rather than a habit of dropping everything.
	root = layoutOf(t, 1000,
		`<p id="p">aaaa <span id="narrow"></span>aaaa aaaa</p>`, css)
	px(t, "a float that fits beside the rest of its line",
		relY(t, find(t, root, "narrow"), find(t, root, "p")), 0)
}

// TestAnonymousBlockHasNoBoxModelOfItsOwn pins what "anonymous" means, and it is
// here because floats are what made the consequence visible.
//
// An anonymous block box inherits and has nothing else: no margin, no border, no
// padding, no background. The obvious shortcut — giving it its parent's whole
// computed style — makes it a copy of the parent's box model, so the anonymous
// block wrapped around the text in <body> takes body's own 8px margin a second
// time and every position below it is out by that much.
//
// #w has a 20px margin, a 5px border and 7px of padding, so its content box is
// 1000 - 40 - 10 - 14 = 936 wide. The anonymous block around "text" must fill it
// exactly, starting at its top left corner.
func TestAnonymousBlockHasNoBoxModelOfItsOwn(t *testing.T) {
	css := noDefaults + `
	#w { margin: 20px; padding: 7px;
	     border-top-style: solid; border-right-style: solid;
	     border-bottom-style: solid; border-left-style: solid;
	     border-top-width: 5px; border-right-width: 5px;
	     border-bottom-width: 5px; border-left-width: 5px }
	#b { height: 10px }`
	root := layoutOf(t, 1000,
		`<section id="w">text<div id="b"></div></section>`, css)

	w := find(t, root, "w")
	if len(w.Children) == 0 {
		t.Fatal("the text produced no anonymous block")
	}
	anon := w.Children[0]
	if anon.Box.Element != nil {
		t.Fatalf("the first child is %q, not the anonymous block",
			anon.Box.Element.Name)
	}
	px(t, "the anonymous block's left", relX(t, anon, w), 0)
	px(t, "the anonymous block's top", relY(t, anon, w), 0)
	px(t, "the anonymous block's width", anon.BorderRect.W, 1000-40-10-14)
	if e := anon.Margin.Horizontal().Add(anon.Padding.Horizontal()).
		Add(anon.Border.Horizontal()); e != 0 {
		t.Errorf("the anonymous block has %v of horizontal box model, want none", e)
	}
}

// TestAFloatedRootGoesToItsEdge pins the one thing §9.5 still says about a float
// with no containing block but the page and no siblings to make room for.
//
// Its width already shrinks to fit like any other float, so leaving the position
// alone would honour half of the declaration and drop the visible half.
func TestAFloatedRootGoesToItsEdge(t *testing.T) {
	root := layoutOf(t, 1000, `<div id="a"></div>`,
		noDefaults+"html { float: right; width: 200px }")
	px(t, "a floated root's left edge", root.BorderRect.X, 800)

	root = layoutOf(t, 1000, `<div id="a"></div>`,
		noDefaults+"html { float: left; width: 200px }")
	px(t, "a left-floated root's left edge", root.BorderRect.X, 0)
}

// TestAClearedBoxThatCollapsesThroughLaysItsMarginOnce pins the sentence CSS 2.1
// §9.5.2 ends on, which is the one rule about clearance that is not about where
// the cleared box goes:
//
//	If the top and bottom margins of an element with clearance are adjoining,
//	its margins collapse with the adjoining margins of following siblings but
//	that resulting margin does not collapse with the bottom margin of the parent
//	block.
//
// Two things follow, and an engine can get either one right on its own while
// producing a page that is wrong.
//
// The arithmetic is the suite's margin-collapse-clear-012, which sets it out in
// a comment in its own source. A parent with a top border — so that nothing
// escapes through its top edge and the numbers are the parent's own — holds a
// 100px float, then an empty box with "clear: left", a 40px top margin and an
// 80px bottom margin, then an empty sibling with a 140px bottom margin.
//
//   - The empty box's margins are adjoining, so they collapse into one run. Its
//     top margin puts its hypothetical border edge at 40, the float's bottom is
//     at 100, so clearance takes the edge to 100. The 40 is *under* the edge
//     already: laying the whole run again below it would put the sibling at 240.
//   - The run goes on collecting: the sibling's 140 joins it. 140 - 40 = 100
//     more, so the content ends at 200.
//   - That resulting margin does not collapse with the parent's bottom margin,
//     so it stays inside and the parent is 200 tall rather than 100. This is the
//     half that a bottom edge with a border on it would hide, which is why the
//     second case below takes the border off.
func TestAClearedBoxThatCollapsesThroughLaysItsMarginOnce(t *testing.T) {
	const doc = `<div id="p"><div id="f"></div><div id="c"></div><div id="s"></div></div>`
	css := noDefaults + `
	#p { border-top: 1px solid black; width: 200px }
	#f { float: left; width: 100px; height: 100px }
	#c { clear: left; margin-top: 40px; margin-bottom: 80px }
	#s { margin-bottom: 140px }`

	root := layoutOf(t, 1000, doc, css)
	p := find(t, root, "p")
	px(t, "the cleared box's border edge", relY(t, find(t, root, "c"), p), 100)
	px(t, "the following sibling's border edge", relY(t, find(t, root, "s"), p), 200)
	px(t, "the parent's content height", p.ContentRect().H, 200)

	// The same without the top border, so that the parent's top edge is open.
	// The float is what stops the run leaving through it — the cleared box's own
	// top margin is separated from the parent's by the clearance — so the
	// numbers are unchanged and the parent's border box is one pixel shorter.
	open := noDefaults + `
	#p { width: 200px }
	#f { float: left; width: 100px; height: 100px }
	#c { clear: left; margin-top: 40px; margin-bottom: 80px }
	#s { margin-bottom: 140px }`
	root = layoutOf(t, 1000, doc, open)
	p = find(t, root, "p")
	px(t, "with an open top edge: the cleared box", relY(t, find(t, root, "c"), p), 100)
	px(t, "with an open top edge: the parent's height", p.BorderRect.H, 200)

	// And the case the rule must not reach. With the float only 20px tall the
	// hypothetical position — 40, the collapsed run — is already past it, so
	// there is no clearance at all, every margin collapses through, and the
	// parent has no height of its own. An engine that reached for the float's
	// bottom rather than for a difference would make this 140 instead.
	nofloat := noDefaults + `
	#p { border-top: 1px solid black; width: 200px }
	#f { float: left; width: 100px; height: 20px }
	#c { clear: left; margin-top: 40px; margin-bottom: 80px }
	#s { margin-bottom: 140px }`
	root = layoutOf(t, 1000, doc, nofloat)
	px(t, "no clearance: the parent's content height",
		find(t, root, "p").ContentRect().H, 0)
}

// TestAClearedChildStopsItsParentCollapsingThrough pins the other half of what
// clearance does to §8.3.1, which is that it stops being a question about the
// cleared box and becomes one about the box holding it.
//
// A box's own two margins are adjoining only when it has "no in-flow children",
// and the engine reads that as "nothing inside it that did not itself collapse
// through" — which is what lets an empty box between two paragraphs disappear
// instead of separating them. A child with clearance did not collapse through:
// the clearance separates its own two margins, so it is a real in-flow child and
// its parent has to stay.
//
// Reaching that is harder than it looks, because a clearance is normally height
// and a parent with height cannot collapse through anyway. It takes a negative
// bottom margin that exactly cancels the clearance:
//
//   - #f is 100 tall, so the float's bottom is 100 below #p's content top.
//   - #c collapses through with a run of max(0, -100) = -100, so its
//     hypothetical border edge is 100 *above* that top and the clearance is 200,
//     putting the edge at 100.
//   - The run continues below the edge, so #p's content ends at 100 - 100 = 0
//     and #p has no height at all.
//
// #p's own margins are 30 above and 50 below. If it collapsed through they would
// be one margin of 50 and #b would follow #a by 50; because it does not, they
// are laid in turn and #b follows by 80. The float's own bottom is not what
// separates them: an ordinary <div> does not contain its floats.
func TestAClearedChildStopsItsParentCollapsingThrough(t *testing.T) {
	css := noDefaults + `
	#a { height: 10px }
	#p { margin-top: 30px; margin-bottom: 50px }
	#f { float: left; width: 100px; height: 100px }
	#c { clear: left; margin-bottom: -100px }
	#b { height: 10px }`
	root := layoutOf(t, 1000, `<section id="w"><div id="a"></div>`+
		`<div id="p"><div id="f"></div><div id="c"></div></div>`+
		`<div id="b"></div></section>`, css)

	w := find(t, root, "w")
	px(t, "the parent's border top", relY(t, find(t, root, "p"), w), 40)
	px(t, "the parent's height", find(t, root, "p").BorderRect.H, 0)
	px(t, "the box after it", relY(t, find(t, root, "b"), w), 90)
}

// nearly compares two float widths at a tolerance far below a layout unit, which
// is a 64th of a pixel.
func nearly(a, b float64) bool {
	d := a - b
	return d < 0.001 && d > -0.001
}

// trim renders a number for a stylesheet, so a case table can drive the CSS.
func trim(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
