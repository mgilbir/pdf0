package pdf0

import "testing"

// TestUACMaps covers the 7.21.3.3 predefined/embedded CMap rule.
func TestUACMaps(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}}
	type0 := func(enc Object) *Dictionary {
		f := &Dictionary{}
		f.Set("Subtype", Name("Type0"))
		f.Set("Encoding", enc)
		return f
	}
	// A predefined CMap name is fine.
	if v := doc.checkOneUACMap(type0(Name("Identity-H"))); len(v) != 0 {
		t.Errorf("Identity-H wrongly flagged: %v", v)
	}
	// A non-predefined name is flagged.
	if v := doc.checkOneUACMap(type0(Name("Adobe-Korea1-2"))); len(v) == 0 {
		t.Error("non-predefined CMap name not flagged")
	}
	// An embedded CMap whose /UseCMap is non-predefined is flagged.
	badUse := &Stream{Dict: Dictionary{}}
	badUse.Dict.Set("UseCMap", Name("Adobe-Korea1-2"))
	if v := doc.checkOneUACMap(type0(badUse)); len(v) == 0 {
		t.Error("embedded CMap with non-predefined /UseCMap not flagged")
	}
	// An embedded CMap with a predefined /UseCMap is fine.
	goodUse := &Stream{Dict: Dictionary{}}
	goodUse.Dict.Set("UseCMap", Name("Identity-H"))
	if v := doc.checkOneUACMap(type0(goodUse)); len(v) != 0 {
		t.Errorf("embedded CMap with predefined /UseCMap wrongly flagged: %v", v)
	}
	// A simple font is out of scope.
	simple := &Dictionary{}
	simple.Set("Subtype", Name("Type1"))
	if v := doc.checkOneUACMap(simple); len(v) != 0 {
		t.Errorf("simple font wrongly flagged: %v", v)
	}
}
