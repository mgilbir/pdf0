package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestAppendPagesIndirectKids is the C15 guard: appending onto a page tree whose
// /Kids is an indirect reference (legal) must not discard the existing pages.
func TestAppendPagesIndirectKids(t *testing.T) {
	dst := &Document{Version: "2.0", Objects: map[int]*object.IndirectObject{}}
	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("Pages", object.IndirectRef{Number: 2})
	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.IndirectRef{Number: 10}) // /Kids is an indirect array
	pages.Set("Count", object.Integer(1))
	page1 := &object.Dictionary{}
	page1.Set("Type", object.Name("Page"))
	page1.Set("Parent", object.IndirectRef{Number: 2})
	page1.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792)})
	dst.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
	dst.Objects[2] = &object.IndirectObject{Number: 2, Value: pages}
	dst.Objects[3] = &object.IndirectObject{Number: 3, Value: page1}
	dst.Objects[10] = &object.IndirectObject{Number: 10, Value: object.Array{object.IndirectRef{Number: 3}}}
	dst.Trailer = object.Dictionary{}
	dst.Trailer.Set("Root", object.IndirectRef{Number: 1})

	if got := dst.PageCount(); got != 1 {
		t.Fatalf("precondition: %d pages, want 1", got)
	}

	base := buildMinimalPDF()
	src, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dst.AppendPages(src); err != nil {
		t.Fatal(err)
	}
	if got := dst.PageCount(); got != 2 {
		t.Fatalf("after AppendPages onto an indirect /Kids: %d pages, want 2 (the existing page must survive)", got)
	}
}
