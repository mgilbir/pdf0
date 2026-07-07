package pdf0

import "testing"

// pageDoc builds a minimal one-page document with the given page dict.
func pageDoc(page *Dictionary) *Document {
	pages := &Dictionary{}
	pages.Set("Type", Name("Pages"))
	pages.Set("Kids", Array{IndirectRef{Number: 3}})
	pages.Set("Count", Integer(1))
	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("Pages", IndirectRef{Number: 2})
	return &Document{Version: "2.0", Objects: map[int]*IndirectObject{
		1: {Number: 1, Value: cat},
		2: {Number: 2, Value: pages},
		3: {Number: 3, Value: page},
	}, Trailer: dictWith("Root", IndirectRef{Number: 1})}
}

func dictWith(k Name, v Object) Dictionary {
	d := Dictionary{}
	d.Set(k, v)
	return d
}

func countRule(errs []ValidationError, rule string) int {
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
	annot := &Dictionary{}
	annot.Set("Type", Name("Annot"))
	annot.Set("Subtype", IndirectRef{Number: 4}) // -> /Screen (forbidden)
	annot.Set("Rect", Array{Integer(0), Integer(0), Integer(1), Integer(1)})
	page := &Dictionary{}
	page.Set("Type", Name("Page"))
	page.Set("Annots", Array{annot})
	doc := pageDoc(page)
	doc.Objects[4] = &IndirectObject{Number: 4, Value: Name("Screen")}

	if got := countRule(ValidatePDFABytes(doc, PDFA2b, nil), "6.3.1"); got == 0 {
		t.Errorf("indirect /Subtype /Screen evaded the subtype rule")
	}
}

// TestAAOnNonWidgetFlagged ensures /AA on a non-widget annotation is flagged at
// 1b/2b/3b (audit C13).
func TestAAOnNonWidgetFlagged(t *testing.T) {
	aa := &Dictionary{}
	aa.Set("PO", &Dictionary{})
	annot := &Dictionary{}
	annot.Set("Type", Name("Annot"))
	annot.Set("Subtype", Name("Link"))
	annot.Set("Rect", Array{Integer(0), Integer(0), Integer(1), Integer(1)})
	annot.Set("AA", aa)
	page := &Dictionary{}
	page.Set("Type", Name("Page"))
	page.Set("Annots", Array{annot})
	doc := pageDoc(page)

	if got := countRule(ValidatePDFABytes(doc, PDFA2b, nil), "6.6.3"); got == 0 {
		t.Errorf("/AA on a non-widget annotation was not flagged at 2b")
	}
	// (At A-4 the same /AA is caught by the per-event trigger rule, which is a
	// separate check; that path is not exercised here.)
}

// TestImageSMaskTransparency1b ensures an image soft mask is flagged at 1b
// (audit C11).
func TestImageSMaskTransparency1b(t *testing.T) {
	img := &Stream{Dict: Dictionary{}}
	img.Dict.Set("Subtype", Name("Image"))
	img.Dict.Set("SMask", IndirectRef{Number: 5})
	xobj := &Dictionary{}
	xobj.Set("Im0", IndirectRef{Number: 4})
	res := &Dictionary{}
	res.Set("XObject", xobj)
	page := &Dictionary{}
	page.Set("Type", Name("Page"))
	page.Set("Resources", res)
	doc := pageDoc(page)
	doc.Objects[4] = &IndirectObject{Number: 4, Value: img}
	doc.Objects[5] = &IndirectObject{Number: 5, Value: &Stream{Dict: Dictionary{}}}

	if got := countRule(ValidatePDFABytes(doc, PDFA1b, nil), "6.4"); got == 0 {
		t.Errorf("image /SMask was not flagged as transparency at 1b")
	}
	// Not applicable at 2b.
	if got := countRule(ValidatePDFABytes(doc, PDFA2b, nil), "6.4"); got != 0 {
		t.Errorf("2b must not use the 1b transparency rule, got %d", got)
	}
}
