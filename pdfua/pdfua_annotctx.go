package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// What a PDF/UA-1 annotation rule needs to know about an annotation beyond its
// own dictionary: the structure element that encloses it, and whether it lies
// outside its page's crop box. The veraPDF PDF/UA-1 profile (7.18.1-t01..t03,
// 7.18.5-t01/t02) exempts an annotation whose rectangle is outside the crop box
// — it is never seen — and reads an annotation's /Alt from its enclosing
// structure element, because an annotation dictionary has no /Alt of its own
// (ISO 32000-1 Table 323 puts /Alt on structure elements). The corpus carries
// both cases as pass files (PDF_UA-1 7.18.1-t02-pass-a, -d, -e, -h and
// 7.18.1-t03-pass-b, -d).

// annotContext holds, for the annotations the document places on pages, the
// enclosing structure element and the clipped crop box of their page.
type annotContext struct {
	annots []*object.Dictionary                      // every page's /Annots, in page order, each once
	parent map[*object.Dictionary]*object.Dictionary // annotation (or field) -> enclosing structure element
	crop   map[*object.Dictionary][4]float64         // annotation -> its page's crop box
}

// newAnnotContext builds the context from the flattened structure tree (an
// OBJR's enclosing element is its parent node) and the page tree (each page's
// /Annots, with the page's inherited /CropBox clipped to its /MediaBox).
func newAnnotContext(d core.View, cat *object.Dictionary) annotContext {
	ctx := annotContext{
		parent: map[*object.Dictionary]*object.Dictionary{},
		crop:   map[*object.Dictionary][4]float64{},
	}
	if cat == nil {
		return ctx
	}
	// A dictionary an OBJR names may be an annotation or a form field: a
	// field and its single widget are often one dictionary.
	nodes := structTree(d, cat)
	for _, n := range nodes {
		if t, _ := d.ResolveName(n.Elem.Get("Type")); t != "OBJR" || n.Parent < 0 {
			continue
		}
		if obj := d.ResolveDict(n.Elem.Get("Obj")); obj != nil {
			if _, dup := ctx.parent[obj]; !dup {
				ctx.parent[obj] = nodes[n.Parent].Elem
			}
		}
	}
	seen := map[*object.Dictionary]bool{}
	for _, pg := range d.PagesWithAttrs(d.CatalogPages()) {
		box, hasBox := pageCropBox(d, pg)
		annots, _ := d.Resolve(pg.Dict.Get("Annots")).(object.Array)
		for _, a := range annots {
			d.Charge(1)
			ad := d.ResolveDict(a)
			if ad == nil || seen[ad] {
				continue
			}
			seen[ad] = true
			ctx.annots = append(ctx.annots, ad)
			if hasBox {
				ctx.crop[ad] = box
			}
		}
	}
	return ctx
}

// outsideCropBox is veraPDF's PDAnnotation.isOutsideCropBox: the annotation's
// /Rect, as written, lies wholly on or beyond one edge of its page's crop box.
// An annotation on no page, or without a four-number /Rect, is not outside.
func (c annotContext) outsideCropBox(d core.View, a *object.Dictionary) bool {
	box, ok := c.crop[a]
	if !ok {
		return false
	}
	r, ok := rect4(d, a.Get("Rect"))
	if !ok {
		return false
	}
	return box[1] >= r[3] || box[0] >= r[2] || box[3] <= r[1] || box[2] <= r[0]
}

// structAlt reports whether the structure element enclosing the annotation
// carries a non-empty /Alt.
func (c annotContext) structAlt(d core.View, a *object.Dictionary) bool {
	if se := c.parent[a]; se != nil {
		return d.NonEmptyStringOrLocked(se.Get("Alt"))
	}
	return false
}

// pageCropBox is a page's crop box as veraPDF reads it (PDPage.getCropBox): the
// inherited /CropBox clipped to the inherited /MediaBox, or the /MediaBox when
// there is no /CropBox.
func pageCropBox(d core.View, pg core.PageWithAttrs) ([4]float64, bool) {
	media, hasMedia := rect4(d, pg.Attrs[1])
	crop, hasCrop := rect4(d, pg.Attrs[2])
	switch {
	case hasCrop && hasMedia:
		return [4]float64{max(crop[0], media[0]), max(crop[1], media[1]), min(crop[2], media[2]), min(crop[3], media[3])}, true
	case hasCrop:
		return crop, true
	}
	return media, hasMedia
}

// rect4 reads a rectangle of exactly four numbers, as written (not normalized).
func rect4(d core.View, o object.Object) ([4]float64, bool) {
	arr, ok := d.Resolve(o).(object.Array)
	if !ok || len(arr) != 4 {
		return [4]float64{}, false
	}
	var r [4]float64
	for i, e := range arr {
		f, ok := d.ResolveNumber(e)
		if !ok {
			return [4]float64{}, false
		}
		r[i] = f
	}
	return r, true
}
