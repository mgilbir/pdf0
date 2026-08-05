package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestUARealContent flags page text drawn outside any marked-content sequence
// and accepts text inside one.
func TestUARealContent(t *testing.T) {
	mk := func(content string) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		cat := &object.Dictionary{}
		cat.Set("Type", object.Name("Catalog"))
		cat.Set("Pages", object.IndirectRef{Number: 2})
		pages := &object.Dictionary{}
		pages.Set("Type", object.Name("Pages"))
		pages.Set("Kids", object.Array{object.IndirectRef{Number: 3}})
		pages.Set("Count", object.Integer(1))
		page := &object.Dictionary{}
		page.Set("Type", object.Name("Page"))
		page.Set("Contents", object.IndirectRef{Number: 4})
		stream := &object.Stream{Dict: object.Dictionary{}, Data: []byte(content)}
		doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
		doc.Objects[2] = &object.IndirectObject{Number: 2, Value: pages}
		doc.Objects[3] = &object.IndirectObject{Number: 3, Value: page}
		doc.Objects[4] = &object.IndirectObject{Number: 4, Value: stream}
		doc.Trailer.Set("Root", object.IndirectRef{Number: 1})
		return doc
	}
	cat := func(doc core.View) *object.Dictionary { return doc.ResolveDict(doc.Trailer.Get("Root")) }

	untagged := mk("BT /F1 12 Tf (hello) Tj ET")
	if len(checkUARealContent(untagged, cat(untagged))) == 0 {
		t.Error("untagged page text not flagged")
	}
	tagged := mk("/P BDC BT /F1 12 Tf (hello) Tj ET EMC")
	if len(checkUARealContent(tagged, cat(tagged))) != 0 {
		t.Error("tagged page text should be clean")
	}
	artifact := mk("/Artifact BMC BT (deco) Tj ET EMC")
	if len(checkUARealContent(artifact, cat(artifact))) != 0 {
		t.Error("artifact page text should be clean")
	}

	// Artifact nested inside tagged content (01-003).
	artInTag := mk("/P <</MCID 0>> BDC /Artifact BMC (x) Tj EMC EMC")
	if len(checkUARealContent(artInTag, cat(artInTag))) == 0 {
		t.Error("artifact nested in tagged content not flagged")
	}
	// Tagged content nested inside an artifact (01-004).
	tagInArt := mk("/Artifact BMC /P <</MCID 0>> BDC (x) Tj EMC EMC")
	if len(checkUARealContent(tagInArt, cat(tagInArt))) == 0 {
		t.Error("tagged content nested in an artifact not flagged")
	}
	// Optional content (/OC) around tagged content is transparent (no violation).
	ocWrap := mk("/OC /MC0 BDC /P <</MCID 0>> BDC (x) Tj EMC EMC")
	if len(checkUARealContent(ocWrap, cat(ocWrap))) != 0 {
		t.Error("/OC-wrapped tagged content should be clean")
	}
}

// TestUAAnnotStructType flags an annotation nested under the wrong structure
// element type and clears it under the right one.
func TestUAAnnotStructType(t *testing.T) {
	mk := func(parentType object.Name) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		cat := &object.Dictionary{}
		cat.Set("Type", object.Name("Catalog"))
		cat.Set("StructTreeRoot", object.IndirectRef{Number: 2})
		// StructTreeRoot -> element (parentType) -> OBJR -> widget annot (obj 5)
		objr := &object.Dictionary{}
		objr.Set("Type", object.Name("OBJR"))
		objr.Set("Obj", object.IndirectRef{Number: 5})
		elem := &object.Dictionary{}
		elem.Set("S", parentType)
		elem.Set("K", object.IndirectRef{Number: 4})
		root := &object.Dictionary{}
		root.Set("Type", object.Name("StructTreeRoot"))
		root.Set("K", object.IndirectRef{Number: 3})
		annot := &object.Dictionary{}
		annot.Set("Type", object.Name("Annot"))
		annot.Set("Subtype", object.Name("Widget"))
		annot.Set("StructParent", object.Integer(0))
		doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
		doc.Objects[2] = &object.IndirectObject{Number: 2, Value: root}
		doc.Objects[3] = &object.IndirectObject{Number: 3, Value: elem}
		doc.Objects[4] = &object.IndirectObject{Number: 4, Value: objr}
		doc.Objects[5] = &object.IndirectObject{Number: 5, Value: annot}
		doc.Trailer.Set("Root", object.IndirectRef{Number: 1})
		return doc
	}
	bad := mk("P") // widget under <P>, not <Form>
	if len(checkUAAnnotStructType(bad, bad.ResolveDict(bad.Trailer.Get("Root")))) == 0 {
		t.Error("widget under <P> not flagged")
	}
	good := mk("Form")
	if len(checkUAAnnotStructType(good, good.ResolveDict(good.Trailer.Get("Root")))) != 0 {
		t.Error("widget under <Form> should be clean")
	}
}
