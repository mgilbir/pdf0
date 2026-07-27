package pdf0

import (
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
	doc := &Document{Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
	set := func(num int, v Object) IndirectRef {
		doc.Objects[num] = &IndirectObject{Number: num, Value: v}
		return IndirectRef{Number: num}
	}
	fnDict := Dictionary{}
	fnDict.Set("FunctionType", Integer(4))
	fnDict.Set("Domain", Array{Integer(0), Integer(1)})
	fnDict.Set("Range", Array{Integer(0), Integer(1), Integer(0), Integer(1), Integer(0), Integer(1)})
	fnDict.Set("Length", Integer(len(prog)))
	fnRef := set(4, &Stream{Dict: fnDict, Data: []byte(prog)})

	imgDict := Dictionary{}
	imgDict.Set("Type", Name("XObject"))
	imgDict.Set("Subtype", Name("Image"))
	imgDict.Set("Width", Integer(w))
	imgDict.Set("Height", Integer(h))
	imgDict.Set("BitsPerComponent", Integer(8))
	imgDict.Set("ColorSpace", Array{Name("Separation"), Name("Spot"), Name("DeviceRGB"), fnRef})
	imgDict.Set("Length", Integer(w * h))
	imgRef := set(5, &Stream{Dict: imgDict, Data: make([]byte, w*h)})

	xobj := &Dictionary{}
	xobj.Set("Im0", imgRef)
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
