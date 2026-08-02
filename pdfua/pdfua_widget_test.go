package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestUAWidgetDescription covers the 7.18.1 form-field Widget /TU-or-/Alt rule,
// including /TU inherited from a parent field.
func TestUAWidgetDescription(t *testing.T) {
	base := func() (core.View, *object.Dictionary) {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		w := &object.Dictionary{}
		w.Set("Type", object.Name("Annot"))
		w.Set("Subtype", object.Name("Widget"))
		w.Set("FT", object.Name("Tx"))
		w.Set("StructParent", object.Integer(0))
		doc.Objects[5] = &object.IndirectObject{Number: 5, Value: w}
		return doc, w
	}
	// No TU, no Alt -> flagged.
	doc, _ := base()
	if !hasUAClause(checkUAAnnotations(doc), "7.18.1") {
		t.Error("Widget without TU/Alt not flagged")
	}
	// Empty TU still counts as missing.
	doc, w := base()
	w.Set("TU", object.String{Value: []byte("")})
	if !hasUAClause(checkUAAnnotations(doc), "7.18.1") {
		t.Error("Widget with empty TU not flagged")
	}
	// Non-empty TU -> clean.
	doc, w = base()
	w.Set("TU", object.String{Value: []byte("Your name")})
	if hasUAClause(checkUAAnnotations(doc), "7.18.1") {
		t.Error("Widget with TU wrongly flagged")
	}
	// TU inherited from the parent field -> clean.
	doc, w = base()
	parent := &object.Dictionary{}
	parent.Set("TU", object.String{Value: []byte("Inherited")})
	doc.Objects[6] = &object.IndirectObject{Number: 6, Value: parent}
	w.Set("Parent", object.IndirectRef{Number: 6})
	if hasUAClause(checkUAAnnotations(doc), "7.18.1") {
		t.Error("Widget with inherited TU wrongly flagged")
	}
	// Alt instead of TU -> clean.
	doc, w = base()
	w.Set("Alt", object.String{Value: []byte("alt")})
	if hasUAClause(checkUAAnnotations(doc), "7.18.1") {
		t.Error("Widget with Alt wrongly flagged")
	}
}
