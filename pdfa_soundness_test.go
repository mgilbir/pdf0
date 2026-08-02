package pdf0

import (
	"strings"
	"testing"
)

// TestJPXForbiddenAtPDFA1 is the C17 guard: JPXDecode (a PDF 1.5 filter) is
// rejected at PDF/A-1, which is based on PDF 1.4, but allowed at 2b.
func TestJPXForbiddenAtPDFA1(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}}
	st := &Stream{Data: []byte("jpx")}
	st.Dict.Set("Filter", Name("JPXDecode"))
	doc.Objects[1] = &IndirectObject{Number: 1, Value: st}

	has := func(errs []ValidationError) bool {
		for _, e := range errs {
			if strings.Contains(e.Message, "JPXDecode") {
				return true
			}
		}
		return false
	}
	if !has(checkNoLZW(doc.view(), PDFA1b)) {
		t.Error("JPXDecode must be forbidden at PDF/A-1")
	}
	if has(checkNoLZW(doc.view(), PDFA2b)) {
		t.Error("JPXDecode must be allowed at PDF/A-2")
	}
}

// TestPageLevelOutputIntentNotDuplicated is the C23 guard: a page-level
// OutputIntent violation is reported once, not twice.
func TestPageLevelOutputIntentNotDuplicated(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}}
	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("Pages", IndirectRef{Number: 2})
	catOI := &Dictionary{}
	catOI.Set("S", Name("GTS_PDFA1"))
	catOI.Set("DestOutputProfile", IndirectRef{Number: 5})
	cat.Set("OutputIntents", Array{catOI}) // non-empty, reaches the main path
	pages := &Dictionary{}
	pages.Set("Type", Name("Pages"))
	pages.Set("Kids", Array{IndirectRef{Number: 3}})
	pages.Set("Count", Integer(1))
	pageOI := &Dictionary{}
	pageOI.Set("S", Name("Foo")) // not GTS_PDFA1 -> a page-level error
	page := &Dictionary{}
	page.Set("Type", Name("Page"))
	page.Set("Parent", IndirectRef{Number: 2})
	page.Set("OutputIntents", Array{pageOI})

	doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
	doc.Objects[2] = &IndirectObject{Number: 2, Value: pages}
	doc.Objects[3] = &IndirectObject{Number: 3, Value: page}
	doc.Objects[5] = &IndirectObject{Number: 5, Value: &Stream{Data: []byte("icc")}}
	doc.Trailer = Dictionary{}
	doc.Trailer.Set("Root", IndirectRef{Number: 1})

	n := 0
	for _, e := range checkOutputIntents(doc.view(), PDFA4) {
		if strings.Contains(e.Message, "page OutputIntents") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("page-level OutputIntent error reported %d times, want 1", n)
	}
}
