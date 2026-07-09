package pdf0

import "testing"

// TestUAWidgetDescription covers the 7.18.1 form-field Widget /TU-or-/Alt rule,
// including /TU inherited from a parent field.
func TestUAWidgetDescription(t *testing.T) {
	base := func() (*Document, *Dictionary) {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		w := &Dictionary{}
		w.Set("Type", Name("Annot"))
		w.Set("Subtype", Name("Widget"))
		w.Set("FT", Name("Tx"))
		w.Set("StructParent", Integer(0))
		doc.Objects[5] = &IndirectObject{Number: 5, Value: w}
		return doc, w
	}
	// No TU, no Alt -> flagged.
	doc, _ := base()
	if !hasUAClause(doc.checkUAAnnotations(), "7.18.1") {
		t.Error("Widget without TU/Alt not flagged")
	}
	// Empty TU still counts as missing.
	doc, w := base()
	w.Set("TU", String{Value: []byte("")})
	if !hasUAClause(doc.checkUAAnnotations(), "7.18.1") {
		t.Error("Widget with empty TU not flagged")
	}
	// Non-empty TU -> clean.
	doc, w = base()
	w.Set("TU", String{Value: []byte("Your name")})
	if hasUAClause(doc.checkUAAnnotations(), "7.18.1") {
		t.Error("Widget with TU wrongly flagged")
	}
	// TU inherited from the parent field -> clean.
	doc, w = base()
	parent := &Dictionary{}
	parent.Set("TU", String{Value: []byte("Inherited")})
	doc.Objects[6] = &IndirectObject{Number: 6, Value: parent}
	w.Set("Parent", IndirectRef{Number: 6})
	if hasUAClause(doc.checkUAAnnotations(), "7.18.1") {
		t.Error("Widget with inherited TU wrongly flagged")
	}
	// Alt instead of TU -> clean.
	doc, w = base()
	w.Set("Alt", String{Value: []byte("alt")})
	if hasUAClause(doc.checkUAAnnotations(), "7.18.1") {
		t.Error("Widget with Alt wrongly flagged")
	}
}
