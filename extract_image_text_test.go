package pdf0

import (
	"strings"
	"testing"
)

// TestExtractTextRecursesIntoForm is the C28 guard: text drawn inside a form
// XObject invoked with Do is included in the extracted text.
func TestExtractTextRecursesIntoForm(t *testing.T) {
	doc := &Document{Version: "2.0", Objects: map[int]*IndirectObject{}}
	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("Pages", IndirectRef{Number: 2})
	pages := &Dictionary{}
	pages.Set("Type", Name("Pages"))
	pages.Set("Kids", Array{IndirectRef{Number: 3}})
	pages.Set("Count", Integer(1))
	page := &Dictionary{}
	page.Set("Type", Name("Page"))
	page.Set("Parent", IndirectRef{Number: 2})
	pres := &Dictionary{}
	xobj := &Dictionary{}
	xobj.Set("Fm0", IndirectRef{Number: 4})
	pres.Set("XObject", xobj)
	page.Set("Resources", pres)
	page.Set("Contents", IndirectRef{Number: 5})

	form := &Stream{Data: []byte("BT (FormHello) Tj ET")}
	form.Dict.Set("Type", Name("XObject"))
	form.Dict.Set("Subtype", Name("Form"))
	form.Dict.Set("Length", Integer(len(form.Data)))
	contents := &Stream{Data: []byte("/Fm0 Do")}
	contents.Dict.Set("Length", Integer(len(contents.Data)))

	doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
	doc.Objects[2] = &IndirectObject{Number: 2, Value: pages}
	doc.Objects[3] = &IndirectObject{Number: 3, Value: page}
	doc.Objects[4] = &IndirectObject{Number: 4, Value: form}
	doc.Objects[5] = &IndirectObject{Number: 5, Value: contents}
	doc.Trailer = Dictionary{}
	doc.Trailer.Set("Root", IndirectRef{Number: 1})

	if text := doc.ExtractText(); !strings.Contains(text, "FormHello") {
		t.Fatalf("text inside a form XObject was not extracted: %q", text)
	}
}

// TestExtractTextInheritedResources is the C24 guard: a page whose /Resources are
// inherited from the /Pages parent must still resolve its fonts, so a font's
// ToUnicode mapping is applied rather than the text degrading to a raw fallback.
func TestExtractTextInheritedResources(t *testing.T) {
	doc := &Document{Version: "2.0", Objects: map[int]*IndirectObject{}}
	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("Pages", IndirectRef{Number: 2})

	// /Resources live on the parent /Pages node, not the page.
	pages := &Dictionary{}
	pages.Set("Type", Name("Pages"))
	pages.Set("Kids", Array{IndirectRef{Number: 3}})
	pages.Set("Count", Integer(1))
	pres := &Dictionary{}
	fontDict := &Dictionary{}
	fontDict.Set("F0", IndirectRef{Number: 4})
	pres.Set("Font", fontDict)
	pages.Set("Resources", pres)

	page := &Dictionary{}
	page.Set("Type", Name("Page"))
	page.Set("Parent", IndirectRef{Number: 2})
	page.Set("Contents", IndirectRef{Number: 6}) // no /Resources of its own

	// Font F0 with a ToUnicode CMap mapping byte 0x41 ('A') to U+0058 ('X').
	font := &Dictionary{}
	font.Set("Type", Name("Font"))
	font.Set("Subtype", Name("Type1"))
	font.Set("ToUnicode", IndirectRef{Number: 5})
	cmap := "/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n" +
		"1 beginbfchar\n<41> <0058>\nendbfchar\nendcmap\nend\nend"
	tounicode := &Stream{Data: []byte(cmap)}
	tounicode.Dict.Set("Length", Integer(len(cmap)))

	contents := &Stream{Data: []byte("BT /F0 12 Tf (A) Tj ET")}
	contents.Dict.Set("Length", Integer(len(contents.Data)))

	doc.Objects[1] = &IndirectObject{Number: 1, Value: cat}
	doc.Objects[2] = &IndirectObject{Number: 2, Value: pages}
	doc.Objects[3] = &IndirectObject{Number: 3, Value: page}
	doc.Objects[4] = &IndirectObject{Number: 4, Value: font}
	doc.Objects[5] = &IndirectObject{Number: 5, Value: tounicode}
	doc.Objects[6] = &IndirectObject{Number: 6, Value: contents}
	doc.Trailer = Dictionary{}
	doc.Trailer.Set("Root", IndirectRef{Number: 1})

	text := doc.ExtractText()
	if !strings.Contains(text, "X") {
		t.Fatalf("inherited-font ToUnicode mapping not applied: %q (expected the mapped 'X')", text)
	}
}

// TestBilevelDecodeInversion is the C30 guard: a CCITT/JBIG2 (bilevel) image's
// /Decode array is honored. Without /Decode the fast path renders sample bit 1 as
// white; with /Decode [1 0] the polarity inverts to black — which the codec
// branches previously ignored.
func TestBilevelDecodeInversion(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}}
	mk := func(decode Object) *Stream {
		st := &Stream{}
		st.Dict.Set("Width", Integer(1))
		st.Dict.Set("Height", Integer(1))
		st.Dict.Set("BitsPerComponent", Integer(1))
		st.Dict.Set("ColorSpace", Name("DeviceGray"))
		if decode != nil {
			st.Dict.Set("Decode", decode)
		}
		return st
	}
	pixel := func(st *Stream) uint32 {
		img := &ExtractedImage{Width: 1, Height: 1, ColorSpace: "DeviceGray", BitsPerComponent: 1}
		doc.renderBilevelSamples(st, img, []byte{0x80}, "unsupported") // sample bit = 1
		if !img.Decoded || img.Image == nil {
			t.Fatal("bilevel samples should decode")
		}
		r, _, _, _ := img.Image.At(0, 0).RGBA()
		return r >> 8
	}

	if got := pixel(mk(nil)); got != 255 {
		t.Errorf("plain bilevel: pixel = %d, want 255 (white)", got)
	}
	if got := pixel(mk(Array{Integer(1), Integer(0)})); got != 0 {
		t.Errorf("/Decode [1 0]: pixel = %d, want 0 (inverted to black)", got)
	}
}
