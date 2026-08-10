package render

import (
	"strings"

	"github.com/mgilbir/pdf0/css"
	"github.com/mgilbir/pdf0/style"
)

// CSS 2.1 §11.1: clipping.
//
// # What clipping is here, and what it is not
//
// A PDF page does not scroll. So "overflow: scroll" and "overflow: auto" are
// not approximated by "hidden" — they *are* "hidden" in this medium, exactly
// and by definition: §11.1.1 says the two scrolling values clip the content and
// provide a scrolling mechanism, and a scrolling mechanism on a sheet of paper
// is not a thing that is missing, it is a thing that has no meaning. There is
// no scrollbar to reserve room for either, since the box is never scrolled.
// That is why none of the four values raises a finding: the engine is not doing
// less than the property asks for.
//
// # Where a clip is expressed
//
// Not as a push/pop pair in the display list. A pair can be left unbalanced,
// and an unbalanced clip in a PDF content stream does not lose a box — it
// blanks the rest of the page, because everything after the missing Q is drawn
// through a clip that was meant to have ended. A malformed document must not be
// able to do that, and "the code is careful" is not the standard: the
// representation itself has to make it impossible.
//
// So a clip travels *on* the operation it applies to, and nothing in the
// display list has any state that spans two operations:
//
//   - A FillRect is intersected with its clip when it is built. That is exact —
//     a rectangle clipped by a rectangle is a rectangle — and it costs the list
//     nothing. It also keeps the overflow-page guardrail honest without that
//     guardrail knowing clipping exists: a fill that is clipped away is never
//     emitted, and one that is partly clipped is emitted at its visible size,
//     which is what a guard measuring where ink reaches should see.
//   - A TileImage already carries the area it may paint. The clip meets that
//     rectangle, which is the same operation by another name.
//   - A DrawImage and a DrawText carry a Clip. Neither can be reduced to an
//     intersection: shrinking a picture's rectangle would rescale it rather
//     than crop it, and a glyph cannot be cut by arithmetic at all. These are
//     the two that reach PDF's own clipping path, and each does so inside its
//     own q/Q, so the nesting is constant and a missing Q is not expressible.
//
// The cost of the choice is paid in the reftest comparison, and it is small:
// picFills needs no change at all, because a fill has already been cut down to
// what it paints. Only the text comparison had to learn anything, and what it
// learnt is one rule — a run whose ink lies entirely outside its clip marks no
// paper.

// Clip is the area an operation may mark.
//
// Active is not redundant with an empty rectangle, and conflating the two would
// be the classic sentinel collision: "clip: rect(0, 0, 0, 0)" asks for a clip
// that admits nothing, which is the *opposite* of no clip at all. The engine
// never emits an operation whose clip is active and empty — there would be
// nothing to draw — so the distinction is only ever read one way, but a reader
// of the display list should not have to know that to be safe.
type Clip struct {
	Rect   Rect
	Active bool
}

// meet is the clip two clips have in common.
func (c Clip) meet(o Clip) Clip {
	switch {
	case !o.Active:
		return c
	case !c.Active:
		return o
	}
	return Clip{Rect: c.Rect.Intersect(o.Rect), Active: true}
}

// with narrows a clip by a rectangle.
func (c Clip) with(r Rect) Clip { return c.meet(Clip{Rect: r, Active: true}) }

// blocks reports whether nothing at all may be painted through this clip.
func (c Clip) blocks() bool { return c.Active && c.Rect.Empty() }

// admits reports whether a rectangle is wholly inside the clip, so that an
// operation covering it needs no clip of its own.
func (c Clip) admits(r Rect) bool { return !c.Active || c.Rect.Contains(r) }

// hides reports whether a rectangle is wholly outside the clip, so that an
// operation covering it paints nothing and need not be emitted.
func (c Clip) hides(r Rect) bool { return c.Active && c.Rect.Intersect(r).Empty() }

// maxClipDepth bounds how many nested clipping boxes contribute to one clip.
//
// The rectangle a clip is represented by does not grow with nesting — meeting
// two rectangles gives one rectangle — so this is not a bound on memory. It is
// a bound on the one thing a hostile document controls here that the engine
// would otherwise follow without limit: how far the clip resolution walks down
// a chain of boxes that each clip.
//
// Past the bound the clip becomes empty rather than being left as it was, and
// the direction is the whole point. Every further clip could only ever shrink
// the region, so stopping and keeping what has been resolved would show content
// that a box the engine gave up on was hiding — a document could bury a clip
// past the limit and have the thing it clipped painted. Clipping to nothing
// loses content instead, which is visible, and is reported.
//
// It is a variable rather than a constant so that a test can lower it far
// enough to watch it fire. A document with two hundred and fifty six nested
// clipping boxes is already refused by the HTML parser's own nesting cap, so a
// bound that could only be reached through that one would never be seen to
// work — which this repository has learnt is the same as not working.
var maxClipDepth = 64

// resolveClips settles, for every fragment, the area its own marks may paint
// and the area its contents may.
//
// The two differ, and the difference is §11.1.1 against §11.1.2. A box's
// "overflow" clips what is *inside* it and not the box itself: an element with
// "overflow: hidden" and a ten-pixel border still draws that border, which lies
// outside the padding box the content is clipped to. A box's "clip" clips the
// element's own rendered content as well, background included — which is what
// makes "clip: rect(0, 0, 0, 0)" on an absolutely positioned <div> with nothing
// but a background colour show nothing at all, and is what forty of the
// suite's clip tests assert.
//
// It runs after everything else in Layout because it needs final rectangles: a
// padding box in page coordinates, for boxes that were positioned last of all.
func (l *layouter) resolveClips(root *Fragment) {
	if root == nil {
		return
	}
	// What each box's *contents* inherit, so that an out-of-flow box can be
	// given the clip of its containing block rather than of wherever in the
	// fragment tree it ended up hanging.
	inherited := map[*Box]Clip{}
	var reported bool

	var walk func(f *Fragment, from Clip, depth int)
	walk = func(f *Fragment, from Clip, depth int) {
		if f.Box == nil {
			return
		}
		if f.Box.Position.outOfFlow() {
			// §11.1.1: an absolutely positioned box is not clipped by an
			// ancestor's overflow unless that ancestor is in its containing
			// block chain. This is the rule the "abspos-overflow" family of the
			// suite is written about and the one implementations get wrong, and
			// the shape of the mistake is instructive: the box hangs, in the
			// fragment tree, from whatever block container it was written
			// inside, so inheriting the clip from *there* is both the obvious
			// thing to do and exactly the bug. A tooltip written inside a
			// scrolling panel but positioned against the page must not be cut
			// off by the panel.
			from, depth = l.clipFromContainingBlock(f.Box, inherited)
		}
		self := from.meet(l.clipRectOf(f))
		content := self
		if l.overflowClips(f.Box) {
			// The padding box, not the border box and not the content box.
			// §11.1.1 says the content is clipped to the element's *padding*
			// edge, which is why a scrolled panel shows its content running
			// under its own padding and stopping at its border.
			content = self.with(f.PaddingRect())
			depth++
		}
		if self.Active || content.Active {
			if depth > maxClipDepth {
				content = Clip{Active: true}
				self = Clip{Active: true}
				if !reported {
					reported = true
					l.rec.Report(RuleLimit, AtHTML(offsetOf(f.Box)),
						"boxes that clip their contents are nested deeper than this "+
							"engine will resolve; nothing inside was drawn")
				}
			}
		}
		f.clipSelf = self
		f.clipContent = content
		inherited[f.Box] = content
		// An inline box's own fragments, one per line it was broken across.
		// They are not children — see LineFragment.Boxes — and they are content
		// of the block container that holds the line, so an "overflow: hidden"
		// on that container cuts a <span>'s background exactly as it cuts the
		// words in it. Nothing inline can clip in its own right: §11.1.1's
		// property does not apply to an inline box, and §11.1.2's applies only
		// to a positioned one, which an inline box is not by the time it
		// reaches a line.
		for i := range f.Lines {
			for _, ib := range f.Lines[i].Boxes {
				ib.clipSelf, ib.clipContent = content, content
			}
		}
		for _, c := range f.Children {
			walk(c, content, depth)
		}
	}
	walk(root, Clip{}, 0)
}

// clipFromContainingBlock is the clip an out-of-flow box inherits: the one its
// containing block passes to its contents, rather than the one in force where
// the box was written.
//
// It mirrors containingBlockFor exactly, including the skip over a positioned
// ancestor that produced no fragment, because the two have to agree: a box
// resolved against one rectangle and clipped by another is a box in a place
// nothing chose.
//
// The depth returned is how many clipping boxes that chain already has in it,
// so that the bound counts a chain rather than a path through the fragment
// tree. A box with no positioned ancestor is clipped by nothing — its
// containing block is the page — and that is the case abspos-overflow-001 is.
func (l *layouter) clipFromContainingBlock(b *Box, inherited map[*Box]Clip) (Clip, int) {
	if b.Position == PositionFixed {
		return Clip{}, 0
	}
	for anc := b.Parent; anc != nil; anc = anc.Parent {
		if !anc.Position.positioned() {
			continue
		}
		if _, ok := l.positioned[anc]; !ok {
			// An inline-level positioned ancestor, which produces no fragment.
			// containingBlockFor skips it and reports; this must skip it too,
			// silently, since the report has already been made there.
			continue
		}
		c := inherited[anc]
		return c, clipDepthOf(c)
	}
	return Clip{}, 0
}

// clipDepthOf is how a resolved clip counts against the nesting bound.
//
// A clip is one rectangle however many boxes narrowed it, so the count cannot
// be recovered from the value. One is the honest answer for "something clips
// here": it keeps the bound counting the chain below an out-of-flow box from
// scratch, which is right, because that chain is a new descent.
func clipDepthOf(c Clip) int {
	if c.Active {
		return 1
	}
	return 0
}

// overflowClips reports whether a box clips what is inside it.
//
// Two conditions, and the second is what overflow-applies-to-001 is for.
// "overflow" applies to block containers and to boxes that establish a
// formatting context — so it does nothing at all on a plain inline box, and it
// does work on a table, which is neither a block container nor flow content.
// An engine that read the property off any box would clip a <span>'s children
// to a padding box that inline layout never gave it.
//
// The root element is the exception §11.1.1 names: its overflow is propagated
// to the viewport rather than applied to its own box, and in a paged medium the
// viewport is the page, which already bounds everything. <body> is the second
// half of the same rule — when the root's own overflow is visible, the body's
// is what propagates, and the body then behaves as though it were visible.
func (l *layouter) overflowClips(b *Box) bool {
	if b == nil || b.Element == nil {
		return false
	}
	if !overflowIsScrollable(b.Style) {
		return false
	}
	switch b.Inner {
	case InnerFlow:
		// Only a block-level one; box.go turns a block-level "overflow: hidden"
		// into a flow root, so a box still in this state and block-level has
		// visible overflow and cannot reach here.
		if b.Outer != OuterBlock {
			return false
		}
	case InnerText, InnerTableColumn, InnerTableColumnGroup:
		return false
	}
	if l.propagatesOverflow(b) {
		return false
	}
	return true
}

// propagatesOverflow reports whether a box's overflow goes to the viewport
// instead of clipping the box.
func (l *layouter) propagatesOverflow(b *Box) bool {
	if b.Parent == nil {
		// The root element.
		return true
	}
	if b.Parent.Parent != nil || !strings.EqualFold(elementName(b), "body") {
		return false
	}
	// A <body> whose parent is the root. Its overflow propagates only when the
	// root did not use its own — otherwise the root's has already gone to the
	// viewport and the body's applies to the body.
	return !overflowIsScrollable(b.Parent.Style)
}

func elementName(b *Box) string {
	if b == nil || b.Element == nil {
		return ""
	}
	return b.Element.Name
}

// clipRectOf is §11.1.2's "clip" property, resolved against a fragment's border
// box.
//
// The property applies to absolutely positioned elements and to nothing else,
// which is what clip-102 asserts by putting "clip: rect(0, 0, 0, 0)" on a
// static <div> and "clip: inherit" on the positioned one inside it: the static
// box must not clip, and the positioned box must, from a value it inherited.
func (l *layouter) clipRectOf(f *Fragment) Clip {
	b := f.Box
	if b == nil || !b.Position.outOfFlow() {
		return Clip{}
	}
	raw := strings.TrimSpace(b.Style["clip"])
	if raw == "" || strings.EqualFold(raw, "auto") {
		return Clip{}
	}
	sides, ok := l.parseClipShape(b, raw)
	if !ok {
		// Not a shape this property accepts — "circle(10px, 25px)", or a
		// rect() whose arguments mix commas and spaces. §4.2 drops such a
		// declaration, so the initial value stands and nothing clips. No
		// finding: dropping it is the correct handling rather than a
		// shortcoming, and reporting would tell an author their valid CSS was
		// unsupported.
		return Clip{}
	}
	return Clip{Rect: sides.against(f.BorderRect), Active: true}
}

// clipSides is a rect() shape: four offsets, each of which may be auto.
//
// All four are measured from the *top left* of the border box — right and
// bottom are distances from the left and top edges rather than from the edges
// they are named after. That reads as a mistake in the property and is what
// every implementation does, and the suite discriminates: clip-092 puts
// "rect(+7.5ex, +7.5ex, +7.5ex, +7.5ex)" on a three-inch square and asserts
// nothing is visible. Measuring right and bottom from their own edges would
// leave a large square of red in the middle of that box.
type clipSides struct {
	top, right, bottom, left style.Unit
	topAuto, rightAuto       bool
	bottomAuto, leftAuto     bool
}

// against places the shape on a border box.
func (s clipSides) against(border Rect) Rect {
	x0, x1 := border.X, border.Right()
	y0, y1 := border.Y, border.Bottom()
	if !s.leftAuto {
		x0 = border.X.Add(s.left)
	}
	if !s.rightAuto {
		x1 = border.X.Add(s.right)
	}
	if !s.topAuto {
		y0 = border.Y.Add(s.top)
	}
	if !s.bottomAuto {
		y1 = border.Y.Add(s.bottom)
	}
	// Written as a corner pair and converted once, so that a shape whose right
	// is left of its left produces a negative extent that Empty reports rather
	// than a rectangle of some plausible size. "rect(0, 0, 0, 0)" is the
	// everyday case of exactly that.
	return Rect{X: x0, Y: y0, W: x1.Sub(x0), H: y1.Sub(y0)}
}

// parseClipShape reads a rect() shape.
//
// CSS 2.1 requires a user agent to accept the offsets separated by commas and
// permits it to accept them separated by white space. Both are accepted here;
// a mixture of the two is not, which is what clip-rect-001 asserts — it writes
// five malformed declarations and one whitespace-separated one and requires
// the whitespace form to be the one that wins.
func (l *layouter) parseClipShape(b *Box, raw string) (clipSides, bool) {
	vals, _ := css.ParseComponentValues(raw)
	var fn *css.ComponentValue
	for i := range vals {
		v := vals[i]
		if v.IsToken() && v.Token.Kind == css.Whitespace {
			continue
		}
		if fn != nil {
			// A second value beside the shape: not a clip.
			return clipSides{}, false
		}
		if !v.IsFunction() || !strings.EqualFold(v.Token.Value, "rect") {
			return clipSides{}, false
		}
		fn = &vals[i]
	}
	if fn == nil {
		return clipSides{}, false
	}
	args, ok := splitClipArgs(fn.Values)
	if !ok || len(args) != 4 {
		return clipSides{}, false
	}

	ctx := l.clipLengthContext(b, raw)
	var out clipSides
	slots := [4]struct {
		value *style.Unit
		auto  *bool
	}{
		{&out.top, &out.topAuto},
		{&out.right, &out.rightAuto},
		{&out.bottom, &out.bottomAuto},
		{&out.left, &out.leftAuto},
	}
	for i, arg := range args {
		length, _, ok := style.ParseLength(arg, ctx)
		if !ok {
			return clipSides{}, false
		}
		switch length.Kind {
		case style.LengthAuto:
			*slots[i].auto = true
		case style.LengthAbsolute:
			*slots[i].value = length.Value
		default:
			// A percentage. The property takes <length> or auto and nothing
			// else, so this is an invalid declaration rather than one that
			// needs a basis.
			return clipSides{}, false
		}
	}
	return out, true
}

// clipLengthContext is what a font-relative offset in a rect() resolves
// against. It is the box's own font, exactly as for every other length on it.
func (l *layouter) clipLengthContext(b *Box, raw string) style.LengthContext {
	var zero style.Unit
	var haveMetrics bool
	if usesCh(raw) {
		zero, haveMetrics = l.zeroAdvance(b)
	}
	var xh style.Unit
	var haveXHeight bool
	if usesEx(raw) {
		xh, haveXHeight = l.xHeightOf(b)
	}
	return l.lengthContext(b, zero, haveMetrics, xh, haveXHeight)
}

// splitClipArgs divides a rect()'s arguments, insisting that one separator is
// used throughout.
func splitClipArgs(vals []css.ComponentValue) ([][]css.ComponentValue, bool) {
	var out [][]css.ComponentValue
	var cur []css.ComponentValue
	commas, gaps := 0, 0
	pending := false
	for _, v := range vals {
		if v.IsToken() && v.Token.Kind == css.Whitespace {
			if len(cur) > 0 {
				pending = true
			}
			continue
		}
		if v.IsToken() && v.Token.Kind == css.Comma {
			commas++
			out = append(out, cur)
			cur = nil
			pending = false
			continue
		}
		if pending {
			// White space did the separating here.
			gaps++
			out = append(out, cur)
			cur = nil
			pending = false
		}
		cur = append(cur, v)
	}
	out = append(out, cur)
	if commas > 0 && gaps > 0 {
		return nil, false
	}
	for _, arg := range out {
		if len(arg) == 0 {
			return nil, false
		}
	}
	return out, true
}
