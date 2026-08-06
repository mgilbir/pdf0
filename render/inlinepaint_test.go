package render

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// Painting a non-replaced inline box: CSS 2.1 §8.6's slice model, §10.6.1's
// content area, and Appendix E's place for both.
//
// Every number below is derived rather than recorded. Courier is the face,
// because it is the one standard face whose advance is the same for every
// character — 600/1000 of the em — and because its AFM gives an ascender of
// 629/1000 and a descender of 157/1000, which is what §10.6.1's content area is
// measured from.
//
// The size is 250px throughout, and that is not arbitrary either: a layout unit
// is a 64th of a pixel, so a size whose unit count is a multiple of a thousand
// turns each of those thousandths into a whole number of units and leaves no
// rounding for a test to trip over. 250px is 16000 units.

const (
	inkSize    = 250.0
	inkAdvance = inkSize * 0.600 // 150, one Courier character
	inkAscent  = inkSize * 0.629 // 157.25
	inkDescent = inkSize * 0.157 // 39.25
	// inkHeight is §10.6.1's content area: the font's ascent plus its descent,
	// and nothing to do with the line box.
	inkHeight = inkAscent + inkDescent // 196.5
)

// courierInk sets the text at that size, with a line-height far larger than the
// content area so that the two cannot be confused in an expected value.
const courierInk = noDefaults + `
body, div, p, span { font-family: Courier; font-size: 250px; line-height: 400px }
`

var blue = style.RGBA{B: 255, A: 1}

// fills returns the filled rectangles of one colour, in painting order.
func inkOf(ops []Op, want style.RGBA) []Rect {
	var out []Rect
	for _, op := range ops {
		r, ok := op.(FillRect)
		if !ok || r.Color != want {
			continue
		}
		out = append(out, r.Rect)
	}
	return out
}

// oneFill is the single rectangle of a colour, or a failure naming what was
// painted instead.
func oneFill(t *testing.T, ops []Op, want style.RGBA) Rect {
	t.Helper()
	got := inkOf(ops, want)
	if len(got) != 1 {
		t.Fatalf("%d rectangles were painted in %v, want 1: %v", len(got), want, got)
	}
	return got[0]
}

// TestInlineBackgroundIsTheFontsContentArea is §10.6.1: the height of a
// non-replaced inline box's content area comes from the font, not from the line
// box it sits on.
//
// It is the rule implementations get wrong in the direction that looks right —
// filling the line box makes a highlighted span in a loosely leaded paragraph
// paint a stripe half again as tall as the letters, and nothing about the page
// says which of the two heights was meant. The line-height here is 400px against
// a content area of 196.5, so the two cannot be confused.
func TestInlineBackgroundIsTheFontsContentArea(t *testing.T) {
	root := layoutOf(t, 4000,
		`<p id="p"><span style="background: green">ab</span></p>`, courierInk)
	got := oneFill(t, Paint(root), green)

	if w := got.W.Px(); w != 2*inkAdvance {
		t.Errorf("the background is %gpx wide, want %g — two Courier characters",
			w, 2*inkAdvance)
	}
	if h := got.H.Px(); h != inkHeight {
		t.Errorf("the background is %gpx tall, want %g — the font's ascent plus its "+
			"descent, and not the %gpx line box", h, inkHeight, 400.0)
	}
	// And it sits on the baseline: its top is the ascent above it.
	base := baselineOfFirstRun(t, root, "p")
	if top := got.Y; top != base.Sub(mustPx(inkAscent)) {
		t.Errorf("the background's top is at %g and the baseline at %g, a gap of %g; "+
			"want the font's ascent, %g",
			top.Px(), base.Px(), base.Sub(top).Px(), inkAscent)
	}
}

// TestInlineBackgroundIgnoresLineHeight is the same rule stated as a difference,
// which is the form a defect cannot satisfy by accident: two paragraphs whose
// line-height differs by a factor of four paint the same rectangle.
func TestInlineBackgroundIgnoresLineHeight(t *testing.T) {
	// The line-height is set on the box that paints, not on the paragraph around
	// it. Written the other way this test could not fail: the stylesheet gives
	// every span a line-height of its own, so a paragraph's value never reaches
	// the box whose rectangle is being measured — a rule that cannot decide
	// anything, which is what a planted defect found here.
	doc := `<p id="p"><span id="s" style="background: green">ab</span></p>`
	one := layoutOf(t, 4000, doc, courierInk+`#s { line-height: 250px }`)
	four := layoutOf(t, 4000, doc, courierInk+`#s { line-height: 1000px }`)

	a := oneFill(t, Paint(one), green)
	b := oneFill(t, Paint(four), green)
	if a.H != b.H {
		t.Errorf("the background is %gpx tall at line-height 250 and %gpx at 1000; "+
			"§10.6.1 measures it against the font", a.H.Px(), b.H.Px())
	}
	if a.W != b.W {
		t.Errorf("the background is %gpx wide at line-height 250 and %gpx at 1000",
			a.W.Px(), b.W.Px())
	}
}

// TestInlineVerticalPaddingIsPaintedAndNotLaidOut is §8.4 and §8.5's asymmetry,
// and it needs both halves asserted together because each alone is satisfiable
// by a defect that gets the other wrong.
//
// The padding is painted: the background grows by it, above and below. The
// padding is not laid out: the line below does not move, and neither does the
// paragraph after it.
func TestInlineVerticalPaddingIsPaintedAndNotLaidOut(t *testing.T) {
	const pad = 30.0
	plain := layoutOf(t, 4000, `<p id="p"><span style="background: green">ab</span></p>
		<p id="q">cd</p>`, courierInk)
	padded := layoutOf(t, 4000,
		`<p id="p"><span style="background: green; padding: 30px 0">ab</span></p>
		<p id="q">cd</p>`, courierInk)

	a := oneFill(t, Paint(plain), green)
	b := oneFill(t, Paint(padded), green)
	if h := b.H.Sub(a.H).Px(); h != 2*pad {
		t.Errorf("30px of vertical padding grew the background by %gpx, want %g",
			h, 2*pad)
	}
	if top := a.Y.Sub(b.Y).Px(); top != pad {
		t.Errorf("the padded background's top is %gpx higher, want %g", top, pad)
	}
	// And nothing moved. The paragraph after it is where it was, which is what
	// §8.4 means by the padding bleeding over the lines around it rather than
	// pushing them apart.
	if got, want := find(t, padded, "q").BorderRect.Y, find(t, plain, "q").BorderRect.Y; got != want {
		t.Errorf("the paragraph after the padded inline is at y=%g, want %g — "+
			"vertical padding on an inline box moved the flow",
			got.Px(), want.Px())
	}
}

// TestInlineBackgroundStopsAtTheMarginEdge is the horizontal half of the box
// model, and it is the one the room reserved on the line does not answer on its
// own: insetItems takes the margin, the border and the padding as a single
// distance, so a fragment that painted the whole of it would paint over the
// margin — the one part of the box model that is meant to show through.
func TestInlineBackgroundStopsAtTheMarginEdge(t *testing.T) {
	root := layoutOf(t, 4000, `<p id="p"><span id="s">ab</span></p>`, courierInk+`
		#s { background: green; margin: 0 40px; padding: 0 10px;
		     border: 20px solid blue }`)
	got := oneFill(t, Paint(root), green)
	if x := got.X.Px(); x != 40 {
		t.Errorf("the background starts at %gpx, want 40 — the margin edge is not "+
			"the border edge", x)
	}
	// The border box: two borders, two paddings and two characters.
	if w := got.W.Px(); w != 2*20+2*10+2*inkAdvance {
		t.Errorf("the background is %gpx wide, want %g", w, 2*20+2*10+2*inkAdvance)
	}
	// And the text is inside all of it, which is what says the two agree.
	if x := runX(t, root, "p", "ab"); x != 40+20+10 {
		t.Errorf("the text is at %gpx, want 70", x)
	}
}

// TestInlineBackgroundImagePlacesAgainstTheFragment pins that a background
// *image* on an inline box goes through the same machinery as everything else's
// — the origin, the position and the clip of css-backgrounds-3 — and that the
// area it is placed against is the fragment's rather than the block's.
//
// The two differ by exactly the half-leading, which is why the y is the
// assertion that matters here: a layer positioned against the line box would sit
// at the top of the line, and a layer positioned against the block would sit at
// the top of the paragraph.
func TestInlineBackgroundImagePlacesAgainstTheFragment(t *testing.T) {
	root := bgLayoutOf(t, `<p id="p"><span style="background-image: url(wide.png);
		background-repeat: no-repeat">ab</span></p>`,
		noDefaults+`body, p, span { font-family: Courier; font-size: 40px;
		line-height: 100px }`)
	got := firstTiling(t, Paint(root))

	// The picture is 40 × 20 and is drawn at its own size.
	if got.Tile.W.Px() != 40 || got.Tile.H.Px() != 20 {
		t.Errorf("the tile is %g × %g, want 40 × 20", got.Tile.W.Px(), got.Tile.H.Px())
	}
	// background-origin's initial value is the padding box, which for a box with
	// no padding is the content area: the ascent above the baseline.
	base := baselineOfFirstRun(t, root, "p")
	if want := base.Sub(mustPx(40 * 0.629)); got.Tile.Y != want {
		t.Errorf("the tile's top is at %g, want %g — the box's own content area, "+
			"not the line box", got.Tile.Y.Px(), want.Px())
	}
}

// TestInlineBorderSlicesAcrossLines is §8.6's slice model, which is the whole
// reason an inline box's decoration is plural.
//
// A box broken over three lines carries its left border on the first fragment,
// its right on the last and neither on the middle one — while the top and the
// bottom are on all three, because the break is horizontal.
func TestInlineBorderSlicesAcrossLines(t *testing.T) {
	// 150px per Courier character: "ab" is 300 wide, the space between two of
	// them is 150 and the borders are 50 each. In a 500px page one word fits and
	// two do not, so the box is broken over three lines.
	root := layoutOf(t, 500, `<p id="p"><span id="s">ab ab ab</span></p>`,
		courierInk+`#s { border: 50px solid blue }`)

	lines := linesOf(t, root, "p")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %v", len(lines), lineTexts(lines))
	}
	// Top, right, bottom, left in pixels, per fragment.
	want := [][4]float64{
		{50, 0, 50, 50},
		{50, 0, 50, 0},
		{50, 50, 50, 0},
	}
	for i, line := range lines {
		if len(line.Boxes) != 1 {
			t.Fatalf("line %d has %d inline box fragments, want 1", i, len(line.Boxes))
		}
		e := line.Boxes[0].Border
		got := [4]float64{e.Top.Px(), e.Right.Px(), e.Bottom.Px(), e.Left.Px()}
		if got != want[i] {
			t.Errorf("fragment %d carries border %v, want %v — §8.6 puts the left "+
				"border on the piece that begins the box and the right on the piece "+
				"that ends it", i, got, want[i])
		}
	}
}

// TestInlineBorderIsDrawnOnTheSlicesThatCarryIt is the same rule asserted on the
// ink rather than on the fragment, because a fragment that carries an edge and
// does not draw it is exactly as wrong as one that does not carry it.
//
// A solid border of one colour on a box broken over three lines is eight bands
// rather than twelve: the fragment in the middle has neither a left edge nor a
// right one.
func TestInlineBorderIsDrawnOnTheSlicesThatCarryIt(t *testing.T) {
	root := layoutOf(t, 500, `<p id="p"><span id="s">ab ab ab</span></p>`,
		courierInk+`#s { border: 50px solid blue }`)
	got := inkOf(Paint(root), blue)
	// Three bands on the fragment that begins the box — top, bottom and left —
	// two on the one in the middle, and three on the one that ends it.
	if len(got) != 8 {
		t.Fatalf("a border over three lines painted %d bands, want 8 "+
			"(3 + 2 + 3): %v", len(got), got)
	}
	// The verticals are the ones the slice model decides, and there are two of
	// them: the left edge of the first fragment and the right edge of the last.
	var verticals int
	for _, r := range got {
		if r.W.Px() == 50 {
			verticals++
		}
	}
	if verticals != 2 {
		t.Errorf("%d vertical border bands were painted, want 2 — one at each end "+
			"of the box and none at the line breaks", verticals)
	}
}

// TestInlineBorderSlicesOverABlock is §8.6's slice model over the other kind of
// break: a block inside an inline splits the box into pieces, and the piece that
// begins it carries the left border while the piece that ends it carries the
// right.
//
// It needs a test of its own because the flags are a different mechanism from
// the line-by-line one. A piece is a box in its own right, so it is both the
// first and the last fragment of *itself* — which is exactly what would give
// each piece a border on all four sides. A planted defect that removed the two
// flags from the reckoning was caught by nothing until this was written.
func TestInlineBorderSlicesOverABlock(t *testing.T) {
	root := layoutOf(t, 4000,
		`<div id="d"><span id="s">ab<div id="mid">x</div>cd</span></div>`,
		courierInk+`#s { border: 50px solid blue }`)
	got := inkOf(Paint(root), blue)
	if len(got) != 6 {
		t.Fatalf("a box split by a block painted %d border bands, want 6 "+
			"(3 + 3): %v", len(got), got)
	}
	var verticals []Rect
	for _, r := range got {
		if r.W.Px() == 50 {
			verticals = append(verticals, r)
		}
	}
	if len(verticals) != 2 {
		t.Fatalf("%d vertical border bands were painted, want 2 — one at each end "+
			"of the box and none at the split: %v", len(verticals), got)
	}
	// The left one is at the start of the first piece; the right one is past
	// "cd", which is two Courier characters into the second.
	if x := verticals[0].X.Px(); x != 0 {
		t.Errorf("the left border is at %gpx, want 0", x)
	}
	if x := verticals[1].X.Px(); x != 2*inkAdvance {
		t.Errorf("the right border is at %gpx, want %g — past the text of the "+
			"piece that ends the box", x, 2*inkAdvance)
	}
}

// TestInlineBackgroundIsPaintedUnderItsOwnText is Appendix E's inline layer: a
// box's background goes down before the text that sits on it, and a nested box's
// background goes down over the box it is inside.
func TestInlineBackgroundIsPaintedUnderItsOwnText(t *testing.T) {
	ops := Paint(layoutOf(t, 4000,
		`<p id="p"><span style="background: blue">a<em style="background: green">b</em></span></p>`,
		courierInk))

	outer, inner, text := -1, -1, -1
	for i, op := range ops {
		switch v := op.(type) {
		case FillRect:
			switch v.Color {
			case blue:
				outer = i
			case green:
				inner = i
			}
		case DrawText:
			if text < 0 {
				text = i
			}
		}
	}
	if outer < 0 || inner < 0 || text < 0 {
		t.Fatalf("the three marks were not all painted: outer=%d inner=%d text=%d",
			outer, inner, text)
	}
	if !(outer < inner) {
		t.Errorf("the nested box's background is at %d and its parent's at %d; "+
			"tree order puts the inner one over the outer", inner, outer)
	}
	if !(inner < text) {
		t.Errorf("the text is at %d and the background at %d; a background is "+
			"painted under the text of its own box", text, inner)
	}
}

// TestBlockBackgroundIsNotPaintedTwice is the trap this feature walks straight
// into, and it is a trap about the box tree rather than about painting.
//
// A text box carries its parent element's *whole* computed style — background,
// border and all — and it is an inline-level box by every test a walk up the
// tree can make. So a chain that kept it would paint <p>'s background a second
// time, over its own text and at the height of its font rather than of its box.
func TestBlockBackgroundIsNotPaintedTwice(t *testing.T) {
	ops := Paint(layoutOf(t, 4000, `<p id="p" style="background: green">ab</p>`,
		courierInk))
	got := inkOf(ops, green)
	if len(got) != 1 {
		t.Fatalf("a block's background painted %d rectangles, want 1: %v", len(got), got)
	}
	// And it is the block's own box, not a line's: the block is 400px tall
	// because that is its line-height, and the content area of its font is not.
	if h := got[0].H.Px(); h != 400 {
		t.Errorf("the background is %gpx tall, want 400 — the block's own box", h)
	}
}

// TestInlineWithNothingToDrawMakesNoFragment is the other half of the same
// question, and it is what keeps this feature from costing every document that
// does not use it.
func TestInlineWithNothingToDrawMakesNoFragment(t *testing.T) {
	root := layoutOf(t, 4000, `<p id="p">a<em>b</em><span>c</span></p>`, courierInk)
	for i, line := range linesOf(t, root, "p") {
		if len(line.Boxes) != 0 {
			t.Errorf("line %d has %d inline box fragments for boxes with no "+
				"background and no border, want 0", i, len(line.Boxes))
		}
	}
}

// TestInlineBackgroundCoversAnAtomicInline pins where the chain starts. An
// inline-block has a fragment of its own and paints its own background; what the
// span around it must do is cover the room the inline-block takes.
func TestInlineBackgroundCoversAnAtomicInline(t *testing.T) {
	root := layoutOf(t, 4000, `<p id="p"><span style="background: green"><span
		id="b" style="display: inline-block; width: 500px; height: 100px;
		background: blue"></span></span></p>`, courierInk)
	ops := Paint(root)
	if got := inkOf(ops, blue); len(got) != 1 {
		t.Fatalf("the inline-block's own background painted %d rectangles, want 1: %v",
			len(got), got)
	}
	outer := oneFill(t, ops, green)
	if w := outer.W.Px(); w != 500 {
		t.Errorf("the span around a 500px inline-block is %gpx wide, want 500", w)
	}
}

// TestHiddenInlineBoxPaintsNothing is §11.2 reaching the new marks. The
// assertion is on the specific colour rather than on the number of operations,
// because "nothing was painted" is the assertion this repository has most often
// found passing for the wrong reason.
func TestHiddenInlineBoxPaintsNothing(t *testing.T) {
	root := layoutOf(t, 4000,
		`<p id="p"><span style="background: green; border: 10px solid blue;
		visibility: hidden">ab</span></p>`, courierInk)
	ops := Paint(root)
	if got := inkOf(ops, green); len(got) != 0 {
		t.Errorf("a hidden inline box painted %d background rectangles: %v", len(got), got)
	}
	if got := inkOf(ops, blue); len(got) != 0 {
		t.Errorf("a hidden inline box painted %d border bands: %v", len(got), got)
	}
}

// TestInlineBackgroundMovesWithRelativePosition is §9.4.3 on a box that has no
// fragment in the flow: the offset moves the text, and the background has to go
// with it or the highlight comes away from the words.
func TestInlineBackgroundMovesWithRelativePosition(t *testing.T) {
	still := layoutOf(t, 4000,
		`<p id="p"><span style="background: green">ab</span></p>`, courierInk)
	moved := layoutOf(t, 4000,
		`<p id="p"><span style="background: green; position: relative;
		left: 100px; top: 40px">ab</span></p>`, courierInk)

	a := oneFill(t, Paint(still), green)
	b := oneFill(t, Paint(moved), green)
	if dx := b.X.Sub(a.X).Px(); dx != 100 {
		t.Errorf("a relatively positioned inline's background moved %gpx across, want 100", dx)
	}
	if dy := b.Y.Sub(a.Y).Px(); dy != 40 {
		t.Errorf("a relatively positioned inline's background moved %gpx down, want 40", dy)
	}
}

// TestInlineBackgroundAboveThePageTopStillProducesADocument is the guardrail
// question, and it is the same one an overline raised: the overflow-page check
// is an Error, so a mark it counts wrongly is not a cosmetic fault but a refusal
// to produce any document at all.
//
// An inline box's vertical padding is kept out of layout by §8.4, so the
// scale-to-fit calculation cannot have accounted for it — which is exactly the
// argument for the check not reading it.
func TestInlineBackgroundAboveThePageTopStillProducesADocument(t *testing.T) {
	res, err := Render(Input{
		HTML: `<p id="p"><span style="background: green; padding: 60px 0">ab</span></p>`,
		CSS: []Stylesheet{{Source: `html, body, p { margin: 0; padding: 0 }
			p { font-size: 40px; line-height: 10px }`}},
	}, Options{})
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if res.Document == nil {
		t.Fatalf("no document was produced: %v", res.Findings)
	}
	for _, f := range res.Findings {
		if f.Rule == RuleOverflowPage {
			t.Errorf("an inline box's padding was counted as content leaving the "+
				"page: %s", f.Error())
		}
	}
	// And the guard still sees a box in the same place, which is what says the
	// exemption is about this mark and not about the check.
	rec := NewRecorder(nil)
	checkPageOverflow(rec, []Op{FillRect{
		Rect:  Rect{Y: mustPx(-10), W: mustPx(10), H: mustPx(5)},
		Color: green,
	}}, A4.Content(), 1)
	if rec.Count(RuleOverflowPage) == 0 {
		t.Error("a box above the page top was not reported; the guard is now blind")
	}
}

// TestInlineDecorationsAreBounded watches the cap fire.
//
// The count is a product of the nesting and the number of lines, and neither the
// box cap nor the parser's nesting cap bounds a product. The document below is
// small and asks for far more fragments than the lowered bound allows.
func TestInlineDecorationsAreBounded(t *testing.T) {
	was := maxInlineDecorations
	maxInlineDecorations = 3
	t.Cleanup(func() { maxInlineDecorations = was })

	rec := NewRecorder(nil)
	in := Input{
		HTML: `<p id="p"><span style="background: green">ab ab ab ab ab ab</span></p>`,
		CSS:  []Stylesheet{{Source: courierInk}},
	}
	got := Build(in)
	w, _ := style.FromPx(500)
	h, _ := style.FromPx(10000)
	root := Layout(got.Root, Size{W: w, H: h}, nil, rec)

	var made int
	for _, line := range linesOf(t, root, "p") {
		made += len(line.Boxes)
	}
	if made != 3 {
		t.Errorf("%d inline box fragments were made against a bound of 3", made)
	}
	var said bool
	for _, f := range rec.Findings() {
		if f.Rule == RuleLimit && strings.Contains(f.Message, "inline boxes") {
			said = true
		}
	}
	if !said {
		t.Errorf("the bound was reached and not reported: %v", rec.Findings())
	}
	// The text is unaffected, which is what the finding claims: a document that
	// hits this loses decoration and not content.
	if n := len(linesOf(t, root, "p")); n != 6 {
		t.Errorf("the paragraph has %d lines, want 6 — the cap changed the layout", n)
	}
}

// TestInlineDecorationBoundIsNotReachedByAnOrdinaryDocument is the other side of
// the bound: a cap that fires on real documents is a bug, and one that has only
// ever been observed not to fire is one nobody knows works. The test above
// watches it fire; this one pins that the counting is of boxes that *draw*.
func TestInlineDecorationBoundCountsOnlyWhatIsDrawn(t *testing.T) {
	was := maxInlineDecorations
	maxInlineDecorations = 1
	t.Cleanup(func() { maxInlineDecorations = was })

	rec := NewRecorder(nil)
	in := Input{
		HTML: `<p id="p"><em>a<em>b<em>c<em>d</em></em></em></em> ab ab ab ab</p>`,
		CSS:  []Stylesheet{{Source: courierInk}},
	}
	got := Build(in)
	w, _ := style.FromPx(1000)
	h, _ := style.FromPx(10000)
	Layout(got.Root, Size{W: w, H: h}, nil, rec)

	for _, f := range rec.Findings() {
		if f.Rule == RuleLimit && strings.Contains(f.Message, "inline boxes") {
			t.Errorf("a document with nothing to draw on its inline boxes reached "+
				"the bound: %s", f.Error())
		}
	}
}
