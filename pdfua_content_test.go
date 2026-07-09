package pdf0

import "testing"

// TestUARealContent flags page text drawn outside any marked-content sequence
// and accepts text inside one.
func TestUARealContent(t *testing.T) {
	mk := func(content string) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
		cat := &Dictionary{}
		cat.Set("Type", Name("Catalog"))
		cat.Set("Pages", IndirectRef{Number: 2})
		pages := &Dictionary{}
		pages.Set("Type", Name("Pages"))
		pages.Set("Kids", Array{IndirectRef{Number: 3}})
		pages.Set("Count", Integer(1))
		page := &Dictionary{}
		page.Set("Type", Name("Page"))
		page.Set("Contents", IndirectRef{Number: 4})
		stream := &Stream{Dict: Dictionary{}, Data: []byte(content)}
		doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
		doc.Objects[2] = &IndirectObject{Number: 2, Value: pages}
		doc.Objects[3] = &IndirectObject{Number: 3, Value: page}
		doc.Objects[4] = &IndirectObject{Number: 4, Value: stream}
		doc.Trailer.Set("Root", IndirectRef{Number: 1})
		return doc
	}
	cat := func(doc *Document) *Dictionary { return doc.ResolveDict(doc.Trailer.Get("Root")) }

	untagged := mk("BT /F1 12 Tf (hello) Tj ET")
	if len(untagged.checkUARealContent(cat(untagged))) == 0 {
		t.Error("untagged page text not flagged")
	}
	tagged := mk("/P BDC BT /F1 12 Tf (hello) Tj ET EMC")
	if len(tagged.checkUARealContent(cat(tagged))) != 0 {
		t.Error("tagged page text should be clean")
	}
	artifact := mk("/Artifact BMC BT (deco) Tj ET EMC")
	if len(artifact.checkUARealContent(cat(artifact))) != 0 {
		t.Error("artifact page text should be clean")
	}

	// Artifact nested inside tagged content (01-003).
	artInTag := mk("/P <</MCID 0>> BDC /Artifact BMC (x) Tj EMC EMC")
	if len(artInTag.checkUARealContent(cat(artInTag))) == 0 {
		t.Error("artifact nested in tagged content not flagged")
	}
	// Tagged content nested inside an artifact (01-004).
	tagInArt := mk("/Artifact BMC /P <</MCID 0>> BDC (x) Tj EMC EMC")
	if len(tagInArt.checkUARealContent(cat(tagInArt))) == 0 {
		t.Error("tagged content nested in an artifact not flagged")
	}
	// Optional content (/OC) around tagged content is transparent (no violation).
	ocWrap := mk("/OC /MC0 BDC /P <</MCID 0>> BDC (x) Tj EMC EMC")
	if len(ocWrap.checkUARealContent(cat(ocWrap))) != 0 {
		t.Error("/OC-wrapped tagged content should be clean")
	}
}

// TestUAAnnotStructType flags an annotation nested under the wrong structure
// element type and clears it under the right one.
func TestUAAnnotStructType(t *testing.T) {
	mk := func(parentType Name) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
		cat := &Dictionary{}
		cat.Set("Type", Name("Catalog"))
		cat.Set("StructTreeRoot", IndirectRef{Number: 2})
		// StructTreeRoot -> element (parentType) -> OBJR -> widget annot (obj 5)
		objr := &Dictionary{}
		objr.Set("Type", Name("OBJR"))
		objr.Set("Obj", IndirectRef{Number: 5})
		elem := &Dictionary{}
		elem.Set("S", parentType)
		elem.Set("K", IndirectRef{Number: 4})
		root := &Dictionary{}
		root.Set("Type", Name("StructTreeRoot"))
		root.Set("K", IndirectRef{Number: 3})
		annot := &Dictionary{}
		annot.Set("Type", Name("Annot"))
		annot.Set("Subtype", Name("Widget"))
		annot.Set("StructParent", Integer(0))
		doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
		doc.Objects[2] = &IndirectObject{Number: 2, Value: root}
		doc.Objects[3] = &IndirectObject{Number: 3, Value: elem}
		doc.Objects[4] = &IndirectObject{Number: 4, Value: objr}
		doc.Objects[5] = &IndirectObject{Number: 5, Value: annot}
		doc.Trailer.Set("Root", IndirectRef{Number: 1})
		return doc
	}
	bad := mk("P") // widget under <P>, not <Form>
	if len(bad.checkUAAnnotStructType(bad.ResolveDict(bad.Trailer.Get("Root")))) == 0 {
		t.Error("widget under <P> not flagged")
	}
	good := mk("Form")
	if len(good.checkUAAnnotStructType(good.ResolveDict(good.Trailer.Get("Root")))) != 0 {
		t.Error("widget under <Form> should be clean")
	}
}
