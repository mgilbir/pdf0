package pdf0

import (
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"testing"
)

// pageDoc builds a minimal one-page document with the given page dict.
func pageDoc(page *object.Dictionary) *Document {
	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{object.IndirectRef{Number: 3}})
	pages.Set("Count", object.Integer(1))
	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("Pages", object.IndirectRef{Number: 2})
	return &Document{Version: "2.0", Objects: map[int]*object.IndirectObject{
		1: {Number: 1, Value: cat},
		2: {Number: 2, Value: pages},
		3: {Number: 3, Value: page},
	}, Trailer: dictWith("Root", object.IndirectRef{Number: 1})}
}

func dictWith(k object.Name, v object.Object) object.Dictionary {
	d := object.Dictionary{}
	d.Set(k, v)
	return d
}

func countRule(errs []pdfa.ValidationError, rule string) int {
	n := 0
	for _, e := range errs {
		if e.Rule == rule {
			n++
		}
	}
	return n
}

// TestIndirectSubtypeStillFlagged ensures a forbidden annotation subtype behind
// an indirect reference is still caught (audit C12).
func TestIndirectSubtypeStillFlagged(t *testing.T) {
	annot := &object.Dictionary{}
	annot.Set("Type", object.Name("Annot"))
	annot.Set("Subtype", object.IndirectRef{Number: 4}) // -> /Screen (forbidden)
	annot.Set("Rect", object.Array{object.Integer(0), object.Integer(0), object.Integer(1), object.Integer(1)})
	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Annots", object.Array{annot})
	doc := pageDoc(page)
	doc.Objects[4] = &object.IndirectObject{Number: 4, Value: object.Name("Screen")}

	if got := countRule(ValidatePDFABytes(doc, pdfa.PDFA2b, nil), "6.3.1"); got == 0 {
		t.Errorf("indirect /Subtype /Screen evaded the subtype rule")
	}
}

// TestAAOnNonWidgetFlagged ensures /AA on a non-widget annotation is flagged at
// 1b/2b/3b (audit C13).
func TestAAOnNonWidgetFlagged(t *testing.T) {
	aa := &object.Dictionary{}
	aa.Set("PO", &object.Dictionary{})
	annot := &object.Dictionary{}
	annot.Set("Type", object.Name("Annot"))
	annot.Set("Subtype", object.Name("Link"))
	annot.Set("Rect", object.Array{object.Integer(0), object.Integer(0), object.Integer(1), object.Integer(1)})
	annot.Set("AA", aa)
	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Annots", object.Array{annot})
	doc := pageDoc(page)

	if got := countRule(ValidatePDFABytes(doc, pdfa.PDFA2b, nil), "6.6.3"); got == 0 {
		t.Errorf("/AA on a non-widget annotation was not flagged at 2b")
	}
	// (At A-4 the same /AA is caught by the per-event trigger rule, which is a
	// separate check; that path is not exercised here.)
}

// TestImageSMaskTransparency1b ensures an image soft mask is flagged at 1b
// (audit C11).
func TestImageSMaskTransparency1b(t *testing.T) {
	img := &object.Stream{Dict: object.Dictionary{}}
	img.Dict.Set("Subtype", object.Name("Image"))
	img.Dict.Set("SMask", object.IndirectRef{Number: 5})
	xobj := &object.Dictionary{}
	xobj.Set("Im0", object.IndirectRef{Number: 4})
	res := &object.Dictionary{}
	res.Set("XObject", xobj)
	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Resources", res)
	doc := pageDoc(page)
	doc.Objects[4] = &object.IndirectObject{Number: 4, Value: img}
	doc.Objects[5] = &object.IndirectObject{Number: 5, Value: &object.Stream{Dict: object.Dictionary{}}}

	if got := countRule(ValidatePDFABytes(doc, pdfa.PDFA1b, nil), "6.4"); got == 0 {
		t.Errorf("image /SMask was not flagged as transparency at 1b")
	}
	// Not applicable at 2b.
	if got := countRule(ValidatePDFABytes(doc, pdfa.PDFA2b, nil), "6.4"); got != 0 {
		t.Errorf("2b must not use the 1b transparency rule, got %d", got)
	}
}
