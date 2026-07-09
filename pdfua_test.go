package pdf0

import (
	"bytes"
	"strings"
	"testing"
)

// TestValidatePDFUA checks the foundational rules fire on a bare document and
// clear once the accessibility scaffolding is present.
func TestValidatePDFUA(t *testing.T) {
	base := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	v := ValidatePDFUA(doc)
	has := func(sub string) bool {
		for _, e := range v {
			if strings.Contains(e.Message, sub) {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"tagged", "structure tree", "default language", "DisplayDocTitle"} {
		if !has(want) {
			t.Errorf("expected a violation mentioning %q; got %v", want, v)
		}
	}

	// Make the document conform to the implemented checks.
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	cat.Set("Lang", String{Value: []byte("en-US")})
	cat.Set("MarkInfo", &Dictionary{Keys: []Name{"Marked"}, Values: []Object{Boolean(true)}})
	cat.Set("ViewerPreferences", &Dictionary{Keys: []Name{"DisplayDocTitle"}, Values: []Object{Boolean(true)}})
	structRoot := &Dictionary{}
	structRoot.Set("Type", Name("StructTreeRoot"))
	doc.Objects[99] = &IndirectObject{Number: 99, Value: structRoot}
	cat.Set("StructTreeRoot", IndirectRef{Number: 99})
	meta := &Stream{Dict: Dictionary{}, Data: []byte(`<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"><rdf:Description xmlns:pdfuaid="http://www.aiim.org/pdfua/ns/id/" pdfuaid:part="1"/></rdf:RDF></x:xmpmeta>`)}
	doc.Objects[98] = &IndirectObject{Number: 98, Value: meta}
	cat.Set("Metadata", IndirectRef{Number: 98})

	if v := ValidatePDFUA(doc); len(v) != 0 {
		t.Errorf("compliant document still reports violations: %v", v)
	}

	// A figure without /Alt is flagged.
	fig := &Dictionary{}
	fig.Set("S", Name("Figure"))
	doc.Objects[100] = &IndirectObject{Number: 100, Value: fig}
	structRoot.Set("K", IndirectRef{Number: 100})
	found := false
	for _, e := range ValidatePDFUA(doc) {
		if strings.Contains(e.Message, "alternate text") {
			found = true
		}
	}
	if !found {
		t.Error("figure without /Alt not flagged")
	}

	// A non-standard, unmapped structure type is flagged (7.1 role map).
	bad := &Dictionary{}
	bad.Set("S", Name("MadeUpType"))
	doc.Objects[101] = &IndirectObject{Number: 101, Value: bad}
	structRoot.Set("K", Array{IndirectRef{Number: 100}, IndirectRef{Number: 101}})
	hasClause := func(c string) bool {
		for _, e := range ValidatePDFUA(doc) {
			if e.Clause == c {
				return true
			}
		}
		return false
	}
	if !hasClause("7.1") {
		t.Error("non-standard structure type not flagged by the role-map check")
	}

	// A page with an annotation but no /Tabs /S is flagged (7.18.3).
	page := doc.PageList()[0]
	page.Set("Annots", Array{IndirectRef{Number: 100}})
	if !hasClause("7.18.3") {
		t.Error("page with annotations and no /Tabs /S not flagged")
	}
	page.Set("Tabs", Name("S"))
	if hasClause("7.18.3") {
		t.Error("/Tabs /S should satisfy the tab-order rule")
	}
}
