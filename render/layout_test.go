package render

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// Block layout.
//
// Geometry is the one part of this engine where a fault is both invisible and
// total: a box eight pixels out looks like a design decision, and nothing about
// the output says otherwise. So every assertion here is an exact number derived
// from the specification's own arithmetic, and the documents are written so that
// each number could only come out right one way.
//
// The default stylesheet is deliberately suppressed in most of these. Its
// margins on <p> and <body> are correct and would otherwise be arithmetic in
// every expected value, hiding the rule actually under test.

const noDefaults = `
html, body, div, p, section { margin: 0; padding: 0; border-top-style: none;
  border-right-style: none; border-bottom-style: none; border-left-style: none }
`

// layoutOf builds and lays out a document in a page of the given width.
func layoutOf(t *testing.T, width float64, htmlSrc string, cssSrc ...string) *Fragment {
	t.Helper()
	in := Input{HTML: htmlSrc}
	for _, c := range cssSrc {
		in.CSS = append(in.CSS, Stylesheet{Source: c})
	}
	got := Build(in)
	if got.Root == nil {
		t.Fatalf("the document produced no boxes")
	}
	rec := NewRecorder(nil)
	w, _ := style.FromPx(width)
	h, _ := style.FromPx(10000)
	frag := Layout(got.Root, Size{W: w, H: h}, nil, rec)
	if frag == nil {
		t.Fatal("layout produced no fragment")
	}
	return frag
}

// find returns the fragment for the element with the given id.
func find(t *testing.T, root *Fragment, id string) *Fragment {
	t.Helper()
	var found *Fragment
	var walk func(*Fragment)
	walk = func(f *Fragment) {
		if found != nil || f == nil {
			return
		}
		if f.Box.Element != nil {
			if got, _ := f.Box.Element.Attr("id"); got == id {
				found = f
				return
			}
		}
		for _, c := range f.Children {
			walk(c)
		}
	}
	walk(root)
	if found == nil {
		t.Fatalf("no fragment for #%s:\n%s", id, sketchFragments(root))
	}
	return found
}

func sketchFragments(f *Fragment) string {
	var out strings.Builder
	var walk func(*Fragment, int)
	walk = func(cur *Fragment, depth int) {
		name := "anonymous"
		if cur.Box.Element != nil {
			name = cur.Box.Element.Name
			if id, _ := cur.Box.Element.Attr("id"); id != "" {
				name += "#" + id
			}
		}
		fmt.Fprintf(&out, "%s%s %s margin%s\n",
			strings.Repeat("  ", depth), name, cur.BorderRect, cur.Margin)
		for _, c := range cur.Children {
			walk(c, depth+1)
		}
	}
	walk(f, 0)
	return out.String()
}

// px compares a length against a number of CSS pixels.
func px(t *testing.T, what string, got style.Unit, want float64) {
	t.Helper()
	expect, _ := style.FromPx(want)
	if got != expect {
		t.Errorf("%s is %.4f px, want %.4f", what, got.Px(), want)
	}
}

// TestAutoWidthFillsTheContainingBlock pins the rule that makes a plain <div>
// as wide as its parent: an auto width takes whatever the margins, borders and
// padding leave.
func TestAutoWidthFillsTheContainingBlock(t *testing.T) {
	root := layoutOf(t, 1000, `<div id="a">x</div>`, noDefaults)
	a := find(t, root, "a")
	px(t, "an auto width", a.BorderRect.W, 1000)

	// Margins take from it.
	root = layoutOf(t, 1000,
		`<div id="a">x</div>`, noDefaults+"#a { margin-left: 100px; margin-right: 50px }")
	a = find(t, root, "a")
	px(t, "the width left by margins", a.BorderRect.W, 850)
	px(t, "the left edge", a.BorderRect.X, 100)

	// Borders and padding are inside the border box, so they do not reduce it —
	// they reduce the content inside it.
	root = layoutOf(t, 1000, `<div id="a">x</div>`,
		noDefaults+"#a { padding-left: 10px; padding-right: 20px; "+
			"border-left-style: solid; border-left-width: 5px }")
	a = find(t, root, "a")
	px(t, "the border box", a.BorderRect.W, 1000)
	px(t, "the content box", a.ContentRect().W, 1000-10-20-5)
}

// TestExplicitWidthAndTheBoxModel pins that width is the *content* width by
// default, so borders and padding make the border box larger than the number the
// author wrote. This is the behaviour box-sizing exists to change, and getting
// it backwards is the commonest box-model fault there is.
func TestExplicitWidthAndTheBoxModel(t *testing.T) {
	root := layoutOf(t, 1000, `<div id="a">x</div>`,
		noDefaults+`#a { width: 400px; padding-left: 10px; padding-right: 10px;
			border-left-style: solid; border-left-width: 5px;
			border-right-style: solid; border-right-width: 5px }`)
	a := find(t, root, "a")
	px(t, "the content width", a.ContentRect().W, 400)
	px(t, "the border box width", a.BorderRect.W, 400+20+10)
}

// TestAutoMarginsCentre pins the idiom every centred page uses. It only works
// once the width is known, which is why the margins are resolved after it.
func TestAutoMarginsCentre(t *testing.T) {
	root := layoutOf(t, 1000, `<div id="a">x</div>`,
		noDefaults+"#a { width: 400px; margin-left: auto; margin-right: auto }")
	a := find(t, root, "a")
	px(t, "the left margin", a.Margin.Left, 300)
	px(t, "the right margin", a.Margin.Right, 300)
	px(t, "the left edge", a.BorderRect.X, 300)

	// One auto margin takes all the slack, which is how a box is pushed right.
	root = layoutOf(t, 1000, `<div id="a">x</div>`,
		noDefaults+"#a { width: 400px; margin-left: auto; margin-right: 0 }")
	a = find(t, root, "a")
	px(t, "the left margin", a.Margin.Left, 600)
	px(t, "the left edge", a.BorderRect.X, 600)
}

// TestOverConstrainedIgnoresTheRightMargin pins §10.3.3's resolution. When
// everything is specified and the sum is wrong, the right margin gives — which
// keeps the box where its left margin put it.
func TestOverConstrainedIgnoresTheRightMargin(t *testing.T) {
	root := layoutOf(t, 1000, `<div id="a">x</div>`,
		noDefaults+"#a { width: 400px; margin-left: 100px; margin-right: 100px }")
	a := find(t, root, "a")
	px(t, "the left edge", a.BorderRect.X, 100)
	px(t, "the width", a.BorderRect.W, 400)
	// 1000 - 100 - 400 = 500 of right margin, not the 100 that was asked for.
	px(t, "the right margin", a.Margin.Right, 500)
}

// TestMarginCollapsing is the rule that makes two paragraphs with 1em margins
// sit 1em apart rather than 2em. It is not a sum and it is not a maximum: the
// largest positive and the most negative are taken separately and added.
func TestMarginCollapsing(t *testing.T) {
	css := noDefaults + `
	#a { margin-bottom: 20px; height: 10px }
	#b { margin-top: 30px; height: 10px }`
	root := layoutOf(t, 1000, `<div id="a"></div><div id="b"></div>`, css)

	a, b := find(t, root, "a"), find(t, root, "b")
	px(t, "the first box's bottom", a.BorderRect.Bottom(), 10)
	// The gap is max(20, 30) = 30, not 50.
	px(t, "the second box's top", b.BorderRect.Y, 40)
}

// TestMarginCollapsingWithNegatives pins the half of the rule that a maximum
// alone would get wrong.
func TestMarginCollapsingWithNegatives(t *testing.T) {
	cases := []struct {
		bottom, top float64
		wantGap     float64
	}{
		{20, 30, 30},    // both positive: the larger
		{30, 20, 30},    // order does not matter
		{20, -30, -10},  // a mixture: added
		{-20, -30, -30}, // both negative: the more negative
		{0, 0, 0},
		{20, 0, 20},
	}
	for _, tc := range cases {
		css := fmt.Sprintf(noDefaults+`
		#a { margin-bottom: %gpx; height: 10px }
		#b { margin-top: %gpx; height: 10px }`, tc.bottom, tc.top)
		root := layoutOf(t, 1000, `<div id="a"></div><div id="b"></div>`, css)
		b := find(t, root, "b")
		px(t, fmt.Sprintf("the gap for %g against %g", tc.bottom, tc.top),
			b.BorderRect.Y, 10+tc.wantGap)
	}
}

// TestTheRootElementsMarginsDoNotCollapse pins §8.3.1's flattest sentence:
// "Margins of the root element's box do not collapse."
//
// It is the one entry in the collapsing model that is about *which* box this is
// rather than about what the box declares, and leaving it out is invisible in
// the ordinary case — a document whose root has no margin collapses nothing
// either way.
//
// The arithmetic, with the default stylesheet's own margins suppressed so that
// only the two written here can appear. <html> has a 20px top margin and <body>
// has none, so <div>'s 20px top margin collapses with <body>'s zero to 20px and
// nothing more: the root's own 20px is separate, and #d's border box starts at
// 20 + 20 = 40px. Collapse the root in and the answer is max(20, 0, 20) = 20px,
// half a bar's height out — which is margin-collapse-020 in the suite.
func TestTheRootElementsMarginsDoNotCollapse(t *testing.T) {
	css := noDefaults + `
	html { margin-top: 20px }
	#d { margin-top: 20px; height: 10px }`
	root := layoutOf(t, 1000, `<div id="d"></div>`, css)
	px(t, "#d's top", find(t, root, "d").BorderRect.Y, 40)

	// The bottom edge is sealed by the same rule, and asserting it separately is
	// what stops the test passing on a change that seals only the top. #e's
	// bottom margin stays inside the root, so the root's border box is as tall
	// as the child *and* its margin: 10 + 30 = 40px.
	css = noDefaults + `
	html { margin-bottom: 20px }
	#e { margin-bottom: 30px; height: 10px }`
	root = layoutOf(t, 1000, `<div id="e"></div>`, css)
	px(t, "the root's height", root.BorderRect.H, 40)
}

// TestAMinHeightSendsACollapsedRunOutOfTheTop pins the sentence CSS 2.2 added to
// §8.3.1 over 2.1:
//
//	If the top margin of a box with non-zero computed 'min-height' and 'auto'
//	computed 'height' collapses with the bottom margin of its last in-flow
//	child, then the child's bottom margin does not collapse with the parent's
//	bottom margin.
//
// A min-height is the only thing that lets the question be asked. Without one
// the parent would collapse through itself and the margin would be above and
// below it alike; with one the parent has a height, and the run has to leave by
// one edge or the other.
//
// The arithmetic. #p has no border, no padding, no height and a 50px min-height;
// its only child is empty with a 50px bottom margin, so the child collapses
// through and the run reaching #p's edges is 50px. #c has a 1px top border, so
// nothing escapes it and everything below is measured from its content top:
//
//   - #p's border box starts 50px down, at y = 1 + 50 = 51;
//   - #p is 50px tall, its min-height, so #s follows at y = 101;
//   - #c's content is 50 + 50 + 50 = 150px tall.
//
// Send the run out of the bottom instead and #p stays at y = 1 with the 50px
// under it: the same total height, and the white square half a square high. That
// is margin-collapse-min-height-002 in the suite exactly.
func TestAMinHeightSendsACollapsedRunOutOfTheTop(t *testing.T) {
	css := noDefaults + `
	#c { border-top-style: solid; border-top-width: 1px }
	#p { min-height: 50px }
	#k { margin-bottom: 50px }
	#s { height: 50px }`
	root := layoutOf(t, 1000,
		`<div id="c"><div id="p"><div id="k"></div></div><div id="s"></div></div>`, css)

	px(t, "#p's top", find(t, root, "p").BorderRect.Y, 51)
	px(t, "#p's height", find(t, root, "p").BorderRect.H, 50)
	px(t, "#s's top", find(t, root, "s").BorderRect.Y, 101)
	px(t, "#c's height", find(t, root, "c").BorderRect.H, 151)

	// The half that has to keep working, and the reason the order rather than
	// the branch is the fix: with something placed in it, the parent's trailing
	// margin still leaves through the bottom and collapses with the sibling's.
	css = noDefaults + `
	#p { min-height: 10px }
	#k { height: 10px; margin-bottom: 50px }
	#s { height: 50px }`
	root = layoutOf(t, 1000,
		`<div id="p"><div id="k"></div></div><div id="s"></div>`, css)
	px(t, "#p's height with content", find(t, root, "p").BorderRect.H, 10)
	px(t, "#s's top after a hoisted bottom margin", find(t, root, "s").BorderRect.Y, 60)
}

// TestARunOfThreeMarginsCollapsesAsOneSet pins §8.3.1's rule over a run longer
// than two, which is where folding it a pair at a time gives a different answer.
//
// The rule is written over the whole set: the largest negative is deducted from
// the largest positive. A set is not recoverable from a value that has already
// had a negative taken off it, so the order the walk happens to fold in decides
// the page — which is a layout that depends on how the code is written.
//
// The arithmetic, with the default stylesheet suppressed. #a has a 16px bottom
// margin. #k is empty with a 96px bottom margin, so it collapses through and its
// two margins join the run. #z has a -96px top margin. All five are adjoining —
// #w has no border, no padding and no height of its own — so the run is
// {16, 0, 0, 96, -96} and collapses to 96 - 96 = 0: #z's border box sits at #a's
// bottom edge, y = 10.
//
// Folded from the right it is collapse(16, collapse(96, -96)) = 16, and #z lands
// an em low. That is margin-bottom-103 and -104 in the suite, where the em is a
// paragraph's and the 96px a 50% margin.
func TestARunOfThreeMarginsCollapsesAsOneSet(t *testing.T) {
	css := noDefaults + `
	#a { height: 10px; margin-bottom: 16px }
	#k { margin-bottom: 96px }
	#z { margin-top: -96px; height: 10px }`
	root := layoutOf(t, 1000,
		`<div id="a"></div><div id="w"><div id="k"></div><div id="z"></div></div>`, css)

	px(t, "#z's top", find(t, root, "z").BorderRect.Y, 10)
	// #w is placed by the same run, so it must not have moved either.
	px(t, "#w's top", find(t, root, "w").BorderRect.Y, 10)

	// The same three margins with the negative in the middle rather than at the
	// end, which a fold that happened to be right-associative would get right by
	// luck. 96 - 96 = 0 again, so #z is still at 10.
	css = noDefaults + `
	#a { height: 10px; margin-bottom: 96px }
	#k { margin-bottom: -96px }
	#z { margin-top: 16px; height: 10px }`
	root = layoutOf(t, 1000,
		`<div id="a"></div><div id="w"><div id="k"></div><div id="z"></div></div>`, css)
	px(t, "#z's top with the negative in the middle", find(t, root, "z").BorderRect.Y, 10)
}

// TestCollapseIsNotASumOrAMaximum tests the rule directly, since the cases above
// reach it through layout and a wrong answer there could come from elsewhere.
func TestCollapseIsNotASumOrAMaximum(t *testing.T) {
	u := func(v float64) style.Unit { r, _ := style.FromPx(v); return r }
	run := func(ms ...float64) style.Unit {
		var m marginRun
		for _, v := range ms {
			m = m.add(u(v))
		}
		return m.value()
	}
	cases := []struct {
		margins []float64
		want    float64
	}{
		{[]float64{20, 10}, 20},
		{[]float64{10, 20}, 20},
		{[]float64{20, -30}, -10},
		{[]float64{-30, 20}, -10},
		{[]float64{-10, -20}, -20},
		{[]float64{0, 0}, 0},

		// Three and more, which is where the rule stops being expressible two at
		// a time. §8.3.1 deducts the largest negative from the largest positive
		// over the whole set: 96 - 96 = 0, whatever the 16 in the middle. Folded
		// pairwise from the right it is collapse(16, collapse(96, -96)) = 16,
		// which is the answer margin-bottom-103 and -104 catch.
		{[]float64{16, 96, -96}, 0},
		{[]float64{96, -96, 16}, 0},
		{[]float64{-96, 16, 96}, 0},
		{[]float64{16, 96, -20, -96}, 0},
		{[]float64{10, 20, 30}, 30},
		{[]float64{-10, -20, -30}, -30},
		{[]float64{50, -10, -30}, 20},
	}
	for _, tc := range cases {
		if got := run(tc.margins...); got != u(tc.want) {
			t.Errorf("collapsing %v gave %g, want %g", tc.margins, got.Px(), tc.want)
		}
	}

	// merge has to be associative and commutative, since the walk folds a run in
	// whatever order the boxes arrive in and hands parts of it up to a parent to
	// be merged again. A rule that was not would give the same document
	// different answers depending on where its boxes were nested.
	var a, b marginRun
	a = a.add(u(16)).add(u(96))
	b = b.add(u(-96)).add(u(-20))
	if got, want := a.merge(b).value(), b.merge(a).value(); got != want {
		t.Errorf("merging in the other order gave %g, not %g", got.Px(), want.Px())
	}
	if got, want := a.merge(b).value(), run(16, 96, -96, -20); got != want {
		t.Errorf("merging two runs gave %g; merging the four margins gives %g",
			got.Px(), want.Px())
	}
}

// TestSiblingsStack pins that boxes follow one another down the page, which is
// the whole of block layout and the thing every other rule modifies.
func TestSiblingsStack(t *testing.T) {
	css := noDefaults + "div { height: 50px }"
	root := layoutOf(t, 1000,
		`<div id="a"></div><div id="b"></div><div id="c"></div>`, css)

	for i, id := range []string{"a", "b", "c"} {
		f := find(t, root, id)
		px(t, "#"+id+"'s top", f.BorderRect.Y, float64(i*50))
		px(t, "#"+id+"'s height", f.BorderRect.H, 50)
	}
}

// TestNestingAddsPaddingAndBorder pins that a child is positioned inside its
// parent's content box, not its border box — the difference being exactly the
// border and padding.
func TestNestingAddsPaddingAndBorder(t *testing.T) {
	css := noDefaults + `
	#outer { padding-top: 10px; padding-left: 20px;
	         border-top-style: solid; border-top-width: 5px;
	         border-left-style: solid; border-left-width: 5px }
	#inner { height: 30px }`
	root := layoutOf(t, 1000, `<div id="outer"><div id="inner"></div></div>`, css)

	inner := find(t, root, "inner")
	px(t, "the child's left", inner.BorderRect.X, 25)
	px(t, "the child's top", inner.BorderRect.Y, 15)
	px(t, "the child's width", inner.BorderRect.W, 1000-25-0)

	outer := find(t, root, "outer")
	// The parent's height is its content plus its own padding and border.
	px(t, "the parent's height", outer.BorderRect.H, 30+10+5)
}

// TestAutoHeightIsTheContent pins that a block with no declared height is as
// tall as what is in it.
func TestAutoHeightIsTheContent(t *testing.T) {
	css := noDefaults + "#a { height: 40px } #b { height: 60px }"
	root := layoutOf(t, 1000,
		`<section id="wrap"><div id="a"></div><div id="b"></div></section>`, css)
	px(t, "the auto height", find(t, root, "wrap").BorderRect.H, 100)
}

// TestExplicitHeightOverridesTheContent pins that a declared height wins, which
// is what makes overflow possible at all.
func TestExplicitHeightOverridesTheContent(t *testing.T) {
	css := noDefaults + "#wrap { height: 25px } #a { height: 100px }"
	root := layoutOf(t, 1000, `<section id="wrap"><div id="a"></div></section>`, css)
	px(t, "the declared height", find(t, root, "wrap").BorderRect.H, 25)
	// The child still has the size it asked for; it simply overflows.
	px(t, "the child's height", find(t, root, "a").BorderRect.H, 100)
}

// TestMinAndMaxClamp pins that the two properties apply, and that a minimum
// wins over a maximum when they contradict — which is CSS's rule and not the
// obvious one.
func TestMinAndMaxClamp(t *testing.T) {
	cases := []struct {
		css       string
		wantWidth float64
	}{
		{"#a { width: 400px; min-width: 600px }", 600},
		{"#a { width: 400px; max-width: 200px }", 200},
		{"#a { width: 400px; min-width: 600px; max-width: 200px }", 600},
		{"#a { min-width: 200px }", 1000},
		{"#a { max-width: 300px }", 300},
	}
	for _, tc := range cases {
		root := layoutOf(t, 1000, `<div id="a"></div>`, noDefaults+tc.css)
		px(t, tc.css, find(t, root, "a").BorderRect.W, tc.wantWidth)
	}

	// And on the height.
	root := layoutOf(t, 1000, `<div id="a"></div>`, noDefaults+"#a { min-height: 80px }")
	px(t, "a minimum height with no content", find(t, root, "a").BorderRect.H, 80)
}

// TestBorderWidthNeedsAStyle pins the rule that surprises people: a border with
// no style draws nothing and occupies nothing, however wide it was declared.
func TestBorderWidthNeedsAStyle(t *testing.T) {
	root := layoutOf(t, 1000, `<div id="a"></div>`,
		noDefaults+"#a { border-left-width: 20px }")
	px(t, "a border with no style", find(t, root, "a").Border.Left, 0)

	root = layoutOf(t, 1000, `<div id="a"></div>`,
		noDefaults+"#a { border-left-width: 20px; border-left-style: solid }")
	px(t, "a border with a style", find(t, root, "a").Border.Left, 20)

	// "none" and "hidden" both mean no border.
	for _, value := range []string{"none", "hidden"} {
		root = layoutOf(t, 1000, `<div id="a"></div>`,
			noDefaults+"#a { border-left-width: 20px; border-left-style: "+value+" }")
		px(t, "border-left-style:"+value, find(t, root, "a").Border.Left, 0)
	}

	// The keyword widths, which are the only place a border width is not a
	// length.
	for value, want := range map[string]float64{"thin": 1, "medium": 3, "thick": 5} {
		root = layoutOf(t, 1000, `<div id="a"></div>`,
			noDefaults+"#a { border-left-width: "+value+"; border-left-style: solid }")
		px(t, "border-left-width:"+value, find(t, root, "a").Border.Left, want)
	}
}

// TestPercentagesResolveAgainstTheWidth pins the rule that looks wrong and is
// not: a percentage padding resolves against the containing block's *width* on
// both axes, which is what gives a box a constant aspect ratio.
func TestPercentagesResolveAgainstTheWidth(t *testing.T) {
	root := layoutOf(t, 800, `<div id="a"></div>`,
		noDefaults+"#a { padding-top: 10%; padding-left: 25% }")
	a := find(t, root, "a")
	px(t, "a percentage top padding", a.Padding.Top, 80)
	px(t, "a percentage left padding", a.Padding.Left, 200)

	// And a percentage width.
	root = layoutOf(t, 800, `<div id="a"></div>`, noDefaults+"#a { width: 50% }")
	px(t, "a percentage width", find(t, root, "a").BorderRect.W, 400)
}

// TestEmResolvesAgainstTheElementsOwnFontSize pins that the font-size chain
// reaches layout. A margin of 1em on an element with a 32px font is 32px, and
// the size it uses is its own rather than its parent's.
func TestEmResolvesAgainstTheElementsOwnFontSize(t *testing.T) {
	css := noDefaults + `
	#outer { font-size: 20px }
	#a { font-size: 2em; margin-top: 1em; height: 1em }`
	root := layoutOf(t, 1000, `<div id="outer"><div id="a"></div></div>`, css)

	a := find(t, root, "a")
	// The element's own font-size is 2 x 20 = 40px, so its em is 40.
	px(t, "an em margin", a.Margin.Top, 40)
	px(t, "an em height", a.BorderRect.H, 40)
}

// TestNoBoxesIsNotACrash pins the two empty cases, which a layout engine meets
// on any document whose root is display:none.
func TestNoBoxesIsNotACrash(t *testing.T) {
	rec := NewRecorder(nil)
	if got := Layout(nil, Size{}, nil, rec); got != nil {
		t.Error("laying out nothing produced a fragment")
	}
	got := Build(Input{HTML: "<p>x</p>", CSS: []Stylesheet{{Source: "html { display: none }"}}})
	if got.Root != nil {
		t.Fatal("the document produced boxes")
	}
	if frag := Layout(got.Root, Size{}, nil, rec); frag != nil {
		t.Error("laying out a document with no boxes produced a fragment")
	}
}

// TestLayoutIsDeterministic pins that two runs agree, since the stages feeding
// this range over maps.
func TestLayoutIsDeterministic(t *testing.T) {
	const src = `<section id="wrap"><div id="a"></div><p id="b">text</p>
		<div id="c"><span>inline</span></div></section>`
	const sheet = `#wrap { padding: 5px } #a { height: 20px; margin-bottom: 10px }
		#b { margin-top: 20px } #c { width: 50%; margin-left: auto }`

	first := sketchFragments(layoutOf(t, 800, src, sheet))
	for i := 0; i < 20; i++ {
		if got := sketchFragments(layoutOf(t, 800, src, sheet)); got != first {
			t.Fatalf("run %d differs:\n%s\n%s", i, first, got)
		}
	}
}

// TestGeometryHelpers pins the rectangle arithmetic the guardrails will ask
// with, including the two edge cases that decide whether a box is overflowing.
func TestGeometryHelpers(t *testing.T) {
	u := func(v float64) style.Unit { r, _ := style.FromPx(v); return r }
	r := Rect{u(10), u(20), u(100), u(50)}

	if r.Right() != u(110) || r.Bottom() != u(70) {
		t.Errorf("the far edges are %v and %v", r.Right().Px(), r.Bottom().Px())
	}
	// A box exactly filling its container is contained: a border landing on the
	// boundary is not overflow.
	if !r.Contains(r) {
		t.Error("a rectangle does not contain itself")
	}
	if r.Contains(Rect{u(10), u(20), u(101), u(50)}) {
		t.Error("a rectangle one unit too wide was reported as contained")
	}

	// Insetting past the extent gives an empty box rather than an inside-out
	// one, which is the shape that makes every later comparison plausible and
	// wrong.
	inset := r.Inset(Edges{Top: u(100), Bottom: u(100), Left: u(200), Right: u(200)})
	if inset.W != 0 || inset.H != 0 {
		t.Errorf("an over-inset rectangle is %v, want empty", inset)
	}
	if !inset.Empty() {
		t.Error("an empty rectangle does not report itself empty")
	}

	// Outsetting is not clamped, because a negative margin legitimately makes a
	// margin box smaller than its border box.
	out := r.Outset(Edges{Top: u(-10), Left: u(-10), Bottom: u(-10), Right: u(-10)})
	if out.W != u(80) || out.H != u(30) {
		t.Errorf("a negative outset gave %v", out)
	}
}

// TestAMinimumHeightHoldsTheLastChildsMarginInside is §8.3.1's requirement that
// nothing separate two margins for them to be adjoining, applied to a minimum.
//
// The list of adjoining pairs asks only for an "'auto' computed height" where a
// parent's bottom margin meets its last child's, and a min-height leaves the
// computed height auto — so read to the letter the child's margin escapes
// however tall the minimum made the box. It cannot: the child's bottom margin
// edge is where the content ended and the box's bottom edge is lower down, so
// the two never meet. The same section says which way the rule falls, in the
// sentence about a non-zero min-height stopping a child's bottom margin
// collapsing with its parent's — written for the narrower case where the
// parent's top margin is in the collapse too, and pointing the same way here.
//
// What it costs to get wrong is the whole point of the property: a min-height is
// how an author reserves a band, and a margin escaping through it moves
// everything below the parent down by a distance that was meant to be inside it.
func TestAMinimumHeightHoldsTheLastChildsMarginInside(t *testing.T) {
	const css = noDefaults + `
	#w { width: 100px }
	#p { min-height: %s }
	#c { height: 30px; margin-bottom: 550px }
	#f { height: 50px }`
	layout := func(t *testing.T, min string) (parentH, footerY float64) {
		t.Helper()
		root := layoutOf(t, 1000,
			`<div id="w"><div id="p"><div id="c"></div></div><div id="f"></div></div>`,
			strings.Replace(css, "%s", min, 1))
		w := find(t, root, "w")
		return find(t, root, "p").BorderRect.H.Px(), relY(t, find(t, root, "f"), w).Px()
	}

	// The minimum binds: the box is a hundred tall against thirty of content, so
	// the child's margin is held inside and the footer follows the box.
	if h, y := layout(t, "100px"); h != 100 || y != 100 {
		t.Errorf("with min-height:100px the parent is %g tall and the footer is at "+
			"%g, want 100 and 100 — the child's 550px bottom margin escaped through "+
			"a box that was taller than its content", h, y)
	}

	// The minimum does not bind: thirty of content against twenty of minimum, so
	// the child's margin reaches the bottom edge and collapses out as always.
	if h, y := layout(t, "20px"); h != 30 || y != 580 {
		t.Errorf("with min-height:20px the parent is %g tall and the footer is at "+
			"%g, want 30 and 580 — a minimum smaller than the content separates "+
			"nothing and must not hold the margin in", h, y)
	}

	// And with no minimum at all, which is the case the other two are measured
	// against.
	if h, y := layout(t, "0"); h != 30 || y != 580 {
		t.Errorf("with no minimum the parent is %g tall and the footer is at %g, "+
			"want 30 and 580", h, y)
	}
}

// TestABoxOfOnlyWhiteSpaceCollapsesThrough is §8.3.1's self-collapsing box, and
// the half of its definition that is about what a box *contains* rather than
// about the properties set on it.
//
// The condition is that the box "does not contain a line box". A box holding
// nothing but collapsible white space contains none — the space is removed at
// both edges of the line it would have been on, and no line is made — so its
// margins meet each other and collapse through it.
//
// Reading the condition as "was given inline children" instead is the difference
// between a box written on one line and the same box written on three, because
// the markup in a real document is indented:
//
//	<div class="b"><span style="position: absolute"></span></div>
//
//	<div class="b">
//	  <span style="position: absolute"></span>
//	</div>
//
// The second is what documents contain, and it stopped the box collapsing —
// moving everything after it down by a margin that should have disappeared.
func TestABoxOfOnlyWhiteSpaceCollapsesThrough(t *testing.T) {
	const css = noDefaults + `
	#k { font-size: 50px; width: 50px }
	#a { margin-bottom: 1em; height: 1em }
	#b { margin-top: 2em; position: relative }
	#c { margin-top: 3em; height: 1em }
	.d { height: 1em; width: 100% }`
	at := func(t *testing.T, inner string) (bTop, cTop float64, bLines int) {
		t.Helper()
		root := layoutOf(t, 1000,
			`<div id="k"><div id="a"></div><div id="b">`+inner+`</div><div id="c"></div></div>`,
			css)
		k := find(t, root, "k")
		b := find(t, root, "b")
		return relY(t, b, k).Px(), relY(t, find(t, root, "c"), k).Px(), len(b.Lines)
	}

	// One em of content, then margins of 1, 2 and 3 ems meeting each other: the
	// collapsed run is the largest of them, so #c sits at 1em + 3em = 200. #b is
	// placed by its own top margin alone, at 1em + 2em = 150, which is where an
	// out-of-flow child of it belongs.
	for _, c := range []struct{ name, inner string }{
		{"nothing at all", ``},
		{"white space only", "\n      "},
		{"an absolutely positioned child", `<div class="d" style="position:absolute"></div>`},
		{"the same, written across lines",
			"\n      " + `<div class="d" style="position:absolute"></div>` + "\n    "},
		{"a floated child, written across lines",
			"\n      " + `<div class="d" style="float:left"></div>` + "\n    "},
	} {
		bTop, cTop, lines := at(t, c.inner)
		if lines != 0 {
			t.Errorf("%s: the box holds %d line boxes, want none — the fixture is "+
				"not testing a self-collapsing box", c.name, lines)
		}
		if bTop != 150 {
			t.Errorf("%s: the collapsed box is at %g, want 150 — a box that "+
				"collapses through is still placed by its own top margin",
				c.name, bTop)
		}
		if cTop != 200 {
			t.Errorf("%s: the box after it is at %g, want 200 — the margins did "+
				"not collapse through", c.name, cTop)
		}
	}

	// The contrast: one character of text is a line box, and a box with a line
	// box in it does not collapse through. #b is then 1em tall and #c follows it.
	bTop, cTop, lines := at(t, "x")
	if lines != 1 {
		t.Fatalf("a box holding text has %d line boxes, want 1", lines)
	}
	if bTop != 150 || cTop <= 200 {
		t.Errorf("with a line box in it the box is at %g and the one after it at "+
			"%g; the second must be below 200, since nothing collapsed through",
			bTop, cTop)
	}

	// And a line box of no height is still a line box. This is the case that
	// separates the rule from the height check beside it: the box is zero tall
	// either way, so only "does it contain a line box" can decide, and §8.3.1
	// says it does and the margins therefore do not collapse through it.
	root := layoutOf(t, 1000,
		`<div id="k"><div id="a"></div>`+
			`<div id="b" style="line-height: 0">x</div>`+
			`<div id="c"></div></div>`, css)
	k := find(t, root, "k")
	b := find(t, root, "b")
	if len(b.Lines) != 1 || b.BorderRect.H != 0 {
		t.Fatalf("the zero-leading box has %d lines and is %gpx tall, want 1 and 0",
			len(b.Lines), b.BorderRect.H.Px())
	}
	// #b keeps its own two margins apart, so the run above it (1em against 2em)
	// puts its edges at 150 and the run below it (0 against 3em) puts #c at 300.
	// Collapsing through would have made one run of all four and put #c at 200.
	if got := relY(t, find(t, root, "c"), k).Px(); got != 300 {
		t.Errorf("after a zero-height box holding a line box the next box is at "+
			"%g, want 300 — its margins collapsed through a box that contains a "+
			"line box", got)
	}
}
