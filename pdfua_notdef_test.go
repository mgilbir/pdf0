package pdf0

import "testing"

// TestUANotdefCID flags a Type0/Identity font that shows CID 0 (.notdef) and
// clears when only non-zero CIDs are shown.
func TestUANotdefCID(t *testing.T) {
	mk := func(content string) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
		font := &Dictionary{}
		font.Set("Type", Name("Font"))
		font.Set("Subtype", Name("Type0"))
		font.Set("Encoding", Name("Identity-H"))
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
		doc.Objects[6] = &IndirectObject{Number: 6, Value: &Stream{Data: []byte(content)}}
		doc.Trailer.Set("Root", IndirectRef{Number: 1})
		return doc
	}
	if len(mk("BT /F1 12 Tf <0000> Tj ET").checkUANotdefCID()) == 0 {
		t.Error("shown CID 0 (.notdef) not flagged")
	}
	if len(mk("BT /F1 12 Tf <00010002> Tj ET").checkUANotdefCID()) != 0 {
		t.Error("non-zero CIDs wrongly flagged")
	}
}
