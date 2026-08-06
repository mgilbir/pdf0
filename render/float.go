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
// It is a flat slice rather than an interval tree because the queries are all
// linear scans over a list that is short in every real document — a page with a
// hundred floats on it is not a page — and because a scan has no ordering
// invariant to get wrong. If it ever needs to be faster, the shape to reach for
// is the pair of "float bottom" lists browsers keep, one per side.
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
}

// mark returns a position in the list, so that the floats added after it can be
// found again.
func (fc *floatContext) mark() int { return len(fc.boxes) }

// shift moves every float added since a mark.
//
// This is the cheap half of the correction described on consulted: if a subtree
// only *added* floats and never read any, its whole contribution is wrong by one
// constant offset and moving it is exactly equivalent to having laid it out at
// the right place.
func (fc *floatContext) shift(from int, dy style.Unit) {
	for i := from; i < len(fc.boxes); i++ {
		fc.boxes[i].rect.Y = fc.boxes[i].rect.Y.Add(dy)
	}
}

// bandAt returns the left and right edges available at one y, between the two
// edges of a containing block.
//
// The query is at a single y rather than over a range, which is what browsers do
// and is worth stating because the alternative looks more careful and is not: a
// line box is placed at the y it starts at, and a float that begins halfway down
// it does not retroactively shorten it. Taking the intersection over the line's
// whole height would shorten lines that the specification leaves alone.
//
// A float of zero height obstructs nothing. That is not an optimisation either —
// an empty floated <div> is a common way to write a clearance hack, and treating
// it as an obstacle would move the text beside it.
func (fc *floatContext) bandAt(y, lo, hi style.Unit) (left, right style.Unit) {
	left, right = lo, hi
	for _, f := range fc.boxes {
		if f.rect.H <= 0 || y < f.rect.Y || y >= f.rect.Bottom() {
			continue
		}
		if f.side == FloatLeft {
			if edge := f.rect.Right(); edge > left {
				left = edge
			}
			continue
		}
		if f.rect.X < right {
			right = f.rect.X
		}
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

// nextBottomBelow returns the smallest float bottom strictly below a y.
//
// It is how the placement search advances: the set of available bands only
// changes where a float ends, so there is no point testing any y between two
// float bottoms. Searching by increments would be both slower and, at a fixed
// point scale of a 64th of a pixel, effectively unbounded.
func (fc *floatContext) nextBottomBelow(y style.Unit) (style.Unit, bool) {
	best, found := style.Unit(0), false
	for _, f := range fc.boxes {
		if f.rect.H <= 0 {
			continue
		}
		if b := f.rect.Bottom(); b > y && (!found || b < best) {
			best, found = b, true
		}
	}
	return best, found
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
	y := top
	// Rule 5, which is the one that stops floats reordering: a float may not
	// start higher than any float declared before it. Without it a narrow float
	// following a wide one slides up past it into the gap beside the text, so
	// the page shows them in the opposite order to the markup — and it looks
	// deliberate, because the result is a perfectly tidy arrangement of the
	// wrong boxes.
	for _, f := range fc.boxes {
		if f.rect.Y > y {
			y = f.rect.Y
		}
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
	var lowest style.Unit
	for _, f := range fc.boxes {
		if !clear.clears(f.side) {
			continue
		}
		if b := f.rect.Bottom(); b > lowest {
			lowest = b
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
	var lowest style.Unit
	for _, f := range fc.boxes {
		if b := f.rect.Bottom(); b > lowest {
			lowest = b
		}
	}
	return lowest
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
	return b.Inner == InnerFlowRoot || b.Float != FloatNone || b.Position.outOfFlow()
}
