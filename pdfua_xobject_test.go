package pdf0

import "testing"

// TestUAReferenceXObjects flags a Form XObject with a /Ref entry.
func TestUAReferenceXObjects(t *testing.T) {
	mk := func(withRef bool) *Document {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		s := &Stream{Dict: Dictionary{}}
		s.Dict.Set("Type", Name("XObject"))
		s.Dict.Set("Subtype", Name("Form"))
		if withRef {
			s.Dict.Set("Ref", &Dictionary{})
		}
		doc.Objects[7] = &IndirectObject{Number: 7, Value: s}
		return doc
	}
	if len(mk(true).checkUAReferenceXObjects()) == 0 {
		t.Error("reference XObject not flagged")
	}
	if len(mk(false).checkUAReferenceXObjects()) != 0 {
		t.Error("plain Form XObject wrongly flagged")
	}
}
