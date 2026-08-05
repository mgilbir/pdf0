package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

func TestUATableTHScope(t *testing.T) {
	mk := func(scope string, hasID bool) (core.View, *object.Dictionary) {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		th := &object.Dictionary{}
		th.Set("S", object.Name("TH"))
		if scope != "" {
			attr := &object.Dictionary{}
			attr.Set("O", object.Name("Table"))
			attr.Set("Scope", object.Name(scope))
			th.Set("A", attr)
		}
		if hasID {
			th.Set("ID", object.String{Value: []byte("h1")})
		}
		doc.Objects[30] = &object.IndirectObject{Number: 30, Value: th}
		tr := &object.Dictionary{}
		tr.Set("S", object.Name("TR"))
		tr.Set("K", object.Array{object.IndirectRef{Number: 30}})
		doc.Objects[20] = &object.IndirectObject{Number: 20, Value: tr}
		table := &object.Dictionary{}
		table.Set("S", object.Name("Table"))
		table.Set("K", object.IndirectRef{Number: 20})
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: table}
		root := &object.Dictionary{}
		root.Set("Type", object.Name("StructTreeRoot"))
		root.Set("K", object.IndirectRef{Number: 10})
		doc.Objects[2] = &object.IndirectObject{Number: 2, Value: root}
		cat := &object.Dictionary{}
		cat.Set("StructTreeRoot", object.IndirectRef{Number: 2})
		doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
		return doc, cat
	}
	// No scope, no ID -> flagged.
	d, cat := mk("", false)
	if len(checkUATableTHScope(d, cat)) == 0 {
		t.Error("TH without Scope or ID not flagged")
	}
	// Scope present -> clean.
	d, cat = mk("Column", false)
	if len(checkUATableTHScope(d, cat)) != 0 {
		t.Error("TH with Scope wrongly flagged")
	}
	// ID present (no scope) -> clean.
	d, cat = mk("", true)
	if len(checkUATableTHScope(d, cat)) != 0 {
		t.Error("TH with ID wrongly flagged")
	}
}
