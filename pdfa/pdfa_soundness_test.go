package pdfa

import (
	"github.com/mgilbir/pdf0/object"
	"strings"
	"testing"
)

// TestJPXForbiddenAtPDFA1 is the C17 guard: JPXDecode (a PDF 1.5 filter) is
// rejected at PDF/A-1, which is based on PDF 1.4, but allowed at 2b.
func TestJPXForbiddenAtPDFA1(t *testing.T) {
	doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	st := &object.Stream{Data: []byte("jpx")}
	st.Dict.Set("Filter", object.Name("JPXDecode"))
	doc.Objects[1] = &object.IndirectObject{Number: 1, Value: st}

	has := func(errs []Violation) bool {
		for _, e := range errs {
			if strings.Contains(e.Message, "JPXDecode") {
				return true
			}
		}
		return false
	}
	if !has(checkNoLZW(doc, PDFA1b)) {
		t.Error("JPXDecode must be forbidden at PDF/A-1")
	}
	if has(checkNoLZW(doc, PDFA2b)) {
		t.Error("JPXDecode must be allowed at PDF/A-2")
	}
}

// TestPageLevelOutputIntentNotDuplicated is the C23 guard: a page-level
// OutputIntent violation is reported once, not twice.
func TestPageLevelOutputIntentNotDuplicated(t *testing.T) {
	doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("Pages", object.IndirectRef{Number: 2})
	catOI := &object.Dictionary{}
	catOI.Set("S", object.Name("GTS_PDFA1"))
	catOI.Set("DestOutputProfile", object.IndirectRef{Number: 5})
	cat.Set("OutputIntents", object.Array{catOI}) // non-empty, reaches the main path
	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{object.IndirectRef{Number: 3}})
	pages.Set("Count", object.Integer(1))
	pageOI := &object.Dictionary{}
	pageOI.Set("S", object.Name("Foo")) // not GTS_PDFA1 -> a page-level error
	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", object.IndirectRef{Number: 2})
	page.Set("OutputIntents", object.Array{pageOI})

	doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
	doc.Objects[2] = &object.IndirectObject{Number: 2, Value: pages}
	doc.Objects[3] = &object.IndirectObject{Number: 3, Value: page}
	doc.Objects[5] = &object.IndirectObject{Number: 5, Value: &object.Stream{Data: []byte("icc")}}
	*doc.Trailer = object.Dictionary{}
	doc.Trailer.Set("Root", object.IndirectRef{Number: 1})

	n := 0
	for _, e := range checkOutputIntents(doc, PDFA4) {
		if strings.Contains(e.Message, "page OutputIntents") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("page-level OutputIntent error reported %d times, want 1", n)
	}
}
