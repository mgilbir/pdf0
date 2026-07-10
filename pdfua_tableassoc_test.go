package pdf0

import "testing"

// buildTableWithCells builds a StructTreeRoot -> Table -> TR -> cells, where
// each cell is (type, scope, hasHeaders, hasID), and returns the catalog.
type testCell struct {
	typ        Name
	scope      string
	hasHeaders bool
	hasID      bool
}

func buildTable(cells ...testCell) (*Document, *Dictionary) {
	doc := &Document{Objects: map[int]*IndirectObject{}}
	var cellRefs Array
	n := 30
	for _, tc := range cells {
		c := &Dictionary{}
		c.Set("S", tc.typ)
		if tc.scope != "" {
			attr := &Dictionary{}
			attr.Set("O", Name("Table"))
			attr.Set("Scope", Name(tc.scope))
			c.Set("A", attr)
		}
		if tc.hasHeaders {
			c.Set("Headers", Array{})
		}
		if tc.hasID {
			c.Set("ID", String{Value: []byte("h1")})
		}
		doc.Objects[n] = &IndirectObject{Number: n, Value: c}
		cellRefs = append(cellRefs, IndirectRef{Number: n})
		n++
	}
	tr := &Dictionary{}
	tr.Set("S", Name("TR"))
	tr.Set("K", cellRefs)
	doc.Objects[20] = &IndirectObject{Number: 20, Value: tr}
	table := &Dictionary{}
	table.Set("S", Name("Table"))
	table.Set("K", IndirectRef{Number: 20})
	doc.Objects[10] = &IndirectObject{Number: 10, Value: table}
	root := &Dictionary{}
	root.Set("Type", Name("StructTreeRoot"))
	root.Set("K", IndirectRef{Number: 10})
	doc.Objects[2] = &IndirectObject{Number: 2, Value: root}
	cat := &Dictionary{}
	cat.Set("StructTreeRoot", IndirectRef{Number: 2})
	doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
	return doc, cat
}

func TestUATableAssociation(t *testing.T) {
	// TH with no scope/headers/id anywhere -> flagged.
	doc, cat := buildTable(testCell{typ: "TH"}, testCell{typ: "TD"})
	if len(doc.checkUATableAssociation(cat)) == 0 {
		t.Error("table with no header association not flagged")
	}
	// A TH with Scope -> clean.
	doc, cat = buildTable(testCell{typ: "TH", scope: "Column"}, testCell{typ: "TD"})
	if len(doc.checkUATableAssociation(cat)) != 0 {
		t.Error("table with a Scope wrongly flagged")
	}
	// A cell with /Headers -> clean.
	doc, cat = buildTable(testCell{typ: "TH"}, testCell{typ: "TD", hasHeaders: true})
	if len(doc.checkUATableAssociation(cat)) != 0 {
		t.Error("table with /Headers wrongly flagged")
	}
	// A table with no TH at all is out of scope -> clean.
	doc, cat = buildTable(testCell{typ: "TD"}, testCell{typ: "TD"})
	if len(doc.checkUATableAssociation(cat)) != 0 {
		t.Error("table without TH wrongly flagged")
	}
}
