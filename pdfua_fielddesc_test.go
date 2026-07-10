package pdf0

import "testing"

// TestUAFieldDescription flags a form field with no /TU whose description is
// placed on a pure-widget child, and accepts the conformant arrangements.
func TestUAFieldDescription(t *testing.T) {
	mk := func(fieldTU string, kid *Dictionary) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		field := &Dictionary{}
		field.Set("FT", Name("Btn"))
		if fieldTU != "" {
			field.Set("TU", String{Value: []byte(fieldTU)})
		}
		doc.Objects[11] = &IndirectObject{Number: 11, Value: kid}
		field.Set("Kids", Array{IndirectRef{Number: 11}})
		doc.Objects[10] = &IndirectObject{Number: 10, Value: field}
		form := &Dictionary{}
		form.Set("Fields", Array{IndirectRef{Number: 10}})
		cat := &Dictionary{}
		cat.Set("AcroForm", form)
		doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
		doc.Trailer = Dictionary{}
		doc.Trailer.Set("Root", IndirectRef{Number: 1})
		return doc
	}
	widget := func(t, tu string) *Dictionary {
		w := &Dictionary{}
		w.Set("Subtype", Name("Widget"))
		if t != "" {
			w.Set("T", String{Value: []byte(t)})
		}
		if tu != "" {
			w.Set("TU", String{Value: []byte(tu)})
		}
		return w
	}
	root := func(d *Document) *Dictionary { return d.ResolveDict(IndirectRef{Number: 1}) }

	// Field has no TU, pure widget kid carries TU -> flagged.
	d := mk("", widget("", "btn1"))
	if len(d.checkUAFieldDescription(root(d))) == 0 {
		t.Error("misplaced widget /TU not flagged")
	}
	// Field supplies its own TU -> clean.
	d = mk("button1", widget("", ""))
	if len(d.checkUAFieldDescription(root(d))) != 0 {
		t.Error("field with /TU wrongly flagged")
	}
	// Widget kid is itself a named sub-field (has /T) -> exempt.
	d = mk("", widget("text2", "desc"))
	if len(d.checkUAFieldDescription(root(d))) != 0 {
		t.Error("sub-field widget wrongly flagged")
	}
	// Widget kid without TU -> clean.
	d = mk("", widget("", ""))
	if len(d.checkUAFieldDescription(root(d))) != 0 {
		t.Error("widget without TU wrongly flagged")
	}
}
