package render

import (
	"strconv"
	"strings"

	"github.com/mgilbir/pdf0/style"
)

// Positioning: the schemes of CSS 2.1 §9.3, the relative offsets of §9.4.3, and
// the absolute sizing of §10.3.7 and §10.6.4.
//
// # Why this is in scope at all
//
// Positioning reads as a dynamic feature — a tooltip that follows a cursor, a
// banner that sticks to a viewport as it is resized — and this engine has
// neither a cursor nor a viewport that changes. The scope boundary of this
// project is *dynamism*, not familiarity, and positioning falls on the static
// side of it: a page whose size is settled before layout runs can resolve every
// offset in §9.3 exactly, once, with no iteration.
//
// "position: fixed" is the case where that is worth spelling out. It resolves
// against the viewport, and in a paged medium the viewport is the page box — so
// a fixed box and an absolute box with no positioned ancestor resolve against
// the *same* rectangle here, and the two are implemented as one computation.
// That is not an approximation standing in for the real thing; it is what the
// definition reduces to when the viewport cannot scroll and cannot resize.
//
// # The tension with relative-then-absolutise, and how it is resolved
//
// layout.go positions every fragment relative to its parent's content box and
// makes the whole tree absolute in a single pass at the end, because a box's own
// position is not known until its margins have finished collapsing with its
// descendants' — and that is only settled once its subtree has been walked. An
// absolutely positioned box does not fit that model at all: its containing block
// is the padding box of an *ancestor*, which is neither its parent nor a
// rectangle that exists while the walk is still inside it.
//
// float.go met the same conflict and answered it with prediction plus bounded
// re-layout. It had to: a float must be *placed* during the walk, because the
// line boxes that follow it are shortened by it, so nothing downstream can be
// laid out until the float has a position. An absolutely positioned box has the
// opposite property, and that property is what makes a much simpler answer
// correct rather than merely convenient. It is out of flow, so nothing in the
// flow moves for it; and §9.4.1 makes it establish a block formatting context,
// so no float inside it escapes and no float outside it reaches in. Its geometry
// therefore has *no* influence on anything else on the page, which means it can
// be laid out afterwards — after absolutise has run and every ancestor has a
// final rectangle in page coordinates to resolve against.
//
// So the walk records a *candidate* where the box was met, carrying the one
// thing that is knowable only during the walk: the static position, which is
// where the box would have gone had it been in the flow and which §10.3.7 and
// §10.6.4 fall back on whenever the offsets do not decide. Everything else is
// computed later against a containing block that is by then a real rectangle.
// There is no prediction here and no re-layout, and the difference from floats
// is worth stating because it looks like inconsistency and is the opposite:
// each mechanism is handled by the cheapest method its own dependencies allow.
//
// # Relative positioning is the easy one, and is deliberately kept that way
//
// §9.4.3 offsets a box *visually* after it has been laid out in the normal flow.
// It still occupies its original space; nothing else moves. So the offset is not
// applied by layout at all — it is carried on the fragment and added by
// absolutise, which already visits every fragment once and already translates
// each subtree by its parent's origin. Adding the offset there makes the whole
// subtree move with the box, which is exactly §9.4.3's behaviour, and it makes
// it impossible to write the classic bug: an offset folded into the flow
// position would move the box's siblings too, and the page would look tidy and
// be wrong.

// PositionScheme is the value of the position property.
//
// It is named for the scheme rather than simply "Position" because a box also
// has a position in the geometric sense, and a field called Position holding
// "relative" beside a field called BorderRect holding a rectangle is a sentence
// nobody reads correctly twice.
type PositionScheme uint8

const (
	// PositionStatic is the normal flow: the box goes where the flow puts it and
	// the four offset properties do not apply to it at all.
	PositionStatic PositionScheme = iota
	// PositionRelative lays the box out in the normal flow and then offsets it
	// visually, leaving the space it occupied behind.
	PositionRelative
	// PositionAbsolute takes the box out of the flow entirely and resolves it
	// against the padding box of its nearest positioned ancestor.
	PositionAbsolute
	// PositionFixed is PositionAbsolute against the page box. In a medium with a
	// viewport that can scroll the two differ; in a paged one the viewport is
	// the page, and they do not.
	PositionFixed
)

func (p PositionScheme) String() string {
	switch p {
	case PositionRelative:
		return "relative"
	case PositionAbsolute:
		return "absolute"
	case PositionFixed:
		return "fixed"
	}
	return "static"
}

// positioned reports whether a box takes part in §9.9's stacking order as a
// positioned box, which every scheme but static does — including relative,
// which is why a relatively positioned box with no offset at all still paints
// above its in-flow siblings.
func (p PositionScheme) positioned() bool { return p != PositionStatic }

// outOfFlow reports whether the box is removed from the normal flow, so that
// nothing else moves to make room for it.
func (p PositionScheme) outOfFlow() bool {
	return p == PositionAbsolute || p == PositionFixed
}

// positionOf reads the position property.
//
// An unrecognised value gives the initial one, which is what the cascade would
// have produced had the declaration been thrown out. "sticky" is recognised and
// refused by the caller rather than silently read as static: it is the one value
// in this property that genuinely needs a scroll position, so it is the one this
// engine cannot answer.
func positionOf(cs style.ComputedStyle) PositionScheme {
	switch strings.ToLower(strings.TrimSpace(cs["position"])) {
	case "relative":
		return PositionRelative
	case "absolute":
		return PositionAbsolute
	case "fixed":
		return PositionFixed
	}
	return PositionStatic
}

// zIndexOf reads the z-index property, reporting whether it is the auto keyword.
//
// Only an integer is a z-index. "z-index: 1.5" is not a rounding question, it is
// an invalid declaration, and the initial value stands — which is what a browser
// does and what keeps a typo from silently reordering a page.
func zIndexOf(cs style.ComputedStyle) (int, bool) {
	raw := strings.TrimSpace(cs["z-index"])
	if raw == "" || strings.EqualFold(raw, "auto") {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, true
	}
	return n, false
}

// forcedGeometry is a box's width, margins and height decided by a caller rather
// than by the box's own declarations.
//
// It exists for exactly one caller: an absolutely positioned box, whose width
// and margins come from the constraint of §10.3.7 against a containing block
// that block layout cannot see. Passing them in is what lets such a box go
// through the ordinary block-layout path — margin collapsing, floats, inline
// content, list markers and all — rather than growing a second, thinner copy of
// block layout that would drift away from the first.
type forcedGeometry struct {
	margin Edges
	width  style.Unit
	// height is used only when hasHeight is set. It carries the case block
	// layout cannot resolve on its own: an absolutely positioned box may have a
	// percentage height, and a percentage of a containing block that is itself
	// being sized by its content is indefinite — but an abspos containing block
	// has been laid out already, so here it is definite and the percentage is a
	// real number.
	height    style.Unit
	hasHeight bool
}

// relativeOffset computes §9.4.3's visual offset for a box.
//
// The rules are stated as four properties and are really two constraints, one
// per axis, each over-determined by one. Horizontally: left and right are
// opposite descriptions of the same displacement, so if both are given they
// contradict each other and one has to lose — in a left-to-right document,
// "left" wins and "right" is ignored. If only one is given the other is its
// negation, and if neither is given there is no offset.
//
// A percentage of left or right is a percentage of the containing block's
// *width*, and of top or bottom a percentage of its *height*. The height is
// frequently indefinite — a block sized by its content has no height to take a
// percentage of while its own children are still being laid out — and CSS 2.1 is
// explicit that such a percentage computes to auto rather than to zero. Zero
// would be the plausible wrong answer: it looks like "no offset", which is what
// auto also produces here, and the two stop agreeing the moment the property is
// paired with its opposite.
func (l *layouter) relativeOffset(b *Box, containing, cbHeight style.Unit, cbDefinite bool) Point {
	axis := func(start, end string, basis style.Unit, definite bool) style.Unit {
		lo, loAuto := l.offsetValue(b, start, basis, definite)
		hi, hiAuto := l.offsetValue(b, end, basis, definite)
		switch {
		case loAuto && hiAuto:
			return 0
		case loAuto:
			// The box moves away from the edge the property names, so a "right"
			// of 10px moves it 10px to the left.
			return style.Unit(0).Sub(hi)
		}
		// Either only the start was given, or both were and the start wins.
		return lo
	}
	return Point{
		X: axis("left", "right", containing, true),
		Y: axis("top", "bottom", cbHeight, cbDefinite),
	}
}

// offsetValue resolves one of the four offset properties, reporting whether it
// is auto — which includes a percentage of a basis that is not definite, since
// §10.3.7's own text says such a value computes to auto.
func (l *layouter) offsetValue(b *Box, property string, basis style.Unit, definite bool) (style.Unit, bool) {
	length, ok := l.parseLength(b, property)
	if !ok || length.Kind == style.LengthAuto {
		return 0, true
	}
	v, ok := length.Resolve(basis, definite)
	if !ok {
		return 0, true
	}
	return v, false
}

// absCandidate is a box taken out of the flow, waiting for its containing block
// to become a real rectangle.
type absCandidate struct {
	box *Box
	// parent is the fragment the box's own fragment will hang from. It is the
	// nearest ancestor that *has* a fragment, which is not necessarily the box's
	// parent — an inline box produces no fragment of its own, so a box written
	// inside a <span> hangs from the block container the span is in.
	//
	// This is a place in the tree and not a containing block, and it decides
	// nothing about the geometry: where the box is resolved against is §10.1's
	// answer and is usually somewhere else entirely, and the painting order
	// among boxes at the same stacking level is keyed on document order rather
	// than on where in the fragment tree they ended up. What hanging it
	// somewhere buys is that the fragment tree stays a tree, so every consumer
	// that walks it reaches the box.
	parent *Fragment
	// staticX and staticY are the static position — where the box's margin box
	// would have started had it been in the flow — relative to parent's content
	// box, in the coordinates layout uses while it is still walking.
	//
	// "Would have started" is exact horizontally and one collapse short
	// vertically: the position recorded is the flow position with the margins
	// pending above it, which is where a static box with no top margin of its
	// own would begin. A box that had one would have collapsed it with those,
	// and CSS 2.1 leaves what the hypothetical box's margins do underspecified
	// enough that browsers differ. The difference is visible only for an
	// absolutely positioned box that names neither vertical offset *and* carries
	// a top margin, which is a combination that asks for two contradictory
	// things at once.
	staticX, staticY style.Unit
	// index is the box's position among its parent's list items, which is what a
	// numbered marker is numbered by. An absolutely positioned list item still
	// generates a marker.
	index int
}

// maxAbsolutes bounds how many out-of-flow boxes one render will place.
//
// Each candidate is a distinct box and is laid out once, so the count is already
// bounded by the box cap — this is a second bound rather than the only one, and
// it is here because the queue is self-feeding: laying an absolutely positioned
// box out can discover more inside it, and a bug in the rollback that settle
// performs would turn that into a loop rather than into a wrong page. A cap that
// turns a hang into a finding is worth its two lines.
//
// It is a variable rather than a constant so that a test can lower it far enough
// to watch it fire. A bound that has only ever been observed not to trip is one
// nobody knows works, which this repository has learned before.
var maxAbsolutes = 1 << 14

// deferAbsolute records an out-of-flow box to be placed once the tree is
// absolute.
func (l *layouter) deferAbsolute(b *Box, parent *Fragment, x, y style.Unit, index int) {
	l.deferred = append(l.deferred, absCandidate{
		box: b, parent: parent, staticX: x, staticY: y, index: index,
	})
}

// placeAbsolutes lays out and positions every box that was taken out of the
// flow, after the in-flow tree has been made absolute.
//
// The loop is over a slice that grows as it runs, because an absolutely
// positioned box may contain more of them. Processing in discovery order is what
// makes each candidate's parent fragment already absolute by the time it is
// reached: a candidate found inside another abspos box is appended while that
// box is being placed, and that box's whole subtree is made absolute before the
// loop moves on.
func (l *layouter) placeAbsolutes(page Rect) {
	for i := 0; i < len(l.deferred); i++ {
		if i >= maxAbsolutes {
			l.rec.Report(RuleLimit, AtHTML(offsetOf(l.deferred[i].box)),
				"more boxes were taken out of the normal flow than this engine will "+
					"place; the rest were left unpositioned and are not on the page")
			return
		}
		l.layoutAbsolute(l.deferred[i], page)
	}
}

// layoutAbsolute places one out-of-flow box.
func (l *layouter) layoutAbsolute(c absCandidate, page Rect) {
	b := c.box
	cb := l.containingBlockFor(b, page)

	border := l.borderWidths(b)
	padding := l.edges(b, "padding", cb.W)
	margin := l.edges(b, "margin", cb.W)

	// The static position, made absolute and then expressed relative to the
	// containing block, which is what §10.3.7 measures "left" from. The parent
	// fragment has already been absolutised, so its content box is final.
	parent := c.parent.ContentRect()
	staticLeft := parent.X.Add(c.staticX).Sub(cb.X)
	staticTop := parent.Y.Add(c.staticY).Sub(cb.Y)

	// A replaced box brings its own size, and §10.3.6 and §10.6.5 say to treat
	// it as though the author had declared both — so the constraint below
	// solves for the offsets and the margins rather than for the size. That is
	// the difference between an absolutely positioned image and an absolutely
	// positioned <div>: "left: 0; right: 0" stretches the div and moves the
	// image, because the image already knows how wide it is.
	var replaced *Size
	if b.Replaced != nil {
		s := l.replacedSize(b, cb.W, cb.H, true)
		replaced = &s
	}

	// Horizontal first, because the width has to be settled before the box can
	// be laid out and the height it needs is what the box's layout produces.
	h := l.solveHorizontal(b, cb, border, padding, margin, staticLeft, replaced)

	// A declared height, resolved here rather than by block layout because only
	// here is the containing block's height definite.
	declaredHeight, hasHeight := l.absoluteLength(b, "height", cb.H)
	if replaced != nil {
		declaredHeight, hasHeight = replaced.H, true
	}

	frag, _ := l.blockIn(b, cb.W,
		flow{ctx: &floatContext{}, cbHeight: cb.H, cbDefinite: true},
		&forcedGeometry{
			margin: Edges{
				Top: margin.Top, Right: h.marginEnd,
				Bottom: margin.Bottom, Left: h.marginStart,
			},
			width:     h.size,
			height:    declaredHeight,
			hasHeight: hasHeight,
		})
	if b.ListItem {
		frag.Marker = l.markerFor(b, frag, c.index)
	}

	// What the content needed, which is the "auto" height of §10.6.4.
	content := frag.BorderRect.H.Sub(padding.Vertical()).Sub(border.Vertical())
	v := l.solveVertical(b, cb, border, padding, margin, staticTop, content, declaredHeight, hasHeight)

	frag.BorderRect.H = v.size.Add(padding.Vertical()).Add(border.Vertical())
	frag.Margin = Edges{
		Top: v.marginStart, Right: h.marginEnd,
		Bottom: v.marginEnd, Left: h.marginStart,
	}

	// The border box lands at the containing block's edge plus the offset plus
	// the margin, because "left" is measured to the *margin* edge and every
	// rectangle this engine stores is a border box.
	x := cb.X.Add(h.start).Add(h.marginStart)
	y := cb.Y.Add(v.start).Add(v.marginStart)

	// The subtree comes out of blockIn in coordinates relative to its own
	// origin; this is the same one-pass translation absolutise does for the
	// in-flow tree, applied to a subtree that was laid out after it.
	absolutise(frag, x.Sub(frag.BorderRect.X), y.Sub(frag.BorderRect.Y))

	c.parent.Children = append(c.parent.Children, frag)
}

// containingBlockFor finds the rectangle an out-of-flow box resolves against:
// §10.1's third and fourth cases.
//
// For "fixed" it is the page box. For "absolute" it is the *padding* box of the
// nearest ancestor whose position is anything but static, and the padding box
// rather than the content box is not a detail — it is what makes a positioned
// box with padding hold an absolutely positioned child inside its padding rather
// than inset by it, which is the difference every "position: relative" wrapper
// in the world depends on.
//
// With no positioned ancestor the answer is the initial containing block, which
// in this engine is the page box: there is one page, its size is settled before
// layout, and the root's content box is laid out to fill it.
func (l *layouter) containingBlockFor(b *Box, page Rect) Rect {
	if b.Position == PositionFixed {
		return page
	}
	for anc := b.Parent; anc != nil; anc = anc.Parent {
		if !anc.Position.positioned() {
			continue
		}
		if f, ok := l.positioned[anc]; ok {
			return f.PaddingRect()
		}
		// A positioned ancestor with no fragment is an inline-level one, and
		// §10.1 forms its containing block from the padding boxes of its first
		// and last inline fragments — boxes this engine does not produce,
		// because inline-level content lives in line boxes rather than in
		// fragments of its own. Skipping it and resolving against the next
		// candidate up puts the box somewhere plausible and not where the author
		// asked, which is precisely the kind of quiet wrongness §6 exists to
		// name, so it is named.
		l.rec.ReportDetail(Finding{
			Rule:   RulePositionApproximated,
			Source: AtHTML(offsetOf(b)),
			Message: "the containing block for this absolutely positioned box is an " +
				"inline box, whose fragments this engine does not produce; it was " +
				"positioned against the next positioned ancestor instead",
			Path:     PathOf(b.Element),
			Property: "position",
		})
	}
	return page
}

// absoluteLength resolves a property against a definite basis, which is what an
// absolutely positioned box's containing block always is.
func (l *layouter) absoluteLength(b *Box, property string, basis style.Unit) (style.Unit, bool) {
	length, ok := l.parseLength(b, property)
	if !ok || length.Kind == style.LengthAuto {
		return 0, false
	}
	return length.Resolve(basis, true)
}

// axisSolution is what solving one axis of the constraint produces.
type axisSolution struct {
	// start is the offset from the containing block's start edge to the box's
	// margin edge: "left" or "top".
	start style.Unit
	// size is the content width or content height.
	size style.Unit
	// marginStart and marginEnd are the two margins along the axis, which the
	// constraint may have had to resolve.
	marginStart, marginEnd style.Unit
}

// absAxis is one axis of §10.3.7 or §10.6.4, reduced to the shape the two share.
//
// The two sections read as separate algorithms and are one algorithm with three
// differences, which is worth writing once rather than twice: the same six
// values are related by the same equation, and an implementation that wrote them
// out separately would have two places for the "over-constrained" case to be
// got wrong in.
type absAxis struct {
	// start, size and end are left/width/right or top/height/bottom, and the
	// three booleans say which of them the author left to the engine.
	start, size, end             style.Unit
	startAuto, sizeAuto, endAuto bool

	marginStart, marginEnd         style.Unit
	marginStartAuto, marginEndAuto bool

	// fixed is the border and padding along the axis, which is never auto.
	fixed style.Unit
	// available is the containing block's extent along the axis, which is what
	// the six values above have to add up to.
	available style.Unit
	// static is where the box's margin edge would have been in the flow,
	// measured from the containing block's start edge.
	static style.Unit

	// autoSize gives the size to use when it is auto and the equation does not
	// decide it. This is the first of the three differences between the
	// sections: horizontally it is shrink-to-fit against whatever room is left,
	// vertically it is simply the height the content needed.
	autoSize func(room style.Unit) style.Unit
	// vertical selects the other two differences. Horizontally an
	// over-constrained box ignores "right" and a pair of auto margins that would
	// come out negative is resolved by pinning the left one to zero; vertically
	// "bottom" is ignored and equal auto margins stay equal however negative
	// they are.
	vertical bool
}

// solveAxis is the constraint of §10.3.7 and §10.6.4.
//
// The equation is: start + margin-start + border + padding + size + margin-end +
// end = the containing block's extent. Seven terms, one of which is fixed, and
// as many as five of the rest may be auto — so the section is a case analysis
// over which are, and the cases are here in the order the specification writes
// them because that order is load-bearing: the "all three auto" case has to be
// tested before the individual ones, and the over-constrained case has to be
// tested before the branch that resolves auto margins, or a box with every value
// given would have its margins recomputed by a rule meant for a box with room to
// spare.
func solveAxis(a absAxis) axisSolution {
	ms, me := a.marginStart, a.marginEnd

	// All three auto. The box goes where the flow would have put it and takes
	// the size its content wants, which is what an absolutely positioned box
	// with no offsets at all does — it stays where it was and stops pushing its
	// neighbours around. Auto margins are zero here rather than a share of the
	// slack, because there is no slack until the position is known.
	if a.startAuto && a.sizeAuto && a.endAuto {
		if a.marginStartAuto {
			ms = 0
		}
		if a.marginEndAuto {
			me = 0
		}
		room := a.available.Sub(a.static).Sub(ms).Sub(me).Sub(a.fixed)
		return axisSolution{
			start:       a.static,
			size:        maxZero(a.autoSize(maxZero(room))),
			marginStart: ms, marginEnd: me,
		}
	}

	// Nothing auto. The equation is over-determined, so either the auto margins
	// absorb the difference or the end offset is ignored.
	if !a.startAuto && !a.sizeAuto && !a.endAuto {
		slack := a.available.Sub(a.start).Sub(a.end).Sub(a.size).Sub(a.fixed)
		switch {
		case a.marginStartAuto && a.marginEndAuto:
			half := slack.Div(2)
			if slack < 0 && !a.vertical {
				// §10.3.7: equal margins that would be negative are not what an
				// author asking to centre a box meant. In a left-to-right
				// document the left margin goes to zero and the right absorbs
				// the whole difference, so the box stays pinned to its left
				// offset and overflows to the right.
				ms, me = 0, slack
			} else {
				ms, me = half, slack.Sub(half)
			}
		case a.marginStartAuto:
			ms = slack.Sub(me)
		case a.marginEndAuto:
			me = slack.Sub(ms)
		default:
			// Over-constrained: the end offset is ignored and the box stays
			// where its start offset put it. Nothing to compute — every value in
			// the equation but the one being dropped was given.
		}
		return axisSolution{start: a.start, size: a.size, marginStart: ms, marginEnd: me}
	}

	// One or two auto. An auto margin is zero here: the slack belongs to
	// whichever of start, size and end was left auto, and §10.3.7 gives it to
	// them rather than sharing it.
	if a.marginStartAuto {
		ms = 0
	}
	if a.marginEndAuto {
		me = 0
	}
	edges := a.fixed.Add(ms).Add(me)

	out := axisSolution{marginStart: ms, marginEnd: me}
	switch {
	case a.startAuto && a.sizeAuto:
		// The end is anchored and the box is sized by its content, so the start
		// is whatever is left over. This is what makes "right: 0" with no width
		// put a shrink-wrapped box against the right edge.
		out.size = maxZero(a.autoSize(maxZero(a.available.Sub(a.end).Sub(edges))))
		out.start = a.available.Sub(a.end).Sub(edges).Sub(out.size)
	case a.startAuto && a.endAuto:
		// Neither edge is anchored, so the box stays where the flow left it.
		out.start, out.size = a.static, a.size
	case a.sizeAuto && a.endAuto:
		out.start = a.start
		out.size = maxZero(a.autoSize(maxZero(a.available.Sub(a.start).Sub(edges))))
	case a.startAuto:
		out.size = a.size
		out.start = a.available.Sub(a.end).Sub(edges).Sub(out.size)
	case a.sizeAuto:
		// Both edges are anchored, so the size is the distance between them.
		// This is the stretch case: "top: 0; bottom: 0" makes a box as tall as
		// its containing block without either number saying how tall that is.
		out.start = a.start
		out.size = maxZero(a.available.Sub(a.start).Sub(a.end).Sub(edges))
	default:
		// Only the end is auto, so nothing about the box's own geometry depends
		// on it.
		out.start, out.size = a.start, a.size
	}
	return out
}

// solveHorizontal applies §10.3.7 and then the min/max clamp of §10.4.
//
// The clamp is not applied to the answer and left there. §10.4 says that if the
// resulting width violates a minimum or a maximum, the whole of §10.3.7 is run
// again with the width treated as that limit — because the width is what the
// other unknowns were solved *from*, so leaving it changed and them unchanged
// gives a box whose left, width and right no longer add up to its containing
// block. That shows as a box anchored to neither edge.
func (l *layouter) solveHorizontal(b *Box, cb Rect, border, padding, margin Edges,
	staticLeft style.Unit, replaced *Size) axisSolution {

	left, leftAuto := l.offsetValue(b, "left", cb.W, true)
	right, rightAuto := l.offsetValue(b, "right", cb.W, true)
	width, hasWidth := l.absoluteLength(b, "width", cb.W)
	if replaced != nil {
		width, hasWidth = replaced.W, true
	}

	axis := absAxis{
		start: left, startAuto: leftAuto,
		size: width, sizeAuto: !hasWidth,
		end: right, endAuto: rightAuto,
		marginStart: margin.Left, marginStartAuto: l.isAuto(b, "margin-left"),
		marginEnd: margin.Right, marginEndAuto: l.isAuto(b, "margin-right"),
		fixed:     border.Horizontal().Add(padding.Horizontal()),
		available: cb.W,
		static:    staticLeft,
		autoSize:  func(room style.Unit) style.Unit { return l.shrinkToFit(b, room) },
	}
	got := solveAxis(axis)
	if replaced != nil {
		// §10.4's constraint table has already applied the minimum and the
		// maximum to both axes together. Applying the width's alone here would
		// undo the height it chose to keep the picture's shape.
		return got
	}
	if clamped := l.clampWidth(b, got.size, cb.W); clamped != got.size {
		axis.size, axis.sizeAuto = clamped, false
		got = solveAxis(axis)
	}
	return got
}

// solveVertical applies §10.6.4 and the min/max clamp, mirroring solveHorizontal
// with the one substantive difference between the sections: an auto height is
// the height the content needed rather than a shrink-to-fit against the room
// available. A box does not get shorter because there is less room below it.
func (l *layouter) solveVertical(b *Box, cb Rect, border, padding, margin Edges,
	staticTop, content, declared style.Unit, hasHeight bool) axisSolution {

	top, topAuto := l.offsetValue(b, "top", cb.H, true)
	bottom, bottomAuto := l.offsetValue(b, "bottom", cb.H, true)

	axis := absAxis{
		start: top, startAuto: topAuto,
		size: declared, sizeAuto: !hasHeight,
		end: bottom, endAuto: bottomAuto,
		marginStart: margin.Top, marginStartAuto: l.isAuto(b, "margin-top"),
		marginEnd: margin.Bottom, marginEndAuto: l.isAuto(b, "margin-bottom"),
		fixed:     border.Vertical().Add(padding.Vertical()),
		available: cb.H,
		static:    staticTop,
		autoSize:  func(style.Unit) style.Unit { return content },
		vertical:  true,
	}
	got := solveAxis(axis)
	if b.Replaced != nil {
		return got
	}
	if clamped := l.clampHeight(b, got.size, cb.W); clamped != got.size {
		axis.size, axis.sizeAuto = clamped, false
		got = solveAxis(axis)
	}
	return got
}
