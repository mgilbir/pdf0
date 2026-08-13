package render

import (
	"github.com/mgilbir/pdf0/style"
	"sort"
	"strings"
	"testing"
)

// CSS 2.1 §8.6: the box model of an inline box in bidirectional context.
//
// # What the rule is
//
// §8.6 states it twice, once per value of direction, and both halves say the
// same thing about the physical axis: the *leftmost* generated box of the
// element carries its left margin, left border and left padding, and the
// *rightmost* carries its right ones. What the direction property changes is
// only which line box those two are looked for on when the element is broken
// across several — the first or the last. On one line it changes nothing.
//
// So the question the engine has to answer is not "what did direction say" but
// "was this box's content reversed by the reordering", and only the algorithm
// knows that. insetSides is where it is answered and why.
//
// # Why the numbers here are readable
//
// Courier is the face: 600 units of 1000, the same for every character
// including the ones it has no glyph for, so at 20px every character is 12px and
// a two-letter word is 24. The insets below are multiples of 24 so that a
// misplaced one lands a whole word away rather than a fraction of one.
//
// text-align is set to left throughout. It is a *physical* value and is not
// resolved against direction, so it pins the line's left edge at zero in a
// right-to-left block and leaves the reordering as the only thing moving
// anything.

const bidiBoxCSS = `
#p { font-family: Courier; font-size: 20px; line-height: 20px;
     width: 300px; text-align: left }
`

// TestInlineMarginStaysOnItsPhysicalSideInAReversedRun is §8.6 on the case it
// exists for.
//
// A margin-left on a span whose content the algorithm reverses. The span's words
// are drawn at the *right* of the line, because they were written first and the
// line reads right to left — and the margin is drawn to their *left*, because
// margin-left is physical and §8.6 puts it on the box's leftmost generated box.
//
// Emitting the two insets in logical order and letting them reverse with the
// content puts it on the far side: the item emitted first is drawn last, so a
// margin-left lands at the right-hand end of the box.
func TestInlineMarginStaysOnItsPhysicalSideInAReversedRun(t *testing.T) {
	// Two Hebrew words in a right-to-left block, the first of them in a span
	// with a 24px left margin. Written order is [span, second word]; drawn order
	// is the reverse, so the second word is at the left of the line.
	//
	//   0..24   the second word
	//   24..48  the span's left margin
	//   48..72  the span's word
	root := layoutOf(t, 600,
		`<div id="p" dir="rtl"><span id="s" style="margin-left: 24px">`+
			hebrewAB+`</span>`+hebrewGD+`</div>`, bidiBoxCSS)
	runs := runsOf(t, root, "p")

	if got := runAt(t, runs, hebrewGD).X.Px(); got != 0 {
		t.Errorf("the word outside the span is at %gpx, want 0 — it is written "+
			"second and drawn first", got)
	}
	if got := runAt(t, runs, hebrewAB).X.Px(); got != 48 {
		t.Errorf("the span's word is at %gpx, want 48 — its 24px margin-left is "+
			"drawn to its physical left, between it and the word before it", got)
	}
}

// TestInlineMarginIsNotMovedWhenTheContentIsNotReversed is the other half of the
// same rule, and the one that makes it about the resolved level rather than
// about the property.
//
// Latin text in a "direction: rtl" block resolves to level two — even, so it
// reads left to right and the reordering leaves it alone. Nothing about the box
// model changes: the margin-left is still physically left of the words, which is
// where it already was.
func TestInlineMarginIsNotMovedWhenTheContentIsNotReversed(t *testing.T) {
	//   0..24   the span's left margin
	//   24..48  "ab"
	//   48..72  "cd"
	root := layoutOf(t, 600,
		`<div id="p" dir="rtl"><span id="s" style="margin-left: 24px">ab</span>cd</div>`,
		bidiBoxCSS)
	runs := runsOf(t, root, "p")

	if got := runAt(t, runs, "ab").X.Px(); got != 24 {
		t.Errorf("the span's text is at %gpx, want 24 — its content is "+
			"left-to-right at level two, so nothing was reversed and the margin is "+
			"where it was written", got)
	}
	if got := runAt(t, runs, "cd").X.Px(); got != 48 {
		t.Errorf("the text after the span is at %gpx, want 48", got)
	}
}

// TestInlineMarginIsNotSwappedByTheDirectionPropertyAlone pins the reading of
// §8.6 that an earlier attempt got wrong, and it is the fixture that separates
// the two.
//
// "direction: rtl" on an inline box with the initial "unicode-bidi: normal" is
// inert — it opens no embedding, so it reorders nothing, by design. Swapping the
// two insets on the property therefore changes the box model of a box whose
// content did not move, and puts the margin on the wrong side of text that is
// still in the order it was written. Measured over the whole suite, that reading
// cost nine clean passes.
//
// The Hebrew word at the front is what makes this a test of insetSides rather
// than of the shortcut around it: without a right-to-left character in the
// paragraph the algorithm is skipped entirely and nothing could have swapped
// anything.
func TestInlineMarginIsNotSwappedByTheDirectionPropertyAlone(t *testing.T) {
	//   0..24   the Hebrew word, level one, a run of its own
	//   24..48  the span's left margin
	//   48..72  "ab", inside the span
	//   72..96  "cd"
	root := layoutOf(t, 600,
		`<div id="p">`+hebrewAB+
			`<span id="s" style="direction: rtl; margin-left: 24px">ab</span>cd</div>`,
		bidiBoxCSS)
	runs := runsOf(t, root, "p")

	if got := runAt(t, runs, "ab").X.Px(); got != 48 {
		t.Errorf("the span's text is at %gpx, want 48 — \"direction: rtl\" with the "+
			"initial unicode-bidi reorders nothing, so the margin-left is still on "+
			"the left", got)
	}
	if got := runAt(t, runs, "cd").X.Px(); got != 72 {
		t.Errorf("the text after the span is at %gpx, want 72", got)
	}
}

// TestInlineInsetOfAMixedBoxIsLeftAlone is the limit of what two items can say,
// written down as a test rather than left to be rediscovered.
//
// A box whose content resolves to more than one level has ends that need not be
// at its visual edges at all, so neither of the two insets is at the box's
// leftmost or rightmost generated box and §8.6 asks for something this
// representation cannot express. The engine leaves such a box in logical order,
// which is the answer that is right whenever the box's outermost level is even —
// and it is here.
//
// It is a real case and not a hypothetical: css-text/white-space's tab-bidi-001
// is exactly this shape, and swapping on the first content item's level put the
// outer span's left border three pixels inside where the reference draws it.
func TestInlineInsetOfAMixedBoxIsLeftAlone(t *testing.T) {
	// The span holds a right-to-left override and then left-to-right text, so
	// its content is levels one and zero and its own edges are at zero.
	//
	//   0..24   the span's left margin
	//   24..48  the overridden Hebrew, one level-one run, reversed with itself
	//   48..72  "cd"
	//   72..96  "ef"
	root := layoutOf(t, 600,
		`<div id="p"><span id="s" style="margin-left: 24px">`+
			`<bdo dir="rtl">`+hebrewAB+`</bdo>cd</span>ef</div>`, bidiBoxCSS)
	runs := runsOf(t, root, "p")

	if got := runAt(t, runs, hebrewAB).X.Px(); got != 24 {
		t.Errorf("the overridden word is at %gpx, want 24 — the span's content is "+
			"not right-to-left throughout, so its margin stays where it was written", got)
	}
	if got := runAt(t, runs, "cd").X.Px(); got != 48 {
		t.Errorf("the span's other text is at %gpx, want 48", got)
	}
	if got := runAt(t, runs, "ef").X.Px(); got != 72 {
		t.Errorf("the text after the span is at %gpx, want 72", got)
	}
}

// TestAnEmptyInlineBoxsInsetTakesTheParagraphsDirection is the fixture the two
// "no level yet" answers needed, and it exists because without it neither of
// them decided anything.
//
// A box with a margin and no content at all has no characters to take a level
// from — and zero is a real level, the left-to-right one, so "no level" and
// "level zero" have to stay distinguishable all the way through. Both places
// that could conflate them were planted with the conflation and nothing in the
// suite moved until this document existed:
//
//   - insetSides gives a box's insets the lowest level anything inside it
//     reached; a box with nothing inside it gets none, rather than getting zero.
//   - the reordering then gives such an item the level of what precedes it, and
//     the *paragraph's* base level where nothing does. That is what rule L1
//     gives a line's leading separators, and it is not zero in a right-to-left
//     paragraph.
//
// Read either as zero and the margin of an empty span at the start of a
// right-to-left line is drawn at the left of the line instead of the right,
// which pushes every word along by its width.
func TestAnEmptyInlineBoxsInsetTakesTheParagraphsDirection(t *testing.T) {
	// The empty span is written first, so it is drawn last: its 24px margin is
	// the rightmost thing on the line and the word is flush left.
	//
	//   0..24   the word
	//   24..48  the empty span's left margin
	root := layoutOf(t, 600,
		`<div id="p" dir="rtl"><span id="s" style="margin-left: 24px"></span>`+
			hebrewAB+`</div>`, bidiBoxCSS)
	runs := runsOf(t, root, "p")

	if got := runAt(t, runs, hebrewAB).X.Px(); got != 0 {
		t.Errorf("the word is at %gpx, want 0 — the empty span before it is drawn "+
			"last on a right-to-left line, so its margin takes the room at the "+
			"right-hand end", got)
	}
}

// TestInlineBorderIsPaintedInTheRoomReservedForIt is the other half of §8.6 and
// the reason the two halves cannot disagree.
//
// insetItems reserves the room and inlinepaint.go draws in it. If one of them
// puts the box's start edge on the left and the other on the right, the border
// is drawn over the words instead of beside them — so this asserts the ink and
// the space together, on a line the reordering turned round.
func TestInlineBorderIsPaintedInTheRoomReservedForIt(t *testing.T) {
	// The same shape as the margin test, with a 24px left border in place of the
	// margin. The span's border box therefore runs 24..72: the border itself at
	// 24..48, and the word it belongs to at 48..72.
	root := layoutOf(t, 600,
		`<div id="p" dir="rtl"><span id="s" style="border-left: 24px solid blue">`+
			hebrewAB+`</span>`+hebrewGD+`</div>`, bidiBoxCSS)

	p := find(t, root, "p")
	if len(p.Lines) != 1 {
		t.Fatalf("the block has %d lines, want 1", len(p.Lines))
	}
	line := p.Lines[0]
	if len(line.Boxes) != 1 {
		t.Fatalf("the line has %d inline box fragments, want 1", len(line.Boxes))
	}
	frag := line.Boxes[0]

	// A run's X is measured from the line box; an inline box's fragment has been
	// absolutised, like every other fragment. The block's content edge is what
	// puts the two in the same space.
	origin := p.ContentRect().X
	if got := frag.BorderRect.X.Sub(origin).Px(); got != 24 {
		t.Errorf("the span's border box starts at %gpx, want 24 — its 24px left "+
			"border is drawn between the word before it and its own word", got)
	}
	if got := frag.BorderRect.W.Px(); got != 48 {
		t.Errorf("the span's border box is %gpx wide, want 48 — a 24px border and "+
			"a 24px word", got)
	}
	if got := frag.Border.Left.Px(); got != 24 {
		t.Errorf("the fragment carries a %gpx left border, want 24", got)
	}
	// And the word sits inside it, at the border box's content edge.
	if got := runAt(t, line.Runs, hebrewAB).X.Px(); got != 48 {
		t.Errorf("the span's word is at %gpx, want 48 — inside its own border", got)
	}
}

// TestReorderingIsLinearInTheLength guards the cost of the two passes §8.6
// added, since both of them look easy to write as a scan per item.
//
// insetSides answers "was every character in this box right-to-left" by
// subtracting running counts taken when the box opened, and "what level are its
// edges at" by folding each box's minimum into its parent's as the stack
// unwinds. Neither rescans a box's content. A quadratic version of either is not
// a slow test — it is a document of a few kilobytes that does not finish.
func TestReorderingIsLinearInTheLength(t *testing.T) {
	if testing.Short() {
		t.Skip("timing")
	}
	// One line of a thousand words, each in its own bordered span, in a
	// right-to-left block: a thousand inset pairs and a thousand runs, all on one
	// line because the block is wide enough to hold them.
	const words = 1000
	src := `<div id="p" dir="rtl" style="width: 100000px">`
	for i := 0; i < words; i++ {
		src += `<span style="border-left: 1px solid blue">` + hebrewAB + `</span>`
	}
	src += `</div>`

	root := layoutOf(t, 100000, src, `#p { font-family: Courier; font-size: 20px }`)
	p := find(t, root, "p")
	if len(p.Lines) != 1 {
		t.Fatalf("the content took %d lines, want 1 — the fixture is meant to put "+
			"every span on one line", len(p.Lines))
	}
	if got := len(p.Lines[0].Runs); got != words {
		t.Errorf("the line holds %d runs, want %d", got, words)
	}
}

// TestASplitInlineKeepsItsInsetOnTheSideTheBlockRunsFrom is §8.6's slice model
// meeting the containing block's direction.
//
// A box broken by a block inside it carries its own inset on the pieces at its
// two ends and nothing on the joins. Which *physical* side that is depends on
// which way the box itself runs: under ltr the piece it begins on is the
// leftmost, under rtl the rightmost — so the same declaration lands on the other
// side of the block.
//
// The direction is set on the containing block here and inherited, which is how
// the suite writes it and is the case that says nothing about *whose* direction
// is read. TestTheBoxsOwnDirectionDecidesWhichEndBeginsIt is the one that does.
//
// A span with nothing in it but a block and a padding on one side is the whole
// of the case, and the suite writes it once each way. The ten pixels are before
// the block under rtl and after it under ltr, and reading the flag as a physical
// side put them after it both times.
func TestASplitInlineKeepsItsInsetOnTheSideTheBlockRunsFrom(t *testing.T) {
	// The padding is on the right, so under ltr it ends the box and under rtl it
	// begins it. The block is 20px tall so that the two pieces are told apart by
	// where they sit rather than by how wide they are.
	const css = noDefaults + `
	#s { padding-right: 10px }
	#b { display: block; height: 20px }`
	insetY := func(t *testing.T, dir string) (before, after bool) {
		t.Helper()
		root := layoutOf(t, 1000,
			`<div id="k" style="direction: `+dir+`"><span id="s"><span id="b"></span></span></div>`,
			css)
		k := find(t, root, "k")
		blockTop := relY(t, find(t, root, "b"), k)
		// Every line box the span's own pieces made, by where it sits relative
		// to the block the span was broken by.
		for _, c := range k.Children {
			if c.Box == nil || c.Box == find(t, root, "b").Box {
				continue
			}
			for _, line := range c.Lines {
				y := c.BorderRect.Y.Add(line.Rect.Y)
				if line.Rect.W <= 0 {
					continue
				}
				if y < blockTop {
					before = true
				} else {
					after = true
				}
			}
		}
		return before, after
	}

	if before, after := insetY(t, "ltr"); before || !after {
		t.Errorf("under ltr the padding-right made a line before=%v after=%v the "+
			"block, want it after only — it is the inset that *ends* the box",
			before, after)
	}
	if before, after := insetY(t, "rtl"); !before || after {
		t.Errorf("under rtl the padding-right made a line before=%v after=%v the "+
			"block, want it before only — in a right-to-left containing block the "+
			"piece the box begins on is the rightmost, so the right inset is the "+
			"one that begins it", before, after)
	}
}

// TestTheBoxsOwnDirectionDecidesWhichEndBeginsIt is §8.6's rule read as it is
// written, and it is written twice — once per direction — with "the element's"
// in both:
//
//	When the element's 'direction' property is 'ltr', the leftmost generated box
//	of the first line box in which the element appears has the left margin, left
//	border and left padding [...] When the element's 'direction' property is
//	'rtl', the rightmost generated box of the first line box [...] has the right
//	padding, right border and right margin.
//
// The element's own, not its containing block's — and until this test the
// difference was unmeasured, because every other document in the suite and in
// this package sets the direction on an ancestor and lets the span inherit it,
// where the two readings agree. CSS2/box has the four combinations as four
// documents; rtl-span-only is a "direction: rtl" span in a left-to-right block
// and its reference gives the *first* line the right inset.
//
// The two halves of the rule are asserted separately because they are two pieces
// of code: which side a *block-in-inline* split suppresses, and which side a
// *line* split draws.
func TestTheBoxsOwnDirectionDecidesWhichEndBeginsIt(t *testing.T) {
	// A block inside the span cuts it in two. The padding is on the right, so
	// under the span's own ltr it ends the box — the piece after the block — and
	// under its own rtl it begins it, whatever the block around it says.
	t.Run("a block-in-inline split", func(t *testing.T) {
		const css = noDefaults + `
		#s { padding-right: 10px }
		#b { display: block; height: 20px }`
		sides := func(t *testing.T, outer, inner string) (before, after bool) {
			t.Helper()
			root := layoutOf(t, 1000,
				`<div id="k" style="direction: `+outer+`">`+
					`<span id="s" style="direction: `+inner+`">`+
					`<span id="b"></span></span></div>`, css)
			k := find(t, root, "k")
			blockTop := relY(t, find(t, root, "b"), k)
			for _, c := range k.Children {
				if c.Box == nil || c.Box == find(t, root, "b").Box {
					continue
				}
				for _, line := range c.Lines {
					if line.Rect.W <= 0 {
						continue
					}
					if c.BorderRect.Y.Add(line.Rect.Y) < blockTop {
						before = true
					} else {
						after = true
					}
				}
			}
			return before, after
		}
		// The span's own direction decides, both times against its container's.
		if before, after := sides(t, "rtl", "ltr"); before || !after {
			t.Errorf("an ltr span in an rtl block put its padding-right "+
				"before=%v after=%v the block, want after only", before, after)
		}
		if before, after := sides(t, "ltr", "rtl"); !before || after {
			t.Errorf("an rtl span in an ltr block put its padding-right "+
				"before=%v after=%v the block, want before only", before, after)
		}
	})

	// A <br> inside the span cuts it in two the other way. The first line draws
	// the box's starting border and the last its ending one, and which physical
	// side each is depends on the span's own direction — so the widths tell them
	// apart without depending on where the lines sit.
	t.Run("a line split", func(t *testing.T) {
		const css = noDefaults + mono + `
		#s { border-left: 4px solid; border-right: 8px solid }`
		borders := func(t *testing.T, outer, inner string) (first, last Edges) {
			t.Helper()
			root := layoutOf(t, 1000,
				`<div id="k" style="direction: `+outer+`">`+
					`<span id="s" style="direction: `+inner+`">a<br>b</span></div>`, css)
			k := find(t, root, "k")
			if len(k.Lines) != 2 {
				t.Fatalf("%d lines, want 2", len(k.Lines))
			}
			for i, line := range k.Lines {
				if len(line.Boxes) != 1 {
					t.Fatalf("line %d has %d inline fragments, want 1", i, len(line.Boxes))
				}
				if i == 0 {
					first = line.Boxes[0].Border
				} else {
					last = line.Boxes[0].Border
				}
			}
			return first, last
		}
		for _, tc := range []struct {
			outer, inner          string
			firstLeft, firstRight float64
			lastLeft, lastRight   float64
		}{
			// ltr: the first line begins the box, so it draws the left border.
			{"ltr", "ltr", 4, 0, 0, 8},
			{"rtl", "ltr", 4, 0, 0, 8},
			// rtl: the first line begins the box at its right.
			{"rtl", "rtl", 0, 8, 4, 0},
			{"ltr", "rtl", 0, 8, 4, 0},
		} {
			first, last := borders(t, tc.outer, tc.inner)
			if first.Left.Px() != tc.firstLeft || first.Right.Px() != tc.firstRight {
				t.Errorf("%s block, %s span: the first line's borders are %g/%g, "+
					"want %g/%g", tc.outer, tc.inner,
					first.Left.Px(), first.Right.Px(), tc.firstLeft, tc.firstRight)
			}
			if last.Left.Px() != tc.lastLeft || last.Right.Px() != tc.lastRight {
				t.Errorf("%s block, %s span: the last line's borders are %g/%g, "+
					"want %g/%g", tc.outer, tc.inner,
					last.Left.Px(), last.Right.Px(), tc.lastLeft, tc.lastRight)
			}
		}
	})
}

// TestAnInsetIsReservedOnTheSideItIsDrawnOn is §8.6's two halves made to agree.
//
// Which side an inline box's margin, border and padding are *drawn* on is
// settled per line, from the box's own direction. How much room they take is
// settled by an item in the line's stream, placed where the reordering puts it.
// Those are two mechanisms answering two different questions, and where they
// disagree the box is drawn somewhere it did not reserve: a "direction: rtl"
// span holding Latin text reorders nothing, so the item stayed at the left of
// the words while the border went to their right, and the fragment came out four
// pixels wide with its own text outside it.
//
// The assertion is the agreement rather than either number: the box's fragment
// has to contain the text it belongs to, whichever way the two run.
func TestAnInsetIsReservedOnTheSideItIsDrawnOn(t *testing.T) {
	const css = noDefaults + mono + `
	#s { border-left: 4px solid; border-right: 8px solid;
	     padding-left: 5px; padding-right: 10px;
	     margin-left: 30px; margin-right: 60px }`
	for _, tc := range []struct{ outer, inner string }{
		{"ltr", "ltr"}, {"ltr", "rtl"}, {"rtl", "ltr"}, {"rtl", "rtl"},
	} {
		root := layoutOf(t, 1000,
			`<div id="k" style="direction: `+tc.outer+`">`+
				`<span id="s" style="direction: `+tc.inner+`">aa<br>bb</span></div>`, css)
		k := find(t, root, "k")
		if len(k.Lines) != 2 {
			t.Fatalf("%s/%s: %d lines, want 2", tc.outer, tc.inner, len(k.Lines))
		}
		for i, line := range k.Lines {
			if len(line.Boxes) != 1 {
				t.Fatalf("%s/%s line %d: %d fragments, want 1",
					tc.outer, tc.inner, i, len(line.Boxes))
			}
			frag := line.Boxes[0]
			// The content area of the fragment: inside its border and padding.
			lo := frag.BorderRect.X.Add(frag.Border.Left).Add(frag.Padding.Left)
			hi := frag.BorderRect.X.Add(frag.BorderRect.W).
				Sub(frag.Border.Right).Sub(frag.Padding.Right)
			if len(line.Runs) == 0 {
				continue
			}
			for _, r := range line.Runs {
				if r.Text == "" {
					continue
				}
				x := line.Rect.X.Add(r.X)
				if x < lo || x.Add(r.Width) > hi {
					t.Errorf("%s block, %s span, line %d: the run %q runs "+
						"%g..%g and its own box's content area is %g..%g — the "+
						"inset was reserved on one side and drawn on the other",
						tc.outer, tc.inner, i, r.Text,
						x.Px(), x.Add(r.Width).Px(), lo.Px(), hi.Px())
				}
			}
		}
	}
}

// TestASideMarginIsUnaffectedByDirection is the other side of the same rule, and
// the one that stops it being applied too widely.
//
// "margin-left" is a left margin whichever way the box runs. What §8.6's two
// halves choose between is which *line* of a broken box carries it, so a box on
// one line carries both of its insets and looks the same either way. The suite
// says so a dozen times over — every bidi-box-model test whose assertion reads
// "side margins should be unaffected by directionality" — and a first attempt at
// the rule above swapped the two on every box and broke sixteen of them.
func TestASideMarginIsUnaffectedByDirection(t *testing.T) {
	runX := func(t *testing.T, dir string) style.Unit {
		t.Helper()
		root := layoutOf(t, 1000,
			`<div id="k">aa <span id="s" style="direction: `+dir+`">bb</span></div>`,
			noDefaults+mono+`#s { margin-left: 40px }`)
		k := find(t, root, "k")
		for _, r := range k.Lines[0].Runs {
			if r.Text == "bb" {
				return k.Lines[0].Rect.X.Add(r.X)
			}
		}
		t.Fatalf("no run for the span")
		return 0
	}
	if ltr, rtl := runX(t, "ltr"), runX(t, "rtl"); ltr != rtl {
		t.Errorf("a span with margin-left starts at %g under ltr and %g under "+
			"rtl; a side margin is the same side either way", ltr.Px(), rtl.Px())
	}
}

// overrideRow is the suite's own fixture for §8.6 under explicit overrides,
// reduced to what can be asserted directly.
//
// The letters a…m are written in an order the RLO, LRO and PDF characters undo,
// so the line reads "abcdefghijklm" — and the spans around c/j/e and around
// i/d/k/b hold letters that end up scattered across it, which is what makes the
// box model interesting: one inline box, several boxes generated for it.
const overrideRow = `<p id="p">a&#x202E;l&#x202D;<span id="one">c&#x202E;j&#x202D;e&#x202E;</span>` +
	`h&#x202D;g&#x202C;f<span id="two">&#x202C;i&#x202C;d&#x202C;k&#x202C;b</span>&#x202C;m</p>`

// visualLetters is the line's text in the order it is drawn, with the format
// characters — which draw nothing — left out.
func visualLetters(t *testing.T, root *Fragment, id string) string {
	t.Helper()
	type at struct {
		x style.Unit
		s string
	}
	var runs []at
	for _, line := range find(t, root, id).Lines {
		for _, r := range line.Runs {
			var keep []rune
			for _, c := range r.Text {
				if c >= 0x202A && c <= 0x202E {
					continue
				}
				keep = append(keep, c)
			}
			if len(keep) == 0 {
				continue
			}
			runs = append(runs, at{line.Rect.X.Add(r.X), string(keep)})
		}
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].x < runs[j].x })
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(r.s)
	}
	return b.String()
}

// TestAnInsetDoesNotDisturbTheReordering is the bug that made an inline box's
// own border rearrange the words around it.
//
// An inset used to take an embedding level of its own — the lowest level inside
// its box — and an item at level 2 dropped into the middle of a level-4 run cuts
// that run in half. Rule L2 then reverses the halves separately, and the line
// comes out in an order UAX #9 never asked for. The suite's bidi-001 and its
// siblings are written to read "abcdefghijklm" through six nested overrides, and
// putting a border on one of the spans was enough to scramble them.
//
// The assertion is the comparison: the same markup with and without a border on
// the spans reads the same, because a border is not something the bidi algorithm
// has an opinion about.
func TestAnInsetDoesNotDisturbTheReordering(t *testing.T) {
	plain := layoutOf(t, 4000, overrideRow, noDefaults+mono+`p { white-space: pre }`)
	if got := visualLetters(t, plain, "p"); got != "abcdefghijklm" {
		t.Fatalf("the overrides alone read %q, want \"abcdefghijklm\" — the "+
			"fixture is wrong before any box model is involved", got)
	}
	bordered := layoutOf(t, 4000, overrideRow, noDefaults+mono+`p { white-space: pre }
		#one, #two { border: 10px solid; margin: 0 50px }`)
	if got := visualLetters(t, bordered, "p"); got != "abcdefghijklm" {
		t.Errorf("with a border on the spans the line reads %q, want "+
			"\"abcdefghijklm\" — an inset is not a character and takes no level", got)
	}
}

// TestAnInsetTakesRoomAtTheEndsOfItsOwnBox is where that room goes once the
// inset is out of the reordering.
//
// §8.6 puts an inline box's margin, border and padding at the two ends of the
// box, and under an override the two ends are the extremes of the several boxes
// generated for it — not the two ends of anything contiguous. #one's letters
// land at c, e and j with other spans' letters between them, so its left inset
// belongs before c and its right inset after j.
func TestAnInsetTakesRoomAtTheEndsOfItsOwnBox(t *testing.T) {
	// One character is 60px wide; the insets are 60 each (50 margin, 10 border).
	root := layoutOf(t, 4000, overrideRow, noDefaults+mono+`p { white-space: pre }
		#one, #two { border: 10px solid; margin: 0 50px }`)
	x := map[string]float64{}
	for _, line := range find(t, root, "p").Lines {
		for _, r := range line.Runs {
			for _, c := range r.Text {
				if c >= 0x202A && c <= 0x202E {
					continue
				}
				x[string(c)] = line.Rect.X.Add(r.X).Px()
				break
			}
		}
	}
	// a b [#two's left inset] ... and #one's left inset between b and c.
	for _, tc := range []struct {
		from, to string
		want     float64
	}{
		{"a", "b", 120}, // a, then #two's left inset, then b
		{"b", "c", 120}, // b, then #one's left inset, then c
		{"c", "d", 60},  // plain neighbours
		{"i", "j", 60},
		{"j", "k", 120}, // j, then #one's right inset, then k
		{"k", "l", 120}, // k, then #two's right inset, then l
		{"l", "m", 60},
	} {
		if got := x[tc.to] - x[tc.from]; got != tc.want {
			t.Errorf("%s to %s is %gpx, want %g", tc.from, tc.to, got, tc.want)
		}
	}
}

// TestOnlyTheEndPiecesOfASplitBoxAreDecorated is §8.6's last sentence:
//
//	All other generated boxes for the element have no horizontal margins,
//	borders or padding.
//
// A box the reordering cut into three draws three boxes, and only the outer two
// carry anything horizontal. Drawing one fragment from the box's leftmost item
// to its rightmost — which is what one piece per line amounts to — paints a
// border straight through the words of whatever sits between them.
func TestOnlyTheEndPiecesOfASplitBoxAreDecorated(t *testing.T) {
	root := layoutOf(t, 4000, overrideRow, noDefaults+mono+`p { white-space: pre }
		#one, #two { border: 10px solid; margin: 0 50px }`)
	p := find(t, root, "p")
	if len(p.Lines) != 1 {
		t.Fatalf("%d lines, want 1", len(p.Lines))
	}
	// The pieces are the line's inline box fragments, not children of anything,
	// so they are found by the element they belong to.
	var pieces []*Fragment
	for _, f := range p.Lines[0].Boxes {
		if f.Box == nil || f.Box.Element == nil {
			continue
		}
		if id, _ := f.Box.Element.Attr("id"); id == "one" {
			pieces = append(pieces, f)
		}
	}
	if len(pieces) != 3 {
		t.Fatalf("#one generated %d boxes, want 3 — its letters land at c, e "+
			"and j with other spans' between them", len(pieces))
	}
	sort.SliceStable(pieces, func(i, j int) bool {
		return pieces[i].BorderRect.X < pieces[j].BorderRect.X
	})
	if got := pieces[0].Border; got.Left.Px() != 10 || got.Right.Px() != 0 {
		t.Errorf("the leftmost piece has borders %g/%g, want 10/0",
			got.Left.Px(), got.Right.Px())
	}
	if got := pieces[1].Border; got.Left.Px() != 0 || got.Right.Px() != 0 {
		t.Errorf("the middle piece has borders %g/%g, want none at all",
			got.Left.Px(), got.Right.Px())
	}
	if got := pieces[2].Border; got.Left.Px() != 0 || got.Right.Px() != 10 {
		t.Errorf("the rightmost piece has borders %g/%g, want 0/10",
			got.Left.Px(), got.Right.Px())
	}
}

// TestAnInvisibleCharacterIsNotAnEndOfTheBox is the rule that keeps §8.6's
// "generated boxes" about boxes that generate something.
//
// A bidi control draws nothing and belongs to whichever element the author wrote
// it in. Under an override it can be reordered to the far end of the line from
// the words of its own span — and then it stood for the span's edge twice over:
// it cut the span's run in two, so the border was painted in pieces with a seam,
// and it stretched the span's extent, so the inset was reserved away from the
// words it belongs to with someone else's text in between.
//
// The suite's bidi-011 is a <span> holding an override whose matching pop is
// written after it, which puts exactly that character exactly there.
func TestAnInvisibleCharacterIsNotAnEndOfTheBox(t *testing.T) {
	// "TE" then a span holding an override and four letters, then a pop and two
	// more letters. The override reverses the span's letters and reaches past
	// its end, so the two letters after it land between the span's control and
	// the span's words.
	root := layoutOf(t, 4000,
		`<div id="k">TE<span id="s">&#x202E;TSET</span>&#x202D;ST</div>`,
		noDefaults+mono+`#s { border: 10px solid }`)
	k := find(t, root, "k")
	if len(k.Lines) != 1 {
		t.Fatalf("%d lines, want 1", len(k.Lines))
	}
	var pieces []*Fragment
	for _, f := range k.Lines[0].Boxes {
		if f.Box == nil || f.Box.Element == nil {
			continue
		}
		if id, _ := f.Box.Element.Attr("id"); id == "s" {
			pieces = append(pieces, f)
		}
	}
	if len(pieces) != 1 {
		t.Fatalf("the span generated %d boxes, want 1 — the only thing between "+
			"its words and its own control draws nothing", len(pieces))
	}
	// And the border encloses the span's letters rather than reaching back past
	// the text that sits between them and the control.
	frag := pieces[0]
	lo := frag.BorderRect.X.Add(frag.Border.Left)
	hi := frag.BorderRect.X.Add(frag.BorderRect.W).Sub(frag.Border.Right)
	for _, r := range k.Lines[0].Runs {
		if r.Text != "TSET" {
			continue
		}
		x := k.Lines[0].Rect.X.Add(r.X)
		if x < lo || x.Add(r.Width) > hi {
			t.Errorf("the span's letters run %g..%g and its own box holds "+
				"%g..%g", x.Px(), x.Add(r.Width).Px(), lo.Px(), hi.Px())
		}
	}
	// The letters that are not the span's are outside it.
	for _, r := range k.Lines[0].Runs {
		if r.Text != "ST" {
			continue
		}
		x := k.Lines[0].Rect.X.Add(r.X)
		if x >= lo && x < hi {
			t.Errorf("%q at %g is inside the span's box %g..%g; the span's "+
				"extent was stretched by a character that draws nothing",
				r.Text, x.Px(), lo.Px(), hi.Px())
		}
	}
}
