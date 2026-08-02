package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestUAFieldDescription flags a form field with no /TU whose description is
// placed on a pure-widget child, and accepts the conformant arrangements.
func TestUAFieldDescription(t *testing.T) {
	mk := func(fieldTU string, kid *object.Dictionary) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		field := &object.Dictionary{}
		field.Set("FT", object.Name("Btn"))
		if fieldTU != "" {
			field.Set("TU", object.String{Value: []byte(fieldTU)})
		}
		doc.Objects[11] = &object.IndirectObject{Number: 11, Value: kid}
		field.Set("Kids", object.Array{object.IndirectRef{Number: 11}})
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: field}
		form := &object.Dictionary{}
		form.Set("Fields", object.Array{object.IndirectRef{Number: 10}})
		cat := &object.Dictionary{}
		cat.Set("AcroForm", form)
		doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
		doc.Trailer.Set("Root", object.IndirectRef{Number: 1})
		return doc
	}
	widget := func(t, tu string) *object.Dictionary {
		w := &object.Dictionary{}
		w.Set("Subtype", object.Name("Widget"))
		if t != "" {
			w.Set("T", object.String{Value: []byte(t)})
		}
		if tu != "" {
			w.Set("TU", object.String{Value: []byte(tu)})
		}
		return w
	}
	root := func(d core.View) *object.Dictionary { return d.ResolveDict(object.IndirectRef{Number: 1}) }

	// Field has no TU, pure widget kid carries TU -> flagged.
	d := mk("", widget("", "btn1"))
	if len(checkUAFieldDescription(d, root(d))) == 0 {
		t.Error("misplaced widget /TU not flagged")
	}
	// Field supplies its own TU -> clean.
	d = mk("button1", widget("", ""))
	if len(checkUAFieldDescription(d, root(d))) != 0 {
		t.Error("field with /TU wrongly flagged")
	}
	// Widget kid is itself a named sub-field (has /T) -> exempt.
	d = mk("", widget("text2", "desc"))
	if len(checkUAFieldDescription(d, root(d))) != 0 {
		t.Error("sub-field widget wrongly flagged")
	}
	// Widget kid without TU -> clean.
	d = mk("", widget("", ""))
	if len(checkUAFieldDescription(d, root(d))) != 0 {
		t.Error("widget without TU wrongly flagged")
	}
}
