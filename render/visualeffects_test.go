package render

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// CSS 2.1 §11.1, clipping.
//
// Every assertion below names a rectangle. "Nothing was painted" is true for a
// great many wrong reasons — a box that never got a background, a colour that
// did not parse, a document that produced no boxes at all — and a clip test
// that only checks a mark is absent passes just as happily when the mark was
// never drawn. So each of these says which fill it expects, at which
// coordinates, and every one of them also asserts something that is *still*
// there in the same document.

// fillsOf returns the non-empty rectangles painted in a given colour, in paint
// order. It differs from inkOf next door by dropping the empty ones, which
// matters here: a clip that cuts a fill away entirely leaves nothing rather
// than a rectangle of no size, and a test that counted both could not tell the
// two apart.
func fillsOf(ops []Op, want style.RGBA) []Rect {
	var out []Rect
	for _, op := range ops {
		if r, ok := op.(FillRect); ok && r.Color == want && !r.Rect.Empty() {
			out = append(out, r.Rect)
		}
	}
	return out
}

// soleFill requires exactly one fill of a colour and returns it, naming what
// was asked for so that a failure says which document it came from.
func soleFill(t *testing.T, ops []Op, want style.RGBA, what string) Rect {
	t.Helper()
	got := fillsOf(ops, want)
	if len(got) != 1 {
		t.Fatalf("%s: %d fills of %v, want 1\n%s", what, len(got), want, sketchClips(ops))
	}
	return got[0]
}

// sketchClips is sketchOps with the clips shown, which is the whole of what
// these tests are about.
func sketchClips(ops []Op) string {
	var b strings.Builder
	for _, op := range ops {
		switch v := op.(type) {
		case FillRect:
			fmt.Fprintf(&b, "  fill %v %v\n", v.Rect, v.Color)
		case DrawText:
			fmt.Fprintf(&b, "  text %q at %.2f,%.2f clip %v\n",
				v.Text, v.At.X.Px(), v.At.Y.Px(), v.Clip)
		case DrawImage:
			fmt.Fprintf(&b, "  image %s %v clip %v\n", v.Key, v.Rect, v.Clip)
		case TileImage:
			fmt.Fprintf(&b, "  tile %s clip %v\n", v.Key, v.Clip)
		}
	}
	return b.String()
}

// rectPx asserts a rectangle in CSS pixels.
func rectPx(t *testing.T, what string, got Rect, x, y, w, h float64) {
	t.Helper()
	px(t, what+" x", got.X, x)
	px(t, what+" y", got.Y, y)
	px(t, what+" width", got.W, w)
	px(t, what+" height", got.H, h)
}

// The clipping box's border is a colour of its own, so that a count of the
// bands cannot be confused with the box's background.
var borderInk = style.RGBA{B: 255, A: 1}

// TestOverflowHiddenClipsToThePaddingBox is the geometry of §11.1.1, and the
// padding box is the whole of what is being asserted: the outer box is 100
// wide with a 10px border and 20px of padding, so its content box is 40 wide
// and its padding box is 80. A child 200 wide must be cut to 80 and not to 40
// (the content box) and not to 100 (the border box).
func TestOverflowHiddenClipsToThePaddingBox(t *testing.T) {
	ops := paintOf(t, `<div id="outer"><div id="inner"></div></div>`,
		noDefaults+`
		#outer { width: 100px; height: 100px; overflow: hidden;
		  padding-top: 20px; padding-right: 20px; padding-bottom: 20px; padding-left: 20px;
		  border-top-style: solid; border-right-style: solid;
		  border-bottom-style: solid; border-left-style: solid;
		  border-top-width: 10px; border-right-width: 10px;
		  border-bottom-width: 10px; border-left-width: 10px;
		  border-top-color: #0000ff; border-right-color: #0000ff;
		  border-bottom-color: #0000ff; border-left-color: #0000ff }
		#inner { background-color: #ff0000; width: 200px; height: 200px }`)

	// The border box is 100 + 40 of padding + 20 of border = 160 square, at the
	// origin. The child starts at the content box's corner, 30,30, and is 200
	// square, so it runs to 230 on both axes. The padding box ends at 150 and
	// the content box at 130 — which is what makes this an assertion about
	// which of the two the clip is, rather than about there being one at all.
	got := soleFill(t, ops, red, "the clipped child")
	rectPx(t, "the clipped child", got, 30, 30, 120, 120)

	// And the outer box's own decorations are untouched: §11.1.1 clips a box's
	// contents, not the box. Four border bands, all of them outside the padding
	// box the child was cut to.
	bands := fillsOf(ops, borderInk)
	if len(bands) != 4 {
		t.Fatalf("the clipping box painted %d border bands, want 4\n%s", len(bands), sketchClips(ops))
	}
	rectPx(t, "the top border band", bands[0], 0, 0, 160, 10)
}

// TestOverflowHiddenClipsText asserts the other half of a box's contents, and
// asserts it as a clip on a specific run rather than as an absence: a run that
// straddles the padding edge is cut, and one wholly inside it is not touched.
func TestOverflowHiddenClipsText(t *testing.T) {
	ops := paintOf(t,
		`<div id="outer"><div id="a">inside</div><div id="b">below</div></div>`,
		noDefaults+`
		#outer { width: 300px; height: 30px; overflow: hidden }
		#a { height: 20px } #b { height: 20px }`)

	var inside, below *DrawText
	for i := range ops {
		v, ok := ops[i].(DrawText)
		if !ok {
			continue
		}
		switch v.Text {
		case "inside":
			inside = &v
		case "below":
			below = &v
		}
	}
	if inside == nil || below == nil {
		t.Fatalf("the two runs did not both paint\n%s", sketchClips(ops))
	}
	if inside.Clip.Active {
		t.Errorf("a run wholly inside the clip carries one: %v", inside.Clip)
	}
	// The second line's baseline is below the 30px box, so its ink straddles
	// the padding edge and it has to be cut. The clip is the padding box, which
	// for a box with no border or padding is the border box.
	if !below.Clip.Active {
		t.Fatalf("a run reaching past the clip was not clipped\n%s", sketchClips(ops))
	}
	rectPx(t, "the clip on the second run", below.Clip.Rect, 0, 0, 300, 30)
}

// TestOverflowHiddenDropsTextEntirelyOutside is the exact case, as opposed to
// the cut one: a run with no ink inside the clip is not emitted at all.
func TestOverflowHiddenDropsTextEntirelyOutside(t *testing.T) {
	ops := paintOf(t,
		`<div id="outer"><div id="a">first</div><div id="b">gone</div></div>`,
		noDefaults+`
		#outer { width: 300px; height: 24px; overflow: hidden }
		#a { height: 200px } #b { height: 20px }`)

	if n := drew(ops, "first"); n != 1 {
		t.Errorf("the visible run was drawn %d times, want 1\n%s", n, sketchClips(ops))
	}
	if n := drew(ops, "gone"); n != 0 {
		t.Errorf("a run 200px below a 24px clip was drawn %d times, want 0\n%s",
			n, sketchClips(ops))
	}
}

// TestOverflowScrollAndAutoClipExactlyAsHiddenDoes.
//
// A PDF page does not scroll, so there is nothing about these two values this
// engine is approximating: the content is clipped and there is no scrolling
// mechanism to provide. The assertion is equality with "hidden" rather than
// "something was clipped", so a change that made one of them clip to a
// different rectangle would be caught.
func TestOverflowScrollAndAutoClipExactlyAsHiddenDoes(t *testing.T) {
	doc := `<div id="outer"><div id="inner"></div></div>`
	base := noDefaults + `
		#outer { width: 100px; height: 50px }
		#inner { background-color: #ff0000; width: 400px; height: 400px }`

	want := soleFill(t, paintOf(t, doc, base+`#outer { overflow: hidden }`),
		red, "overflow: hidden")
	rectPx(t, "the hidden clip", want, 0, 0, 100, 50)

	for _, value := range []string{"scroll", "auto"} {
		got := soleFill(t, paintOf(t, doc, base+`#outer { overflow: `+value+` }`),
			red, "overflow: "+value)
		if got != want {
			t.Errorf("overflow: %s clipped to %v, want %v (the same as hidden)",
				value, got, want)
		}
	}
}

// TestOverflowOnAnInlineBoxClipsNothing is what overflow-applies-to-001's third
// case checks: the property applies to block containers and to boxes that
// establish a formatting context, and a <span> is neither.
//
// This test is weaker than it looks and the reason is recorded rather than left
// to be rediscovered. An inline box produces no fragment of its own — inline
// content lives in line boxes — so the clip resolution never sees the span at
// all, and planting a check that would have let it clip changes nothing here or
// in the suite. What this pins is the outcome; overflowClips says why there is
// no code behind it.
func TestOverflowOnAnInlineBoxClipsNothing(t *testing.T) {
	ops := paintOf(t, `<div id="outer"><span id="s"><span id="i"></span></span></div>`,
		noDefaults+`
		#outer { width: 300px }
		#s { overflow: hidden; width: 10px; height: 10px }
		#i { display: inline-block; background-color: #ff0000;
		     width: 200px; height: 200px }`)

	got := soleFill(t, ops, red, "the child of an inline box with overflow: hidden")
	px(t, "the unclipped child's width", got.W, 200)
	px(t, "the unclipped child's height", got.H, 200)
}

// TestOverflowOnATableRowClipsNothing is the reachable half of the same
// "applies to" clause, and the one there is code for. A row and a row group do
// produce fragments with the cells inside them, so an engine that read the
// property off any box would cut a cell's content at its row's edge.
//
// The cell in the same document is the control: overflow *does* apply to a
// table cell, which is a block container.
func TestOverflowOnATableRowClipsNothing(t *testing.T) {
	doc := `<table id="t"><tbody id="g"><tr id="r"><td id="d"><div id="i"></div></td></tr></tbody></table>`
	css := noDefaults + `
		table { border-spacing: 0 } td { padding: 0 }
		#t { width: 100px; table-layout: fixed }
		#i { background-color: #ff0000; width: 300px; height: 300px }`

	for _, on := range []string{"#r", "#g"} {
		ops := paintOf(t, doc, css+on+` { overflow: hidden }`)
		got := soleFill(t, ops, red, "a cell's content under "+on+" { overflow: hidden }")
		px(t, "the width of content under "+on, got.W, 300)
	}
	ops := paintOf(t, doc, css+`#d { overflow: hidden }`)
	got := soleFill(t, ops, red, "a cell's content under td { overflow: hidden }")
	px(t, "the width of content clipped by its cell", got.W, 100)
}

// TestOverflowOnATableClipsItsContents is the other side of the same rule: a
// table is not a block container and does establish a formatting context, so
// the property applies to it.
func TestOverflowOnATableClipsItsContents(t *testing.T) {
	ops := paintOf(t,
		`<table id="t"><tr><td><div id="inner"></div></td></tr></table>`,
		noDefaults+`
		table { border-spacing: 0 } td { padding: 0 }
		#t { width: 100px; height: 40px; overflow: hidden; table-layout: fixed }
		#inner { background-color: #ff0000; width: 300px; height: 300px }`)

	// Only the width. A table is not shortened by a declared height — §17.5.3
	// makes it a minimum — so the vertical axis has nothing to clip here, and
	// asserting a height would be asserting that rule rather than this one.
	got := soleFill(t, ops, red, "the content of a table with overflow: hidden")
	px(t, "the clipped width", got.W, 100)

	// The control, so that the 100 above is the clip rather than the table's
	// own column width having squeezed the child.
	loose := paintOf(t,
		`<table id="t"><tr><td><div id="inner"></div></td></tr></table>`,
		noDefaults+`
		table { border-spacing: 0 } td { padding: 0 }
		#t { width: 100px; height: 40px; table-layout: fixed }
		#inner { background-color: #ff0000; width: 300px; height: 300px }`)
	px(t, "the unclipped child's width",
		soleFill(t, loose, red, "the same table without overflow").W, 300)
}

// TestOverflowClipsAreNested asserts that two clipping ancestors both apply,
// with the specific rectangle the intersection produces rather than merely
// "smaller than either".
func TestOverflowClipsAreNested(t *testing.T) {
	ops := paintOf(t,
		`<div id="a"><div id="b"><div id="inner"></div></div></div>`,
		noDefaults+`
		#a { width: 200px; height: 60px; overflow: hidden }
		#b { width: 300px; height: 40px; overflow: hidden;
		     position: relative; left: 50px }
		#inner { background-color: #ff0000; width: 400px; height: 400px }`)

	// #b is 300 wide starting at x=50, so it reaches 350; #a stops at 200. The
	// heights are 40 and 60, so 40 wins. The intersection is 50,0 150x40.
	got := soleFill(t, ops, red, "a child inside two clipping ancestors")
	rectPx(t, "the doubly clipped child", got, 50, 0, 150, 40)
}

// TestAbsoluteBoxEscapesAnOverflowNotInItsContainingBlockChain is §11.1.1's
// exception and the rule implementations get wrong.
//
// The clipping box is *static*, so the absolutely positioned box inside it
// resolves against the initial containing block and is not clipped by it. The
// same document with "position: relative" on the clipping box is the control,
// and it must clip — without that half the test would pass on an engine that
// had simply forgotten to clip absolutely positioned boxes at all.
func TestAbsoluteBoxEscapesAnOverflowNotInItsContainingBlockChain(t *testing.T) {
	doc := `<div id="panel"><div id="pop"></div></div>`
	base := noDefaults + `
		#panel { width: 100px; height: 50px; overflow: hidden }
		#pop { background-color: #ff0000; position: absolute;
		       top: 0; left: 0; width: 400px; height: 400px }`

	escaped := soleFill(t, paintOf(t, doc, base), red,
		"an abspos box inside a static overflow ancestor")
	rectPx(t, "the unclipped absolute box", escaped, 0, 0, 400, 400)

	clipped := soleFill(t, paintOf(t, doc, base+`#panel { position: relative }`), red,
		"an abspos box inside a positioned overflow ancestor")
	rectPx(t, "the clipped absolute box", clipped, 0, 0, 100, 50)
}

// TestAbsoluteBoxIsClippedByAnAncestorOfItsContainingBlock completes the rule.
// The clip need not be *on* the containing block: any ancestor of it clips too,
// and a static box between the two does not.
func TestAbsoluteBoxIsClippedByAnAncestorOfItsContainingBlock(t *testing.T) {
	ops := paintOf(t,
		`<div id="a"><div id="b"><div id="cb"><div id="pop"></div></div></div></div>`,
		noDefaults+`
		#a  { width: 120px; height: 70px; overflow: hidden }
		#b  { width: 300px; height: 300px; overflow: hidden }
		#cb { position: relative; width: 300px; height: 300px }
		#pop { background-color: #ff0000; position: absolute;
		       top: 0; left: 0; width: 400px; height: 400px }`)

	// #a is an ancestor of the containing block #cb, so it clips. #b sits
	// between #cb and the box, is not in the containing block chain, and must
	// not — its own 300x300 would be indistinguishable here, which is why #a is
	// the smaller of the two and #b is as large as its own contents.
	got := soleFill(t, ops, red, "an abspos box under a clipping ancestor of its containing block")
	rectPx(t, "the clipped absolute box", got, 0, 0, 120, 70)
}

// TestFixedBoxIsNotClippedByAnyOverflow: a fixed box's containing block is the
// page, so nothing in the document is in its containing block chain.
func TestFixedBoxIsNotClippedByAnyOverflow(t *testing.T) {
	ops := paintOf(t,
		`<div id="panel"><div id="pop"></div></div>`,
		noDefaults+`
		#panel { position: relative; width: 100px; height: 50px; overflow: hidden }
		#pop { background-color: #ff0000; position: fixed;
		       top: 0; left: 0; width: 300px; height: 300px }`)

	got := soleFill(t, ops, red, "a fixed box inside a clipping positioned ancestor")
	rectPx(t, "the unclipped fixed box", got, 0, 0, 300, 300)
}

// TestClipRectCutsTheElementsOwnBackground is §11.1.2 rather than §11.1.1, and
// the difference between the two is exactly this: "clip" cuts the element's own
// rendered content, "overflow" does not. Forty of the suite's clip tests are a
// positioned <div> whose only content is a background colour.
func TestClipRectCutsTheElementsOwnBackground(t *testing.T) {
	ops := paintOf(t, `<div id="a"></div>`,
		noDefaults+`#a { background-color: #ff0000; position: absolute;
		  top: 0; left: 0; width: 96px; height: 96px;
		  clip: rect(0px, 0px, 0px, 0px) }`)

	if got := fillsOf(ops, red); len(got) != 0 {
		t.Errorf("a box clipped to nothing painted %v\n%s", got, sketchClips(ops))
	}
}

// TestClipRectGeometry pins where the four offsets are measured from, which is
// the one thing about this property that reads like a mistake and is not: all
// four are distances from the *top left* border edge, so "right" is how far
// right the clip reaches rather than how far in from the right edge.
//
// clip-092 in the suite discriminates: "rect(+7.5ex, +7.5ex, +7.5ex, +7.5ex)"
// on a three-inch square must show nothing. Measuring right and bottom from
// their own edges would leave a large square visible.
func TestClipRectGeometry(t *testing.T) {
	ops := paintOf(t, `<div id="a"></div>`,
		noDefaults+`#a { background-color: #ff0000; position: absolute;
		  top: 10px; left: 20px; width: 100px; height: 100px;
		  clip: rect(5px, 40px, 60px, 10px) }`)

	got := soleFill(t, ops, red, "a box with a rect() clip")
	// The border box starts at 20,10. The clip runs from left=10 to right=40
	// and from top=5 to bottom=60, all relative to that corner.
	rectPx(t, "the clipped background", got, 30, 15, 30, 55)
}

// TestClipRectAutoSidesTakeTheBorderEdge: "auto" means the box's own edge, so a
// rect() with two autos cuts only two sides.
func TestClipRectAutoSidesTakeTheBorderEdge(t *testing.T) {
	ops := paintOf(t, `<div id="a"></div>`,
		noDefaults+`#a { background-color: #ff0000; position: absolute;
		  top: 0; left: 0; width: 100px; height: 100px;
		  clip: rect(25px, auto, auto, 10px) }`)

	got := soleFill(t, ops, red, "a box with two auto clip sides")
	rectPx(t, "the clipped background", got, 10, 25, 90, 75)
}

// TestClipAppliesOnlyToPositionedBoxes. clip-102 in the suite is exactly this
// document: the static parent declares the rect and must not clip, and the
// positioned child inherits the value by keyword and must.
func TestClipAppliesOnlyToPositionedBoxes(t *testing.T) {
	ops := paintOf(t, `<div id="p"><div id="c"></div></div>`,
		noDefaults+`
		#p { background-color: #008000; height: 40px; clip: rect(0, 0, 0, 0) }
		#c { background-color: #ff0000; position: absolute; top: 0; left: 0;
		     width: 96px; height: 96px; clip: inherit }`)

	if got := fillsOf(ops, green); len(got) != 1 {
		t.Fatalf("the static box with a clip painted %d times, want 1 — clip does "+
			"not apply to it\n%s", len(got), sketchClips(ops))
	}
	if got := fillsOf(ops, red); len(got) != 0 {
		t.Errorf("the positioned box that inherited rect(0,0,0,0) painted %v\n%s",
			got, sketchClips(ops))
	}
}

// TestClipShapeSyntax covers what the property accepts and what it drops.
//
// The whitespace-separated form is legal — CSS 2.1 permits a user agent to
// support it — and a mixture of commas and white space is not, which is what
// clip-rect-001 asserts. A shape that is not rect() at all is an invalid
// declaration, so the initial value stands and nothing is clipped.
func TestClipShapeSyntax(t *testing.T) {
	cases := []struct {
		value string
		clips bool
	}{
		{"rect(0, 0, 0, 0)", true},
		{"rect(0 0 0 0)", true},
		{"rect( 0px 0px 0px 0px )", true},
		{"rect(-0px, -0px, -0px, -0px)", true},
		{"rect(+0pt, +0pt, +0pt, +0pt)", true},
		{"rect(0,0,0 0)", false}, // mixed separators
		{"rect(0 0,0,0)", false}, // mixed separators
		{"rect(0 0,0 0)", false}, // mixed separators
		{"rect(0, 0, 0)", false}, // three offsets
		{"circle(10px, 25px)", false},
		{"rect(0, 0, 0, 0) extra", false},
		{"rect(0%, 0%, 0%, 0%)", false}, // the property takes no percentage
		{"auto", false},
		{"rect(auto, auto, auto, auto)", false},
	}
	for _, tc := range cases {
		ops := paintOf(t, `<div id="a"></div>`,
			noDefaults+`#a { background-color: #ff0000; position: absolute;
			  top: 0; left: 0; width: 96px; height: 96px; clip: `+tc.value+` }`)
		painted := len(fillsOf(ops, red))
		if tc.clips && painted != 0 {
			t.Errorf("clip: %s left %d fills; it clips to nothing", tc.value, painted)
		}
		if !tc.clips && painted != 1 {
			t.Errorf("clip: %s painted %d fills, want 1; it is not a clip", tc.value, painted)
		}
	}
}

// TestClippingNeverMovesABox. §11.1 is about painting, and a clipped box
// occupies exactly the space it did — the box after it does not move up, and
// the clipping box's own height is unchanged.
func TestClippingNeverMovesABox(t *testing.T) {
	plain := layoutOf(t, 600,
		`<div id="a"><div id="i">x</div></div><div id="after">y</div>`,
		noDefaults+`#a { width: 100px; height: 40px } #i { height: 200px }`)
	clipped := layoutOf(t, 600,
		`<div id="a"><div id="i">x</div></div><div id="after">y</div>`,
		noDefaults+`#a { width: 100px; height: 40px; overflow: hidden } #i { height: 200px }`)

	if got, want := find(t, clipped, "i").BorderRect, find(t, plain, "i").BorderRect; got != want {
		t.Errorf("the clipped child's box is %v, want %v — clipping is painting only", got, want)
	}
	if got, want := find(t, clipped, "after").BorderRect.Y, find(t, plain, "after").BorderRect.Y; got != want {
		t.Errorf("the box after a clipping one is at y=%.2f, want %.2f",
			got.Px(), want.Px())
	}
}

// TestOverflowEstablishesABlockFormattingContext is §9.4.1, which is what makes
// "overflow: hidden" the idiom for containing a float. It is asserted here
// beside the clipping because the two arrive together and only one of them is
// about painting.
func TestOverflowEstablishesABlockFormattingContext(t *testing.T) {
	root := layoutOf(t, 600,
		`<div id="a"><div id="f"></div></div>`,
		noDefaults+`#a { width: 200px; overflow: hidden }
		 #f { float: left; width: 50px; height: 80px }`)
	px(t, "the height of a box containing a float with overflow: hidden",
		find(t, root, "a").BorderRect.H, 80)
}

// TestClipDepthBoundFires.
//
// The bound is lowered rather than reached, because a document with sixty-four
// nested clipping boxes takes longer to build than the rest of this file takes
// to run — and a bound that has never been seen to fire is one nobody knows
// works. Past it the clip becomes empty, so the content is lost rather than
// leaked, and the finding says so.
func TestClipDepthBoundFires(t *testing.T) {
	defer func(v int) { maxClipDepth = v }(maxClipDepth)
	maxClipDepth = 2

	const depth = 5
	var doc strings.Builder
	for i := 0; i < depth; i++ {
		fmt.Fprintf(&doc, `<div class="c">`)
	}
	doc.WriteString(`<div id="inner"></div>`)
	doc.WriteString(strings.Repeat("</div>", depth))

	in := Input{HTML: doc.String(), CSS: []Stylesheet{{Source: noDefaults + `
		.c { width: 400px; height: 400px; overflow: hidden }
		#inner { background-color: #ff0000; width: 50px; height: 50px }`}}}
	built := Build(in)
	rec := NewRecorder(nil)
	w, _ := style.FromPx(600)
	h, _ := style.FromPx(1000)
	ops := Paint(Layout(built.Root, Size{W: w, H: h}, nil, rec))

	if got := fillsOf(ops, red); len(got) != 0 {
		t.Errorf("content past the clip nesting bound was painted at %v", got)
	}
	var reported bool
	for _, f := range rec.Findings() {
		if f.Rule == RuleLimit && strings.Contains(f.Message, "clip") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("the clip nesting bound fired and said nothing:\n%v", rec.Findings())
	}

	// And the same document below the bound is not affected, so the test is
	// about the bound rather than about clipping four boxes deep.
	maxClipDepth = 64
	built = Build(in)
	rec = NewRecorder(nil)
	ops = Paint(Layout(built.Root, Size{W: w, H: h}, nil, rec))
	if got := fillsOf(ops, red); len(got) != 1 {
		t.Errorf("below the bound the content painted %d times, want 1", len(got))
	}
}

// TestClipIntersectionCannotWrap.
//
// A clip is a rectangle intersection, and an intersection that wrapped would
// turn a clip into an amplifier: a negative width read as a very large one
// would let a hostile document paint over a page it was meant to be confined
// to. Unit arithmetic saturates, so the worst case is a saturated extent — and
// a disjoint pair produces a negative extent that Empty reports, rather than
// zero.
func TestClipIntersectionCannotWrap(t *testing.T) {
	// The invariant, over the extremes: an intersection is never *larger* than
	// either rectangle it came from. A wrap is precisely a violation of it — a
	// negative extent read as a very large positive one.
	// Well-formed rectangles only, which is what the invariant is about: a
	// rectangle that already has a negative extent is inside-out before this
	// gets to it, and "the intersection is no larger" says nothing useful about
	// one. A clip is always built from real rectangles — a padding box, or a
	// rect() shape whose corners were ordered before its extent was taken.
	extremes := []Rect{
		{X: style.MinUnit, Y: style.MinUnit, W: style.MaxUnit, H: style.MaxUnit},
		{X: 0, Y: 0, W: style.MaxUnit, H: style.MaxUnit},
		{X: style.MaxUnit, Y: style.MaxUnit, W: style.MaxUnit, H: style.MaxUnit},
		{X: style.MinUnit, Y: style.MinUnit, W: 0, H: 0},
		{X: -1, Y: -1, W: 2, H: 2},
		{X: style.MaxUnit / 2, Y: style.MaxUnit / 2, W: style.MaxUnit, H: style.MaxUnit},
	}
	for _, a := range extremes {
		for _, b := range extremes {
			got := a.Intersect(b)
			if got.W > a.W || got.W > b.W || got.H > a.H || got.H > b.H {
				t.Errorf("%v intersected with %v gave %v, which is larger than one "+
					"of them — the arithmetic wrapped", a, b, got)
			}
		}
	}

	disjoint := Rect{X: 1000, Y: 1000, W: 10, H: 10}.
		Intersect(Rect{X: 0, Y: 0, W: 10, H: 10})
	if !disjoint.Empty() {
		t.Errorf("two disjoint rectangles intersected to %v, which is not empty", disjoint)
	}
	if disjoint.W >= 0 {
		t.Errorf("a disjoint intersection has width %v; it should be negative rather "+
			"than clamped, so that a wrap would be visible", disjoint.W)
	}
}

// TestClippedAwayMarkDoesNotTripTheOverflowPageGuard.
//
// The guard is Error severity: if it fires, no document is produced at all. A
// box that reaches off the page and is then clipped back onto it is not
// evidence that the scale-to-fit calculation was wrong, and it must not be
// reported as though it were.
func TestClippedAwayMarkDoesNotTripTheOverflowPageGuard(t *testing.T) {
	doc := `<div id="a"><div id="i"></div></div>`
	css := noDefaults + `
		#a { width: 100px; height: 50px; overflow: hidden }
		#i { background-color: #ff0000; width: 4000px; height: 4000px }`

	got, err := Render(Input{HTML: doc, CSS: []Stylesheet{{Source: css}}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range got.Findings {
		if f.Rule == RuleOverflowPage {
			t.Errorf("a clipped-away box tripped the page-overflow guard: %s", f.Message)
		}
	}
	if got.Document == nil {
		t.Fatalf("no document was produced: %v", got.Findings)
	}
	// The control: the same box without the clip does reach off the page, so
	// the guard is one that can fire on this document.
	loose, err := Render(Input{HTML: doc, CSS: []Stylesheet{{Source: noDefaults + `
		#a { width: 100px; height: 50px }
		#i { background-color: #ff0000; width: 4000px; height: 4000px }`}}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var fired bool
	for _, f := range loose.Findings {
		if f.Rule == RuleOverflowPage {
			fired = true
		}
	}
	if !fired {
		t.Error("the unclipped control did not trip the page-overflow guard, so the " +
			"assertion above proves nothing")
	}
}

// TestClipReachesTheContentStreamBalanced.
//
// Two claims, and the second is the security one. A clipped picture has to
// reach PDF as a clipping path — nothing else can crop it — and every clip has
// to be inside a q/Q pair, because a clip left open in a content stream does
// not lose a box, it blanks everything drawn after it.
func TestClipReachesTheContentStreamBalanced(t *testing.T) {
	stream := contentStreamOf(t,
		`<div id="a"><div id="t">clipped words that run past the edge</div></div>`,
		Options{},
		noDefaults+`
			#a { width: 60px; height: 14px; overflow: hidden }
			#t { width: 600px; white-space: pre }`)

	// Every W is inside a q that has not been closed yet, the depth never goes
	// negative, and the stream ends where it started — which is what
	// "balanced" has to mean rather than "the counts are equal".
	var clips, depth, worst int
	for _, line := range strings.Split(stream, "\n") {
		switch strings.TrimSpace(line) {
		case "q":
			depth++
		case "Q":
			depth--
			if depth < worst {
				worst = depth
			}
		case "W":
			clips++
			if depth < 2 {
				t.Errorf("a clipping path at graphics-state depth %d; it must be "+
					"inside the operation's own q", depth)
			}
		}
	}
	if clips == 0 {
		t.Errorf("no clipping path in the content stream:\n%s", stream)
	}
	if worst < 0 {
		t.Errorf("the graphics-state stack went %d deep below zero", worst)
	}
	if depth != 0 {
		t.Errorf("the content stream ends %d levels deep:\n%s", depth, stream)
	}
}

// TestClippedPictureCarriesItsClipRatherThanASmallerRectangle.
//
// A picture is the one mark that cannot be clipped by arithmetic: its rectangle
// is where it is *stretched to*, so narrowing it would squeeze the whole image
// into the visible strip rather than showing less of it. The assertion is
// therefore two things at once — the rectangle is untouched, and the clip is
// there.
func TestClippedPictureCarriesItsClipRatherThanASmallerRectangle(t *testing.T) {
	root := replacedLayout(t, 500,
		`<div id="p"><img id="i" src="wide.png"></div>`,
		noDefaults+`
		#p { width: 20px; height: 30px; overflow: hidden }
		#i { width: 40px; height: 20px }`)
	ops := Paint(root)

	var pics []DrawImage
	for _, op := range ops {
		if v, ok := op.(DrawImage); ok {
			pics = append(pics, v)
		}
	}
	if len(pics) != 1 {
		t.Fatalf("%d pictures were drawn, want 1\n%s", len(pics), sketchClips(ops))
	}
	px(t, "the clipped picture's own width", pics[0].Rect.W, 40)
	px(t, "the clipped picture's own height", pics[0].Rect.H, 20)
	if !pics[0].Clip.Active {
		t.Fatalf("a picture wider than its clipping ancestor carries no clip")
	}
	rectPx(t, "the clip on the picture", pics[0].Clip.Rect, 0, 0, 20, 30)

	// And the comparison sees through it: only the visible strip takes part in
	// occlusion, so a mark behind the clipped-away half is not hidden by it.
	for _, f := range picFills(ops) {
		if f.r.Right() > pics[0].Clip.Rect.Right() {
			t.Errorf("the comparison saw a mark reaching to %.2f, past the clip at %.2f",
				f.r.Right().Px(), pics[0].Clip.Rect.Right().Px())
		}
	}
}

// TestClippedTilingNarrowsTheAreaItPaintsAndNotItsTiles.
//
// A tiling already carries the area it may paint, so §11.1 meets that
// rectangle. What must not move is the tile: a background cut in half still has
// to line up with the same background on the box beside it, which it would not
// if the clip had shifted the first tile.
func TestClippedTilingNarrowsTheAreaItPaintsAndNotItsTiles(t *testing.T) {
	const doc = `<div id="p"><div id="b">x</div></div>`
	const inner = `#b { width: 200px; height: 100px; background-image: url(wide.png) }`

	loose := firstTiling(t, bgPaintOf(t, doc,
		noDefaults+`#p { width: 20px; height: 30px }`+inner))
	tight := firstTiling(t, bgPaintOf(t, doc,
		noDefaults+`#p { width: 20px; height: 30px; overflow: hidden }`+inner))

	rectPx(t, "the unclipped tiling's area", loose.Clip, 0, 0, 200, 100)
	rectPx(t, "the clipped tiling's area", tight.Clip, 0, 0, 20, 30)
	if tight.Tile != loose.Tile || tight.StepX != loose.StepX || tight.StepY != loose.StepY {
		t.Errorf("the clip moved the tiles: tile %v step %v,%v, want %v step %v,%v",
			tight.Tile, tight.StepX.Px(), tight.StepY.Px(),
			loose.Tile, loose.StepX.Px(), loose.StepY.Px())
	}
}

// TestTheComparisonSeesAClippedRun.
//
// The reftest oracle is what most of this engine's evidence rests on, and a
// clip is the one thing in a display list it could silently ignore: a fill
// arrives already cut down, so nothing about occlusion needed to change, and a
// run of text carrying a Clip would compare equal to the same run drawn whole
// unless the comparison were told otherwise. This is the check on that, on the
// model of TestWPTOracleHasTeeth.
func TestTheComparisonSeesAClippedRun(t *testing.T) {
	face, ok := StandardFonts().Face("Helvetica", false, false)
	if !ok {
		t.Skip("the standard faces are not available")
	}
	u := func(v float64) style.Unit { r, _ := style.FromPx(v); return r }
	run := DrawText{
		At: Point{u(10), u(30)}, Text: "hello", Face: face, Size: u(16),
		Color: style.RGBA{A: 1},
	}
	cut := run
	cut.Clip = Clip{Rect: Rect{u(10), u(20), u(20), u(20)}, Active: true}
	narrower := run
	narrower.Clip = Clip{Rect: Rect{u(10), u(20), u(10), u(20)}, Active: true}

	clip := pageClip()
	if pictureEqual([]Op{cut}, []Op{run}, clip) {
		t.Error("a run cut by a clip compared equal to the same run drawn whole")
	}
	if pictureEqual([]Op{cut}, []Op{narrower}, clip) {
		t.Error("two runs cut by different clips compared equal")
	}
	if !pictureEqual([]Op{cut}, []Op{cut}, clip) {
		t.Error("a run compared unequal to itself")
	}
}

// TestTheComparisonSeesAClippedPicture is the same check for a picture, whose
// clip cannot be folded into its rectangle either.
func TestTheComparisonSeesAClippedPicture(t *testing.T) {
	u := func(v float64) style.Unit { r, _ := style.FromPx(v); return r }
	img := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	whole := DrawImage{Rect: Rect{u(0), u(0), u(40), u(20)}, Image: img, Key: "k"}
	cut := whole
	cut.Clip = Clip{Rect: Rect{u(0), u(0), u(10), u(20)}, Active: true}

	clip := pageClip()
	if pictureEqual([]Op{cut}, []Op{whole}, clip) {
		t.Error("a picture cut by a clip compared equal to the whole picture")
	}
	// And what it *is* equal to: a fill of its colour over the visible strip,
	// which is the uniform-colour equivalence the comparison already makes.
	strip := FillRect{Rect: Rect{u(0), u(0), u(10), u(20)}, Color: style.RGBA{R: 255, A: 1}}
	if !pictureEqual([]Op{cut}, []Op{strip}, clip) {
		t.Error("a solid picture clipped to a strip did not compare equal to that strip")
	}
}

// TestInlineBoxBackgroundIsClippedByItsBlock.
//
// An inline box's fragments are not children in the fragment tree — there is
// one per line it was broken across, hanging off the line box — so the clip
// resolution has to reach them explicitly. Forgetting to leaves a <span>'s
// background running out of the side of the "overflow: hidden" box that holds
// the words it belongs to, which is the one place a clip can be missed without
// anything else looking wrong.
func TestInlineBoxBackgroundIsClippedByItsBlock(t *testing.T) {
	doc := `<div id="p"><span id="s">wwwwwwwwwwwwwwwwwwwwwwww</span></div>`
	css := noDefaults + `
		#p { width: 40px; height: 40px }
		#s { background-color: #ff0000; white-space: pre }`

	loose := soleFill(t, paintOf(t, doc, css), red, "an inline background with no clip")
	tight := soleFill(t, paintOf(t, doc, css+`#p { overflow: hidden }`), red,
		"an inline background inside overflow: hidden")

	if loose.W <= tight.W {
		t.Fatalf("the unclipped inline background is %.2f wide and the clipped one "+
			"%.2f; the document does not distinguish them", loose.W.Px(), tight.W.Px())
	}
	px(t, "the clipped inline background's width", tight.W, 40)
}

// TestCollapsedGridLinesAreNotCutByTheTablesOwnOverflow.
//
// §17.6.2's grid lines are centred on the boundaries between cells, and the
// ones at the table's edge are centred on its border box — outside the padding
// box that "overflow: hidden" clips the table's *contents* to. They are the
// table's own border by another name, so they take the table's own clip. Cutting
// them there would erase the frame of every collapsing table that also declared
// an overflow, and would do it by half a border width, which reads as a
// rendering artefact rather than as a rule being applied to the wrong box.
func TestCollapsedGridLinesAreNotCutByTheTablesOwnOverflow(t *testing.T) {
	doc := `<table id="t"><tr><td id="d">x</td></tr></table>`
	css := noDefaults + `
		#t { border-collapse: collapse; width: 60px;
		     border-top-style: solid; border-top-width: 8px;
		     border-top-color: #0000ff }
		#d { padding: 0 }`

	loose := fillsOf(paintOf(t, doc, css), borderInk)
	tight := fillsOf(paintOf(t, doc, css+`#t { overflow: hidden }`), borderInk)

	if len(loose) == 0 {
		t.Fatalf("the collapsing table drew no grid line, so this proves nothing")
	}
	if len(tight) != len(loose) {
		t.Fatalf("the table with overflow drew %d grid lines and the one without %d",
			len(tight), len(loose))
	}
	for i := range loose {
		if tight[i] != loose[i] {
			t.Errorf("grid line %d is %v with overflow and %v without; the table's own "+
				"border must not be cut by its own overflow", i, tight[i], loose[i])
		}
	}
}
