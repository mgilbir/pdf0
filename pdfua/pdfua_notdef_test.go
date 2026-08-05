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
