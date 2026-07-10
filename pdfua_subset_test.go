package pdf0

import "testing"

func TestIsSubsetFont(t *testing.T) {
	yes := []Name{"ABCDEF+Arial", "SJYPRV+Georgia-BoldItalic", "LFTWBJ+Frutiger"}
	no := []Name{"Arial", "abcdef+Arial", "ABCDE+Arial", "ABCDEF-Arial", "AB2DEF+Arial", "+Arial", ""}
	mk := func(bf Name) *Dictionary {
		d := &Dictionary{}
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
