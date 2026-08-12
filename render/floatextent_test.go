package render

import (
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// §9.5's non-overlap rule, asked about a box rather than about a point.
//
// The rule is one sentence: "the border box of a table, a block-level replaced
// element, or an element in the normal flow that establishes a new block
// formatting context must not overlap the margin box of any floats in the same
// block formatting context as the element itself." Three words in it decide
// everything below and each was once read wrongly here:
//
//   - "border box", not margin box, so what has to fit is the box's own edges
//     and not the space it asks to be left around them;
//   - "overlap", which is a relation between two rectangles and so is about the
//     box's whole height, not about the band at the line it starts on;
//   - "any floats", not "any floats inside the containing block", so a float
//     the containing block does not reach still counts.
//
// Every expected number is derived here from the arithmetic, and each document
// is arranged so that the wrong reading gives a different answer — a test whose
// expectation is also where the old code put the box proves nothing.

// TestBFCBoxAvoidsAFloatThatBeginsBelowItsTop is the case the single-y query
// cannot see.
//
// Two left floats, stacked by "clear: left": the first is 50 wide and 75 tall
// from y=0, the second 100 wide and 75 tall from y=75. A flow root 200 wide and
// 50 tall follows each.
//
//	first root:  y = 0, its height reaches 50, and only the first float is
//	             within that range, so the band is [50, 400] and it goes at 50.
//	second root: y = 50, its height reaches 100, so it spans both floats —
//	             the first from 50 to 75 and the second from 75 to 100 — and
//	             the band is the intersection, [100, 400]. It goes at 100.
//
// Asking at the top edge alone gives 50 for the second box as well, which puts
// a hundred pixels of it over the second float.
func TestBFCBoxAvoidsAFloatThatBeginsBelowItsTop(t *testing.T) {
	css := noDefaults + `
	#w { width: 400px }
	.f { float: left; clear: left }
	.r { overflow: hidden; width: 200px; height: 50px }`
	root := layoutOf(t, 400, `<section id="w">
		<div class="f" id="f1" style="width: 50px; height: 75px"></div>
		<div class="f" id="f2" style="width: 100px; height: 75px"></div>
		<div class="r" id="a"></div>
		<div class="r" id="b"></div>
	</section>`, css)

	w := find(t, root, "w")
	px(t, "the second float's top", relY(t, find(t, root, "f2"), w), 75)

	a, b := find(t, root, "a"), find(t, root, "b")
	px(t, "the first flow root's top", relY(t, a, w), 0)
	px(t, "the first flow root's left", relX(t, a, w), 50)
	px(t, "the second flow root's top", relY(t, b, w), 50)
	px(t, "the second flow root's left", relX(t, b, w), 100)
}

// TestFloatFitBudgetIsSeenToDecide lowers the bound on the correction above to
// zero and requires the box to land where the uncorrected prediction put it.
//
// A cap that has only ever been observed not to trip is one nobody knows works,
// and this one is not a safety net that never sees traffic: the document in
// TestBFCBoxAvoidsAFloatThatBeginsBelowItsTop needs exactly one round of it, so
// removing the budget has to move the box back to 50 — which is also the
// clearest statement of what the correction is worth.
func TestFloatFitBudgetIsSeenToDecide(t *testing.T) {
	saved := maxFloatFits
	maxFloatFits = 0
	defer func() { maxFloatFits = saved }()

	css := noDefaults + `
	#w { width: 400px }
	.f { float: left; clear: left }
	.r { overflow: hidden; width: 200px; height: 50px }`
	root := layoutOf(t, 400, `<section id="w">
		<div class="f" style="width: 50px; height: 75px"></div>
		<div class="f" style="width: 100px; height: 75px"></div>
		<div class="r" id="a"></div>
		<div class="r" id="b"></div>
	</section>`, css)

	w := find(t, root, "w")
	px(t, "the second flow root's left with no budget",
		relX(t, find(t, root, "b"), w), 50)
}

// TestLineFitBudgetIsSeenToDecide is the same for the inline half.
func TestLineFitBudgetIsSeenToDecide(t *testing.T) {
	saved := maxLineFits
	maxLineFits = 0
	defer func() { maxLineFits = saved }()

	css := noDefaults + `
	#w { width: 400px; line-height: 0 }
	.f { float: left; clear: left }
	.b { display: inline-block; vertical-align: top; width: 200px; height: 50px }`
	root := layoutOf(t, 400, `<section id="w"><div class="f"
		style="width: 50px; height: 75px"></div><div class="f"
		style="width: 100px; height: 75px"></div><span class="b"
		></span><span class="b" id="b"></span></section>`, css)

	w := find(t, root, "w")
	px(t, "the second inline-block's left with no budget",
		relX(t, find(t, root, "b"), w), 50)
}

// TestBFCBoxAvoidsAFloatOutsideItsContainingBlock is the case a width
// comparison cannot see.
//
// The wrapper is 100 wide with a right float 50 wide and 50 tall in it, so the
// float occupies x 50 to 100. Inside the wrapper is a plain block with
// "margin-right: 50px", whose content box is therefore x 0 to 50, and inside
// that a flow root with a declared width of 100.
//
// The band, clamped to the 50-wide containing block, is the whole of it — the
// float begins exactly where the block ends — so every width in the arithmetic
// says there is room. The flow root's border box is x 0 to 100 and lies squarely
// on the float, so it belongs below it, at y=50.
func TestBFCBoxAvoidsAFloatOutsideItsContainingBlock(t *testing.T) {
	css := noDefaults + `
	#w { width: 100px }
	#f { float: right; width: 50px; height: 50px }
	#m { margin-right: 50px }
	#r { overflow: hidden; width: 100px; height: 50px }`
	root := layoutOf(t, 400, `<section id="w">
		<div id="f"></div>
		<div id="m"><div id="r"></div></div>
	</section>`, css)

	w, r := find(t, root, "w"), find(t, root, "r")
	px(t, "the flow root's top", relY(t, r, w), 50)
	px(t, "the flow root's width", r.BorderRect.W, 100)
}

// TestBFCBoxKeepsATrailingMarginThatDoesNotFit pins the "border box" half.
//
// The wrapper is 100 wide with a left float 50 wide, leaving a band of 50. The
// flow root declares a width of 50 and a "margin-right: 1px". Its border box
// fills the band exactly, so it belongs beside the float — §10.3.3 is
// over-constrained and resolves it by ignoring the margin-right, which is
// therefore not part of what has to fit.
//
// Counting the margin makes the box need 51 of a 50-wide band and drops it a
// hundred pixels for a pixel nobody can see.
func TestBFCBoxKeepsATrailingMarginThatDoesNotFit(t *testing.T) {
	css := noDefaults + `
	#w { width: 100px }
	#f { float: left; width: 50px; height: 100px }
	#r { overflow: hidden; margin-right: 1px; width: 50px; height: 100px }`
	root := layoutOf(t, 400, `<section id="w">
		<div id="f"></div><div id="r"></div>
	</section>`, css)

	w, r := find(t, root, "w"), find(t, root, "r")
	px(t, "the flow root's top", relY(t, r, w), 0)
	px(t, "the flow root's left", relX(t, r, w), 50)
}

// TestBFCBoxCountsANegativeMarginAsRoom is the same rule read the other way.
//
// A negative margin does not over-constrain anything: it makes §10.3.3's
// equality hold at a *larger* width, so it is room and it counts. The wrapper is
// 50 wide with a right float 25 wide, leaving a band of 25 — and a flow root
// declaring a width of 75 with a "margin-right: -50px" fits that band exactly,
// so it stays beside the float rather than dropping below it.
func TestBFCBoxCountsANegativeMarginAsRoom(t *testing.T) {
	css := noDefaults + `
	#w { width: 50px }
	#f { float: left; width: 25px; height: 50px }
	#r { overflow: hidden; margin-right: -50px; width: 75px; height: 50px }`
	root := layoutOf(t, 400, `<section id="w">
		<div id="f"></div><div id="r"></div>
	</section>`, css)

	w, r := find(t, root, "w"), find(t, root, "r")
	px(t, "the flow root's top", relY(t, r, w), 0)
	px(t, "the flow root's left", relX(t, r, w), 25)
	px(t, "the flow root's width", r.BorderRect.W, 75)
}

// TestBFCBoxWithAnAutoMarginTakesTheBandsSlack pins where the box sits inside
// the band it was fitted to.
//
// §10.3.3's auto margins are resolved against the room the box actually has,
// which beside a float is the band and not the containing block. The wrapper is
// 400 wide with a right float 50 wide, so the band is [0, 350]; a box 200 wide
// with "margin-left: auto" takes the 150 that leaves and ends flush against the
// float.
//
// Treating an auto margin as zero — on the reasoning that a box fitted to a band
// has no slack to give away — leaves the box at the left edge, 150 px from where
// it belongs.
func TestBFCBoxWithAnAutoMarginTakesTheBandsSlack(t *testing.T) {
	css := noDefaults + `
	#w { width: 400px }
	#f { float: right; width: 50px; height: 100px }
	#r { overflow: hidden; width: 200px; height: 50px; margin-left: auto }`
	root := layoutOf(t, 400, `<section id="w">
		<div id="f"></div><div id="r"></div>
	</section>`, css)

	w, r := find(t, root, "w"), find(t, root, "r")
	px(t, "the flow root's top", relY(t, r, w), 0)
	px(t, "the flow root's left", relX(t, r, w), 150)
}

// TestLineBoxIsShortenedByAFloatItReachesDownTo is the inline half of the same
// rule: "line boxes created next to the float are shortened to make room for
// the margin box of the float", where "next to" relates two rectangles.
//
// The floats are the ones above — 50 wide then 100 wide, 75 tall each — and the
// content is two inline-blocks 200 wide and 50 tall. The first line holds one of
// them: at y=0 the band is [50, 400], which is 350 wide and cannot hold two. The
// second line starts at y=50 and is 50 tall, so it reaches to y=100 and meets
// the second float; its band is [100, 400].
//
// Measuring the band at the line's top edge alone puts the second inline-block
// at x=50, over the float.
func TestLineBoxIsShortenedByAFloatItReachesDownTo(t *testing.T) {
	css := noDefaults + `
	#w { width: 400px; line-height: 0 }
	.f { float: left; clear: left }
	.b { display: inline-block; vertical-align: top; width: 200px; height: 50px }`
	root := layoutOf(t, 400, `<section id="w"><div class="f"
		style="width: 50px; height: 75px"></div><div class="f"
		style="width: 100px; height: 75px"></div><span class="b"
		id="a"></span><span class="b" id="b"></span></section>`, css)

	w, a, b := find(t, root, "w"), find(t, root, "a"), find(t, root, "b")
	px(t, "the first inline-block's top", relY(t, a, w), 0)
	px(t, "the first inline-block's left", relX(t, a, w), 50)
	px(t, "the second inline-block's top", relY(t, b, w), 50)
	px(t, "the second inline-block's left", relX(t, b, w), 100)
}

// TestBandOverARangeIsTheIntersection is the geometry on its own, without a
// document, so that the two ends of spansRange are pinned rather than inferred
// from where a box came out.
//
// Three floats in a 400-wide block: a left one from 0 to 100 spanning y 0 to 50,
// a right one from 300 to 400 spanning y 40 to 60, and a left one from 0 to 200
// spanning y 60 to 100.
func TestBandOverARangeIsTheIntersection(t *testing.T) {
	u := func(v float64) style.Unit { r, _ := style.FromPx(v); return r }
	fc := &floatContext{boxes: []placedFloat{
		{rect: Rect{X: u(0), Y: u(0), W: u(100), H: u(50)}, side: FloatLeft},
		{rect: Rect{X: u(300), Y: u(40), W: u(100), H: u(20)}, side: FloatRight},
		{rect: Rect{X: u(0), Y: u(60), W: u(200), H: u(40)}, side: FloatLeft},
	}}

	cases := []struct {
		what             string
		top, bottom      float64
		wantLeft, wantHi float64
	}{
		// An empty range is the single-y question, and a float that begins
		// exactly at that y obstructs it.
		{"a point inside the first float", 0, 0, 100, 400},
		// At y=50 the first float has ended — its range is half-open, so a box
		// starting where it stops is beside nothing — but the right float,
		// which spans 40 to 60, is still there.
		{"a point at the first float's bottom", 50, 50, 0, 300},
		// A range that ends exactly where a float begins does not meet it; one
		// that starts exactly where a float ends does not meet it either.
		{"a range ending at the second float's top", 0, 40, 100, 400},
		{"a range meeting the second float", 0, 41, 100, 300},
		{"a range starting at the first float's bottom", 50, 60, 0, 300},
		// The intersection over a range that meets two left floats is the
		// further of the two.
		{"a range meeting both left floats", 40, 70, 200, 300},
	}
	for _, c := range cases {
		left, right := fc.bandOver(u(c.top), u(c.bottom), u(0), u(400))
		px(t, c.what+": left edge", left, c.wantLeft)
		px(t, c.what+": right edge", right, c.wantHi)
	}
}

// TestFloatOverlapIsExclusiveAtTheEdges pins that touching is not overlapping,
// which is the whole reason a box may sit beside a float at all.
func TestFloatOverlapIsExclusiveAtTheEdges(t *testing.T) {
	u := func(v float64) style.Unit { r, _ := style.FromPx(v); return r }
	fc := &floatContext{boxes: []placedFloat{
		{rect: Rect{X: u(0), Y: u(0), W: u(100), H: u(50)}, side: FloatLeft},
	}}

	cases := []struct {
		what string
		r    Rect
		want bool
	}{
		{"a box beginning at the float's right edge",
			Rect{X: u(100), Y: u(0), W: u(100), H: u(50)}, false},
		{"a box beginning one unit inside it",
			Rect{X: u(100).Sub(1), Y: u(0), W: u(100), H: u(50)}, true},
		{"a box beginning at the float's bottom",
			Rect{X: u(0), Y: u(50), W: u(100), H: u(50)}, false},
		{"a box beginning one unit above it",
			Rect{X: u(0), Y: u(50).Sub(1), W: u(100), H: u(50)}, true},
		{"a box of no height",
			Rect{X: u(0), Y: u(0), W: u(100), H: 0}, false},
		{"a box of no width",
			Rect{X: u(0), Y: u(0), W: 0, H: u(50)}, false},

		// The mirror of the first four. Each of the four comparisons is a
		// separate statement and the cases above reach only two of them: with the
		// float below and to the right of the box it is the other two that decide,
		// and a mutation of either survived the whole suite.
		{"a box ending at the float's top",
			Rect{X: u(0), Y: u(-50), W: u(100), H: u(50)}, false},
		{"a box ending one unit inside it",
			Rect{X: u(0), Y: u(-50).Add(1), W: u(100), H: u(50)}, true},
		{"a box ending at the float's left edge",
			Rect{X: u(-100), Y: u(0), W: u(100), H: u(50)}, false},
		{"a box ending one unit inside it horizontally",
			Rect{X: u(-100).Add(1), Y: u(0), W: u(100), H: u(50)}, true},

		// A box with no extent, placed where the intersection test would
		// otherwise find it. The cases above put it on the float's own edge,
		// where the comparisons answer false anyway and the early return is not
		// what decided — an empty "overflow: hidden" div beside a float is a real
		// thing and must not be pushed below it.
		{"a box of no height inside the float's span",
			Rect{X: u(0), Y: u(25), W: u(100), H: 0}, false},
		{"a box of no width inside the float's span",
			Rect{X: u(25), Y: u(0), W: 0, H: u(50)}, false},
	}
	for _, c := range cases {
		if got := fc.overlaps(c.r); got != c.want {
			t.Errorf("%s: overlaps is %v, want %v", c.what, got, c.want)
		}
	}

	// A float with no extent obstructs nothing — an empty floated div is a
	// common clearance hack — and the skip that says so is reached only by a
	// float the intersection test would otherwise have found, which means one
	// with extent in the other axis.
	empty := &floatContext{boxes: []placedFloat{
		{rect: Rect{X: u(25), Y: u(0), W: 0, H: u(50)}, side: FloatLeft},
		{rect: Rect{X: u(0), Y: u(25), W: u(100), H: 0}, side: FloatLeft},
	}}
	for _, c := range []struct {
		what string
		r    Rect
	}{
		{"a box over a float of no width", Rect{X: u(0), Y: u(0), W: u(100), H: u(50)}},
		{"a box over a float of no height", Rect{X: u(0), Y: u(0), W: u(100), H: u(50)}},
	} {
		if empty.overlaps(c.r) {
			t.Errorf("%s: overlaps is true, want false", c.what)
		}
	}
}

// TestZeroHeightBoxCollapsesItsOwnMargins is §8.3.1's zero-versus-auto.
//
// Its list of adjoining pairs asks for a computed height of "zero or 'auto'"
// where a box's own two margins meet, and for "'auto'" where a parent's bottom
// margin meets its last child's. Reading one condition for both makes a
// "height: 0" box a barrier its margins cannot cross.
//
// The document is three blocks in a row: 20 tall, then a "height: 0" box with
// 40 of margin on each side, then 20 tall. Every margin in the run is adjoining
// every other, so they collapse to one 40, and the third box's top is at 60. A
// height read as a barrier gives 20 + 40 + 0 + 40 = 100.
func TestZeroHeightBoxCollapsesItsOwnMargins(t *testing.T) {
	css := noDefaults + `
	#w { width: 100px }
	#a, #c { height: 20px }
	#a { margin-bottom: 40px }
	#b { height: 0; margin-top: 40px; margin-bottom: 40px }
	#c { margin-top: 40px }`
	root := layoutOf(t, 400, `<section id="w">
		<div id="a"></div><div id="b"></div><div id="c"></div>
	</section>`, css)

	w := find(t, root, "w")
	px(t, "the first box's top", relY(t, find(t, root, "a"), w), 0)
	px(t, "the zero-height box's top", relY(t, find(t, root, "b"), w), 60)
	px(t, "the third box's top", relY(t, find(t, root, "c"), w), 60)
	px(t, "the wrapper's content height", w.ContentRect().H, 80)
}

// TestDeclaredHeightStillStopsMarginsCollapsingThroughIt is the other half of
// the same rule, and the reason the condition is "zero or auto" rather than
// "any declared height".
//
// The same document with the middle box one pixel tall: its own two margins are
// no longer adjoining, so the run is 20, then 40, then 1, then 40, and the third
// box's top is at 101.
func TestDeclaredHeightStillStopsMarginsCollapsingThroughIt(t *testing.T) {
	css := noDefaults + `
	#w { width: 100px }
	#a, #c { height: 20px }
	#a { margin-bottom: 40px }
	#b { height: 1px; margin-top: 40px; margin-bottom: 40px }
	#c { margin-top: 40px }`
	root := layoutOf(t, 400, `<section id="w">
		<div id="a"></div><div id="b"></div><div id="c"></div>
	</section>`, css)

	w := find(t, root, "w")
	px(t, "the one-pixel box's top", relY(t, find(t, root, "b"), w), 60)
	px(t, "the third box's top", relY(t, find(t, root, "c"), w), 101)
}

// TestComputedHeightRatherThanUsedHeightDecidesSelfCollapsing is the fixture
// the height clause needed to be worth having.
//
// Without it the clause decides nothing: "no height" is otherwise already
// implied by the box's border box coming out zero-tall, so removing it changed
// no test — which is the shape of a guard that reads as care and is decoration.
// This is the one document that separates the two.
//
// §8.3.1 asks for "zero or 'auto' *computed* 'height'", and 'max-height' does
// not change the computed value of 'height'. So a box with "height: 100px;
// max-height: 0" is nought pixels tall and its margins are still not adjoining:
// the run is 20, then 40, then a box of no height, then 40, and the third box's
// top is at 100 rather than at 60.
//
// No reftest in the suite covers this, and browsers are known to differ on it —
// several decide from the used height rather than the computed one. The
// specification's words are what is asserted here, which is also the behaviour
// this engine had before the clause was widened to admit "height: 0".
func TestComputedHeightRatherThanUsedHeightDecidesSelfCollapsing(t *testing.T) {
	css := noDefaults + `
	#w { width: 100px }
	#a, #c { height: 20px }
	#a { margin-bottom: 40px }
	#b { height: 100px; max-height: 0; margin-top: 40px; margin-bottom: 40px }
	#c { margin-top: 40px }`
	root := layoutOf(t, 400, `<section id="w">
		<div id="a"></div><div id="b"></div><div id="c"></div>
	</section>`, css)

	w := find(t, root, "w")
	px(t, "the clamped box's height", find(t, root, "b").BorderRect.H, 0)
	px(t, "the clamped box's top", relY(t, find(t, root, "b"), w), 60)
	px(t, "the third box's top", relY(t, find(t, root, "c"), w), 100)
}

// TestParentWithADeclaredHeightKeepsItsChildsBottomMargin pins the pair that
// still asks for "auto", so that widening the self-collapsing test did not
// widen this one with it.
//
// A wrapper with a declared height of 50 holding one 20-tall child with a
// 40px bottom margin: the child's margin cannot escape through a bottom edge
// the height has fixed, so the wrapper is 50 tall and the box after it starts
// there rather than at 60.
func TestParentWithADeclaredHeightKeepsItsChildsBottomMargin(t *testing.T) {
	css := noDefaults + `
	#w { width: 100px }
	#p { height: 50px }
	#a { height: 20px; margin-bottom: 40px }
	#n { height: 10px }`
	root := layoutOf(t, 400, `<section id="w">
		<div id="p"><div id="a"></div></div><div id="n"></div>
	</section>`, css)

	w := find(t, root, "w")
	px(t, "the wrapper's height", find(t, root, "p").BorderRect.H, 50)
	px(t, "the following box's top", relY(t, find(t, root, "n"), w), 50)
}
