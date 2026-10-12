package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestUAHeadingSkip flags a skipped heading level and accepts a proper sequence.
func TestUAHeadingSkip(t *testing.T) {
	mk := func(levels ...string) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, nil)
		cat := &object.Dictionary{}
		cat.Set("Type", object.Name("Catalog"))
		root := object.NewDictionary(object.Entry{Key: "Type", Value: object.Name("StructTreeRoot")})
		var kids object.Array
		for i, lvl := range levels {
			h := &object.Dictionary{}
			h.Set("S", object.Name(lvl))
			doc.Objects[10+i] = &object.IndirectObject{Number: 10 + i, Value: h}
			kids = append(kids, object.IndirectRef{Number: 10 + i})
		}
		root.Set("K", kids)
		doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
		doc.Objects[2] = &object.IndirectObject{Number: 2, Value: root}
		cat.Set("StructTreeRoot", object.IndirectRef{Number: 2})
		doc.Trailer.Set("Root", object.IndirectRef{Number: 1})
		return doc
	}
	// ISO 14289-1 puts the skip in 7.4.2's bullets, as veraPDF (7.4.2-t01)
	// and the corpus's 7.4.2-t01-fail-b do.
	skips := func(doc core.View) []string {
		return findings(checkUAHeadings(doc, doc.ResolveDict(doc.Trailer.Get("Root"))), "7.4.2", "skipping a level")
	}
	if len(skips(mk("H1", "H3"))) == 0 {
		t.Error("H1→H3 skip not flagged under 7.4.2")
	}
	if got := skips(mk("H1", "H2", "H3", "H2", "H1")); len(got) != 0 {
		t.Errorf("a valid heading sequence was flagged: %v", got)
	}
}
