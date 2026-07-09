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
}
