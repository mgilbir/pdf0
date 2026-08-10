package render

import "github.com/mgilbir/pdf0/style"

// Floats: CSS 2.1 §9.5, and the clearance of §9.5.2.
//
// A float is the oldest out-of-flow mechanism CSS has and the only one this
// engine implements. The box is taken out of the normal flow, shifted to one
// edge of its containing block, and pushed as far up as it will go — and then
// the line boxes of everything that follows are *shortened* so the text runs
// beside it rather than under it. That second half is what makes floats worth
// having, and it is the half an engine can quietly not do: a float that is
// merely positioned and never avoided produces a page where an image sits on top
// of a paragraph, which looks like a painting bug rather than a layout one.
//
// # Why the geometry lives in a context of its own
//
// Everything else in this engine is positioned relative to its parent. A float
// cannot be: it is placed relative to the block formatting context it belongs
// to, and that context spans every descendant of the box that established it. A
// float declared inside a <div> shortens the lines of the <p> that comes *after*
// that div, so the two have to be measured against the same origin even though
// neither is an ancestor of the other.
//
// So a floatContext holds rectangles in the coordinates of the content box of
// the formatting-context root, every box inside it knows its own offset from
// that origin, and the translation between the two happens at each step of the
// walk. §9.4.1 decides where a new context begins — a float, a box with
// overflow other than visible, an inline-block, an explicit flow-root — and each
// of those gets a fresh, empty context, which is the whole reason "overflow:
// hidden" is the idiom for containing a float.
//
// # Margin boxes, not border boxes
//
// The rectangles here are *margin* boxes. §9.5 says a line box beside a float is
// shortened to make room for the float's margin box, and a float's own margins
// never collapse with anything — so the margin is part of the obstacle rather
// than something to be resolved against a neighbour. Storing border boxes and
// adding the margin at each query would give the same answers for the everyday
// case and the wrong ones for a float with a negative margin, which is a
// deliberate technique for letting text overlap an image.

// FloatSide is the value of the float property.
type FloatSide uint8

const (
	// FloatNone leaves the box in the normal flow.
	FloatNone FloatSide = iota
	FloatLeft
	FloatRight
)

func (f FloatSide) String() string {
	switch f {
	case FloatLeft:
		return "left"
	case FloatRight:
		return "right"
	}
	return "none"
}

// ClearSide is the value of the clear property: which sides of earlier floats a
// box refuses to sit beside.
type ClearSide uint8

const (
	ClearNone ClearSide = iota
	ClearLeft
	ClearRight
	ClearBoth
)

func (c ClearSide) String() string {
	switch c {
	case ClearLeft:
		return "left"
	case ClearRight:
		return "right"
	case ClearBoth:
		return "both"
	}
	return "none"
}

// clears reports whether a box with this clear value has to get past a float on
// the given side.
func (c ClearSide) clears(side FloatSide) bool {
	switch c {
	case ClearBoth:
		return true
	case ClearLeft:
		return side == FloatLeft
	case ClearRight:
		return side == FloatRight
	}
	return false
}

// placedFloat is one float, already positioned.
type placedFloat struct {
	// rect is the margin box, in the coordinates of the formatting context's
	// content box.
	rect Rect
	side FloatSide
}

// floatContext is the set of floats in one block formatting context.
//
// The list of rectangles is the whole of the state; everything else is derived
// from it. It was once *all* of it, and every query was a scan — which was a
// perfectly good decision until it was measured, because a page with a hundred
// floats on it is not a page but a document with a hundred thousand is a denial
// of service. What answers the queries now is floatindex.go, which keeps the
// staircase of band edges and the running maxima that the scans used to
// recompute. The list stays the source of truth: the index is caught up to it
// lazily, so a context built from a literal list still answers correctly.
type floatContext struct {
	boxes []placedFloat

	// consulted counts the queries that a float actually changed the answer to.
	//
	// It is not a statistic. Layout has to place a box before it knows the
	// collapsed margin above it, so the offset it uses is predicted and then
	// checked (see layout.go), and this is what says whether a wrong prediction
	// could have reached the geometry. A subtree that never touched a float can
	// have its floats translated; one that read them has to be laid out again.
	consulted int

	idx floatIndex
}

// sync catches the index up with the list of floats.
//
// It runs before every query and — this is the part that matters — before place
// appends, which is what keeps a truncation from being mistaken for the floats
// that replaced it. Nothing else may append, so no rewrite of the list can get
// past this unnoticed: it is either shorter than the index, or the index is a
// prefix of it.
func (fc *floatContext) sync() {
	if fc.idx.n == len(fc.boxes) {
		return
	}
	fc.idx.rewind(len(fc.boxes))
	for fc.idx.n < len(fc.boxes) {
		fc.idx.absorb(fc.boxes[fc.idx.n])
	}
}

// mark returns a position in the list, so that the floats added after it can be
// found again.
func (fc *floatContext) mark() int { return len(fc.boxes) }

// truncate discards every float added since a mark, which is what a subtree that
// has to be laid out again leaves behind.
func (fc *floatContext) truncate(mark int) {
	fc.boxes = fc.boxes[:mark]
}

// shift moves every float added since a mark.
//
// This is the cheap half of the correction described on consulted: if a subtree
// only *added* floats and never read any, its whole contribution is wrong by one
// constant offset and moving it is exactly equivalent to having laid it out at
// the right place.
//
// The rewind is not an optimisation to be skipped. The list is being rewritten
// in place and its length does not change, so sync cannot see that anything
// happened; taking the moved floats out of the index here is what makes them be
// put back at their new positions.
func (fc *floatContext) shift(from int, dy style.Unit) {
	fc.idx.rewind(from)
	for i := from; i < len(fc.boxes); i++ {
		fc.boxes[i].rect.Y = fc.boxes[i].rect.Y.Add(dy)
	}
}

// bandAt returns the left and right edges available at one y, between the two
// edges of a containing block.
//
// A float of zero height obstructs nothing. That is not an optimisation — an
// empty floated <div> is a common way to write a clearance hack, and treating it
// as an obstacle would move the text beside it.
func (fc *floatContext) bandAt(y, lo, hi style.Unit) (left, right style.Unit) {
	return fc.bandOver(y, y, lo, hi)
}

// bandOver returns the left and right edges available over a vertical range,
// between the two edges of a containing block.
//
// The range matters, and getting it wrong was worth a dozen of the suite's
// tests. §9.5's non-overlap rule is about a *box*, not about a point: a float
// whose top is below the top of the box still overlaps it, so the room a box has
// is the intersection of the bands over its whole height rather than the band at
// its first line. The suite says so by name —
// floats-wrap-top-below-bfc-001l.xht is "test for wrapping around floats whose
// top is below the top of what must wrap around them" — and an engine that asks
// at a single y puts the box in the wider band above the float and lets the
// float pass straight through it.
//
// A degenerate range, where bottom is not below top, is the single-y question,
// and it is answered as such rather than as an empty intersection: a float that
// begins exactly at y obstructs a query at y.
func (fc *floatContext) bandOver(top, bottom, lo, hi style.Unit) (left, right style.Unit) {
	fc.sync()
	left, right = lo, hi
	if edge, ok := bandEdge(&fc.idx.left, top, bottom); ok && edge > left {
		left = edge
	}
	if edge, ok := bandEdge(&fc.idx.right, top, bottom); ok && edge < right {
		right = edge
	}
	if right < left {
		// Floats from both sides that overlap leave no room at all rather than
		// an inside-out band, which every caller would then have to guard.
		right = left
	}
	if left != lo || right != hi {
		fc.consulted++
	}
	return left, right
}

// bandEdge asks a staircase for the winning edge over [top, bottom), where an
// empty range is the point top.
//
// The two cases are the two halves of the question bandOver documents: a float
// spans a point when it begins at or above it and ends below it, and it spans a
// range when the two ranges meet at all.
func bandEdge(s *stair, top, bottom style.Unit) (style.Unit, bool) {
	if bottom <= top {
		return s.at(top)
	}
	return s.over(top, bottom)
}

// overlaps reports whether a rectangle meets the margin box of any float.
//
// This is the question §9.5 actually asks — "the border box of a table, a
// block-level replaced element, or an element in the normal flow that
// establishes a new block formatting context must not overlap the margin box of
// any floats" — and it is not the same question as whether the box is narrower
// than the band. Two cases separate them, and both are in the suite:
//
//   - A float outside the containing block. floats-wrap-bfc-with-margin-008 puts
//     a 100px box in a 50px containing block beside a right float that begins
//     exactly where that block ends. The band is the whole containing block, so
//     a width comparison says the box fits; the box is twice as wide as the
//     block and lands squarely on the float.
//   - A box whose own margin moves its border box. The band says where there is
//     room, not where the border box will be.
//
// Touching is not overlapping: a box that begins exactly at a float's right edge
// is beside it, which is the whole point of the band.
func (fc *floatContext) overlaps(r Rect) bool {
	if r.W <= 0 || r.H <= 0 {
		// A box with no extent covers nothing, so nothing can be under it. A
		// zero-height flow root is a real thing — an empty "overflow: hidden"
		// div — and it does not get pushed below a float.
		return false
	}
	for _, f := range fc.boxes {
		if f.rect.W <= 0 || f.rect.H <= 0 {
			continue
		}
		if f.rect.X < r.Right() && f.rect.Right() > r.X &&
			f.rect.Y < r.Bottom() && f.rect.Bottom() > r.Y {
			fc.consulted++
			return true
		}
	}
	return false
}

// nextBottomBelow returns the smallest float bottom strictly below a y.
//
// It is how the placement search advances: the set of available bands only
// changes where a float ends, so there is no point testing any y between two
// float bottoms. Searching by increments would be both slower and, at a fixed
// point scale of a 64th of a pixel, effectively unbounded.
func (fc *floatContext) nextBottomBelow(y style.Unit) (style.Unit, bool) {
	fc.sync()
	return firstAbove(fc.idx.bottoms, y)
}

// place positions a float and records it, returning its margin box.
//
// size is the margin box; top is the highest the margin box may go, which the
// caller has already worked out from the flow position and from clearance; lo
// and hi are the containing block's content edges. All four are in the
// formatting context's coordinates.
//
// The search is the one §9.5.1 describes, expressed as: try here; if the band is
// too narrow, drop to where the next float ends and try again. Rules 1 to 6 are
// the caller's — they are about where the float may not go *above* — and rules 7
// to 9 are here: as far up as possible, then as far to the chosen side as
// possible, and a float that cannot fit beside another goes below it.
func (fc *floatContext) place(size Size, side FloatSide, top, lo, hi style.Unit) Rect {
	fc.sync()
	y := top
	// Rule 5, which is the one that stops floats reordering: a float may not
	// start higher than any float declared before it. Without it a narrow float
	// following a wide one slides up past it into the gap beside the text, so
	// the page shows them in the opposite order to the markup — and it looks
	// deliberate, because the result is a perfectly tidy arrangement of the
	// wrong boxes.
	if n := fc.idx.n; n > 0 && fc.idx.marks[n-1].topMax > y {
		y = fc.idx.marks[n-1].topMax
	}
	for {
		left, right := fc.bandAt(y, lo, hi)
		if right.Sub(left) >= size.W {
			break
		}
		if left == lo && right == hi {
			// The band is the whole containing block and the float still does
			// not fit, so it is wider than the block it is in. §9.5.1 rule 8
			// says a float that big overflows rather than being pushed down for
			// ever, and dropping it a line at a time would never find room.
			break
		}
		next, ok := fc.nextBottomBelow(y)
		if !ok {
			break
		}
		y = next
	}

	left, right := fc.bandAt(y, lo, hi)
	x := left
	if side == FloatRight {
		// A right float that is wider than the space it has overflows to the
		// left, which is the mirror of rule 8 and is why this is not clamped
		// back to lo the way a left float is.
		x = right.Sub(size.W)
	} else if x < lo {
		x = lo
	}

	rect := Rect{X: x, Y: y, W: size.W, H: size.H}
	fc.boxes = append(fc.boxes, placedFloat{rect: rect, side: side})
	return rect
}

// avoidsFloats reports whether §9.5's non-overlap rule applies to a box.
//
// The rule names three kinds of box: "a table, a block-level replaced element,
// or an element in the normal flow that establishes a new block formatting
// context". The common thread is that none of them has line boxes at its own
// level for the float to shorten, so the only way to keep the float visible is
// to move the whole box.
//
// An ordinary block is deliberately not on the list, and that is the rule's
// whole point: a plain <div> beside a float *does* overlap it, and only its
// lines are shortened. An engine that moved every block would put a gap beside
// every float where the specification puts running text.
//
// A box that is itself out of flow is excluded because it is placed by its own
// rules — a float by §9.5.1, an absolutely positioned box by §10.3.7 — and both
// of those already know about the floats they care about.
func avoidsFloats(b *Box) bool {
	if b == nil || b.outOfFlow() || b.Outer != OuterBlock {
		return false
	}
	if b.Replaced != nil {
		return true
	}
	return establishesBFC(b)
}

// avoidFloats places a box that may not overlap a float, returning how far below
// the given position it had to drop and the width and margins it has to be laid
// out with. A nil geometry means no float reaches it and the ordinary rules
// apply unchanged.
//
// The two halves are the two things §9.5 permits: "implementations should clear
// the said element by placing it below any preceding floats, but may place it
// adjacent to such floats if there is enough space. They may even make the
// border box of said element narrower than defined by section 10.3.3."
//
//   - A box whose width is its own — declared, or a percentage of its containing
//     block — keeps that width and drops until a band is wide enough to hold it.
//     A "width: 150px" box beside a 200px float in a 300px block will not fit in
//     the 100px left, so it goes below the float at its full width rather than
//     being squeezed.
//   - A box with an auto width is narrowed to the band instead, which is what
//     makes "overflow: hidden" the way to write a column beside a float.
//
// A table arrives here as the wrapper §17.4 put around it, which is a flow root
// and has no width of its own — the declaration is on the table inside. So a
// table beside a float always takes the second path and narrows, and the suite's
// floats-wrap-bfc-005 says browsers drop a "width: 50%" table instead. That one
// is left: asking the wrapper what the table declared means reaching through
// §17.5.2, and the tests that turn on it are failing on their cell content as
// well. It is named here rather than left to be discovered.
//
// # Which box the rule is about
//
// §9.5 constrains the *border* box, and against the float's *margin* box. The
// distinction is not pedantry: a box with "margin-right: 1px" whose border box
// exactly fills the band beside a float belongs beside that float, and the
// suite's new-fc-beside-float-with-margin asserts it. Counting the margin as
// part of what has to fit drops the box a hundred pixels for the sake of one
// pixel nobody can see.
//
// # top, height and known
//
// top is where the box's border box would have gone, in the containing block's
// coordinates; the return value is measured from there. height is the border
// box's height, and known says whether it has been laid out yet.
//
// The pair is the awkward part of the rule, and it is awkward in the
// specification rather than here: the room a box has depends on its height, and
// its height depends on the room it has. Layout therefore asks twice — once
// before the box exists, with known false, where the question can only be the
// band at the box's first line; and once after, with the height it turned out
// to have, where the real rectangle can be tested and the box moved if the
// first answer was too generous.
func (l *layouter) avoidFloats(b *Box, containing style.Unit, origin flow,
	top, height style.Unit, known bool) (style.Unit, *forcedGeometry) {

	if !avoidsFloats(b) || len(origin.ctx.boxes) == 0 {
		return 0, nil
	}
	lo, hi := origin.x, origin.x.Add(containing)

	margin := l.edges(b, "margin", containing)
	// An auto margin is zero for as long as the box is being fitted: it is a
	// share of whatever the band has left over, and how much that is cannot be
	// known until the band and the width are both settled. It is resolved at the
	// bottom of this function, once they are.
	leftAuto, rightAuto := l.isAuto(b, "margin-left"), l.isAuto(b, "margin-right")
	if leftAuto {
		margin.Left = 0
	}
	if rightAuto {
		margin.Right = 0
	}
	// The box's own edges, which are inside the border box, and the same plus its
	// margins. Both are needed, for two questions that only look like one.
	edges := l.borderWidths(b).Horizontal().
		Add(l.paddingOf(b, containing).Horizontal())
	fixed := edges.Add(margin.Horizontal())

	// The distinction is whether the used width depends on the room available.
	// A declared width does not, so the box keeps it and drops. An auto width
	// does, in both of its forms — filling the band, or shrinking to fit it — so
	// the box narrows instead. Reading a shrink-to-fit width against the
	// *containing block* and then insisting on it is the mistake worth naming:
	// it turns every auto-width table beside a float into one that drops below
	// it, which is what an earlier draft of this did.
	declared, hasWidth := l.explicitWidth(b, containing)
	if hasWidth {
		declared = l.clampWidth(b, declared, containing)
	}
	// How wide a band the box needs, and this is where the two questions part.
	//
	// A box that narrows takes its width *from* the band, so what the band has to
	// pay for besides the content is its edges and both its margins. A negative
	// margin makes that number negative and so makes the box wider than the band,
	// which is the whole of what a negative margin is for — the suite turns on it
	// in floats-wrap-bfc-with-margin-006 and -007 and in
	// new-fc-beside-float-with-margin-rtl.
	//
	// A box with a declared width is §10.3.3's over-constrained case, and that is
	// what decides whether its margins count. The equality "margin-left + border
	// + padding + width + padding + border + margin-right = the room available"
	// cannot hold when the width is fixed, so one value has to give, and in a
	// left-to-right box it is margin-right: "the specified value of margin-right
	// is ignored". A *positive* margin-right is therefore not part of what has to
	// fit — new-fc-beside-float-with-margin is a "margin-right: 1px" beside a
	// band its border box exactly fills, and dropping the box a hundred pixels
	// for one invisible pixel is what counting it does. A *negative* one is not
	// over-constraining at all: it makes the equality hold at a larger width, so
	// it is room and it counts.
	need := fixed
	if hasWidth {
		need = declared.Add(edges).Add(margin.Left).Add(style.Min(margin.Right, 0))
	}

	// bottom is the far edge of the range the band is asked over. Before the box
	// is laid out there is no such range and the question degenerates to the
	// band at its top, which is bandOver's empty-range case.
	extent := func(y style.Unit) style.Unit {
		if !known {
			return y
		}
		return y.Add(height)
	}

	// usedWidth is the width the box would be laid out with in a given band, and
	// borderBox is where its border box would then be.
	usedWidth := func(left, right style.Unit) style.Unit {
		if hasWidth {
			return declared
		}
		room := maxZero(right.Sub(left).Sub(fixed))
		if b.TableWrapper {
			// §17.4: the wrapper is as wide as the table's border box, and the
			// table's auto width is settled against the room it has — which is
			// now the band's rather than the containing block's.
			return l.shrinkToFit(b, room)
		}
		return l.clampWidth(b, room, containing)
	}
	borderBox := func(y, left, right style.Unit) Rect {
		return Rect{
			X: left.Add(margin.Left), Y: y,
			W: usedWidth(left, right).Add(edges), H: height,
		}
	}

	// Drop to the first band that holds it, exactly as a float does. The search
	// steps from one float bottom to the next because that is where the set of
	// bands changes; see nextBottomBelow — and because it terminates: there are
	// finitely many float bottoms and each step is strictly below the last.
	y := origin.y.Add(top)
	for {
		left, right := origin.ctx.bandOver(y, extent(y), lo, hi)
		// Whether the border box lands on a float even though the band says
		// there is room for it. A width comparison cannot see that, because the
		// band is clamped to the containing block and a float can be outside
		// one: floats-wrap-bfc-with-margin-008 puts a hundred-pixel box in a
		// fifty-pixel block beside a right float that begins exactly where the
		// block ends, and every width in the arithmetic says it fits.
		//
		// Only asked of a declared width, and only once the height is known. A
		// box that narrows took its width from this band, so it is by
		// construction where the band put it, and a rectangle test would
		// rediscover nothing but the negative margin the author wrote on purpose.
		hit := known && hasWidth && origin.ctx.overlaps(borderBox(y, left, right))
		if right.Sub(left) >= need && !hit {
			break
		}
		if left == lo && right == hi && !hit {
			// The whole containing block is not wide enough, so no band below
			// will be either. §9.5.1 rule 8's overflow, for the same reason —
			// unless the box is lying on a float, which is not something a
			// wider band below cannot fix.
			break
		}
		next, ok := origin.ctx.nextBottomBelow(y)
		if !ok {
			break
		}
		y = next
	}

	drop := y.Sub(origin.y).Sub(top)
	left, right := origin.ctx.bandOver(y, extent(y), lo, hi)
	if left == lo && right == hi {
		// No float reaches this band, so nothing about the box's own width or
		// position changes. Only the drop, if any, is the rule's doing.
		return drop, nil
	}

	width := usedWidth(left, right)
	if hasWidth && (leftAuto || rightAuto) {
		// §10.3.3's auto margins, resolved against the band rather than against
		// the containing block. That is the only substitution the rule makes:
		// the box has been fitted to a band, so the room it has to share out is
		// the band's room, and an auto margin still takes all of what is left.
		//
		// It is what puts a "width: 200px; margin-left: auto" box against the
		// *right* float rather than against the left edge of a block that has a
		// float in the way. Zeroing the auto margin instead — which is what this
		// did first, on the reasoning that a box fitted to a band has no slack to
		// give away — puts the box a hundred and fifty pixels from where every
		// browser puts it, and the suite says so in
		// floats-wrap-top-below-bfc-001r.
		//
		// A negative remainder is not shared out. It means the box is wider than
		// the band and is overflowing it, and an auto margin that went negative
		// would pull the border box back onto the float this whole function
		// exists to keep it off.
		slack := maxZero(right.Sub(left).Sub(width).Sub(edges).
			Sub(margin.Left).Sub(margin.Right))
		switch {
		case leftAuto && rightAuto:
			half := slack.Div(2)
			margin.Left, margin.Right = half, slack.Sub(half)
		case leftAuto:
			margin.Left = slack
		default:
			margin.Right = slack
		}
	}
	// The box goes against the near edge of the band. Its own left margin is
	// still its own, so the shift is added to it rather than replacing it.
	margin.Left = margin.Left.Add(left.Sub(lo))
	return drop, &forcedGeometry{margin: margin, width: width}
}

// clearance returns the y a box with this clear value must start at or below.
//
// It is the bottom of the lowest float on a cleared side, and zero when there is
// none — the caller compares it against the position the box would have had
// anyway, because clearance is the *difference* and §9.5.2 is explicit that a
// box already below the floats gets none.
//
// Every float in the context is earlier in the document than the box asking,
// since floats are placed as they are met, so there is no need to filter by
// source order. That is an invariant of the walk rather than of this type, and
// it is the reason clear cannot be answered by a query made after layout.
func (fc *floatContext) clearance(clear ClearSide) style.Unit {
	if clear == ClearNone {
		return 0
	}
	fc.sync()
	var lowest style.Unit
	if n := fc.idx.n; n > 0 {
		a := fc.idx.marks[n-1]
		switch clear {
		case ClearLeft:
			lowest = a.bottomLeft
		case ClearRight:
			lowest = a.bottomRight
		default:
			lowest = a.bottomAll
		}
	}
	if lowest > 0 {
		fc.consulted++
	}
	return lowest
}

// bottom is the lowest edge any float in the context reaches.
//
// This is §10.6.7: the auto height of a box that establishes a formatting
// context includes the floats inside it, so that "overflow: hidden" on a wrapper
// stops a floated image hanging out of the bottom of it. An ordinary block does
// not do this, and the difference between the two is the whole of why the idiom
// exists.
func (fc *floatContext) bottom() style.Unit {
	fc.sync()
	if n := fc.idx.n; n > 0 {
		return fc.idx.marks[n-1].bottomAll
	}
	return 0
}

// flow is where a step of layout sits inside a block formatting context.
//
// x and y are the coordinates of the *containing block's content box left edge*
// and of the box's own border box top, both measured from the content box of the
// formatting-context root. The two are deliberately different things: a float is
// constrained horizontally by its containing block and vertically by where the
// flow has reached, and carrying the pair is what lets a box resolve its own
// margins — which may be auto, and so are not known to the caller — without the
// caller having to know them first.
type flow struct {
	ctx  *floatContext
	x, y style.Unit

	// cbHeight is the content height of the containing block, and cbDefinite
	// says whether it is a height the author declared rather than one the
	// content happened to produce.
	//
	// The pair is here rather than passed alongside the width because it is the
	// same kind of fact: where a step of layout sits and what it may resolve a
	// percentage against. Only the vertical offsets of §9.4.3 read it, and they
	// read the *definite* flag as much as the number — a percentage of a height
	// that is not definite computes to auto, and answering it with the height
	// the content later turned out to need would be a number that looks right
	// and is not the one CSS specifies.
	cbHeight   style.Unit
	cbDefinite bool
}

// establishesBFC reports whether a box lays its floats out in a context of its
// own rather than its parent's.
//
// The list is §9.4.1's, reduced to what the box tree can currently produce. A
// float is on it, which is easy to overlook and matters: floats inside a float do
// not escape it, so a floated sidebar containing a floated image is as tall as
// the image.
//
// An absolutely positioned box is on it too, and that entry earns more than
// containment: it is what makes the deferred placement of position.go sound. A
// box that neither reads the float geometry around it nor contributes to it has
// no interaction with the flow in either direction, so laying it out after the
// walk has finished produces exactly what laying it out during the walk would
// have.
//
// For every display value this engine actually lays out, that would already be
// true without the clause — §9.7 blockifies an out-of-flow box to a flow root,
// and the first test here catches it. The clause is not therefore redundant: the
// displays whose inner half *survives* blockification, a table and a flex
// container, stay themselves when absolutely positioned, and this is the only
// thing that seals those. Neither is laid out yet, so the clause is checked
// directly rather than through a page.
func establishesBFC(b *Box) bool {
	switch b.Inner {
	case InnerFlowRoot, InnerTable, InnerTableCell, InnerTableCaption:
		// A cell, a caption and a table each seal their floats in. §17.4 puts
		// the table's on the wrapper, which is a flow root and so already on
		// this list; the table box is here as well because a float that escaped
		// the grid would be placed against a formatting context whose geometry
		// the table algorithm never consulted.
		return true
	}
	return b.Float != FloatNone || b.Position.outOfFlow()
}
