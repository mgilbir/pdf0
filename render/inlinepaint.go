package render

import (
	"github.com/mgilbir/pdf0/style"
)

// The background and the border of a non-replaced inline box.
//
// # Why an inline box is not a rectangle
//
// Everything else this engine paints a background on is a box with one border
// rectangle. An inline box is not: CSS 2.1 §8.6 gives it a *slice* model, so a
// <span> broken across three lines paints three fragments, and the three are not
// interchangeable. The left margin, border and padding belong to the fragment
// the box begins on and the right ones to the fragment it ends on; the fragments
// between carry neither.
//
// The room those insets take is already reserved — insetItems does it, and it
// takes the same decisions this file does about which side belongs to which
// fragment, from the same two flags. What was missing was the ink.
//
// # What decides a fragment's height
//
// Not the line box. §10.6.1 makes the height of a non-replaced inline box's
// content area depend on the font rather than on the line it sits in, so a
// fragment reaches the font's ascent above the baseline and its descent below,
// whatever the line-height is. That is why a "line-height: 3" paragraph does not
// paint a triple-height stripe behind a highlighted span, and why a
// "line-height: 0.5" one paints a stripe *taller* than the line it is on.
//
// The vertical padding and border are then added outside that, and they are
// painted even though §8.3, §8.4 and §8.5 keep them out of layout: an inline
// box's vertical padding bleeds over the lines above and below without moving
// any of them. That asymmetry — the ink is there and the layout does not know
// about it — is the reason these fills are marked Overhang, and FillRect says
// what that costs.
//
// # §8.6's bidi box model, and where it still stops
//
// A fragment's extent here is the span from the leftmost to the rightmost of the
// items that belong to it, insets included — so it follows insetSides without
// having to know about it. §8.6 puts the left margin, border and padding on the
// box's leftmost generated box; insetSides puts the room for them there, this
// takes the leftmost edge of that room as the fragment's, and the two agree by
// construction rather than by two readings of the same rule.
//
// What is still not done is a box whose content a reordering splits into two
// visual pieces on one line. It paints one rectangle covering both, where a
// browser paints two.
//
// Outline is not painted either. Nothing in this engine paints one, on an inline
// box or on any other, so an inline box is not the place to start.

// maxInlineDecorations bounds how many of these fragments one document may
// produce.
//
// The count is a product of two things a document controls independently: the
// number of line boxes an inline box is broken across, and how deeply inline
// boxes are nested — a background on each of 200 nested spans over 500 lines is
// a hundred thousand fragments from a document of a few kilobytes. Neither
// existing bound holds it: the HTML parser's nesting cap and maxBoxes bound the
// *tree*, and this is a product of the tree with the lines, which is bounded by
// neither.
//
// Only a box with something to draw is counted, which is what keeps an ordinary
// document at zero — see paintedInlines. The cap is what holds the document that
// draws on every one of them.
//
// 65536 is far past any real document. This engine lays out one page, so a
// fragment per line of it with a hundred nested backgrounds is still an order of
// magnitude below.
//
// It is a variable rather than a constant so that a test can lower it far enough
// to watch it fire. A bound that has only ever been observed not to trip is one
// nobody knows works.
var maxInlineDecorations = 1 << 16

// inlinePiece is the part of one inline box that lies on one line box, before
// the box model has been resolved against it.
//
// It is recorded rather than turned into a fragment on the spot because whether
// it is the box's *last* piece is not known until the whole inline formatting
// context has been laid out, and that is what decides which fragment carries the
// box's right margin, border and padding.
type inlinePiece struct {
	box *Box
	// line is the index of the line box in the block fragment's Lines.
	line int
	// left and right are the extents of the box's items on that line, measured
	// from the block's content edge. They are the *margin* extents where the
	// piece carries an inset, because the inset item insetItems emits covers the
	// margin as well as the border and the padding.
	left, right style.Unit
	// baseline is where *this box's* baseline sits, from the block's content
	// edge: the line's own, moved by §10.8.1's vertical-align applied to this
	// box. It is the box's rather than the line's so that a raised <span>'s
	// background is raised with its words — the ink and the room it sits in have
	// to come apart nowhere.
	baseline style.Unit
	// first says no earlier line held this box, so this piece begins it.
	first bool
}

// inlineDecor collects the pieces over one inline formatting context.
//
// One of these serves a whole call to inlineContent rather than a whole
// document, which is what makes "first" and "last" mean the right thing when a
// block is laid out twice: settle throws a fragment away and lays the box out
// again, and a record kept on the layouter would remember the discarded
// attempt's lines and refuse to begin the box a second time.
type inlineDecor struct {
	l *layouter
	// containing is the width a percentage margin or padding on an inline box
	// resolves against, which is the containing block's and not the line's.
	containing style.Unit
	// strut is the block's own line metrics, which is what §10.8.1 measures a
	// vertical-align keyword against — "text-top" is the top of the parent's
	// content area. It is the same value stackLine used, so a box's ink and its
	// text are moved by the same arithmetic.
	strut  strut
	pieces []inlinePiece
	// last is the index of the most recent piece made for each box, which is
	// what finish needs to find the fragment that ends it.
	last map[*Box]int
}

// addLine records what each painting inline box occupied on one line.
//
// at is where the line box's own left edge sits within the block's content box,
// with §16.2's alignment shift already in it, so that adding an item's offset
// within the line gives a coordinate in the same space the line boxes are in.
func (d *inlineDecor) addLine(index int, items []inlineItem, xs []style.Unit,
	at, baseline style.Unit, stack *lineStack) {

	// The pieces made for this line, so that a box met twice on it extends its
	// piece rather than starting another. A linear scan rather than a map: the
	// number of painting inline boxes on one line is a handful, and a map here
	// would be an allocation on every line of every document.
	start := len(d.pieces)

	for k, item := range items {
		chain := d.l.inlineChain(item)
		if len(chain) == 0 {
			continue
		}
		left := at.Add(xs[k])
		right := left.Add(item.width)
		for _, box := range chain {
			at := -1
			for j := start; j < len(d.pieces); j++ {
				if d.pieces[j].box == box {
					at = j
					break
				}
			}
			if at >= 0 {
				if left < d.pieces[at].left {
					d.pieces[at].left = left
				}
				if right > d.pieces[at].right {
					d.pieces[at].right = right
				}
				continue
			}
			if !d.room(box) {
				continue
			}
			_, seen := d.last[box]
			// §10.8.1's vertical-align on this box, which moved its text and has
			// to move its ink by exactly as much. It is the *box's* own
			// accumulation and not the item's: the item carries the sum down to
			// the innermost box it sits in, and a fragment for a box halfway up
			// that chain is placed by the sum down to itself.
			//
			// The extents are the box's line-height split around its baseline —
			// §10.8's inline box — rather than the font's content area the
			// fragment is drawn over, which is §10.6.1's and a different
			// question.
			base := baseline
			if va, ok := d.l.inlineAligns[box]; ok {
				above, below := d.l.leading(box)
				base = base.Add(stack.shift(va, above, below))
			}
			d.pieces = append(d.pieces, inlinePiece{
				box: box, line: index, left: left, right: right,
				baseline: base, first: !seen,
			})
			if d.last == nil {
				d.last = make(map[*Box]int)
			}
			d.last[box] = len(d.pieces) - 1
		}
	}
}

// room reports whether another piece may be recorded, and says so once when it
// refuses.
func (d *inlineDecor) room(b *Box) bool {
	if d.l.inlineDecorations < maxInlineDecorations {
		d.l.inlineDecorations++
		return true
	}
	if !d.l.inlineDecorCapped {
		d.l.inlineDecorCapped = true
		d.l.rec.Report(RuleLimit, AtHTML(offsetOf(b)),
			"more inline boxes have a background or a border to paint, over more "+
				"lines, than this engine will draw; the rest were left undrawn and "+
				"their text is unaffected")
	}
	return false
}

// finish turns the pieces into fragments and hangs each on its line.
//
// The box model is resolved here rather than in addLine because §8.6's slice
// model needs both ends of the box: a fragment carries the box's left margin,
// border and padding only if it begins the box, and its right ones only if it
// ends it, and the second of those is a fact about every other line.
func (d *inlineDecor) finish(parent *Fragment) {
	for i := range d.pieces {
		p := &d.pieces[i]
		b := p.box
		if p.line >= len(parent.Lines) {
			// A bounds check on an index the caller computed, not a case that
			// happens: addLine is given the position the line is about to be
			// appended at, on the statement before the append. It is here because
			// the alternative to a skip is a panic on an untrusted document, and
			// because the two are far enough apart in inlineContent for a later
			// change to break the invariant quietly.
			continue
		}

		margin := d.l.edges(b, "margin", d.containing)
		border := d.l.borderWidths(b)
		padding := d.l.paddingOf(b, d.containing)

		// §8.6, and the same two flags insetItems reads, mapped to sides the
		// same way: a piece of an inline box split by a block inside it does not
		// begin or does not end the box, and neither does a line that continues
		// one. Which physical side "begins" it is the containing block's
		// business — see splitInsetSides.
		noLeft, noRight := splitInsetSides(b)
		// Which of the box's two ends this piece is, and then which physical side
		// each end is. §8.6 gives the piece that *begins* the box the box's
		// starting inset, and for an element whose own direction is right-to-left
		// the starting inset is the right one — see beginsAtRight.
		//
		// Reading "first piece" as "left inset" is the left-to-right half of the
		// rule written down as though it were the whole of it, and it drew the
		// border down the wrong side of every right-to-left inline box broken
		// over lines. CSS2/box/rtl-basic and rtl-span-only are the two documents.
		keepLeft, keepRight := p.first, d.last[b] == i
		if beginsAtRight(b) {
			keepLeft, keepRight = keepRight, keepLeft
		}
		if !keepLeft || noLeft {
			margin.Left, border.Left, padding.Left = 0, 0, 0
		}
		if !keepRight || noRight {
			margin.Right, border.Right, padding.Right = 0, 0, 0
		}
		// §8.3: margin-top and margin-bottom do not apply to a non-replaced
		// inline box at all. They are zeroed so that the fragment says what was
		// used — a border and a padding on this axis are painted and a margin is
		// not — and nothing reads them: MarginRect is never asked of one of these,
		// which a planted defect confirmed by setting both to 99 and breaking no
		// test. It is a statement about the value rather than a computation
		// anything depends on, and it is written down as one.
		margin.Top, margin.Bottom = 0, 0

		// §10.6.1: the content area is the font's, not the line's.
		st := d.l.strutFor(b)
		x := p.left.Add(margin.Left)
		frag := &Fragment{
			Box: b, Margin: margin, Border: border, Padding: padding,
			BorderRect: Rect{
				X: x,
				Y: p.baseline.Sub(st.ascent).Sub(padding.Top).Sub(border.Top),
				W: p.right.Sub(margin.Right).Sub(x),
				H: st.ascent.Add(st.descent).
					Add(padding.Vertical()).Add(border.Vertical()),
			},
			// §9.4.3's displacement, accumulated over the inline boxes this one
			// sits inside and including its own. It is folded into the position
			// by absolutise rather than applied at paint time, because a
			// background image is placed against the rectangle the box is drawn
			// at and this is the only rectangle it has.
			Offset: d.l.inlineOffsets[b],
		}
		parent.Lines[p.line].Boxes = append(parent.Lines[p.line].Boxes, frag)
	}
}

// inlineChain is the inline boxes an item sits inside that have something to
// paint, outermost first.
func (l *layouter) inlineChain(item inlineItem) []*Box {
	start := item.box
	if start == nil {
		return nil
	}
	if item.atomicBox != nil {
		// A replaced element or an inline-block has a fragment of its own, and
		// that fragment's background and border are painted by the machinery
		// every other box uses. What is wanted here is what encloses it.
		start = start.Parent
	}
	return l.paintedInlines(start)
}

// paintedInlines walks up from a box to the inline boxes around it, keeping the
// ones with a background or a border.
//
// The walk stops at the first ancestor that is not an inline box, which is the
// block container whose lines these are — and at an atomic inline, which is a
// formatting context of its own and paints itself.
//
// A text box is walked *through* rather than kept, and that is load-bearing
// rather than tidiness: a text box carries its parent element's whole computed
// style, background-color and all, so keeping it would paint the parent's
// background a second time — and would paint a *block's* background over its own
// text, since the text box inside a <p> is an inline box by this test.
func (l *layouter) paintedInlines(b *Box) []*Box {
	if b == nil {
		return nil
	}
	if got, ok := l.inlineChains[b]; ok {
		return got
	}
	var out []*Box
	for cur := b; cur != nil && cur.Outer == OuterInline; cur = cur.Parent {
		if cur.Replaced != nil || isAtomicInline(cur) {
			break
		}
		if cur.IsText() {
			continue
		}
		if l.inlinePaints(cur) {
			out = append(out, cur)
		}
	}
	// Outermost first, which is tree order among boxes that nest — and so the
	// order Appendix E paints them in, each over the one it is inside.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	l.inlineChains[b] = out
	return out
}

// inlinePaints reports whether an inline box has anything to draw.
//
// It is asked before a fragment is made rather than after, because the fragment
// is what costs: an ordinary document's inline boxes are <em> and <a> with no
// background and no border, and making a rectangle for each of them on each line
// would be work in proportion to the document that nothing would ever read.
func (l *layouter) inlinePaints(b *Box) bool {
	if got, ok := l.inlineDraws[b]; ok {
		return got
	}
	draws := l.hasOwnBackground(b) || l.borderWidths(b) != (Edges{})
	l.inlineDraws[b] = draws
	return draws
}
