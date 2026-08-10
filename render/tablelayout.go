package render

import (
	"strconv"
	"strings"

	"github.com/mgilbir/pdf0/css"
	"github.com/mgilbir/pdf0/style"
)

// Table layout: CSS 2.1 §17.5 and §17.6.
//
// The grid is built first, then the columns are given widths, then the rows are
// given heights, and only then does anything have a position. That order is
// forced rather than chosen: a cell's height depends on its width, its width
// depends on every other cell in its column, and the table's own width depends
// on all of them at once. Nothing here can be done in one pass over the boxes.
//
// # The two border models
//
// The separated model of §17.6.1, with border-spacing and empty-cells, and the
// collapsing model of §17.6.2, which lives in bordercollapse.go. They are not
// two settings of one algorithm: in the separated model every cell has its own
// border and the gaps between them are empty page, and in the collapsing model
// the borders belong to the *grid* and each cell owns half of the four lines
// around it.
//
// The second model changes almost nothing here, and that is the point of how it
// is arranged. A collapsed grid line lies *inside* the two boxes that share it —
// half of it is each one's used border — so the columns abut, the spacing is
// zero, and every column width, row height and position below is the same
// arithmetic it always was. What differs is what a cell's border comes out to,
// which borderWidths answers, and who draws it, which the table does.

// ---------------------------------------------------------------------------
// The grid
// ---------------------------------------------------------------------------

// The table grid: §17.5's model of a table as a rectangle of slots, each holding
// at most one cell, with a cell occupying a rectangle of them.
//
// It is built once and read by everything after it, because every question the
// layout asks — how wide is column three, how tall is row two, where does this
// cell start — is a question about the grid and not about the box tree. The box
// tree cannot answer them: a cell's column is decided by how many slots the
// cells before it took, which is a running total the tree does not record.
//
// # What is bounded here, and why it has to be
//
// colspan and rowspan are attacker-controlled integers in a document this engine
// is expected to accept from anywhere. "colspan=99999999" is the shortest denial
// of service in HTML: an engine that trusted it would allocate a grid the width
// of the number. HTML's own parsing limits are 1000 for colspan and 65534 for
// rowspan and they are applied here, together with a bound on the whole grid —
// a thousand cells each spanning a thousand columns is inside HTML's limits and
// is still a million columns.
//
// A rowspan costs nothing to bound because it is never materialised: occupancy
// is one integer per *column* saying which row frees it, so "rowspan=65534" in a
// two-row table allocates nothing at all. It is clipped to the row group in any
// case, which is what §17.5.1 requires and what stops a cell in one section from
// reaching into the next.

// maxTableColumns bounds the width of a grid.
//
// It is a variable rather than a constant so that a test can lower it and watch
// it fire without building a document four thousand columns wide. A bound that
// has only ever been observed not to trip is one nobody knows works.
var maxTableColumns = 1 << 12

// The two span limits are HTML's own, and are the numbers a browser's parser
// clamps to.
const (
	maxColSpan = 1000
	maxRowSpan = 65534
)

// tableGrid is a table's cells placed in slots.
type tableGrid struct {
	// cols is the number of columns, which is the widest row's reach and is not
	// something any one part of the markup declares.
	cols int
	rows []tableRowInfo
	// cells are in document order, each knowing where it starts and how far it
	// reaches. There is deliberately no slot-to-cell array: everything the
	// layout needs is answered by iterating this, and materialising the whole
	// rectangle would be the one allocation the span limits exist to prevent.
	cells []*tableCell
	// colBoxes is the <col> describing each column, or nil. It is per column
	// rather than per element because a <col span=3> describes three of them.
	colBoxes  []*Box
	colGroups []tableSpan
	rowGroups []tableSpan
}

// tableSpan is a group box and the range of rows or columns it covers, which is
// the area its background is painted over.
type tableSpan struct {
	box   *Box
	first int
	count int
}

type tableRowInfo struct {
	box *Box
	// group indexes rowGroups, or is -1 for a row written directly in a table.
	group int
}

type tableCell struct {
	box              *Box
	row, col         int
	rowSpan, colSpan int
}

// tableGridFor builds a table's grid, memoized.
//
// The memo is not an optimisation. The grid is asked for once while the table's
// width is being resolved and again while its content is laid out, and a table
// inside a float is measured before it is laid out as well — so a table nested
// n deep would cost 2^n without it.
func (l *layouter) tableGridFor(table *Box) *tableGrid {
	if g, ok := l.grids[table]; ok {
		return g
	}
	g := l.buildTableGrid(table)
	l.grids[table] = g
	return g
}

func (l *layouter) buildTableGrid(table *Box) *tableGrid {
	g := &tableGrid{}
	l.collectColumns(table, g)

	// §17.5.1's ordering: the header first and the footer last, wherever they
	// were written. Only the first of each moves — a second <thead> is a body
	// group where it stands, which is what browsers do and what stops a document
	// with three of them having its rows shuffled.
	var head, foot *rowRun
	var runs []*rowRun
	for _, c := range table.Children {
		switch {
		case isRowGroup(c):
			run := &rowRun{group: c}
			for _, r := range c.Children {
				if r.Inner == InnerTableRow {
					run.rows = append(run.rows, r)
				}
			}
			switch strings.ToLower(strings.TrimSpace(c.Style["display"])) {
			case "table-header-group":
				if head == nil {
					head = run
					continue
				}
			case "table-footer-group":
				if foot == nil {
					foot = run
					continue
				}
			}
			runs = append(runs, run)
		case c.Inner == InnerTableRow:
			// A row written directly in the table. Consecutive ones share a run,
			// so a rowspan among them is clipped to the same range a real row
			// group would have given it.
			if n := len(runs); n > 0 && runs[n-1].group == nil {
				runs[n-1].rows = append(runs[n-1].rows, c)
				continue
			}
			runs = append(runs, &rowRun{rows: []*Box{c}})
		}
	}
	ordered := make([]*rowRun, 0, len(runs)+2)
	if head != nil {
		ordered = append(ordered, head)
	}
	ordered = append(ordered, runs...)
	if foot != nil {
		ordered = append(ordered, foot)
	}

	for _, run := range ordered {
		l.placeRun(g, run)
	}
	if len(g.colBoxes) > g.cols {
		g.cols = len(g.colBoxes)
	}
	for len(g.colBoxes) < g.cols {
		g.colBoxes = append(g.colBoxes, nil)
	}
	return g
}

// rowRun is a row group and its rows, or a stretch of rows written directly in
// a table.
type rowRun struct {
	group *Box
	rows  []*Box
}

// collectColumns reads the <col> and <colgroup> structure.
//
// A column group with no columns inside it still describes columns — that is
// what its span attribute is for — so the two cases produce the same thing and
// differ only in where the count comes from.
func (l *layouter) collectColumns(table *Box, g *tableGrid) {
	add := func(col *Box, n int) {
		if col != nil {
			// A column box never reaches block layout — it produces no fragment of
			// its own — so the two value checks blockIn makes for every other box
			// are asked here.
			l.checkVisibility(col)
			l.checkTableBoxSizing(col)
		}
		for i := 0; i < n && len(g.colBoxes) < maxTableColumns; i++ {
			g.colBoxes = append(g.colBoxes, col)
		}
	}
	for _, c := range table.Children {
		switch c.Inner {
		case InnerTableColumnGroup:
			start := len(g.colBoxes)
			if len(c.Children) == 0 {
				add(nil, spanAttr(c))
			}
			for _, col := range c.Children {
				add(col, spanAttr(col))
			}
			g.colGroups = append(g.colGroups, tableSpan{
				box: c, first: start, count: len(g.colBoxes) - start,
			})
		case InnerTableColumn:
			add(c, spanAttr(c))
		}
	}
}

// placeRun assigns one row group's cells to slots.
//
// until[c] is the row at which column c becomes free again, and it is the whole
// of the rowspan bookkeeping: one integer per column, so a span of sixty
// thousand rows costs exactly what a span of two costs.
func (l *layouter) placeRun(g *tableGrid, run *rowRun) {
	group := -1
	first := len(g.rows)
	if run.group != nil {
		group = len(g.rowGroups)
		g.rowGroups = append(g.rowGroups, tableSpan{box: run.group, first: first})
	}

	var until []int
	for ri, rowBox := range run.rows {
		g.rows = append(g.rows, tableRowInfo{box: rowBox, group: group})

		col := 0
		for _, cb := range rowBox.Children {
			if cb.Inner != InnerTableCell {
				continue
			}
			for col < len(until) && until[col] > ri {
				col++
			}
			if col >= maxTableColumns {
				l.rec.Report(RuleLimit, AtHTML(offsetOf(cb)),
					"the table reaches more columns than this engine will lay out; "+
						"the cells past the limit are not on the page")
				break
			}
			colSpan := spanValue(cb, "colspan", maxColSpan)
			rowSpan := spanValue(cb, "rowspan", maxRowSpan)
			if rowSpan == 0 {
				// HTML's "to the end of this row group", which is the one place
				// a zero is meaningful rather than invalid.
				rowSpan = len(run.rows) - ri
			}
			// §17.5.1: a cell may not reach past its row group. Clipping here
			// rather than at each use keeps every later loop over the spanned
			// rows bounded by rows that exist.
			if rowSpan > len(run.rows)-ri {
				rowSpan = len(run.rows) - ri
			}
			if col+colSpan > maxTableColumns {
				colSpan = maxTableColumns - col
			}
			for len(until) < col+colSpan {
				until = append(until, 0)
			}
			for k := col; k < col+colSpan; k++ {
				until[k] = ri + rowSpan
			}
			g.cells = append(g.cells, &tableCell{
				box: cb, row: first + ri, col: col,
				rowSpan: rowSpan, colSpan: colSpan,
			})
			col += colSpan
		}
		if len(until) > g.cols {
			g.cols = len(until)
		}
	}
	if group >= 0 {
		g.rowGroups[group].count = len(g.rows) - first
	}
}

// spanValue reads a colspan or rowspan attribute, clamped.
//
// An absent, unreadable or negative value is one, which is what HTML says and is
// the only safe answer: a span of zero would put two cells in one slot and a
// negative one would walk the column index backwards.
func spanValue(b *Box, name string, limit int) int {
	if b.Element == nil {
		return 1
	}
	raw, ok := b.Element.Attr(name)
	if !ok {
		return 1
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 1
	}
	if n == 0 && name != "rowspan" {
		// Zero is a value only rowspan takes; everywhere else HTML gives it one.
		return 1
	}
	if n > limit {
		return limit
	}
	return n
}

// spanAttr reads a <col> or <colgroup> span, which is at least one.
func spanAttr(b *Box) int {
	if n := spanValue(b, "span", maxColSpan); n >= 1 {
		return n
	}
	return 1
}

// ---------------------------------------------------------------------------
// Spacing and the border model
// ---------------------------------------------------------------------------

// tableSpacing is what separates one column from the next and one row from the
// next: §17.6.1's border-spacing.
//
// In §17.6.2's collapsing model there is nothing between them — a grid line lies
// *inside* the two boxes that share it, half in each — so the spacing is zero
// and the whole of §17.5.2 below runs unchanged. What the collapsed grid is
// carried here for is the two questions that are not about spacing: whether
// empty-cells applies, and what the table has to draw.
type tableSpacing struct {
	h, v style.Unit
	// collapsed is §17.6.2's resolved grid, or nil in the separated model.
	collapsed *collapsedGrid
}

// spacingOf reads the border model a table asks for.
//
// border-spacing takes one length or two: one applies to both axes, two are
// horizontal then vertical. It applies only in the separated model, so
// "border-collapse: collapse" gives zero whatever the spacing says — and the
// resolved grid instead.
func (l *layouter) spacingOf(table *Box) tableSpacing {
	if borderCollapses(table) {
		return tableSpacing{collapsed: l.collapsedGridFor(table)}
	}

	raw := strings.TrimSpace(table.Style["border-spacing"])
	if raw == "" {
		return tableSpacing{}
	}
	fields := strings.Fields(raw)
	read := func(s string) style.Unit {
		v, ok := l.lengthOfValue(table, s)
		if !ok || v < 0 {
			return 0
		}
		return v
	}
	switch len(fields) {
	case 1:
		v := read(fields[0])
		return tableSpacing{h: v, v: v}
	case 2:
		return tableSpacing{h: read(fields[0]), v: read(fields[1])}
	}
	return tableSpacing{}
}

// lengthOfValue resolves a raw length that is one part of a multi-value
// property, which parseLength cannot be asked for because it reads a whole
// declaration.
func (l *layouter) lengthOfValue(b *Box, raw string) (style.Unit, bool) {
	key := lengthKey{value: raw, fontSize: b.FontSize}
	length, ok := l.lengths[key]
	if !ok {
		vals, _ := css.ParseComponentValues(raw)
		parsed, _, valid := style.ParseLength(vals, style.LengthContext{
			FontSize:       b.FontSize,
			RootFontSize:   b.FontSize,
			ViewportWidth:  l.avail.W,
			ViewportHeight: l.avail.H,
			ViewportKnown:  true,
		})
		if !valid {
			return 0, false
		}
		l.lengths[key] = parsed
		length = parsed
	}
	return length.Resolve(0, true)
}

// ---------------------------------------------------------------------------
// Column widths
// ---------------------------------------------------------------------------

// tableColumnDemand is what one column asks for: the width below which its
// content overflows, and the width at which nothing in it would wrap.
type tableColumnDemand struct {
	min, max style.Unit
	// percent is a percentage width asked for by a column or by a cell in it,
	// or zero. It is kept apart from the two lengths because it is not a demand
	// on the content at all — it is a claim on the table's final width, which is
	// not known while the demands are being collected.
	percent float64
}

// tableColumnDemands measures every column, memoized per table.
//
// # The knowing divergences, and why
//
// §17.5.2.2 is the least specified algorithm in CSS 2.1: it names the two widths
// a cell contributes and then says the distribution "should" be done in a way it
// does not define. What is here is the arrangement browsers converged on, with
// three places where a choice had to be made:
//
//   - A cell's declared width raises both its minimum and its maximum, rather
//     than being a third quantity. A column holding "width: 200px" is at least
//     200 wide, which is what an author who wrote it expects, and treating it as
//     a preference that content could shrink below made the declaration look
//     ignored.
//   - A percentage is *not* a demand on the content. CSS Sizing says a
//     percentage that cannot be resolved behaves as auto for intrinsic sizing,
//     so it contributes nothing here and is applied as a claim once the table
//     has a width. What is given up is a table whose width should have grown to
//     satisfy a percentage column; the percentage is honoured against the width
//     the table gets instead.
//   - A cell spanning several columns distributes its excess demand over them in
//     proportion to their maxima, and equally when every maximum is zero. The
//     specification says only that the excess "should be distributed"; in
//     proportion is what keeps a wide column wide.
func (l *layouter) tableColumnDemands(table *Box, s tableSpacing) []tableColumnDemand {
	if got, ok := l.tableDemands[table]; ok {
		return got
	}
	g := l.tableGridFor(table)
	out := make([]tableColumnDemand, g.cols)
	// Recorded before the work rather than after, for the reason contentWidths
	// gives: a shared subtree measured once per path to it is the cost this memo
	// exists to avoid.
	l.tableDemands[table] = out

	for i, col := range g.colBoxes {
		if col == nil {
			continue
		}
		length, ok := l.parseLength(col, "width")
		if !ok {
			continue
		}
		switch length.Kind {
		case style.LengthAbsolute:
			out[i].min = style.Max(out[i].min, length.Value)
			out[i].max = style.Max(out[i].max, length.Value)
		case style.LengthPercent:
			out[i].percent = max(out[i].percent, length.Percent)
		}
	}

	// The cells that occupy one column each settle it; the spanning ones then
	// top up whatever they are still short of. Doing it in that order is what
	// makes the answer independent of the order the cells were written in.
	for _, c := range g.cells {
		if c.colSpan != 1 {
			continue
		}
		lo, hi, pct := l.cellDemand(c.box)
		out[c.col].min = style.Max(out[c.col].min, lo)
		out[c.col].max = style.Max(out[c.col].max, hi)
		out[c.col].percent = max(out[c.col].percent, pct)
	}
	for _, c := range g.cells {
		if c.colSpan == 1 {
			continue
		}
		lo, hi, _ := l.cellDemand(c.box)
		// The spacing between the columns a cell spans is width the cell gets
		// for free: a cell two columns wide sits across the gap between them.
		gaps := s.h.Mul(float64(c.colSpan - 1))
		spreadDemand(out[c.col:c.col+c.colSpan], lo.Sub(gaps), hi.Sub(gaps))
	}
	for i := range out {
		if out[i].max < out[i].min {
			out[i].max = out[i].min
		}
	}
	return out
}

// cellDemand is a cell's two widths, measured over its border box, plus any
// percentage width it declares.
//
// Margins are deliberately not added: "margin" does not apply to a table-cell,
// and adding a declared one would make a stylesheet that sets a margin on every
// element quietly widen every column.
func (l *layouter) cellDemand(cell *Box) (min, max style.Unit, percent float64) {
	inner := l.contentWidths(cell)
	if length, ok := l.parseLength(cell, "width"); ok {
		switch length.Kind {
		case style.LengthAbsolute:
			inner.min = style.Max(inner.min, length.Value)
			inner.max = style.Max(inner.max, length.Value)
		case style.LengthPercent:
			percent = length.Percent
		}
	}
	edges := l.cellInset(cell)
	return inner.min.Add(edges), inner.max.Add(edges), percent
}

// cellInset is what a cell's border box holds besides its content: the
// horizontal padding and border together.
//
// Under §17.6.2 borderWidths gives half of each grid line rather than the
// declared border, which is exactly what a column has to make room for — the
// other half lies in the cell next door.
//
// A percentage padding resolves against zero here, which is deliberate and is
// the same choice the automatic algorithm makes: the containing block a cell's
// padding would resolve against is the column this is helping to decide.
func (l *layouter) cellInset(cell *Box) style.Unit {
	return l.borderWidths(cell).Horizontal().
		Add(l.paddingOf(cell, 0).Horizontal())
}

// spreadDemand raises a run of columns so that they can hold a cell that spans
// them, distributing the shortfall in proportion to what each already asks for.
func spreadDemand(cols []tableColumnDemand, min, max style.Unit) {
	spread := func(want style.Unit, get func(*tableColumnDemand) *style.Unit) {
		var have style.Unit
		weights := make([]float64, len(cols))
		for i := range cols {
			v := *get(&cols[i])
			have = have.Add(v)
			weights[i] = float64(cols[i].max)
		}
		if want <= have {
			return
		}
		add := make([]style.Unit, len(cols))
		distribute(want.Sub(have), weights, add)
		for i := range cols {
			p := get(&cols[i])
			*p = p.Add(add[i])
		}
	}
	spread(min, func(d *tableColumnDemand) *style.Unit { return &d.min })
	spread(max, func(d *tableColumnDemand) *style.Unit { return &d.max })
}

// distribute shares an amount over a set of weights so that the parts add up to
// exactly the whole.
//
// The running total is what makes that true: each part is the difference between
// two roundings of a cumulative fraction, so the errors cancel instead of
// accumulating. Rounding each share on its own leaves a table a unit or two
// narrower than its columns, which shows as a hairline down the right edge.
func distribute(total style.Unit, weights []float64, out []style.Unit) {
	if len(weights) == 0 {
		return
	}
	var sum float64
	for _, w := range weights {
		if w > 0 {
			sum += w
		}
	}
	equal := sum <= 0
	if equal {
		sum = float64(len(weights))
	}
	var acc float64
	var given style.Unit
	for i := range weights {
		w := weights[i]
		if equal {
			w = 1
		} else if w < 0 {
			w = 0
		}
		acc += w
		want := total.Mul(acc / sum)
		out[i] = out[i].Add(want.Sub(given))
		given = want
	}
}

// tableGridWidths is the pair §17.5.2.2 calls MIN and MAX: the narrowest and
// widest the whole grid can be, spacing included.
func (l *layouter) tableGridWidths(table *Box) (min, max style.Unit) {
	s := l.spacingOf(table)
	g := l.tableGridFor(table)
	demands := l.tableColumnDemands(table, s)
	for _, d := range demands {
		min = min.Add(d.min)
		max = max.Add(d.max)
	}
	gaps := s.h.Mul(float64(g.cols + 1))
	return min.Add(gaps), max.Add(gaps)
}

// tableUsedWidth is §17.5.2's choice of table width.
//
// available is what the containing block leaves after the table's own border and
// padding, so everything here is a content width.
//
// The auto case is the specification's own formula, and it is worth reading
// twice because it is not shrink-to-fit: the table is as wide as the containing
// block *unless* its content wants less, in which case it is as wide as its
// content wants. A table with two short columns does not fill the page, and a
// table with more content than fits is as wide as its minimum and overflows.
func (l *layouter) tableUsedWidth(table *Box, available style.Unit) style.Unit {
	declared, hasDeclared := l.declaredTableWidth(table, available)
	if hasDeclared && tableLayoutIsFixed(table) {
		// §17.5.2.1: the fixed algorithm's whole promise is that the table is
		// the width it was given and the content does not argue. Flooring it by
		// the content's minimum, which is what the automatic algorithm does,
		// would take the promise back — a table declared 300px wide holding one
		// long word would come out as wide as the word.
		return maxZero(declared)
	}

	min, max := l.tableGridWidths(table)
	min = style.Max(min, l.captionMinWidth(table))
	if hasDeclared {
		// A declared width is a floor of the minimum, not a licence to squeeze
		// the content out of the table: §17.5.2 makes the used width the greater
		// of the two.
		return style.Max(declared, min)
	}
	if max < available {
		return style.Max(max, min)
	}
	return style.Max(min, available)
}

// declaredTableWidth resolves a width the author put on the table, as a content
// width.
//
// Everything downstream wants a content width — that is what layout.go's caller
// treats a declared width as, and what columnWidths divides — so htmlTableWidth's
// conversion happens once, here.
func (l *layouter) declaredTableWidth(table *Box, available style.Unit) (style.Unit, bool) {
	length, ok := l.parseLength(table, "width")
	if !ok || length.Kind == style.LengthAuto {
		return 0, false
	}
	v, ok := length.Resolve(available, true)
	if !ok {
		return 0, false
	}
	if htmlTableWidth(table) {
		v = maxZero(v.Sub(l.tableEdges(table)))
	}
	return v, true
}

// htmlTableWidth reports whether a declared width on this table is a width of its
// *border* box rather than of its content box.
//
// It is the divergence §17.6.1 names, and it is one of the few places in CSS 2.1
// where "width" does not mean what it means everywhere else:
//
//	The width of the table is the distance from the left inner padding edge to
//	the right inner padding edge (including the border spacing but excluding
//	padding and border). However, in HTML and XHTML1, the width of the <table>
//	element is the distance from the left border edge to the right border edge.
//
// So the answer turns on the *element*, not on the display value and not on the
// border model: a <table> is measured to its border edges and a div with
// "display: table" is measured to its content edges, and the two are otherwise
// the same box.
//
// The suite tests both sides against each other with almost the same document,
// which is what makes this decidable rather than a reading:
//
//   - separated-border-model-003b is a <table> and asserts "the width of an
//     HTML/XHTML table is the distance between the left and right table border
//     edges".
//   - separated-border-model-004b is a div with "display: table" and asserts "the
//     width of a CSS table is the distance from the left inner padding edge to
//     the right inner padding edge ... excluding table padding and table
//     borders".
//
// Getting the scope wrong is expensive in both directions and was measured
// rather than guessed. Applying the border-edge reading to every table fixed 8
// tests and broke 42 — every border-applies-to, border-color-applies-to and
// background-position-applies-to case sizes a "display: table" box, and each one
// came out a border narrower than the square it has to match.
func htmlTableWidth(table *Box) bool {
	return table.Element != nil && table.Element.Name == "table"
}

// tableEdges is the table box's own horizontal border and padding.
//
// Under §17.6.2 borderWidths gives half of the collapsed outer line, which is the
// part of it that lies inside the table box and so the part a declared width
// covers.
func (l *layouter) tableEdges(table *Box) style.Unit {
	return l.borderWidths(table).Horizontal().
		Add(l.paddingOf(table, 0).Horizontal())
}

// tableLayoutIsFixed reports whether §17.5.2.1's algorithm applies.
//
// It needs a width to divide, and §17.5.2.1 says in as many words that a table
// whose width is auto uses the automatic algorithm whatever table-layout asks
// for. So the caller checks the width and this checks only the keyword.
func tableLayoutIsFixed(table *Box) bool {
	return strings.EqualFold(strings.TrimSpace(table.Style["table-layout"]), "fixed")
}

// captionMinWidth is §17.5.2's CAPMIN: the narrowest the captions can be.
//
// A caption cannot make the table wider than the widest caption *needs* to be —
// only its minimum counts, not its preferred width. A paragraph of prose in a
// <caption> wraps; it does not stretch the table across the page.
func (l *layouter) captionMinWidth(table *Box) style.Unit {
	wrapper := table.Parent
	if wrapper == nil || !wrapper.TableWrapper {
		return 0
	}
	var out style.Unit
	for _, c := range wrapper.Children {
		if c.Inner != InnerTableCaption {
			continue
		}
		out = style.Max(out, l.outerWidths(c, 0).min)
	}
	// CAPMIN is a border-box width of the table; what this returns is compared
	// against a content width, so the table's own edges come off it.
	edges := l.borderWidths(table).Horizontal().
		Add(l.paddingOf(table, 0).Horizontal())
	return maxZero(out.Sub(edges))
}

// columnWidths distributes a settled table width over the columns.
func (l *layouter) columnWidths(table *Box, width style.Unit, s tableSpacing) []style.Unit {
	g := l.tableGridFor(table)
	if g.cols == 0 {
		return nil
	}
	room := maxZero(width.Sub(s.h.Mul(float64(g.cols + 1))))
	if _, ok := l.declaredTableWidth(table, width); ok && tableLayoutIsFixed(table) {
		return l.fixedColumnWidths(table, g, room, s)
	}
	return l.autoColumnWidths(table, room, s)
}

// autoColumnWidths is §17.5.2.2's distribution.
//
// Three regimes, and the middle one is the interesting one: between the grid's
// minimum and its maximum the surplus goes where it does the most good, which is
// in proportion to how much each column would still like. A column already at
// its maximum gets none of it, so a table of one long paragraph and one short
// word does not give them half each.
func (l *layouter) autoColumnWidths(table *Box, room style.Unit, s tableSpacing) []style.Unit {
	demands := l.tableColumnDemands(table, s)
	out := make([]style.Unit, len(demands))

	var min, max style.Unit
	for _, d := range demands {
		min = min.Add(d.min)
		max = max.Add(d.max)
	}
	for i, d := range demands {
		out[i] = d.min
	}

	switch {
	case room <= min:
		// Narrower than the content can be. The columns keep their minima and
		// the table overflows, which is what §17.5.2.2 asks for: a table is
		// never squeezed below the width its content needs.
	case room <= max:
		weights := make([]float64, len(demands))
		for i, d := range demands {
			weights[i] = float64(d.max.Sub(d.min))
		}
		distribute(room.Sub(min), weights, out)
	default:
		for i, d := range demands {
			out[i] = d.max
		}
		// Wider than anything wants. §17.5.2.2 does not say where the surplus
		// goes; in proportion to what each column already holds is what browsers
		// do, and it keeps the relative widths the content asked for.
		weights := make([]float64, len(demands))
		for i, d := range demands {
			weights[i] = float64(d.max)
		}
		distribute(room.Sub(max), weights, out)
	}

	l.applyPercentages(demands, out, room)
	return out
}

// applyPercentages honours a percentage column width against the width the table
// ended up with.
//
// It runs last because a percentage is a claim on a total that the rest of the
// algorithm is still deciding. What a column claims is taken from the columns
// that have room above their minimum, in proportion to how much room they have,
// so a "width: 50%" column gets its half and nothing is pushed below the width
// its content needs. A set of percentages adding to more than the whole is
// satisfied as far as the space allows and no further.
func (l *layouter) applyPercentages(demands []tableColumnDemand, out []style.Unit, room style.Unit) {
	var wanted style.Unit
	for i, d := range demands {
		if d.percent <= 0 {
			continue
		}
		if target := room.Mul(d.percent / 100); target > out[i] {
			wanted = wanted.Add(target.Sub(out[i]))
		}
	}
	if wanted == 0 {
		return
	}

	// What the other columns can give up without going below their minima.
	weights := make([]float64, len(out))
	var slack style.Unit
	for i, d := range demands {
		if d.percent > 0 {
			continue
		}
		if give := out[i].Sub(d.min); give > 0 {
			weights[i] = float64(give)
			slack = slack.Add(give)
		}
	}
	take := style.Min(wanted, slack)
	if take <= 0 {
		return
	}
	taken := make([]style.Unit, len(out))
	distribute(take, weights, taken)
	for i := range out {
		out[i] = out[i].Sub(taken[i])
	}

	// And hand it out, in proportion to what each percentage column asked for so
	// that a shortfall is shared rather than falling on the last one.
	gains := make([]float64, len(out))
	for i, d := range demands {
		if d.percent <= 0 {
			continue
		}
		if target := room.Mul(d.percent / 100); target > out[i] {
			gains[i] = float64(target.Sub(out[i]))
		}
	}
	add := make([]style.Unit, len(out))
	distribute(take, gains, add)
	for i := range out {
		out[i] = out[i].Add(add[i])
	}
}

// fixedColumnWidths is §17.5.2.1.
//
// The point of the fixed algorithm is that it does not look at the content, so a
// table can be laid out as soon as its first row has been read. Everything it
// needs is in the column elements and the first row: a declared width wins, a
// cell in the first row settles a column that no <col> described, and whatever
// is left over is shared equally between the columns nobody said anything about.
//
// It is the one place where content is knowingly clipped, so it is the one place
// this engine reports a column narrower than what is in it.
//
// # What a cell's declared width is a width of
//
// §17.5.2.1 says a first-row cell's "width" determines the width of its column
// and does not say which box that width is of. The answer is the same as
// everywhere else in CSS: "width" is the *content* box, so what the column has to
// hold is that value plus the cell's horizontal padding and border. The
// specification is thin enough on this that the working group was asked to
// clarify it — the suite's fixed-table-layout-003* family links the thread and
// then writes the arithmetic out in a comment in each reference, which is as
// close to a normative statement as this rule has.
//
// It is not a detail. "width: 80px; padding: 0 60px" is a 200px column, and
// reading the 80 as the whole of it makes the column two and a half times too
// narrow — and then hands the 120px difference to the columns that had no width
// at all, so every column in the table moves.
//
// Under §17.6.2 the cell's border is half of each grid line it touches rather
// than the border it declared, which is what borderWidths already answers, so
// the collapsed case needs nothing of its own here: a 60px collapsed border
// contributes 30 to the column and the other 30 lies in the neighbour.
//
// "box-sizing: border-box" would make the declared value the border box and this
// addition wrong. It is not handled, for the reason checkTableBoxSizing gives:
// box-sizing is not applied to any table box, and saying so is what that finding
// is for.
func (l *layouter) fixedColumnWidths(table *Box, g *tableGrid, room style.Unit,
	s tableSpacing) []style.Unit {

	out := make([]style.Unit, g.cols)
	set := make([]bool, g.cols)

	assign := func(from, span int, width style.Unit) {
		// A width declared on a cell spanning several columns is divided over
		// them, minus the spacing it swallows.
		width = maxZero(width.Sub(s.h.Mul(float64(span - 1))))
		share := width.Div(float64(span))
		for k := from; k < from+span && k < len(out); k++ {
			if set[k] {
				continue
			}
			out[k], set[k] = share, true
		}
	}

	for i, col := range g.colBoxes {
		if col == nil {
			continue
		}
		if v, ok := l.lengthOf(col, "width", room); ok && !l.isAuto(col, "width") {
			out[i], set[i] = maxZero(v), true
		}
	}
	for _, c := range g.cells {
		if c.row != 0 {
			// Only the first row speaks. That is the whole of what makes the
			// algorithm fixed.
			continue
		}
		if v, ok := l.lengthOf(c.box, "width", room); ok && !l.isAuto(c.box, "width") {
			assign(c.col, c.colSpan, maxZero(v).Add(l.cellInset(c.box)))
		}
	}

	var used style.Unit
	free := 0
	for i := range out {
		if set[i] {
			used = used.Add(out[i])
			continue
		}
		free++
	}
	if free > 0 {
		weights := make([]float64, len(out))
		for i := range out {
			if !set[i] {
				weights[i] = 1
			}
		}
		distribute(maxZero(room.Sub(used)), weights, out)
	} else if used < room {
		// Every column was declared and they do not fill the table. The surplus
		// is spread over them in proportion, which is what keeps a fixed table
		// with a declared width the width it was given.
		weights := make([]float64, len(out))
		for i := range out {
			weights[i] = float64(out[i])
		}
		distribute(room.Sub(used), weights, out)
	}

	l.reportColumnUnderflow(table, out, s)
	return out
}

// reportColumnUnderflow names a column too narrow for what is in it.
//
// This is §6.2's silent clip in its table-shaped form: the text is there, the
// column is there, and the part past the edge is simply not drawn. The fixed
// algorithm produces it by design — that is the trade it offers — so the author
// is told rather than left to notice.
func (l *layouter) reportColumnUnderflow(table *Box, widths []style.Unit, s tableSpacing) {
	demands := l.tableColumnDemands(table, s)
	for i, w := range widths {
		if i >= len(demands) || w >= demands[i].min {
			continue
		}
		l.rec.ReportDetail(Finding{
			Rule:   RuleTableColumnUnderflow,
			Source: AtHTML(offsetOf(table)),
			Message: "column " + strconv.Itoa(i+1) + " is " + fmtPx(w) +
				" wide under the fixed table layout and its content needs " +
				fmtPx(demands[i].min) + "; the overflow is not drawn",
			Path:     PathOf(table.Element),
			Property: "table-layout",
		})
	}
}

// ---------------------------------------------------------------------------
// Intrinsic widths, for the boxes outside the table that have to size it
// ---------------------------------------------------------------------------

// tableContentWidths is a table box's intrinsic pair: the grid's own MIN and
// MAX, which is what a float or a shrink-to-fit ancestor asks for.
func (l *layouter) tableContentWidths(table *Box) intrinsicWidths {
	min, max := l.tableGridWidths(table)
	if length, ok := l.parseLength(table, "width"); ok && length.Kind == style.LengthAbsolute {
		// A declared width is what the table will be, floored by its minimum —
		// except under the fixed algorithm, which is a promise that the content
		// does not get a say.
		//
		// A *percentage* width is deliberately not read here: CSS Sizing makes a
		// percentage that cannot be resolved behave as auto for intrinsic
		// sizing, and resolving it against a containing block that is itself
		// being sized by this answer is circular.
		//
		w := length.Value
		if htmlTableWidth(table) {
			// On a <table> the declared number is the border box — see
			// htmlTableWidth — and what this returns is a content width.
			w = maxZero(w.Sub(l.tableEdges(table)))
		}
		if !tableLayoutIsFixed(table) {
			w = style.Max(w, min)
		}
		return intrinsicWidths{min: maxZero(w), max: maxZero(w)}
	}
	return intrinsicWidths{min: min, max: max}
}

// tableWrapperWidths is §17.4's wrapper measured: as wide as the table, and at
// least as wide as the narrowest a caption can be.
//
// A caption's *preferred* width is deliberately left out. A caption of running
// prose would otherwise stretch the table across the page to keep itself on one
// line, which is not what a caption is for — it wraps.
func (l *layouter) tableWrapperWidths(wrapper *Box) intrinsicWidths {
	var out intrinsicWidths
	for _, c := range wrapper.Children {
		if c.Inner == InnerTable {
			got := l.tableContentWidths(c)
			edges := l.borderWidths(c).Horizontal().
				Add(l.paddingOf(c, 0).Horizontal())
			out.min = style.Max(out.min, got.min.Add(edges))
			out.max = style.Max(out.max, got.max.Add(edges))
			continue
		}
		out.min = style.Max(out.min, l.outerWidths(c, 0).min)
	}
	if out.max < out.min {
		out.max = out.min
	}
	return out
}

// ---------------------------------------------------------------------------
// Laying the grid out
// ---------------------------------------------------------------------------

// placedCell is one cell laid out at its column width, before the row it is in
// has decided how tall it is.
type placedCell struct {
	cell *tableCell
	frag *Fragment
	// natural is the height the cell's content needed, which is what it would
	// have been given were it not in a row with anything else in it.
	natural style.Unit
	// baseline is where the cell's first line sits, measured from its border-box
	// top. §17.5.3 makes a cell with no line box baseline-align on its bottom
	// content edge, so there is always one.
	baseline style.Unit
	align    string
	// absFrom is where in the deferred queue this cell's out-of-flow boxes
	// begin, so that vertical alignment can move their static positions with the
	// content they were written in.
	absFrom int
}

// tableContent lays out a table's grid and returns the content height it needs.
//
// width is the table's used content width, already settled by resolveWidth
// through the same §17.5.2 algorithm the columns are distributed by, so the
// columns divide it exactly.
func (l *layouter) tableContent(table *Box, parent *Fragment, width style.Unit,
	origin flow) style.Unit {

	g := l.tableGridFor(table)
	if len(g.rows) == 0 {
		return 0
	}
	s := l.spacingOf(table)
	cols := l.columnWidths(table, width, s)

	// The left edge of each column, measured from the table's content box. The
	// grid starts one spacing in, and there is one between every pair — which is
	// what makes border-spacing show around the outside of a table as well as
	// between its cells. In the collapsing model the spacing is zero and the
	// columns abut: a grid line lies inside the two columns that share it.
	colX := make([]style.Unit, len(cols))
	x := s.h
	for i, w := range cols {
		colX[i] = x
		x = x.Add(w).Add(s.h)
	}

	placed := l.layoutCells(table, g, cols, s, width)
	rowH, rowBaseline := l.rowHeights(g, placed, s, origin)

	rowY := make([]style.Unit, len(g.rows))
	y := s.v
	for i, h := range rowH {
		rowY[i] = y
		y = y.Add(h).Add(s.v)
	}
	gridHeight := y.Sub(s.v).Sub(s.v)
	gridWidth := maxZero(width.Sub(s.h).Sub(s.h))

	l.paintableColumns(parent, g, cols, colX, s.v, gridHeight)
	l.assembleRows(parent, g, placed, cols, colX, rowY, rowH, rowBaseline, s, gridWidth)
	if s.collapsed != nil && len(s.collapsed.hoff) > 0 {
		// A table with rows and no columns at all has no grid lines to resolve
		// and none to draw; its own border was halved by the degenerate case in
		// buildCollapsedGrid and is painted the ordinary way.
		l.paintCollapsedGrid(table, parent, s.collapsed, cols, colX, rowH, rowY)
	}
	return y
}

// paintCollapsedGrid hands the table's fragment the grid lines to draw.
//
// The line positions are derived here rather than in bordercollapse.go because
// this is where the columns and rows first have sizes: which border wins is a
// question about styles and is answered before any of this, and where it goes is
// a question about geometry and cannot be.
func (l *layouter) paintCollapsedGrid(table *Box, parent *Fragment, cg *collapsedGrid,
	cols, colX, rowH, rowY []style.Unit) {

	// Where each grid line begins, relative to the table's *border* box.
	//
	// Column c's border box begins at the centre of line c — that is what "the
	// border is centred on the grid line" means — so the line begins its own
	// leading half before it. At the left edge that lands exactly on the table's
	// border box, since the table's used border is that same leading half.
	lineX := make([]style.Unit, cg.cols+1)
	originX := cg.table.Left
	for c := 0; c < cg.cols && c < len(colX); c++ {
		lineX[c] = colX[c].Add(originX).Sub(leadingHalf(cg.vgutter[c]))
	}
	if last := cg.cols - 1; last >= 0 && last < len(colX) {
		lineX[cg.cols] = colX[last].Add(cols[last]).Add(originX).
			Sub(leadingHalf(cg.vgutter[cg.cols]))
	}
	lineY := make([]style.Unit, cg.rows+1)
	originY := cg.table.Top
	for r := 0; r < cg.rows && r < len(rowY); r++ {
		lineY[r] = rowY[r].Add(originY).Sub(leadingHalf(cg.hgutter[r]))
	}
	if last := cg.rows - 1; last >= 0 && last < len(rowY) {
		lineY[cg.rows] = rowY[last].Add(rowH[last]).Add(originY).
			Sub(leadingHalf(cg.hgutter[cg.rows]))
	}

	parent.inCollapsedGrid = true
	parent.collapsed = cg.bands(lineX, lineY)
	if cg.truncated {
		l.rec.Report(RuleLimit, AtHTML(offsetOf(table)),
			"the table's collapsed borders break into more stretches of grid line "+
				"than this engine will draw; the rest of them are not on the page")
	}
}

// layoutCells lays every cell out at the width its columns give it.
//
// A cell is laid out through blockIn with its geometry forced, for the reason
// position.go gives for doing the same to an absolutely positioned box: margin
// collapsing, floats, line breaking and the height rules are the same inside a
// cell as anywhere else, and a second implementation of them would agree with
// this one on the day it was written and on no day after. What is forced is the
// width, because a cell's width comes from its column and not from its own
// declaration, and the margins, because "margin" does not apply to a cell.
func (l *layouter) layoutCells(table *Box, g *tableGrid, cols []style.Unit,
	s tableSpacing, tableWidth style.Unit) []placedCell {

	out := make([]placedCell, 0, len(g.cells))
	for _, c := range g.cells {
		// The cell's border box: its columns, and the spacing it swallows
		// between them. In the collapsing model that already holds the cell's
		// half of the grid line on each side, because a column there is exactly
		// a cell's border box and the columns abut.
		var outer style.Unit
		for k := c.col; k < c.col+c.colSpan && k < len(cols); k++ {
			outer = outer.Add(cols[k])
		}
		outer = outer.Add(s.h.Mul(float64(c.colSpan - 1)))

		// A percentage on a cell's padding resolves against the table's width,
		// because §10.1 makes the table the cell's containing block.
		border := l.borderWidths(c.box)
		padding := l.paddingOf(c.box, tableWidth)
		content := maxZero(outer.Sub(border.Horizontal()).Sub(padding.Horizontal()))

		absFrom := len(l.deferred)
		frag, _ := l.blockIn(c.box, tableWidth,
			flow{ctx: &floatContext{}, cbHeight: 0, cbDefinite: false},
			&forcedGeometry{width: content})
		frag.BorderRect.W = outer
		// The cell's half of each grid line is drawn by the table along with the
		// other half, which belongs to the neighbour: neither of them may draw it.
		frag.inCollapsedGrid = s.collapsed != nil

		out = append(out, placedCell{
			cell: c, frag: frag, natural: frag.BorderRect.H,
			baseline: baselineOfCell(frag),
			align:    strings.ToLower(strings.TrimSpace(c.box.Style["vertical-align"])),
			absFrom:  absFrom,
		})
	}
	return out
}

// baselineOfCell is §17.5.3's baseline of a cell: the baseline of its first line
// box, or its bottom content edge when it has none.
//
// The fallback is not a detail. Without it an empty cell in a row of text would
// have no opinion, and the first cell that did would decide the row's baseline
// on its own — which is right; but a cell holding only a block with no text
// would then align on nothing and sit at the top of a row it should have been
// pushed down in.
func baselineOfCell(f *Fragment) style.Unit {
	if v, ok := firstBaseline(f); ok {
		return v
	}
	return maxZero(f.BorderRect.H.Sub(f.Border.Bottom).Sub(f.Padding.Bottom))
}

// firstBaseline finds the baseline of the first line box in a subtree, measured
// from the border-box top of the fragment it was asked about.
func firstBaseline(f *Fragment) (style.Unit, bool) {
	top := f.Border.Top.Add(f.Padding.Top)
	if len(f.Lines) > 0 {
		return top.Add(f.Lines[0].Rect.Y).Add(f.Lines[0].Baseline), true
	}
	for _, c := range f.Children {
		if c.Box != nil && c.Box.outOfFlow() {
			// A float or a positioned box is not in the flow, so it is not what
			// the text beside the table lines up with.
			continue
		}
		if v, ok := firstBaseline(c); ok {
			return top.Add(c.BorderRect.Y).Add(v), true
		}
	}
	return 0, false
}

// rowHeights is §17.5.3.
//
// # The two things CSS 2.1 leaves open, and what is done about them
//
// A cell spanning several rows has a height requirement that belongs to no one
// row, and the specification says only that the rows must be tall enough
// between them. The shortfall goes to the *last* row of the span here, which is
// the arrangement that leaves a table looking like its markup: the rows above
// keep the heights their own content asked for, and the growth is at the bottom
// where the eye reads it as the spanning cell's own.
//
// A table taller than its rows is the other. The surplus is shared in proportion
// to the heights the rows already have, so a table given a height stretches
// evenly rather than putting all of it in one row.
func (l *layouter) rowHeights(g *tableGrid, placed []placedCell, s tableSpacing,
	origin flow) (rowH, rowBaseline []style.Unit) {

	rowH = make([]style.Unit, len(g.rows))
	rowBaseline = make([]style.Unit, len(g.rows))

	// The row's baseline is the lowest any of its baseline-aligned cells wants,
	// and it has to be known before any height is, since a cell pushed down to
	// meet it is that much taller.
	for _, p := range placed {
		if !isBaselineAligned(p.align) {
			continue
		}
		rowBaseline[p.cell.row] = style.Max(rowBaseline[p.cell.row], p.baseline)
	}

	for r, info := range g.rows {
		if v, ok := l.absoluteLengthOf(info.box, "height"); ok {
			// A declared row height is a minimum. A percentage is not read: it
			// would be a percentage of the table's height, which is what the
			// rows are in the middle of deciding.
			rowH[r] = style.Max(rowH[r], v)
		}
	}
	for _, p := range placed {
		if p.cell.rowSpan != 1 {
			continue
		}
		r := p.cell.row
		rowH[r] = style.Max(rowH[r], p.stretchedHeight(rowBaseline[r]))
	}
	// The spanning cells last, against rows that already hold everything else.
	for _, p := range placed {
		if p.cell.rowSpan == 1 {
			continue
		}
		r, n := p.cell.row, p.cell.rowSpan
		var have style.Unit
		for k := r; k < r+n; k++ {
			have = have.Add(rowH[k])
		}
		have = have.Add(s.v.Mul(float64(n - 1)))
		if want := p.stretchedHeight(rowBaseline[r]); want > have {
			rowH[r+n-1] = rowH[r+n-1].Add(want.Sub(have))
		}
	}

	if origin.cbDefinite {
		var total style.Unit
		for _, h := range rowH {
			total = total.Add(h)
		}
		total = total.Add(s.v.Mul(float64(len(rowH) + 1)))
		if origin.cbHeight > total {
			weights := make([]float64, len(rowH))
			for i, h := range rowH {
				weights[i] = float64(h)
			}
			distribute(origin.cbHeight.Sub(total), weights, rowH)
		}
	}
	return rowH, rowBaseline
}

// stretchedHeight is how tall a cell is once it has been moved to sit on its
// row's baseline.
func (p placedCell) stretchedHeight(rowBaseline style.Unit) style.Unit {
	if !isBaselineAligned(p.align) {
		return p.natural
	}
	return p.natural.Add(maxZero(rowBaseline.Sub(p.baseline)))
}

// isBaselineAligned reports whether a cell aligns on the row's baseline.
//
// §17.5.3 gives a cell four alignments and treats everything else as baseline —
// "vertical-align: super" on a cell is not a small lift, it is nothing at all.
func isBaselineAligned(align string) bool {
	switch align {
	case "top", "bottom", "middle":
		return false
	}
	return true
}

// absoluteLengthOf resolves a property that is only meaningful as a real length,
// which is what a row's height is while the table is still deciding its own.
func (l *layouter) absoluteLengthOf(b *Box, property string) (style.Unit, bool) {
	length, ok := l.parseLength(b, property)
	if !ok || length.Kind != style.LengthAbsolute {
		return 0, false
	}
	return length.Value, true
}

// paintableColumns emits the fragments the column and column-group backgrounds
// are painted on.
//
// They come first among the table's children because that is painting order:
// §17.5.1 stacks the table, then the column groups, then the columns, then the
// row groups, rows and cells, and the painter walks a fragment's children in
// order. Emitting them here rather than teaching the painter about tables is
// what keeps table painting from being a second traversal.
func (l *layouter) paintableColumns(parent *Fragment, g *tableGrid,
	cols, colX []style.Unit, top, height style.Unit) {

	if height <= 0 {
		return
	}
	span := func(box *Box, first, count int) *Fragment {
		if first >= len(cols) {
			return nil
		}
		last := first + count - 1
		if last >= len(cols) {
			last = len(cols) - 1
		}
		return &Fragment{
			Box: box,
			BorderRect: Rect{
				X: colX[first], Y: top,
				W: colX[last].Add(cols[last]).Sub(colX[first]),
				H: height,
			},
		}
	}

	grouped := make([]bool, len(cols))
	for _, cg := range g.colGroups {
		frag := span(cg.box, cg.first, cg.count)
		if frag == nil {
			continue
		}
		for i := cg.first; i < cg.first+cg.count && i < len(cols); i++ {
			grouped[i] = true
			if g.colBoxes[i] == nil {
				continue
			}
			if i > cg.first && g.colBoxes[i] == g.colBoxes[i-1] {
				// One <col span=3> is one box describing three columns, and one
				// background across all of them.
				last := frag.Children[len(frag.Children)-1]
				last.BorderRect.W = colX[i].Add(cols[i]).Sub(frag.BorderRect.X).Sub(last.BorderRect.X)
				continue
			}
			frag.Children = append(frag.Children, &Fragment{
				Box: g.colBoxes[i],
				BorderRect: Rect{
					X: colX[i].Sub(frag.BorderRect.X), Y: 0,
					W: cols[i], H: height,
				},
			})
		}
		parent.Children = append(parent.Children, frag)
	}
	for i, col := range g.colBoxes {
		if col == nil || grouped[i] {
			continue
		}
		if i > 0 && g.colBoxes[i-1] == col {
			last := parent.Children[len(parent.Children)-1]
			last.BorderRect.W = colX[i].Add(cols[i]).Sub(last.BorderRect.X)
			continue
		}
		parent.Children = append(parent.Children, &Fragment{
			Box:        col,
			BorderRect: Rect{X: colX[i], Y: top, W: cols[i], H: height},
		})
	}
}

// assembleRows builds the row-group, row and cell fragments and gives each cell
// the height its row settled on.
func (l *layouter) assembleRows(parent *Fragment, g *tableGrid, placed []placedCell,
	cols, colX, rowY, rowH, rowBaseline []style.Unit, s tableSpacing, gridWidth style.Unit) {

	rowFrags := make([]*Fragment, len(g.rows))
	groupFrags := make([]*Fragment, len(g.rowGroups))
	for i, rg := range g.rowGroups {
		if rg.count == 0 {
			continue
		}
		// A row group's fragment is made here rather than by block layout, so the
		// two value checks every other box gets in blockIn are asked here instead.
		l.checkVisibility(rg.box)
		l.checkTableBoxSizing(rg.box)
		last := rg.first + rg.count - 1
		groupFrags[i] = &Fragment{
			Box: rg.box,
			BorderRect: Rect{
				X: s.h, Y: rowY[rg.first], W: gridWidth,
				H: rowY[last].Add(rowH[last]).Sub(rowY[rg.first]),
			},
		}
		parent.Children = append(parent.Children, groupFrags[i])
	}
	for r, info := range g.rows {
		l.checkVisibility(info.box)
		l.checkTableBoxSizing(info.box)
		frag := &Fragment{
			Box:        info.box,
			BorderRect: Rect{X: s.h, Y: rowY[r], W: gridWidth, H: rowH[r]},
		}
		if info.group >= 0 && groupFrags[info.group] != nil {
			group := groupFrags[info.group]
			frag.BorderRect.X = 0
			frag.BorderRect.Y = rowY[r].Sub(group.BorderRect.Y)
			group.Children = append(group.Children, frag)
		} else {
			parent.Children = append(parent.Children, frag)
		}
		rowFrags[r] = frag
	}

	for _, p := range placed {
		c := p.cell
		row := rowFrags[c.row]

		var height style.Unit
		for k := c.row; k < c.row+c.rowSpan; k++ {
			height = height.Add(rowH[k])
		}
		height = height.Add(s.v.Mul(float64(c.rowSpan - 1)))

		l.alignCell(p, height, rowBaseline[c.row])
		p.frag.BorderRect = Rect{
			X: colX[c.col].Sub(s.h), Y: 0,
			W: p.frag.BorderRect.W, H: height,
		}
		if s.collapsed == nil && cellIsEmpty(p.frag) && strings.EqualFold(
			strings.TrimSpace(c.box.Style["empty-cells"]), "hide") {
			// §17.6.1.1: an empty cell in the *separated* model may be asked to
			// draw nothing at all. Leaving the fragment out is exactly that —
			// there is nothing in it but a background and a border, and its
			// width and height have already been counted.
			//
			// The property does not apply in the collapsing model and the
			// specification says so in as many words. It could not: an empty
			// cell there owns half of four grid lines that its neighbours own
			// the other half of, so hiding it would take away half of a border
			// that belongs to the cell next door.
			continue
		}
		row.Children = append(row.Children, p.frag)
	}
}

// cellIsEmpty reports whether a cell has anything in it to draw around.
func cellIsEmpty(f *Fragment) bool { return len(f.Lines) == 0 && len(f.Children) == 0 }

// alignCell applies §17.5.3's vertical-align by moving the cell's content within
// the height its row gave it.
//
// The content moves rather than the box: a cell's background and border fill the
// row whatever the alignment, and only what is inside slides. The deferred
// out-of-flow boxes move with it, because their static position is where they
// were written *among that content* — a box that stayed behind would be
// positioned against a place the text has left.
func (l *layouter) alignCell(p placedCell, height, rowBaseline style.Unit) {
	slack := maxZero(height.Sub(p.natural))
	var delta style.Unit
	switch p.align {
	case "top":
	case "bottom":
		delta = slack
	case "middle":
		delta = slack.Div(2)
	default:
		delta = style.Min(maxZero(rowBaseline.Sub(p.baseline)), slack)
	}
	if delta == 0 {
		return
	}
	for _, c := range p.frag.Children {
		c.BorderRect.Y = c.BorderRect.Y.Add(delta)
	}
	for i := range p.frag.Lines {
		p.frag.Lines[i].Rect.Y = p.frag.Lines[i].Rect.Y.Add(delta)
	}
	for i := p.absFrom; i < len(l.deferred); i++ {
		if l.deferred[i].parent == p.frag {
			l.deferred[i].staticY = l.deferred[i].staticY.Add(delta)
		}
	}
}
