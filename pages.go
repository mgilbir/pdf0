package pdf0

import (
	"github.com/mgilbir/pdf0/object"
)

// This file implements the page-level document API: listing a document's
// pages, extracting a subset of them into a new document, and appending one
// document's pages to another. All of it rests on a cycle-safe graph copier
// that renumbers objects and remaps every indirect reference. The copy is
// structural, not semantic: a page is re-parented onto a fresh /Pages node, so
// attributes it inherited from its former ancestors (/Resources, /MediaBox,
// /Rotate — ISO 32000-2 clause 7.7.3.4) are not materialised onto the copy.

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

// graphCopier copies an object graph from a source document into a destination,
// assigning fresh object numbers and remapping indirect references. It is
// cycle-safe: each source object is copied once.
type graphCopier struct {
	src     *Document
	dst     *Document
	mapping map[int]int // source object number → destination object number
	nextNum int
}

func newGraphCopier(src, dst *Document, startNum int) *graphCopier {
	return &graphCopier{src: src, dst: dst, mapping: map[int]int{}, nextNum: startNum}
}

// copyRef copies the object referenced by ref (and its graph) into dst, skipping
// the given keys on the top object (used to drop a page's /Parent up-link).
func (g *graphCopier) copyRef(ref object.IndirectRef, skip map[object.Name]bool) object.IndirectRef {
	if n, ok := g.mapping[ref.Number]; ok {
		return object.IndirectRef{Number: n}
	}
	dstNum := g.nextNum
	g.nextNum++
	g.mapping[ref.Number] = dstNum

	src := g.src.Objects[ref.Number]
	if src == nil {
		g.dst.Objects[dstNum] = &object.IndirectObject{Number: dstNum, Value: object.Null{}}
		return object.IndirectRef{Number: dstNum}
	}
	placeholder := &object.IndirectObject{Number: dstNum, Value: object.Null{}}
	g.dst.Objects[dstNum] = placeholder
	placeholder.Value = g.copyValue(src.Value, skip)
	return object.IndirectRef{Number: dstNum}
}

func (g *graphCopier) copyValue(o object.Object, skip map[object.Name]bool) object.Object {
	switch v := o.(type) {
	case object.IndirectRef:
		return g.copyRef(v, nil)
	case *object.Dictionary:
		return g.copyDict(v, skip)
	case object.Array:
		cp := make(object.Array, len(v))
		for i := range v {
			cp[i] = g.copyValue(v[i], nil)
		}
		return cp
	case *object.Stream:
		d := g.copyDict(&v.Dict, skip)
		return &object.Stream{Dict: *d, Data: append([]byte(nil), v.Data...)}
	}
	return o // scalars are immutable
}

func (g *graphCopier) copyDict(d *object.Dictionary, skip map[object.Name]bool) *object.Dictionary {
	cp := &object.Dictionary{}
	for i, key := range d.Keys {
		if skip[key] {
			continue
		}
		cp.Keys = append(cp.Keys, key)
		cp.Values = append(cp.Values, g.copyValue(d.Values[i], nil))
	}
	return cp
}

// newDocWithPageTree creates an empty document with a catalog (object 1) and an
// empty /Pages node (object 2), ready to receive pages.
func newDocWithPageTree(version string) (*Document, int, int) {
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
	return doc, 1, 2
}

// pageRefsOf returns the indirect references to each page in the document.
func (d *Document) pageRefsOf() []object.IndirectRef {
	var refs []object.IndirectRef
	for _, pg := range d.view().Pages(d.view().CatalogPages()) {
		refs = append(refs, object.IndirectRef{Number: pg.ObjNum})
	}
	return refs
}

// appendPageInto copies one page (by its source reference) into dst under its
// /Pages node, re-pointing /Parent and inheriting nothing.
func appendPageInto(g *graphCopier, dst *Document, pagesNum int, srcPageRef object.IndirectRef) {
	newRef := g.copyRef(srcPageRef, map[object.Name]bool{"Parent": true})
	// A source page held as a direct (inline) dictionary in /Kids has no object
	// number, so copyRef installs a Null placeholder; skip it instead of panicking
	// on the type assertion (audit C16).
	iobj := dst.Objects[newRef.Number]
	if iobj == nil {
		return
	}
	pageObj, ok := iobj.Value.(*object.Dictionary)
	if !ok {
		return
	}
	pageObj.Set("Parent", object.IndirectRef{Number: pagesNum})

	pages, ok := dst.Objects[pagesNum].Value.(*object.Dictionary)
	if !ok {
		return
	}
	// Resolve /Kids: the destination's page tree may store it as an indirect
	// reference to an array. Reading it directly (the previous code) yielded nil
	// and silently dropped every existing page (audit C15).
	kids, _ := dst.Resolve(pages.Get("Kids")).(object.Array)
	pages.Set("Kids", append(append(object.Array{}, kids...), newRef))
	pages.Set("Count", object.Integer(len(kids)+1))
}

// ExtractPages returns a new document containing only the given pages (0-based,
// in the order given). The source is not modified.
func (d *Document) ExtractPages(indices []int) (*Document, error) {
	srcPages := d.pageRefsOf()
	out, _, pagesNum := newDocWithPageTree(d.Version)
	g := newGraphCopier(d, out, 3)
	for _, idx := range indices {
		if idx < 0 || idx >= len(srcPages) {
			return nil, errPageOutOfRange(idx, len(srcPages))
		}
		appendPageInto(g, out, pagesNum, srcPages[idx])
	}
	finalizeSize(out)
	return out, nil
}

// AppendPages copies every page of other onto the end of this document.
func (d *Document) AppendPages(other *Document) {
	catalog := d.ResolveDict(d.Trailer.Get("Root"))
	if catalog == nil {
		return
	}
	pages := d.ResolveDict(catalog.Get("Pages"))
	if pages == nil {
		return
	}
	pagesNum := d.view().DictObjNum(pages)
	max := 0
	for num := range d.Objects {
		if num > max {
			max = num
		}
	}
	g := newGraphCopier(other, d, max+1)
	for _, ref := range other.pageRefsOf() {
		appendPageInto(g, d, pagesNum, ref)
	}
}

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
	return "page index out of range"
}

func errPageOutOfRange(idx, count int) error { return pageRangeError{idx, count} }
