package pdf0

import "testing"

func TestUATableTHScope(t *testing.T) {
	mk := func(scope string, hasID bool) (*Document, *Dictionary) {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		th := &Dictionary{}
		th.Set("S", Name("TH"))
		if scope != "" {
			attr := &Dictionary{}
			attr.Set("O", Name("Table"))
			attr.Set("Scope", Name(scope))
			th.Set("A", attr)
		}
		if hasID {
			th.Set("ID", String{Value: []byte("h1")})
		}
		doc.Objects[30] = &IndirectObject{Number: 30, Value: th}
		tr := &Dictionary{}
		tr.Set("S", Name("TR"))
		tr.Set("K", Array{IndirectRef{Number: 30}})
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
	// No scope, no ID -> flagged.
	d, cat := mk("", false)
	if len(d.checkUATableTHScope(cat)) == 0 {
		t.Error("TH without Scope or ID not flagged")
	}
	// Scope present -> clean.
	d, cat = mk("Column", false)
	if len(d.checkUATableTHScope(cat)) != 0 {
		t.Error("TH with Scope wrongly flagged")
	}
	// ID present (no scope) -> clean.
	d, cat = mk("", true)
	if len(d.checkUATableTHScope(cat)) != 0 {
		t.Error("TH with ID wrongly flagged")
	}
}
