package pdfua

import (
	"github.com/mgilbir/pdf0/object"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
)

// TestUAToUnicodeForbidden verifies the ToUnicode-value scan the 7.21.7 UA rule
// relies on: a mapping to U+0000/FEFF/FFFE is rejected, a normal one accepted.
func TestUAToUnicodeForbidden(t *testing.T) {
	mk := func(dst string) *object.Stream {
		body := "/CIDInit /ProcSet findresource begin 12 dict begin begincmap\n" +
			"1 beginbfchar\n<0041> <" + dst + ">\nendbfchar\nendcmap end end"
		return &object.Stream{Dict: object.Dictionary{}, Data: []byte(body)}
	}
	doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	if !core.HasForbiddenUnicodeTargets(doc, mk("0000")) {
		t.Error("mapping to U+0000 not detected")
	}
	if !core.HasForbiddenUnicodeTargets(doc, mk("FEFF")) {
		t.Error("mapping to U+FEFF not detected")
	}
	if core.HasForbiddenUnicodeTargets(doc, mk("0041")) {
		t.Error("mapping to U+0041 wrongly rejected")
	}
}
