package textorder

import (
	"strings"
	"testing"
)

// split is a line as one unit per character, in the order drawn.
func split(s string) []string {
	var units []string
	for _, r := range s {
		units = append(units, string(r))
	}
	return units
}

func reverse(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

func TestLineReadsDrawnTextInReadingOrder(t *testing.T) {
	for _, tc := range []struct{ name, drawn, want string }{
		{"latin", "ab cd", "ab cd"},
		{"a Hebrew word in a Latin line", "ab " + reverse("שלום") + " cd", "ab שלום cd"},
		{"a Hebrew line", reverse("שלום עולם"), "שלום עולם"},
		// A number in a right-to-left line keeps its own order.
		{"a number in a Hebrew line", "123 " + reverse("שלום"), "שלום 123"},
		// A Latin word in a mostly Hebrew line: the line is right to left.
		{"a Latin word in a Hebrew line", "abc " + reverse("שלום"), "שלום abc"},
		// Latin drawn backwards is not something reordering can see.
		{"overridden Latin", "cba", "cba"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Line(split(tc.drawn), nil); got != tc.want {
				t.Errorf("Line(%q) = %q; want %q", tc.drawn, got, tc.want)
			}
		})
	}
}

// TestAUnitMovesWhole: a glyph's text is one unit, not turned round inside
// itself, and a combining mark drawn after its base moves with the base.
func TestAUnitMovesWhole(t *testing.T) {
	// "بِسْمِ" drawn as HarfBuzz returns it: clusters right to left, each
	// cluster's base then its mark.
	drawn := []string{"م", "ِ", "س", "ْ", "ب", "ِ"}
	if got, want := Line(drawn, nil), "بِسْمِ"; got != want {
		t.Errorf("marks: got %q; want %q", got, want)
	}
	// A lam-alef ligature's glyph stands for "لا", in reading order already.
	drawn = []string{"م", "لا", "س"}
	if got, want := Line(drawn, nil), "سلام"; got != want {
		t.Errorf("ligature: got %q; want %q", got, want)
	}
}

// TestAFixedUnitIsNotLookedInto: an /ActualText is in reading order already,
// and is placed whole, taking the direction around it.
func TestAFixedUnitIsNotLookedInto(t *testing.T) {
	units := []string{"a", "b", " ", "שלום", " ", "c", "d"}
	fixed := []bool{false, false, false, true, false, false, false}
	if got, want := Line(units, fixed), "ab שלום cd"; got != want {
		t.Errorf("got %q; want %q", got, want)
	}
	// Only fixed right-to-left text: nothing to reorder.
	if Reorder([]string{"שלום"}, []bool{true}) != nil {
		t.Error("a line whose only right-to-left text is fixed was reordered")
	}
	if Reorder(split("plain text"), nil) != nil {
		t.Error("a left-to-right line was reordered")
	}
	if got := Line([]string{"x", "", "y"}, nil); !strings.EqualFold(got, "xy") {
		t.Errorf("an empty unit: %q", got)
	}
}
