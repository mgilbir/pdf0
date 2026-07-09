package pdf0

import "testing"

// TestUAFormXObjectMCID flags a tagged (MCID-bearing) form XObject painted more
// than once and accepts one painted a single time.
func TestUAFormXObjectMCID(t *testing.T) {
	mk := func(pageContent string) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
		form := &Stream{Dict: Dictionary{}, Data: []byte("/P <</MCID 0>> BDC (hi) Tj EMC")}
		form.Dict.Set("Type", Name("XObject"))
		form.Dict.Set("Subtype", Name("Form"))
		doc.Objects[7] = &IndirectObject{Number: 7, Value: form}
		xobjs := &Dictionary{}
		xobjs.Set("Fm0", IndirectRef{Number: 7})
		res := &Dictionary{}
		res.Set("XObject", xobjs)
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
		doc.Objects[6] = &IndirectObject{Number: 6, Value: &Stream{Data: []byte(pageContent)}}
		doc.Trailer.Set("Root", IndirectRef{Number: 1})
		return doc
	}
	if len(mk("/Fm0 Do /Fm0 Do").checkUAFormXObjectMCID()) == 0 {
		t.Error("tagged form painted twice not flagged")
	}
	if v := mk("/Fm0 Do").checkUAFormXObjectMCID(); len(v) != 0 {
		t.Errorf("tagged form painted once wrongly flagged: %v", v)
	}
}

func TestBytesContainsToken(t *testing.T) {
	if !bytesContainsToken([]byte("<</MCID 0>>"), "/MCID") {
		t.Error("/MCID token not found")
	}
	if bytesContainsToken([]byte("/MCIDExtra 0"), "/MCID") {
		t.Error("/MCID wrongly matched inside /MCIDExtra")
	}
}
