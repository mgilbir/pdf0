package pdf0

import "testing"

// heading adds a structure element with type s (and optional kids) to doc and
// returns a reference to it.
func heading(doc *Document, num int, s Name, kids ...Object) IndirectRef {
	e := &Dictionary{}
	e.Set("S", s)
	if len(kids) > 0 {
		e.Set("K", Array(kids))
	}
	doc.Objects[num] = &IndirectObject{Number: num, Value: e}
	return IndirectRef{Number: num}
}

// headingDoc wraps a structure tree with the given top-level /K under a catalog.
func headingDoc(doc *Document, k Object) *Dictionary {
	root := &Dictionary{}
	root.Set("Type", Name("StructTreeRoot"))
	root.Set("K", k)
	doc.Objects[2] = &IndirectObject{Number: 2, Value: root}
	cat := &Dictionary{}
	cat.Set("StructTreeRoot", IndirectRef{Number: 2})
	doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
	return cat
}

// TestUAFirstHeadingH1 flags a document whose first heading is not H1.
func TestUAFirstHeadingH1(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}}
	cat := headingDoc(doc, Array{heading(doc, 10, "H2"), heading(doc, 11, "H3")})
	if !hasUAClause(doc.checkUAHeadings(cat), "7.4.2") {
		t.Error("first heading H2 not flagged")
	}
	doc = &Document{Objects: map[int]*IndirectObject{}}
	cat = headingDoc(doc, Array{heading(doc, 10, "H1"), heading(doc, 11, "H2")})
	if hasUAClause(doc.checkUAHeadings(cat), "7.4.2") {
		t.Error("first heading H1 wrongly flagged")
	}
}

// TestUAOneHPerNode flags a node with two child H headings.
func TestUAOneHPerNode(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}}
	sect := heading(doc, 12, "Sect", heading(doc, 10, "H"), heading(doc, 11, "H"))
	cat := headingDoc(doc, sect)
	if !hasUAClause(doc.checkUAOneHPerNode(cat), "7.4.4") {
		t.Error("two H children under one node not flagged")
	}
	doc = &Document{Objects: map[int]*IndirectObject{}}
	single := heading(doc, 12, "Sect", heading(doc, 10, "H"))
	cat = headingDoc(doc, single)
	if hasUAClause(doc.checkUAOneHPerNode(cat), "7.4.4") {
		t.Error("single H child wrongly flagged")
	}
}
