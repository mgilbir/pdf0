package render

import "github.com/mgilbir/pdf0/style"

// The summaries floatContext answers its queries from, so that a page with a lot
// of floats on it is not quadratic.
//
// # Why this exists
//
// Every question a floatContext answers used to be a scan of every float in it,
// and the placement search asks several of them per float — so a document with n
// floats did Θ(n²) work. Measured, on one block of empty 3×3 floats: a thousand
// took 8 ms and thirty-two thousand took 2.4 s, which is 32× the input for 300×
// the time. A document is untrusted input. Anyone who can hand this engine a page
// can hand it a hundred thousand floats, and the flat scan turns that into
// minutes of CPU for a page that renders as a grey rectangle.
//
// # The shape of the answer
//
// The band at a given y is decided, on each side, by a *single* float: for the
// left edge it is the greatest right-edge among the left floats whose
// [top, bottom) contains y. That is a range update with a point query, and the
// function it builds is a staircase — piecewise constant, changing only where a
// float begins or ends. Held as a sorted array of breakpoints it answers a point
// query by binary search, and a float is added by rewriting only the breakpoints
// its own span touches.
//
// The other four queries are cheaper still. "How far down does the lowest float
// on this side reach" and "what is the highest top any float has" are running
// maxima, and each float's value for them is recorded as it arrives, so a
// truncation gets the earlier answer back by looking one entry further up the
// list. "Where does the next float end below y" is a binary search in a sorted
// list of bottoms.
//
// # The two things that make it harder than it looks
//
// The floats are not simply appended and then read. Layout places a box before it
// knows the collapsed margin above it (see settleIn), so a whole subtree's floats
// are routinely either *moved* by a constant afterwards or *discarded* and placed
// again. Both break any structure that only knows how to grow.
//
// Discarding is handled by remembering, per float, exactly which breakpoints it
// rewrote and what stood there before — so taking a float back out is replaying
// that one splice backwards. It is exact rather than approximate: the array after
// removing float k is byte for byte the array that existed before float k was
// added, which is what lets the property test compare against a linear scan after
// an arbitrary sequence of adds and removals.
//
// Moving is expressed in terms of discarding: the moved suffix is taken back out,
// the rectangles are translated, and the suffix is added again. That costs what
// the translation used to cost multiplied by the cost of one insertion, and the
// alternative — a structure that can translate a suffix in place — would need the
// breakpoints of the moved floats to be separable from the ones they were merged
// with, which is exactly the merging that makes the staircase small.
//
// # What this does not make fast
//
// Queries are not monotonic in y: the line-breaking loop asks again over a line's
// height once it has broken it, and avoidFloats asks about an arbitrary
// rectangle. Nothing here assumes a cursor that only moves forward.
//
// A query over a *range* of y — a line's height, a box's height — is the maximum
// of the staircase over that range, and this scans the breakpoints inside it
// rather than answering in logarithmic time. That is a deliberate stop: the range
// is a line box or a box's height in every document anyone writes, so it spans a
// handful of breakpoints, and a structure that answered it in logarithmic time
// would need a balanced tree with lazy tags whose undo is a great deal harder to
// argue than a splice. It is never worse than the scan it replaced.
//
// overlaps is left as a scan of every float for a related reason: it is a
// rectangle-intersection test in two dimensions, and neither staircase decides
// it — the float with the greatest right edge over a range need not be the one
// whose left edge reaches into the rectangle.

// stairStep is one breakpoint of a staircase: the value that holds from y until
// the next step's y.
//
// set is not a decoration. An empty staircase and a staircase whose value is zero
// are different facts — a left float whose right edge is at zero raises nothing,
// but it is still an obstacle for the range queries to find — and a sentinel
// value would have to be a real coordinate, which style.Unit's saturating
// arithmetic can produce. A step with set false is the gap between two floats.
type stairStep struct {
	y    style.Unit
	edge style.Unit
	set  bool
}

// stair is the greatest (or least) float edge in force at each y.
//
// steps is sorted by y and holds no two adjacent entries with the same value, so
// its length is bounded by the number of *distinct* spans the floats cover rather
// than by the number of floats: the common page, where floats stack in rows,
// gives one or two steps per row and not one per float.
type stair struct {
	steps []stairStep

	// least says which of two floats covering the same y wins. The left edge of
	// a band is the greatest right edge of the left floats; the right edge is the
	// least left edge of the right floats.
	least bool
}

// stairEdit is what one call to cover changed, so that it can be undone.
type stairEdit struct {
	at      int
	added   int
	removed []stairStep
}

// beats reports whether a is the value to keep when two floats cover the same y.
func (s *stair) beats(a, b style.Unit) bool {
	if s.least {
		return a < b
	}
	return a > b
}

// stepAt returns the index of the step in force at y, or -1 when no float
// reaches that high.
func (s *stair) stepAt(y style.Unit) int {
	lo, hi := 0, len(s.steps)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if s.steps[mid].y <= y {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo - 1
}

// lowerBound returns the first index whose step begins at or after y.
func (s *stair) lowerBound(y style.Unit) int {
	lo, hi := 0, len(s.steps)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if s.steps[mid].y < y {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// at answers the point query: the edge in force at one y.
func (s *stair) at(y style.Unit) (style.Unit, bool) {
	k := s.stepAt(y)
	if k < 0 || !s.steps[k].set {
		return 0, false
	}
	return s.steps[k].edge, true
}

// over answers the range query: the winning edge anywhere in [y0, y1).
//
// This is the scan named at the top of the file. It starts at the step in force
// at y0 — which may have begun well above it — and stops at the first step that
// begins at or after y1.
func (s *stair) over(y0, y1 style.Unit) (style.Unit, bool) {
	k := s.stepAt(y0)
	if k < 0 {
		k = 0
	}
	var best style.Unit
	found := false
	for ; k < len(s.steps) && s.steps[k].y < y1; k++ {
		st := s.steps[k]
		if !st.set {
			continue
		}
		if !found || s.beats(st.edge, best) {
			best, found = st.edge, true
		}
	}
	return best, found
}

// combine applies a float's edge to one step.
func (s *stair) combine(st stairStep, edge style.Unit) stairStep {
	if !st.set {
		return stairStep{y: st.y, edge: edge, set: true}
	}
	if s.beats(edge, st.edge) {
		st.edge = edge
	}
	return st
}

// cover records a float that occupies [y0, y1) with the given edge, returning
// what it changed.
//
// The window that can change is [y0, y1) and nothing else, so the rewrite is a
// splice: the steps that begin inside the window are recombined with the new
// edge, a step is opened at y0 unless one already begins there, and the value the
// old staircase had just below y1 is restored at y1 unless a step already begins
// there. Adjacent steps that end up equal are merged, which is what keeps the
// array proportional to the distinct spans rather than to the floats.
func (s *stair) cover(y0, y1, edge style.Unit) stairEdit {
	lo, hi := s.lowerBound(y0), s.lowerBound(y1)

	// out is the replacement for steps[lo:hi]. It is built against the value in
	// force immediately above the window, so a first step that repeats it is
	// dropped rather than splitting one run into two identical ones.
	var out []stairStep
	var last stairStep
	haveLast := lo > 0
	if haveLast {
		last = s.steps[lo-1]
	}
	push := func(st stairStep) {
		if haveLast && last.set == st.set && (!st.set || last.edge == st.edge) {
			return
		}
		out = append(out, st)
		last, haveLast = st, true
	}

	if lo == len(s.steps) || s.steps[lo].y > y0 {
		// The float begins part way through the step above it (or past the end of
		// the staircase), so the covered run needs a step of its own at y0.
		prev := stairStep{y: y0}
		if lo > 0 {
			prev = s.steps[lo-1]
			prev.y = y0
		}
		push(s.combine(prev, edge))
	}
	for k := lo; k < hi; k++ {
		push(s.combine(s.steps[k], edge))
	}
	if hi == len(s.steps) || s.steps[hi].y > y1 {
		// Below the float the staircase goes back to whatever it was, which is
		// the value in force just above y1 — the last step the window swallowed,
		// or nothing at all when the float reaches past the end.
		tail := stairStep{y: y1}
		if hi > 0 {
			tail = s.steps[hi-1]
			tail.y = y1
		}
		push(tail)
	}
	// The step just below the window can now repeat the last one written, in
	// which case it is redundant and is swallowed too.
	if hi < len(s.steps) && haveLast {
		if next := s.steps[hi]; next.set == last.set && (!next.set || next.edge == last.edge) {
			hi++
		}
	}

	e := stairEdit{at: lo, added: len(out)}
	if hi > lo {
		e.removed = append(e.removed, s.steps[lo:hi]...)
	}
	s.steps = spliceSteps(s.steps, lo, hi, out)
	return e
}

// undo puts back exactly what cover took out.
func (s *stair) undo(e stairEdit) {
	s.steps = spliceSteps(s.steps, e.at, e.at+e.added, e.removed)
}

// spliceSteps replaces dst[from:to] with with, in place where the capacity
// allows. copy is a move, so the overlapping shifts below are sound.
func spliceSteps(dst []stairStep, from, to int, with []stairStep) []stairStep {
	switch delta := len(with) - (to - from); {
	case delta > 0:
		dst = append(dst, make([]stairStep, delta)...)
		copy(dst[to+delta:], dst[to:])
	case delta < 0:
		copy(dst[to+delta:], dst[to:])
		dst = dst[:len(dst)+delta]
	}
	copy(dst[from:], with)
	return dst
}

// absorbed is everything one float did to the index, kept so that it can be
// taken back out again.
//
// The four maxima are the running values *including* this float, so truncating
// the list to k floats leaves the answers for the first k in the last entry. The
// three bottoms are floored at zero because both of the questions they answer
// are: §9.5.2's clearance and §10.6.7's height are both "how far down do the
// floats reach", and a float entirely above the origin pushes nothing.
type absorbed struct {
	// on names the staircase edit belongs to, and is FloatNone for a float with
	// no height, which obstructs nothing and so is on neither staircase.
	on   FloatSide
	edit stairEdit

	// bottomAt is where this float's bottom went in the sorted list, or -1 when
	// it contributed none.
	bottomAt int

	topMax      style.Unit
	bottomAll   style.Unit
	bottomLeft  style.Unit
	bottomRight style.Unit
}

// floatIndex is the derived half of a floatContext.
//
// n counts the floats it has absorbed, which is how a context that has been
// truncated — or one built by a test from a literal list — is noticed and caught
// up with. The index is never the source of truth: floatContext.boxes is, and
// this is rebuilt from it incrementally.
type floatIndex struct {
	n           int
	left, right stair
	bottoms     []style.Unit
	marks       []absorbed
}

// absorb folds one float into the summaries.
func (ix *floatIndex) absorb(f placedFloat) {
	// The right-hand staircase keeps the *least* edge, and this is where that is
	// said, because a floatContext is made with a composite literal in half a
	// dozen places and there is no constructor to say it in. An empty staircase
	// answers nothing whatever the flag says, so setting it on the way to the
	// first float is early enough — and it is set unconditionally rather than
	// once, because a flag that is only right after some particular call is the
	// kind of state this would rather not have.
	ix.right.least = true

	a := absorbed{on: FloatNone, bottomAt: -1}
	if ix.n > 0 {
		prev := ix.marks[ix.n-1]
		a.topMax = prev.topMax
		a.bottomAll, a.bottomLeft, a.bottomRight =
			prev.bottomAll, prev.bottomLeft, prev.bottomRight
	} else {
		// No float has been seen, so there is no highest top yet. The maxima of
		// bottoms start at zero because they are floored there; this one is not,
		// and its caller reads it only when there is at least one float.
		a.topMax = style.MinUnit
	}

	top, bottom := f.rect.Y, f.rect.Bottom()
	a.topMax = style.Max(a.topMax, top)
	a.bottomAll = style.Max(a.bottomAll, bottom)
	switch f.side {
	case FloatLeft:
		a.bottomLeft = style.Max(a.bottomLeft, bottom)
	case FloatRight:
		a.bottomRight = style.Max(a.bottomRight, bottom)
	}

	// A float of zero height obstructs nothing — an empty floated div is a
	// common clearance hack — so it reaches neither staircase nor the list of
	// bottoms the placement search steps through. It still counts for clearance
	// and for the height of what contains it, which is why those are above this.
	if f.rect.H > 0 {
		a.bottomAt = insertUnit(&ix.bottoms, bottom)
	}
	// bottom can fail to be below top even with a positive height, because
	// style.Unit saturates: a float at the end of the range has nowhere to
	// extend to. Such a float spans no y at all, so it goes on no staircase —
	// which is the answer the scan gave too, since neither half of spansRange
	// can hold for an empty span.
	//
	// Which staircase is decided by "left, or else right" rather than by a switch
	// over the three values, because that is what the scan did: it tested for
	// FloatLeft and treated everything else as an obstacle on the right. Nothing
	// puts a float with no side into a context today — a box only reaches place
	// when its float property is set — so the two readings agree on every input
	// that occurs, and the one that agrees on the inputs that do not is the one
	// that cannot be the reason an answer changed.
	if f.rect.H > 0 && bottom > top {
		if f.side == FloatLeft {
			a.on, a.edit = FloatLeft, ix.left.cover(top, bottom, f.rect.Right())
		} else {
			a.on, a.edit = FloatRight, ix.right.cover(top, bottom, f.rect.X)
		}
	}

	ix.marks = append(ix.marks, a)
	ix.n++
}

// rewind takes the index back to the first k floats.
func (ix *floatIndex) rewind(k int) {
	for ix.n > k {
		ix.n--
		a := ix.marks[ix.n]
		switch a.on {
		case FloatLeft:
			ix.left.undo(a.edit)
		case FloatRight:
			ix.right.undo(a.edit)
		}
		if a.bottomAt >= 0 {
			copy(ix.bottoms[a.bottomAt:], ix.bottoms[a.bottomAt+1:])
			ix.bottoms = ix.bottoms[:len(ix.bottoms)-1]
		}
	}
	ix.marks = ix.marks[:ix.n]
}

// insertUnit puts v in a sorted list, returning where it went so that the same
// entry can be taken out again.
func insertUnit(list *[]style.Unit, v style.Unit) int {
	s := *list
	lo, hi := 0, len(s)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if s[mid] < v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	s = append(s, 0)
	copy(s[lo+1:], s[lo:])
	s[lo] = v
	*list = s
	return lo
}

// firstAbove returns the first entry of a sorted list strictly greater than v.
func firstAbove(s []style.Unit, v style.Unit) (style.Unit, bool) {
	lo, hi := 0, len(s)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if s[mid] <= v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == len(s) {
		return 0, false
	}
	return s[lo], true
}
