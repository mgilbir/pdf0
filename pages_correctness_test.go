package pdf0

import (
	"bytes"
	"testing"
)

// TestAppendPagesIndirectKids is the C15 guard: appending onto a page tree whose
// /Kids is an indirect reference (legal) must not discard the existing pages.
func TestAppendPagesIndirectKids(t *testing.T) {
	dst := &Document{Version: "2.0", Objects: map[int]*IndirectObject{}}
	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("Pages", IndirectRef{Number: 2})
	pages := &Dictionary{}
	pages.Set("Type", Name("Pages"))
	pages.Set("Kids", IndirectRef{Number: 10}) // /Kids is an indirect array
	pages.Set("Count", Integer(1))
	page1 := &Dictionary{}
	page1.Set("Type", Name("Page"))
	page1.Set("Parent", IndirectRef{Number: 2})
	page1.Set("MediaBox", Array{Integer(0), Integer(0), Integer(612), Integer(792)})
	dst.Objects[1] = &IndirectObject{Number: 1, Value: cat}
	dst.Objects[2] = &IndirectObject{Number: 2, Value: pages}
	dst.Objects[3] = &IndirectObject{Number: 3, Value: page1}
	dst.Objects[10] = &IndirectObject{Number: 10, Value: Array{IndirectRef{Number: 3}}}
	dst.Trailer = Dictionary{}
	dst.Trailer.Set("Root", IndirectRef{Number: 1})

	if got := dst.PageCount(); got != 1 {
		t.Fatalf("precondition: %d pages, want 1", got)
	}

	base := buildMinimalPDF()
	src, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	dst.AppendPages(src)
	if got := dst.PageCount(); got != 2 {
		t.Fatalf("after AppendPages onto an indirect /Kids: %d pages, want 2 (the existing page must survive)", got)
	}
}

// TestInlinePageNoPanic is the C16 guard: a page held as a direct (inline)
// dictionary in /Kids — which the parser accepts — must not panic ExtractPages
// or AppendPages.
func TestInlinePageNoPanic(t *testing.T) {
	doc := &Document{Version: "2.0", Objects: map[int]*IndirectObject{}}
	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("Pages", IndirectRef{Number: 2})
	pages := &Dictionary{}
	pages.Set("Type", Name("Pages"))
	inline := &Dictionary{}
	inline.Set("Type", Name("Page"))
	pages.Set("Kids", Array{inline}) // a direct-dict page, no object number
	pages.Set("Count", Integer(1))
	doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
	doc.Objects[2] = &IndirectObject{Number: 2, Value: pages}
	doc.Trailer = Dictionary{}
	doc.Trailer.Set("Root", IndirectRef{Number: 1})

	// Neither call may panic.
	_, _ = doc.ExtractPages([]int{0})
	dst, _, _ := newDocWithPageTree("2.0")
	dst.AppendPages(doc)
}
