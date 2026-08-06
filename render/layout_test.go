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
	frag := Layout(got.Root, Size{W: w, H: h}, rec)
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

// TestCollapseIsNotASumOrAMaximum tests the rule directly, since the cases above
// reach it through layout and a wrong answer there could come from elsewhere.
func TestCollapseIsNotASumOrAMaximum(t *testing.T) {
	u := func(v float64) style.Unit { r, _ := style.FromPx(v); return r }
	cases := []struct{ a, b, want float64 }{
		{20, 10, 20},
		{10, 20, 20},
		{20, -30, -10},
		{-30, 20, -10},
		{-10, -20, -20},
		{0, 0, 0},
	}
	for _, tc := range cases {
		if got := collapse(u(tc.a), u(tc.b)); got != u(tc.want) {
			t.Errorf("collapsing %g and %g gave %g, want %g",
				tc.a, tc.b, got.Px(), tc.want)
		}
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
	if got := Layout(nil, Size{}, rec); got != nil {
		t.Error("laying out nothing produced a fragment")
	}
	got := Build(Input{HTML: "<p>x</p>", CSS: []Stylesheet{{Source: "html { display: none }"}}})
	if got.Root != nil {
		t.Fatal("the document produced boxes")
	}
	if frag := Layout(got.Root, Size{}, rec); frag != nil {
		t.Error("laying out a document with no boxes produced a fragment")
	}
}

// TestInlineContentIsReportedOnce pins the honesty of the gap. Text has no
// height yet, and a document full of paragraphs must say so once rather than per
// paragraph — a report that long is one nobody reads.
func TestInlineContentIsReportedOnce(t *testing.T) {
	got := Build(Input{HTML: "<p>a</p><p>b</p><p>c</p><p>d</p>"})
	rec := NewRecorder(nil)
	w, _ := style.FromPx(1000)
	Layout(got.Root, Size{W: w, H: w}, rec)

	n := 0
	for _, f := range rec.Findings() {
		if strings.Contains(f.Message, "inline layout") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the inline gap was reported %d times, want once: %v", n, rec.Findings())
	}

	// A document with no inline content says nothing about it.
	got = Build(Input{HTML: `<div><div></div></div>`})
	rec = NewRecorder(nil)
	Layout(got.Root, Size{W: w, H: w}, rec)
	for _, f := range rec.Findings() {
		if strings.Contains(f.Message, "inline layout") {
			t.Errorf("a document with no text reported the inline gap: %v", f)
		}
	}
}

// TestFirstChildTopMarginIsApplied pins that a first child's top margin moves
// it, which nothing else here covered — the collapsing tests all give the first
// box a *bottom* margin, so dropping the top one entirely changed no expected
// value.
func TestFirstChildTopMarginIsApplied(t *testing.T) {
	css := noDefaults + "#wrap { padding-top: 5px } #a { margin-top: 30px; height: 10px }"
	root := layoutOf(t, 1000, `<section id="wrap"><div id="a"></div></section>`, css)

	a := find(t, root, "a")
	// The padding puts the content box at 5, and the margin adds 30.
	px(t, "the first child's top", a.BorderRect.Y, 35)
	px(t, "the parent's height", find(t, root, "wrap").BorderRect.H, 5+30+10)
}

// TestParentChildCollapseIsReported pins the honesty of the half of margin
// collapsing that is not implemented.
//
// A parent's margin collapsing with its first or last child's is the rule that
// is missing. The difference is visible geometry — the boxes sit further apart
// here than in a browser — so a document where it applies must be told, and one
// where it does not must not be.
func TestParentChildCollapseIsReported(t *testing.T) {
	said := func(src, sheet string) bool {
		in := Input{HTML: src, CSS: []Stylesheet{{Source: sheet}}}
		got := Build(in)
		rec := NewRecorder(nil)
		w, _ := style.FromPx(1000)
		Layout(got.Root, Size{W: w, H: w}, rec)
		for _, f := range rec.Findings() {
			if strings.Contains(f.Message, "collapse through a parent") {
				return true
			}
		}
		return false
	}

	// Nothing separates the parent from its first child's margin, so the rule
	// would apply and is reported.
	if !said(`<section id="wrap"><div id="a"></div></section>`,
		noDefaults+"#a { margin-top: 20px; height: 10px }") {
		t.Error("a margin that should collapse through a parent was not reported")
	}

	// A padding between them stops the collapse, so there is nothing to report:
	// this engine and a browser agree.
	if said(`<section id="wrap"><div id="a"></div></section>`,
		noDefaults+"#wrap { padding-top: 1px } #a { margin-top: 20px; height: 10px }") {
		t.Error("a case where the margins do not collapse was reported anyway")
	}

	// And a document with no adjoining margins at all hears nothing.
	if said(`<section id="wrap"><div id="a"></div></section>`,
		noDefaults+"#a { height: 10px }") {
		t.Error("a document with no adjoining margins was reported")
	}
}

// TestNegativePaddingIsRefused pins that a negative padding is not a thing. The
// declaration is invalid and the initial value stands, rather than the box being
// inset by a negative amount.
func TestNegativePaddingIsRefused(t *testing.T) {
	root := layoutOf(t, 1000, `<div id="a"></div>`,
		noDefaults+"#a { padding-left: -20px; padding-top: -10px }")
	a := find(t, root, "a")
	px(t, "a negative left padding", a.Padding.Left, 0)
	px(t, "a negative top padding", a.Padding.Top, 0)

	// A negative *margin* is legal and does move the box, so the refusal above
	// is about padding specifically and not about negatives in general.
	root = layoutOf(t, 1000, `<div id="a"></div>`, noDefaults+"#a { margin-left: -20px }")
	px(t, "a negative left margin", find(t, root, "a").BorderRect.X, -20)
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
