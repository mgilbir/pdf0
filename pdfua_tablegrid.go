package pdf0

// Table-grid analysis for PDF/UA structure validation. A table's TR/TH/TD
// elements, together with their RowSpan/ColSpan table attributes, are laid out
// on a grid exactly as a renderer would. Definite structural defects — a span
// that extends past the last row, cells that overlap, or rows that do not all
// span the same number of columns (a hole) — are reported. Only unambiguous
// defects are flagged so a well-formed table never raises a false positive.

// maxGridFills bounds the number of grid slots gridDefects will fill for one
// table. It caps the work a pathological table (huge RowSpan/ColSpan values)
// can force. It counts actually-filled slots, so a large but sparse table is
// unaffected; the largest real tables observed fill well under a million.
const maxGridFills = 1 << 24 // 16,777,216

// tableCell is one TH/TD with its resolved span.
type tableCell struct {
	rowSpan int
	colSpan int
}

// tableRow is the ordered cells of one TR.
type tableRow []tableCell

// checkUATableTHScope enforces the per-cell form of 7.5: every TH header cell
// must be individually identifiable, carrying either a Scope attribute or an
// /ID (so data cells can associate with it). A TH with neither leaves part of
// the header structure undeterminable. Diffing the veraPDF fail files against
// their pass siblings showed that Scope and /ID are interchangeable here (some
// pass files give every TH a Scope, others give every TH an /ID), which is why
// an /ID exempts a TH that has no Scope — the boundary an earlier Scope-only
// rule got wrong.
func (d *Document) checkUATableTHScope(cat *Dictionary) []UAViolation {
	var v []UAViolation
	reported := map[int]bool{}
	d.walkStructElems(cat, func(el *Dictionary, t Name) {
		if t != "TH" {
			return
		}
		if d.cellHasScope(el) || el.Get("ID") != nil {
			return
		}
		num := d.dictObjNum(el)
		if !reported[num] {
			reported[num] = true
			v = append(v, UAViolation{"7.5", "table header cell (TH) has neither a Scope attribute nor an /ID", num})
		}
	})
	return v
}

// cellHasScope reports whether a cell carries a /Scope in a Table attribute.
func (d *Document) cellHasScope(cell *Dictionary) bool {
	for _, ad := range d.tableAttrDicts(cell) {
		if ad.Get("Scope") != nil {
			return true
		}
	}
	return false
}

// checkUATableGrid lays out every Table's cells and reports grid defects (7.2).
func (d *Document) checkUATableGrid(cat *Dictionary) []UAViolation {
	root := d.ResolveDict(cat.Get("StructTreeRoot"))
	if root == nil {
		return nil
	}
	roleMap := d.ResolveDict(root.Get("RoleMap"))
	var v []UAViolation
	seen := map[int]bool{}
	var walk func(node Object)
	walk = func(node Object) {
		if ref, ok := node.(IndirectRef); ok {
			if seen[ref.Number] {
				return
			}
			seen[ref.Number] = true
		}
		elem := d.ResolveDict(node)
		if elem == nil {
			if arr, ok := d.Resolve(node).(Array); ok {
				for _, kid := range arr {
					walk(kid)
				}
			}
			return
		}
		if d.standardStructType(elem, roleMap) == "Table" {
			if rows := d.collectTableRows(elem, roleMap); len(rows) > 0 {
				v = append(v, gridDefects(rows)...)
			}
		}
		for _, kid := range d.structKids(elem) {
			walk(kid)
		}
	}
	walk(root.Get("K"))
	return v
}

// collectTableRows returns the table's rows in document order, descending
// through THead/TBody/TFoot row groups (but not into nested tables).
func (d *Document) collectTableRows(table *Dictionary, roleMap *Dictionary) []tableRow {
	var rows []tableRow
	var visit func(node Object, top bool)
	seen := map[int]bool{}
	visit = func(node Object, top bool) {
		if ref, ok := node.(IndirectRef); ok {
			if seen[ref.Number] {
				return
			}
			seen[ref.Number] = true
		}
		elem := d.ResolveDict(node)
		if elem == nil {
			if arr, ok := d.Resolve(node).(Array); ok {
				for _, kid := range arr {
					visit(kid, top)
				}
			}
			return
		}
		t := d.standardStructType(elem, roleMap)
		if !top && t == "Table" {
			return // nested table handled separately
		}
		switch t {
		case "TR":
			rows = append(rows, d.collectRowCells(elem, roleMap))
			return
		case "THead", "TBody", "TFoot":
			for _, kid := range d.structKids(elem) {
				visit(kid, false)
			}
			return
		}
		for _, kid := range d.structKids(elem) {
			visit(kid, false)
		}
	}
	visit(table, true)
	return rows
}

// collectRowCells returns the TH/TD cells of a TR with their spans.
func (d *Document) collectRowCells(tr *Dictionary, roleMap *Dictionary) tableRow {
	var cells tableRow
	for _, kid := range d.structKids(tr) {
		c := d.ResolveDict(kid)
		if c == nil {
			continue
		}
		t := d.standardStructType(c, roleMap)
		if t != "TH" && t != "TD" {
			continue
		}
		cells = append(cells, tableCell{rowSpan: d.cellSpan(c, "RowSpan"), colSpan: d.cellSpan(c, "ColSpan")})
	}
	return cells
}

// cellSpan reads a RowSpan/ColSpan value from a cell's /A table attribute,
// defaulting to 1. A non-positive value is treated as 1.
func (d *Document) cellSpan(cell *Dictionary, key Name) int {
	for _, ad := range d.tableAttrDicts(cell) {
		if n, ok := d.Resolve(ad.Get(key)).(Integer); ok {
			if n < 1 {
				return 1
			}
			return int(n)
		}
	}
	return 1
}

// tableAttrDicts returns a cell's attribute dictionaries whose owner (/O) is
// Table.
func (d *Document) tableAttrDicts(cell *Dictionary) []*Dictionary {
	var out []*Dictionary
	switch a := d.Resolve(cell.Get("A")).(type) {
	case *Dictionary:
		if o, _ := a.Get("O").(Name); o == "Table" {
			out = append(out, a)
		}
	case Array:
		for _, e := range a {
			if ad := d.ResolveDict(e); ad != nil {
				if o, _ := ad.Get("O").(Name); o == "Table" {
					out = append(out, ad)
				}
			}
		}
	}
	return out
}

// gridDefects lays the rows onto a grid and reports definite defects.
func gridDefects(rows []tableRow) []UAViolation {
	nRows := len(rows)
	// occupied[r] is the set of columns already filled in row r (by a cell in
	// this or an earlier row via a row span).
	occupied := make([]map[int]bool, nRows)
	for i := range occupied {
		occupied[i] = map[int]bool{}
	}
	rowWidth := make([]int, nRows)
	overlap := false
	outOfRows := false

	// Total grid slots filled so far. Bound it: a table whose spans imply more
	// than maxGridFills occupied slots — e.g. a single cell with a multi-million
	// ColSpan, or many such cells — is pathological and is not laid out, so a
	// small degenerate table cannot force billions of map writes. This counts
	// actually-filled slots (not the nominal rows×cols area), so a genuinely
	// large but sparse table (tens of thousands of rows/cols, few real cells)
	// is unaffected; real tables fill far below the limit.
	var fills int64
	oversize := false
	for r := 0; r < nRows && !oversize; r++ {
		col := 0
		for _, cell := range rows[r] {
			// Skip columns already taken by a row span from above.
			for occupied[r][col] {
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
			if er > 0 && cs > 0 && int64(er) > (maxGridFills-fills)/int64(cs) {
				oversize = true
				break
			}
			for dr := 0; dr < er; dr++ {
				for dc := 0; dc < cs; dc++ {
					c := col + dc
					if occupied[r+dr][c] {
						overlap = true
					}
					occupied[r+dr][c] = true
					fills++
				}
			}
			col += cs
		}
		rowWidth[r] = col
	}
	if oversize {
		// Too large to lay out within the work budget; report no grid defects
		// rather than hang or fabricate a result on adversarial input.
		return nil
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

	var v []UAViolation
	if outOfRows {
		v = append(v, UAViolation{"7.2", "a table cell's RowSpan extends beyond the last row of the table", 0})
	}
	if overlap {
		v = append(v, UAViolation{"7.2", "table cells overlap on the grid (inconsistent RowSpan/ColSpan)", 0})
	}
	if hole {
		v = append(v, UAViolation{"7.2", "table rows do not all span the same number of columns (a grid cell is empty)", 0})
	}
	return v
}
