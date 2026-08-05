package pdf0

import (
	"github.com/mgilbir/pdf0/images"
	"github.com/mgilbir/pdf0/object"
	"testing"
	"time"
)

// buildTwoImageDoc returns a document with two image XObjects: a trivial 1x1
// gray image and the expensive tint-transform image from the DoS test (large
// only in decode work, not bytes).
func buildTwoImageDoc(expensiveProg string, w, h int) *Document {
	doc := &Document{Objects: map[int]*object.IndirectObject{}, Trailer: object.Dictionary{}}
	set := func(num int, v object.Object) object.IndirectRef {
		doc.Objects[num] = &object.IndirectObject{Number: num, Value: v}
		return object.IndirectRef{Number: num}
	}
	small := object.Dictionary{}
	small.Set("Type", object.Name("XObject"))
	small.Set("Subtype", object.Name("Image"))
	small.Set("Width", object.Integer(1))
	small.Set("Height", object.Integer(1))
	small.Set("BitsPerComponent", object.Integer(8))
	small.Set("ColorSpace", object.Name("DeviceGray"))
	small.Set("Length", object.Integer(1))
	smallRef := set(6, &object.Stream{Dict: small, Data: []byte{0x80}})

	fnDict := object.Dictionary{}
	fnDict.Set("FunctionType", object.Integer(4))
	fnDict.Set("Domain", object.Array{object.Integer(0), object.Integer(1)})
	fnDict.Set("Range", object.Array{object.Integer(0), object.Integer(1), object.Integer(0), object.Integer(1), object.Integer(0), object.Integer(1)})
	fnDict.Set("Length", object.Integer(len(expensiveProg)))
	fnRef := set(4, &object.Stream{Dict: fnDict, Data: []byte(expensiveProg)})
	big := object.Dictionary{}
	big.Set("Type", object.Name("XObject"))
	big.Set("Subtype", object.Name("Image"))
	big.Set("Width", object.Integer(w))
	big.Set("Height", object.Integer(h))
	big.Set("BitsPerComponent", object.Integer(8))
	big.Set("ColorSpace", object.Array{object.Name("Separation"), object.Name("Spot"), object.Name("DeviceRGB"), fnRef})
	big.Set("Length", object.Integer(w*h))
	bigRef := set(5, &object.Stream{Dict: big, Data: make([]byte, w*h)})

	xobj := &object.Dictionary{}
	xobj.Set("ImA", smallRef)
	xobj.Set("ImB", bigRef)
	res := &object.Dictionary{}
	res.Set("XObject", xobj)
	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Resources", res)
	pageRef := set(3, page)
	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{pageRef})
	pages.Set("Count", object.Integer(1))
	pagesRef := set(2, pages)
	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("Pages", pagesRef)
	doc.Trailer.Set("Root", set(1, cat))
	return doc
}

// TestImagesIteratorMatchesExtract: full iteration yields the same images, in
// the same order, as ExtractImages.
func TestImagesIteratorMatchesExtract(t *testing.T) {
	doc := buildTwoImageDoc("{ pop 0 0 1 }", 4, 4)
	eager := doc.ExtractImages()
	var lazy []images.ExtractedImage
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
	// early must avoid all of it. (Stays under the PostScript step budget per evaluation.)
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
