package render

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// Positioning: §9.3's schemes, §9.4.3's relative offsets, §10.3.7's and
// §10.6.4's absolute sizing, and §9.9's stacking order.
//
// Every number below is derived from CSS 2.1's own arithmetic, with the
// derivation written out in the comment above the assertion, and never recorded
// from a run. Each document is arranged so that only the rule under test can
// produce the answer — which for positioning takes more care than it does
// elsewhere, because the plausible wrong answers are so close by. A box
// resolved against its parent's content box instead of its containing block's
// padding box is still a box in a sensible place, so every containing-block test
// below gives those two rectangles different origins and says by how much.

// fills renders a document and returns the colours of its filled rectangles, in
// painting order.
//
// Painting *order* is what the stacking tests are about, so this deliberately
// does not sort — unlike the reftest harness next door, which sorts because the
// two documents of a reftest reach the same marks by different structures and so
// legitimately emit them in different sequences. That sorting is exactly why the
// WPT oracle cannot see a z-index fault, and why these tests exist.
func fills(t *testing.T, htmlSrc, cssSrc string) []string {
	t.Helper()
	root := layoutOf(t, 1000, htmlSrc, noDefaults+cssSrc)
	names := map[style.RGBA]string{
		{R: 255, A: 1}: "red",
		{G: 255, A: 1}: "green",
		{B: 255, A: 1}: "blue",
		{A: 1}:         "black",
	}
	var out []string
	for _, op := range Paint(root) {
		switch v := op.(type) {
		case FillRect:
			name, ok := names[v.Color]
			if !ok {
				name = v.Color.String()
			}
			out = append(out, name)
		case DrawText:
			if strings.TrimSpace(v.Text) == "" {
				continue
			}
			out = append(out, "text "+v.Text)
		}
	}
	return out
}

func order(t *testing.T, what string, got, want []string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%s painted %v, want %v", what, got, want)
	}
}

// countFor returns how many fragments a box produced, which is one for every box
// this engine lays out and is the assertion that catches a box placed twice.
func countFor(root *Fragment, id string) int {
	n := 0
	var walk func(*Fragment)
	walk = func(f *Fragment) {
		if f == nil {
			return
		}
		if f.Box != nil && f.Box.Element != nil {
			if got, _ := f.Box.Element.Attr("id"); got == id {
				n++
			}
		}
		for _, c := range f.Children {
			walk(c)
		}
	}
	walk(root)
	return n
}

// findingsOf lays a document out and returns everything reported, from both the
// build and the layout.
func findingsOf(t *testing.T, htmlSrc, cssSrc string) []Finding {
	t.Helper()
	built := Build(Input{HTML: htmlSrc, CSS: []Stylesheet{{Source: noDefaults + cssSrc}}})
	if built.Root == nil {
		t.Fatal("the document produced no boxes")
	}
	rec := NewRecorder(nil)
	w, _ := style.FromPx(1000)
	h, _ := style.FromPx(10000)
	Layout(built.Root, Size{W: w, H: h}, nil, rec)
	return append(append([]Finding(nil), built.Findings...), rec.Findings()...)
}

func hasRule(findings []Finding, rule Rule) bool {
	for _, f := range findings {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

// TestRelativeOffsetsTheBoxAndNothingElse pins the whole of §9.4.3: the box is
// laid out in the normal flow, then drawn somewhere else, and the space it
// occupied stays occupied.
//
// The two halves fail in opposite directions and the second is the one an
// implementation gets wrong. If the offset were added to the flow position
// instead of applied afterwards, the box would still land in the right place —
// so an assertion about the box alone proves nothing. What tells the two apart
// is the sibling below it and the height of the wrapper around it, both of which
// an offset folded into the flow would move by the same 15px.
func TestRelativeOffsetsTheBoxAndNothingElse(t *testing.T) {
	css := `
	#a { height: 40px }
	#r { position: relative; top: 15px; left: 25px; height: 30px }
	#b { height: 20px }`
	root := layoutOf(t, 1000,
		`<div id="w"><div id="a"></div><div id="r"></div><div id="b"></div></div>`,
		noDefaults+css)

	w := find(t, root, "w")
	// #a is 40 tall, so the flow reaches 40; the offset moves the drawn box to
	// 40 + 15 and 0 + 25.
	px(t, "the relative box's top", relY(t, find(t, root, "r"), w), 55)
	px(t, "the relative box's left", relX(t, find(t, root, "r"), w), 25)

	// The box still occupies 40..70, so the sibling after it is at 70 and not at
	// 85, and the wrapper is 90 tall and not 105.
	px(t, "the sibling below a relative box", relY(t, find(t, root, "b"), w), 70)
	px(t, "the sibling below a relative box's left", relX(t, find(t, root, "b"), w), 0)
	px(t, "the wrapper's height", w.BorderRect.H, 90)
}

// TestRelativeOffsetUsesTheOppositeEdgeAndResolvesTheContradiction pins the
// other three quarters of §9.4.3, which are about what the four properties mean
// when they are not all given.
//
// Left and right describe the same displacement from opposite ends, so a "right"
// on its own is a move to the *left* — the sign is the thing to get wrong, and
// getting it wrong produces a box that moves the right distance the wrong way.
// When both are given they contradict each other and in a left-to-right document
// "left" wins; an implementation that let the last one written win would give
// -40 here rather than +5.
func TestRelativeOffsetUsesTheOppositeEdgeAndResolvesTheContradiction(t *testing.T) {
	for _, tc := range []struct {
		name   string
		rules  string
		dx, dy float64
	}{
		{"right alone moves left", "right: 20px", -20, 0},
		{"bottom alone moves up", "bottom: 12px", 0, -12},
		{"left wins over right", "left: 5px; right: 40px", 5, 0},
		{"top wins over bottom", "top: 3px; bottom: 40px", 0, 3},
		{"neither given is no offset", "", 0, 0},
	} {
		root := layoutOf(t, 1000,
			`<div id="w"><div id="a"></div><div id="r"></div></div>`,
			noDefaults+`#a { height: 40px } #r { position: relative; height: 10px; `+tc.rules+` }`)
		w := find(t, root, "w")
		r := find(t, root, "r")
		px(t, tc.name+": x", relX(t, r, w), tc.dx)
		px(t, tc.name+": y", relY(t, r, w), 40+tc.dy)
	}
}

// TestRelativeCarriesItsDescendantsWithIt pins that the offset is a translation
// of the subtree and not a move of one rectangle.
//
// A descendant is positioned against its parent's content box, so an
// implementation that offset only the box itself would leave every child behind
// — and the box would be an empty frame beside its own content, which is a
// symptom that reads as a painting bug.
func TestRelativeCarriesItsDescendantsWithIt(t *testing.T) {
	root := layoutOf(t, 1000,
		`<div id="r"><div id="c"></div></div>`,
		noDefaults+`
		#r { position: relative; top: 10px; left: 6px }
		#c { height: 20px; margin-left: 4px }`)

	// #r sits at the flow origin and is drawn at (6, 10); #c is 4 further right
	// inside it, so it is drawn at (10, 10). Without the translation it would be
	// at (4, 0).
	c := find(t, root, "c")
	px(t, "a child of a relative box: x", c.BorderRect.X, 10)
	px(t, "a child of a relative box: y", c.BorderRect.Y, 10)
}

// TestRelativePercentageOffsetNeedsADefiniteHeight pins the rule CSS 2.1 states
// for a vertical percentage: it is a percentage of the containing block's
// height, and when that height is not one the author declared the value computes
// to auto rather than to a number.
//
// The document is built so that three different readings give three different
// answers. The containing block is 1000 wide and 200 tall, so resolving against
// the width would give 250; its content is 30 tall, so resolving against the
// height the content happened to need would give 7.5; and the correct answer is
// 50. The second case removes the declared height, where the only correct answer
// is no offset at all and the plausible wrong one is 7.5.
func TestRelativePercentageOffsetNeedsADefiniteHeight(t *testing.T) {
	doc := `<div id="p"><div id="r"></div></div>`

	root := layoutOf(t, 1000, doc, noDefaults+`
		#p { height: 200px }
		#r { position: relative; top: 25%; height: 30px }`)
	px(t, "25% of a declared 200px height", find(t, root, "r").BorderRect.Y, 50)

	root = layoutOf(t, 1000, doc, noDefaults+`
		#r { position: relative; top: 25%; height: 30px }`)
	px(t, "25% of a height the content decided", find(t, root, "r").BorderRect.Y, 0)

	// The two readings of an unresolvable percentage — "auto" and "zero" — give
	// the same answer above and different ones here, which is why this case is
	// the one that tests it. "top" computing to auto hands the axis to "bottom",
	// so the box moves up by 20; "top" computing to zero would win over "bottom"
	// and the box would not move at all.
	root = layoutOf(t, 1000, doc, noDefaults+`
		#r { position: relative; top: 25%; bottom: 20px; height: 30px }`)
	px(t, "an unresolvable top hands the axis to bottom",
		find(t, root, "r").BorderRect.Y, -20)

	// And with the height declared, "top" resolves and wins over "bottom".
	root = layoutOf(t, 1000, doc, noDefaults+`
		#p { height: 200px }
		#r { position: relative; top: 25%; bottom: 20px; height: 30px }`)
	px(t, "a resolvable top wins over bottom", find(t, root, "r").BorderRect.Y, 50)
}

// TestAbsoluteIsRemovedFromTheFlow pins §9.3's first claim about absolute
// positioning, which is the one everything else depends on: the box takes no
// space at all.
//
// The float tests next door make the same assertion about floats, and the
// difference is what makes this worth its own test: a float still shortens the
// line boxes beside it and still counts towards a formatting-context root's
// height, so "out of flow" means something weaker there. Here it means the page
// is identical to one with the box deleted, which is what the second half
// checks.
func TestAbsoluteIsRemovedFromTheFlow(t *testing.T) {
	css := `
	#a { height: 30px }
	#x { position: absolute; width: 100px; height: 100px }
	#b { height: 20px }`
	root := layoutOf(t, 1000,
		`<div id="w"><div id="a"></div><div id="x"></div><div id="b"></div></div>`, noDefaults+css)

	w := find(t, root, "w")
	// #a ends at 30 and #b follows immediately: the 100px box between them
	// contributes nothing, so the wrapper is 50 tall rather than 150.
	px(t, "the block after an absolutely positioned one", relY(t, find(t, root, "b"), w), 30)
	px(t, "the wrapper's height", w.BorderRect.H, 50)

	// And it is still laid out: it exists, at its own size.
	x := find(t, root, "x")
	px(t, "the absolutely positioned box's width", x.BorderRect.W, 100)
	px(t, "the absolutely positioned box's height", x.BorderRect.H, 100)
}

// TestAbsoluteResolvesAgainstThePaddingBoxOfTheNearestPositionedAncestor pins
// §10.1's third case, which is the whole of what "position: relative" on a
// wrapper is for.
//
// Four rectangles in this document could plausibly be the containing block, and
// they are given four different origins so that the assertion can only be
// satisfied by the right one:
//
//	the page box                       at (0, 0)
//	the parent's content box           at (51, 35)
//	the positioned ancestor's content  at (34, 18)
//	the positioned ancestor's padding  at (23, 7)   ← the answer
//
// The padding-versus-content distinction is the one that looks like a detail and
// is not: it is what lets a positioned wrapper with padding hold an overlay
// covering the padding as well, and an engine that used the content box would
// inset every overlay by the wrapper's own padding.
func TestAbsoluteResolvesAgainstThePaddingBoxOfTheNearestPositionedAncestor(t *testing.T) {
	css := `
	#outer { padding: 7px; margin-left: 3px }
	#rel   { position: relative; padding: 11px; margin-left: 13px; height: 100px }
	#mid   { padding: 17px }
	#a     { position: absolute; left: 0; top: 0; width: 10px; height: 10px }`
	root := layoutOf(t, 1000,
		`<div id="outer"><div id="rel"><div id="mid"><div id="a"></div></div></div></div>`,
		noDefaults+css)

	// #outer's border box starts at x=3 (its margin) and its content at x=10.
	// #rel's border box is 13 further right, at 23, and 7 down; with no border
	// its padding box is its border box.
	a := find(t, root, "a")
	px(t, "left: 0 against the positioned ancestor's padding box: x", a.BorderRect.X, 23)
	px(t, "top: 0 against the positioned ancestor's padding box: y", a.BorderRect.Y, 7)

	// And the search walked past #mid and #outer without complaining about
	// either. The geometry above cannot see that on its own: a search that took
	// the first ancestor it met regardless of position would still end up at
	// #rel, because only a positioned box is recorded as a possible containing
	// block in the first place — what it would do differently is report every
	// unpositioned ancestor as one it could not form.
	if got := findingsOf(t,
		`<div id="outer"><div id="rel"><div id="mid"><div id="a"></div></div></div></div>`,
		css); hasRule(got, RulePositionApproximated) {
		t.Errorf("walking past two unpositioned ancestors was reported: %v", got)
	}
}

// TestAbsoluteWithNoPositionedAncestorUsesThePageBox pins §10.1's second case.
//
// The wrapper is given a margin so that "the page box" and "the parent's box"
// are 50px apart; without it the two answers coincide and the test would pass
// under an implementation that never looked for an ancestor at all.
func TestAbsoluteWithNoPositionedAncestorUsesThePageBox(t *testing.T) {
	root := layoutOf(t, 1000,
		`<div id="w"><div id="a"></div></div>`,
		noDefaults+`
		#w { margin: 50px; height: 200px }
		#a { position: absolute; left: 0; top: 0; width: 10px; height: 10px }`)

	a := find(t, root, "a")
	px(t, "an absolute box with no positioned ancestor: x", a.BorderRect.X, 0)
	px(t, "an absolute box with no positioned ancestor: y", a.BorderRect.Y, 0)
}

// TestFixedIgnoresPositionedAncestors pins §9.6.1 in the form it takes on a
// page: the containing block is the viewport, the viewport is the page box, and
// so a positioned ancestor makes no difference at all.
//
// It is the same document as the padding-box test above, with one word changed,
// which is what makes it a real assertion rather than a restatement: the
// absolute answer there is (23, 7) and the fixed answer here is (0, 0). An
// implementation that treated "fixed" as a synonym for "absolute" — the obvious
// shortcut, and half right — lands on the first.
func TestFixedIgnoresPositionedAncestors(t *testing.T) {
	css := `
	#outer { padding: 7px; margin-left: 3px }
	#rel   { position: relative; padding: 11px; margin-left: 13px; height: 100px }
	#a     { position: fixed; left: 0; top: 0; width: 10px; height: 10px }`
	root := layoutOf(t, 1000,
		`<div id="outer"><div id="rel"><div id="a"></div></div></div>`, noDefaults+css)

	a := find(t, root, "a")
	px(t, "a fixed box inside a positioned ancestor: x", a.BorderRect.X, 0)
	px(t, "a fixed box inside a positioned ancestor: y", a.BorderRect.Y, 0)
}

// TestAbsoluteFallsBackToTheStaticPosition pins the clause of §10.3.7 and
// §10.6.4 that decides where a box with no offsets goes: where it would have
// been if it had never been taken out of the flow.
//
// This is the case that makes "position: absolute" with no offsets a no-op to
// look at, and an engine that skipped it would send every such box to its
// containing block's top left corner — which is a page that looks arranged.
//
// The containing block's padding box starts at (0, 0) and its content box at
// (20, 20), and the static position is 60px further down than either because of
// the sibling above. Three answers, one right.
func TestAbsoluteFallsBackToTheStaticPosition(t *testing.T) {
	root := layoutOf(t, 1000,
		`<div id="rel"><div id="p"></div><div id="a"></div></div>`,
		noDefaults+`
		#rel { position: relative; padding: 20px; height: 200px }
		#p   { height: 60px }
		#a   { position: absolute; width: 10px; height: 10px }`)

	a := find(t, root, "a")
	px(t, "the static position: x", a.BorderRect.X, 20)
	px(t, "the static position: y", a.BorderRect.Y, 80)
}

// TestAbsoluteSolvesTheHorizontalConstraint walks §10.3.7's case analysis.
//
// The equation is left + margin-left + border + padding + width + margin-right +
// right = the containing block's width, and each row below leaves a different
// subset of it to the engine. The containing block is 400 wide with its left
// edge at x=0, so every expected number is the arithmetic in the comment beside
// it and nothing else.
func TestAbsoluteSolvesTheHorizontalConstraint(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rules string
		x, w  float64
	}{
		// Nothing to solve: both given.
		{"left and width", "left: 30px; width: 100px", 30, 100},
		// left = 400 - 30 - 100.
		{"right and width", "right: 30px; width: 100px", 270, 100},
		// The stretch case. width = 400 - 20 - 60, and it is 320 rather than 0
		// precisely because an auto width between two offsets is *not*
		// shrink-to-fit — the box has no content at all.
		{"left and right, width auto", "left: 20px; right: 60px", 20, 320},
		// margin-left = margin-right = (400 - 0 - 0 - 100) / 2.
		{"auto margins centre it", "left: 0; right: 0; width: 100px; margin-left: auto; margin-right: auto", 150, 100},
		// Over-constrained: every term is given and they do not add up, so
		// "right" is dropped and the box stays where "left" put it. An engine
		// that dropped "left" instead would put it at 290.
		{"over-constrained drops right", "left: 10px; right: 10px; width: 100px", 10, 100},
		// left = 400 - 20 (right) - 5 (margin-right) - 100 (width).
		{"left auto with a right margin", "right: 20px; width: 100px; margin-right: 5px", 275, 100},
	} {
		root := layoutOf(t, 1000,
			`<div id="w"><div id="a"></div></div>`,
			noDefaults+`
			#w { position: relative; width: 400px; height: 300px }
			#a { position: absolute; height: 10px; `+tc.rules+` }`)
		a := find(t, root, "a")
		px(t, tc.name+": x", a.BorderRect.X, tc.x)
		px(t, tc.name+": width", a.BorderRect.W, tc.w)
	}
}

// TestAbsoluteAutoWidthShrinksToFit pins the other half of §10.3.7's auto width:
// when only one horizontal offset is given there is nothing to stretch between,
// and the width is the shrink-to-fit of §10.3.5.
//
// Shrink-to-fit is min(max(preferred minimum, available), preferred), and the
// two cases below land on a different term of it. With the box at the left edge
// there is more room than the content wants, so the preferred width wins; with
// it 380px along there is less, so the available width wins — and the answer is
// exactly 20, which is the assertion that can only come from that formula. The
// guard above the second case fails loudly if the face's metrics ever move the
// two widths to the same side of 20.
func TestAbsoluteAutoWidthShrinksToFit(t *testing.T) {
	// max-content is the whole run on one line, measured piece by piece the way
	// inline layout measures it; min-content is the widest unbreakable run.
	u := func(v float64) style.Unit { r, _ := style.FromPx(v); return r }
	maxContent := u(measured(t, "aa", 16)).Add(u(measured(t, " ", 16))).Add(u(measured(t, "aa", 16)))
	minContent := u(measured(t, "aa", 16))

	css := `
	#w { position: relative; width: 400px; height: 300px; font-family: Helvetica }
	#a { position: absolute; height: 10px; `
	doc := `<div id="w"><div id="a">aa aa</div></div>`

	root := layoutOf(t, 1000, doc, noDefaults+css+`left: 0 }`)
	if got := find(t, root, "a").BorderRect.W; got != maxContent {
		t.Errorf("an auto width with room to spare is %.4f px, want the preferred width %.4f",
			got.Px(), maxContent.Px())
	}

	if !(minContent < u(20) && maxContent > u(20)) {
		t.Fatalf("the face's metrics no longer straddle 20px (min %.2f, max %.2f), "+
			"so this case no longer tests the middle term of shrink-to-fit",
			minContent.Px(), maxContent.Px())
	}
	root = layoutOf(t, 1000, doc, noDefaults+css+`left: 380px }`)
	px(t, "an auto width squeezed against the right edge", find(t, root, "a").BorderRect.W, 20)
}

// TestAbsoluteSolvesTheVerticalConstraint walks §10.6.4, whose one substantive
// difference from §10.3.7 is what an auto size means when the equation does not
// decide it: the content's height, never a shrink-to-fit.
//
// The third and fourth rows are the pair that proves it. Between two offsets the
// height stretches to 250 although the content needs 33; with only a top the
// height is 33 although there is 290 of room. An engine that used one rule for
// both would fail one row or the other.
func TestAbsoluteSolvesTheVerticalConstraint(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rules string
		y, h  float64
	}{
		{"top and height", "top: 20px; height: 50px", 20, 50},
		// top = 300 - 20 - 50.
		{"bottom and height", "bottom: 20px; height: 50px", 230, 50},
		// height = 300 - 10 - 40, not the 33 the content needs.
		{"top and bottom, height auto", "top: 10px; bottom: 40px", 10, 250},
		// The content's height, not the 290 there is room for.
		{"top alone, height auto", "top: 10px", 10, 33},
		// margin-top = margin-bottom = (300 - 50) / 2.
		{"auto margins centre it", "top: 0; bottom: 0; height: 50px; margin-top: auto; margin-bottom: auto", 125, 50},
	} {
		root := layoutOf(t, 1000,
			`<div id="w"><div id="a"><div id="c"></div></div></div>`,
			noDefaults+`
			#w { position: relative; width: 400px; height: 300px }
			#c { height: 33px }
			#a { position: absolute; left: 0; width: 10px; `+tc.rules+` }`)
		a := find(t, root, "a")
		px(t, tc.name+": y", a.BorderRect.Y, tc.y)
		px(t, tc.name+": height", a.BorderRect.H, tc.h)
	}
}

// TestAbsoluteWidthClampRunsTheConstraintAgain pins §10.4's instruction that a
// width changed by a minimum or a maximum sends the whole of §10.3.7 back round.
//
// It matters because the width is what the other unknowns were solved *from*.
// Here "right: 0" with an auto left and an auto width first gives a
// shrink-to-fit width of 0 — the box is empty — and therefore a left of 400,
// putting the box's zero-width edge against the containing block's right edge.
// The minimum then makes it 300 wide. An implementation that stopped there would
// leave left at 400 and draw a 300px box starting at the containing block's
// right edge, entirely outside it; running the constraint again gives left =
// 400 - 300 and the box lands where "right: 0" asked.
func TestAbsoluteWidthClampRunsTheConstraintAgain(t *testing.T) {
	root := layoutOf(t, 1000,
		`<div id="w"><div id="a"></div></div>`,
		noDefaults+`
		#w { position: relative; width: 400px; height: 300px }
		#a { position: absolute; right: 0; min-width: 300px; height: 10px }`)

	a := find(t, root, "a")
	px(t, "a clamped auto width: width", a.BorderRect.W, 300)
	px(t, "a clamped auto width: x", a.BorderRect.X, 100)
}

// TestAbsoluteEstablishesAFormattingContext pins §9.4.1's entry for absolutely
// positioned boxes, which is what makes the deferred placement in position.go
// sound as well as what makes the geometry right.
//
// Two consequences, and they fail in different places. A child's top margin
// stays inside rather than escaping through the top edge, so the box is 50 tall
// and its child sits 30 down; without the seal the box would be 20 tall, its
// child at its very top, and the escaped margin would be lost entirely because
// there is no flow outside for it to collapse into. And a float inside it is
// contained by §10.6.7, so the box is as tall as the float rather than letting
// it hang out of the bottom.
func TestAbsoluteEstablishesAFormattingContext(t *testing.T) {
	root := layoutOf(t, 1000,
		`<div id="a"><div id="c"></div></div>`,
		noDefaults+`
		#a { position: absolute; left: 0; top: 0; width: 100px }
		#c { height: 20px; margin-top: 30px }`)
	a, c := find(t, root, "a"), find(t, root, "c")
	px(t, "an absolute box seals its top margin: height", a.BorderRect.H, 50)
	px(t, "an absolute box seals its top margin: child y", relY(t, c, a), 30)

	root = layoutOf(t, 1000,
		`<div id="a"><div id="f"></div></div>`,
		noDefaults+`
		#a { position: absolute; left: 0; top: 0; width: 100px }
		#f { float: left; width: 40px; height: 70px }`)
	px(t, "an absolute box contains its floats", find(t, root, "a").BorderRect.H, 70)

	// Both assertions above are satisfied by §9.7 alone: blockification already
	// turns an out-of-flow box into a flow root, and a flow root is a formatting
	// context. What they cannot see is the display values whose inner half
	// *survives* blockification — a table and a flex container stay a table and a
	// flex container when absolutely positioned — for which §9.4.1's own entry
	// for absolute positioning is the only thing that seals them. Nothing lays a
	// table out yet, so the assertion is on the predicate rather than on a page.
	if b := boxOf(t, `<div><div id="a">x</div></div>`,
		`#a { position: absolute; display: table }`, "a"); !establishesBFC(b) {
		t.Error("an absolutely positioned table does not establish a formatting " +
			"context, so its floats would escape and its margins would collapse out")
	}
}

// boxOf builds a document and returns the box an element generated.
func boxOf(t *testing.T, htmlSrc, cssSrc, id string) *Box {
	t.Helper()
	built := Build(Input{HTML: htmlSrc, CSS: []Stylesheet{{Source: noDefaults + cssSrc}}})
	var found *Box
	var walk func(*Box)
	walk = func(b *Box) {
		if b == nil || found != nil {
			return
		}
		if b.Element != nil {
			if got, _ := b.Element.Attr("id"); got == id {
				found = b
				return
			}
		}
		for _, c := range b.Children {
			walk(c)
		}
	}
	walk(built.Root)
	if found == nil {
		t.Fatalf("no box for #%s", id)
	}
	return found
}

// TestAbsoluteBlockifiesAnInline pins §9.7's row for absolute positioning: the
// display of an out-of-flow box is blockified, so "position: absolute" on a
// <span> needs no "display: block" beside it.
//
// The assertion is about the *box tree*, because the layout answer alone would
// not distinguish the two. Inline layout hands an out-of-flow box to the same
// placement code whatever its display, so a span that stayed inline would still
// be positioned and still be the right size; what would differ is everything the
// box tree decides from a box's outer display, starting with whether a block
// inside it splits it in two.
func TestAbsoluteBlockifiesAnInline(t *testing.T) {
	built := Build(Input{
		HTML: `<div><span id="a">x</span></div>`,
		CSS:  []Stylesheet{{Source: noDefaults + `#a { position: absolute }`}},
	})
	var found *Box
	var walk func(*Box)
	walk = func(b *Box) {
		if b == nil || found != nil {
			return
		}
		if b.Element != nil {
			if id, _ := b.Element.Attr("id"); id == "a" {
				found = b
				return
			}
		}
		for _, c := range b.Children {
			walk(c)
		}
	}
	walk(built.Root)
	if found == nil {
		t.Fatal("no box for the absolutely positioned span")
	}
	if found.Outer != OuterBlock {
		t.Errorf("an absolutely positioned span is %v outside, want block", found.Outer)
	}
	if found.Inner != InnerFlowRoot {
		t.Errorf("an absolutely positioned span is %v inside, want flow-root", found.Inner)
	}
	// §9.7's other half: float computes to none, so the two mechanisms cannot
	// both try to take the same box out of the flow.
	if found.Float != FloatNone {
		t.Errorf("an absolutely positioned box floats %v, want none", found.Float)
	}
}

// TestAbsoluteInsideInlineContentTakesThePenPosition pins the static position of
// a box written among words rather than between blocks.
//
// §10.3.7 defines it as where the box's first box would have been with "position:
// static", and for a <span> that is an inline box in the middle of the line — so
// the answer is the pen position, not the start of the line. The document puts
// two characters before it so that the two answers are 111.2px apart at this
// size, which is the width of "aa" in Helvetica at 100px and is stated by
// measuring rather than by being written down.
func TestAbsoluteInsideInlineContentTakesThePenPosition(t *testing.T) {
	root := layoutOf(t, 1000,
		`<div id="w">aa<span id="a"></span>bb</div>`,
		noDefaults+`
		#w { width: 400px; font-family: Helvetica; font-size: 100px }
		#a { position: absolute; width: 10px; height: 10px }`)

	a := find(t, root, "a")
	px(t, "an absolute box among words: x", a.BorderRect.X, measured(t, "aa", 100))
	px(t, "an absolute box among words: y", a.BorderRect.Y, 0)
}

// TestPositionedPaintsAboveInFlowContent pins Appendix E's step 7 against its
// steps 4 to 6: everything positioned is painted after everything that is not,
// whatever order the two were written in.
//
// This is the reason "position: relative" with no offsets at all is a way to lift
// a box above its neighbours, and it is the assertion that a tree-order painter
// fails: the red box is written *first*, so tree order paints it first and it
// ends up underneath.
func TestPositionedPaintsAboveInFlowContent(t *testing.T) {
	got := fills(t, `<div id="r"></div><div id="s"></div>`, `
		#r { position: relative; height: 50px; background-color: #ff0000 }
		#s { height: 50px; background-color: #0000ff }`)
	order(t, "a relative box against a later in-flow sibling", got, []string{"blue", "red"})
}

// TestZIndexOrdersPositionedBoxes pins §9.9: within a stacking context the
// positioned boxes are painted in ascending z-index, and a z-index of auto
// counts as zero for that ordering.
//
// The three boxes are written in the order 5, 1, auto, so document order and
// z-index order are exact reverses of each other and nothing but the sort can
// produce the answer.
func TestZIndexOrdersPositionedBoxes(t *testing.T) {
	got := fills(t, `<div id="w"><div id="a"></div><div id="b"></div><div id="c"></div></div>`, `
		#w { position: relative; height: 100px }
		div div { position: absolute; top: 0; left: 0; width: 50px; height: 50px }
		#a { z-index: 5; background-color: #ff0000 }
		#b { z-index: 1; background-color: #0000ff }
		#c { background-color: #00ff00 }`)
	order(t, "three positioned boxes by z-index", got, []string{"green", "blue", "red"})
}

// TestEqualZIndexPaintsInDocumentOrder pins Appendix E's tie-break, which is the
// half of §9.9 that has no number in it: two boxes at the same level stack back
// to front in the order they were written.
//
// The document is the one case where the order the painter *walks* the fragment
// tree in and the order the boxes were *written* in come apart, and finding it
// took a planted defect: an absolutely positioned box is placed after the flow
// has been laid out, so its fragment is appended to the end of its parent's
// children — behind every in-flow sibling, whatever order the markup had. #f is
// a float that is also relatively positioned, so it is both a positioned box and
// one that layout appends during the walk; #a is absolute and written first but
// appended second. Tree order says red then blue and the walk says the reverse,
// so only a sort keyed on document order gives the right answer.
func TestEqualZIndexPaintsInDocumentOrder(t *testing.T) {
	got := fills(t, `<div id="p"><div id="a"></div><div id="f"></div></div>`, `
		#p { position: relative; height: 100px }
		#a { position: absolute; top: 0; left: 0; width: 50px; height: 50px;
		     background-color: #ff0000 }
		#f { float: left; position: relative; width: 50px; height: 50px;
		     background-color: #0000ff }`)
	order(t, "an absolute box written before a positioned float", got,
		[]string{"red", "blue"})

	// Reversed in the markup, reversed on the page — and this time the walk
	// order and document order agree, so it is the case that would still pass
	// under a painter with no tie-break at all. It is here to show the first
	// assertion is not an artefact of the arrangement.
	got = fills(t, `<div id="p"><div id="f"></div><div id="a"></div></div>`, `
		#p { position: relative; height: 100px }
		#a { position: absolute; top: 0; left: 0; width: 50px; height: 50px;
		     background-color: #ff0000 }
		#f { float: left; position: relative; width: 50px; height: 50px;
		     background-color: #0000ff }`)
	order(t, "a positioned float written before an absolute box", got,
		[]string{"blue", "red"})
}

// TestNegativeZIndexPaintsBehindInFlowContent pins Appendix E's step 3, and the
// exact depth it reaches.
//
// A negative z-index goes behind the in-flow content of its stacking context but
// *not* behind that context's own background — which is what makes the wrapper's
// green rectangle the boundary here. The wrapper is given "z-index: 0" so that
// it really is a stacking context; the next test is the same document without
// it, and the answer changes.
func TestNegativeZIndexPaintsBehindInFlowContent(t *testing.T) {
	got := fills(t, `<div id="w"><div id="n"></div><div id="s"></div></div>`, `
		#w { position: relative; z-index: 0; height: 100px; background-color: #00ff00 }
		#n { position: absolute; z-index: -1; top: 0; left: 0; width: 50px; height: 50px;
		     background-color: #ff0000 }
		#s { height: 50px; background-color: #0000ff }`)
	order(t, "a negative z-index inside a stacking context", got,
		[]string{"green", "red", "blue"})
}

// TestZIndexAutoIsNotAStackingContext pins the distinction §9.9 turns on, using
// the same document as the test above with one declaration removed.
//
// "position: relative" alone does not make a stacking context, so the wrapper's
// negatively-stacked descendant is hoisted into the root's context and paints
// before the wrapper's own background — it escapes from behind its parent, which
// is exactly the behaviour authors rely on and exactly what "z-index: 0" on the
// parent prevents. Reading auto as zero gives the previous test's answer for
// both documents, which is a page where the red box has silently disappeared
// under its own parent.
func TestZIndexAutoIsNotAStackingContext(t *testing.T) {
	got := fills(t, `<div id="w"><div id="n"></div><div id="s"></div></div>`, `
		#w { position: relative; height: 100px; background-color: #00ff00 }
		#n { position: absolute; z-index: -1; top: 0; left: 0; width: 50px; height: 50px;
		     background-color: #ff0000 }
		#s { height: 50px; background-color: #0000ff }`)
	order(t, "a negative z-index under a z-index: auto parent", got,
		[]string{"red", "green", "blue"})

	// The other direction of the same rule: a large z-index inside a plain
	// relative wrapper is *not* trapped by it, so it paints above a later
	// sibling of the wrapper. Under a wrapper that is a stacking context it
	// would be trapped, and the wrapper's level would decide.
	got = fills(t, `<div id="w"><div id="n"></div></div><div id="s"></div>`, `
		#w { position: relative; height: 50px }
		#n { position: absolute; z-index: 5; top: 0; left: 0; width: 50px; height: 50px;
		     background-color: #ff0000 }
		#s { position: relative; z-index: 1; height: 50px; background-color: #0000ff }`)
	order(t, "a positive z-index under a z-index: auto parent", got,
		[]string{"blue", "red"})
}

// TestPaintingOrderLayersBlocksFloatsAndText pins Appendix E steps 4, 5 and 6,
// which are the three layers the old tree-order walk conflated.
//
// The float is written first and the block second, so tree order would paint red
// then blue; the specification paints every block background first, then the
// floats, then the inline content — which is what puts a floated image over the
// background of the paragraph it sits in and under that paragraph's own words.
// The float's own text goes with the float rather than joining the text layer,
// because §E.2 paints a float as though it were a stacking context.
func TestPaintingOrderLayersBlocksFloatsAndText(t *testing.T) {
	got := fills(t, `<div id="w"><div id="f">F</div><div id="b">B</div></div>`, `
		#w { width: 400px; font-family: Helvetica }
		#f { float: left; width: 100px; height: 40px; background-color: #ff0000 }
		#b { height: 40px; background-color: #0000ff }`)
	order(t, "a float against the block it sits in", got,
		[]string{"blue", "red", "text F", "text B"})
}

// TestRelativeOffsetsAnInlineBox pins §9.4.3 applied to an inline box, which is
// the common half of relative positioning and the one that cannot use the
// fragment mechanism the block half uses: an inline box has no fragment at all,
// because a <span> broken across a line belongs to two line boxes and to neither
// exclusively.
//
// The document places two characters before the span so that the offset and the
// pen position are different numbers, and asserts the drawn position of the
// text rather than any box — which is the only thing there is to assert, and the
// only thing that moves.
func TestRelativeOffsetsAnInlineBox(t *testing.T) {
	at := textAt(t, `<div id="w">aa<span id="s">bb</span></div>`, `
		#w { width: 1000px; font-family: Helvetica; font-size: 100px }
		#s { position: relative; left: 30px; top: 7px }`)

	// "aa" is where the flow put it; "bb" follows it and is then moved.
	pen := measured(t, "aa", 100)
	px(t, "the unoffset run's x", at["aa"].X, 0)
	px(t, "the offset run's x", at["bb"].X, pen+30)
	// The baseline of both runs is the same but for the 7px offset, which is
	// what says the offset moved the text and not the line.
	if got := at["bb"].Y.Sub(at["aa"].Y); got != mustPx(7) {
		t.Errorf("the offset run's baseline is %.4f px below the unoffset one, want 7",
			got.Px())
	}
}

// TestRelativeOffsetsNestOnAnInline pins that the offsets accumulate down a
// chain of inline boxes.
//
// They have to, because §9.4.3 moves a box together with its contents and the
// contents of an inline box have been flattened into runs by the time anything
// could walk the tree again. An implementation that recorded only the innermost
// box's offset would move "b" by 5 rather than by 15, which is a smaller move in
// the right direction — the shape of error that looks like a rounding problem.
func TestRelativeOffsetsNestOnAnInline(t *testing.T) {
	at := textAt(t, `<div id="w"><span id="o">a<em id="i">b</em></span></div>`, `
		#w { width: 1000px; font-family: Helvetica; font-size: 100px }
		#o { position: relative; left: 10px }
		#i { position: relative; left: 5px; font-style: normal }`)

	px(t, "the outer span's run", at["a"].X, 10)
	px(t, "the nested span's run", at["b"].X, measured(t, "a", 100)+15)
}

// TestInlineContainingBlockIsReported pins the other case the rule names: a
// containing block §10.1 forms from an inline box's fragments, which this engine
// does not produce.
//
// The box is still placed — against the next positioned ancestor up — so nothing
// about the page says the answer came from the wrong rectangle. That is exactly
// the shape of wrongness the finding exists for.
func TestInlineContainingBlockIsReported(t *testing.T) {
	fired[RulePositionApproximated] = true
	got := findingsOf(t,
		`<div><span id="s"><em id="a">x</em></span></div>`,
		`#s { position: relative } #a { position: absolute; left: 0; top: 0 }`)
	if !hasRule(got, RulePositionApproximated) {
		t.Errorf("an absolute box inside a positioned inline said nothing: %v", got)
	}

	// With the wrapper made a block, the containing block is one this engine
	// forms exactly and there is nothing to report.
	got = findingsOf(t,
		`<div><span id="s"><em id="a">x</em></span></div>`,
		`#s { position: relative; display: block } #a { position: absolute; left: 0; top: 0 }`)
	if hasRule(got, RulePositionApproximated) {
		t.Errorf("an absolute box inside a positioned block was reported: %v", got)
	}
}

// TestPositionedRootIsReported pins the one box this mechanism cannot reach.
//
// The walk records a candidate where it *meets* an out-of-flow box, and nothing
// meets the root element: layout starts there, it has no parent to take a static
// position from, and its fragment is the return value rather than a child of
// anything. It is laid out in the flow, which is a page that looks arranged and
// is missing the declaration, so it says so.
func TestPositionedRootIsReported(t *testing.T) {
	fired[RulePositionApproximated] = true
	got := findingsOf(t, `<div>x</div>`, `html { position: absolute; top: 40px }`)
	if !hasRule(got, RulePositionApproximated) {
		t.Errorf("an absolutely positioned root element said nothing: %v", got)
	}
	// The same declaration one level down is handled, and reports nothing.
	got = findingsOf(t, `<div>x</div>`, `body { position: absolute; top: 40px }`)
	if hasRule(got, RulePositionApproximated) {
		t.Errorf("an absolutely positioned <body> was reported: %v", got)
	}
}

// TestStickyIsReported pins the one value of the position property this engine
// genuinely cannot answer, and it is worth having because it is the value that
// marks where the scope boundary actually lies. Absolute and fixed are static
// computations on a page; sticky is defined by where a scroll container has been
// scrolled to, and a page does not scroll.
func TestStickyIsReported(t *testing.T) {
	got := findingsOf(t, `<div id="a">x</div>`, `#a { position: sticky; top: 0 }`)
	found := false
	for _, f := range got {
		if f.Rule == RuleUnsupportedValue && f.Property == "position" {
			found = true
		}
	}
	if !found {
		t.Errorf("\"position: sticky\" was laid out as static and said nothing: %v", got)
	}
}

// TestOutOfFlowLimitFires watches the bound on the placement queue trip.
//
// The bound is a second one — the box cap already limits how many boxes exist —
// and it is here because the queue feeds itself: placing an absolutely positioned
// box can discover more inside it. A cap that has only ever been observed not to
// trip is one nobody knows works, so this lowers it far enough to see.
func TestOutOfFlowLimitFires(t *testing.T) {
	defer func(old int) { maxAbsolutes = old }(maxAbsolutes)
	maxAbsolutes = 2

	got := findingsOf(t,
		`<div><i id="a"></i><i id="b"></i><i id="c"></i><i id="d"></i></div>`,
		`i { position: absolute; left: 0; top: 0; width: 10px; height: 10px }`)
	if !hasRule(got, RuleLimit) {
		t.Errorf("four out-of-flow boxes past a cap of two reported nothing: %v", got)
	}

	// And with the cap where it belongs, four boxes are four boxes.
	maxAbsolutes = 1 << 14
	got = findingsOf(t,
		`<div><i id="a"></i><i id="b"></i><i id="c"></i><i id="d"></i></div>`,
		`i { position: absolute; left: 0; top: 0; width: 10px; height: 10px }`)
	if hasRule(got, RuleLimit) {
		t.Errorf("four out-of-flow boxes under the ordinary cap reported a limit: %v", got)
	}
}

// TestAbsoluteInARelaidSubtreeIsPlacedOnce pins the interaction between this
// mechanism and the float repair next door.
//
// float.go lays a subtree out twice when the position predicted for it was wrong
// and the subtree read the float geometry. The first layout's fragments are
// thrown away — but the out-of-flow boxes it found were recorded on a queue that
// outlives it, so without a rollback each of them is laid out and placed a second
// time.
//
// # Why counting fragments is not enough, and what is
//
// The obvious assertion is that the box produced one fragment, and it passes
// under the defect: the duplicate is attached to a fragment inside the subtree
// that was discarded, so it is unreachable from the root and never painted. It
// is invisible in the output and real everywhere else — it is work done twice,
// it doubles per level of nested repair, and it consumes the placement budget.
// So the assertion with teeth is against that budget: three is enough for the
// two boxes this document has and not enough for four.
//
// The document is arranged to trigger the repair, and the arrangement needs all
// of its parts. #s has no margin of its own, so its position is predicted from
// zero; its child's 50px top margin escapes through its open top edge and moves
// it down by that much; and its text is laid out beside the float, so the
// subtree *read* the float geometry, which is what forces the expensive repair
// rather than the cheap translation of a subtree that only added floats.
//
// #x is not decoration. Without it nothing has been committed to #w's flow when
// #s is reached, so the 50px margin hoists out of #w, out of <body> and out of
// <html> — it moves the whole document rather than #s, the prediction was right
// after all, and no repair happens. The assertion that #s sits at 60 is what
// says the repair really occurred, and it fails if that stops being true.
func TestAbsoluteInARelaidSubtreeIsPlacedOnce(t *testing.T) {
	const doc = `<div id="w"><div id="f"></div><div id="x"></div>` +
		`<div id="s"><p id="p">text</p><div id="a"></div><div id="b"></div></div></div>`
	const sheet = `
		#w { width: 400px; font-family: Helvetica }
		#f { float: left; width: 100px; height: 200px }
		#x { height: 10px }
		#p { margin-top: 50px; margin-bottom: 0 }
		#a, #b { position: absolute; left: 0; top: 0; width: 10px; height: 10px }`

	root := layoutOf(t, 1000, doc, noDefaults+sheet)
	if n := countFor(root, "a"); n != 1 {
		t.Errorf("the absolutely positioned box produced %d fragments in the tree, want 1", n)
	}
	// #x ends the flow at 10 and #p's 50px margin collapses into #s's own, so #s
	// starts at 60 — half a hundred pixels below the 10 it was predicted at,
	// which is the whole reason the subtree had to be laid out again.
	px(t, "the re-laid subtree's top", relY(t, find(t, root, "s"), find(t, root, "w")), 60)

	// Two out-of-flow boxes, and a budget of three. The discarded layout found
	// both of them; if its findings survive, four boxes are placed and the
	// budget is exhausted.
	defer func(old int) { maxAbsolutes = old }(maxAbsolutes)
	maxAbsolutes = 3
	if got := findingsOf(t, doc, sheet); hasRule(got, RuleLimit) {
		t.Errorf("two out-of-flow boxes exhausted a budget of three, so a discarded "+
			"layout's boxes were placed as well as the real ones: %v", got)
	}
}

// textAt renders a document and returns where each run of text was drawn,
// keyed by the text itself.
func textAt(t *testing.T, htmlSrc, cssSrc string) map[string]Point {
	t.Helper()
	root := layoutOf(t, 1000, htmlSrc, noDefaults+cssSrc)
	out := map[string]Point{}
	for _, op := range Paint(root) {
		if v, ok := op.(DrawText); ok {
			out[v.Text] = v.At
		}
	}
	return out
}
