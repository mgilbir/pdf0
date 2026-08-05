package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestUAReferenceXObjects flags a Form XObject with a /Ref entry.
func TestUAReferenceXObjects(t *testing.T) {
	mk := func(withRef bool) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		s := &object.Stream{Dict: object.Dictionary{}}
		s.Dict.Set("Type", object.Name("XObject"))
		s.Dict.Set("Subtype", object.Name("Form"))
		if withRef {
			s.Dict.Set("Ref", &object.Dictionary{})
		}
		doc.Objects[7] = &object.IndirectObject{Number: 7, Value: s}
		return doc
	}
	if len(checkUAReferenceXObjects(mk(true))) == 0 {
		t.Error("reference XObject not flagged")
	}
	if len(checkUAReferenceXObjects(mk(false))) != 0 {
		t.Error("plain Form XObject wrongly flagged")
	}
}
