package pdf0

// PageList returns the document's page dictionaries in reading order.
func (d *Document) PageList() []*Dictionary {
	var pages []*Dictionary
	for _, pg := range collectPages(d, d.catalogPages()) {
		pages = append(pages, pg.dict)
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
func (g *graphCopier) copyRef(ref IndirectRef, skip map[Name]bool) IndirectRef {
	if n, ok := g.mapping[ref.Number]; ok {
		return IndirectRef{Number: n}
	}
	dstNum := g.nextNum
	g.nextNum++
	g.mapping[ref.Number] = dstNum

	src := g.src.Objects[ref.Number]
	if src == nil {
		g.dst.Objects[dstNum] = &IndirectObject{Number: dstNum, Value: Null{}}
		return IndirectRef{Number: dstNum}
	}
	placeholder := &IndirectObject{Number: dstNum, Value: Null{}}
	g.dst.Objects[dstNum] = placeholder
	placeholder.Value = g.copyValue(src.Value, skip)
	return IndirectRef{Number: dstNum}
}

func (g *graphCopier) copyValue(o Object, skip map[Name]bool) Object {
	switch v := o.(type) {
	case IndirectRef:
		return g.copyRef(v, nil)
	case *Dictionary:
		return g.copyDict(v, skip)
	case Array:
		cp := make(Array, len(v))
		for i := range v {
			cp[i] = g.copyValue(v[i], nil)
		}
		return cp
	case *Stream:
		d := g.copyDict(&v.Dict, skip)
		return &Stream{Dict: *d, Data: append([]byte(nil), v.Data...)}
	}
	return o // scalars are immutable
}

func (g *graphCopier) copyDict(d *Dictionary, skip map[Name]bool) *Dictionary {
	cp := &Dictionary{}
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
	doc := &Document{Version: version, Objects: map[int]*IndirectObject{}}
	catalog := &Dictionary{}
	catalog.Set("Type", Name("Catalog"))
	catalog.Set("Pages", IndirectRef{Number: 2})
	pages := &Dictionary{}
	pages.Set("Type", Name("Pages"))
	pages.Set("Kids", Array{})
	pages.Set("Count", Integer(0))
	doc.Objects[1] = &IndirectObject{Number: 1, Value: catalog}
	doc.Objects[2] = &IndirectObject{Number: 2, Value: pages}
	doc.Trailer = Dictionary{}
	doc.Trailer.Set("Root", IndirectRef{Number: 1})
	return doc, 1, 2
}

// pageRefsOf returns the indirect references to each page in the document.
func (d *Document) pageRefsOf() []IndirectRef {
	var refs []IndirectRef
	for _, pg := range collectPages(d, d.catalogPages()) {
		refs = append(refs, IndirectRef{Number: pg.objNum})
	}
	return refs
}

// appendPageInto copies one page (by its source reference) into dst under its
// /Pages node, re-pointing /Parent and inheriting nothing.
func appendPageInto(g *graphCopier, dst *Document, pagesNum int, srcPageRef IndirectRef) {
	newRef := g.copyRef(srcPageRef, map[Name]bool{"Parent": true})
	pageObj := dst.Objects[newRef.Number].Value.(*Dictionary)
	pageObj.Set("Parent", IndirectRef{Number: pagesNum})

	pages := dst.Objects[pagesNum].Value.(*Dictionary)
	kids, _ := pages.Get("Kids").(Array)
	pages.Set("Kids", append(append(Array{}, kids...), newRef))
	pages.Set("Count", Integer(len(kids)+1))
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
	pagesNum := d.dictObjNum(pages)
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
	d.Trailer.Set("Size", Integer(max+1))
}

type pageRangeError struct {
	idx, count int
}

func (e pageRangeError) Error() string {
	return "page index out of range"
}

func errPageOutOfRange(idx, count int) error { return pageRangeError{idx, count} }
