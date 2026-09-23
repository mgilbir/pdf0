package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestUAFormXObjectMCID flags a tagged (MCID-bearing) form XObject painted more
// than once and accepts one painted a single time.
func TestUAFormXObjectMCID(t *testing.T) {
	mk := func(pageContent string) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, nil)
		form := &object.Stream{Dict: object.Dictionary{}, Data: []byte("/P <</MCID 0>> BDC (hi) Tj EMC")}
		form.Dict.Set("Type", object.Name("XObject"))
		form.Dict.Set("Subtype", object.Name("Form"))
		doc.Objects[7] = &object.IndirectObject{Number: 7, Value: form}
		xobjs := &object.Dictionary{}
		xobjs.Set("Fm0", object.IndirectRef{Number: 7})
		res := &object.Dictionary{}
		res.Set("XObject", xobjs)
		page := &object.Dictionary{}
		page.Set("Type", object.Name("Page"))
		page.Set("Resources", res)
		page.Set("Contents", object.IndirectRef{Number: 6})
		pages := &object.Dictionary{}
		pages.Set("Type", object.Name("Pages"))
		pages.Set("Kids", object.Array{object.IndirectRef{Number: 3}})
		pages.Set("Count", object.Integer(1))
		cat := &object.Dictionary{}
		cat.Set("Type", object.Name("Catalog"))
		cat.Set("Pages", object.IndirectRef{Number: 2})
		doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
		doc.Objects[2] = &object.IndirectObject{Number: 2, Value: pages}
		doc.Objects[3] = &object.IndirectObject{Number: 3, Value: page}
		doc.Objects[6] = &object.IndirectObject{Number: 6, Value: &object.Stream{Data: []byte(pageContent)}}
		doc.Trailer.Set("Root", object.IndirectRef{Number: 1})
		return doc
	}
	if len(checkUAFormXObjectMCID(mk("/Fm0 Do /Fm0 Do"))) == 0 {
		t.Error("tagged form painted twice not flagged")
	}
	if v := checkUAFormXObjectMCID(mk("/Fm0 Do")); len(v) != 0 {
		t.Errorf("tagged form painted once wrongly flagged: %v", v)
	}
}

func TestContainsNameToken(t *testing.T) {
	for _, tc := range []struct {
		content string
		want    bool
	}{
		{"/P <</MCID 0>> BDC", true},
		{"/P <</MC#49D 0>> BDC", true}, // #49 is I: the same name, escaped
		{"/MCIDExtra 0", false},
		{"(/MCID 0) Tj", false},             // inside a string
		{"% /MCID 0\n", false},              // inside a comment
		{"BI /W 1 /H 1 ID /MCID EI", false}, // inside inline-image data
	} {
		if got := containsNameToken(core.Canceler{}, []byte(tc.content), "MCID"); got != tc.want {
			t.Errorf("%q: containsNameToken = %v, want %v", tc.content, got, tc.want)
		}
	}
}
