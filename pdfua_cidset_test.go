package pdf0

import "testing"

// TestMarkComposite verifies that a composite glyph's component indices are
// recorded, and that a simple glyph records nothing.
func TestMarkComposite(t *testing.T) {
	// Composite glyph: numberOfContours = -1 (0xFFFF), 8-byte bbox, then one
	// component with ARG_1_AND_2_ARE_WORDS set (flags 0x0001), glyphIndex 42,
	// two 2-byte args, and no MORE_COMPONENTS.
	composite := []byte{
		0xFF, 0xFF, // numberOfContours = -1
		0, 0, 0, 0, 0, 0, 0, 0, // bbox
		0x00, 0x01, // flags: ARG_1_AND_2_ARE_WORDS, no MORE_COMPONENTS
		0x00, 0x2A, // component glyphIndex = 42
		0x00, 0x00, 0x00, 0x00, // arg1, arg2 (words)
	}
	out := make([]bool, 100)
	markComposite(composite, 100, out)
	if !out[42] {
		t.Error("composite component glyph 42 not recorded")
	}
	if out[0] || out[1] {
		t.Error("unexpected component recorded")
	}

	// A simple glyph (numberOfContours >= 0) records nothing.
	simple := []byte{0x00, 0x02, 0, 0, 0, 0, 0, 0, 0, 0}
	out2 := make([]bool, 100)
	markComposite(simple, 100, out2)
	for i, b := range out2 {
		if b {
			t.Errorf("simple glyph wrongly recorded component %d", i)
		}
	}
}

// TestUACIDFontCIDSetNonSubset confirms a non-subset font is out of scope.
func TestUACIDFontCIDSetNonSubset(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}}
	f := &Dictionary{}
	f.Set("Subtype", Name("Type0"))
	f.Set("BaseFont", Name("Arial")) // no subset tag
	doc.Objects[10] = &IndirectObject{Number: 10, Value: f}
	if isSubsetFont(f) {
		t.Fatal("Arial should not be a subset font")
	}
	if v := doc.checkCIDFontCIDSet(f); len(v) != 0 {
		t.Errorf("non-CIDFontType2 font wrongly flagged: %v", v)
	}
}
