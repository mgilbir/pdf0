package pdf0

import (
	"fmt"

	"github.com/mgilbir/pdf0/object"
)

// This file implements the page-level document API: listing a document's pages,
// extracting a subset of them into a new document, and appending one
// document's pages to another. The copying is done by the page importer
// (pageimport.go) and the target's page tree is maintained by pagetree.go.

// PageList returns the document's page dictionaries in reading order.
func (d *Document) PageList() []*object.Dictionary {
	var pages []*object.Dictionary
	for _, pg := range d.view().Pages(d.view().CatalogPages()) {
		pages = append(pages, pg.Dict)
	}
	return pages
}

// PageCount returns the number of pages.
func (d *Document) PageCount() int { return len(d.PageList()) }

// newDocWithPageTree creates an empty document with a catalog (object 1) and an
// empty /Pages node (object 2), ready to receive pages.
func newDocWithPageTree(version string) *Document {
	if version == "" {
		version = "2.0"
	}
	doc := &Document{Version: version, Objects: map[int]*object.IndirectObject{}}
	catalog := &object.Dictionary{}
	catalog.Set("Type", object.Name("Catalog"))
	catalog.Set("Pages", object.IndirectRef{Number: 2})
	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{})
	pages.Set("Count", object.Integer(0))
	doc.Objects[1] = &object.IndirectObject{Number: 1, Value: catalog}
	doc.Objects[2] = &object.IndirectObject{Number: 2, Value: pages}
	doc.Trailer = object.Dictionary{}
	doc.Trailer.Set("Root", object.IndirectRef{Number: 1})
	return doc
}

// ExtractPages returns a new document holding copies of the given pages
// (0-based, in the order given; an index may repeat, and each repetition is a
// page of its own). The source is not modified.
//
// Each copy is a standalone page: the attributes it inherited from the source's
// page tree (/Resources, /MediaBox, /CropBox, /Rotate) are written on it, and
// nothing of the source is copied beyond what the pages use. Links between
// extracted pages are rewritten to the copies; links to pages left behind are
// removed. The report says what else was carried and what was not — see
// ImportReport and the policy described in pageimport.go.
//
// A Locked source (encrypted, not decrypted) is refused: its pages are
// ciphertext, and the extract would be written in the clear.
func (d *Document) ExtractPages(indices []int) (*Document, ImportReport, error) {
	if d == nil {
		return nil, ImportReport{}, errNilDocument
	}
	out := newDocWithPageTree(d.Version)
	report, err := importPages(d, out, indices, true)
	if err != nil {
		return nil, report, err
	}
	finalizeSize(out)
	return out, report, nil
}

// AppendPages copies every page of other onto the end of this document, with
// the same semantics as ExtractPages: each copy is standalone, links among the
// appended pages follow the copies, form fields join this document's form, and
// what is not carried is reported. other may be d itself.
//
// Either document being Locked is refused, as is a nil one; on error this
// document is unchanged.
func (d *Document) AppendPages(other *Document) (ImportReport, error) {
	if d == nil {
		return ImportReport{}, errNilDocument
	}
	if other == nil {
		return ImportReport{}, fmt.Errorf("pdf0: the document to append is nil")
	}
	indices := make([]int, other.PageCount())
	for i := range indices {
		indices[i] = i
	}
	return importPages(other, d, indices, false)
}

// finalizeSize records /Size on a document ExtractPages built, which has no
// source file. Write and WriteIncremental compute /Size themselves.
func finalizeSize(d *Document) {
	max := 0
	for num := range d.Objects {
		if num > max {
			max = num
		}
	}
	d.Trailer.Set("Size", object.Integer(max+1))
}

type pageRangeError struct {
	idx, count int
}

func (e pageRangeError) Error() string {
	return fmt.Sprintf("pdf0: page index %d is out of range; the document has %d page(s)", e.idx, e.count)
}

func errPageOutOfRange(idx, count int) error { return pageRangeError{idx, count} }
