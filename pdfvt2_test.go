package pdf0

import (
	"bytes"
	"strings"
	"testing"
)

// buildPDFVT2Doc adapts the conforming PDF/VT-1 fixture to PDF/VT-2 by changing
// the GTS_PDFVTVersion identifier.
func buildPDFVT2Doc() *Document {
	d := buildPDFVT1Doc()
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	ms := d.Resolve(cat.Get("Metadata")).(*Stream)
	ms.Data = bytes.Replace(ms.Data, []byte("PDF/VT-1"), []byte("PDF/VT-2"), 1)
	return d
}

func vtHas(v []PDFVTViolation, substr string) bool {
	for _, e := range v {
		if strings.Contains(e.Message, substr) {
			return true
		}
	}
	return false
}

func TestValidatePDFVT2Valid(t *testing.T) {
	if v := ValidatePDFVT2(buildPDFVT2Doc()); len(v) != 0 {
		t.Errorf("conformant PDF/VT-2 document flagged: %d (first: %v)", len(v), v[0])
	}
}

func TestValidatePDFVT2Identification(t *testing.T) {
	// A file identified as PDF/VT-1 must not pass a PDF/VT-2 check.
	if v := ValidatePDFVT2(buildPDFVT1Doc()); !vtHas(v, "does not identify PDF/VT-2") {
		t.Errorf("PDF/VT-2 should reject a PDF/VT-1 identifier; got %v", v)
	}
}

// TestPDFVT2ReferenceXObjectRelaxed: a reference (external-content) XObject is
// rejected by PDF/VT-1 (PDF/X-4 base) but permitted by PDF/VT-2 (PDF/X-5 base).
func TestPDFVT2ReferenceXObjectRelaxed(t *testing.T) {
	addRef := func(d *Document) {
		form := &Dictionary{}
		form.Set("Type", Name("XObject"))
		form.Set("Subtype", Name("Form"))
		form.Set("Ref", &Dictionary{})
		d.Objects[200] = &IndirectObject{Number: 200, Value: &Stream{Dict: *form}}
	}
	d1 := buildPDFVT1Doc()
	addRef(d1)
	if !vtHas(ValidatePDFVT(d1), "reference XObjects") {
		t.Error("PDF/VT-1 should reject a reference XObject")
	}
	d2 := buildPDFVT2Doc()
	addRef(d2)
	if vtHas(ValidatePDFVT2(d2), "reference XObjects") {
		t.Error("PDF/VT-2 should permit a reference XObject")
	}
}
