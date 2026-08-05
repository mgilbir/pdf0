package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestUAHeadingSkip flags a skipped heading level and accepts a proper sequence.
func TestUAHeadingSkip(t *testing.T) {
	mk := func(levels ...string) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		cat := &object.Dictionary{}
		cat.Set("Type", object.Name("Catalog"))
		root := &object.Dictionary{Keys: []object.Name{"Type"}, Values: []object.Object{object.Name("StructTreeRoot")}}
		var kids object.Array
		for i, lvl := range levels {
			h := &object.Dictionary{}
			h.Set("S", object.Name(lvl))
			doc.Objects[10+i] = &object.IndirectObject{Number: 10 + i, Value: h}
			kids = append(kids, object.IndirectRef{Number: 10 + i})
		}
		root.Set("K", kids)
		doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
		doc.Objects[2] = &object.IndirectObject{Number: 2, Value: root}
		cat.Set("StructTreeRoot", object.IndirectRef{Number: 2})
		doc.Trailer.Set("Root", object.IndirectRef{Number: 1})
		return doc
	}
	has74 := func(doc core.View) bool {
		for _, e := range checkUAHeadings(doc, doc.ResolveDict(doc.Trailer.Get("Root"))) {
			if e.Clause == "7.4" {
				return true
			}
		}
		return false
	}
	if !has74(mk("H1", "H3")) {
		t.Error("H1→H3 skip not flagged")
	}
	if has74(mk("H1", "H2", "H3", "H2", "H1")) {
		t.Error("a valid heading sequence was flagged")
	}
}
