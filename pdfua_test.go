package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/object"
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
	doc.Version = "1.7" // PDF/UA-1 is a 1.x profile
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	cat.Set("Lang", object.String{Value: []byte("en-US")})
	cat.Set("MarkInfo", &object.Dictionary{Keys: []object.Name{"Marked"}, Values: []object.Object{object.Boolean(true)}})
	cat.Set("ViewerPreferences", &object.Dictionary{Keys: []object.Name{"DisplayDocTitle"}, Values: []object.Object{object.Boolean(true)}})
	structRoot := &object.Dictionary{}
	structRoot.Set("Type", object.Name("StructTreeRoot"))
	doc.Objects[99] = &object.IndirectObject{Number: 99, Value: structRoot}
	cat.Set("StructTreeRoot", object.IndirectRef{Number: 99})
	meta := &object.Stream{Dict: object.Dictionary{}, Data: []byte(`<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"><rdf:Description xmlns:pdfuaid="http://www.aiim.org/pdfua/ns/id/" pdfuaid:part="1"><dc:title xmlns:dc="http://purl.org/dc/elements/1.1/">Test</dc:title></rdf:Description><rdf:Description rdf:about=""/></rdf:RDF></x:xmpmeta>`)}
	doc.Objects[98] = &object.IndirectObject{Number: 98, Value: meta}
	cat.Set("Metadata", object.IndirectRef{Number: 98})

	if v := ValidatePDFUA(doc); len(v) != 0 {
		t.Errorf("compliant document still reports violations: %v", v)
	}

	// A figure without /Alt is flagged.
	fig := &object.Dictionary{}
	fig.Set("S", object.Name("Figure"))
	doc.Objects[100] = &object.IndirectObject{Number: 100, Value: fig}
	structRoot.Set("K", object.IndirectRef{Number: 100})
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
	bad := &object.Dictionary{}
	bad.Set("S", object.Name("MadeUpType"))
	doc.Objects[101] = &object.IndirectObject{Number: 101, Value: bad}
	structRoot.Set("K", object.Array{object.IndirectRef{Number: 100}, object.IndirectRef{Number: 101}})
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
	page.Set("Annots", object.Array{object.IndirectRef{Number: 100}})
	if !hasClause("7.18.3") {
		t.Error("page with annotations and no /Tabs /S not flagged")
	}
	page.Set("Tabs", object.Name("S"))
	if hasClause("7.18.3") {
		t.Error("/Tabs /S should satisfy the tab-order rule")
	}
}
