package pdfa

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// cidSetDoc is a PDF/A-1b page showing CID 1 of an Identity-H font over
// cidMapProgram, whose CIDToGIDMap is cgm and whose FontDescriptor's CIDSet
// lists exactly the CIDs in listed.
func cidSetDoc(t *testing.T, cgm object.Object, listed ...int) core.View {
	t.Helper()
	doc := mkPDFAViewT(t, PDFA1b)
	page := addTestPage(doc)
	set := make([]byte, 1)
	for _, cid := range listed {
		set[cid/8] |= 0x80 >> (cid % 8)
	}
	doc.Objects[40] = &object.IndirectObject{Number: 40, Value: object.NewStream(&object.Dictionary{}, set)}
	doc.Objects[41] = &object.IndirectObject{Number: 41, Value: object.NewStream(&object.Dictionary{}, cidMapProgram())}
	fd := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("FontDescriptor")},
		object.Entry{Key: "FontName", Value: object.Name("ABCDEF+Test")},
		object.Entry{Key: "Flags", Value: object.Integer(4)},
		object.Entry{Key: "FontFile2", Value: object.IndirectRef{Number: 41}},
		object.Entry{Key: "CIDSet", Value: object.IndirectRef{Number: 40}},
	)
	desc := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Font")},
		object.Entry{Key: "Subtype", Value: object.Name("CIDFontType2")},
		object.Entry{Key: "BaseFont", Value: object.Name("ABCDEF+Test")},
		object.Entry{Key: "FontDescriptor", Value: fd},
		object.Entry{Key: "CIDToGIDMap", Value: cgm},
	)
	doc.Objects[42] = &object.IndirectObject{Number: 42, Value: desc}
	font := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Font")},
		object.Entry{Key: "Subtype", Value: object.Name("Type0")},
		object.Entry{Key: "BaseFont", Value: object.Name("ABCDEF+Test")},
		object.Entry{Key: "Encoding", Value: object.Name("Identity-H")},
		object.Entry{Key: "DescendantFonts", Value: object.Array{object.IndirectRef{Number: 42}}},
	)
	doc.Objects[43] = &object.IndirectObject{Number: 43, Value: font}
	doc.Objects[44] = &object.IndirectObject{Number: 44, Value: object.NewStream(&object.Dictionary{}, []byte("BT /F1 12 Tf <0001> Tj ET"))}
	page.Set("Resources", dictWith("Font", dictWith("F1", object.IndirectRef{Number: 43})))
	page.Set("Contents", object.IndirectRef{Number: 44})
	return doc
}

// TestCIDSetCompletenessFollowsTheCIDToGIDMap: at PDF/A-1 a CIDFont subset's
// CIDSet lists every CID whose glyph the program has (ISO 19005-1 6.3.5). For a
// CIDFontType2 with a stream CIDToGIDMap that is the CIDs the map sends to a
// glyph with an outline — the same selection the glyph checks use. The check
// used to decline every stream-mapped font, since it could only equate CIDs
// with glyph indices (a sibling of audit 2026-09-22 C68).
func TestCIDSetCompletenessFollowsTheCIDToGIDMap(t *testing.T) {
	const msg = "CIDSet does not list all glyphs present in the embedded font program"
	has := func(vs []Violation) bool {
		for _, v := range vs {
			if v.Message == msg {
				return true
			}
		}
		return false
	}
	// The map sends CID 1 to glyph 3, which has an outline, and nothing else
	// to a glyph: CID 1 is the one CID with a present glyph.
	mapped := func() object.Object { return cidToGIDStream(map[int]int{1: 3}) }
	if got := checkCIDSetProgramComplete(cidSetDoc(t, mapped(), 3), PDFA1b); !has(got) {
		t.Errorf("a CIDSet listing CID 3, when the map gives only CID 1 a glyph, was not reported: %v", got)
	}
	if got := checkCIDSetProgramComplete(cidSetDoc(t, mapped(), 1), PDFA1b); has(got) {
		t.Errorf("a CIDSet listing exactly the mapped CID was reported: %v", got)
	}
	// Identity: glyphs 1 and 3 have outlines, so CIDs 1 and 3 must be listed.
	if got := checkCIDSetProgramComplete(cidSetDoc(t, object.Name("Identity"), 1), PDFA1b); !has(got) {
		t.Errorf("an Identity font's CIDSet missing CID 3 was not reported: %v", got)
	}
	if got := checkCIDSetProgramComplete(cidSetDoc(t, object.Name("Identity"), 1, 3), PDFA1b); has(got) {
		t.Errorf("an Identity font's complete CIDSet was reported: %v", got)
	}
}
