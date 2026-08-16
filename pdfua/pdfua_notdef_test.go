package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestUANotdefCID flags a Type0/Identity font that shows CID 0 (.notdef) and
// clears when only non-zero CIDs are shown.
func TestUANotdefCID(t *testing.T) {
	mk := func(content string) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		font := &object.Dictionary{}
		font.Set("Type", object.Name("Font"))
		font.Set("Subtype", object.Name("Type0"))
		font.Set("Encoding", object.Name("Identity-H"))
		fontRes := &object.Dictionary{}
		fontRes.Set("F1", object.IndirectRef{Number: 5})
		res := &object.Dictionary{}
		res.Set("Font", fontRes)
		page := &object.Dictionary{}
		page.Set("Type", object.Name("Page"))
		page.Set("Resources", res)
		page.Set("Contents", object.IndirectRef{Number: 6})
		pages := &object.Dictionary{}
		pages.Set("Type", object.Name("Pages"))
		pages.Set("Kids", object.Array{object.IndirectRef{Number: 3}})
		pages.Set("Count", object.Integer(1))
		cat := &object.Dictionary{}
		cat.Set("Type", object.Name("Catalog"))
		cat.Set("Pages", object.IndirectRef{Number: 2})
		doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
		doc.Objects[2] = &object.IndirectObject{Number: 2, Value: pages}
		doc.Objects[3] = &object.IndirectObject{Number: 3, Value: page}
		doc.Objects[5] = &object.IndirectObject{Number: 5, Value: font}
		doc.Objects[6] = &object.IndirectObject{Number: 6, Value: &object.Stream{Data: []byte(content)}}
		doc.Trailer.Set("Root", object.IndirectRef{Number: 1})
		return doc
	}
	if len(checkUANotdefCID(mk("BT /F1 12 Tf <0000> Tj ET"))) == 0 {
		t.Error("shown CID 0 (.notdef) not flagged")
	}
	if len(checkUANotdefCID(mk("BT /F1 12 Tf <00010002> Tj ET"))) != 0 {
		t.Error("non-zero CIDs wrongly flagged")
	}
}

// TestUANotdefCIDReadsAnEmbeddedCMap: the same rule for a font that carries its
// own CMap rather than using Identity-H.
//
// It was skipped for such a font, which is most of the Type 0 fonts in the
// veraPDF corpus that are not Identity. Reading the CMap makes the check apply,
// and it has to apply *through* the CMap: the byte 0x41 here is one code naming
// CID 0, and nothing about the bytes says so — a checker looking for two zero
// bytes finds nothing to report, and one assuming two-byte codes finds a CID
// that does not exist.
func TestUANotdefCIDReadsAnEmbeddedCMap(t *testing.T) {
	mk := func(content, cmapSrc string) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		enc := &object.Stream{Dict: object.Dictionary{}, Data: []byte(cmapSrc)}
		enc.Dict.Set("Length", object.Integer(len(cmapSrc)))
		font := &object.Dictionary{}
		font.Set("Type", object.Name("Font"))
		font.Set("Subtype", object.Name("Type0"))
		font.Set("Encoding", object.IndirectRef{Number: 7})
		fontRes := &object.Dictionary{}
		fontRes.Set("F1", object.IndirectRef{Number: 5})
		res := &object.Dictionary{}
		res.Set("Font", fontRes)
		page := &object.Dictionary{}
		page.Set("Type", object.Name("Page"))
		page.Set("Resources", res)
		page.Set("Contents", object.IndirectRef{Number: 6})
		pages := &object.Dictionary{}
		pages.Set("Type", object.Name("Pages"))
		pages.Set("Kids", object.Array{object.IndirectRef{Number: 3}})
		pages.Set("Count", object.Integer(1))
		cat := &object.Dictionary{}
		cat.Set("Type", object.Name("Catalog"))
		cat.Set("Pages", object.IndirectRef{Number: 2})
		doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
		doc.Objects[2] = &object.IndirectObject{Number: 2, Value: pages}
		doc.Objects[3] = &object.IndirectObject{Number: 3, Value: page}
		doc.Objects[5] = &object.IndirectObject{Number: 5, Value: font}
		doc.Objects[6] = &object.IndirectObject{Number: 6, Value: &object.Stream{Data: []byte(content)}}
		doc.Objects[7] = &object.IndirectObject{Number: 7, Value: enc}
		doc.Trailer.Set("Root", object.IndirectRef{Number: 1})
		return doc
	}
	// One byte to a code. 0x41 names CID 0; 0x42 names CID 7.
	const cmapSrc = `begincmap
1 begincodespacerange
<00> <FF>
endcodespacerange
2 begincidchar
<41> 0
<42> 7
endcidchar
endcmap`
	if len(checkUANotdefCID(mk("BT /F1 12 Tf <41> Tj ET", cmapSrc))) == 0 {
		t.Error("a one-byte code naming CID 0 was not flagged; the CMap was not read")
	}
	if len(checkUANotdefCID(mk("BT /F1 12 Tf <42> Tj ET", cmapSrc))) != 0 {
		t.Error("a code naming CID 7 was flagged as .notdef")
	}
	// And the pair together: read as Identity-H, <4142> is one code naming CID
	// 16706 and nothing is reported — the false negative this replaces.
	if len(checkUANotdefCID(mk("BT /F1 12 Tf <4142> Tj ET", cmapSrc))) == 0 {
		t.Error("two one-byte codes, the first naming CID 0, were not flagged")
	}
}
