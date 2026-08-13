package render

import (
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/style"
)

// CSS Overflow 4's clamp, which is a property of a subtree and not of a
// paragraph.
//
// §"max-lines": "If the box is a line-clamp container, its line-based clamp
// point is set to the first possible clamp point after its Nth descendant
// in-flow line box." Descendant, so the count runs down through the container's
// block children and theirs: the suite's line-clamp-005 clamps to three lines a
// box whose first line is its own, whose second and third come from a nested
// block, and whose fourth would have come from the block after that.
//
// That is why the count cannot live in the loop that makes the lines. That loop
// is per block container and knows nothing of its siblings, so the count lives
// on the layouter instead — as a stack, since a clamp container inside another
// counts toward both.

// lineClamp is one clamp container being laid out.
type lineClamp struct {
	// box is the container, and face and size are its font. The mark is set in
	// the *container's* font because §"block-ellipsis" wraps it "in an anonymous
	// inline whose parent is the block container's root inline box" — the
	// container's root inline box, not that of whichever descendant the last
	// line happened to fall in. The suite's line-clamp-002 sets its text in a
	// span a quarter of the block's size and puts the mark at the block's.
	box  *Box
	face *fonts.Face
	size style.Unit

	// limit is how many descendant in-flow line boxes the property allows and
	// seen is how many have been made.
	limit, seen int

	// stopAt is where layout actually halts, which is one line past limit on
	// the counting pass and limit itself on the real one.
	//
	// Two numbers rather than one because the extra line is only how the
	// counting pass *learns* that something was cut. Everything else the clamp
	// decides — where the mark goes, and above all how §5.1 balances, since a
	// balanced box is broken to fill the lines it is allowed — has to be
	// decided against the number the property states, or the counting pass
	// balances the content into a line the clamp will not show and then finds
	// it overflowing. That is line-clamp-006, whose content balances into
	// exactly the two lines it is clamped to and must therefore show no mark at
	// all.
	stopAt int

	// ellipsis is the room to keep on the last line the clamp allows.
	//
	// Zero on the pass that is only counting, because nothing is discarded
	// until the count is known and a block clamped to three lines that has
	// three says nothing at all. That is the difference between this property
	// and a truncation, and it is why the container is laid out twice. See
	// clampedChildren.
	ellipsis style.Unit

	// reached says the clamp point has gone by, so everything after it is
	// "fragmented away and neither rendered nor measured".
	reached bool
}

// clampRoom is what the clamps in force leave the block container about to be
// laid out: how many more line boxes it may make and the room the mark will
// need on the last of them.
//
// The tightest of them, since a line counts toward every clamp it is inside.
func (l *layouter) clampRoom() (budget int, ellipsis style.Unit, ok bool) {
	for _, c := range l.clamps {
		if n := c.limit - c.seen; !ok || n < budget {
			budget, ellipsis, ok = n, c.ellipsis, true
		}
	}
	return budget, ellipsis, ok
}

// clampEndingHere is the clamp whose last allowed line is the one about to be
// made, or nil when the line is not one.
//
// The widest mark when more than one clamp ends on the same line, so the room
// kept is enough for whichever is drawn.
func (l *layouter) clampEndingHere() *lineClamp {
	var out *lineClamp
	for _, c := range l.clamps {
		if c.seen == c.limit-1 && (out == nil || c.ellipsis > out.ellipsis) {
			out = c
		}
	}
	return out
}

// clampLine records one line box against every clamp in force.
func (l *layouter) clampLine() {
	for _, c := range l.clamps {
		c.seen++
		if c.seen >= c.stopAt {
			c.reached = true
		}
	}
}

// clampReached says the clamp point has gone by, so what remains is discarded.
func (l *layouter) clampReached() bool {
	for _, c := range l.clamps {
		if c.reached {
			return true
		}
	}
	return false
}

// outOfClamp lays out content that no clamp counts, and returns whatever f did.
//
// A float, because §"max-lines" counts *in-flow* line boxes and says so. An
// atomic inline, because its content sits on a line box that is itself counted
// and charging the clamp for both is charging it twice for one band of the
// page. A table cell, because a clamp point in the middle of a table row is not
// a fragmentation this engine can carry out — so counting a table's lines would
// spend the allowance without being able to honour it, and discard the blocks
// *after* the table on the strength of lines that were never clamped.
//
// Not an absolutely positioned box, which is out of flow and is nonetheless
// absent from this list: they are placed after the whole in-flow tree, by which
// time no clamp is on the stack at all. A call here would be unreachable.
func outOfClamp[T any](l *layouter, f func() T) T {
	held := l.clamps
	l.clamps = nil
	defer func() { l.clamps = held }()
	return f()
}

// clampedChildren is children with CSS Overflow 4's clamp applied over the
// whole subtree.
//
// The container is laid out twice when it is really cut, and once when it is
// not. The first pass is allowed one line more than the clamp does and keeps no
// room for the mark: a box whose content fits inside the clamp never reaches
// the extra line, says nothing, and the layout that pass produced is the one
// kept. Only a box that reached it is laid out again — this time with the true
// limit and with room for the mark on its last line, which is a different break
// on that line and so cannot be patched on afterwards.
//
// Two passes rather than a look-ahead because the question the mark answers is
// "was anything discarded", and across a subtree that is not a question about
// the text in hand: the content that overflows may be in a block three siblings
// later. Laying it out is the only exact way to ask.
func (l *layouter) clampedChildren(b *Box, frag *Fragment, width style.Unit,
	topOpen, bottomOpen bool, inner flow) (style.Unit, marginRun, marginRun, bool) {

	n := l.lineClamp(b)
	if n == 0 {
		return l.children(b, frag, width, topOpen, bottomOpen, inner)
	}

	kids, lines := len(frag.Children), len(frag.Lines)
	absAt, floats := len(l.deferred), inner.ctx.mark()

	c := &lineClamp{box: b, limit: n, stopAt: n + 1}
	l.clamps = append(l.clamps, c)
	height, top, bottom, placed := l.children(b, frag, width, topOpen, bottomOpen, inner)
	l.clamps = l.clamps[:len(l.clamps)-1]
	if c.seen <= n {
		// Nothing was cut, so the property has nothing to say and this layout
		// is the document's. A box clamped to three lines that has three shows
		// no mark: that is the difference between a clamp and a truncation.
		return height, top, bottom, placed
	}

	frag.Children, frag.Lines = frag.Children[:kids], frag.Lines[:lines]
	l.deferred = l.deferred[:absAt]
	inner.ctx.truncate(floats)

	c = &lineClamp{box: b, limit: n, stopAt: n}
	if face, ok := l.fontFor(b); ok {
		c.face, c.size = face, b.FontSize
		c.ellipsis = l.measure(face, blockEllipsis, b.FontSize)
	}
	l.clamps = append(l.clamps, c)
	height, top, bottom, placed = l.children(b, frag, width, topOpen, bottomOpen, inner)
	l.clamps = l.clamps[:len(l.clamps)-1]
	return height, top, bottom, placed
}
