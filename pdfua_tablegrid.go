package pdf0

// Table-grid analysis for PDF/UA structure validation. A table's TR/TH/TD
// elements, together with their RowSpan/ColSpan table attributes, are laid out
// on a grid exactly as a renderer would. Definite structural defects — a span
// that extends past the last row, cells that overlap, or rows that do not all
// span the same number of columns (a hole) — are reported. Only unambiguous
// defects are flagged so a well-formed table never raises a false positive.

// tableCell is one TH/TD with its resolved span.
type tableCell struct {
	rowSpan int
	colSpan int
}

// tableRow is the ordered cells of one TR.
type tableRow []tableCell

// checkUATableAssociation enforces the minimum of 7.5: a table that contains
// header cells (TH) must provide some header-association mechanism. If not one
// cell in the whole table carries a Scope attribute, a /Headers reference, or an
// /ID, the header structure is not determinable at all and is flagged. This is
// the conservative whole-table form of the rule — a table that uses any of the
// three mechanisms is left alone, so a well-formed table never false-positives.
func (d *Document) checkUATableAssociation(cat *Dictionary) []UAViolation {
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
			if d.tableLacksAssociation(elem, roleMap) {
				v = append(v, UAViolation{"7.5", "table with header cells provides no header association (no Scope, /Headers, or /ID on any cell)", 0})
			}
		}
		for _, kid := range d.structKids(elem) {
			walk(kid)
		}
	}
	walk(root.Get("K"))
	return v
}

// tableLacksAssociation reports whether a table has at least one TH but no cell
// carrying a Scope attribute, a /Headers reference, or an /ID.
func (d *Document) tableLacksAssociation(table *Dictionary, roleMap *Dictionary) bool {
	hasTH, anyAssoc := false, false
	seen := map[int]bool{}
	var scan func(node Object, top bool)
	scan = func(node Object, top bool) {
		if ref, ok := node.(IndirectRef); ok {
			if seen[ref.Number] {
				return
			}
			seen[ref.Number] = true
		}
		e := d.ResolveDict(node)
		if e == nil {
			if arr, ok := d.Resolve(node).(Array); ok {
				for _, k := range arr {
					scan(k, top)
				}
			}
			return
		}
		t := d.standardStructType(e, roleMap)
		if !top && t == "Table" {
			return
		}
		if t == "TH" {
			hasTH = true
			if d.cellHasScope(e) {
				anyAssoc = true
			}
		}
		if t == "TH" || t == "TD" {
			if e.Get("Headers") != nil || e.Get("ID") != nil {
				anyAssoc = true
			}
		}
		for _, k := range d.structKids(e) {
			scan(k, false)
		}
	}
	scan(table, true)
	return hasTH && !anyAssoc
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

	for r := 0; r < nRows; r++ {
		col := 0
		for _, cell := range rows[r] {
			// Skip columns already taken by a row span from above.
			for occupied[r][col] {
				col++
			}
			if r+cell.rowSpan > nRows {
				outOfRows = true
			}
			for dr := 0; dr < cell.rowSpan && r+dr < nRows; dr++ {
				for dc := 0; dc < cell.colSpan; dc++ {
					c := col + dc
					if occupied[r+dr][c] {
						overlap = true
					}
					occupied[r+dr][c] = true
				}
			}
			col += cell.colSpan
		}
		rowWidth[r] = col
	}

	// A hole exists if, after placement, some row does not fill the full grid
	// width (the maximum column reached by any row).
	width := 0
	for _, w := range rowWidth {
		if w > width {
			width = w
		}
	}
	hole := false
	for r := 0; r < nRows; r++ {
		for c := 0; c < width; c++ {
			if !occupied[r][c] {
				hole = true
			}
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
