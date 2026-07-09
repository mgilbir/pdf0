package pdf0

import "testing"

// TestUACharMapping flags a Type0/Identity font used for text without a
// ToUnicode CMap, and clears it once ToUnicode is present.
func TestUACharMapping(t *testing.T) {
	mk := func(withToUnicode bool) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
		font := &Dictionary{}
		font.Set("Type", Name("Font"))
		font.Set("Subtype", Name("Type0"))
		font.Set("Encoding", Name("Identity-H"))
		if withToUnicode {
			font.Set("ToUnicode", IndirectRef{Number: 9})
			doc.Objects[9] = &IndirectObject{Number: 9, Value: &Stream{}}
		}
		fontRes := &Dictionary{}
		fontRes.Set("F1", IndirectRef{Number: 5})
		res := &Dictionary{}
		res.Set("Font", fontRes)
		page := &Dictionary{}
		page.Set("Type", Name("Page"))
		page.Set("Resources", res)
		page.Set("Contents", IndirectRef{Number: 6})
		pages := &Dictionary{}
		pages.Set("Type", Name("Pages"))
		pages.Set("Kids", Array{IndirectRef{Number: 3}})
		pages.Set("Count", Integer(1))
		cat := &Dictionary{}
		cat.Set("Type", Name("Catalog"))
		cat.Set("Pages", IndirectRef{Number: 2})
		doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
		doc.Objects[2] = &IndirectObject{Number: 2, Value: pages}
		doc.Objects[3] = &IndirectObject{Number: 3, Value: page}
		doc.Objects[5] = &IndirectObject{Number: 5, Value: font}
		doc.Objects[6] = &IndirectObject{Number: 6, Value: &Stream{Data: []byte("BT /F1 12 Tf <0001> Tj ET")}}
		doc.Trailer.Set("Root", IndirectRef{Number: 1})
		return doc
	}
	if len(mk(false).checkUACharMapping()) == 0 {
		t.Error("Identity font without ToUnicode not flagged")
	}
	if len(mk(true).checkUACharMapping()) != 0 {
		t.Error("Identity font with ToUnicode should be clean")
	}
}
