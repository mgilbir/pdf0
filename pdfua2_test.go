package pdf0

import (
	"bytes"
	"strings"
	"testing"
)

// buildUA2Doc builds a document conforming to the implemented PDF/UA-2 checks: a
// PDF 2.0 file that is tagged, has a structure tree, a default language, a shown
// title, and XMP declaring pdfuaid:part 2.
func buildUA2Doc(t *testing.T) *Document {
	t.Helper()
	base := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	doc.Version = "2.0"
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	cat.Set("Lang", String{Value: []byte("en-US")})
	cat.Set("MarkInfo", &Dictionary{Keys: []Name{"Marked"}, Values: []Object{Boolean(true)}})
	cat.Set("ViewerPreferences", &Dictionary{Keys: []Name{"DisplayDocTitle"}, Values: []Object{Boolean(true)}})
	structRoot := &Dictionary{}
	structRoot.Set("Type", Name("StructTreeRoot"))
	doc.Objects[99] = &IndirectObject{Number: 99, Value: structRoot}
	cat.Set("StructTreeRoot", IndirectRef{Number: 99})
	meta := &Stream{Dict: Dictionary{}, Data: []byte(`<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"><rdf:Description xmlns:pdfuaid="http://www.aiim.org/pdfua/ns/id/" pdfuaid:part="2"><dc:title xmlns:dc="http://purl.org/dc/elements/1.1/">Test</dc:title></rdf:Description></rdf:RDF></x:xmpmeta>`)}
	doc.Objects[98] = &IndirectObject{Number: 98, Value: meta}
	cat.Set("Metadata", IndirectRef{Number: 98})
	return doc
}

func TestValidatePDFUA2Valid(t *testing.T) {
	if v := buildUA2Doc(t).ValidatePDFUA2(); len(v) != 0 {
		t.Errorf("conformant PDF/UA-2 document flagged: %v", v)
	}
}

func TestValidatePDFUA2Violations(t *testing.T) {
	uaHas := func(v []UAViolation, substr string) bool {
		for _, e := range v {
			if strings.Contains(e.Message, substr) {
				return true
			}
		}
		return false
	}

	// pdfuaid:part 1 is wrong for PDF/UA-2.
	d := buildUA2Doc(t)
	d.Objects[98].Value.(*Stream).Data = bytes.Replace(d.Objects[98].Value.(*Stream).Data, []byte(`pdfuaid:part="2"`), []byte(`pdfuaid:part="1"`), 1)
	if v := d.ValidatePDFUA2(); !uaHas(v, "pdfuaid:part must be 2") {
		t.Errorf("part 1 should be rejected for PDF/UA-2; got %v", v)
	}

	// PDF/UA-2 requires PDF 2.0.
	d = buildUA2Doc(t)
	d.Version = "1.7"
	if v := d.ValidatePDFUA2(); !uaHas(v, "PDF 2.0") {
		t.Errorf("PDF 1.7 should be rejected for PDF/UA-2; got %v", v)
	}

	// A carried-over PDF/UA-1 structural requirement still fires (not tagged).
	d = buildUA2Doc(t)
	d.ResolveDict(d.Trailer.Get("Root")).Delete("MarkInfo")
	if v := d.ValidatePDFUA2(); !uaHas(v, "tagged") {
		t.Errorf("an untagged document should be flagged; got %v", v)
	}
}
