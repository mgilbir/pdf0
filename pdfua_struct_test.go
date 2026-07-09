package pdf0

import "testing"

// TestUAStructNesting flags a misplaced table cell and accepts a well-formed
// table structure.
func TestUAStructNesting(t *testing.T) {
	// Build: StructTreeRoot -> Table -> kids. In the bad case a TD hangs directly
	// off the Table; in the good case Table -> TR -> TD.
	mk := func(good bool) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
		elem := func(num int, s Name, kids Array) {
			d := &Dictionary{}
			d.Set("S", s)
			if kids != nil {
				d.Set("K", kids)
			}
			doc.Objects[num] = &IndirectObject{Number: num, Value: d}
		}
		if good {
			elem(12, "TD", nil)
			elem(11, "TR", Array{IndirectRef{Number: 12}})
			elem(10, "Table", Array{IndirectRef{Number: 11}})
		} else {
			elem(12, "TD", nil)
			elem(10, "Table", Array{IndirectRef{Number: 12}}) // TD directly under Table
		}
		root := &Dictionary{}
		root.Set("Type", Name("StructTreeRoot"))
		root.Set("K", IndirectRef{Number: 10})
		cat := &Dictionary{}
		cat.Set("Type", Name("Catalog"))
		cat.Set("StructTreeRoot", IndirectRef{Number: 2})
		doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
		doc.Objects[2] = &IndirectObject{Number: 2, Value: root}
		doc.Trailer.Set("Root", IndirectRef{Number: 1})
		return doc
	}
	bad := mk(false)
	if len(bad.checkUAStructNesting(bad.ResolveDict(bad.Trailer.Get("Root")))) == 0 {
		t.Error("TD directly under Table not flagged")
	}
	good := mk(true)
	if v := good.checkUAStructNesting(good.ResolveDict(good.Trailer.Get("Root"))); len(v) != 0 {
		t.Errorf("well-formed table flagged: %v", v)
	}
}
