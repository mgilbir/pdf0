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

// TestUAHeaderVersion flags a 2.0 header and accepts a 1.x one.
func TestUAHeaderVersion(t *testing.T) {
	d := &Document{Version: "2.0"}
	if len(d.checkUAHeaderVersion()) == 0 {
		t.Error("2.0 header not flagged for PDF/UA-1")
	}
	d.Version = "1.7"
	if len(d.checkUAHeaderVersion()) != 0 {
		t.Error("1.7 header wrongly flagged")
	}
}

// TestUASuspects flags /MarkInfo /Suspects true.
func TestUASuspects(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}}
	cat := &Dictionary{}
	mark := &Dictionary{}
	mark.Set("Suspects", Boolean(true))
	cat.Set("MarkInfo", mark)
	if len(doc.checkUASuspects(cat)) == 0 {
		t.Error("Suspects true not flagged")
	}
	mark.Set("Suspects", Boolean(false))
	if len(doc.checkUASuspects(cat)) != 0 {
		t.Error("Suspects false wrongly flagged")
	}
}

// TestUAStrongWeak flags a document mixing H and Hn headings.
func TestUAStrongWeak(t *testing.T) {
	mk := func(types ...Name) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		var kids Array
		n := 10
		for _, ty := range types {
			e := &Dictionary{}
			e.Set("S", ty)
			doc.Objects[n] = &IndirectObject{Number: n, Value: e}
			kids = append(kids, IndirectRef{Number: n})
			n++
		}
		root := &Dictionary{}
		root.Set("Type", Name("StructTreeRoot"))
		root.Set("K", kids)
		doc.Objects[2] = &IndirectObject{Number: 2, Value: root}
		cat := &Dictionary{}
		cat.Set("StructTreeRoot", IndirectRef{Number: 2})
		doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
		return doc
	}
	if d := mk("H", "H1"); len(d.checkUAStrongWeak(d.ResolveDict(IndirectRef{Number: 1}))) == 0 {
		t.Error("mixed H/H1 not flagged")
	}
	if d := mk("H1", "H2"); len(d.checkUAStrongWeak(d.ResolveDict(IndirectRef{Number: 1}))) != 0 {
		t.Error("pure strong structure wrongly flagged")
	}
	if d := mk("H", "H"); len(d.checkUAStrongWeak(d.ResolveDict(IndirectRef{Number: 1}))) != 0 {
		t.Error("pure weak structure wrongly flagged")
	}
}

// TestUANotes flags Note elements lacking IDs or sharing an ID.
func TestUANotes(t *testing.T) {
	mk := func(ids ...string) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		var kids Array
		n := 10
		for _, id := range ids {
			e := &Dictionary{}
			e.Set("S", Name("Note"))
			if id != "" {
				e.Set("ID", String{Value: []byte(id)})
			}
			doc.Objects[n] = &IndirectObject{Number: n, Value: e}
			kids = append(kids, IndirectRef{Number: n})
			n++
		}
		root := &Dictionary{}
		root.Set("Type", Name("StructTreeRoot"))
		root.Set("K", kids)
		doc.Objects[2] = &IndirectObject{Number: 2, Value: root}
		cat := &Dictionary{}
		cat.Set("StructTreeRoot", IndirectRef{Number: 2})
		doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
		return doc
	}
	if d := mk(""); len(d.checkUANotes(d.ResolveDict(IndirectRef{Number: 1}))) == 0 {
		t.Error("Note without ID not flagged")
	}
	if d := mk("a", "a"); len(d.checkUANotes(d.ResolveDict(IndirectRef{Number: 1}))) == 0 {
		t.Error("duplicate Note ID not flagged")
	}
	if d := mk("a", "b"); len(d.checkUANotes(d.ResolveDict(IndirectRef{Number: 1}))) != 0 {
		t.Error("unique Note IDs wrongly flagged")
	}
}
