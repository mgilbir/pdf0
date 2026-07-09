package pdf0

import "testing"

// TestUAToUnicodeForbidden verifies the ToUnicode-value scan the 7.21.7 UA rule
// relies on: a mapping to U+0000/FEFF/FFFE is rejected, a normal one accepted.
func TestUAToUnicodeForbidden(t *testing.T) {
	mk := func(dst string) *Stream {
		body := "/CIDInit /ProcSet findresource begin 12 dict begin begincmap\n" +
			"1 beginbfchar\n<0041> <" + dst + ">\nendbfchar\nendcmap end end"
		return &Stream{Dict: Dictionary{}, Data: []byte(body)}
	}
	doc := &Document{Objects: map[int]*IndirectObject{}}
	if !hasForbiddenUnicodeTargets(doc, mk("0000")) {
		t.Error("mapping to U+0000 not detected")
	}
	if !hasForbiddenUnicodeTargets(doc, mk("FEFF")) {
		t.Error("mapping to U+FEFF not detected")
	}
	if hasForbiddenUnicodeTargets(doc, mk("0041")) {
		t.Error("mapping to U+0041 wrongly rejected")
	}
}
