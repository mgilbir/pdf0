package pdf0

import "testing"

// TestUAHeadingSkip flags a skipped heading level and accepts a proper sequence.
func TestUAHeadingSkip(t *testing.T) {
	mk := func(levels ...string) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
		cat := &Dictionary{}
		cat.Set("Type", Name("Catalog"))
		root := &Dictionary{Keys: []Name{"Type"}, Values: []Object{Name("StructTreeRoot")}}
		var kids Array
		for i, lvl := range levels {
			h := &Dictionary{}
			h.Set("S", Name(lvl))
			doc.Objects[10+i] = &IndirectObject{Number: 10 + i, Value: h}
			kids = append(kids, IndirectRef{Number: 10 + i})
		}
		root.Set("K", kids)
		doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
		doc.Objects[2] = &IndirectObject{Number: 2, Value: root}
		cat.Set("StructTreeRoot", IndirectRef{Number: 2})
		doc.Trailer.Set("Root", IndirectRef{Number: 1})
		return doc
	}
	has74 := func(doc *Document) bool {
		for _, e := range doc.checkUAHeadings(doc.ResolveDict(doc.Trailer.Get("Root"))) {
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
