package render

import (
	"sort"
	"strings"

	"github.com/mgilbir/pdf0/style"
)

// The collapsing border model: CSS 2.1 §17.6.2.
//
// It is not a variation on the separated model of §17.6.1 but a different
// geometry. In the separated model every cell keeps its own border and the
// spacing between them is empty page; here the borders belong to the *grid*.
// Each line of the grid carries one border, chosen from the six boxes that meet
// on it, and that border is centred on the line so half of it falls in the cell
// on either side.
//
// # The three questions this file answers
//
// *Which border is drawn.* §17.6.2.1 is an ordered cascade over the borders of
// the two cells, the two rows, the two row groups, the two columns, the two
// column groups and the table that can all meet on one stretch of one grid line.
// It is precisely specified and it is precisely implemented below, in the order
// the specification states — and the order is what makes it worth testing
// carefully, because a case that the widest-wins rule settles comes out right
// even with the style-priority rule broken.
//
// *How wide the grid lines are.* A line's width is the widest border that wins
// anywhere along it, because the columns either side of it have to be in the
// same place in every row. A narrower border on the same line is drawn centred
// in the space, which is what leaves the gap an author sees when one row's
// border is thinner than another's.
//
// *Where everything ends up.* This is the part that moves every number on the
// page and the part the specification states in one sentence: "the width of the
// table includes half the width of the table's border". So the table's content
// box reaches to the *centre* of the outermost grid lines and its own used
// border is the outer half of them — which makes the table's border box exactly
// as wide as the ink, from the outside edge of the leftmost line to the outside
// edge of the rightmost. A table of four empty cells with 20px borders is 100px
// across, not 80 and not 160, and that number is what most of the suite's
// border-conflict references are measuring.
//
// A table also has no padding at all in this model, and border-spacing does not
// apply. empty-cells does not apply either: §17.6.1.1 is a rule of the separated
// model, and an empty cell here still owns half of four grid lines that its
// neighbours own the other half of.
//
// # Why none of this is done per grid square
//
// A grid is rows × columns *slots*, and a document is untrusted: a hundred
// thousand <tr> elements and one row of cells spanning to the column cap is a
// grid of four hundred million slots written in under a megabyte. Nothing here
// may be proportional to that, which rules out the obvious implementation —
// an array of edges, resolved one square at a time.
//
// What is proportional to the slots is only the *answer*, and the answer is
// almost always constant along a line: the cells decide it where there are
// cells, and the rows, columns and table decide it everywhere else. So each grid
// line is resolved as a small number of runs, each run a stretch with one winner,
// and a run is produced per cell edge rather than per slot. The occupancy of a
// row is carried from the row before it as a list of stretches rather than as an
// array of slots, for the same reason tablelayout.go carries a rowspan as one
// integer per column: the shape of the bookkeeping is what the span limits are
// there to bound.

// borderCollapses reports whether a table asks for §17.6.2's model.
func borderCollapses(table *Box) bool {
	return strings.EqualFold(strings.TrimSpace(table.Style["border-collapse"]), "collapse")
}

// The two halves a grid line is split into by the boxes that meet on it.
//
// They add up to the whole, which is the only property that matters: a line an
// odd number of layout units wide split into two equal halves would leave a
// hairline between two cells that are meant to touch, and one such line per
// column is a visible seam down a table. The leading half is the one above or to
// the left of the centre.
func leadingHalf(v style.Unit) style.Unit  { return v / 2 }
func trailingHalf(v style.Unit) style.Unit { return v - v/2 }

// The property names, indexed by side, so that resolving an edge does not build
// a string. Conflict resolution asks for several of these per cell, and a table
// is the one place in this engine where "per cell" can mean tens of thousands.
var (
	borderStyleProp = [4]string{"border-top-style", "border-right-style",
		"border-bottom-style", "border-left-style"}
	borderWidthProp = [4]string{"border-top-width", "border-right-width",
		"border-bottom-width", "border-left-width"}
	borderColorProp = [4]string{"border-top-color", "border-right-color",
		"border-bottom-color", "border-left-color"}
)

// The element order of §17.6.2.1's last rule: "a style set on a cell wins over
// one on a row, which wins over a row group, column, column group and, lastly,
// table".
const (
	rankCell uint8 = iota
	rankRow
	rankRowGroup
	rankColumn
	rankColumnGroup
	rankTable
)

// borderCand is one of the borders that meet on an edge.
type borderCand struct {
	box   *Box
	side  side
	kind  borderStyle
	width style.Unit
	rank  uint8
	// order breaks a tie between two boxes of the same kind: 0 for the one
	// further to the left or further to the top, which §17.6.2.1 says wins.
	order uint8
}

// collapsePriority is §17.6.2.1's style order, largest first: "double, solid,
// dashed, dotted, ridge, outset, groove, and the lowest: inset".
//
// none is below all of them, which is the second rule of the cascade expressed
// as a number — but only as a tie-break, since a border whose style is none has
// no width and so has already lost the widest-wins rule to anything real.
func collapsePriority(k borderStyle) int {
	switch k {
	case borderDouble:
		return 8
	case borderSolid:
		return 7
	case borderDashed:
		return 6
	case borderDotted:
		return 5
	case borderRidge:
		return 4
	case borderOutset:
		return 3
	case borderGroove:
		return 2
	case borderInset:
		return 1
	}
	return 0
}

// beats is §17.6.2.1, in the order it is stated.
//
// The order is the whole of it and it is not interchangeable. Reading the rules
// as a set rather than as a sequence gives an implementation that is right
// whenever the widths differ — which is most cases, and which is why a test for
// this has to be built so that exactly one rule can decide it.
func beats(a, b borderCand) bool {
	// "Borders with the border-style of hidden take precedence over all other
	// conflicting borders."
	if (a.kind == borderHidden) != (b.kind == borderHidden) {
		return a.kind == borderHidden
	}
	// "Borders with a style of none have the lowest priority." That rule needs
	// no clause of its own here, and it is worth saying why rather than writing
	// a comparison that can never decide anything: a border whose style is none
	// has no width at all — borderSide gives it none, which is the same rule
	// that makes "border-width: 5px" with no style occupy nothing — so the
	// widest-wins rule below has already demoted it below every border that
	// would be drawn. A clause for it was written first, and a planted defect
	// that deleted it changed no rendering anywhere: two zero-width borders draw
	// the same nothing whichever of them is called the winner.
	//
	// "narrow borders are discarded in favor of wider ones"
	if a.width != b.width {
		return a.width > b.width
	}
	// "If several have the same border-width then style is preferred in this
	// order: double, solid, dashed, dotted, ridge, outset, groove, inset."
	if pa, pb := collapsePriority(a.kind), collapsePriority(b.kind); pa != pb {
		return pa > pb
	}
	// "If border styles differ only in color, then a style set on a cell wins
	// over one on a row, which wins over a row group, column, column group and,
	// lastly, table."
	if a.rank != b.rank {
		return a.rank < b.rank
	}
	// "When two elements of the same type conflict, then the one further to the
	// left ... and further to the top wins."
	return a.order < b.order
}

// collapsedWin is the border that won an edge, kept as the box and side it came
// from rather than as a resolved colour.
//
// The colour is left to the painter deliberately: "currentcolor" on a border
// means the *declaring* box's colour, and a colour resolved here would have to
// carry that context anyway. Layout has no colour parser and does not need one.
type collapsedWin struct {
	box   *Box
	side  side
	width style.Unit
	kind  borderStyle
}

// edgeWinner accumulates the candidates meeting on one edge.
//
// It is a value rather than a slice on purpose: an edge is resolved once per run
// of a grid line, and a table has more runs than a document has boxes. The
// constant part of a line — the rows, the table — is resolved once and the
// result copied, so the per-run cost is two comparisons and no allocation.
type edgeWinner struct {
	best borderCand
	any  bool
}

func (e *edgeWinner) add(c borderCand) {
	if c.box == nil {
		return
	}
	if !e.any || beats(c, e.best) {
		e.best, e.any = c, true
	}
}

// result is the border to draw, which is nothing when the winner has no width.
//
// That one test covers hidden and none as well, since neither has a width — the
// two are told apart from each other, and from a real border, by beats above,
// which is where the difference between them decides anything.
func (e edgeWinner) result() collapsedWin {
	if !e.any || e.best.width <= 0 {
		return collapsedWin{}
	}
	return collapsedWin{box: e.best.box, side: e.best.side,
		width: e.best.width, kind: e.best.kind}
}

// borderSide reads one side of one box as a candidate.
func (l *layouter) borderSide(b *Box, s side, rank, order uint8) borderCand {
	if b == nil {
		return borderCand{}
	}
	kind := parseBorderStyle(b.Style[borderStyleProp[s]])
	var w style.Unit
	if kind != borderNone && kind != borderHidden {
		// The same reading borderWidths does, minus its shortcut: a style of
		// none or hidden makes the declared width irrelevant, and both of those
		// are decided by the cascade above rather than by a number.
		v, ok := l.lengthOf(b, borderWidthProp[s], 0)
		if !ok {
			w = keywordBorderWidth(b.Style[borderWidthProp[s]])
		} else {
			w = maxZero(v)
		}
	}
	return borderCand{box: b, side: s, kind: kind, width: w, rank: rank, order: order}
}

// ---------------------------------------------------------------------------
// The resolved grid
// ---------------------------------------------------------------------------

// collapsedRun is a stretch of one grid line over which a single border wins.
//
// from and to are columns for a horizontal line and rows for a vertical one.
type collapsedRun struct {
	from, to int
	win      collapsedWin
}

// collapsedGrid is a table's grid lines, resolved.
type collapsedGrid struct {
	cols, rows int

	// vgutter[c] is the used width of vertical grid line c, which is the widest
	// border winning anywhere along it; hgutter[r] is the same for horizontal
	// line r. A line is that wide in every row, because the columns either side
	// of it are in the same place in every row.
	vgutter, hgutter []style.Unit

	vruns []collapsedRun
	voff  []int32
	hruns []collapsedRun
	hoff  []int32

	// cells is the used border of each cell: half of each of the four grid lines
	// it touches.
	cells map[*Box]Edges
	// table is the table's own used border: the outer half of the outermost
	// lines, which is the half that is not in its content box.
	table Edges

	// truncated records that the run cap was reached, so the caller reports it
	// once rather than per line.
	truncated bool

	// resolved counts how many stretches of grid line the walks below asked the
	// conflict resolution about.
	//
	// It exists to be asserted on. The whole design of this file is that the
	// resolution is per *cell edge* and not per grid square — a table of a
	// hundred million slots written in a few kilobytes is the shape it is
	// defending against — and nothing else can witness that: the runs that come
	// out are merged, so an implementation that resolved every square separately
	// would produce byte-identical drawing and only be a hundred million times
	// slower. A planted defect that did exactly that went unnoticed until this
	// counter existed.
	resolved int
}

// maxCollapsedRuns bounds how many stretches of grid line this engine will
// resolve for one table.
//
// It is not the number of cells. A table whose every cell declares a border
// produces a run per cell edge, so a legitimate table of a quarter of a million
// cells sits just under this — and a hostile one does not: a hundred thousand
// rows crossed by cells that span alternate columns, under a stylesheet that
// gives the rows a border, produces a run per gap per row and reaches hundreds
// of millions. The geometry is still computed in full when this trips, because
// the gutters are maxima and cost nothing to keep; what is dropped is the
// drawing, and it is reported rather than silently thinned.
//
// A variable so that a test can lower it and watch it fire, for the reason
// maxTableColumns gives.
var maxCollapsedRuns = 1 << 20

// runSink collects the runs of one direction, merging a run into the one before
// it when they touch and agree.
//
// The merge is what keeps a <col span="4096"> from producing four thousand
// identical runs, and what makes a table whose borders all come from the same
// row one run per line instead of one per column.
type runSink struct {
	runs []collapsedRun
	// start is where the current line's runs begin, so that a run is never
	// merged into the last run of the previous line.
	start int
	full  bool
}

func (s *runSink) add(from, to int, w collapsedWin) {
	if w.box == nil || from >= to {
		return
	}
	if n := len(s.runs); n > s.start {
		if last := &s.runs[n-1]; last.to == from && last.win == w {
			last.to = to
			return
		}
	}
	if len(s.runs) >= maxCollapsedRuns {
		s.full = true
		return
	}
	s.runs = append(s.runs, collapsedRun{from: from, to: to, win: w})
}

func (s *runSink) endLine() int {
	s.start = len(s.runs)
	return s.start
}

// occupant is a stretch of one row held by one cell, or a stretch of one column.
//
// It is the whole of the occupancy bookkeeping. A row is carried as a list of
// these rather than as an array of slots, so a cell spanning a thousand columns
// costs one entry and a table of four hundred million slots costs no more memory
// than its widest row.
type occupant struct {
	from, to int
	cell     *tableCell
}

// ---------------------------------------------------------------------------
// Building it
// ---------------------------------------------------------------------------

// collapsedGridFor resolves a table's grid lines, memoized.
//
// The memo matters for the same reason tableGridFor's does: this is asked once
// while the table's width is being measured, once per cell while the columns are
// being sized, and again while the table is laid out.
func (l *layouter) collapsedGridFor(table *Box) *collapsedGrid {
	if cg, ok := l.collapsed[table]; ok {
		return cg
	}
	cg := l.buildCollapsedGrid(table)
	if l.collapsed == nil {
		l.collapsed = map[*Box]*collapsedGrid{}
	}
	l.collapsed[table] = cg
	return cg
}

func (l *layouter) buildCollapsedGrid(table *Box) *collapsedGrid {
	g := l.tableGridFor(table)
	cg := &collapsedGrid{cols: g.cols, rows: len(g.rows)}

	if cg.cols == 0 || cg.rows == 0 {
		// No grid to collapse against. The table still has a border and still
		// keeps half of it, which is what a browser shows for an empty table:
		// there is nothing on the other side of the line for the other half to
		// belong to. Nothing is drawn by this file for such a table — its
		// fragment carries these widths and the ordinary border painter uses
		// them, which is right because the table's own style is the only one
		// that can have won.
		var e Edges
		for _, s := range [4]side{sideTop, sideRight, sideBottom, sideLeft} {
			c := l.borderSide(table, s, rankTable, 0)
			w := c.width
			if c.kind == borderNone || c.kind == borderHidden {
				w = 0
			}
			switch s {
			case sideTop:
				e.Top = leadingHalf(w)
			case sideRight:
				e.Right = trailingHalf(w)
			case sideBottom:
				e.Bottom = trailingHalf(w)
			case sideLeft:
				e.Left = leadingHalf(w)
			}
		}
		// Empty, but present: the two arrays are indexed without a length check
		// wherever a grid is read, and a nil one here would be a panic in a
		// document as ordinary as "<table></table>".
		cg.vgutter = make([]style.Unit, cg.cols+1)
		cg.hgutter = make([]style.Unit, cg.rows+1)
		cg.cells = map[*Box]Edges{}
		cg.table = e
		return cg
	}

	cg.vgutter = make([]style.Unit, cg.cols+1)
	cg.hgutter = make([]style.Unit, cg.rows+1)

	byRow, rowAt := indexCells(g.cells, cg.rows, func(c *tableCell) int { return c.row })
	byCol, colAt := indexCells(g.cells, cg.cols, func(c *tableCell) int { return c.col })

	rowGroupStart, rowGroupEnd := spanEdges(g.rowGroups, cg.rows)
	colGroupStart, colGroupEnd := spanEdges(g.colGroups, cg.cols)
	colGroupOf := groupOfColumn(g.colGroups, cg.cols)

	l.resolveHorizontal(table, g, cg, byRow, rowAt, rowGroupStart, rowGroupEnd, colGroupOf)
	l.resolveVertical(table, g, cg, byCol, colAt, rowGroupStart, rowGroupEnd,
		colGroupStart, colGroupEnd)

	cg.finish(g)
	return cg
}

// indexCells buckets the cells by the line they start on, stably.
//
// Stability is what makes the buckets usable without a sort: the cells arrive in
// row order and, within a row, in column order, so bucketing by row leaves each
// bucket in column order and bucketing by column leaves each in row order —
// which is exactly the order the two walks below consume them in.
func indexCells(cells []*tableCell, n int, key func(*tableCell) int) ([]*tableCell, []int32) {
	at := make([]int32, n+2)
	for _, c := range cells {
		if k := key(c); k >= 0 && k < n {
			at[k+1]++
		}
	}
	for i := 1; i < len(at); i++ {
		at[i] += at[i-1]
	}
	out := make([]*tableCell, at[n])
	next := make([]int32, n+1)
	copy(next, at[:n+1])
	for _, c := range cells {
		if k := key(c); k >= 0 && k < n {
			out[next[k]] = c
			next[k]++
		}
	}
	return out, at
}

// spanEdges maps a grid line to the group box that begins or ends on it.
func spanEdges(spans []tableSpan, n int) (starts, ends []*Box) {
	starts = make([]*Box, n+1)
	ends = make([]*Box, n+1)
	for _, s := range spans {
		if s.count <= 0 || s.box == nil {
			continue
		}
		if s.first >= 0 && s.first <= n {
			starts[s.first] = s.box
		}
		if last := s.first + s.count; last >= 0 && last <= n {
			ends[last] = s.box
		}
	}
	return starts, ends
}

// groupOfColumn maps a column to the column group holding it, or -1.
func groupOfColumn(groups []tableSpan, cols int) []int {
	out := make([]int, cols)
	for i := range out {
		out[i] = -1
	}
	for gi, s := range groups {
		for c := s.first; c < s.first+s.count && c < cols; c++ {
			if c >= 0 {
				out[c] = gi
			}
		}
	}
	return out
}

// resolveHorizontal walks the horizontal grid lines, top to bottom.
//
// Line r sits between row r-1 and row r, so the walk carries the occupancy of
// the row above it and builds the occupancy of the row below as it goes. Where
// one cell holds the slot on both sides of the line it is *inside* that cell,
// and no border is drawn there at all — a rowspan is a cell, not two cells with
// a line between them, and letting a row's border cut through one would draw a
// rule across the middle of an entry.
func (l *layouter) resolveHorizontal(table *Box, g *tableGrid, cg *collapsedGrid,
	byRow []*tableCell, rowAt []int32, groupStart, groupEnd []*Box, colGroupOf []int) {

	rows, cols := cg.rows, cg.cols
	var sink runSink
	cg.hoff = make([]int32, rows+2)

	above := make([]occupant, 0, 16)
	below := make([]occupant, 0, 16)

	for r := 0; r <= rows; r++ {
		if r < rows {
			below = nextRowOccupancy(above, below, byRow[rowAt[r]:rowAt[r+1]], r)
		} else {
			below = below[:0]
		}

		// The part of the line every column shares: the two rows, the two row
		// groups whose edge this is, and the table at the very top and bottom.
		var base edgeWinner
		if r > 0 {
			base.add(l.borderSide(g.rows[r-1].box, sideBottom, rankRow, 0))
		}
		if r < rows {
			base.add(l.borderSide(g.rows[r].box, sideTop, rankRow, 1))
		}
		base.add(l.borderSide(groupEnd[r], sideBottom, rankRowGroup, 0))
		base.add(l.borderSide(groupStart[r], sideTop, rankRowGroup, 1))
		outer := sideTop
		if r == rows {
			outer = sideBottom
		}
		if r == 0 || r == rows {
			base.add(l.borderSide(table, outer, rankTable, 0))
		}

		i, j, at := 0, 0, 0
		for at < cols {
			for i < len(above) && above[i].to <= at {
				i++
			}
			for j < len(below) && below[j].to <= at {
				j++
			}
			var ac, bc *tableCell
			end := cols
			if i < len(above) {
				if above[i].from <= at {
					ac = above[i].cell
					if above[i].to < end {
						end = above[i].to
					}
				} else if above[i].from < end {
					end = above[i].from
				}
			}
			if j < len(below) {
				if below[j].from <= at {
					bc = below[j].cell
					if below[j].to < end {
						end = below[j].to
					}
				} else if below[j].from < end {
					end = below[j].from
				}
			}

			if ac != nil && ac == bc {
				// Inside a cell that spans the line.
				at = end
				continue
			}
			for k := at; k < end; {
				stop := end
				if r == 0 || r == rows {
					// The columns and column groups only meet a horizontal line
					// at the top and bottom of the table, and they are the only
					// thing on such a line that varies from column to column.
					stop = sameColumnUntil(g, colGroupOf, k, end)
				}
				w := base
				if ac != nil {
					w.add(l.borderSide(ac.box, sideBottom, rankCell, 0))
				}
				if bc != nil {
					w.add(l.borderSide(bc.box, sideTop, rankCell, 1))
				}
				if r == 0 || r == rows {
					w.add(l.borderSide(g.colBoxes[k], outer, rankColumn, 0))
					if gi := colGroupOf[k]; gi >= 0 {
						w.add(l.borderSide(g.colGroups[gi].box, outer, rankColumnGroup, 0))
					}
				}
				cg.resolved++
				won := w.result()
				if won.width > cg.hgutter[r] {
					cg.hgutter[r] = won.width
				}
				sink.add(k, stop, won)
				k = stop
			}
			at = end
		}
		cg.hoff[r+1] = int32(sink.endLine())
		above, below = below, above
	}
	cg.hruns, cg.truncated = sink.runs, cg.truncated || sink.full
}

// resolveVertical is the same walk turned ninety degrees: line c sits between
// column c-1 and column c, and the occupancy of a column is carried across from
// the column before it.
func (l *layouter) resolveVertical(table *Box, g *tableGrid, cg *collapsedGrid,
	byCol []*tableCell, colAt []int32, rowGroupStart, rowGroupEnd []*Box,
	colGroupStart, colGroupEnd []*Box) {

	rows, cols := cg.rows, cg.cols
	var sink runSink
	cg.voff = make([]int32, cols+2)

	left := make([]occupant, 0, 16)
	right := make([]occupant, 0, 16)

	for c := 0; c <= cols; c++ {
		if c < cols {
			right = nextColOccupancy(left, right, byCol[colAt[c]:colAt[c+1]], c)
		} else {
			right = right[:0]
		}

		// The part of the line every row shares: the two columns and the two
		// column groups whose edge this is. The table, the rows and the row
		// groups only meet a vertical line at the left and right edges, and the
		// rows differ from row to row, so they are added inside the walk.
		var base edgeWinner
		if c > 0 {
			base.add(l.borderSide(g.colBoxes[c-1], sideRight, rankColumn, 0))
		}
		if c < cols {
			base.add(l.borderSide(g.colBoxes[c], sideLeft, rankColumn, 1))
		}
		base.add(l.borderSide(colGroupEnd[c], sideRight, rankColumnGroup, 0))
		base.add(l.borderSide(colGroupStart[c], sideLeft, rankColumnGroup, 1))
		outer := sideLeft
		if c == cols {
			outer = sideRight
		}
		if c == 0 || c == cols {
			base.add(l.borderSide(table, outer, rankTable, 0))
		}

		i, j, at := 0, 0, 0
		for at < rows {
			for i < len(left) && left[i].to <= at {
				i++
			}
			for j < len(right) && right[j].to <= at {
				j++
			}
			var lc, rc *tableCell
			end := rows
			if i < len(left) {
				if left[i].from <= at {
					lc = left[i].cell
					if left[i].to < end {
						end = left[i].to
					}
				} else if left[i].from < end {
					end = left[i].from
				}
			}
			if j < len(right) {
				if right[j].from <= at {
					rc = right[j].cell
					if right[j].to < end {
						end = right[j].to
					}
				} else if right[j].from < end {
					end = right[j].from
				}
			}

			if lc != nil && lc == rc {
				at = end
				continue
			}
			for k := at; k < end; {
				stop := end
				if c == 0 || c == cols {
					// One row at a time: every row is a different box, so
					// nothing along the left or right edge of a table is shared
					// between two of them.
					stop = k + 1
				}
				w := base
				if lc != nil {
					w.add(l.borderSide(lc.box, sideRight, rankCell, 0))
				}
				if rc != nil {
					w.add(l.borderSide(rc.box, sideLeft, rankCell, 1))
				}
				if c == 0 || c == cols {
					w.add(l.borderSide(g.rows[k].box, outer, rankRow, 0))
					if gi := g.rows[k].group; gi >= 0 {
						w.add(l.borderSide(g.rowGroups[gi].box, outer, rankRowGroup, 0))
					}
				}
				cg.resolved++
				won := w.result()
				if won.width > cg.vgutter[c] {
					cg.vgutter[c] = won.width
				}
				sink.add(k, stop, won)
				k = stop
			}
			at = end
		}
		cg.voff[c+1] = int32(sink.endLine())
		left, right = right, left
	}
	cg.vruns, cg.truncated = sink.runs, cg.truncated || sink.full
}

// sameColumnUntil is how far a stretch of a horizontal edge line may run before
// the column describing it changes.
func sameColumnUntil(g *tableGrid, colGroupOf []int, from, end int) int {
	k := from + 1
	for k < end && g.colBoxes[k] == g.colBoxes[from] && colGroupOf[k] == colGroupOf[from] {
		k++
	}
	return k
}

// nextRowOccupancy is the occupancy of row r, built from the occupancy of the
// row before it: the cells that reach into r survive, and the cells that begin
// at r join them.
//
// Both inputs are in column order and neither overlaps the other, so the merge
// is a single pass and the result is in column order too.
func nextRowOccupancy(prev, out []occupant, starting []*tableCell, r int) []occupant {
	out = out[:0]
	i, j := 0, 0
	for i < len(prev) || j < len(starting) {
		if j >= len(starting) || (i < len(prev) && prev[i].from < starting[j].col) {
			if p := prev[i]; p.cell.row+p.cell.rowSpan > r {
				out = append(out, p)
			}
			i++
			continue
		}
		c := starting[j]
		out = append(out, occupant{from: c.col, to: c.col + c.colSpan, cell: c})
		j++
	}
	return out
}

// nextColOccupancy is the same for a column.
func nextColOccupancy(prev, out []occupant, starting []*tableCell, c int) []occupant {
	out = out[:0]
	i, j := 0, 0
	for i < len(prev) || j < len(starting) {
		if j >= len(starting) || (i < len(prev) && prev[i].from < starting[j].row) {
			if p := prev[i]; p.cell.col+p.cell.colSpan > c {
				out = append(out, p)
			}
			i++
			continue
		}
		cell := starting[j]
		out = append(out, occupant{from: cell.row, to: cell.row + cell.rowSpan, cell: cell})
		j++
	}
	return out
}

// finish turns the resolved line widths into the geometry the layout uses.
//
// # Where a grid line goes, and why nothing needs a gap
//
// A collapsed line is *inside* the two boxes it separates rather than between
// them: each takes half of it as its own used border. So a column of the grid is
// exactly a cell's border box — its content, its padding, and half of the line
// on either side — and the columns simply abut. That is what makes the
// collapsing model reuse the whole of §17.5.2's column algorithm rather than
// needing a second one: it is the separated model with no spacing at all and a
// different set of border widths.
//
// The two outermost lines are the exception, and they are what §17.6.2's "the
// width of the table includes half the width of the table's border" is about.
// Their outer half is the table's own used border and their inner half belongs
// to the first and last column, so the table's border box reaches from the
// outside edge of the first line to the outside edge of the last: a table of
// four empty cells with 20px borders is 100px across, which is exactly the area
// its borders are drawn over.
//
// Every line is split with leadingHalf on the left or above and trailingHalf on
// the right or below, and the two always add up to the whole — a line an odd
// number of layout units wide must not leave a seam between the two boxes that
// share it.
func (cg *collapsedGrid) finish(g *tableGrid) {
	cg.table = Edges{
		Top:    leadingHalf(cg.hgutter[0]),
		Right:  trailingHalf(cg.vgutter[cg.cols]),
		Bottom: trailingHalf(cg.hgutter[cg.rows]),
		Left:   leadingHalf(cg.vgutter[0]),
	}

	cg.cells = make(map[*Box]Edges, len(g.cells))
	for _, c := range g.cells {
		cg.cells[c.box] = Edges{
			Top:    trailingHalf(cg.hgutter[min(c.row, cg.rows)]),
			Right:  leadingHalf(cg.vgutter[min(c.col+c.colSpan, cg.cols)]),
			Bottom: leadingHalf(cg.hgutter[min(c.row+c.rowSpan, cg.rows)]),
			Left:   trailingHalf(cg.vgutter[min(c.col, cg.cols)]),
		}
	}
}

// ---------------------------------------------------------------------------
// Painting
// ---------------------------------------------------------------------------

// collapsedBand is one stretch of grid line, ready to draw, in coordinates
// relative to the table's border box.
type collapsedBand struct {
	rect  Rect
	box   *Box
	side  side
	kind  borderStyle
	width style.Unit
}

// bands turns the resolved runs into rectangles, once the columns and rows have
// their sizes.
//
// lineX[c] is where vertical grid line c starts and lineY[r] where horizontal
// line r starts, both relative to the table's border box. A border narrower than
// its line is centred in it, which is where the space goes when one row asks for
// a thinner border than another on the same line.
//
// Each band is extended over the lines that cross it, so that the square where
// two lines meet is covered twice rather than left blank. Which of the two
// covers it is decided by the order they are drawn in — see paintCollapsed.
func (cg *collapsedGrid) bands(lineX, lineY []style.Unit) []collapsedBand {
	out := make([]collapsedBand, 0, len(cg.hruns)+len(cg.vruns))
	for r := 0; r <= cg.rows; r++ {
		for _, run := range cg.hruns[cg.hoff[r]:cg.hoff[r+1]] {
			y := lineY[r].Add(leadingHalf(cg.hgutter[r].Sub(run.win.width)))
			x0 := lineX[run.from]
			x1 := lineX[run.to].Add(cg.vgutter[run.to])
			out = append(out, collapsedBand{
				rect: Rect{X: x0, Y: y, W: x1.Sub(x0), H: run.win.width},
				box:  run.win.box, side: run.win.side,
				kind: run.win.kind, width: run.win.width,
			})
		}
	}
	for c := 0; c <= cg.cols; c++ {
		for _, run := range cg.vruns[cg.voff[c]:cg.voff[c+1]] {
			x := lineX[c].Add(leadingHalf(cg.vgutter[c].Sub(run.win.width)))
			y0 := lineY[run.from]
			y1 := lineY[run.to].Add(cg.hgutter[run.to])
			out = append(out, collapsedBand{
				rect: Rect{X: x, Y: y0, W: run.win.width, H: y1.Sub(y0)},
				box:  run.win.box, side: run.win.side,
				kind: run.win.kind, width: run.win.width,
			})
		}
	}
	return out
}

// paintCollapsed draws a table's grid lines.
//
// They are drawn by the table rather than by each cell, which is what §17.6.2
// requires and what makes the whole model work: a cell owns half of the line
// beside it and the neighbour owns the other half, so neither of them can draw
// it. They are also drawn after every background in the table — the table's own,
// the column groups', the rows', the cells' — because a border centred on a grid
// line runs through the edge of two cells and under a row's background, and a
// background painted over it would erase it.
//
// The narrower bands go first, so that where two lines of different widths meet
// the wider one owns the corner. That is the visible half of §17.6.2's ordering:
// a thick rule crossing a hairline should not be nicked by it.
func (p *painter) paintCollapsed(f *Fragment) {
	if len(f.collapsed) == 0 {
		return
	}
	// The table's own clip rather than its content clip. A collapsed grid line
	// is half in the cell on each side of it, and the ones at the table's edge
	// are centred on the table's border box — outside the padding box a
	// "table { overflow: hidden }" clips its contents to. Cutting them there
	// would erase the frame of every collapsing table that also declared an
	// overflow, which is not what §11.1.1 clips: the outer grid lines are the
	// table's own border by another name.
	p.clipping(f.clipSelf, func() { p.paintCollapsedBands(f) })
}

func (p *painter) paintCollapsedBands(f *Fragment) {
	order := make([]int, len(f.collapsed))
	for i := range order {
		order[i] = i
	}
	// Sorted over indices rather than over the bands themselves: a band is the
	// larger value and a table can have tens of thousands of them. Stable, so
	// that two bands of the same width keep the order bands built them in — the
	// horizontal lines and then the vertical ones, which puts the vertical rule
	// over the horizontal one where two equal borders cross.
	sort.SliceStable(order, func(i, j int) bool {
		return f.collapsed[order[i]].width < f.collapsed[order[j]].width
	})

	for _, i := range order {
		b := f.collapsed[i]
		colour, ok := p.color(b.box, borderColorProp[b.side])
		if !ok || colour.A == 0 {
			continue
		}
		band := Rect{
			X: f.BorderRect.X.Add(b.rect.X), Y: f.BorderRect.Y.Add(b.rect.Y),
			W: b.rect.W, H: b.rect.H,
		}
		p.paintEdge(band, b.kind, colour, b.side, b.width)
	}
}
