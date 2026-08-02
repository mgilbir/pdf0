package pdfa

import (
	"testing"
)

// TestExampleFindingsPicksLowestObject covers the other half of the class: rules
// that report one example object number rather than one example name. The
// candidates reach exampleFindings in doc.Objects / collectContentStreamData map
// order, so the finding must keep the smallest object number regardless of the
// order in which candidates are offered.
func TestExampleFindingsPicksLowestObject(t *testing.T) {
	mk := func(order []int) []ValidationError {
		var f exampleFindings
		for _, n := range order {
			f.add(ValidationError{Rule: "6.1.8", Level: PDFA4, Message: "same message", Object: n})
		}
		return f.errs
	}
	for _, order := range [][]int{{7, 3, 9}, {3, 7, 9}, {9, 7, 3}} {
		got := mk(order)
		if len(got) != 1 {
			t.Fatalf("order %v: expected one deduplicated finding, got %d", order, len(got))
		}
		if got[0].Object != 3 {
			t.Errorf("order %v: expected the lowest object number 3, got %d", order, got[0].Object)
		}
	}
	// A different message is a different example and is kept separately.
	var f exampleFindings
	f.add(ValidationError{Rule: "6.1.8", Message: "a", Object: 5})
	f.add(ValidationError{Rule: "6.1.8", Message: "b", Object: 4})
	if len(f.errs) != 2 {
		t.Errorf("distinct messages must not be deduplicated: got %d findings", len(f.errs))
	}
}
