package pdf0

import (
	"testing"
)

// TestDocumentUnmodifiedCombinesVerdicts pins the C11 combined verdict.
func TestDocumentUnmodifiedCombinesVerdicts(t *testing.T) {
	cases := []struct {
		valid, covers, want bool
	}{
		{true, true, true},
		{true, false, false},
		{false, true, false},
		{false, false, false},
	}
	for _, c := range cases {
		got := SignatureResult{Valid: c.valid, CoversWholeDocument: c.covers}.DocumentUnmodified()
		if got != c.want {
			t.Errorf("DocumentUnmodified(Valid=%v,Covers=%v) = %v, want %v", c.valid, c.covers, got, c.want)
		}
	}
}
