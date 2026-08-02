package pdfua

import (
	"github.com/mgilbir/pdf0/object"
	"testing"
)

func TestIsSubsetFont(t *testing.T) {
	yes := []object.Name{"ABCDEF+Arial", "SJYPRV+Georgia-BoldItalic", "LFTWBJ+Frutiger"}
	no := []object.Name{"Arial", "abcdef+Arial", "ABCDE+Arial", "ABCDEF-Arial", "AB2DEF+Arial", "+Arial", ""}
	mk := func(bf object.Name) *object.Dictionary {
		d := &object.Dictionary{}
		d.Set("BaseFont", bf)
		return d
	}
	for _, bf := range yes {
		if !isSubsetFont(mk(bf)) {
			t.Errorf("%q should be a subset font", bf)
		}
	}
	for _, bf := range no {
		if isSubsetFont(mk(bf)) {
			t.Errorf("%q should not be a subset font", bf)
		}
	}
}
