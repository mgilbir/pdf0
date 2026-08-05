package pdf0

import (
	"github.com/mgilbir/pdf0/object"
	"strings"
	"testing"
	"time"
)

// TestExtractImagesTintFunctionCached: a Separation image evaluates its tint
// transform once per pixel. Without the per-run cache each evaluation
// re-decoded and re-parsed the type-4 program stream, so a small image took
// minutes (Common Crawl sweep #13: 60s+ on sub-megabyte files). The program
// below parses ~40k tokens; per-pixel re-parsing would take minutes, the
// cached run must finish comfortably under the bound.
func TestExtractImagesTintFunctionCached(t *testing.T) {
	// { pop 0 0 1 { ...padding... } pop } — pops the tint value, pushes the
	// RGB output, then pushes and drops a large never-executed procedure whose
	// only cost is parsing.
	var b strings.Builder
	b.WriteString("{ pop 0 0 1 {")
	for i := 0; i < 20000; i++ {
		b.WriteString(" 0")
	}
	b.WriteString(" } pop }")
	prog := b.String()

	const w, h = 300, 300
	doc := &Document{Objects: map[int]*object.IndirectObject{}, Trailer: object.Dictionary{}}
	set := func(num int, v object.Object) object.IndirectRef {
		doc.Objects[num] = &object.IndirectObject{Number: num, Value: v}
		return object.IndirectRef{Number: num}
	}
	fnDict := object.Dictionary{}
	fnDict.Set("FunctionType", object.Integer(4))
	fnDict.Set("Domain", object.Array{object.Integer(0), object.Integer(1)})
	fnDict.Set("Range", object.Array{object.Integer(0), object.Integer(1), object.Integer(0), object.Integer(1), object.Integer(0), object.Integer(1)})
	fnDict.Set("Length", object.Integer(len(prog)))
	fnRef := set(4, &object.Stream{Dict: fnDict, Data: []byte(prog)})

	imgDict := object.Dictionary{}
	imgDict.Set("Type", object.Name("XObject"))
	imgDict.Set("Subtype", object.Name("Image"))
	imgDict.Set("Width", object.Integer(w))
	imgDict.Set("Height", object.Integer(h))
	imgDict.Set("BitsPerComponent", object.Integer(8))
	imgDict.Set("ColorSpace", object.Array{object.Name("Separation"), object.Name("Spot"), object.Name("DeviceRGB"), fnRef})
	imgDict.Set("Length", object.Integer(w*h))
	imgRef := set(5, &object.Stream{Dict: imgDict, Data: make([]byte, w*h)})

	xobj := &object.Dictionary{}
	xobj.Set("Im0", imgRef)
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
	catRef := set(1, cat)
	doc.Trailer.Set("Root", catRef)

	start := time.Now()
	imgs := doc.ExtractImages()
	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("ExtractImages took %v on a %dx%d Separation image; the tint program is being re-parsed per pixel", d, w, h)
	}
	if len(imgs) != 1 || !imgs[0].Decoded {
		t.Fatalf("image not decoded: %+v", imgs)
	}
}
