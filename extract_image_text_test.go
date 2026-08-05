package pdf0

import (
	"github.com/mgilbir/pdf0/object"
	"strings"
	"testing"
)

// TestExtractTextRecursesIntoForm is the C28 guard: text drawn inside a form
// XObject invoked with Do is included in the extracted text.
func TestExtractTextRecursesIntoForm(t *testing.T) {
	doc := &Document{Version: "2.0", Objects: map[int]*object.IndirectObject{}}
	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("Pages", object.IndirectRef{Number: 2})
	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{object.IndirectRef{Number: 3}})
	pages.Set("Count", object.Integer(1))
	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", object.IndirectRef{Number: 2})
	pres := &object.Dictionary{}
	xobj := &object.Dictionary{}
	xobj.Set("Fm0", object.IndirectRef{Number: 4})
	pres.Set("XObject", xobj)
	page.Set("Resources", pres)
	page.Set("Contents", object.IndirectRef{Number: 5})

	form := &object.Stream{Data: []byte("BT (FormHello) Tj ET")}
	form.Dict.Set("Type", object.Name("XObject"))
	form.Dict.Set("Subtype", object.Name("Form"))
	form.Dict.Set("Length", object.Integer(len(form.Data)))
	contents := &object.Stream{Data: []byte("/Fm0 Do")}
	contents.Dict.Set("Length", object.Integer(len(contents.Data)))

	doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
	doc.Objects[2] = &object.IndirectObject{Number: 2, Value: pages}
	doc.Objects[3] = &object.IndirectObject{Number: 3, Value: page}
	doc.Objects[4] = &object.IndirectObject{Number: 4, Value: form}
	doc.Objects[5] = &object.IndirectObject{Number: 5, Value: contents}
	doc.Trailer = object.Dictionary{}
	doc.Trailer.Set("Root", object.IndirectRef{Number: 1})

	if text := doc.ExtractText(); !strings.Contains(text, "FormHello") {
		t.Fatalf("text inside a form XObject was not extracted: %q", text)
	}
}

// TestExtractTextInheritedResources is the C24 guard: a page whose /Resources are
// inherited from the /Pages parent must still resolve its fonts, so a font's
// ToUnicode mapping is applied rather than the text degrading to a raw fallback.
func TestExtractTextInheritedResources(t *testing.T) {
	doc := &Document{Version: "2.0", Objects: map[int]*object.IndirectObject{}}
	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("Pages", object.IndirectRef{Number: 2})

	// /Resources live on the parent /Pages node, not the page.
	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{object.IndirectRef{Number: 3}})
	pages.Set("Count", object.Integer(1))
	pres := &object.Dictionary{}
	fontDict := &object.Dictionary{}
	fontDict.Set("F0", object.IndirectRef{Number: 4})
	pres.Set("Font", fontDict)
	pages.Set("Resources", pres)

	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", object.IndirectRef{Number: 2})
	page.Set("Contents", object.IndirectRef{Number: 6}) // no /Resources of its own

	// Font F0 with a ToUnicode CMap mapping byte 0x41 ('A') to U+0058 ('X').
	font := &object.Dictionary{}
	font.Set("Type", object.Name("Font"))
	font.Set("Subtype", object.Name("Type1"))
	font.Set("ToUnicode", object.IndirectRef{Number: 5})
	cmap := "/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n" +
		"1 beginbfchar\n<41> <0058>\nendbfchar\nendcmap\nend\nend"
	tounicode := &object.Stream{Data: []byte(cmap)}
	tounicode.Dict.Set("Length", object.Integer(len(cmap)))

	contents := &object.Stream{Data: []byte("BT /F0 12 Tf (A) Tj ET")}
	contents.Dict.Set("Length", object.Integer(len(contents.Data)))

	doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
	doc.Objects[2] = &object.IndirectObject{Number: 2, Value: pages}
	doc.Objects[3] = &object.IndirectObject{Number: 3, Value: page}
	doc.Objects[4] = &object.IndirectObject{Number: 4, Value: font}
	doc.Objects[5] = &object.IndirectObject{Number: 5, Value: tounicode}
	doc.Objects[6] = &object.IndirectObject{Number: 6, Value: contents}
	doc.Trailer = object.Dictionary{}
	doc.Trailer.Set("Root", object.IndirectRef{Number: 1})

	text := doc.ExtractText()
	if !strings.Contains(text, "X") {
		t.Fatalf("inherited-font ToUnicode mapping not applied: %q (expected the mapped 'X')", text)
	}
}

// TestBilevelDecodeInversion is the C30 guard: a CCITT/JBIG2 (bilevel) image's
// /Decode array is honored. Without /Decode the fast path renders sample bit 1 as
// white; with /Decode [1 0] the polarity inverts to black — which the codec
// branches previously ignored.
