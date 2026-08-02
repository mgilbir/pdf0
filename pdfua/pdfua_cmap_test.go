package pdfua

import (
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestUACMaps covers the 7.21.3.3 predefined/embedded CMap rule.
func TestUACMaps(t *testing.T) {
	doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	type0 := func(enc object.Object) *object.Dictionary {
		f := &object.Dictionary{}
		f.Set("Subtype", object.Name("Type0"))
		f.Set("Encoding", enc)
		return f
	}
	// A predefined CMap name is fine.
	if v := checkOneUACMap(doc, type0(object.Name("Identity-H"))); len(v) != 0 {
		t.Errorf("Identity-H wrongly flagged: %v", v)
	}
	// A non-predefined name is flagged.
	if v := checkOneUACMap(doc, type0(object.Name("Adobe-Korea1-2"))); len(v) == 0 {
		t.Error("non-predefined CMap name not flagged")
	}
	// An embedded CMap whose /UseCMap is non-predefined is flagged.
	badUse := &object.Stream{Dict: object.Dictionary{}}
	badUse.Dict.Set("UseCMap", object.Name("Adobe-Korea1-2"))
	if v := checkOneUACMap(doc, type0(badUse)); len(v) == 0 {
		t.Error("embedded CMap with non-predefined /UseCMap not flagged")
	}
	// An embedded CMap with a predefined /UseCMap is fine.
	goodUse := &object.Stream{Dict: object.Dictionary{}}
	goodUse.Dict.Set("UseCMap", object.Name("Identity-H"))
	if v := checkOneUACMap(doc, type0(goodUse)); len(v) != 0 {
		t.Errorf("embedded CMap with predefined /UseCMap wrongly flagged: %v", v)
	}
	// A simple font is out of scope.
	simple := &object.Dictionary{}
	simple.Set("Subtype", object.Name("Type1"))
	if v := checkOneUACMap(doc, simple); len(v) != 0 {
		t.Errorf("simple font wrongly flagged: %v", v)
	}
}
