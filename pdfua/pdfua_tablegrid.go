package pdfua

import (
	"strings"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// Table-grid analysis for PDF/UA structure validation. A table's TR/TH/TD
// elements, together with their RowSpan/ColSpan table attributes, are laid out
// on a grid exactly as a renderer would. Definite structural defects — a span
// that extends past the last row, cells that overlap, or rows that do not all
// span the same number of columns (a hole) — are reported (7.2). Only
// unambiguous defects are flagged so a well-formed table never raises a false
// positive. A table laid out without defects is then asked whether each data
// cell's headers can be determined (7.5, tableHeaderDefects).

// The number of grid slots gridDefects will fill for one table defaults to
// core.DefaultMaxTableGridFills; a caller can change it with WithMaxTableGridFills.
// It caps the work a pathological table (huge RowSpan/ColSpan values) can force.
// It counts actually-filled slots, so a large but sparse table is unaffected;
// the largest real tables observed fill well under a million.

// tableCell is one TH/TD with its resolved span, and, once laid out, the grid
// slot its top-left corner is in.
type tableCell struct {
	rowSpan int
	colSpan int
	elem    *object.Dictionary // the cell's element (nil in grid-only tests)
	th      bool               // a TH; otherwise a TD
	row     int                // set by gridLayout
	col     int                // set by gridLayout
	// attrs are the cell's attribute objects owned by Table: those in its
	// /A, then those its /C names in the class map (ownedAttrs).
	attrs []*object.Dictionary
}

// tableRow is the ordered cells of one TR.
type tableRow []tableCell

// checkUATableGrid lays out every Table's cells and reports grid defects (7.2)
// and, in a regular table, data cells whose headers cannot be determined
// (7.5; see tableHeaderDefects).
func checkUATableGrid(d core.View, cat *object.Dictionary, part string) []Violation {
	root := d.ResolveDict(cat.Get("StructTreeRoot"))
	if root == nil {
		return nil
	}
	roleMap := d.ResolveDict(root.Get("RoleMap"))
	classMap := d.ResolveDict(root.Get("ClassMap"))
	var v []Violation
	// Tables whose /K is the same array have the same rows, and so the same
	// defects: they are laid out once (audit 2026-09-22 C18, the table form).
	// The slots each layout fills are charged to the run's work meter, so many
	// distinct tables each at the per-table bound still cost the run what
	// they cost; TableGridFills stays as the bound on one table's grid, which
	// is memory.
	type layout struct {
		defects  []Violation
		complete bool
	}
	byK := map[int]layout{}
	for _, n := range structTree(d, cat) {
		if n.StdType != "Table" {
			continue
		}
		kref, shared := n.Elem.Get("K").(object.IndirectRef)
		if l, ok := byK[kref.Number]; ok && shared {
			v = append(v, l.defects...)
			continue
		}
		var l layout
		l.complete = true
		if rows, groups := collectTableRows(d, n.Elem, roleMap, classMap); len(rows) > 0 {
			maxFills := d.Limits.TableGridFills
			lay := layoutTable(rows, maxFills)
			l.defects, l.complete = lay.defects, lay.complete
			d.Charge(int(min(lay.fills, maxFills)))
			if !l.complete {
				d.Note(core.GuardGridFills, "a table's RowSpan/ColSpan values imply more than "+core.LimitBound(maxFills, core.DefaultMaxTableGridFills)+" grid slots; that table was not laid out, so none of its grid rules ran", d.ObjNumOf(n.Elem))
			} else if part == "2" {
				l.defects = append(l.defects, rowGroupDefects(lay, groups)...)
			}
			if l.complete && len(l.defects) == 0 {
				l.defects = tableHeaderDefects(d, lay, part)
			}
			v = append(v, l.defects...)
		}
		if shared {
			byK[kref.Number] = l
		}
	}
	return v
}

// collectTableRows returns the table's rows in document order, descending
// through THead/TBody/TFoot row groups (but not into nested tables), and the
// row groups' boundaries: for each group, the index of its first row and the
// index after its last.
//
// The walk is iterative (kids pushed in reverse, so rows come out in document
// order): a table's structure is as deep as the file says (audit 2026-09-22
// C84). Each node visited is charged to the run's work meter.
func collectTableRows(d core.View, table *object.Dictionary, roleMap, classMap *object.Dictionary) ([]tableRow, []int) {
	var rows []tableRow
	var groups []int
	type item struct {
		node     object.Object
		top      bool
		groupEnd bool // marks where a row group's kids end
	}
	seen := map[int]bool{}
	stack := []item{{node: table, top: true}}
	push := func(kids []object.Object) {
		for i := len(kids) - 1; i >= 0; i-- {
			stack = append(stack, item{node: kids[i]})
		}
	}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		d.Charge(1)
		if it.groupEnd {
			groups = append(groups, len(rows))
			continue
		}
		node, top := it.node, it.top
		if ref, ok := node.(object.IndirectRef); ok {
			if seen[ref.Number] {
				continue
			}
			seen[ref.Number] = true
		}
		elem := d.ResolveDict(node)
		if elem == nil {
			if arr, ok := d.Resolve(node).(object.Array); ok {
				for i := len(arr) - 1; i >= 0; i-- {
					stack = append(stack, item{node: arr[i], top: top})
				}
			}
			continue
		}
		t := standardStructType(d, elem, roleMap)
		if !top && t == "Table" {
			continue // nested table handled separately
		}
		if t == "TR" {
			rows = append(rows, collectRowCells(d, elem, roleMap, classMap))
			continue
		}
		if t == "THead" || t == "TBody" || t == "TFoot" {
			groups = append(groups, len(rows))
			stack = append(stack, item{groupEnd: true})
		}
		push(structKids(d, elem))
	}
	return rows, groups
}

// rowGroupDefects is ISO 14289-2 8.2.5.26's "Row groupings formed by THead,
// TBody and TFoot structure elements shall be regular": no cell's RowSpan
// carries it across a group's first or last row into the next (veraPDF
// 8.2.5.26-t02, GFSETable.checkRegular, PDF/UA-2 only).
func rowGroupDefects(l tableLayout, groups []int) []Violation {
	for _, c := range l.cells {
		for _, b := range groups {
			if c.row < b && c.rowSpan > b-c.row {
				return []Violation{{Clause: "7.2", Message: "a table cell's RowSpan crosses the boundary of a THead, TBody or TFoot row group", Object: 0}}
			}
		}
	}
	return nil
}

// collectRowCells returns the TH/TD cells of a TR with their spans.
func collectRowCells(d core.View, tr *object.Dictionary, roleMap, classMap *object.Dictionary) tableRow {
	var cells tableRow
	for _, kid := range structKids(d, tr) {
		d.Charge(1)
		c := d.ResolveDict(kid)
		if c == nil {
			continue
		}
		t := standardStructType(d, c, roleMap)
		if t != "TH" && t != "TD" {
			continue
		}
		attrs := ownedAttrs(d, c, classMap, "Table")
		cells = append(cells, tableCell{rowSpan: cellSpan(d, attrs, "RowSpan"), colSpan: cellSpan(d, attrs, "ColSpan"), elem: c, th: t == "TH", attrs: attrs})
	}
	return cells
}

// cellSpan reads a RowSpan/ColSpan value from a cell's Table attributes,
// defaulting to 1. A non-positive value is treated as 1.
func cellSpan(d core.View, attrs []*object.Dictionary, key object.Name) int {
	for _, ad := range attrs {
		if n, ok := d.Resolve(ad.Get(key)).(object.Integer); ok {
			if n < 1 {
				return 1
			}
			return int(n)
		}
	}
	return 1
}

// gridDefects lays the rows onto a grid and reports definite defects. maxFills
// bounds the number of slots it will fill; see WithMaxTableGridFills. The
// second result reports that maxFills stopped the layout, so "no defects" means
// "not determined" rather than "clean".
func gridDefects(rows []tableRow, maxFills int64) ([]Violation, bool) {
	v, complete, _ := gridLayout(rows, maxFills)
	return v, complete
}

// gridLayout is gridDefects, also reporting how many slots it filled: the
// work it did, for the caller to charge.
func gridLayout(rows []tableRow, maxFills int64) ([]Violation, bool, int64) {
	l := layoutTable(rows, maxFills)
	return l.defects, l.complete, l.fills
}

// tableLayout is a table laid out on its grid.
type tableLayout struct {
	rows     []tableRow // the cells, each with its row and col set
	defects  []Violation
	complete bool  // false: maxFills stopped the layout, and nothing else is set
	fills    int64 // the work done
	width    int
	// occupied[r][c] is 1 + the index in cells of the cell covering slot
	// (r, c), or absent when nothing does.
	occupied []map[int]int32
	cells    []*tableCell
	emptyRow bool // some row holds no cell of its own
}

// cellAt is the cell covering slot (r, c), or nil.
func (l *tableLayout) cellAt(r, c int) *tableCell {
	if i := l.occupied[r][c]; i > 0 {
		return l.cells[i-1]
	}
	return nil
}

func layoutTable(rows []tableRow, maxFills int64) tableLayout {
	nRows := len(rows)
	// occupied[r] maps each column already filled in row r (by a cell in
	// this or an earlier row via a row span) to the cell filling it.
	occupied := make([]map[int]int32, nRows)
	for i := range occupied {
		occupied[i] = map[int]int32{}
	}
	var cells []*tableCell
	rowWidth := make([]int, nRows)
	overlap := false
	outOfRows := false
	emptyRow := false

	// Total grid slots filled so far. Bound it: a table whose spans imply more
	// than maxFills occupied slots — e.g. a single cell with a multi-million
	// ColSpan, or many such cells — is pathological and is not laid out, so a
	// small degenerate table cannot force billions of map writes. This counts
	// actually-filled slots (not the nominal rows×cols area), so a genuinely
	// large but sparse table (tens of thousands of rows/cols, few real cells)
	// is unaffected; real tables fill far below the limit.
	var fills int64
	oversize := false
	for r := 0; r < nRows && !oversize; r++ {
		col := 0
		if len(rows[r]) == 0 {
			emptyRow = true
		}
		for i := range rows[r] {
			cell := &rows[r][i]
			// Skip columns already taken by a row span from above.
			for occupied[r][col] != 0 {
				col++
			}
			// Overflow-safe "RowSpan extends beyond the last row" test.
			if cell.rowSpan > nRows-r {
				outOfRows = true
			}
			er := cell.rowSpan
			if er > nRows-r {
				er = nRows - r
			}
			cs := cell.colSpan
			// Would filling this cell (er×cs slots) exceed the budget? The
			// division keeps the comparison from overflowing on a huge span.
			if er > 0 && cs > 0 && int64(er) > (maxFills-fills)/int64(cs) {
				oversize = true
				break
			}
			cell.row, cell.col = r, col
			cells = append(cells, cell)
			id := int32(len(cells))
			for dr := 0; dr < er; dr++ {
				for dc := 0; dc < cs; dc++ {
					c := col + dc
					if occupied[r+dr][c] != 0 {
						overlap = true
					}
					occupied[r+dr][c] = id
					fills++
				}
			}
			col += cs
		}
		rowWidth[r] = col
	}
	if oversize {
		// Too large to lay out within the work budget; report no grid defects
		// rather than hang or fabricate a result on adversarial input. The
		// defects already found (outOfRows, overlap) are discarded with the
		// rest: they were computed against a grid this function abandoned, and
		// half a layout is not evidence. The caller reports the trip instead.
		return tableLayout{complete: false, fills: fills}
	}

	// A hole exists if, after placement, some row does not fill the full grid
	// width (the maximum column reached by any row). Every occupied[r] key is a
	// distinct filled column in [0,width), so a row has a hole exactly when it
	// has fewer than width filled columns — an O(rows) test rather than scanning
	// the whole rows×width grid, which for a sparse-but-huge table (tens of
	// thousands of rows and columns) would be billions of lookups.
	width := 0
	for _, w := range rowWidth {
		if w > width {
			width = w
		}
	}
	hole := false
	for r := 0; r < nRows; r++ {
		if len(occupied[r]) < width {
			hole = true
			break
		}
	}

	var v []Violation
	if outOfRows {
		v = append(v, Violation{Clause: "7.2", Message: "a table cell's RowSpan extends beyond the last row of the table", Object: 0})
	}
	if overlap {
		v = append(v, Violation{Clause: "7.2", Message: "table cells overlap on the grid (inconsistent RowSpan/ColSpan)", Object: 0})
	}
	if hole {
		v = append(v, Violation{Clause: "7.2", Message: "table rows do not all span the same number of columns (a grid cell is empty)", Object: 0})
	}
	return tableLayout{rows: rows, defects: v, complete: true, fills: fills + int64(nRows), width: width, occupied: occupied, cells: cells, emptyRow: emptyRow}
}

// tableHeaderDefects is veraPDF's table-header rule (PDF/UA-1 7.5-t01/t02,
// PDF/UA-2 8.2.5.26-t05/t06; GFSETable.checkTable), over a table laid out
// without defects. When every TH has a Scope the table is determinable.
// Otherwise every TD — but one at the top-left slot — must name its headers
// in a /Headers table attribute, each the /ID of a TH in the table; for
// PDF/UA-2 a TD that does not may instead find a header by position
// (positionalHeaders). A TD that does neither is reported. The parts differ
// as veraPDF 1.30.3 has them: it dropped the positional search for PDF/UA-1
// in 2026 (veraPDF-validation 6998f6ec), and the corpus agrees either way.
//
// The rule is about the data cells. The rule it replaces asked every TH for a
// Scope or an /ID, which a conforming table need not give a header no data
// cell refers to (corpus PDF_UA-1 7.5-t01-pass-e, PDF_UA-2
// 8.2.5.26-t05-pass-e).
//
// veraPDF exempts the data cell at the top-left slot: the stub head, where the
// header row and the header column cross, which no header pertains to. Its
// rule knows only that position; pdf0 exempts a stub head wherever the
// crossing is (stubHeads). The PDF Association's conformant
// PDFUA-Reference-08 has two, blank cells at the start of a column-header row
// in the middle of a table whose first column is all row headers, and
// veraPDF 1.30.3 reports both (7.5-1).
func tableHeaderDefects(d core.View, l tableLayout, part string) []Violation {
	if l.emptyRow {
		return nil // veraPDF reads a row with no cell of its own as irregular
	}
	allScoped := true
	ids := map[string]bool{}
	for _, c := range l.cells {
		if !c.th || c.elem == nil {
			continue
		}
		if _, ok := cellScope(d, c); !ok {
			allScoped = false
		}
		if s, r := d.StringValue(c.elem.Get("ID")); r == core.ReasonOK && len(s.Value) > 0 {
			ids[string(s.Value)] = true
		}
	}
	if allScoped {
		return nil
	}
	var byPosition []bool
	if part == "2" {
		byPosition = positionalHeaders(d, l)
	}
	stub := stubHeads(l)
	var v []Violation
	for i, c := range l.cells {
		d.Charge(1)
		if c.th || c.elem == nil || (c.row == 0 && c.col == 0) || stub[i] {
			continue
		}
		var unknown []string
		named := false
		for _, h := range tableAttrArray(d, c, "Headers") {
			s, r := d.StringValue(h)
			if r != core.ReasonOK {
				continue
			}
			named = true
			if !ids[string(s.Value)] {
				unknown = append(unknown, core.DecodePDFTextString(s.Value))
			}
		}
		if (named && len(unknown) == 0) || (byPosition != nil && byPosition[i]) {
			continue
		}
		msg := "table data cell (TD) has no /Headers and not every TH has a Scope, so its headers cannot be determined"
		if len(unknown) > 0 {
			msg = "table data cell (TD) names header IDs no TH carries (" + strings.Join(unknown, ", ") + ") and not every TH has a Scope, so its headers cannot be determined"
		}
		v = append(v, Violation{Clause: "7.5", Message: msg, Object: d.ObjNumOf(c.elem)})
	}
	return v
}

// positionalHeaders is veraPDF's algorithmic header lookup
// (GFSETable.hasHeaders): for each cell, whether it is a TD with a header above
// it in one of its columns or to its left in one of its rows. Going up a
// column (left along a row), a TH whose Scope is Column or Both (Row or Both)
// is a header; a TH that is not is passed over, but the first non-TH after a
// run of THs ends the search. A TH without a Scope takes one from its slot:
// Both at the top-left, Column in the first row, Row in the first column, and
// Both elsewhere (GFSETH.getDefaultScope).
//
// Each column and row is scanned once, carrying the scan's state from one
// slot to the next, so the cost is the number of slots — which the layout
// already paid for — rather than slots × table height.
func positionalHeaders(d core.View, l tableLayout) []bool {
	out := make([]bool, len(l.cells))
	nRows := len(l.occupied)
	scopeAt := func(c *tableCell, r, col int) string {
		if s, ok := cellScope(d, c); ok {
			return s // as written: an empty name or a misspelling is a header for nothing
		}
		switch {
		case r == 0 && col == 0:
			return "Both"
		case r == 0:
			return "Column"
		case col == 0:
			return "Row"
		}
		return "Both"
	}
	// scan walks one line of n slots; at(i) is the cell in slot i; it marks a
	// TD whose leading edge is at slot i when the walk back from slot i-1
	// finds a header. found0/found1 are that walk's result from the slot
	// before, starting without and with a header already passed.
	scan := func(n int, at func(int) (*tableCell, int, int), want string, leading func(*tableCell, int) bool) {
		found0, found1 := false, false
		for i := 0; i < n; i++ {
			c, r, col := at(i)
			if c != nil && !c.th && leading(c, i) && found0 {
				out[cellIndex(l, c)] = true
			}
			if c == nil {
				found0, found1 = false, false
				continue
			}
			qual := c.th && c.elem != nil && (scopeAt(c, r, col) == "Both" || scopeAt(c, r, col) == want)
			switch {
			case qual:
				found0, found1 = true, true
			case c.th:
				found0 = found1
			default:
				found1 = false
				// found0 unchanged: a non-TH before any TH is passed over.
			}
		}
	}
	for col := 0; col < l.width; col++ {
		d.Charge(nRows)
		scan(nRows, func(r int) (*tableCell, int, int) { return l.cellAt(r, col), r, col }, "Column",
			func(c *tableCell, r int) bool { return c.row == r })
	}
	for r := 0; r < nRows; r++ {
		d.Charge(l.width)
		scan(l.width, func(col int) (*tableCell, int, int) { return l.cellAt(r, col), r, col }, "Row",
			func(c *tableCell, col int) bool { return c.col == col })
	}
	return out
}

// stubHeads marks, for each cell, whether it is a TD every other slot of whose
// rows and columns is a TH: the cell where a header row crosses a header
// column. The non-TH slots of each row and column are counted once, so the
// cost is the number of slots.
func stubHeads(l tableLayout) []bool {
	out := make([]bool, len(l.cells))
	nRows := len(l.occupied)
	rowOther := make([]int, nRows)   // non-TH slots in each row
	colOther := make([]int, l.width) // non-TH slots in each column
	for r := 0; r < nRows; r++ {
		for c := 0; c < l.width; c++ {
			if cell := l.cellAt(r, c); cell == nil || !cell.th {
				rowOther[r]++
				colOther[c]++
			}
		}
	}
	for i, c := range l.cells {
		if c.th {
			continue
		}
		er, ec := min(c.row+c.rowSpan, nRows), min(c.col+c.colSpan, l.width)
		ok := true
		for r := c.row; r < er && ok; r++ {
			ok = rowOther[r] == ec-c.col // the cell's own slots, and no other
		}
		for col := c.col; col < ec && ok; col++ {
			ok = colOther[col] == er-c.row
		}
		out[i] = ok
	}
	return out
}

// cellIndex is c's index in l.cells.
func cellIndex(l tableLayout, c *tableCell) int {
	return int(l.occupied[c.row][c.col]) - 1
}

// cellScope is a cell's Scope table attribute and whether it has one. The
// value is as written: a Scope of the empty name is present, and says
// neither Row, Column nor Both (corpus PDF_UA-2 8.2.5.26-t05-fail-a, whose
// header cells carry /Scope / and fail).
func cellScope(d core.View, cell *tableCell) (string, bool) {
	return firstAttrName(d, cell.attrs, "Scope")
}

// tableAttrArray is the first array a cell's Table attributes give for key.
func tableAttrArray(d core.View, cell *tableCell, key object.Name) object.Array {
	for _, ad := range cell.attrs {
		if a, ok := d.Resolve(ad.Get(key)).(object.Array); ok {
			return a
		}
	}
	return nil
}
