package pdf0

import (
	"testing"
	"time"
)

// buildTwoImageDoc returns a document with two image XObjects: a trivial 1x1
// gray image and the expensive tint-transform image from the DoS test (large
// only in decode work, not bytes).
func buildTwoImageDoc(expensiveProg string, w, h int) *Document {
	doc := &Document{Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
	set := func(num int, v Object) IndirectRef {
		doc.Objects[num] = &IndirectObject{Number: num, Value: v}
		return IndirectRef{Number: num}
	}
	small := Dictionary{}
	small.Set("Type", Name("XObject"))
	small.Set("Subtype", Name("Image"))
	small.Set("Width", Integer(1))
	small.Set("Height", Integer(1))
	small.Set("BitsPerComponent", Integer(8))
	small.Set("ColorSpace", Name("DeviceGray"))
	small.Set("Length", Integer(1))
	smallRef := set(6, &Stream{Dict: small, Data: []byte{0x80}})

	fnDict := Dictionary{}
	fnDict.Set("FunctionType", Integer(4))
	fnDict.Set("Domain", Array{Integer(0), Integer(1)})
	fnDict.Set("Range", Array{Integer(0), Integer(1), Integer(0), Integer(1), Integer(0), Integer(1)})
	fnDict.Set("Length", Integer(len(expensiveProg)))
	fnRef := set(4, &Stream{Dict: fnDict, Data: []byte(expensiveProg)})
	big := Dictionary{}
	big.Set("Type", Name("XObject"))
	big.Set("Subtype", Name("Image"))
	big.Set("Width", Integer(w))
	big.Set("Height", Integer(h))
	big.Set("BitsPerComponent", Integer(8))
	big.Set("ColorSpace", Array{Name("Separation"), Name("Spot"), Name("DeviceRGB"), fnRef})
	big.Set("Length", Integer(w*h))
	bigRef := set(5, &Stream{Dict: big, Data: make([]byte, w*h)})

	xobj := &Dictionary{}
	xobj.Set("ImA", smallRef)
	xobj.Set("ImB", bigRef)
	res := &Dictionary{}
	res.Set("XObject", xobj)
	page := &Dictionary{}
	page.Set("Type", Name("Page"))
	page.Set("Resources", res)
	pageRef := set(3, page)
	pages := &Dictionary{}
	pages.Set("Type", Name("Pages"))
	pages.Set("Kids", Array{pageRef})
	pages.Set("Count", Integer(1))
	pagesRef := set(2, pages)
	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("Pages", pagesRef)
	doc.Trailer.Set("Root", set(1, cat))
	return doc
}

// TestImagesIteratorMatchesExtract: full iteration yields the same images, in
// the same order, as ExtractImages.
func TestImagesIteratorMatchesExtract(t *testing.T) {
	doc := buildTwoImageDoc("{ pop 0 0 1 }", 4, 4)
	eager := doc.ExtractImages()
	var lazy []ExtractedImage
	for im := range doc.Images() {
		lazy = append(lazy, im)
	}
	if len(eager) != 2 || len(lazy) != 2 {
		t.Fatalf("expected 2 images each: eager=%d lazy=%d", len(eager), len(lazy))
	}
	for i := range eager {
		if eager[i].ObjNum != lazy[i].ObjNum || eager[i].Decoded != lazy[i].Decoded {
			t.Errorf("image %d differs: eager=%+v lazy=%+v", i, eager[i], lazy[i])
		}
	}
}

// TestImagesIteratorLazy: breaking after the first image must skip the second
// image's decode work entirely. The second image carries an expensive (but
// cache-defeating: the run cache is per-walk, the per-pixel exec dominates)
// tint program that takes far longer than the bound if decoded.
func TestImagesIteratorLazy(t *testing.T) {
	// A ~40k-operator program EXECUTED per pixel of a 200x200 image if the
	// image is decoded: ~1.6G psExec steps, well over the bound. Breaking
	// early must avoid all of it. (Stays under maxPSSteps per evaluation.)
	var b []byte
	b = append(b, "{ pop 0 0 1"...)
	for i := 0; i < 20000; i++ {
		b = append(b, " 0 pop"...)
	}
	b = append(b, " }"...)
	doc := buildTwoImageDoc(string(b), 200, 200)

	start := time.Now()
	got := 0
	for im := range doc.Images() {
		got++
		if im.ObjNum != 6 {
			t.Fatalf("first yielded image is %d, want the cheap image 6", im.ObjNum)
		}
		break
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("early break still took %v; the remaining image was decoded eagerly", d)
	}
	if got != 1 {
		t.Errorf("yielded %d images before break, want 1", got)
	}
}
